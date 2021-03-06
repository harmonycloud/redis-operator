/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package RedisCluster contains all the logic for handling Kubernetes RedisClusters.
// It implements a set of strategies (rolling, recreate) for deploying an application,
// the means to rollback to previous versions, proportional scaling for mitigating
// risk, cleanup policy, and other useful features of RedisClusters.

package redis

import (
	"context"
	"fmt"
	"harmonycloud.cn/middleware/redis-cluster/pkg/options"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"strings"
	"sync"
	//"time"
	//"harmonycloud.cn/middleware/redis-cluster/cmd/operator-manager/app/options"
	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/pkg/k8s"
	"harmonycloud.cn/middleware/redis-cluster/pkg/record"
	"harmonycloud.cn/middleware/redis-cluster/util"
	rcserrors "harmonycloud.cn/middleware/redis-cluster/util/errors"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	apps "k8s.io/api/apps/v1"
	fclient "sigs.k8s.io/controller-runtime/pkg/client"

	//"k8s.io/kubernetes/pkg/controller"
	//labelsutil "k8s.io/kubernetes/pkg/util/labels"
	//labelsutil "k8s.io/kubernetes/pkg/util/labels"
	//labelsutil "k8s.io/kubernetes/pkg/util/labels"
)

const (
	// maxRetries is the number of times a RedisCluster will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a RedisCluster is going to be requeued:
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries             = 15
	redisServicePort6379   = 6379
	redisExporterPort19105 = 19105
	redisServicePort16379  = 16379
	pauseKey               = "pause.middleware.harmonycloud.cn"
	defaultPipeline        = "10"
	finalizersForeGround   = "Foreground"
	redisContainerName     = "redis-cluster"
	redisTribClientRbPath  = "/var/lib/gems/1.9.1/gems/redis-3.3.3/lib/redis/client.rb"

	redisAnnoRollbackUpdateIdKey = "rollbackUpdateId"
)

//var replcaeRedisTribClientRbPwdCmd = fmt.Sprintf(`sed -i "s/:password =>.*$/:password => \"%v\",/g" %v`, "", redisTribClientRbPath)

type handleClusterType int

const (
	createCluster handleClusterType = iota
	upgradeCluster
	dropCluster
	rollbackUpdateCluster
	nothingCluster
)

var (
	//redis????????????map
	redisClusterRequirePassMap = &sync.Map{}
	//????????????????????????pod??????ready???????????????
	redisClusterSyncStartTimeMap = &sync.Map{}
	//redis????????????????????????,????????????????????????
	redisClusterStopWait = &sync.Map{}
)

// controllerKind contains the schema.GroupVersionKind for this controller type.
var controllerKind = SchemeGroupVersion.WithKind("RedisCluster")

// RedisClusterOperator is responsible for synchronizing RedisCluster objects stored
// in the system with actual running replica sets and pods.
type RedisClusterOperator struct {
	//extensionCRClient *extensionsclient.Clientset

	client client
	defaultClient   clientset.Interface
	frameClient fclient.Client
	kubeConfig      *rest.Config

	K8SService k8s.Services
	// ?????????????????????
	rfHealer *RedisFailoverHealer
	// ?????????????????????
	rfChecker *RedisFailoverChecker

	//options *options.OperatorManagerServer

	eventRecorder record.EventRecorder

	options *options.OperatorManagerServer
}

// NewRedisClusterOperator creates a new RedisClusterOperator.
func NewRedisClusterOperator(
	kubeClient clientset.Interface,
	frameClient fclient.Client,
	kubeConfig *rest.Config,
	options options.OperatorManagerServer) (*RedisClusterOperator, error) {

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(kubeClient.CoreV1().RESTClient()).Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "redis-operator"})
	rco := &RedisClusterOperator{
		frameClient: frameClient,
		K8SService:    k8s.New(kubeClient),
		rfHealer:      NewRedisFailoverHealer(k8s.New(kubeClient), NewClient()),
		rfChecker:     NewRedisFailoverChecker(k8s.New(kubeClient), NewClient()),
		kubeConfig:    kubeConfig,
		defaultClient: kubeClient,
		options:       &options,
		eventRecorder:   recorder,

	}
	return rco, nil
}

// getStatefulSetForRedisCluster It returns the StatefulSets that this RedisCluster should manage.
func (rco *RedisClusterOperator) getStatefulSetForRedisCluster(rc *RedisCluster) (*appsv1.StatefulSet, error) {
	// List all StatefulSets to find those we own but that no longer match our annotation.
	stsList, err := rco.defaultClient.AppsV1().StatefulSets(rc.Namespace).List(context.TODO(),metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, sts := range stsList.Items {
		// If a statefulSet with a redis.middleware.hc.cn=redisclusters-{redisClusterName} annotation, it should be redis work queue sync
		if len(sts.Annotations) == 0 || sts.Annotations[MiddlewareRedisTypeKey] != (MiddlewareRedisClustersPrefix+rc.Name) {
			continue
		}

		if sts.Namespace == rc.Namespace && sts.Name == rc.Name {
			return &sts, nil
		}
	}

	klog.V(4).Infof("could not find statefulSet for RedisCluster %s in namespace %s with annotation: %v", rc.Name, rc.Namespace, MiddlewareRedisTypeKey)

	return nil, nil
}

func (rco *RedisClusterOperator) Sync(redisCluster *RedisCluster) (err error) {
	var isHorizontalScale, isVerticalScale bool

	klog.V(4).Infof("Started syncing redisCluster: %v/%v ResourceVersion: %v",
		redisCluster.Namespace, redisCluster.Name, redisCluster.ResourceVersion)

	if redisCluster.DeletionTimestamp != nil {
		klog.V(4).Infof("redisCluster: %v/%v deleting", redisCluster.Namespace, redisCluster.Name)
		return nil
	}

	// Construct histories of the RedisCluster, and get the hash of current history
	cur, old, err := rco.constructHistory(redisCluster)
	if err != nil {
		return fmt.Errorf("failed to construct revisions of RedisCluster: %v", err)
	}

	//cleanup History RedisCluster controller revision
	defer func() {
		if err != nil {
			return
		}
		err = rco.cleanupHistory(redisCluster, old)
		if err != nil {
			klog.Errorf("failed to clean up revisions of RedisCluster: %v", err)
		}
	}()

	//????????????
	err = rco.storeRedisClusterPassword(redisCluster)
	if err != nil {
		return err
	}

	var handleFlag handleClusterType
	existSts, err := rco.getStatefulSetForRedisCluster(redisCluster)
	if err != nil {
		klog.Errorf("get cluster statefulset: %v/%v is error: %v", redisCluster.Namespace, redisCluster.Name, err)
		return err
	} else if strings.EqualFold(redisCluster.Spec.Finalizers, finalizersForeGround) && redisCluster.DeletionTimestamp == nil {
		handleFlag = dropCluster
		klog.Infof("RedisCluster %v/%v has been drop", redisCluster.Namespace, redisCluster.Name)
		rco.eventRecorder.LabeledEventf(redisCluster, record.DeletingEvent, v1.EventTypeNormal, "DeleteClusterStart", "deleting:startDeleting:Cluster %v is deleting", redisCluster.Name)
	} else if redisCluster.DeletionTimestamp == nil && (existSts == nil || *existSts.Spec.Replicas == 0) {
		klog.Infof("RedisCluster %v/%v has been create firstly, will create and init redis cluster", redisCluster.Namespace, redisCluster.Name)
		rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "CreateClusterStart", "creating:startCreating:Cluster %v is creating start", redisCluster.Name)
		handleFlag = createCluster
	} else if redisCluster.Annotations[redisAnnoRollbackUpdateIdKey] != "" {
		handleFlag = rollbackUpdateCluster
	} else if redisCluster.DeletionTimestamp == nil {
		//??????????????????????????????
		if cur.Labels[util.OperatorRevisionHashLabelKey] == existSts.Labels[util.OperatorRevisionHashLabelKey] {
			handleFlag = nothingCluster
		}else {
			// ????????????????????????????????????
			isHorizontalScale,isVerticalScale,err = rco.getUpgradeType(redisCluster,cur,old)
			if err != nil{
				return err
			}
			klog.Infof("RedisCluster %v/%v has been upgrade", redisCluster.Namespace, redisCluster.Name)
			rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "upgradeCluster", "upgrading:Cluster %v is upgrading", redisCluster.Name)
			handleFlag = upgradeCluster
		}
	}

	if handleFlag != nothingCluster{
		//?????????????????????????????????
		redisClusterSyncStartTimeMap.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
	}

	switch handleFlag {
	// ???????????????
	case createCluster:
		return rco.createClusterFirst(redisCluster,cur.Labels[util.OperatorRevisionHashLabelKey],existSts)
	// ????????????
	case upgradeCluster:
		return rco.upgradeCluster(redisCluster,isHorizontalScale,isVerticalScale,
			cur.Labels[util.OperatorRevisionHashLabelKey],*existSts.Spec.Replicas)
	// ??????????????????????????????
	case nothingCluster:
		return rco.checkAndUpdateRedisClusterStatus(redisCluster)
	// ????????????
	case rollbackUpdateCluster:
		return rco.makeRollBackup(redisCluster,cur,old)
	// ????????????
	case dropCluster:
		//?????????????????????????????????
		redisClusterRequirePassMap.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
		redisClusterSyncStartTimeMap.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
		redisClusterStopWait.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
		//???????????????Deleting
		newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, rcserrors.NewRedisClusterState(redisCluster.Status.Reason, RedisClusterDeleting, redisCluster.Status.ReasonType), false)
		if err != nil {
			return err
		}
		err = rco.dropRedisCluster(newRedisCluster)
		return err
	default:
		klog.Error("RedisCluster crd Error.")
	}
	return err
}

func (rco *RedisClusterOperator) storeRedisClusterPassword(redisCluster *RedisCluster) error {
	tempredisCluster := redisCluster.DeepCopy()
	namespace := tempredisCluster.Namespace
	name := tempredisCluster.Name
	password := tempredisCluster.Spec.Pod[0].RequirePass
	if password == "" {
		return nil
	}

	//???????????????
	oldPwd, isExist := rco.loadRedisClusterPasswordByKey(fmt.Sprintf("%v/%v", namespace, name))
	//????????????
	if !isExist || oldPwd != password {
		redisClusterRequirePassMap.Store(fmt.Sprintf("%v/%v", namespace, name), password)
		//??????exporter sts
		err := rco.handleRedisExporterStsAndSvc(redisCluster)
		if err != nil {
			//TODO ?????????
			return err
		}
	}

	return nil
}

func (rco *RedisClusterOperator) loadRedisClusterPassword(namespace, name string) (string, bool) {
	podKey := fmt.Sprintf("%v/%v", namespace, name)
	key := podKey[0:strings.LastIndex(podKey, "-")]

	pwd, isExist := redisClusterRequirePassMap.Load(key)
	if pwd == nil || pwd == "" || !isExist {
		return `""`, isExist
	}

	return pwd.(string), isExist
}

func (rco *RedisClusterOperator) loadRedisClusterPasswordByKey(key string) (string, bool) {
	pwd, isExist := redisClusterRequirePassMap.Load(key)
	if pwd == nil || pwd == "" || !isExist {
		return `""`, isExist
	}

	return pwd.(string), isExist
}

func (rco *RedisClusterOperator) getUpgradeType(redisCluster *RedisCluster,cur *apps.ControllerRevision,  old []*apps.ControllerRevision) (bool, bool,error) {
	var curRedisClusterSpec, oldRedisClusterSpec *RedisClusterSpec

	//?????????old???????????????
	if len(old) == 0 {
		err := fmt.Errorf("RedisCluster: %v/%v history controller revision is empty", cur.Namespace, cur.Annotations[MiddlewareRedisClusterNameKey])
		klog.Error(err)
		return false,false,err
	}

	curRedisClusterSpec, oldRedisClusterSpec, err := rco.convertControllerRevision(redisCluster, cur, old)
	if err != nil {
		return false,false,err
	}
	if oldRedisClusterSpec == nil {
		err = fmt.Errorf("RedisCluster: %v/%v history controller revision is empty, ", cur.Namespace, cur.Annotations[MiddlewareRedisClusterNameKey])
		klog.Errorf(err.Error())
		return false,false,err
	}

	//??????????????????
	isHorizontalScale := rco.isHorizontalScale(curRedisClusterSpec, oldRedisClusterSpec)
	//??????????????????
	isVerticalScale := rco.isVerticalScale(curRedisClusterSpec, oldRedisClusterSpec)
	return isHorizontalScale,isVerticalScale,nil
}


func (rco *RedisClusterOperator) upgradeCluster(redisCluster *RedisCluster,isHorizontalScale bool,isVerticalScale bool,
	curHash string,curReplicas int32) (err error){
	if !isHorizontalScale && !isVerticalScale {
		toUpdateSts := rco.buildRedisClusterStatefulset(redisCluster.Namespace, redisCluster.Name, redisCluster)
		util.AddLabel(toUpdateSts.Labels, util.OperatorRevisionHashLabelKey, curHash)
		//update statefulset
		_, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Update(context.TODO(),toUpdateSts,metav1.UpdateOptions{})
		return err
	}

	if isHorizontalScale {
		//??????????????????
		updateType := redisCluster.Spec.UpdateStrategy.Type
		if updateType != AssignReceiveStrategyType && updateType != AutoReceiveStrategyType {
			err := fmt.Errorf("upgrade redis cluster: %v/%v UpdateStrategy Type error: %v", redisCluster.Namespace, redisCluster.Name, updateType)
			klog.Error(err.Error())
			rco.updateRedisClusterStatus(redisCluster, nil, rcserrors.NewInvalid(err.Error(), RedisClusterRunning), false)
			return nil
		}

		//????????????????????????,?????????????????????
		strategiesLen := int32(len(redisCluster.Spec.UpdateStrategy.AssignStrategies))
		scaleLen := *redisCluster.Spec.Replicas - curReplicas
		if (updateType == AssignReceiveStrategyType) && strategiesLen != (scaleLen/2) {
			err := fmt.Errorf("upgrade redis cluster: %v/%v slots AssignStrategies error", redisCluster.Namespace, redisCluster.Name)
			klog.Error(err.Error())
			rco.updateRedisClusterStatus(redisCluster, nil, rcserrors.NewInvalid(err.Error(), RedisClusterRunning), false)
			return nil
		}
	}

	var newRedisCluster *RedisCluster
	//???????????????Scaling
	if redisCluster.Status.Phase != RedisClusterRollback {
		newRedisCluster, err = rco.updateRedisClusterStatus(redisCluster, nil, rcserrors.NewRedisClusterState(redisCluster.Status.Reason, RedisClusterScaling, redisCluster.Status.ReasonType), false)
		if err != nil {
			return err
		}
	} else {
		newRedisCluster = redisCluster
	}

	//????????????
	err = rco.storeRedisClusterPassword(newRedisCluster)
	if err != nil {
		return err
	}

	if isHorizontalScale {
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "UpgradeClusterStart", "hScaling:hScalingClusterStart:Cluster %v is upgrading start", redisCluster.Name)
	} else {
		rco.eventRecorder.LabeledEventf(redisCluster, record.VScaleEvent, v1.EventTypeNormal, "UpgradeClusterStart", "vScaling:vScalingClusterStart:Cluster %v is upgrading start", redisCluster.Name)
	}


	//??????
	oldEndpoints, err := rco.defaultClient.CoreV1().Endpoints(newRedisCluster.Namespace).Get(context.TODO(),newRedisCluster.Name, metav1.GetOptions{})
	if err != nil {
		//???????????????Failed
		rco.updateRedisClusterStatus(newRedisCluster, oldEndpoints, rcserrors.NewUnexpectedError(err.Error(), RedisClusterFailed), false)
		return err
	}

	//????????????
	recSts := rco.buildRedisClusterStatefulset(redisCluster.Namespace, redisCluster.Name, newRedisCluster)
	util.AddLabel(recSts.Labels, util.OperatorRevisionHashLabelKey, curHash)

	//update statefulset
	sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Update(context.TODO(),recSts,metav1.UpdateOptions{})

	if err != nil {
		return fmt.Errorf("upgrade redis cluster scale statefulset: %v is error: %v", sts.Name, err)
	}

	// ????????????
	if isVerticalScale {
		rco.eventRecorder.LabeledEventf(newRedisCluster, record.VScaleEvent, v1.EventTypeNormal, "ScalePod", "vScaling:scalePod:Cluster %v is scaling pod", redisCluster.Name)
		klog.V(3).Infof("vertical scale: %v/%v updates stateful set has finished, updating in background", redisCluster.Namespace, redisCluster.Name)
		var ep *v1.Endpoints
		//?????????????????????,???????????????,??????????????????,??????????????????,????????????????????????,?????????????????????,???????????????
		ep, err = rco.checkPodInstanceIsReadyByEndpoint(redisCluster.Namespace, redisCluster.Name, *sts.Spec.Replicas)
		if err != nil && err == stopWaitError {
			klog.Warningf("stop wait all pod ready, will start drop redisCluster:%v/%v", redisCluster.Namespace, redisCluster.Name)
			return nil
		}

		if err != nil {

			rco.eventRecorder.LabeledEventf(newRedisCluster, record.VScaleEvent, v1.EventTypeWarning, "UpgradeClusterFailed", "vScaling:vScalingClusterFailed:Cluster %v is upgraded failed: %v", redisCluster.Name, err.Error())

			//???????????????????????????
			_, err := rco.updateRedisClusterStatus(newRedisCluster, ep, rcserrors.NewUpgradeFailed(err.Error(), RedisClusterRollback), false)
			if err != nil {
				return err
			}

			/*newRcs, err := rco.updateRedisClusterStatus(newRedisCluster, ep, rcserrors.NewUpgradeFailed(err.Error(), RedisClusterRunning), true)
			if err != nil {
				return err
			}
			_, maxControllerRevision := maxRevision(old)
			rcs, err := controllerRevisionToRedisClusterSpec(maxControllerRevision)
			if err != nil {
				return err
			}
			newRcs.Spec = *rcs
			//??????????????????controller revision??????
			_, err = rco.customCRDClient.CrV1alpha1().RedisClusters(namespace).Update(newRcs)*/
			return nil
		}

		rco.eventRecorder.LabeledEventf(newRedisCluster, record.VScaleEvent, v1.EventTypeNormal, "UpgradeClusterSuccess", "vScaling:vScalingClusterSuccess:Cluster %v is upgraded successfully", newRedisCluster.Name)

		if !isHorizontalScale {
			return err
		}
	}

	rco.eventRecorder.LabeledEventf(newRedisCluster, record.HScaleEvent, v1.EventTypeNormal, "ScalePod", "hScaling:scalePod:Cluster %v is scaling pod", newRedisCluster.Name)
	//??????endpoint pod???ready,?????????????????????
	return rco.upgradeRedisCluster(newRedisCluster, oldEndpoints)
}

func (rco *RedisClusterOperator)createClusterFirst(redisCluster *RedisCluster,curHash string,existSts *appsv1.StatefulSet)error{
	//???????????????Creating
	newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, rcserrors.NewRedisClusterState(redisCluster.Status.Reason, RedisClusterCreating, redisCluster.Status.ReasonType), false)
	if err != nil {
		return err
	}

	//????????????
	err = rco.storeRedisClusterPassword(newRedisCluster)
	if err != nil {
		return err
	}

	klog.Infof("Changed sync redisCluster: %v ResourceVersion: %v -->> %v", redisCluster.Name, redisCluster.ResourceVersion, newRedisCluster.ResourceVersion)

	//create service
	recService := rco.buildRedisClusterService(newRedisCluster, redisCluster.Namespace, redisCluster.Name)
	_, err = rco.defaultClient.CoreV1().Services(redisCluster.Namespace).Create(context.TODO(),recService,metav1.CreateOptions{})

	if errors.IsAlreadyExists(err) {
		klog.Warningf("redis cluster create service: %v/%v Is Already Exists", redisCluster.Namespace, redisCluster.Name)
	} else if err != nil {
		return fmt.Errorf("init redis cluster create service: %v/%v is error: %v", redisCluster.Namespace, redisCluster.Name, err)
	}

	recSts := rco.buildRedisClusterStatefulset(redisCluster.Namespace, redisCluster.Name, newRedisCluster)
	util.AddLabel(recSts.Labels, util.OperatorRevisionHashLabelKey, curHash)

	var sts *appsv1.StatefulSet
	if existSts == nil {
		// create when statefulset is not exist
		sts, err = rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Create(context.TODO(),recSts,metav1.CreateOptions{})
	} else {
		// update when statefulset Replicas is 0
		sts, err = rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Update(context.TODO(),recSts,metav1.UpdateOptions{})
	}

	if err != nil {
		return fmt.Errorf("init redis cluster create statefulset: %v is error: %v", sts.Name, err)
	}

	rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "CreatePod", "creating:createPod:Cluster %v is creating pod", redisCluster.Name)

	//??????endpoint pod???ready,???????????????????????????
	err = rco.createAndInitRedisCluster(newRedisCluster)
	if err != nil {
		rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeWarning, "CreateClusterFailed", "creating:createClusterFailed:Cluster %v is created failed: %v", redisCluster.Name, err)
	}
	return err
}