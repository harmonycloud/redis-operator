package redis

import (
	"context"
	"fmt"
	"time"

	"harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	myErrors "harmonycloud.cn/middleware/redis-cluster/util/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/klog"
)

const (
	redisRoleName         = "redis"
	redisDefaultPort      = 6379
	redisServiceName      = "redis"
	exporterPort          = 9121
	exporterPortName      = "http-metrics"
	exporterContainerName = "redis-exporter"
	redisName             = "r"
	timeToPrepareFist     = 30 * time.Second
	timeToPrepare         = 10 * time.Minute
)

func (rco *RedisClusterOperator) SyncReplication(redisCluster *v1alpha1.RedisCluster) error {
	own := []metav1.OwnerReference{
		*metav1.NewControllerRef(redisCluster, schema.FromAPIVersionAndKind("redis.middleware.hc.cn/v1alpha1", "RedisCluster")),
	}

	label := redisCluster.Spec.Pod[0].Labels
	// 创建service
	myErrors.EnsureNormal(rco.EnsureRedisService(redisCluster, label, own))

	// 创建statefulset
	myErrors.EnsureNormal(rco.EnsureRedisStatefulset(redisCluster, label, own))

	klog.V(2).Infof("check and healthy redis. namespace[%v] name[%s] ", redisCluster.Namespace, redisCluster.Name)
	// 检查redis状态
	myErrors.EnsureNormal(rco.CheckAndHeal(redisCluster))

	// 创建master的service
	myErrors.EnsureNormal(rco.EnsureMasterRedisService(redisCluster, label, own))

	klog.V(2).Infof("update redis statue. namespace[%v] name[%s] ", redisCluster.Namespace, redisCluster.Name)

	// 更新状态
	myErrors.EnsureNormalMsgf(
		rco.updaeReplicationStatue(redisCluster),
		"update redis statue failed. namespace[%v] name[%s]  %v",
		redisCluster.Namespace, redisCluster.Name)
	return nil
}

// EnsureRedisService makes sure the redis statefulset exists
func (r *RedisClusterOperator) EnsureRedisService(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) error {
	svc := generateRedisService(rc, labels, ownerRefs)
	return r.K8SService.CreateIfNotExistsService(rc.Namespace, svc)
}

// EnsureRedisService makes sure the redis statefulset exists
func (r *RedisClusterOperator) EnsureMasterRedisService(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) error {
	svc := generateMasterRedisService(rc, labels, ownerRefs)
	return r.K8SService.CreateIfNotExistsService(rc.Namespace, svc)
}

func (r *RedisClusterOperator) UpdateMasterRedisService(rc *RedisCluster) {
	masterSvc, _ := r.K8SService.GetService(rc.Namespace, GetRedisMasterName(rc))
	masterSvc.Spec.Selector["statefulset.kubernetes.io/pod-name"] = generateName(redisName, rc.Name) + "-x"
	r.K8SService.UpdateService(masterSvc.Namespace, masterSvc)
}

// EnsureRedisStatefulset makes sure the redis statefulset exists in the desired state
func (r *RedisClusterOperator) EnsureRedisStatefulset(rf *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) error {
	if err := r.ensurePodDisruptionBudget(rf, redisName, redisRoleName, labels, ownerRefs); err != nil {
		return err
	}
	// ss := generateRedisStatefulSet(rf, labels, ownerRefs)
	ss := r.buildRedisClusterStatefulset(rf.Namespace, rf.Name, rf)
	return r.K8SService.CreateOrUpdateStatefulSet(rf.Namespace, ss)
}

func generateRedisService(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.Service {
	name := GetRedisName(rc)
	namespace := rc.Namespace

	selectorLabels := generateSelectorLabels(rc.Name, name)
	// labels = util.MergeLabels(labels, selectorLabels)

	// defaultAnnotations := map[string]string{
	// 	"prometheus.io/scrape": "true",
	// 	"prometheus.io/port":   "http",
	// 	"prometheus.io/path":   "/metrics",
	// }
	labels = MergeLabels(labels, generateSelectorLabels(rc.Name, name))

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-readonly", name),
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
			// Annotations:     annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
			Ports: []corev1.ServicePort{
				{
					Port:     redisDefaultPort,
					Protocol: corev1.ProtocolTCP,
					Name:     redisServiceName,
				},
				{
					Port:     exporterPort,
					Protocol: corev1.ProtocolTCP,
					Name:     exporterContainerName,
				},
			},
			Selector: selectorLabels,
		},
	}
}

func generateMasterRedisService(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.Service {
	name := GetRedisMasterName(rc)
	namespace := rc.Namespace

	selectorLabels := generateSelectorLabels(rc.Name, name)
	// 默认没有执行任何一个pod
	selectorLabels["statefulset.kubernetes.io/pod-name"] = generateName(redisName, rc.Name) + "-x"
	labels = MergeLabels(labels, generateSelectorLabels(rc.Name, name))

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
			// Annotations:     annotations,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			// ClusterIP: corev1.ClusterIPNone,
			Ports: []corev1.ServicePort{
				{
					Port:     redisDefaultPort,
					Protocol: corev1.ProtocolTCP,
					Name:     redisServiceName,
				},
			},
			Selector: selectorLabels,
		},
	}
}

func GetRedisMasterName(rc *RedisCluster) string {
	return generateName(redisName, rc.Name)
}
func GetRedisName(rc *RedisCluster) string {
	return generateName(redisName, rc.Name)
}

func generateRedisStatefulSet(rf *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) *appsv1.StatefulSet {
	tempRedisCluster := rf.DeepCopy()
	name := GetRedisName(rf)
	namespace := rf.Namespace

	selectorLabels := generateSelectorLabels(rf.Name, name)
	labels = MergeLabels(labels, selectorLabels)
	podTpl := tempRedisCluster.Spec.Pod[0]
	pathPrefix := "/data"
	k8sClusterDomainName := "cluster.local"

	initContainerLimits := map[v1.ResourceName]resource.Quantity{
		//50m
		v1.ResourceCPU: resource.MustParse("50m"),
		//100Mi
		v1.ResourceMemory: resource.MustParse("100Mi"),
	}
	containerEnv := []v1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "metadata.name",
				},
			},
		},
		{
			Name:  "NAMESPACE",
			Value: namespace,
		},
		{
			Name: "PODIP",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		{
			Name:  "TZ",
			Value: "Asia/Shanghai",
		},
		{
			Name:  "REDIS_CLUSTER_NAME",
			Value: tempRedisCluster.Name,
		},
		{
			Name:  "REDIS_PASS",
			Value: tempRedisCluster.Spec.Pod[0].RequirePass,
		},
		{
			Name:  "REDIS_INSTANCE_PORT",
			Value: "6379",
		},
		{
			Name:  envDomainNameKey,
			Value: k8sClusterDomainName,
		},
		{
			Name:  envPathPrefixKey,
			Value: pathPrefix,
		},
	}
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    rf.Spec.Replicas,
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.OnDeleteStatefulSetStrategyType,
			},
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					// Annotations: rf.Spec.Redis.PodAnnotations,
				},
				Spec: corev1.PodSpec{
					// Affinity:          getAffinity(rf.Spec.Redis.Affinity, labels),
					// Tolerations:       rf.Spec.Redis.Tolerations,
					// NodeSelector:      rf.Spec.Redis.NodeSelector,
					// SecurityContext:   getSecurityContext(rf.Spec.Redis.SecurityContext),
					// HostNetwork:       rf.Spec.Redis.HostNetwork,
					// DNSPolicy:         getDnsPolicy(rf.Spec.Redis.DNSPolicy),
					// ImagePullSecrets:  rf.Spec.Redis.ImagePullSecrets,
					// PriorityClassName: rf.Spec.Redis.PriorityClassName,
					InitContainers: []v1.Container{
						{
							Command:         []string{"/bin/sh", "-c", "sh /init.sh"},
							Image:           rf.Spec.Repository + podTpl.InitImage,
							ImagePullPolicy: tempRedisCluster.Spec.Pod[0].ImagePullPolicy,
							Env:             containerEnv,
							Name:            "redis-init",
							Resources: v1.ResourceRequirements{
								Limits:   initContainerLimits,
								Requests: initContainerLimits,
							},
							Ports: []v1.ContainerPort{
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisServicePort16379,
								},
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisServicePort6379,
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "redis-data",
									MountPath: fmt.Sprintf("%v/redis/%v/%v/%v", pathPrefix, k8sClusterDomainName, namespace, name),
								},
								{
									Name:      "redis-config",
									MountPath: "/config/redis.conf",
									SubPath:   "redis.conf",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "redis",
							Image: fmt.Sprintf("%s%s", rf.Spec.Repository, rf.Spec.Pod[0].MiddlewareImage),
							// ImagePullPolicy: pullPolicy(rf.Spec.Pod.imagePullPolicy),
							Ports: []corev1.ContainerPort{
								{
									Name:          "redis",
									ContainerPort: 6379,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							// VolumeMounts: volumeMounts,
							// Command:      redisCommand,
							// ReadinessProbe: &corev1.Probe{
							// 	// InitialDelaySeconds: graceTime,
							// 	TimeoutSeconds: 5,
							// 	Handler: corev1.Handler{
							// 		Exec: &corev1.ExecAction{
							// 			Command: []string{"/bin/sh", "/redis-readiness/ready.sh"},
							// 		},
							// 	},
							// },
							// LivenessProbe: &corev1.Probe{
							// 	// InitialDelaySeconds: graceTime,
							// 	TimeoutSeconds: 5,
							// 	Handler: corev1.Handler{
							// 		Exec: &corev1.ExecAction{
							// 			Command: []string{
							// 				"sh",
							// 				"-c",
							// 				"redis-cli -h $(hostname) ping",
							// 			},
							// 		},
							// 	},
							// },
							// Resources: rf.Spec.Redis.Resources,
							// Lifecycle: &corev1.Lifecycle{
							// 	PreStop: &corev1.Handler{
							// 		Exec: &corev1.ExecAction{
							// 			Command: []string{"/bin/sh", "/redis-shutdown/shutdown.sh"},
							// 		},
							// 	},
							// },
						},
					},
					// Volumes: volumes,
				},
			},
		},
	}
	return ss
}

func (r *RedisClusterOperator) CheckAndHeal(redisCluster *RedisCluster) error {
	// 保证statefulset和cr数量相等
	err := r.rfChecker.CheckRedisNumber(redisCluster)
	if err != nil {
		panic(myErrors.NewErrMsg("Number of redis mismatch, this could be for a change on the statefulset"))
	}

	redisesIP, err := r.rfChecker.GetRedisesIPs(redisCluster)
	myErrors.EnsureNormal(err)
	// 首次必须保证，全部pod都是ready状态才行
	if redisCluster.Status.Phase == RedisClusterCreating || redisCluster.Status.Phase == "" {
		if len(redisesIP) != int(*redisCluster.Spec.Replicas) {
			panic(myErrors.NewErrMsg("wait for all redis pod ready"))
		}
	}

	nMasters, err := r.rfChecker.GetNumberMasters(redisCluster)
	myErrors.EnsureNormal(err)

	switch nMasters {
	case 0:
		// 只有1个节点，直接设置为master
		if len(redisesIP) == 1 {
			klog.V(1).Infof("find one redis node %s, set to master", redisesIP[0])
			if err := r.rfHealer.MakeMaster(redisesIP[0], redisCluster); err != nil {
				panic(myErrors.NewErr(err))
			}
			break
		}
		// 获取最小的pod启动时间
		minTime, err2 := r.rfChecker.GetMinimumRedisPodTime(redisCluster)
		if err2 != nil {
			panic(myErrors.NewErr(err2))
		}

		//等待准备时间,第一次需要30s，后面60s
		if (minTime > timeToPrepare) || (minTime > timeToPrepareFist && redisCluster.Status.Phase != RedisClusterRunning) {
			klog.V(1).Infof("time %.f more than expected. Not even one master, fixing..., namespace[%v] name[%s]", minTime.Round(time.Second).Seconds(), redisCluster.Namespace, redisCluster.Name)
			// 设置最旧的pod为master
			if err2 := r.rfHealer.SetOldestAsMaster(redisCluster); err2 != nil {
				panic(myErrors.NewErr(err2))
			}

		} else {
			klog.V(1).Infof("No master found, wait until failover, namespace[%v] name[%s]", redisCluster.Namespace, redisCluster.Name)
			panic(myErrors.New(myErrors.ERR_NO_MASTER, ""))
		}
	case 1:
		break
	default:
		panic(myErrors.New(myErrors.ERR_UNKNOW, "More than one master, fix manually"))
	}

	master, err := r.rfChecker.GetMasterIP(redisCluster)
	klog.V(2).Infof("find redis master, namespace[%v] name[%s]  ip[%s]", redisCluster.Namespace, redisCluster.Name, master)
	if err != nil {
		return err
	}

	// 检查是否所有slave都配置了该master
	if err2 := r.rfChecker.CheckAllSlavesFromMaster(master, redisCluster); err2 != nil {
		klog.V(1).Infof("redis not all slaves have set master, set all slave to master. namespace[%v] name[%s]  masterIp[%s]", redisCluster.Namespace, redisCluster.Name, master)

		// 如果没有，那么设置slave
		if err3 := r.rfHealer.SetMasterOnAll(master, redisCluster); err3 != nil {
			klog.Warningf("redis set all slave to master failed. namespace[%v] name[%s]  masterIp[%s]", redisCluster.Namespace, redisCluster.Name, master)
			return err3
		}
	}
	return nil
}

// 如果没有master，那么选主最旧的pod为master
// 如果有多个master，也选主最旧的pod为master，故障恢复的时候也适用
func (r *RedisClusterOperator) electMaster(rf *RedisCluster) error {
	minTime, err2 := r.rfChecker.GetMinimumRedisPodTime(rf)
	if err2 != nil {
		return err2
	}
	// 等待最新的pod有时间准备
	if minTime > timeToPrepare {
		klog.Infof("time %v more than expected. Not even one master, fixing...", minTime.Round(time.Second).Seconds())
		// 将最旧的pod设置为master
		if err2 := r.rfHealer.SetOldestAsMaster(rf); err2 != nil {
			return err2
		}
	} else {
		// We'll wait until failover is done
		klog.Warning("No master found, wait until failover")
		return nil
	}
	return nil
}

func (r *RedisClusterOperator) electMasterFirst(rf *RedisCluster) error {
	if err := r.rfHealer.SetOldestAsMaster(rf); err != nil {
		return err
	}
	return nil
}

func (r *RedisClusterOperator) updaeReplicationStatue(redisCluster *RedisCluster) error {
	err := fmt.Errorf("")

	if redisCluster.Status.Phase == "" {
		redisCluster.Status.Phase = RedisClusterCreating
	} else if redisCluster.Status.Phase == RedisClusterCreating || redisCluster.Status.Phase == RedisClusterRunning {
		redisCluster, err = r.chekcRedisRuning(redisCluster)
		if err != nil {
			return nil
		}
	}

	// 更新状态
	err = r.frameClient.Status().Update(context.TODO(), redisCluster)

	if err != nil {
		return err
	}
	return nil
}

func setRedisClusterCondition(pod corev1.Pod, t RedisClusterConditionType, port string) RedisClusterCondition {
	return RedisClusterCondition{
		Name:               pod.Name,
		HostIP:             pod.Status.HostIP,
		Status:             "true",
		Type:               t,
		LastTransitionTime: *&pod.Status.Conditions[1].LastTransitionTime,
		Instance:           fmt.Sprintf("%s:%s", pod.Status.PodIP, port),
	}
}

func (r *RedisClusterOperator) chekcRedisRuning(redisCluster *RedisCluster) (*RedisCluster, error) {
	// redisesIP, err := r.rfChecker.GetRedisesIPs(redisCluster)
	// if err != nil {
	// 	return redisCluster, err
	// }

	// if len(redisesIP) != int(*redisCluster.Spec.Replicas) {
	// 	return redisCluster, err
	// }

	// masterIPs, err := r.rfChecker.GetMasterIPs(redisCluster)
	// if len(masterIPs) != 1 {
	// 	return nil, fmt.Errorf("number of redis nodes known as master is different than 1")
	// }
	// 获取master
	// master := masterIPs[0]
	// 检查所有slave是否归属该master，如果是那么是正常状态
	// err = r.rfChecker.CheckAllSlavesFromMaster(master, redisCluster)
	// if err != nil {
	// 	return nil, err
	// }
	// 设置为runing状态
	redisCluster.Status.Phase = RedisClusterRunning
	redisCluster.Status.Replicas = *redisCluster.Spec.Replicas

	masterPod, err := r.rfChecker.GetRedisesMasterPod(redisCluster)
	if err != nil {
		return nil, err
	}
	slavePods, err := r.rfChecker.GetRedisesSlavesPods(redisCluster)
	if err != nil {
		return nil, err
	}
	redisClusterCondition := []RedisClusterCondition{}
	redisClusterCondition = append(redisClusterCondition, setRedisClusterCondition(*masterPod, "master", redisPort))
	for _, pod := range slavePods {
		redisClusterCondition = append(redisClusterCondition, setRedisClusterCondition(pod, "slave", redisPort))
	}
	redisCluster.Status.Conditions = redisClusterCondition
	return redisCluster, nil
}

// 保存状态
func (r *RedisClusterOperator) updateStatue(redisCluster *RedisCluster, status RedisClusterPhase, err error) error {
	if status != "" {
		redisCluster.Status.Phase = status
		r.frameClient.Status().Update(context.TODO(), redisCluster)
		return nil
	} else {
		return err
	}
}

func (r *RedisClusterOperator) UpdateRedisError(redisCluster *RedisCluster, code int, msg string) error {
	redisCluster.Status.Phase = RedisClusterError
	redisCluster.Status.Reason = fmt.Sprintf("%d-%s", code, msg)
	r.frameClient.Status().Update(context.TODO(), redisCluster)
	return nil
}
