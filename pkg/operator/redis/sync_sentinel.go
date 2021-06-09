package redis

import (
	"context"
	"fmt"

	"harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	myErrors "harmonycloud.cn/middleware/redis-cluster/util/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// exporterPort                  = 9121
	sentinelExporterPort = 9355
	// exporterPortName              = "http-metrics"
	// exporterContainerName         = "redis-exporter"
	sentinelExporterContainerName = "sentinel-exporter"
	exporterDefaultRequestCPU     = "25m"
	exporterDefaultLimitCPU       = "50m"
	exporterDefaultRequestMemory  = "50Mi"
	exporterDefaultLimitMemory    = "100Mi"
)

func (r *RedisClusterOperator) SyncSentinel(redisCluster *v1alpha1.RedisCluster) error {
	masterIPs := []string{}

	if redisCluster.Status.SentinelPhase == "" {
		redisCluster.Status.SentinelPhase = RedisClusterCreating
	}

	// 获取master ip
	masterIPs, err := r.rfChecker.GetMasterIPs(redisCluster)
	myErrors.EnsureNormalMsgf(err, "get master failed")
	if len(masterIPs) != 1 {
		myErrors.NewErrMsg("master num is not 1")
	}

	own := []metav1.OwnerReference{
		*metav1.NewControllerRef(redisCluster, schema.FromAPIVersionAndKind("redis.middleware.hc.cn/v1alpha1", "RedisCluster")),
	}

	myErrors.EnsureNormal(r.EnsureSentinelService(redisCluster, redisCluster.Labels, own))
	myErrors.EnsureNormal(r.EnsureSentinelConfigMap(redisCluster, redisCluster.Labels, own, "127.0.0.1", redisCluster.Spec.Pod[0].RequirePass))

	myErrors.EnsureNormal(r.EnsureSentinelDeployment(redisCluster, redisCluster.Labels, own))
	myErrors.EnsureNormal(r.CheckAndHealSentinel(redisCluster))
	klog.V(2).Infof("update redis sentinel statue. namespace[%v] name[%s] ", redisCluster.Namespace, redisCluster.Name)

	myErrors.EnsureNormal(r.updaeteSentinelStatue(redisCluster))

	return nil

}

// EnsureSentinelService makes sure the sentinel service exists
func (rco *RedisClusterOperator) EnsureSentinelService(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) error {
	svc := generateSentinelService(rc, labels, ownerRefs)
	return rco.K8SService.CreateOrUpdateService(rc.Namespace, svc)
}

func (rco *RedisClusterOperator) EnsureSentinelConfigMap(rf *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference, masterIP string, pass string) error {
	cm := generateSentinelConfigMap(rf, labels, ownerRefs, masterIP, pass)
	return rco.K8SService.CreateOrUpdateConfigMap(rf.Namespace, cm)
}

func (rco *RedisClusterOperator) EnsureSentinelDeployment(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) error {
	if err := rco.ensurePodDisruptionBudget(rc, sentinelName, sentinelRoleName, labels, ownerRefs); err != nil {
		return err
	}
	d := generateSentinelDeployment(rc, labels, ownerRefs)
	return rco.K8SService.CreateOrUpdateDeployment(rc.Namespace, d)
}

// EnsureRedisStatefulset makes sure the pdb exists in the desired state
func (rco *RedisClusterOperator) ensurePodDisruptionBudget(rf *RedisCluster, name string, component string, labels map[string]string, ownerRefs []metav1.OwnerReference) error {
	name = generateName(name, rf.Name)
	namespace := rf.Namespace

	minAvailable := intstr.FromInt(2)
	if *rf.Spec.Replicas <= 2 {
		minAvailable = intstr.FromInt(int(*rf.Spec.Replicas - 1))
	}

	labels = MergeLabels(labels, generateSelectorLabels(rf.Name, name))

	pdb := generatePodDisruptionBudget(name, namespace, labels, ownerRefs, minAvailable)

	return rco.K8SService.CreateOrUpdatePodDisruptionBudget(namespace, pdb)
}

func generateSentinelConfigMap(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference, masterIP string, password string) *corev1.ConfigMap {
	name := GetSentinelName(rc)
	namespace := rc.Namespace

	labels = MergeLabels(labels, generateSelectorLabels(rc.Name, name))
	sentinelConfigFileContent := `sentinel monitor mymaster %s 6379 2
sentinel down-after-milliseconds mymaster 1000
sentinel failover-timeout mymaster 3000
sentinel parallel-syncs mymaster 2
sentinel auth-pass mymaster %s`
	sentinelConfigFileContent = fmt.Sprintf(sentinelConfigFileContent, masterIP, password)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Data: map[string]string{
			sentinelConfigFileName: sentinelConfigFileContent,
		},
	}
}

func generateSentinelService(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) *v1.Service {
	name := GetSentinelName(rc)
	namespace := rc.Namespace

	sentinelTargetPort := intstr.FromInt(26379)
	selectorLabels := generateSelectorLabels(rc.Name, name)

	labels = MergeLabels(labels, generateSelectorLabels(rc.Name, name))

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
			// Annotations:     rf.Spec.Sentinel.ServiceAnnotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "sentinel",
					Port:       26379,
					TargetPort: sentinelTargetPort,
					Protocol:   "TCP",
				},
				{
					Name:       exporterContainerName,
					Port:       exporterPort,
					TargetPort: intstr.FromInt(exporterPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// GetSentinelName returns the name for sentinel resources
func GetSentinelName(rc *RedisCluster) string {
	return fmt.Sprintf("%s-%s", rc.Name, sentinelName)
}

func generateName(typeName, metaName string) string {
	return metaName
}

func generateSelectorLabels(mName string, name string) map[string]string {
	return map[string]string{
		"app":        mName,
		"middleware": "redis",
		"component":  name,
	}
}

// MergeLabels merges all the label maps received as argument into a single new label map.
func MergeLabels(allLabels ...map[string]string) map[string]string {
	res := map[string]string{}

	for _, labels := range allLabels {
		if labels != nil {
			for k, v := range labels {
				res[k] = v
			}
		}
	}
	return res
}

func generatePodDisruptionBudget(name string, namespace string, labels map[string]string, ownerRefs []metav1.OwnerReference, minAvailable intstr.IntOrString) *policyv1beta1.PodDisruptionBudget {
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: policyv1beta1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	}
}

func getSentinelCommand(rc *RedisCluster) []string {
	if len(rc.Spec.Sentinel.Command) > 0 {
		return rc.Spec.Sentinel.Command
	}
	return []string{
		"redis-server",
		fmt.Sprintf("/redis/%s", sentinelConfigFileName),
		"--sentinel",
	}
}

func pullPolicy(specPolicy corev1.PullPolicy) corev1.PullPolicy {
	if specPolicy == "" {
		return corev1.PullAlways
	}
	return specPolicy
}

func generateSentinelDeployment(rc *RedisCluster, labels map[string]string, ownerRefs []metav1.OwnerReference) *appsv1.Deployment {
	name := GetSentinelName(rc)
	configMapName := GetSentinelName(rc)
	namespace := rc.Namespace

	sentinelCommand := getSentinelCommand(rc)
	selectorLabels := generateSelectorLabels(rc.Name, name)
	labels = MergeLabels(labels, selectorLabels)

	hostNetwork := false
	dnsPolicy := v1.DNSClusterFirst

	sd := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: rc.Spec.Sentinel.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
					// Annotations: rc.Spec.Sentinel.PodAnnotations,
				},
				Spec: corev1.PodSpec{
					HostNetwork:   hostNetwork,
					DNSPolicy:     dnsPolicy,
					RestartPolicy: v1.RestartPolicyAlways,
					InitContainers: []corev1.Container{
						{
							Name:  "sentinel-config-copy",
							Image: fmt.Sprintf("%s%s", rc.Spec.Repository, rc.Spec.Pod[0].InitImage),
							// ImagePullPolicy: pullPolicy(rf.Spec.Sentinel.ImagePullPolicy),
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "sentinel-config",
									MountPath: "/redis",
								},
								{
									Name:      "sentinel-config-writable",
									MountPath: "/redis-writable",
								},
							},
							Command: []string{
								"cp",
								fmt.Sprintf("/redis/%s", sentinelConfigFileName),
								fmt.Sprintf("/redis-writable/%s", sentinelConfigFileName),
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("32Mi"),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "sentinel",
							Image:           fmt.Sprintf("%s%s", rc.Spec.Repository, rc.Spec.Pod[0].MiddlewareImage),
							ImagePullPolicy: pullPolicy(rc.Spec.Pod[0].ImagePullPolicy),
							Ports: []corev1.ContainerPort{
								{
									Name:          "sentinel",
									ContainerPort: 26379,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "sentinel-config-writable",
									MountPath: "/redis",
								},
							},
							Command: sentinelCommand,
							ReadinessProbe: &corev1.Probe{
								// InitialDelaySeconds: graceTime,
								TimeoutSeconds: 5,
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh",
											"-c",
											"redis-cli -h $(hostname) -p 26379 ping",
										},
									},
								},
							},
							LivenessProbe: &corev1.Probe{
								// InitialDelaySeconds: graceTime,
								TimeoutSeconds: 5,
								Handler: corev1.Handler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"sh",
											"-c",
											"redis-cli -h $(hostname) -p 26379 ping",
										},
									},
								},
							},
							Resources: rc.Spec.Sentinel.Resources,
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "sentinel-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
						},
						{
							Name: "sentinel-config-writable",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
	// if rc.Spec.Sentinel.Exporter.Enabled {
	exporter := createExporterContainer(rc, sd.Name, true)
	sd.Spec.Template.Spec.Containers = append(sd.Spec.Template.Spec.Containers, exporter)
	// }
	return sd
}

func (r *RedisClusterOperator) updaeteSentinelStatue(redisCluster *RedisCluster) error {
	err := fmt.Errorf("")
	redisCluster, err = r.checkRedisSentinelRuning(redisCluster)

	err = r.frameClient.Status().Update(context.TODO(),redisCluster)
	myErrors.EnsureNormal(err)
	return nil
}

func (r *RedisClusterOperator) checkRedisSentinelRuning(redisCluster *RedisCluster) (*RedisCluster, error) {
	// 获取所有的哨兵pod
	sPod, err := r.rfChecker.GetRedisesSentinelPod(redisCluster)
	if err != nil {
		return redisCluster, nil
	}
	sentinelConditions := []RedisClusterCondition{}
	i := 0
	for _, pod := range sPod {
		sentinelConditions = append(sentinelConditions, setRedisClusterCondition(pod, "sentinel", sentinelPort))
		i++
	}
	redisCluster.Status.SentinelReplicas = int32(i)
	redisCluster.Status.SentinelPhase = RedisClusterRunning
	redisCluster.Status.SentinelConditions = sentinelConditions
	return redisCluster, nil
}

func (r *RedisClusterOperator) CheckAndHealSentinel(redisCluster *RedisCluster) error {
	myErrors.EnsureNormalMsgf(
		r.rfChecker.CheckSentinelNumber(redisCluster),
		"Number of sentinel mismatch, this could be for a change on the deployment")

	// 获取哨兵
	sentinels, err := r.rfChecker.GetSentinelsIPs(redisCluster)

	if len(sentinels) < int(*redisCluster.Spec.Sentinel.Replicas)/2 {
		panic(myErrors.NewErrMsg("alived sentinels num is small replicas/2"))
	}
	myErrors.EnsureNormal(err)

	// 检查master, 保证哨兵和主从master相同
	err, master := r.CheckMaster(redisCluster, sentinels)
	myErrors.EnsureNormal(err)

	myErrors.EnsureNormal(r.checkAndHealSentinels(redisCluster, sentinels))

	// 设置master的service
	return r.MakeSvcToMaster(redisCluster, master)
}

func (r *RedisClusterOperator) CheckMaster(redisCluster *RedisCluster, sentinels []string) (error, string) {
	// 实际的master
	masterIPs, err := r.rfChecker.GetMasterIPs(redisCluster)
	if err != nil {
		panic(err)
	}
	if len(masterIPs) == 0 {
		panic("not find master")
	}
	if len(masterIPs) > 1 {
		panic("find redis master >1")
	}

	master := masterIPs[0]

	for _, sip := range sentinels {
		if actualMonitorIP, err := r.rfChecker.CheckSentinelMonitor(sip, master); err != nil {
			if actualMonitorIP == "" {
				return err, master
			} else if actualMonitorIP == "127.0.0.1" {
				// 表示哨兵刚启动， 设置哨兵监听当前master
				klog.V(1).Infof("sentinel is not monitoring the correct master,correcting. namespace[%v] name[%s] sentinelIP[%s] master[%s] %v", redisCluster.Namespace, redisCluster.Name, sip, master, err)
				if err := r.rfHealer.NewSentinelMonitor(sip, master, redisCluster); err != nil {
					klog.Warningf("sentinel correct master failed. namespace[%v] name[%s] sentinelIP[%s] master[%s] %v", redisCluster.Namespace, redisCluster.Name, sip, master, err)
					return err, ""
				}
			} else {
				// // 运行过程中如果与哨兵不一致
				// err := r.rfHealer.SetMasterOnAll(actualMonitorIP, redisCluster)
				// if err != nil {
				// 	return err, master
				// }
			}
		}
	}

	return nil, master
}
func (r *RedisClusterOperator) CheckMonitorList(redisCluster *RedisCluster, sentinels []string) error {
	// r.rfChecker.redisClient.GetSentinelsInMemory()
	return nil
}
func (r *RedisClusterOperator) MakeSvcToMaster(redisCluster *RedisCluster, master string) error {
	masterSvcName := GetRedisMasterName(redisCluster)
	service, err := r.K8SService.GetService(redisCluster.Namespace, masterSvcName)
	if err != nil {
		return err
	}
	serviceD := service.DeepCopy()
	pod, err := r.rfChecker.GetPodFromIP(redisCluster, master)
	if err != nil {
		return err
	}
	if serviceD.Spec.Selector["statefulset.kubernetes.io/pod-name"] != pod.Name {
		serviceD.Spec.Selector["statefulset.kubernetes.io/pod-name"] = pod.Name
		klog.V(1).Infof("Make redis master servce to pod[%s] ip[%s],  namespace[%v] name[%s]  ", pod.Name, master, redisCluster.Namespace, redisCluster.Name)

		err = r.K8SService.UpdateService(serviceD.Namespace, serviceD)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *RedisClusterOperator) checkAndHealSentinels(rf *RedisCluster, sentinels []string) error {
	for _, sip := range sentinels {
		if err := r.rfChecker.CheckSentinelNumberInMemory(sip, rf); err != nil {
			klog.V(1).Infof("Sentinel has more sentinel in memory than spected. namespace[%v] name[%s] sentinelIP[%s]%v", rf.Namespace, rf.Name, sip, err)
			if err := r.rfHealer.RestoreSentinel(sip); err != nil {
				return err
			}
		}
	}
	for _, sip := range sentinels {
		if err := r.rfChecker.CheckSentinelSlavesNumberInMemory(sip, rf); err != nil {
			klog.V(1).Infof("Sentinel has more slaves in memory than spected. namespace[%v] name[%s] sentinelIP[%s]%v", rf.Namespace, rf.Name, sip, err)
			if err := r.rfHealer.RestoreSentinel(sip); err != nil {
				return err
			}
		}
	}
	for _, sip := range sentinels {
		if err := r.rfHealer.SetSentinelCustomConfig(sip, rf); err != nil {
			return err
		}
	}
	return nil
}

func createExporterContainer(rf *RedisCluster, host string, isSentinel bool) corev1.Container {
	args := []string{}
	if !isSentinel {
		args = []string{"-redis.addr", "redis://$(PODIP):6379", "-redis.password", rf.Spec.Pod[0].RequirePass,
			"-redis-only-metrics=true"}
	} else {
		args = []string{"-redis.addr", "redis://$(PODIP):26379", "-redis-only-metrics=true"}
	}

	containerEnv := []v1.EnvVar{
		{
			Name: "PODIP",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
	}
	container := corev1.Container{
		Args:            args,
		Name:            "redis-exporter",
		Image:           fmt.Sprintf("%s%s", rf.Spec.Repository, rf.Spec.Pod[0].MonitorImage),
		ImagePullPolicy: pullPolicy(rf.Spec.Pod[0].ImagePullPolicy),
		Ports: []corev1.ContainerPort{
			{
				Name:          "metrics",
				ContainerPort: exporterPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Env: containerEnv,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(exporterDefaultLimitCPU),
				corev1.ResourceMemory: resource.MustParse(exporterDefaultLimitMemory),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(exporterDefaultRequestCPU),
				corev1.ResourceMemory: resource.MustParse(exporterDefaultRequestMemory),
			},
		},
	}
	return container
}
