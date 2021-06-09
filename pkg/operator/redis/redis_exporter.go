package redis

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

// 确保只有在创建集群,集群横向扩容,集群修改密码时调用该方法,否则可能修改了podTemplate导致pod重启
// 构造RedisExporter Statefulset
//namespace：Statefulset所在ns
//name：Statefulset name
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) buildRedisExporterStatefulset(redisCluster *RedisCluster) (*appsv1.StatefulSet, error) {

	namespace := redisCluster.Namespace
	name := redisCluster.Name
	exporterName := name + "-exporter"
	var oldRedisAddrs string

	oldSet, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Get(context.TODO(),exporterName,metav1.GetOptions{})
	if err != nil {
		//第一次创建
		if !errors.IsNotFound(err) {
			return nil, err
		} else {
			oldRedisAddrs = ""
		}
	} else {
		command := oldSet.Spec.Template.Spec.Containers[0].Command
		//获取旧地址
		oldRedisAddrs = strings.Split(command[1], "=")[1]
	}

	tempRedisCluster := redisCluster.DeepCopy()

	//create statefulset
	revisionHistoryLimit := int32(10)
	replicas := int32(1)
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
	}

	podTpl := tempRedisCluster.Spec.Pod[0]

	containerEnv = append(containerEnv, podTpl.Env...)
	podLabels := podTpl.Labels
	if podLabels == nil {
		podLabels = make(map[string]string, 1)
	}
	podLabels["app"] = exporterName

	//拷贝map
	stsLabel := make(map[string]string)

	annos := podTpl.Annotations
	if annos == nil {
		annos = make(map[string]string)
	}

	annos[MiddlewareRedisTypeKey] = MiddlewareRedisClustersPrefix + name
	//annos[pauseKey] = strconv.FormatBool(tempRedisCluster.Spec.Pause)
	//exporter不需要固定节点;只需要固定IP
	delete(annos, middlewarePodFixedNodeKey)

	flagTrue := true

	var stsUpdateStrategy appsv1.StatefulSetUpdateStrategy

	stsUpdateStrategy.Type = appsv1.RollingUpdateStatefulSetStrategyType

	exporterContainerLimits := map[v1.ResourceName]resource.Quantity{
		//100m
		v1.ResourceCPU: resource.MustParse("100m"),
		//200Mi
		v1.ResourceMemory: resource.MustParse("200Mi"),
	}

	//获取密码
	password, isExistPassword := rco.loadRedisClusterPasswordByKey(fmt.Sprintf("%v/%v", namespace, name))
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", redisCluster.Namespace, redisCluster.Name)
		password = ""
	}

	label := labels.SelectorFromSet(labels.Set(map[string]string{"app": name}))
	options := metav1.ListOptions{
		LabelSelector: label.String(),
	}
	//获取所有实例地址
	podList, err := rco.defaultClient.CoreV1().Pods(namespace).List(context.TODO(),options)
	if err != nil {
		return nil, err
	}

	var podAddrs []string
	for _, pod := range podList.Items {
		podAddrs = append(podAddrs, fmt.Sprintf("%v:%v", pod.Status.PodIP, redisServicePort6379))
	}
	//,连接
	redisAddrs := strings.Join(podAddrs, ",")

	var newRedisExporterCmd []string
	//旧地址长度如果大于新地址长度则使用旧地址(如果是pod重启之类场景旧地址是不能被更新丢掉的)
	if oldRedisAddrs != "" && len(oldRedisAddrs) > len(redisAddrs) {
		newRedisExporterCmd = []string{"/redis_exporter", fmt.Sprintf("-redis.addr=%v", oldRedisAddrs), fmt.Sprintf("-redis.password=%v", password), "-web.listen-address=:19105", "-redis-only-metrics=true", fmt.Sprintf("-redis.alias=%v", redisContainerName)}
	} else {
		newRedisExporterCmd = []string{"/redis_exporter", fmt.Sprintf("-redis.addr=%v", redisAddrs), fmt.Sprintf("-redis.password=%v", password), "-web.listen-address=:19105", "-redis-only-metrics=true", fmt.Sprintf("-redis.alias=%v", redisContainerName)}
	}

	recSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      exporterName,
			Namespace: namespace,
			//维修状态
			Annotations: annos,
			Labels:      stsLabel,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         controllerKind.Group + "/" + controllerKind.Version,
					Kind:               controllerKind.Kind,
					Name:               tempRedisCluster.Name,
					UID:                tempRedisCluster.UID,
					BlockOwnerDeletion: &flagTrue,
					Controller:         &flagTrue,
				},
			},
		},
		Spec: appsv1.StatefulSetSpec{
			UpdateStrategy:       stsUpdateStrategy,
			Replicas:             &replicas,
			RevisionHistoryLimit: &revisionHistoryLimit,
			//StatefulSetSpec下只能更新UpdateStrategy、Template、Replicas,其他都禁止更新
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": exporterName,
				},
			},
			//serviceName和redisCluster一样
			ServiceName: exporterName,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: annos,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Command:         newRedisExporterCmd,
							Image:           tempRedisCluster.Spec.Repository + podTpl.MonitorImage,
							ImagePullPolicy: tempRedisCluster.Spec.Pod[0].ImagePullPolicy,
							Env:             containerEnv,
							Name:            "redis-exporter",
							Resources: v1.ResourceRequirements{
								Limits:   exporterContainerLimits,
								Requests: exporterContainerLimits,
							},
							Ports: []v1.ContainerPort{
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisExporterPort19105,
								},
							},
						},
					},
					DNSPolicy:     v1.DNSClusterFirst,
					RestartPolicy: v1.RestartPolicyAlways,
					Affinity:      podTpl.Affinity,
				},
			},
		},
	}
	return recSts, nil
}

//构造RedisExporter Service
//namespace：service所在ns
//name：service name
func (rco *RedisClusterOperator) buildRedisExporterService(redisCluster *RedisCluster) *v1.Service {

	tempRedisCluster := redisCluster.DeepCopy()
	name := tempRedisCluster.Name
	exporterName := name + "-exporter"
	namespace := redisCluster.Namespace

	podTpl := tempRedisCluster.Spec.Pod[0]

	podLabels := podTpl.Labels
	if podLabels == nil {
		podLabels = make(map[string]string, 1)
	}
	podLabels["app"] = exporterName

	flagTrue := true
	//build service
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      exporterName,
			Namespace: namespace,
			Annotations: map[string]string{
				MiddlewareRedisTypeKey: MiddlewareRedisClustersPrefix + name,
			},
			Labels: podLabels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         controllerKind.Group + "/" + controllerKind.Version,
					Kind:               controllerKind.Kind,
					Name:               name,
					UID:                tempRedisCluster.UID,
					BlockOwnerDeletion: &flagTrue,
					Controller:         &flagTrue,
				},
			},
		},
		Spec: v1.ServiceSpec{
			ClusterIP: v1.ClusterIPNone,
			Ports: []v1.ServicePort{
				{
					Name: "redis-exporter",
					Port: redisExporterPort19105,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: int32(redisExporterPort19105),
					},
				},
			},

			Selector: podLabels,
		},
	}
}

//有则更新,无则创建,抛弃不用了
func (rco *RedisClusterOperator) handleRedisExporterStsAndSvc(redisCluster *RedisCluster) error {

	return nil
	exporterName := redisCluster.Name + "-exporter"
	namespace := redisCluster.Namespace

	_, err := rco.defaultClient.CoreV1().Services(namespace).Get(context.TODO(),exporterName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("get %v/%v exporter svc error: %v", namespace, exporterName, err)
			return err
		} else {
			service := rco.buildRedisExporterService(redisCluster)
			_, err = rco.defaultClient.CoreV1().Services(namespace).Create(context.TODO(),service,metav1.CreateOptions{})
			if err != nil {
				klog.Errorf("create %v/%v exporter svc error: %v", namespace, exporterName, err)
				return err
			}
		}
	}

	newExporterSts, err := rco.buildRedisExporterStatefulset(redisCluster)
	if err != nil {
		klog.Errorf("build %v/%v exporter sts error: %v", namespace, exporterName, err)
		return err
	}

	//创建exporter service
	_, err = rco.defaultClient.AppsV1().StatefulSets(namespace).Get(context.TODO(),exporterName,metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			klog.Errorf("get %v/%v exporter sts error: %v", namespace, exporterName, err)
			return err
		} else {
			//创建exporter sts
			_, err = rco.defaultClient.AppsV1().StatefulSets(namespace).Create(context.TODO(),newExporterSts,metav1.CreateOptions{})
		}
	} else {
		//更新exporter sts
		_, err = rco.defaultClient.AppsV1().StatefulSets(namespace).Update(context.TODO(),newExporterSts,metav1.UpdateOptions{})
	}

	if err != nil {
		klog.Errorf("create or update %v/%v exporter sts error: %v", namespace, exporterName, err)
		return err
	}

	//等待exporter pod ready
	_, err = rco.checkExporterPodInstanceIsReadyByEndpoint(namespace, redisCluster.Name, 1)
	if err != nil && err == stopWaitError {
		klog.Warningf("stop wait all pod ready, will start drop redisCluster:%v/%v", namespace, redisCluster.Name)
		return nil
	}

	return nil
}

//这里将阻塞-->>检查实例是否ready
func (rco *RedisClusterOperator) checkExporterPodInstanceIsReadyByEndpoint(namespace, name string, replicas int32) (*v1.Endpoints, error) {

	exporterName := name + "-exporter"

	var endpoints *v1.Endpoints
	var err error
	f := wait.ConditionFunc(func() (bool, error) {
		//有删除标识停止等待
		value, isExist := redisClusterStopWait.Load(fmt.Sprintf("%v/%v", namespace, name))
		if isExist {
			if value.(bool) {
				return false, stopWaitError
			}
		}

		endpoints, err = rco.defaultClient.CoreV1().Endpoints(namespace).Get(context.TODO(),exporterName, metav1.GetOptions{})

		if err != nil {
			return false, fmt.Errorf("get redis exporter endpoint is error: %v", err)
		}

		if endpoints.Subsets == nil {
			sts, err :=  rco.defaultClient.AppsV1().StatefulSets(namespace).Get(context.TODO(),exporterName,metav1.GetOptions{})

			if err != nil {
				return false, fmt.Errorf("get redis exporter StatefulSets: %v/%v is error: %v", namespace, exporterName, err)
			}
			if sts == nil || *sts.Spec.Replicas == 0 {
				return false, fmt.Errorf("redis exporter StatefulSets: %v/%v is not exist or Spec.Replicas is 0", namespace, exporterName)
			}
			return false, nil
		}

		if len(endpoints.Subsets) == 0 {
			return false, nil
		}

		if int32(len(endpoints.Subsets[0].Addresses)) == replicas {
			return true, nil
		}
		return false, nil
	})

	timeout := rco.options.ClusterTimeOut
	err = wait.Poll(2*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		klog.Errorf("create or update exporter is error: %v", err)
		if err == wait.ErrWaitTimeout {
			podStatusMessages, err1 := rco.getPodStatus(namespace, name)
			if err1 != nil {
				return endpoints, err
			}
			return endpoints, fmt.Errorf("%v, %v", err, podStatusMessages)
		}
		return endpoints, err
	}
	return endpoints, nil
}
