package redis

import (
	"bytes"
	"context"
	"encoding/json"
	errors2 "errors"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	client2 "sigs.k8s.io/controller-runtime/pkg/client"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/pkg/record"
	"harmonycloud.cn/middleware/redis-cluster/util"
	"harmonycloud.cn/middleware/redis-cluster/util/errors"
	"harmonycloud.cn/middleware/redis-cluster/util/recycler"
	stsutil "harmonycloud.cn/middleware/redis-cluster/util/statefulset"
	apps "k8s.io/api/apps/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	//"k8s.io/kubernetes/pkg/controller/volume/events"
)

const (
	defaultPathPrefix = "/nfs"
	//stderrWarning = "Warning: Using a password with '-a' or '-u' option on the command line interface may not be safe."
	stderrWarning     = "Warning"
	stderrUnknownNode = "Unknown node"

	redisClusterPvSuffix         = "-pv"
	redisClusterPvcSuffix        = "-claim"
	redisClusterConfigMapsSuffix = "-config"
	envDomainNameKey             = "DOMAINNAME"
	envPathPrefixKey             = "PATHPREFIX"
	middlewarePodFixedNodeKey    = "fixed-node-middleware-pod"
	dateFormatter                = "2006-01-02 15:04:05"
	forgetNodeExecCount          = 2
	redisPasswordArg             = " -a '%v' "

	sentinelName           = "sentinel"
	baseName               = "rc"
	appLabel               = "redis-cluster"
	sentinelRoleName       = "sentinel"
	sentinelConfigFileName = "sentinel.conf"
)

var stopWaitError = errors2.New("receive drop redisCluster, stop wait")

//redis-cli -c -h 10.16.78.90 -p 6379 cluster info
//clusterInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execClusterInfo(clusterInstanceIp, podName, namespace string) (bool, bool, error) {
	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	clusterInfoCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli -c -h %v -p 6379 %v cluster info", clusterInstanceIp, pwdArgs)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterInfoCmd, redisContainerName, podName, namespace, nil)
	//klog.Infof("clusterInfoCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
		err := fmt.Errorf("exec cluster info Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", clusterInfoCmd, stdout, stderr, err)
		klog.Errorf(err.Error())
		return false, false, err
	}

	isOK := strings.Contains(stdout, clusterStatusOK)

	subMatches := clusterInfoReg.FindStringSubmatch(stdout)
	if len(subMatches) != 2 {
		err := fmt.Errorf("cluster info by podName: %v/%v match cluster_known_nodes error. stdout: %v\n", namespace, podName, stdout)
		klog.Errorf(err.Error())
		return false, false, err
	}

	isOnlyKnownSelf := false
	if subMatches[1] == "1" {
		isOnlyKnownSelf = true
	}

	return isOK, isOnlyKnownSelf, nil
}

func (rco *RedisClusterOperator) execMSInfo(clusterInstanceIp, podName, namespace string) (bool, bool, error) {
	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	clusterNodeInfosCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli  -h %v -p 6379 %v info replication", clusterInstanceIp, pwdArgs)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterNodeInfosCmd, redisContainerName, podName, namespace, nil)
	klog.Infof("clusterNodeInfosCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	repliInfos := strings.Split(stdout, "\r\n")
	connectedSlaves, err := strconv.Atoi(strings.Split(repliInfos[2], ":")[1])

	if connectedSlaves == 0 {
		return false, false, nil
	} else {
		return true, false, nil
	}

}

//回滚
func (rco *RedisClusterOperator) rollback(endpoints *v1.Endpoints, err error, redisCluster *RedisCluster, old []*apps.ControllerRevision) error {

	defer func() {
		if err != nil {
			klog.Errorf("rollback redisCluster: %v/%v error: %v", redisCluster.Namespace, redisCluster.Name, err)
		}
	}()
	//更新状态为Failing
	newRcs, err := rco.updateRedisClusterStatus(redisCluster, endpoints, err, false)

	if err != nil {
		return err
	}

	if old == nil {
		_, old, err = rco.constructHistory(redisCluster)
		if err != nil {
			return err
		}
	}
	_, maxControllerRevision := maxRevision(old)
	rcs, err := controllerRevisionToRedisClusterSpec(maxControllerRevision)
	if err != nil {
		return err
	}
	newRcs.Spec = *rcs
	//创建pod失败回滚到上一个controller revision版本
	err = rco.frameClient.Status().Update(context.TODO(),newRcs)
	//_, err = rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Update(newRcs)
	return err
}

//升级redis集群
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) upgradeRedisCluster(redisCluster *RedisCluster, oldEndpoints *v1.Endpoints) (err error) {
	var (
		endpoints       = oldEndpoints.DeepCopy()
		newRedisCluster = redisCluster.DeepCopy()
	)
	defer func() {
		if err != nil {
			err = rco.handleUpgradeRedisClusterError(err, newRedisCluster, endpoints)
		}
		return
	}()

	if len(oldEndpoints.Subsets) == 0 || len(oldEndpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("RedisCluster: %v/%v endpoints address is empty", redisCluster.Namespace, redisCluster.Name)
	}

	//判断pod都已经更新到最新sts版本
	err = rco.checkStsPodUpdated(redisCluster.Namespace, redisCluster.Name)
	if err != nil && err == stopWaitError {
		klog.Warningf("stop wait all pod ready, will start drop redisCluster:%v/%v", redisCluster.Namespace, redisCluster.Name)
		return nil
	}
	if err != nil {
		klog.Errorf("upgrade redisCluster: %v/%v wait sts pod is updated error: %v", redisCluster.Namespace, redisCluster.Name, err)
		err = errors.NewCreatePodFailedWhenUpgradeCluster(err.Error(), RedisClusterRollback)
		return err
	}
	//这里将阻塞
	endpoints, err = rco.checkPodInstanceIsReadyByEndpoint(redisCluster.Namespace, redisCluster.Name, *redisCluster.Spec.Replicas)
	if err != nil && err == stopWaitError {
		klog.Warningf("stop wait all pod ready, will start drop redisCluster:%v/%v", redisCluster.Namespace, redisCluster.Name)
		return nil
	}

	if err != nil {
		klog.Warningf("upgrade redisCluster: %v/%v wait pod is ready error: %v -- start rollback", redisCluster.Namespace, redisCluster.Name, err)
		err = errors.NewCreatePodFailedWhenUpgradeCluster(err.Error(), RedisClusterRollback)
		return err
	}

	//TODO 横向纵向同时扩容,当实例数先扩到10后,开始将旧的6个pod更新重建,在扩到10的一刹那,可能endpoints里已经有10个ready的pod
	//TODO 此时会开始加节点,卡槽迁移过程中,老的实例被删除重建,可能会导致迁移失败,所以加statefulset里status的updateRevision和curRevision的判断是否全部pod已经被更新为最新版本

	//更新状态为Upgrading
	newRedisCluster, err = rco.updateRedisClusterStatus(redisCluster, nil, errors.NewRedisClusterState(redisCluster.Status.Reason, RedisClusterUpgrading, redisCluster.Status.ReasonType), false)
	if err != nil {
		return err
	}

	//密码存储
	err = rco.storeRedisClusterPassword(newRedisCluster)
	if err != nil {
		return err
	}

	if endpoints.Subsets == nil || endpoints.Subsets[0].Addresses == nil {
		err = fmt.Errorf("upgraded redisCluster: %v/%v endpoints is blank", redisCluster.Namespace, redisCluster.Name)
		klog.Error(err.Error())
		//更新状态为Running以及更新当前error信息
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, nil, nil)
		return err
	}

	//endpoints根据podName从小到大排序
	sortEndpointsByPodName(endpoints, oldEndpoints)

	upgradeType := newRedisCluster.Spec.UpdateStrategy.Type
	//实例全部ready初始化集群
	switch upgradeType {
	case AutoReceiveStrategyType:
		err = rco.autoAssignSlotToRedisCluster(endpoints, newRedisCluster, oldEndpoints)
		if err != nil {
			klog.Errorf("redisCluster upgrade autoAssign slot to RedisCluster is error: %v", err)
			return err
		}
	case AssignReceiveStrategyType:
		err = rco.manualAssignSlotToRedisCluster(endpoints, newRedisCluster, oldEndpoints)
		if err != nil {
			klog.Errorf("redisCluster upgrade manualAssign slot to RedisCluster is error: %v", err)
			return err
		}
	default:
		klog.Errorf("redisCluster: %v/%v UpdateStrategy Type only [AutoReceive, AssignReceive] error", redisCluster.Namespace, redisCluster.Name)
		return err
	}

	//handle exporter sts and service
	err = rco.handleRedisExporterStsAndSvc(redisCluster)
	if err != nil {
		//TODO 不回滚
		return err
	}
	rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "UpgradeClusterSuccess", "hScaling:hScalingClusterSuccess:Cluster %v is upgraded successfully", redisCluster.Name)
	return nil
}

//自动分配
//负责调用自动分配逻辑和更新状态
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
//TODO 如果有一个节点重启了,从该节点看到的nodeInfo里该节点IP是不对的,可能会存在问题
func (rco *RedisClusterOperator) autoAssignSlotToRedisCluster(endpoints *v1.Endpoints, redisCluster *RedisCluster, oldEndpoints *v1.Endpoints) error {

	existInstanceIps, _, err := rco.scaleRedisCluster(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		klog.Errorf(err.Error())
		return err
	}

	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "RebalanceSlots", "hScaling:rebalanceSlots:Cluster %v is rebalancing slots", redisCluster.Name)
	//执行rebalance命令,将16384个卡槽平均分配到新master节点
	err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIps[0], reference.Name, reference.Namespace, redisCluster.Spec.UpdateStrategy.Pipeline)
	if err != nil {
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "RebalanceSlotsFailed", "hScaling:rebalanceSlotsFailed:Cluster %v rebalances slots failed: %v", redisCluster.Name, err)
		//更新状态为Running
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewRebalanceSlotsFailed(err.Error(), RedisClusterFailed), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterFailed, RedisClusterReasonTypeRebalanceSlotsFailed, nil, existInstanceIps)
		return err
	}

	_, err = rco.execClusterNodes(existInstanceIps[0], reference.Namespace, reference.Name, false)
	if err != nil {
		err = fmt.Errorf("auto upgrade redisCluster: %v/%v cluster nodes error: %v ", reference.Namespace, reference.Name, err)
		klog.Error(err.Error())
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "RebalanceSlotsFailed", "hScaling:rebalanceSlotsFailed:Cluster %v rebalances slots failed: %v", redisCluster.Name, err)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterFailed, RedisClusterReasonTypeRebalanceSlotsFailed, nil, existInstanceIps)
		return err
	}

	klog.Infof("cluster upgrade success, will update redisCluster status as Running")
	//更新状态为Running
	_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
	if err != nil {
		klog.Errorf("redisCluster:%v/%v after cluster auto upgrade success, update redisCluster status error: %v", redisCluster.Namespace, redisCluster.Name, err)
		latestRc := &RedisCluster{}
		err = rco.frameClient.Get(context.TODO(),types.NamespacedName{redisCluster.Namespace,redisCluster.Name},latestRc)
		//latestRc, err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		_, err = rco.updateRedisClusterStatus(latestRc, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		return err
	}
	return err
}

//手动分配
//负责调用手动分配逻辑和更新状态
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) manualAssignSlotToRedisCluster(endpoints *v1.Endpoints, redisCluster *RedisCluster, oldEndpoints *v1.Endpoints) error {

	//1、加入新master

	//2、给新master加入slave

	//3、reshare分配卡槽

	existInstanceIps, newMasterNodeIds, err := rco.scaleRedisCluster(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		//更新状态为Running以及更新当前error信息
		klog.Errorf(err.Error())
		return err
	}

	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "ReshareSlots", "hScaling:reshareSlots:Cluster %v is resharing slots", redisCluster.Name)
	//执行reshare命令,将指定nodeId个卡槽分配到新master节点
	err = rco.reshareRedisClusterSlotsToMasterNode(redisCluster.Spec.UpdateStrategy, existInstanceIps[0], reference.Name, reference.Namespace, newMasterNodeIds)
	if err != nil {
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "ReshareSlotsFailed", "hScaling:reshareSlotsFailed:Cluster %v reshares slots failed: %v", redisCluster.Name, err)
		//更新状态为Running
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewReshareSlotsFailed(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterFailed, RedisClusterReasonTypeReshareSlotsFailed, nil, existInstanceIps)
		return err
	}

	_, err = rco.execClusterNodes(existInstanceIps[0], redisCluster.Namespace, reference.Name, true)
	if err != nil {
		err = fmt.Errorf("manual upgrade redisCluster: %v/%v cluster nodes error: %v ", reference.Namespace, reference.Name, err)
		klog.Error(err.Error())
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "ReshareSlotsFailed", "hScaling:reshareSlotsFailed:Cluster %v reshares slots failed: %v", redisCluster.Name, err)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterFailed, RedisClusterReasonTypeReshareSlotsFailed, nil, existInstanceIps)
		return err
	}

	klog.Infof("cluster upgrade success, will update redisCluster status as Running")
	//更新状态为Running
	_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
	if err != nil {
		klog.Errorf("redisCluster:%v/%v after cluster manual upgrade success, update redisCluster status error: %v", redisCluster.Namespace, redisCluster.Name, err)
		latestRc := &RedisCluster{}
		err = rco.frameClient.Get(context.TODO(),types.NamespacedName{redisCluster.Namespace,redisCluster.Name},latestRc)
		//latestRc, err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		_, err = rco.updateRedisClusterStatus(latestRc, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		return err
	}
	return err
}

//创建和初始化集群,包括新建master,加slave和更新redisCluster对象status
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) createAndInitRedisCluster(redisCluster *RedisCluster) error {

	password, isExistPassword := rco.loadRedisClusterPasswordByKey(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", redisCluster.Namespace, redisCluster.Name)
		pwdArgs = ""
	}

	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(redisCluster.Namespace, redisCluster.Name, *redisCluster.Spec.Replicas)
	if err != nil && err == stopWaitError {
		klog.Warningf("stop wait all pod ready, will start drop redisCluster:%v/%v", redisCluster.Namespace, redisCluster.Name)
		return nil
	}
	//由于没加入到集群,数据保留也没关系,再者,由于是hostpath方式,所以没ready的pod也exec不进去执行删除命令,创建失败middleware加删除标识,走删除集群流程
	if err != nil {
		//更新状态为Failing
		_, err := rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewCreatePodFailedWhenCreateCluster(err.Error(), RedisClusterRollback), false)
		return err
	}

	if endpoints == nil || len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	//endpoints根据podName从小到大排序
	sortEndpointsByPodName(endpoints)

	//获取address
	willAssignIPAddresses, err := rco.assignMasterSlaveIPAddress(endpoints, nil)
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
		return err
	}

	//分配master、slave IP
	masterInstanceIps, slaveInstanceIps, err := rco.assignMasterSlaveIP(willAssignIPAddresses)
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
		return err
	}

	//endpoint里pod信息
	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "InitCluster", "creating:initCluster:Cluster %v is initializing", redisCluster.Name)

	if len(masterInstanceIps) > 1 {
		//先创建初始化集群
		err = rco.createCluster(masterInstanceIps, reference)
		if err != nil {
			rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeWarning, "InitClusterFailed", "creating:initCluster:Cluster %v init failed: %v", redisCluster.Name, err)
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewInitClusterFailed(err.Error(), RedisClusterRollback), false)
			return err
		}

		//查询新建集群的节点信息,主要是master的nodeId信息
		nodeInfos, err := rco.execClusterNodes(masterInstanceIps[0], reference.Namespace, reference.Name, true)

		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
			return err
		}

		if len(nodeInfos) != len(slaveInstanceIps) {
			err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
			klog.Errorf(err.Error())
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
			return err
		}

		// 根据masterInstanceIp拿nodeId,下标对应
		var masterInstanceNodeIds []string
		for _, masterInstanceIp := range masterInstanceIps {
			for _, info := range nodeInfos {
				if masterInstanceIp+":6379" == info.IpPort {
					masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
					break
				}
			}
		}

		rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "AddSlave", "creating:addSlave:Cluster %v is adding slave", redisCluster.Name)
		var successAddInstances []string
		//给新建集群的master加入slave
		//redis-cli --cluster add-node --slave --master-id 1d91acc0 10.168.78.82:6379 10.168.78.83:6379
		err = rco.addSlaveToClusterMaster(slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds, reference.Namespace, reference.Name, &successAddInstances)
		if err != nil {
			rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeWarning, "AddSlaveFailed", "creating:addSlaveFailed:Cluster %v adds slave failed: %v", redisCluster.Name, err)
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewAddSlaveFailedWhenCreateCluster(err.Error(), RedisClusterRollback), false)
			return err
		}

		//会先去查询集群状态,检查成功之前会阻塞,直到检查超时,检查目的:redis实例之间node.conf不一致可能查到刚加入的slave为master的情况
		_, err = rco.execClusterNodes(masterInstanceIps[0], redisCluster.Namespace, reference.Name, true)

		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
			return err
		}

		klog.Infof("redisCluster: %v/%v create and init success, will create redis exporter", redisCluster.Namespace, redisCluster.Name)

	} else {

		var createMasterSlaveCommand = fmt.Sprintf("redis-cli  -h %v -p 6379 %v SLAVEOF %v 6379 | redis-cli  -h %v -p 6379 %v  CONFIG SET masterauth %v", slaveInstanceIps[0], pwdArgs, masterInstanceIps[0], slaveInstanceIps[0], pwdArgs, password)
		klog.Infof("create createMasterSlaveCommand is: %v ", createMasterSlaveCommand)

		commandMaster := []string{"/bin/sh", "-c", createMasterSlaveCommand}
		slavePodName := endpoints.Subsets[0].Addresses[1].TargetRef.Name
		slavePodNamespace := endpoints.Subsets[0].Addresses[1].TargetRef.Namespace
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, redisContainerName, slavePodName, slavePodNamespace, nil)

		if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
			err := fmt.Errorf("exec create MasterSlave Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", commandMaster, stdout, stderr, err)
			klog.Errorf(err.Error())
			return err
		}
	}
	//handle exporter sts and service
	err = rco.handleRedisExporterStsAndSvc(redisCluster)
	if err != nil {
		//TODO 不回滚
		return err
	}

	//更新状态为Running
	_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)

	if err == nil {
		rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "CreateClusterSuccess", "creating:createClusterSuccess:Cluster %v is created", redisCluster.Name)
	}
	return err
}

//查看节点信息
//clusterInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execClusterNodes(clusterInstanceIp, namespace, podName string, isWaitAllNodeAgreeConfig bool) ([]*redisNodeInfo, error) {

	if isWaitAllNodeAgreeConfig {
		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(clusterInstanceIp, podName, namespace)
		if err != nil {
			return nil, err
		}
	}

	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	clusterNodeInfosCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli -c -h %v -p 6379 %v cluster nodes", clusterInstanceIp, pwdArgs)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterNodeInfosCmd, redisContainerName, podName, namespace, nil)
	klog.Infof("clusterNodeInfosCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
		err := fmt.Errorf("exec cluster nodes Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v \n", clusterNodeInfosCmd, stdout, stderr, err)
		klog.Errorf(err.Error())
		return []*redisNodeInfo{}, err
	}

	outNodeInfos := strings.Split(stdout, "\n")

	var nodeInfos []*redisNodeInfo

	//转换node信息
	for _, lineInfo := range outNodeInfos {
		if len(lineInfo) == 0 {
			continue
		}

		info := strings.Fields(lineInfo)

		if len(info) < 8 {
			continue
		}

		//klog.Infof("nodeInfo: %v ", info)
		nodeInfo := &redisNodeInfo{}
		nodeInfo.NodeId = info[0]
		nodeInfo.IpPort = strings.Split(info[1], "@")[0]

		//处理myself,slave或者myself,master、master,fail
		if strings.Contains(info[2], "myself") {
			nodeInfo.Flags = strings.Split(info[2], ",")[1]
		} else {
			nodeInfo.Flags = info[2]
		}

		nodeInfo.Master = info[3]
		nodeInfo.PingSent = info[4]
		nodeInfo.PongRecv = info[5]
		nodeInfo.ConfigEpoch = info[6]
		nodeInfo.LinkState = info[7]

		//slave没有slot,也可能master没分配slot
		if len(info) >= 9 {
			nodeInfo.Slot = strings.Join(info[8:], " ")
		}

		nodeInfos = append(nodeInfos, nodeInfo)
	}

	return nodeInfos, nil
}

/*
# Replication
role:slave
master_host:10.168.115.164
master_port:6379
master_link_status:up
master_last_io_seconds_ago:5
master_sync_in_progress:0
slave_repl_offset:7547
slave_priority:100
slave_read_only:1
connected_slaves:0
master_repl_offset:0
repl_backlog_active:0
repl_backlog_size:268435456
repl_backlog_first_byte_offset:0
repl_backlog_histlen:0


# Replication
role:master
connected_slaves:1
slave0:ip=10.168.102.208,port=6379,state=online,offset=9503,lag=0
master_repl_offset:9503
repl_backlog_active:1
repl_backlog_size:268435456
repl_backlog_first_byte_offset:2
repl_backlog_histlen:9502

# Replication
role:master
connected_slaves:0
master_repl_offset:1555
repl_backlog_active:1
repl_backlog_size:268435456
repl_backlog_first_byte_offset:2
repl_backlog_histlen:1554
*/
func (rco *RedisClusterOperator) execMSNodes(clusterInstanceIp, namespace, podName string) ([]*redisNodeInfo, error) {
	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	clusterNodeInfosCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli  -h %v -p 6379 %v info replication", clusterInstanceIp, pwdArgs)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterNodeInfosCmd, redisContainerName, podName, namespace, nil)
	klog.Infof("clusterNodeInfosCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	repliInfos := strings.Split(stdout, "\r\n")

	var nodeInfos []*redisNodeInfo
	var masterOrSlave = strings.Split(repliInfos[1], ":")[1]
	masterNodeInfo := &redisNodeInfo{}
	masterNodeInfo.Flags = "master"
	slaveNodeInfo := &redisNodeInfo{}
	slaveNodeInfo.Flags = "slave"

	if masterOrSlave == "master" {

		masterNodeInfo.IpPort = clusterInstanceIp + ":6379"
		slaveIp := strings.Split(strings.Split(strings.Split(repliInfos[3], ":")[1], ",")[0], "=")[1]
		slaveNodeInfo.IpPort = slaveIp + ":6379"
	} else if masterOrSlave == "slave" {
		slaveNodeInfo.IpPort = clusterInstanceIp + ":6379"
		masterIp := strings.Split(repliInfos[2], ":")[1]
		masterNodeInfo.IpPort = masterIp + ":6379"
	}

	nodeInfos = append(nodeInfos, masterNodeInfo)
	nodeInfos = append(nodeInfos, slaveNodeInfo)

	return nodeInfos, nil
}

//这里将阻塞-->>检查实例是否ready
func (rco *RedisClusterOperator) checkPodInstanceIsReadyByEndpoint(namespace, name string, replicas int32) (*v1.Endpoints, error) {
	var endpoints *v1.Endpoints
	var err error
	f := wait.ConditionFunc(func() (bool, error) {
		//有删除标识停止等待
		value, isExist := redisClusterStopWait.Load(fmt.Sprintf("%v/%v", namespace, name))
		if isExist {
			v, ok := value.(bool)
			if ok && v {
				return false, stopWaitError
			}
		}

		endpoints, err = rco.defaultClient.CoreV1().Endpoints(namespace).Get(context.TODO(),name, metav1.GetOptions{})

		if err != nil {
			return false, fmt.Errorf("get redis cluster endpoint is error: %v", err)
		}

		if endpoints.Subsets == nil {
			sts, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Get(context.TODO(),name,metav1.GetOptions{})
			if err != nil {
				return false, fmt.Errorf("get redis cluster StatefulSets: %v/%v is error: %v", namespace, name, err)
			}
			if sts == nil || *sts.Spec.Replicas == 0 {
				return false, fmt.Errorf("redis cluster StatefulSets: %v/%v is not exist or Spec.Replicas is 0", namespace, name)
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

	//2s查询一次endpoint,看pod是否全部ready,if return true,则开始初始化集群;
	// 否则继续查,总共time.Duration(timeout)分钟后创建超时,更新redisCluster状态
	timeout := rco.options.ClusterTimeOut
	err = wait.Poll(2*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//创建或者升级集群超时
		klog.Errorf("create or upgrade is error: %v .. update redisCluster status..", err)
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

//超时后的pod message
func (rco *RedisClusterOperator) getPodStatus(namespace, name string) (string, error) {

	//生成Selector
	label := labels.SelectorFromSet(labels.Set(map[string]string{"app": name}))

	//查找sts pod
	pods, err := stsutil.GetPodsForStatefulSet(rco.defaultClient, namespace, label)
	if err != nil {
		klog.Errorf("get sts pod error: %v after timed out waiting ", err)
		return "", err
	}

	var errorList []string
	for _, pod := range pods {
		for _, con := range pod.Status.Conditions {
			if con.Status != v1.ConditionTrue {
				errorList = append(errorList, fmt.Sprintf("InstanceName: %v\nReason: %v\nMessage: %v\n", pod.Name, con.Reason, con.Message))
			}
		}
	}

	return strings.Join(errorList, "\n"), nil
}

//这里将阻塞-->>检查sts pod是否更新到最新状态
func (rco *RedisClusterOperator) checkStsPodUpdated(namespace, name string) error {
	var err error
	f := wait.ConditionFunc(func() (bool, error) {
		//有删除标识停止等待
		value, isExist := redisClusterStopWait.Load(fmt.Sprintf("%v/%v", namespace, name))
		if isExist {
			v, ok := value.(bool)
			if ok && v {
				return false, stopWaitError
			}
		}

		latestSts, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Get(context.TODO(),name,metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get redis cluster latestSts is error: %v", err)
		}

		if latestSts.Status.CurrentRevision == latestSts.Status.UpdateRevision && latestSts.Status.CurrentReplicas == latestSts.Status.UpdatedReplicas && latestSts.Status.Replicas == *latestSts.Spec.Replicas {
			return true, nil
		}

		return false, nil
	})

	timeout := rco.options.ClusterTimeOut
	err = wait.Poll(2*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//创建或者升级集群超时
		klog.Errorf("upgrade is error: %v", err)
		if err == wait.ErrWaitTimeout {
			podStatusMessages, err1 := rco.getPodStatus(namespace, name)
			if err1 != nil {
				return err
			}
			return fmt.Errorf("%v, %v", err, podStatusMessages)
		}
		return err
	}

	return nil
}

//更新redisCluster对象的状态
//redisCluster：redisCluster对象
//endpoints：endpoint信息
//phase：集群状态
//abnormalReason：异常原因
func (rco *RedisClusterOperator) updateRedisClusterStatus(redisCluster *RedisCluster, endpoints *v1.Endpoints, errStatus error, isOnlyBuildRedisCluster bool) (*RedisCluster, error) {

	scaleFunc := func(tempStatus *RedisClusterStatus) (*RedisCluster, error) {
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			klog.Errorf(err.Error())
			return redisCluster, err
		}

		if endpoints == nil {
			err = rco.frameClient.Get(context.TODO(),types.NamespacedName{redisCluster.Namespace,redisCluster.GetName()},endpoints)

			//endpoints, err = rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(context.TODO(),redisCluster.GetName(), metav1.GetOptions{})
			if err != nil {
				return redisCluster, fmt.Errorf("update redisCluster status -- get redis cluster endpoint is error: %v", err)
			}
		}

		tempStatus.Conditions = buildInstanceStatusCondition(redisCluster, endpoints)

		tempStatus.Replicas = sts.Status.ReadyReplicas
		tempStatus.Reason = errors.Message(errStatus)
		tempStatus.ReasonType = errors.ReasonType(errStatus)
		return nil, nil
	}

	var tempStatus RedisClusterStatus

	phase := errors.Phase(errStatus)
	klog.V(4).Infof("rc: %v/%v phase: %v", redisCluster.Namespace, redisCluster.Name, phase)
	switch phase {
	//代表该CRD刚创建
	case RedisClusterNone:
		tempStatus = RedisClusterStatus{
			Phase:          RedisClusterNone,
			Reason:         errors.Message(errStatus),
			Conditions:     redisCluster.Status.Conditions,
			ReasonType:     errors.ReasonType(errStatus),
			HistoryReasons: redisCluster.Status.HistoryReasons,
		}
		//代表等待redis资源对象创建完毕
	case RedisClusterCreating:

		tempStatus = RedisClusterStatus{
			//Replicas: *sts.Spec.Replicas,
			Replicas:       0,
			Phase:          RedisClusterCreating,
			Reason:         errors.Message(errStatus),
			ReasonType:     errors.ReasonType(errStatus),
			Conditions:     redisCluster.Status.Conditions,
			HistoryReasons: redisCluster.Status.HistoryReasons,
		}

		//代表已进行初始化操作
	case RedisClusterRunning:

		var err error
		if endpoints == nil {
			endpoints, err = rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(context.TODO(),redisCluster.GetName(), metav1.GetOptions{})
			if err != nil {
				return redisCluster, fmt.Errorf("update redisCluster status -- get redis cluster endpoint is error: %v", err)
			}
		}

		if endpoints.Subsets == nil {
			return redisCluster, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
		}

		sortEndpointsByPodName(endpoints)

		sts, err :=  rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			klog.Errorf(err.Error())
			return redisCluster, err
		}

		clusterConditions, err := rco.buildRedisClusterStatusConditions(redisCluster, endpoints)
		if err != nil {
			klog.Errorf(err.Error())
			return rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterFailed), false)
		}

		tempStatus = RedisClusterStatus{
			Replicas:       sts.Status.ReadyReplicas,
			Phase:          RedisClusterRunning,
			Reason:         errors.Message(errStatus),
			Conditions:     clusterConditions,
			ReasonType:     errors.ReasonType(errStatus),
			HistoryReasons: redisCluster.Status.HistoryReasons,
		}

		//代表着实例不一致(用户修改实例，operator发现实例不一致，更新statefulset，更新状态)
	case RedisClusterScaling:
		rc, err := scaleFunc(&tempStatus)
		if err != nil {
			return rc, err
		}
		tempStatus.HistoryReasons = redisCluster.Status.HistoryReasons
		tempStatus.Phase = RedisClusterScaling
		//代表着升级中
	case RedisClusterUpgrading:
		sts, err :=  rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})
		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			klog.Errorf(err.Error())
			return redisCluster, err
		}
		tempStatus.Replicas = sts.Status.ReadyReplicas
		tempStatus.Phase = RedisClusterUpgrading
		tempStatus.Reason = errors.Message(errStatus)
		tempStatus.ReasonType = errors.ReasonType(errStatus)
		tempStatus.Conditions = redisCluster.Status.Conditions
		tempStatus.HistoryReasons = redisCluster.Status.HistoryReasons

		//代表着某异常故障
	case RedisClusterFailed:

		var err error
		if endpoints == nil {
			endpoints, err = rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(context.TODO(),redisCluster.GetName(), metav1.GetOptions{})
			if err != nil {
				return redisCluster, fmt.Errorf("update redisCluster status -- get redis cluster endpoint is error: %v", err)
			}
		}

		tempStatus.Conditions = buildInstanceStatusCondition(redisCluster, endpoints)

		sts, err :=  rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			klog.Errorf(err.Error())
			return redisCluster, err
		}
		tempStatus.Replicas = sts.Status.ReadyReplicas
		tempStatus.Phase = RedisClusterFailed
		tempStatus.Reason = errors.Message(errStatus)
		tempStatus.ReasonType = errors.ReasonType(errStatus)
		tempStatus.HistoryReasons = redisCluster.Status.HistoryReasons
		//tempStatus.Conditions = redisCluster.Status.Conditions
		//代表着某异常故障
	case RedisClusterDeleting:
		sts, err :=  rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
				klog.Errorf(err.Error())
				return redisCluster, err
			}
			tempStatus.Replicas = 0
		} else {
			tempStatus.Replicas = sts.Status.ReadyReplicas
		}
		tempStatus.Phase = RedisClusterDeleting
		tempStatus.Reason = errors.Message(errStatus)
		tempStatus.ReasonType = errors.ReasonType(errStatus)
		tempStatus.Conditions = redisCluster.Status.Conditions
		tempStatus.HistoryReasons = redisCluster.Status.HistoryReasons
	case RedisClusterRollback:
		tempStatus.Replicas = redisCluster.Status.Replicas
		tempStatus.Phase = RedisClusterRollback
		tempStatus.Reason = errors.Message(errStatus)
		tempStatus.ReasonType = errors.ReasonType(errStatus)
		tempStatus.Conditions = redisCluster.Status.Conditions
		//ReasonUpdateId相同的则只保留最新的记录;通过最多只保留5条记录;避免过长
		deepCopyStatus := redisCluster.Status.DeepCopy()
		historyReasons := deepCopyStatus.HistoryReasons
		isFound := false
		for index := range historyReasons {
			if historyReasons[index].ReasonUpdateId == redisCluster.Spec.UpdateId {
				historyReasons[index].Reason = tempStatus.Reason
				historyReasons[index].LastTransitionTime = metav1.NewTime(time.Now())
				historyReasons[index].ReasonType = tempStatus.ReasonType
				historyReasons[index].Phase = tempStatus.Phase
				isFound = true
				break
			}
		}

		if !isFound {
			tempStatus.HistoryReasons = append(deepCopyStatus.HistoryReasons, Reason{
				ReasonUpdateId:     redisCluster.Spec.UpdateId,
				LastTransitionTime: metav1.NewTime(time.Now()),
				Reason:             tempStatus.Reason,
				ReasonType:         tempStatus.ReasonType,
				Phase:              tempStatus.Phase,
			})
		}
		sortReasonsByUpdateTime(&tempStatus.HistoryReasons)
		if len(tempStatus.HistoryReasons) > 5 {
			tempStatus.HistoryReasons = tempStatus.HistoryReasons[0:5]
		}

	case RedisClusterRestarting:
		rc, err := scaleFunc(&tempStatus)
		if err != nil {
			return rc, err
		}

		tempStatus.Phase = RedisClusterRestarting
		tempStatus.HistoryReasons = redisCluster.Status.HistoryReasons
	case RedisClusterRollbacking:
		tempStatus.Replicas = redisCluster.Status.Replicas
		tempStatus.Phase = RedisClusterRollbacking
		tempStatus.Reason = errors.Message(errStatus)
		tempStatus.ReasonType = errors.ReasonType(errStatus)
		tempStatus.Conditions = redisCluster.Status.Conditions
		tempStatus.HistoryReasons = redisCluster.Status.HistoryReasons
	}

	exporterName := redisCluster.Name + "-exporter"
	//获取所有实例地址
	exporterPod, err := rco.defaultClient.CoreV1().Pods(redisCluster.Namespace).Get(context.TODO(),exporterName+"-0", metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		} else {
			//不存在
			tempStatus.ExporterDomainName = redisCluster.Status.ExporterDomainName
			tempStatus.ExporterAddr = redisCluster.Status.ExporterAddr
		}
	}

	if exporterPod == nil || exporterPod.Status.PodIP == "" {
		tempStatus.ExporterDomainName = redisCluster.Status.ExporterDomainName
		tempStatus.ExporterAddr = redisCluster.Status.ExporterAddr
	} else {
		tempStatus.ExporterDomainName = fmt.Sprintf("%v.%v.%v.svc.%v", exporterPod.Name, exporterName, exporterPod.Namespace, rco.options.ClusterDomain)
		tempStatus.ExporterAddr = fmt.Sprintf("%v:%v", exporterPod.Status.PodIP, redisExporterPort19105)
	}

	// sort
	sort.SliceStable(tempStatus.Conditions, func(i, j int) bool {
		name1 := tempStatus.Conditions[i].Name
		name2 := tempStatus.Conditions[j].Name
		return name1 < name2
	})

	//判断新旧clusterConditions是否变化(除LastTransitionTime字段),变化才更新状态,避免更新频繁(其实只是LastTransitionTime变化了)
	if !util.NotUpdateRedisClusterStatus(tempStatus, redisCluster.Status) {

		//之前是否组成过集群
		if tempStatus.Phase == RedisClusterRunning || len(tempStatus.Conditions) != 0 {
			tempStatus.FormedClusterBefore = true
		} else {
			tempStatus.FormedClusterBefore = redisCluster.Status.FormedClusterBefore
		}

		//not necessary deep copy
		rcDeepCopy := redisCluster.DeepCopy()
		rcDeepCopy.Status = tempStatus

		//回滚前只生成修改后的rediscluster对象
		if isOnlyBuildRedisCluster {
			return rcDeepCopy, nil
		}

		//updateStatus只更新status部分,不更新其余部分
		//update只更新非status部分,不更新status部分
		//以上这两条只适用于k8s版本1.12以上
		err := rco.frameClient.Status().Update(context.TODO(),rcDeepCopy)
		newRedisCluster:= &RedisCluster{}
		rco.frameClient.Get(context.TODO(),types.NamespacedName{rcDeepCopy.Namespace,rcDeepCopy.Name},newRedisCluster)
		//newRedisCluster, err := rco.customCRDClient.CrV1alpha1().RedisClusters(rcDeepCopy.Namespace).UpdateStatus(rcDeepCopy)
		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", rcDeepCopy.Namespace, rcDeepCopy.Name, err)
			klog.Errorf(err.Error())
			return newRedisCluster, err
		}

		return newRedisCluster, nil
	}

	return redisCluster, nil
}

func buildInstanceStatusCondition(redisCluster *RedisCluster, endpoints *v1.Endpoints) []RedisClusterCondition {

	var statusConditions []RedisClusterCondition
	//所有实例都没Ready
	if endpoints.Subsets == nil || len(endpoints.Subsets[0].Addresses) == 0 {
		//创建失败
		if len(redisCluster.Status.Conditions) == 0 {
			statusConditions = redisCluster.Status.Conditions
		} else {
			//集群之前成功,现在失败,更新各个实例状态为失败
			for _, condition := range redisCluster.Status.Conditions {
				condition.Status = v1.ConditionFalse
				statusConditions = append(statusConditions, condition)
			}
		}
	} else {
		//部分实例ready
		for _, condition := range redisCluster.Status.Conditions {
			isFound := false
			for _, addr := range endpoints.Subsets[0].Addresses {
				if addr.TargetRef.Name == condition.Name {
					isFound = true
					break
				}
			}
			//endpoint addr里没有地址则更新为false
			if isFound {
				condition.Status = v1.ConditionTrue
			} else {
				condition.Status = v1.ConditionFalse
			}
			statusConditions = append(statusConditions, condition)
		}
	}
	return statusConditions
}

//构造redisCluster的状态Condition信息
//redisCluster：redisCluster对象
//endpoints：endpoint信息
func (rco *RedisClusterOperator) buildRedisClusterStatusConditions(redisCluster *RedisCluster, endpoints *v1.Endpoints) ([]RedisClusterCondition, error) {

	//TODO endpoint信息不全导致部分实例的condition中信息缺失
	if endpoints.Subsets == nil || len(endpoints.Subsets[0].Addresses) == 0 {
		err := fmt.Errorf("redisCluster: %v/%v endpoints is blank", redisCluster.Namespace, redisCluster.Name)
		return nil, err
	}

	//kubelet配置文件里的域名,默认cluster.local
	suffixDomain := rco.options.ClusterDomain

	address := endpoints.Subsets[0].Addresses

	var nodeInfos []*redisNodeInfo
	var err error
	if len(address) == 2 {
		nodeInfos, err = rco.execMSNodes(address[0].IP, address[0].TargetRef.Namespace, address[0].TargetRef.Name)
	} else {
		nodeInfos, err = rco.execClusterNodes(address[0].IP, address[0].TargetRef.Namespace, address[0].TargetRef.Name, false)
	}

	if err != nil {
		return nil, err
	}

	//查k8s node信息
	nodeList, err := rco.defaultClient.CoreV1().Nodes().List(context.TODO(),metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	//key nodeName value nodeIP
	nodeInfoMap := make(map[string]string, len(nodeList.Items))
	for _, node := range nodeList.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				nodeInfoMap[node.Name] = addr.Address
			}
		}
	}

	//pod信息,key podIP , value type:instanceInfo
	instanceInfoMap := make(map[string]*instanceInfo, len(address))
	for _, addr := range address {
		info := &instanceInfo{}
		info.HostName = addr.TargetRef.Name
		info.NodeName = *addr.NodeName
		info.DomainName = fmt.Sprintf("%v.%v.%v.svc.%v", addr.TargetRef.Name, endpoints.Name, addr.TargetRef.Namespace, suffixDomain)
		info.InstanceIP = addr.IP
		info.HostIP = nodeInfoMap[info.NodeName]
		instanceInfoMap[addr.IP+":6379"] = info
	}

	existedInstanceInfoMap := make(map[string]RedisClusterCondition, len(redisCluster.Status.Conditions))
	for _, existedCon := range redisCluster.Status.Conditions {
		existedInstanceInfoMap[existedCon.Instance] = existedCon
	}

	var conditions []RedisClusterCondition

	for _, info := range nodeInfos {

		condition := RedisClusterCondition{}
		condition.NodeId = info.NodeId
		condition.Instance = info.IpPort
		condition.Status = v1.ConditionTrue
		if masterFlagType == info.Flags {
			condition.Type = MasterConditionType
		} else if slaveFlagType == info.Flags {
			condition.Type = SlaveConditionType
		} else {
			condition.Status = v1.ConditionFalse
		}

		condition.Slots = info.Slot

		condition.MasterNodeId = info.Master

		condition.Namespace = redisCluster.Namespace
		condition.Reason = ""
		now := metav1.NewTime(time.Now())
		condition.LastTransitionTime = now
		condition.Message = ""

		instanceInfo := instanceInfoMap[condition.Instance]
		if instanceInfo != nil {
			condition.Name = instanceInfo.HostName
			condition.Hostname = instanceInfo.NodeName
			condition.DomainName = instanceInfo.DomainName
			condition.HostIP = instanceInfo.HostIP
		} else {
			//如果没有该实例的endpoint信息,则使用旧的
			condition = existedInstanceInfoMap[condition.Instance]
			condition.Status = v1.ConditionFalse
		}

		conditions = append(conditions, condition)
	}

	return conditions, nil
}

//构造Service
//namespace：service所在ns
//name：service name
func (rco *RedisClusterOperator) buildRedisClusterService(redisCluster *RedisCluster, namespace, name string) *v1.Service {

	var servicePort int32 = redisServicePort6379
	if redisCluster.Spec.ServicePort != 0 {
		servicePort = redisCluster.Spec.ServicePort
	}
	tempRedisCluster := redisCluster.DeepCopy()

	podTpl := tempRedisCluster.Spec.Pod[0]

	podLabels := podTpl.Labels
	if podLabels == nil {
		podLabels = make(map[string]string, 1)

	}
	podLabels = MergeLabels(podLabels, generateSelectorLabels(redisCluster.Name, name))

	flagTrue := true
	//build service
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
			//ClusterIP: v1.ClusterIPNone,
			Ports: []v1.ServicePort{
				{
					Name: name,
					Port: servicePort,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: int32(redisServicePort6379),
					},
				},
				{
					Name: "redis-exporter",
					Port: exporterPort,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: int32(exporterPort),
					},
				},
			},

			Selector: MergeLabels(podLabels, generateSelectorLabels(redisCluster.Name, name)),
		},
	}
}

//构造Statefulset
//namespace：Statefulset所在ns
//name：Statefulset name
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) buildRedisClusterStatefulset(namespace, name string, redisCluster *RedisCluster) *appsv1.StatefulSet {

	tempRedisCluster := redisCluster.DeepCopy()

	//create statefulset
	revisionHistoryLimit := int32(10)
	defaultMode := int32(420)
	k8sClusterDomainName := "cluster.local"
	pathPrefix := "/data"
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

	podTpl := tempRedisCluster.Spec.Pod[0]

	containerEnv = append(containerEnv, podTpl.Env...)
	podLabels := podTpl.Labels
	if podLabels == nil {
		podLabels = make(map[string]string, 1)
	}
	podLabels = MergeLabels(podLabels, generateSelectorLabels(redisCluster.Name, name))

	//拷贝map
	stsLabel := make(map[string]string)
	labelJson, _ := json.Marshal(podLabels)
	_ = json.Unmarshal(labelJson, &stsLabel)

	annos := podTpl.Annotations
	if annos == nil {
		annos = make(map[string]string)
	}
	annos[MiddlewareRedisTypeKey] = MiddlewareRedisClustersPrefix + name
	//annos[pauseKey] = strconv.FormatBool(tempRedisCluster.Spec.Pause)

	flagTrue := true

	var stsUpdateStrategy appsv1.StatefulSetUpdateStrategy
	if len(podTpl.UpdateStrategy.Type) == 0 {
		//stsUpdateStrategy.Type = appsv1.OnDeleteStatefulSetStrategyType
		stsUpdateStrategy.Type = appsv1.RollingUpdateStatefulSetStrategyType
	} else {
		stsUpdateStrategy = podTpl.UpdateStrategy
	}

	initContainerLimits := map[v1.ResourceName]resource.Quantity{
		//50m
		v1.ResourceCPU: resource.MustParse("50m"),
		//100Mi
		v1.ResourceMemory: resource.MustParse("100Mi"),
	}

	/*exporterContainerLimits := map[v1.ResourceName]resource.Quantity{
		//100m
		v1.ResourceCPU: resource.MustParse("100m"),
		//200Mi
		v1.ResourceMemory: resource.MustParse("200Mi"),
	}*/

	//pod管理策略
	podManagePolicy := appsv1.OrderedReadyPodManagement
	if tempRedisCluster.Spec.PodManagementPolicy != "" {
		podManagePolicy = tempRedisCluster.Spec.PodManagementPolicy
	}

	volumeClaimTemplates := []v1.PersistentVolumeClaim{}
	//volumeClaimTemplates不为空时，sts 使用stoageclasss
	if tempRedisCluster.Spec.VolumeClaimTemplates != nil {
		volumeClaimTemplates = []v1.PersistentVolumeClaim{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: tempRedisCluster.Spec.VolumeClaimTemplates[0].ObjectMeta.Name,
				},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{
						tempRedisCluster.Spec.VolumeClaimTemplates[0].Spec.AccessModes[0],
					},
					StorageClassName: tempRedisCluster.Spec.VolumeClaimTemplates[0].Spec.StorageClassName,
					Resources: v1.ResourceRequirements{
						Requests: tempRedisCluster.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests,
					},
				},
			},
		}
	}
	if len(volumeClaimTemplates)==0{

	}

	redisConfigVolume := v1.Volume{
		Name: "redis-config",
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				DefaultMode: &defaultMode,
				LocalObjectReference: v1.LocalObjectReference{
					Name: tempRedisCluster.Name + "-config",
				},
			},
		},
	}

	// pvc 不为空，sts 使用pvc
	volume := v1.Volume{}
	if tempRedisCluster.Spec.Volumes != (RedisClusterPodVolume{}) {
		volume = v1.Volume{
			Name: "redis-data",
			VolumeSource: v1.VolumeSource{
				PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
					ClaimName: tempRedisCluster.Spec.Volumes.PersistentVolumeClaimName,
				},
			},
		}
	}

	volumes := []v1.Volume{}
	if volume != (v1.Volume{}) {
		volumes = []v1.Volume{
			redisConfigVolume,
			volume,
		}
	} else {
		volumes = []v1.Volume{
			redisConfigVolume,
		}
	}

	dnsPolicy := v1.DNSClusterFirst
	hostNetwork := false
	if tempRedisCluster.Spec.Pod[0].HostNetwork {
		dnsPolicy = v1.DNSClusterFirstWithHostNet
		hostNetwork = true
	}

	recSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
			//并行启动redis pod,需要在纵向扩容时通过OnDelete策略控制pod更新顺序
			PodManagementPolicy:  podManagePolicy,
			UpdateStrategy:       stsUpdateStrategy,
			Replicas:             tempRedisCluster.Spec.Replicas,
			RevisionHistoryLimit: &revisionHistoryLimit,
			VolumeClaimTemplates: volumeClaimTemplates,
			//StatefulSetSpec下只能更新UpdateStrategy、Template、Replicas,其他都禁止更新
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":       name,
					"component": name,
				},
			},
			//serviceName和redisCluster一样
			ServiceName: tempRedisCluster.Name,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: annos,
				},
				Spec: v1.PodSpec{
					HostNetwork: hostNetwork,
					DNSPolicy:   dnsPolicy,
					Containers: []v1.Container{
						{
							Command:         []string{"/bin/redis-server", fmt.Sprintf("%v/redis/%v/%v/%v/$(POD_NAME)/config/redis.conf", pathPrefix, k8sClusterDomainName, namespace, name)},
							Image:           tempRedisCluster.Spec.Repository + podTpl.MiddlewareImage,
							ImagePullPolicy: tempRedisCluster.Spec.Pod[0].ImagePullPolicy,
							Env:             containerEnv,
							Name:            redisContainerName,
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
							//用于纵向扩容时检查集群状态
							ReadinessProbe: &v1.Probe{
								Handler: v1.Handler{
									Exec: &v1.ExecAction{
										Command: []string{"/bin/bash", "-c", "/data/redis-health.sh"},
									},
								},
								InitialDelaySeconds: int32(5),
								TimeoutSeconds:      int32(20),
								PeriodSeconds:       int32(5),
								SuccessThreshold:    int32(1),
								FailureThreshold:    int32(3),
							},
							Resources: podTpl.Resources,
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "redis-data",
									MountPath: fmt.Sprintf("%v/redis/%v/%v/%v", pathPrefix, k8sClusterDomainName, namespace, name),
								},
							},
						},
					},
					InitContainers: []v1.Container{
						{
							Command:         []string{"/bin/sh", "-c", "sh /init.sh"},
							Image:           tempRedisCluster.Spec.Repository + podTpl.InitImage,
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
									MountPath: "/config",
								},
							},
						},
					},
					RestartPolicy: v1.RestartPolicyAlways,
					Affinity:      podTpl.Affinity,
					Volumes: volumes,
				},
			},
		},
	}
	exporter := createExporterContainer(redisCluster, recSts.Name, false)
	recSts.Spec.Template.Spec.Containers = append(recSts.Spec.Template.Spec.Containers, exporter)
	return recSts
}

//新建redis集群创建master
//masterInstanceIps：master节点pod ip
//reference：endpoint里address[0]信息,主要获取podName和namespace用于执行redis-trib.rb命令
func (rco *RedisClusterOperator) createCluster(masterInstanceIps []string, reference *v1.ObjectReference) error {

	/*password, isExistPassword := loadRedisClusterPassword(reference.Namespace, reference.Name)
	if !isExistPassword {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", reference.Namespace, reference.Name)
	}
	replcaeRedisTribClientRbPwdCmd := rco.replaceRedisTribClientRbPwd(reference.Namespace, reference.Name, password)*/

	password, isExistPassword := rco.loadRedisClusterPassword(reference.Namespace, reference.Name)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", reference.Namespace, reference.Name)
		pwdArgs = ""
	}

	var buf bytes.Buffer

	buf.WriteString("echo yes | redis-cli --cluster create " + pwdArgs + " ")

	for i := 0; i < len(masterInstanceIps); i++ {
		buf.WriteString(masterInstanceIps[i])
		buf.WriteString(":6379")
		buf.WriteString(" ")
	}

	//createMasterCommand := fmt.Sprintf("echo yes | redis-cli --cluster create %v:6379 %v:6379 %v:6379", masterInstance[0], masterInstance[1], masterInstance[2])
	createMasterCommand := buf.String()
	klog.Infof("create createMasterCommand is: %v ", createMasterCommand)

	commandMaster := []string{"/bin/sh", "-c", createMasterCommand}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, redisContainerName, reference.Name, reference.Namespace, nil)

	if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
		err := fmt.Errorf("exec create Master Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", commandMaster, stdout, stderr, err)
		klog.Errorf(err.Error())
		return err
	}

	klog.Infof("create createMaster stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)
	return nil
}

//给redis集群加入新slave
//slaveInstanceIps：需要加为slave的pod实例ip列表
//masterInstanceIps：需要加slave的master pod实例ip列表
//masterInstanceNodeIds：需要加slave的master nodeId列表
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) addSlaveToClusterMaster(slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds []string, namespace, podName string, successAddInstances *[]string) error {

	if len(slaveInstanceIps) != len(masterInstanceIps) || len(masterInstanceIps) != len(masterInstanceNodeIds) {
		return fmt.Errorf("add Slave To ClusterMaster check len error, slaveInstanceIps: %v, masterInstanceIps: %v, masterInstanceNodeIds: %v", slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds)
	}

	//给新建集群的master加入slave
	//redis-cli --cluster add-node 10.168.78.82:6379 10.168.78.83:6379 --cluster-slave --cluster-master-id 1d91acc0
	for i := 0; i < len(slaveInstanceIps); i++ {

		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(masterInstanceIps[0], podName, namespace)
		if err != nil {
			return err
		}

		password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
		pwdArgs := fmt.Sprintf(redisPasswordArg, password)
		if !isExistPassword || password == "" {
			klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
			pwdArgs = ""
		}

		addSlaveCommand := fmt.Sprintf(" redis-cli --cluster add-node %v:6379 %v:6379 --cluster-slave --cluster-master-id %v %v ", slaveInstanceIps[i], masterInstanceIps[i], masterInstanceNodeIds[i], pwdArgs)
		commandSlave := []string{"/bin/sh", "-c", addSlaveCommand}
		klog.Infof("add slave cmd: %v", addSlaveCommand)
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandSlave, redisContainerName, podName, namespace, nil)
		if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
			err := fmt.Errorf("redisCluster: %v/%v -- add new slave to cluster is error -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, stdout, stderr, err)
			klog.Errorf(err.Error())
			return err
		}
		*successAddInstances = append(*successAddInstances, slaveInstanceIps[i])
		klog.V(4).Infof("redisCluster: %v/%v add new slave to cluster: -- \nstdout: %v", namespace, podName, stdout)
	}

	return nil
}

//添加master节点
//newMasterInstanceIPs:新master pod实例ip
//existInstanceIp：redis集群中已存在的实例ip
//podName: pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在ns
func (rco *RedisClusterOperator) addMasterNodeToRedisCluster(newMasterInstanceIPs []string, existInstanceIp, podName, namespace string, successAddInstances *[]string) error {

	for i := 0; i < len(newMasterInstanceIPs); i++ {

		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(existInstanceIp, podName, namespace)
		if err != nil {
			return err
		}

		password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
		pwdArgs := fmt.Sprintf(redisPasswordArg, password)
		if !isExistPassword || password == "" {
			klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
			pwdArgs = ""
		}

		addMasterCmd := fmt.Sprintf(" redis-cli --cluster add-node %v:6379 %v:6379 %v ", newMasterInstanceIPs[i], existInstanceIp, pwdArgs)

		klog.Infof("redisCluster: %v/%v -- add new master to cluster cmd: %v", namespace, podName, addMasterCmd)
		commandMaster := []string{"/bin/sh", "-c", addMasterCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, redisContainerName, podName, namespace, nil)

		if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
			err = fmt.Errorf("redisCluster: %v/%v -- add new master to cluster is error -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, stdout, stderr, err)
			klog.Errorf(err.Error())
			return err
		}

		//添加成功的节点IP
		*successAddInstances = append(*successAddInstances, newMasterInstanceIPs[i])

		klog.V(4).Infof("redisCluster: %v/%v add new master to cluster: -- \nstdout: %v", namespace, podName, stdout)
	}

	return nil
}

// rebalance自动分配卡槽
// redis-cli --cluster  rebalance --cluster-use-empty-masters 1.1.1.1:6379
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, podName, namespace, crdPipeline string) error {

	//将阻塞-->>检查集群状态是否ok
	err := rco.waitRedisStatusOK(existInstanceIp, podName, namespace)
	if err != nil {
		return err
	}

	pipeline := defaultPipeline
	if crdPipeline != "" {
		pipeline = crdPipeline
	}

	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	rebalanceCmd := fmt.Sprintf(" redis-cli --cluster rebalance --cluster-use-empty-masters --cluster-pipeline %v %v:6379 %v ", pipeline, existInstanceIp, pwdArgs)
	commandRebalance := []string{"/bin/sh", "-c", rebalanceCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandRebalance, redisContainerName, podName, namespace, nil)

	if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
		err := fmt.Errorf("redisCluster: %v/%v -- rebalanceCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, commandRebalance, stdout, stderr, err)
		klog.Errorf(err.Error())
		return err
	}

	klog.V(4).Infof("redisCluster: %v/%v rebalanceCmd: %v \n -- \nstdout: %v", namespace, podName, commandRebalance, stdout)

	return nil
}

//reshare手动调整卡槽
//redis-cli --cluster reshard --cluster-from c49a3b06ad93638037d56855ff702787ad16e3ea --cluster-to 174ad1122349c33c475dcbd54489ea847ad8474f --cluster-slots 100 --cluster-yes 10.168.78.119:6379
//assignStrategies：reshare卡槽分配策略
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) reshareRedisClusterSlotsToMasterNode(assignStrategies RedisClusterUpdateStrategy, existInstanceIp, podName, namespace string, newMasterNodeIds []string) error {

	strategies := assignStrategies.AssignStrategies
	crdPipeline := assignStrategies.Pipeline
	pipeline := defaultPipeline
	if crdPipeline != "" {
		pipeline = crdPipeline
	}

	for i, nodeId := range newMasterNodeIds {

		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(existInstanceIp, podName, namespace)
		if err != nil {
			return err
		}

		password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
		pwdArgs := fmt.Sprintf(redisPasswordArg, password)
		if !isExistPassword || password == "" {
			klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
			pwdArgs = ""
		}

		reshareCmd := fmt.Sprintf(" redis-cli --cluster reshard %v:6379 --cluster-from %v --cluster-to %v --cluster-slots %v --cluster-pipeline %v --cluster-yes %v ", existInstanceIp, strategies[i].FromReplicas, nodeId, *strategies[i].Slots, pipeline, pwdArgs)
		commandReshare := []string{"/bin/sh", "-c", reshareCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandReshare, redisContainerName, podName, namespace, nil)

		if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
			err := fmt.Errorf("redisCluster: %v/%v -- reshareCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v \n", namespace, podName, commandReshare, stdout, stderr, err)
			klog.Errorf(err.Error())
			return err
		}
		klog.V(4).Infof("redisCluster: %v/%v reshareCmd: %v \n -- \nstdout: %v", namespace, podName, commandReshare, stdout)
	}

	return nil
}

//执行redis-cli --cluster check 1.1.1.1:6379检查集群状态是否ok,这里将阻塞,最多rco.options.ClusterTimeOut分钟
//等待检查redis集群状态ok
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) waitRedisStatusOK(existInstanceIp, podName, namespace string) error {
	f := wait.ConditionFunc(func() (bool, error) {

		isOK, err := rco.execCheckRedisCluster(existInstanceIp, podName, namespace)

		if err != nil {
			return false, fmt.Errorf("check redis cluster is error: %v", err)
		}

		if !isOK {
			return false, nil
		}

		return true, nil
	})

	timeout := rco.options.ClusterTimeOut
	//2s check一次,看集群各节点node状态是否同步,if return true,开始执行后续逻辑;
	// 否则继续检查,总共time.Duration(timeout)分钟后超时,更新redisCluster状态
	err := wait.Poll(2*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//检查集群状态一直不成功-->>超时
		err := fmt.Errorf("check redisCluster: %v/%v status error: %v", namespace, podName, err)
		klog.Errorf(err.Error())
		return err
	}
	return nil
}

//检查redis集群状态
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execCheckRedisCluster(existInstanceIp, podName, namespace string) (bool, error) {
	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	//checkCmd := fmt.Sprintf(" %v ; redis-cli --cluster check %v:6379 ", replcaeRedisTribClientRbPwdCmd, existInstanceIp)
	checkCmd := fmt.Sprintf(" redis-cli --cluster check %v:6379 %v ", existInstanceIp, pwdArgs)
	commandCheck := []string{"/bin/sh", "-c", checkCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandCheck, redisContainerName, podName, namespace, nil)

	//redis-cli检查的状态包含[ERR]时,err != nil
	if strings.Contains(stdout, "[ERR]") {
		return false, nil
	}

	if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
		err := fmt.Errorf("redisClusterInstance: %v/%v -- checkCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, commandCheck, stdout, stderr, err)
		klog.Errorf(err.Error())
		return false, err
	}

	klog.V(4).Infof("redisClusterInstance: %v/%v checkCmd: %v \n -- \nstdout: %v", namespace, podName, commandCheck, stdout)
	return true, nil
}

//获取当前redisCluster实例信息,和新加的节点master和slave信息
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) currentRedisClusterInfo(redisCluster *RedisCluster, endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]string, error) {
	//升级之前的集群IP列表和nodeId列表
	var existInstanceIps []string
	oldAddresses := oldEndpoints.Subsets[0].Addresses
	addresses := endpoints.Subsets[0].Addresses
	if len(addresses) == 0 || len(oldAddresses) == 0 {
		return nil, fmt.Errorf("endpoints.Subsets.addresses is empty, maybe statefulset %v/%v all replicas not ready", redisCluster.Namespace, redisCluster.Name)
	}

	for _, addr := range oldAddresses {
		//existInstanceNames = append(existInstanceNames, addr.TargetRef.Name)
		existInstanceIps = append(existInstanceIps, addr.IP)
	}

	return existInstanceIps, nil
}

//升级集群时加master和slave
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) scaleRedisCluster(redisCluster *RedisCluster, endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) (existInstanceIps []string, newMasterNodeIds []string, err error) {

	var successAddInstances []string

	/*defer func() {
		err = rco.handleUpgradeRedisClusterError(err, successAddInstances, existInstanceIps[0], redisCluster, endpoints)
	}()*/

	//获取已有redis集群信息
	addresses := endpoints.Subsets[0].Addresses
	//endpoint里pod信息
	reference := addresses[0].TargetRef
	existInstanceIps, err = rco.currentRedisClusterInfo(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		//更新状态为Running以及更新当前error信息
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
		return nil, nil, err
	}

	//查询当前集群的节点信息
	nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], reference.Namespace, reference.Name, true)
	if err != nil {
		//更新状态为Running以及更新当前error信息
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
		return nil, nil, err
	}

	//TODO 升级时IP分配需要参考升级前的,防止分配结果不符合master、slave IP分配要求
	// 获取新增的master、slave IP集合、下标为m->s对应关系
	newMasterInstanceIPs, newSlaveInstanceIPs, _, err := rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, oldEndpoints)
	if err != nil {
		//更新状态为Running以及更新当前error信息
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
		return nil, nil, err
	}

	rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "AddMaster", "hScaling:addMaster:Cluster %v is adding master", redisCluster.Name)

	//2、加入新master
	err = rco.addMasterNodeToRedisCluster(newMasterInstanceIPs, existInstanceIps[0], reference.Name, reference.Namespace, &successAddInstances)
	if err != nil {
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "AddMasterFailed", "hScaling:addMasterFailed:Cluster %v adds master failed: %v", redisCluster.Name, err)
		//更新状态为Running以及更新当前error信息
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewAddMasterFailedWhenUpgradeRedisCluster(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeAddMasterFailedWhenUpgradeCluster, successAddInstances, existInstanceIps)
		return nil, nil, err
	}

	//3、加入新master后查询当前集群节点信息,主要是获取新master节点ID
	nodeInfos, err = rco.execClusterNodes(existInstanceIps[0], reference.Namespace, reference.Name, true)
	if err != nil {
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
		return nil, nil, err
	}

	// 根据newMasterInstanceIPs拿nodeId,下标对应
	for _, masterInstanceIp := range newMasterInstanceIPs {
		for _, info := range nodeInfos {
			if strings.Contains(info.IpPort, masterInstanceIp) {
				newMasterNodeIds = append(newMasterNodeIds, info.NodeId)
				break
			}
		}
	}

	if len(newMasterNodeIds) != len(newMasterInstanceIPs) {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
		return nil, nil, err
	}

	rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "AddSlave", "hScaling:addSlave:Cluster %v is adding slave", redisCluster.Name)
	//4、给新master加slave
	err = rco.addSlaveToClusterMaster(newSlaveInstanceIPs, newMasterInstanceIPs, newMasterNodeIds, reference.Namespace, reference.Name, &successAddInstances)
	if err != nil {
		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "AddSlave", "hScaling:addSlaveFailed:Cluster %v adds slave failed: %v", redisCluster.Name, err)
		//rco.updateRedisClusterStatus(redisCluster, endpoints, errors.NewAddSlaveFailedWhenUpgradeCluster(err.Error(), RedisClusterRunning), false)
		err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeAddSlaveFailedWhenUpgradeCluster, successAddInstances, existInstanceIps)
		return nil, nil, err
	}

	return existInstanceIps, newMasterNodeIds, nil
}

func (rco *RedisClusterOperator) handleUpgradeRedisClusterError(err error, redisCluster *RedisCluster, endpoints *v1.Endpoints) error {

	if endpoints == nil || len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		err := fmt.Errorf("redisCluster: %v/%v endpoints is blank", redisCluster.Namespace, redisCluster.Name)
		klog.Error(err)
		return err
	}

	//复制err,避免被覆盖
	copyErr := err

	//卡槽迁移失败只更新结果为失败
	/*if errors.IsRebalanceSlotsFailed(copyErr) || errors.IsReshareSlotsFailed(copyErr) {
		_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, copyErr, false)
		return nil
	}*/

	rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "UpgradeClusterFailed", "hScaling:hScalingClusterFailed:Cluster %v is upgrade failed: %v", redisCluster.Name, err.Error())
	_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, copyErr, false)
	return nil

	//TODO 删除后面的逻辑
	//获取已有redis集群信息
	addresses := endpoints.Subsets[0].Addresses
	//endpoint里pod信息
	reference := addresses[0].TargetRef
	//删除节点
	delNode := func(successAddInstances, existInstanceIps []string) error {
		//节点node信息
		nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], reference.Namespace, reference.Name, false)
		if err != nil {
			return err
		}

		//已加成功的节点nodeId
		var successNodeIds []string
		for _, addedInstance := range successAddInstances {
			for _, nodeInfo := range nodeInfos {
				if strings.HasPrefix(nodeInfo.IpPort, addedInstance) {
					successNodeIds = append(successNodeIds, nodeInfo.NodeId)
					break
				}
			}
		}

		klog.V(3).Infof("redisCluster: %v/%v delete nodes: %v", redisCluster.Namespace, redisCluster.Name, successNodeIds)
		//forget节点
		//err = rco.delNodeFromRedisCluster(reference.Name, reference.Namespace, successNodeIds, existInstanceIps, redisCluster.Spec.RedisInstancePort)
		err = rco.forgetNodeFromRedisCluster(reference.Name, reference.Namespace, successNodeIds, existInstanceIps)
		if err != nil {
			return err
		}
		return nil
	}

	klog.V(3).Infof("redisCluster: %v/%v rollback start", redisCluster.Namespace, redisCluster.Name)

	if errors.IsRedisClusterReasonStatus(copyErr) {

		//如果存在已经加成功的节点,则删除节点
		addedIps := errors.SuccessAddInstances(copyErr)
		existInstanceIps := errors.ExistInstanceIps(copyErr)
		klog.V(3).Infof("redisCluster: %v/%v rollback addedIps: %v existInstanceIps: %v", redisCluster.Namespace, redisCluster.Name, addedIps, existInstanceIps)
		if len(addedIps) != 0 && len(existInstanceIps) != 0 {

			//1. 先删除数据
			//获取加成功节点的podName
			var successPodNames []string
			for _, addedInstance := range addedIps {
				for _, addr := range addresses {
					if strings.EqualFold(addr.IP, addedInstance) {
						successPodNames = append(successPodNames, addr.TargetRef.Name)
						break
					}
				}
			}

			klog.V(3).Infof("redisCluster: %v/%v delete data: %v", redisCluster.Namespace, redisCluster.Name, successPodNames)
			//删除数据
			err = rco.deleteDataByJobs(redisCluster, successPodNames)
			if err != nil {
				return err
			}

			//2.停掉实例
			set, err :=  rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})

			if err != nil {
				return err
			}

			sts := set.DeepCopy()

			rep := int32(6)
			sts.Spec.Replicas = &rep
			_, err = rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Update(context.TODO(),sts,metav1.UpdateOptions{})
			if err != nil {
				return err
			}

			time.Sleep(time.Duration(10 * time.Second))

			//3.forget加成功的节点
			err = delNode(addedIps, existInstanceIps)
			if err != nil {
				klog.Errorf("redisCluster: %v/%v delete nodes %v error: %v", redisCluster.Namespace, redisCluster.Name, addedIps, err)
				return err
			}
		}
	}

	//其他失败直接回滚
	klog.V(3).Infof("redisCluster: %v/%v rollback", redisCluster.Namespace, redisCluster.Name)

	//回滚到之前的版本
	//纵向扩容失败
	err = rco.rollback(endpoints, copyErr, redisCluster, nil)
	if err != nil {
		klog.Errorf("upgrade redisCluster: %v/%v rollback error: %v", redisCluster.Namespace, redisCluster.Name, err)
		return err
	}
	/*newRcs, err := rco.updateRedisClusterStatus(redisCluster, endpoints, err, true)
	if err != nil {
		return err
	}
	_, old, err := rco.constructHistory(redisCluster)
	if err != nil {
		return err
	}

	_, maxControllerRevision := maxRevision(old)
	rcs, err := controllerRevisionToRedisClusterSpec(maxControllerRevision)
	if err != nil {
		return err
	}
	newRcs.Spec = *rcs
	//更新到上一个controller revision版本
	_, err = rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Update(newRcs)*/
	return nil
}

//级联删除statefulset、service、cr
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) dropRedisCluster(redisCluster *RedisCluster) error {
	var err error

	rco.eventRecorder.LabeledEventf(redisCluster, record.DeletingEvent, v1.EventTypeNormal, "DeleteData", "deleting:deleteData:Cluster %v is deleting data", redisCluster.Name)
	//删除redis集群全部数据
	err = rco.deleteDataByJobs(redisCluster, nil)
	if err != nil {
		return err
	}

	foreGround := metav1.DeletePropagationForeground

	rco.eventRecorder.LabeledEventf(redisCluster, record.DeletingEvent, v1.EventTypeNormal, "DeletePod", "deleting:deletePod:Cluster %v is deleting pod", redisCluster.Name)
	//删除cr级联删除statefulset、service
	err = rco.frameClient.Delete(context.TODO(),redisCluster, &client2.DeleteOptions{PropagationPolicy: &foreGround})
	//err = rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Delete(redisCluster.Name, options)

	if err != nil {
		klog.Errorf("Drop RedisCluster: %v/%v error: %v", redisCluster.Namespace, redisCluster.Name, err)
		return err
	}

	//最后确保删除map里的stopWait标志位
	defer redisClusterStopWait.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))

	rco.eventRecorder.LabeledEventf(redisCluster, record.DeletingEvent, v1.EventTypeNormal, "DeleteResources", "deleting:deleteResources:Cluster %v is deleting relate resources", redisCluster.Name)
	//如果pvc没有删除掉,则删除
	err = rco.defaultClient.CoreV1().PersistentVolumeClaims(redisCluster.Namespace).Delete(context.TODO(),fmt.Sprintf("%v%v", redisCluster.Name, redisClusterPvcSuffix), metav1.DeleteOptions{})

	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Drop RedisCluster: %v/%v pvc error: %v", redisCluster.Namespace, redisCluster.Name, err)
			return err
		}
	}

	//如果pv没有删除掉,则删除
	err = rco.defaultClient.CoreV1().PersistentVolumes().Delete(context.TODO(),fmt.Sprintf("%v%v", redisCluster.Name, redisClusterPvSuffix), metav1.DeleteOptions{})

	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Drop RedisCluster: %v/%v pv error: %v", redisCluster.Namespace, redisCluster.Name, err)
			return err
		}
	}

	//如果configmaps没有删除掉,则删除
	err = rco.defaultClient.CoreV1().ConfigMaps(redisCluster.Namespace).Delete(context.TODO(),fmt.Sprintf("%v%v", redisCluster.Name, redisClusterConfigMapsSuffix), metav1.DeleteOptions{})

	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Drop RedisCluster: %v/%v configmaps error: %v", redisCluster.Namespace, redisCluster.Name, err)
			return err
		}
	}

	return nil
}

//exec进入pod执行删除数据操作
func (rco *RedisClusterOperator) deleteData(redisCluster *RedisCluster, specificPodNames []string, podNodeMap map[string]string, k8sClusterDomainName, pathPrefix string) ([]string, error) {

	var (
		errs            []error
		mu              = sync.Mutex{}
		needJobsDelPods []string
		podMu           = sync.Mutex{}
		wg              = sync.WaitGroup{}
	)

	appendError := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}

	appendFailedDelPods := func(podName string) {
		podMu.Lock()
		defer podMu.Unlock()
		needJobsDelPods = append(needJobsDelPods, podName)
	}

	delFunc := func(podName string) {
		command := fmt.Sprintf("  rm -rf %v ", fmt.Sprintf("%v/redis/%v/%v/%v/%v", pathPrefix, k8sClusterDomainName, redisCluster.Namespace, redisCluster.Name, podName))
		deleteDataCmd := []string{"/bin/sh", "-c", command}

		stdout, stderr, err := rco.ExecToPodThroughAPI(deleteDataCmd, redisContainerName, podName, redisCluster.Namespace, nil)
		if err != nil || stderr != "" {
			err = fmt.Errorf("delete RedisCluster: %v/%v data deleteDataCmd: %v error: %v, stdout: %v, stderr: %v", redisCluster.Namespace, redisCluster.Name, deleteDataCmd, err, stdout, stderr)
			klog.Warningf(err.Error())
			appendError(err)
			appendFailedDelPods(podName)
		}
	}

	//起线程执行
	if len(specificPodNames) == 0 {
		for podName := range podNodeMap {
			wg.Add(1)
			go func(podName string) {
				defer wg.Done()
				delFunc(podName)
			}(podName)
		}
	} else {
		for _, name := range specificPodNames {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				delFunc(name)
			}(name)
		}
	}
	wg.Wait()

	if len(errs) != 0 {
		err := fmt.Errorf("delete redisCluster: %v/%v -- %v data is error: %v", redisCluster.Namespace, redisCluster.Name, needJobsDelPods, errs)
		//TODO 更新状态回滚或者删除集群失败?
		klog.Warning(err.Error())
		//rco.updateRedisClusterStatus()
		return needJobsDelPods, err
	}

	return nil, nil
}

//起job pod执行删除数据操作
func (rco *RedisClusterOperator) deleteDataByJobs(redisCluster *RedisCluster, specificPodNames []string) error {

	//生成Selector
	label := labels.SelectorFromSet(labels.Set(map[string]string{"app": redisCluster.Name}))

	//TODO 在endpoints里取address、notReadyAddress(在pod creating状态时endpoint里没有记录)取pod的nodeName,但pod如果没起来可能取不到
	//查找sts pod
	pods, err := stsutil.GetPodsForStatefulSet(rco.defaultClient, redisCluster.Namespace, label)
	if err != nil {
		return err
	}

	//已删除
	if len(pods) == 0 {
		return nil
	}

	var pathPrefix, k8sClusterDomainName string
	for _, env := range redisCluster.Spec.Pod[0].Env {
		if env.Name == envDomainNameKey {
			k8sClusterDomainName = env.Value
		} else if env.Name == envPathPrefixKey {
			pathPrefix = env.Value
		}
	}

	if len(pathPrefix) == 0 {
		pathPrefix = defaultPathPrefix
	}

	var (
		errs []error
		mu   = sync.Mutex{}
		wg   = sync.WaitGroup{}
	)

	appendError := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}

	//podName和nodeName的对应关系
	podNodeMap := make(map[string]string, *redisCluster.Spec.Replicas)
	for _, pod := range pods {
		podNodeMap[pod.Name] = pod.Spec.NodeName
	}

	//从status的condition中取podName和nodeName的对应关系
	conditionPodNodeMap := make(map[string]string, len(redisCluster.Status.Conditions))
	for _, con := range redisCluster.Status.Conditions {
		conditionPodNodeMap[con.Name] = con.Hostname
	}

	//合并podNodeMap和status的condition,合并后的pod可能还没有node(如创建场景一个pod一直处于pending状态,此时其实没必要删除数据)
	for k, v := range conditionPodNodeMap {
		if v != "" {
			podNodeMap[k] = v
		}
	}

	if len(podNodeMap) == 0 {
		//不需要删除
		return nil
	}

	//先用exec方式删除
	failedDelPods, err := rco.deleteData(redisCluster, specificPodNames, podNodeMap, k8sClusterDomainName, pathPrefix)
	if err == nil || len(failedDelPods) == 0 {
		//删除成功
		return nil
	}

	//每个node上的实例
	nodePodMap := make(map[string][]string, *redisCluster.Spec.Replicas)
	for pod, node := range podNodeMap {
		//podName在失败列表中;创建场景一个pod一直处于pending状态,pod还没有node,此时其实没必要删除数据
		if util.InSlice(pod, failedDelPods) && node != "" {
			nodePodMap[node] = append(nodePodMap[node], pod)
		}
	}

	klog.V(3).Infof("delData podNodeMap is %v", podNodeMap)

	mountPath := fmt.Sprintf("%v/redis/%v/%v", pathPrefix, redisCluster.Namespace, redisCluster.Name)
	//起pod删除数据
	delFunc := func(nodeName, command string, count int) {
		klog.V(3).Infof("recycler pod nodeName: %v command: %v", nodeName, command)
		pod, err := recycler.NewMiddlewareDataRecyclerPodTemplate(mountPath, nodeName, command, redisCluster.Spec.Repository)
		pod.Name = fmt.Sprintf("%v-%v-%v", redisCluster.Name, time.Now().Nanosecond(), count)
		if err != nil {
			appendError(err)
			return
		}
		err = recycler.RecycleVolumeByWatchingPodUntilCompletion(pod, rco.defaultClient, rco.newRecyclerEventRecorder(pod))
		if err != nil {
			appendError(err)
			return
		}
	}

	var delPods []string
	count := 0
	for node, pods := range nodePodMap {
		command := fmt.Sprintf(" rm -rf  ")
		for _, pod := range pods {
			delPods = append(delPods, pod)
			command += fmt.Sprintf(" %v/%v ", mountPath, pod)
		}
		wg.Add(1)
		go func(node, command string, count int) {
			defer wg.Done()
			delFunc(node, command, count)
		}(node, command, count)
		count++
	}

	wg.Wait()

	if len(errs) != 0 {
		err = fmt.Errorf("delete redisCluster: %v/%v -- %v data is error: %v", redisCluster.Namespace, redisCluster.Name, delPods, errs)
		//TODO 更新状态回滚或者删除集群失败?
		klog.Warning(err.Error())
		//rco.updateRedisClusterStatus()
		return nil
	}

	return nil
}

// newRecyclerEventRecorder returns a RecycleEventRecorder that sends all events
// to given pod.
func (rco *RedisClusterOperator) newRecyclerEventRecorder(pod *v1.Pod) recycler.RecycleEventRecorder {
	return func(eventtype, message string) {
		rco.eventRecorder.Eventf(pod, eventtype, "RecyclerPod", "Recycler pod: %s", message)
	}
}

//分配master和slave ip,符合:1、两个主节点尽可能不在同一节点上;2、master和对应slave尽可能不在同一宿主机上
//addresses：需要划分master、slave IP的address集合
func (rco *RedisClusterOperator) assignMasterSlaveIP(addresses []v1.EndpointAddress) ([]string, []string, error) {

	if len(addresses) == 0 {
		return nil, nil, fmt.Errorf("create redisCluster endpoints addresses is empty")
	}

	//key: nodeName value: ips
	nodeIPs := make(map[string][]v1.EndpointAddress)
	for _, addr := range addresses {
		nodeIPs[*addr.NodeName] = append(nodeIPs[*addr.NodeName], addr)
	}

	//将nodeIPs map的key排序,保证多次遍历map时输出顺序一致
	//同时保证node上IP多的nodeKey排到前面
	type kv struct {
		nodeKey  string
		ipsValue []v1.EndpointAddress
	}

	var nodeIPsKvs []kv
	for k, v := range nodeIPs {
		nodeIPsKvs = append(nodeIPsKvs, kv{k, v})
	}

	sort.Slice(nodeIPsKvs, func(i, j int) bool {
		// 降序
		return len(nodeIPsKvs[i].ipsValue) > len(nodeIPsKvs[j].ipsValue)
	})

	sortedKeys := make([]string, 0)
	for _, k := range nodeIPsKvs {
		sortedKeys = append(sortedKeys, k.nodeKey)
	}

	// sort 'string' key in increasing order
	//sort.Strings(sortedKeys)

	// slave replicas count
	replicas := 1
	// all master and slave count
	nodesCount := len(addresses)
	// master count
	mastersCount := nodesCount / (replicas + 1)

	// Select master instances
	var interleaved []v1.EndpointAddress
	isLoop := true
	for isLoop {
		// take one ip from each node until we run out of addr
		// across every node.
		// loop map by sortedKeys Guarantee same order loop repeatedly
		// ref：https://blog.csdn.net/slvher/article/details/44779081
		for _, key := range sortedKeys {
			//如果该node上没有IP了,且节点已经够了,停止循环
			if len(nodeIPs[key]) == 0 {
				if len(interleaved) == nodesCount {
					isLoop = false
					//continue
					break
				}
			} else {
				//从每个node上取出一个IP加入interleaved
				interleaved = append(interleaved, nodeIPs[key][0])
				//移除该node上被取出的IP
				nodeIPs[key] = nodeIPs[key][1:]
			}
		}
	}

	masters := interleaved[0:mastersCount]

	// master ips
	var masterInstanceIPs []string
	for _, addr := range masters {
		masterInstanceIPs = append(masterInstanceIPs, addr.IP)
	}

	// Remaining nodes Count
	nodesCount -= len(masters)

	// Remaining
	interleaved = interleaved[mastersCount:]

	// Rotating the list sometimes helps to get better initial anti-affinity before the optimizer runs.
	interleaved = append(interleaved[1:], interleaved[:1]...)

	// slave ips
	var slaveInstanceIPs []string

	// print assign info when the method end
	//defer klog.V(4).Infof("masterInstanceIPs: %v\nslaveInstanceIPs: %v", masterInstanceIPs, slaveInstanceIPs)
	//这里要用闭包方式打印日志,否则slaveInstanceIPs是空slice
	// ref:https://www.kancloud.cn/liupengjie/go/576456
	var slaves []v1.EndpointAddress
	defer func() {
		// 判断一组master、slave是否在同一节点上
		for i := 0; i < len(masters); i++ {
			if *(masters[i].NodeName) == *(slaves[i].NodeName) {
				klog.Warningf("A group [master->slave] nodeName equal; masterInstanceIP: %v slaveInstanceIPs: %v \n", masterInstanceIPs[i], slaveInstanceIPs[i])
			}
		}
		klog.V(4).Infof("\nmasterInstanceIPs: %v\nslaveInstanceIPs: %v", masterInstanceIPs, slaveInstanceIPs)
	}()

	for _, master := range masters {
		assignedReplicas := 0
		for assignedReplicas < replicas {

			// 0 indicate assign finished
			if nodesCount == 0 {
				return masterInstanceIPs, slaveInstanceIPs, nil
			}

			// Return the first node not matching our current master
			var instanceIP string
			removeIndex := 0

			// find slave and master node don't match
			for i, addr := range interleaved {
				if *master.NodeName != *addr.NodeName {
					instanceIP = addr.IP
					removeIndex = i
					break
				}
			}

			// If we found a IP, use it as a best-first match.
			// Otherwise, we didn't find a IP on a different node, so we
			// go ahead and use a same-node replica.
			if instanceIP != "" {
				slaveInstanceIPs = append(slaveInstanceIPs, instanceIP)
			} else {
				slaveInstanceIPs = append(slaveInstanceIPs, interleaved[0].IP)
				removeIndex = 0

			}

			// 用于判断一组master、slave是否在同一节点上
			slaves = append(slaves, interleaved[removeIndex])

			//remove assigned addr
			// if interleaved = ["0", "1", "2"]
			// removeIndex = 0 -- >> interleaved[:0], interleaved[1:]...  -- >> ["1", "2"]
			// removeIndex = 1 -- >> interleaved[:1], interleaved[2:]...  -- >> ["0", "2"]
			// removeIndex = 2 -- >> interleaved[:2], interleaved[3:]...  -- >> ["0", "1"]
			interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)

			// nodesCount dec
			nodesCount -= 1

			// assignedReplicas inc
			assignedReplicas += 1
		}
	}

	return masterInstanceIPs, slaveInstanceIPs, nil
}

//需要分配master和slave ip的address
//endpoints：扩实例之后的endpoint信息
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) assignMasterSlaveIPAddress(endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]v1.EndpointAddress, error) {
	subset := endpoints.Subsets
	if len(subset) == 0 || len(subset[0].Addresses) == 0 {
		return nil, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", endpoints.Namespace, endpoints.Name)
	}

	//old endpoints为nil时表示为创建时分配;否则为升级时分配
	var addresses []v1.EndpointAddress
	if oldEndpoints == nil {
		addresses = subset[0].Addresses
	} else {
		oldSubset := oldEndpoints.Subsets
		if len(oldSubset) == 0 || len(oldSubset[0].Addresses) == 0 {
			klog.Warningf("RedisCluster: %v/%v, oldEndpoints.Subsets is nil", endpoints.Namespace, endpoints.Name)
			addresses = subset[0].Addresses
		} else {
			for _, new := range subset[0].Addresses {
				for i, old := range oldSubset[0].Addresses {
					if new.IP == old.IP {
						break
					}
					if len(oldSubset[0].Addresses)-1 == i {
						// 说明new是升级时新实例的addr
						addresses = append(addresses, new)
					}
				}
			}
		}
	}

	return addresses, nil
}

//从redis集群中移除节点
//existInstanceIp：redis集群中已经存在的节点
//podName：某一个ready的podName
//namespace：某一个ready的pod所在分区
//delNodeIds：要删除的节点nodeId
func (rco *RedisClusterOperator) delNodeFromRedisCluster(podName, namespace string, delNodeIds, existInstanceIps []string) error {
	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	for _, delNodeId := range delNodeIds {
		// redis-cli --cluster del-node 172.22.242.238:7007 ac2e9f579d7b844d302419628108edff98ebac11 -a "abc"
		delNodeCmd := fmt.Sprintf(" redis-cli --cluster del-node %v:6379 %v %v ", existInstanceIps[0], delNodeId, pwdArgs)
		commandDelNode := []string{"/bin/sh", "-c", delNodeCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandDelNode, redisContainerName, podName, namespace, nil)

		if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
			err := fmt.Errorf("redisCluster: %v/%v -- delNode error -- delNodeCmd: %v\nstdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, delNodeCmd, stdout, stderr, err)
			klog.Errorf(err.Error())
			return err
		}
		klog.V(4).Infof("redisCluster: %v/%v delNode: -- \nstdout: %v", namespace, podName, stdout)
	}

	return nil
}

//从redis集群中移除节点
//existInstanceIp：redis集群中已经存在的节点
//podName：某一个ready的podName
//namespace：某一个ready的pod所在分区
//delNodeIds：要删除的节点nodeId
func (rco *RedisClusterOperator) forgetNodeFromRedisCluster(podName, namespace string, forgetNodeIds, existInstanceIps []string) error {
	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	for count := 0; count < forgetNodeExecCount; count++ {
		//连接所有节点执行forget
		for _, existInstanceIp := range existInstanceIps {
			for _, delNodeId := range forgetNodeIds {
				// redis-cli --cluster -h 172.22.242.238 -p 7007 -a abc cluster forget ac2e9f579d7b844d302419628108edff98ebac11
				forgetNodeCmd := fmt.Sprintf(" redis-cli -c -h %v -p 6379 %v cluster forget %v ", existInstanceIp, pwdArgs, delNodeId)
				commandDelNode := []string{"/bin/sh", "-c", forgetNodeCmd}
				stdout, stderr, err := rco.ExecToPodThroughAPI(commandDelNode, redisContainerName, podName, namespace, nil)

				if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning) && !strings.Contains(stderr, stderrUnknownNode)) {
					err := fmt.Errorf("redisCluster: %v/%v -- forgetNode error -- forgetNodeCmd: %v\nstdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, forgetNodeCmd, stdout, stderr, err)
					klog.Errorf(err.Error())
					return err
				}
				klog.V(4).Infof("redisCluster: %v/%v forgetNode: -- \nstdout: %v", namespace, podName, stdout)
			}
		}
	}

	return nil
}

//根据podName从小到大排序endpoints
func sortEndpointsByPodName(endpoints ...*v1.Endpoints) {
	if len(endpoints) == 0 {
		return
	}

	for _, endpoint := range endpoints {
		if endpoint == nil || len(endpoint.Subsets) == 0 {
			return
		}
		addresses := endpoint.Subsets[0].Addresses
		sort.SliceStable(addresses, func(i, j int) bool {
			name1 := addresses[i].TargetRef.Name
			name2 := addresses[j].TargetRef.Name
			return name1 < name2
		})
	}
}
