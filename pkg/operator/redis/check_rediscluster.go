package redis

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/pkg/record"
	"harmonycloud.cn/middleware/redis-cluster/util"
	"harmonycloud.cn/middleware/redis-cluster/util/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

var reg = regexp.MustCompile(`([\d.]+):6379 \((\w+)...\) -> (\d+) keys \| (\d+) slots \| (\d+) slaves`)
var clusterInfoReg = regexp.MustCompile(`cluster_known_nodes:(\d+)`)

//检查集群状态
//redisCluster：redisCluster对象
//TODO 重构代码,为了不影响现在已好逻辑,重构后必须需要同时满足以下几点:
//1、状态为RedisClusterFailed,且ReasonType为RedisClusterReasonTypeRebalanceSlotsFailed或者RedisClusterReasonTypeReshareSlotsFailed时直接return nil(卡槽迁移导致的失败不予处理)
//2、状态非RedisClusterRollback,且非RedisClusterCreating,且pod ready实例数不等于redisCluster.spec.Replicas时,说明是集群重启场景,首先更新status到RedisClusterScaling
//3、为减少集群纵向扩容和集群(单实例)重启的相似度,导致集群出现异常回滚,需要区分这两种场景;扩容用Scaling表示(statefulset.status.currentRevision和status.updateRevision不同时表示扩容);重启用ReStarting(sts的status.currentRevision和status.updateRevision相同时表示重启,注意:第一次创建实例启动时也是相等的)
func (rco *RedisClusterOperator) checkAndUpdateRedisClusterStatus(redisCluster *RedisCluster) error {
	password, isExistPassword := rco.loadRedisClusterPasswordByKey(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", redisCluster.Namespace, redisCluster.Name)
		pwdArgs = ""
	}
	// 检查集群状态总是获取最新的redisCluster对象,从缓存中获取的是按照队列顺序的,不一定是最新的
	latestRedisCluster := &RedisCluster{}
	err := rco.frameClient.Get(context.TODO(),types.NamespacedName{redisCluster.Namespace,redisCluster.Name},latestRedisCluster)

	if err != nil {
		return fmt.Errorf("checkAndUpdateRedisClusterStatus get latest RedisCluster: %v/%v error: %v", redisCluster.Namespace, redisCluster.Name, err)
	}

	klog.V(4).Infof("Started check redisCluster: %v/%v ResourceVersion: %v phase %v", latestRedisCluster.Namespace, latestRedisCluster.Name, latestRedisCluster.ResourceVersion, latestRedisCluster.Status.Phase)

	//处理exporter
	err = rco.handleRedisExporterStsAndSvc(latestRedisCluster)
	if err != nil {
		return err
	}

	//redisCluster对应的crd
	sts, err := rco.getStatefulSetForRedisCluster(latestRedisCluster)
	if err != nil {
		klog.Error(err)
		return err
	}

	if sts == nil {
		klog.Warningf("RedisCluster: %v/%v statefulset is not exist", redisCluster.Namespace, redisCluster.Name)
		return nil
	}

	//如果状态为回滚,则判断是否回滚完成
	if latestRedisCluster.Status.Phase == RedisClusterRollback {
		klog.V(3).Infof("redisCluster: %v/%v is rollbacking, no handle check!", latestRedisCluster.Namespace, latestRedisCluster.Name)
		return nil
	}

	//卡槽迁移失败不需要处理
	if latestRedisCluster.Status.Phase == RedisClusterFailed && (latestRedisCluster.Status.ReasonType == RedisClusterReasonTypeRebalanceSlotsFailed || latestRedisCluster.Status.ReasonType == RedisClusterReasonTypeReshareSlotsFailed) {
		klog.V(4).Infof("redisCluster: %v%v phase is Failed reasonType is %v, no need handle", latestRedisCluster.Namespace, latestRedisCluster.Name, latestRedisCluster.Status.ReasonType)
		return nil
	}

	endpoints, err := rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("checkAndUpdateRedisClusterStatus get latest endpoints: %v/%v error: %v", redisCluster.Namespace, redisCluster.Name, err)
	}

	sortEndpointsByPodName(endpoints)
	//endpoint里地址数不等于要求实例数,则更新为扩容中,直到超时
	if endpoints == nil || len(endpoints.Subsets) == 0 || int32(len(endpoints.Subsets[0].Addresses)) != *latestRedisCluster.Spec.Replicas {

		//集群或者单实例重启；CurrentRevision和UpdateRevision相等可能是sts创建时,也可能是pod重启，其中创建时ObservedGeneration为1
		//集群重启先不加超时,因为比较复杂,涉及到pod自动重启和调用接口重启
		if latestRedisCluster.Status.Phase != RedisClusterCreating && sts.Status.CurrentRevision == sts.Status.UpdateRevision && int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas {
			latestRedisCluster, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState(latestRedisCluster.Status.Reason, RedisClusterRestarting, latestRedisCluster.Status.ReasonType), false)
			if err != nil {
				return fmt.Errorf("update RedisCluster %v/%v status to Restarting is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			}
			return nil
		}

		err = wait.ErrWaitTimeout
		//判断是否超时,超时更新phase为
		if v, ok := redisClusterSyncStartTimeMap.Load(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name)); !ok {
			redisClusterSyncStartTimeMap.Store(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name), time.Now())
		} else {
			spendTime := time.Now().Sub(v.(time.Time)).Minutes()
			if spendTime > float64(rco.options.ClusterTimeOut) {
				//超时后移除开始时间
				redisClusterSyncStartTimeMap.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
				//超时
				if latestRedisCluster.Status.Phase == RedisClusterCreating {
					err = errors.NewCreatePodFailedWhenCreateCluster(err.Error(), RedisClusterFailed)
					klog.Warningf("create redisCluster: %v/%v wait pod is ready error: %v", latestRedisCluster.Namespace, latestRedisCluster.Name, err)
					_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, err, false)
				} else if latestRedisCluster.Status.Phase == RedisClusterFailed {

				} else if latestRedisCluster.Status.Phase == RedisClusterRestarting && int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas {
					//实例数和condition数是一致的说明是集群重启
					err = errors.NewRedisClusterState(err.Error(), RedisClusterFailed, RedisClusterReasonTypeCreatePodFailed)
					klog.Warningf("restart redisCluster: %v/%v wait pod is ready error: %v", latestRedisCluster.Namespace, latestRedisCluster.Name, err)
					_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, err, false)
				} else {
					//扩容时,pod一直起不来会导致回滚
					err = errors.NewCreatePodFailedWhenUpgradeCluster(err.Error(), RedisClusterRollback)
					klog.Warningf("upgrade redisCluster: %v/%v wait pod is ready error: %v -- start rollback", latestRedisCluster.Namespace, latestRedisCluster.Name, err)
					//更新状态为Failing
					_, err := rco.updateRedisClusterStatus(redisCluster, endpoints, err, false)

					if err != nil {
						return err
					}
				}

				if err != nil {
					klog.Errorf("update redisCluster: %v/%v is error: %v", latestRedisCluster.Namespace, latestRedisCluster.Name, err)
					return err
				}

				return nil
			}
		}

		if latestRedisCluster.Status.Phase == RedisClusterCreating {
			klog.V(3).Infof("redisCluster: %v/%v is phase is creating wait all pod ready", latestRedisCluster.Namespace, latestRedisCluster.Name)
			return nil
		}

		//集群扩容
		//更新开始等待时间
		//更新crd状态为Scaling
		latestRedisCluster, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState(latestRedisCluster.Status.Reason, RedisClusterScaling, latestRedisCluster.Status.ReasonType), false)
		if err != nil {
			return fmt.Errorf("update RedisCluster %v/%v status to Scaling is error: %v", redisCluster.Namespace, redisCluster.Name, err)
		}

		return nil
	}

	//pod全部ready删除开始时间
	redisClusterSyncStartTimeMap.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))

	if (latestRedisCluster.Status.Phase == RedisClusterRunning &&
		int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas) ||
		(latestRedisCluster.Status.Phase == RedisClusterUpgrading &&
			int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas) ||
		(latestRedisCluster.Status.Phase == RedisClusterCreating &&
			int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas) ||
		latestRedisCluster.Status.Phase == RedisClusterRestarting {

		clusterInstanceIp := endpoints.Subsets[0].Addresses[0].IP
		reference := endpoints.Subsets[0].Addresses[0].TargetRef
		var clusterStatusIsOK bool
		var err error

		if len(endpoints.Subsets[0].Addresses) != 2 {
			clusterStatusIsOK, _, err = rco.execClusterInfo(clusterInstanceIp, reference.Name, reference.Namespace)
		} else {
			pod1 := redisCluster.Status.Conditions[0]
			pod2 := redisCluster.Status.Conditions[1]
			var masterIp, slaveIp, masterName, slaveName string
			if pod1.Type == "master" {
				masterIp = strings.Split(pod1.Instance, ":")[0] //10.168.127.149:6379
				masterName = pod1.Name

				slaveIp = strings.Split(pod2.Instance, ":")[0]
				slaveName = pod2.Name
			} else {
				masterIp = strings.Split(pod2.Instance, ":")[0]
				masterName = pod2.Name
				slaveIp = strings.Split(pod1.Instance, ":")[0]
				slaveName = pod1.Name
			}
			if endpoints.Subsets[0].Addresses[0].TargetRef.Name == masterName {
				masterIp = endpoints.Subsets[0].Addresses[0].IP
				slaveIp = endpoints.Subsets[0].Addresses[1].IP
			} else if endpoints.Subsets[0].Addresses[1].TargetRef.Name == masterName {
				masterIp = endpoints.Subsets[0].Addresses[1].IP
				slaveIp = endpoints.Subsets[0].Addresses[0].IP
			}
			clusterStatusIsOK, _, err = rco.execMSInfo(masterIp, reference.Name, reference.Namespace)
			if !clusterStatusIsOK {
				pause := redisCluster.Spec.Pause
				if pause == true {
					clusterStatusIsOK = true
				} else {
					var createMasterSlaveCommand = fmt.Sprintf("redis-cli  -h %v -p 6379 %v SLAVEOF %v 6379 | redis-cli  -h %v -p 6379 %v  CONFIG SET masterauth %v", slaveIp, pwdArgs, masterIp, slaveIp, pwdArgs, password)
					//createMasterCommand := fmt.Sprintf("echo yes | redis-cli --cluster create %v:6379 %v:6379 %v:6379", masterInstance[0], masterInstance[1], masterInstance[2])

					klog.Infof("create createMasterSlaveCommand is: %v %v", createMasterSlaveCommand, slaveName)

					commandMaster := []string{"/bin/sh", "-c", createMasterSlaveCommand}
					slavePodNamespace := endpoints.Subsets[0].Addresses[1].TargetRef.Namespace
					stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, redisContainerName, slaveName, slavePodNamespace, nil)

					if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) {
						err := fmt.Errorf("exec create MasterSlave Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", commandMaster, stdout, stderr, err)
						klog.Errorf(err.Error())
						return err
					} else {
						clusterStatusIsOK = true
					}
					//clusterStatusIsOK = true
				}
			}
		}

		if err != nil {
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterFailed), false)
			return err
		}

		if !clusterStatusIsOK {
			abnormalReason := clusterStateFailed
			if latestRedisCluster.Status.Reason != "" {
				abnormalReason = latestRedisCluster.Status.Reason
			}
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(abnormalReason, RedisClusterFailed), false)
			return nil
		}

		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		return err
	}

	//TODO 状态为Running时，可能只有部分节点形成集群
	//TODO 纵向扩容异常处理

	klog.Infof("latestRedisCluster: %v/%v current phase: %v", latestRedisCluster.Namespace, latestRedisCluster.Name, latestRedisCluster.Status.Phase)

	var phase RedisClusterPhase
	//正常检查集群状态导致的失败需要处理
	if latestRedisCluster.Status.Phase == RedisClusterFailed {
		if !latestRedisCluster.Status.FormedClusterBefore && effectiveConditionLen(latestRedisCluster.Status.Conditions) == 0 {
			phase = RedisClusterCreating
		} else if int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas {
			phase = RedisClusterRestarting
		} else {
			phase = RedisClusterUpgrading
		}
	} else {
		phase = latestRedisCluster.Status.Phase
	}

	addresses := endpoints.Subsets[0].Addresses

	klog.Infof("latestRedisCluster: %v/%v current phase: %v , fix start.", latestRedisCluster.Namespace, latestRedisCluster.Name, phase)

	isClusteredAddress, isOnlyKnownSelfAddress, err := rco.getIsClusteredAndOnlyKnownSelfAddress(addresses)
	if err != nil {
		rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(clusterStateFailed, RedisClusterFailed), false)
		return err
	}

	//TODO 升级时加节点成功,但未进行卡槽迁移怎么搞?卡槽迁移失败是否要进行自动检测修复,如果失败后不检测,则集群状态变化感知不到;如果检测,则状态会一直更新为Upgrade
	/*if len(addresses) == len(isClusteredAddress) {
		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		return err
	}*/

	var (
		successAddInstances []string
		nodeInfos           []*redisNodeInfo
	)

	//异常场景处理
	switch phase {
	case RedisClusterCreating:

		//表示新集群,所有实例都没有初始化,开始初始化集群
		if int32(len(isOnlyKnownSelfAddress)) == *latestRedisCluster.Spec.Replicas || len(isClusteredAddress) == 0 {

			if !latestRedisCluster.Status.FormedClusterBefore && effectiveConditionLen(latestRedisCluster.Status.Conditions) == 0 {
				err = rco.createAndInitRedisCluster(latestRedisCluster)
				if err != nil {
					rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeWarning, "CreateClusterFailed", "creating:createClusterFailed:Cluster %v is created failed: %v", redisCluster.Name, err)
				}
				return err
			}
			//conditions里不为空,说明以前是集群,现在都是独立节点,这种异常场景情况先不处理
			err = fmt.Errorf("latestRedisCluster: %v/%v was cluster, but not now! current phase: %v , isClusteredAddress = 0 , no handle", latestRedisCluster.Namespace, latestRedisCluster.Name, latestRedisCluster.Status.Phase)
			klog.Error(err.Error())
			return err
		}

		if len(addresses) > 2 {

			existInstanceIp := isClusteredAddress[0].IP
			//查询当前集群的节点信息
			nodeInfos, err = rco.execClusterNodes(existInstanceIp, isClusteredAddress[0].TargetRef.Namespace, isClusteredAddress[0].TargetRef.Name, true)
			if err != nil {
				//更新状态为Failed
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
				return err
			}

			var willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps []string
			willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, err = rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, nil)

			reference := addresses[0].TargetRef

			rco.eventRecorder.LabeledEventf(latestRedisCluster, record.CreatingEvent, v1.EventTypeNormal, "AddMaster", "creating:addMaster:Cluster %v is adding master", latestRedisCluster.Name)
			//加master
			if len(willAddClusterMasterIps) != 0 {
				err = rco.addMasterNodeToRedisCluster(willAddClusterMasterIps, existInstanceIp, reference.Name, reference.Namespace, &successAddInstances)
				if err != nil {
					rco.eventRecorder.LabeledEventf(latestRedisCluster, record.CreatingEvent, v1.EventTypeWarning, "AddMaster", "creating:addMasterFailed:Cluster %v is adding master", latestRedisCluster.Name)
					//更新状态为Failed
					rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewAddMasterFailedWhenCreateRedisCluster(err.Error(), RedisClusterRollback), false)
					return err
				}
			}

			//查询新建集群的节点信息,主要是master的nodeId信息
			nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name, true)

			if err != nil {
				//更新状态为Failed
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
				return err
			}

			rco.eventRecorder.LabeledEventf(latestRedisCluster, record.CreatingEvent, v1.EventTypeNormal, "AddSlave", "creating:addSlave:Cluster %v is adding slave", latestRedisCluster.Name)
			if len(willAddClusterSlaveIps) != 0 {
				// 根据slaveParentIps拿nodeId,下标对应
				var masterInstanceNodeIds []string
				for _, masterInstanceIp := range slaveParentIps {
					for _, info := range nodeInfos {
						if masterInstanceIp+":6379" == info.IpPort {
							masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
							break
						}
					}
				}

				err = rco.addSlaveToClusterMaster(willAddClusterSlaveIps, slaveParentIps, masterInstanceNodeIds, reference.Namespace, reference.Name, &successAddInstances)
				if err != nil {
					rco.eventRecorder.LabeledEventf(latestRedisCluster, record.CreatingEvent, v1.EventTypeWarning, "AddSlave", "creating:addSlaveFailed:Cluster %v is adding slave", latestRedisCluster.Name)
					//更新状态为Failed
					rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewAddSlaveFailedWhenCreateCluster(err.Error(), RedisClusterRollback), false)
					return err
				}
			}

			err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, reference.Name, reference.Namespace, "")
			if err != nil {
				//更新状态为Failed
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRollback), false)
				return err
			}
		}
		klog.Infof("cluster create and init fix success")

		//更新状态为Running
		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		if err == nil {
			rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeNormal, "CreateClusterSuccess", "creating:createClusterSuccess:Cluster %v is created", redisCluster.Name)
		}
		return err

	case RedisClusterScaling:

		if int32(effectiveConditionLen(latestRedisCluster.Status.Conditions)) == *latestRedisCluster.Spec.Replicas {
			_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
			return nil
		}

		//表示新集群,所有实例都没有初始化,开始初始化集群
		//这个步骤很危险,因为创建失败,当中间件查询创建结果时,middleware会加删除标识回滚删除集群;因此这里判断逻辑一定要全面
		if int32(len(isOnlyKnownSelfAddress)) == *latestRedisCluster.Spec.Replicas || len(isClusteredAddress) == 0 {
			if !latestRedisCluster.Status.FormedClusterBefore && effectiveConditionLen(latestRedisCluster.Status.Conditions) == 0 {
				err = rco.createAndInitRedisCluster(latestRedisCluster)
				if err != nil {
					rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeWarning, "CreateClusterFailed", "creating:createClusterFailed:Cluster %v is created failed: %v", redisCluster.Name, err)
				}
				return err
			}
			//conditions里不为空,说明以前是集群,现在都是独立节点,这种异常场景情况先不处理
			err = fmt.Errorf("latestRedisCluster: %v/%v was cluster, but not now! current phase: %v , isClusteredAddress = 0 , no handle", latestRedisCluster.Namespace, latestRedisCluster.Name, latestRedisCluster.Status.Phase)
			klog.Error(err.Error())
			return err
		}

		//复制出一个Endpoints
		oldEndpoints := endpoints.DeepCopy()
		//没有scale前的endpoint里address应该是Conditions长度
		subLen := len(addresses) - effectiveConditionLen(latestRedisCluster.Status.Conditions)
		//TODO 数组下表越界
		oldEndpoints.Subsets[0].Addresses = addresses[:subLen]
		return rco.upgradeRedisCluster(latestRedisCluster, oldEndpoints)
	case RedisClusterUpgrading:

		if int32(len(isOnlyKnownSelfAddress)) == *latestRedisCluster.Spec.Replicas || len(isClusteredAddress) == 0 {
			if !latestRedisCluster.Status.FormedClusterBefore && effectiveConditionLen(latestRedisCluster.Status.Conditions) == 0 {
				err = rco.createAndInitRedisCluster(latestRedisCluster)
				if err != nil {
					rco.eventRecorder.LabeledEventf(redisCluster, record.CreatingEvent, v1.EventTypeWarning, "CreateClusterFailed", "creating:createClusterFailed:Cluster %v is created failed: %v", redisCluster.Name, err)
				}
				return err
			}
			//conditions里不为空,说明以前是集群,现在都是独立节点,这种异常场景情况先不处理
			err = fmt.Errorf("latestRedisCluster: %v/%v was cluster, but not now! current phase: %v , isClusteredAddress = 0 , no handle", latestRedisCluster.Namespace, latestRedisCluster.Name, latestRedisCluster.Status.Phase)
			klog.Error(err.Error())
			return err
		}

		//复制出一个Endpoints
		oldEndpoints := endpoints.DeepCopy()
		//新增的实例数
		subLen := len(addresses) - effectiveConditionLen(latestRedisCluster.Status.Conditions)
		oldEndpoints.Subsets[0].Addresses = addresses[:subLen]

		//没有形成集群的实例数如果等于新加实例数,则说明还没有进行升级操作
		if subLen == len(isOnlyKnownSelfAddress) {
			return rco.upgradeRedisCluster(latestRedisCluster, oldEndpoints)
		}

		existInstanceIp := isClusteredAddress[0].IP

		var existInstanceIps []string
		for _, addr := range isClusteredAddress {
			existInstanceIps = append(existInstanceIps, addr.IP)
		}

		//升级错误处理
		defer func() {
			if err != nil {
				err = rco.handleUpgradeRedisClusterError(err, latestRedisCluster, endpoints)
			}
		}()

		//查询当前集群的节点信息
		nodeInfos, err = rco.execClusterNodes(existInstanceIp, isClusteredAddress[0].TargetRef.Namespace, isClusteredAddress[0].TargetRef.Name, true)
		if err != nil {
			//更新状态为Running
			//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
			//err = errors.NewUnexpectedError(err.Error(), RedisClusterRunning)
			err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
			return err
		}

		var willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps []string
		willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, err = rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, oldEndpoints)

		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "AddMaster", "hScaling:addMaster:Cluster %v is adding master", redisCluster.Name)
		reference := addresses[0].TargetRef
		//加master
		if len(willAddClusterMasterIps) != 0 {
			err = rco.addMasterNodeToRedisCluster(willAddClusterMasterIps, existInstanceIp, reference.Name, reference.Namespace, &successAddInstances)
			if err != nil {
				rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "AddMasterFailed", "hScaling:addMasterFailed:Cluster %v adds master failed: %v", redisCluster.Name, err)
				//更新状态为Running
				//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewAddMasterFailedWhenUpgradeRedisCluster(err.Error(), RedisClusterRunning), false)
				//err = errors.NewAddMasterFailedWhenUpgradeRedisCluster(err.Error(), RedisClusterRunning)
				err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeAddMasterFailedWhenUpgradeCluster, successAddInstances, existInstanceIps)
				return err
			}
		}

		//查询新建集群的节点信息,主要是master的nodeId信息
		nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name, true)

		if err != nil {
			//更新状态为Failed
			//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
			//err = errors.NewUnexpectedError(err.Error(), RedisClusterRunning)
			err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
			return err
		}

		rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "AddSlave", "hScaling:addSlave:Cluster %v is adding slave", redisCluster.Name)
		//加slave
		if len(willAddClusterSlaveIps) != 0 {
			// 根据slaveParentIps拿nodeId,下标对应
			var masterInstanceNodeIds []string
			for _, masterInstanceIp := range slaveParentIps {
				for _, info := range nodeInfos {
					if masterInstanceIp+":6379" == info.IpPort {
						masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
						break
					}
				}
			}

			err = rco.addSlaveToClusterMaster(willAddClusterSlaveIps, slaveParentIps, masterInstanceNodeIds, reference.Namespace, reference.Name, &successAddInstances)
			if err != nil {
				rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "AddSlave", "hScaling:addSlaveFailed:Cluster %v adds slave failed: %v", redisCluster.Name, err)
				//更新状态为Running
				//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewAddSlaveFailedWhenUpgradeCluster(err.Error(), RedisClusterRunning), false)
				//err = errors.NewAddSlaveFailedWhenUpgradeCluster(err.Error(), RedisClusterRunning)
				err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeAddSlaveFailedWhenUpgradeCluster, successAddInstances, existInstanceIps)
				return err
			}
		}

		upgradeType := latestRedisCluster.Spec.UpdateStrategy.Type
		switch upgradeType {
		case AutoReceiveStrategyType:
			rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "RebalanceSlots", "hScaling:rebalanceSlots:Cluster %v is rebalancing slots", redisCluster.Name)
			err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, reference.Name, reference.Namespace, latestRedisCluster.Spec.UpdateStrategy.Pipeline)
			if err != nil {
				rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "RebalanceSlotsFailed", "hScaling:rebalanceSlotsFailed:Cluster %v rebalances slots failed: %v", redisCluster.Name, err)
				//更新状态为Running
				//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRebalanceSlotsFailed(err.Error(), RedisClusterRunning), false)
				err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeRebalanceSlotsFailed, successAddInstances, existInstanceIps)
				return err
			}
		case AssignReceiveStrategyType:
			var infos []redisTribInfo
			infos, err = rco.execRedisTribInfo(existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Running
				//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
				//err = errors.NewUnexpectedError(err.Error(), RedisClusterRunning)
				err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
				return err
			}

			//查看卡槽分配情况
			var willAssginSlotsIP []string
			for _, info := range infos {
				if info.Slots == 0 {
					willAssginSlotsIP = append(willAssginSlotsIP, info.Ip)
				}
			}

			rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "ReshareSlots", "hScaling:reshareSlots:Cluster %v is resharing slots", redisCluster.Name)

			if len(willAssginSlotsIP) != 0 {
				updateStrategy := latestRedisCluster.Spec.UpdateStrategy.DeepCopy()
				strategies := updateStrategy.AssignStrategies
				strategyLen := len(strategies)
				willAssignIpLen := len(willAssginSlotsIP)
				if strategyLen < willAssignIpLen {
					err = fmt.Errorf("assign slots to new master strategies is error: strategyCount too less")
					klog.Error(err.Error())
					//更新状态为Running
					//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
					//err = errors.NewUnexpectedError(err.Error(), RedisClusterRunning)
					err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
					return err
				}

				updateStrategy.AssignStrategies = strategies[(strategyLen - willAssignIpLen):]

				//查询新建集群的节点信息,主要是master的nodeId信息
				nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name, true)

				if err != nil {
					//更新状态为Failed
					//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewUnexpectedError(err.Error(), RedisClusterRunning), false)
					//err = errors.NewUnexpectedError(err.Error(), RedisClusterRunning)
					err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterRollback, RedisClusterReasonTypeUnexpectedError, successAddInstances, existInstanceIps)
					return err
				}

				var willAssginSlotsNodeIds []string
				for _, willIp := range willAssginSlotsIP {
					for _, info := range nodeInfos {
						if willIp+":6379" == info.IpPort {
							willAssginSlotsNodeIds = append(willAssginSlotsNodeIds, info.NodeId)
						}
					}
				}

				//reshare分配卡槽
				err = rco.reshareRedisClusterSlotsToMasterNode(*updateStrategy, existInstanceIp, reference.Name, reference.Namespace, willAssginSlotsNodeIds)
				if err != nil {
					rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeWarning, "ReshareSlotsFailed", "hScaling:reshareSlotsFailed:Cluster %v reshares slots failed: %v", redisCluster.Name, err)
					err = errors.NewUpgradeFailedErrorInfo(err.Error(), RedisClusterFailed, RedisClusterReasonTypeReshareSlotsFailed, successAddInstances, existInstanceIps)
					//更新状态为Running
					//rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewReshareSlotsFailed(err.Error(), RedisClusterRunning), false)
					return err
				}
			}
		default:
			err = fmt.Errorf("latestRedisCluster UpdateStrategy Type only [AutoReceive, AssignReceive] error")
			klog.Error(err.Error())
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewInvalid(err.Error(), RedisClusterRunning), false)
			return err
		}

		klog.Infof("cluster upgrade fix success")
		//更新状态为Running
		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		if err == nil {
			rco.eventRecorder.LabeledEventf(redisCluster, record.HScaleEvent, v1.EventTypeNormal, "UpgradeClusterSuccess", "hScaling:hScalingClusterSuccess:Cluster %v is upgraded successfully", redisCluster.Name)
		}

		return err
	case RedisClusterRunning, RedisClusterRestarting:
		//集群或者实例被重启
		klog.Infof("redisCluster: %v/%v is restarted", latestRedisCluster.Namespace, latestRedisCluster.Name)
		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, errors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		return err
	default:
		klog.Warningf("redisCluster: %v/%v phase: %v -- this is no need handle", latestRedisCluster.Namespace, latestRedisCluster.Name, phase)
		return nil
	}

	return nil
}

/*
获取已形成集群的address和独立的address
*/
func (rco *RedisClusterOperator) getIsClusteredAndOnlyKnownSelfAddress(addresses []v1.EndpointAddress) ([]v1.EndpointAddress, []v1.EndpointAddress, error) {
	if len(addresses) == 2 {
		return addresses, nil, nil
	}
	var isClusteredAddress, isOnlyKnownSelfAddress []v1.EndpointAddress

	for _, addr := range addresses {
		isOK, isOnlyKnownSelf, err := rco.execClusterInfo(addr.IP, addr.TargetRef.Name, addr.TargetRef.Namespace)
		if err != nil {
			return nil, nil, err
		}

		//如果isOnlyKnownSelf为false,其状态为fail,表示集群有问题(可能原因pod实例IP和node配置里的IP不匹配)
		if !isOnlyKnownSelf && !isOK {
			klog.Errorf("%v by instance: %v", clusterStateFailed, addr.IP)
			return nil, nil, err
		}

		//该addr对应的podIP已经组成集群
		if !isOnlyKnownSelf && isOK {
			isClusteredAddress = append(isClusteredAddress, addr)
		} else {
			//不会出现isOnlyKnownSelf和isOK同时为true
			//独立的实例,没有加入到集群
			isOnlyKnownSelfAddress = append(isOnlyKnownSelfAddress, addr)
		}
	}
	return isClusteredAddress, isOnlyKnownSelfAddress, nil
}

//查看redis集群master信息
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execRedisTribInfo(existInstanceIp, podName, namespace string) ([]redisTribInfo, error) {

	password, isExistPassword := rco.loadRedisClusterPassword(namespace, podName)
	pwdArgs := fmt.Sprintf(redisPasswordArg, password)
	if !isExistPassword || password == "" {
		klog.V(3).Infof("redisCluster: %v/%v pwd is not exist", namespace, podName)
		pwdArgs = ""
	}

	infoCmd := fmt.Sprintf(" redis-cli --cluster info %v:6379 %v ", existInstanceIp, pwdArgs)
	commandInfo := []string{"/bin/sh", "-c", infoCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandInfo, redisContainerName, podName, namespace, nil)

	if err != nil || (stderr != "" && !strings.Contains(stderr, stderrWarning)) || strings.Contains(stdout, "[ERR]") {
		err := fmt.Errorf("redisClusterInstance: %v/%v -- infoCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, commandInfo, stdout, stderr, err)
		klog.Errorf(err.Error())
		return []redisTribInfo{}, err
	}
	klog.V(4).Infof("redisClusterInstance: %v/%v infoCmd: %v \n -- \nstdout: %v", namespace, podName, commandInfo, stdout)

	var masterInfos []redisTribInfo
	infos := strings.Split(stdout, "\n")
	for _, info := range infos {
		submatches := reg.FindStringSubmatch(info)
		if len(submatches) != 6 {
			continue
		}
		keys, _ := strconv.Atoi(submatches[3])
		slots, _ := strconv.Atoi(submatches[4])
		slaves, _ := strconv.Atoi(submatches[5])
		masterInfo := redisTribInfo{
			Ip:           submatches[1],
			Port:         strconv.Itoa(redisServicePort6379),
			NodeIdPrefix: submatches[2],
			Keys:         keys,
			Slots:        slots,
			Slaves:       slaves,
		}
		masterInfos = append(masterInfos, masterInfo)
	}

	return masterInfos, nil
}

/*
待加入集群的master、slave ip以及slave对应的masterIP
willAddClusterMasterIps：需要加入的master ip
willAddClusterSlaveIps：需要加入的slave ip
slaveParentIps: 需要加入的slave对应的master ip,因为异常场景下可能slave对应的master ip不在willAddClusterMasterIps集合里
*/
func (rco *RedisClusterOperator) buildWillAddClusterMasterSlaveIPs(nodeInfos []*redisNodeInfo, newEndpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]string, []string, []string, error) {

	if len(newEndpoints.Subsets) == 0 || len(newEndpoints.Subsets[0].Addresses) == 0 {
		return nil, nil, nil, fmt.Errorf("newEndpoints subsets or addresses is empty")
	}

	newAddresses := newEndpoints.Subsets[0].Addresses

	//已存在的master、slave IP以及其绑定关系
	var existedMasterInstanceIPs, existedSlaveInstanceIPs []string
	masterSlaveConnector := make(map[string]string, len(newAddresses))
	for _, info := range nodeInfos {
		if masterFlagType == info.Flags {
			masterIP := strings.Split(info.IpPort, ":")[0]
			for _, info1 := range nodeInfos {
				if info.NodeId == info1.Master {
					masterSlaveConnector[masterIP] = strings.Split(info1.IpPort, ":")[0]
					break
				}
			}
			existedMasterInstanceIPs = append(existedMasterInstanceIPs, masterIP)
		} else {
			existedSlaveInstanceIPs = append(existedSlaveInstanceIPs, strings.Split(info.IpPort, ":")[0])
		}
	}

	return composeMasterSlaveIP(newAddresses, existedMasterInstanceIPs, existedSlaveInstanceIPs, masterSlaveConnector)
}

func composeMasterSlaveIP(newAddresses []v1.EndpointAddress, existedMasterInstanceIPs, existedSlaveInstanceIPs []string, masterSlaveConnector map[string]string) ([]string, []string, []string, error) {
	//参考升级前,根据新endpoints里的Addresses生成IP分配
	var willAddClusterAddr, existMasterAddr, existSlaveAddr []v1.EndpointAddress
	for _, addr := range newAddresses {
		if util.InSlice(addr.IP, existedMasterInstanceIPs) {
			existMasterAddr = append(existMasterAddr, addr)
		} else if util.InSlice(addr.IP, existedSlaveInstanceIPs) {
			existSlaveAddr = append(existSlaveAddr, addr)
		} else {
			willAddClusterAddr = append(willAddClusterAddr, addr)
		}
	}

	if len(willAddClusterAddr) == 0 {
		return nil, nil, nil, nil
	}

	willAssignMasterCount := len(newAddresses)/2 - len(existedMasterInstanceIPs)

	//分配master
	var willAddClusterMasterAddr []v1.EndpointAddress

	for i := 0; i < len(willAddClusterAddr); {

		if len(willAddClusterMasterAddr) == willAssignMasterCount {
			break
		}

		isSameNode := false
		for _, existAddr := range existMasterAddr {
			if *existAddr.NodeName == *willAddClusterAddr[i].NodeName {
				isSameNode = true
				break
			}
		}

		if isSameNode {
			i++
			continue
		}

		//找到不同node的addr ip
		willAddClusterMasterAddr = append(willAddClusterMasterAddr, willAddClusterAddr[i])
		existMasterAddr = append(existMasterAddr, willAddClusterAddr[i])
		willAddClusterAddr = append(willAddClusterAddr[:i], willAddClusterAddr[i+1:]...)
	}

	//如果willAddClusterMasterAddr长度不够willAssignMasterCount则取前面的addr作为master实例
	for i := 0; len(willAddClusterMasterAddr) < willAssignMasterCount; i++ {
		willAddClusterMasterAddr = append(willAddClusterMasterAddr, willAddClusterAddr[i])
		existMasterAddr = append(existMasterAddr, willAddClusterAddr[i])
		willAddClusterAddr = append(willAddClusterAddr[:i], willAddClusterAddr[i+1:]...)
	}

	//没有slave的master Addr
	var noSlaveMasterAddr []v1.EndpointAddress
	for _, addr := range existMasterAddr {
		if _, ok := masterSlaveConnector[addr.IP]; !ok {
			noSlaveMasterAddr = append(noSlaveMasterAddr, addr)
		}
	}

	//key: nodeName value: ips
	nodeIPs := make(map[string][]v1.EndpointAddress)
	for _, addr := range willAddClusterAddr {
		nodeIPs[*addr.NodeName] = append(nodeIPs[*addr.NodeName], addr)
	}

	//将nodeIPs map的key排序,保证多次遍历map时输出顺序一致
	sortedKeys := make([]string, 0)
	for k := range nodeIPs {
		sortedKeys = append(sortedKeys, k)
	}

	// sort 'string' key in increasing order
	sort.Strings(sortedKeys)

	// all master and slave count
	nodesCount := len(willAddClusterAddr)

	// Select master instances
	var interleaved []v1.EndpointAddress
	isLoop := true
	for isLoop {
		// take one ip from each node until we run out of addr
		// across every node.
		// loop map by sortedKeys Guarantee same order loop repeatedly
		// ref：https://blog.csdn.net/slvher/article/details/44779081
		for _, key := range sortedKeys {
			if len(nodeIPs[key]) == 0 {
				if len(interleaved) == nodesCount {
					isLoop = false
					break
				}
			} else {
				interleaved = append(interleaved, nodeIPs[key][0])
				nodeIPs[key] = nodeIPs[key][1:]
			}
		}
	}

	// Rotating the list sometimes helps to get better initial anti-affinity before the optimizer runs.
	//interleaved = append(interleaved[1:], interleaved[:1]...)

	//选slave
	var willAddClusterSlaveIPs, slaveParentIps []string
	var willAddClusterSlaveAddr []v1.EndpointAddress

	for _, master := range noSlaveMasterAddr {
		if nodesCount == 0 {
			break
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
			willAddClusterSlaveIPs = append(willAddClusterSlaveIPs, instanceIP)
		} else {
			willAddClusterSlaveIPs = append(willAddClusterSlaveIPs, interleaved[0].IP)
			removeIndex = 0
		}

		//待加入slave的master addr
		slaveParentIps = append(slaveParentIps, master.IP)

		// 用于判断一组master、slave是否在同一节点上
		willAddClusterSlaveAddr = append(willAddClusterSlaveAddr, interleaved[removeIndex])

		//remove assigned addr
		// if interleaved = ["0", "1", "2"]
		// removeIndex = 0 -- >> interleaved[:0], interleaved[1:]...  -- >> ["1", "2"]
		// removeIndex = 1 -- >> interleaved[:1], interleaved[2:]...  -- >> ["0", "2"]
		// removeIndex = 2 -- >> interleaved[:2], interleaved[3:]...  -- >> ["0", "1"]
		interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)

		// nodesCount dec
		nodesCount -= 1
	}

	var willAddClusterMasterIPs []string
	for _, addr := range willAddClusterMasterAddr {
		willAddClusterMasterIPs = append(willAddClusterMasterIPs, addr.IP)
	}

	klog.V(4).Infof("\nwillAddClusterMasterIPs: %v\nwillAddClusterSlaveIPs: %v\nslaveParentIps: %v", willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps)

	return willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, nil
}

//根据更新时间从大到小排序reasons
func sortReasonsByUpdateTime(reasons *[]Reason) {
	if len(*reasons) == 0 {
		return
	}

	sort.SliceStable(*reasons, func(i, j int) bool {
		t1 := (*reasons)[i].LastTransitionTime
		t2 := (*reasons)[j].LastTransitionTime
		return t1.Time.After(t2.Time)
	})
}

//返回有效的conditions长度
func effectiveConditionLen(conditions []RedisClusterCondition) int {
	count := 0
	for _, con := range conditions {
		if con.Name != "" {
			count++
		}
	}
	return count
}
