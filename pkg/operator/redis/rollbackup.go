package redis

import (
	"context"
	"fmt"
	"harmonycloud.cn/middleware/redis-cluster/pkg/record"
	"harmonycloud.cn/middleware/redis-cluster/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"strings"
	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	rcerrors "harmonycloud.cn/middleware/redis-cluster/util/errors"
)

func (rco *RedisClusterOperator)makeRollBackup(redisCluster *RedisCluster,cur *apps.ControllerRevision,  old []*apps.ControllerRevision) (err error){
	//开始回滚updateId版本
	updateId := redisCluster.Annotations[redisAnnoRollbackUpdateIdKey]

	//相等则表示已经开始回滚
	//更新crd到updateId
	if old == nil {
		_, old, err = rco.constructHistory(redisCluster)
		if err != nil {
			return err
		}
	}
	//old中最大的版本
	_, rv := maxRevision(old)

	//先更新状态为回滚中
	newRc, err := rco.updateRedisClusterStatus(redisCluster, nil, rcerrors.NewRedisClusterState("", RedisClusterRollbacking, ""), false)
	if err != nil {
		return err
	}

	klog.V(4).Infof("redisCluster: %v/%v start rollbacking", redisCluster.Namespace, redisCluster.Name)
	rcs, err := controllerRevisionToRedisClusterSpec(rv)
	if err != nil {
		return err
	}

	horizontalScaleFlag := rco.isHorizontalScale(rcs, &redisCluster.Spec)

	klog.V(4).Infof("redisCluster: %v/%v start rollbacking horizontalScaleFlag: %v",redisCluster.Namespace, redisCluster.Name, horizontalScaleFlag)

	//如果当前crd的id和要回滚的id一样,才更新crd
	hash := ""
	if updateId == newRc.Spec.UpdateId {
		if horizontalScaleFlag {
			rco.eventRecorder.LabeledEventf(redisCluster, record.HRollbackEvent, v1.EventTypeNormal, "RollbackClusterStart", "hRollback:hRollbackClusterStart:Cluster %v is rollbacking start", redisCluster.Name)
		} else {
			rco.eventRecorder.LabeledEventf(redisCluster, record.VRollbackEvent, v1.EventTypeNormal, "RollbackClusterStart", "vRollback:vRollbackClusterStart:Cluster %v is rollbacking start", redisCluster.Name)
		}
		newRc.Spec = *rcs
		hash = rv.Labels[util.OperatorRevisionHashLabelKey]
		err := rco.frameClient.Update(context.TODO(),newRc)
		//创建pod失败回滚到上一个controller revision版本
		//_, err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Update(newRc)
		if err != nil {
			return err
		}
	} else {
		hash = cur.Labels[util.OperatorRevisionHashLabelKey]
	}

	//查询sts是否更新
	set, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(context.TODO(),redisCluster.Name,metav1.GetOptions{})
	if err != nil {
		return err
	}

	//纵向扩容失败后的回滚则返回
	if !horizontalScaleFlag {
		if hash != set.Labels[util.OperatorRevisionHashLabelKey] {
			updateSts := rco.buildRedisClusterStatefulset(redisCluster.Namespace, redisCluster.Name, redisCluster)
			util.AddLabel(updateSts.Labels, util.OperatorRevisionHashLabelKey, rv.Labels[util.OperatorRevisionHashLabelKey])
			//update statefulset
			_, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Update(context.TODO(),updateSts,metav1.UpdateOptions{})
			if err != nil {
				return err
			}
			rco.eventRecorder.LabeledEventf(redisCluster, record.VRollbackEvent, v1.EventTypeNormal, "ScalePod", "vRollback:scalePod:Cluster %v is scaling pod", redisCluster.Name)
		}

		if set.Status.UpdateRevision != set.Status.CurrentRevision || set.Status.ReadyReplicas != *set.Spec.Replicas {
			return nil
		}

		klog.V(4).Infof("redisCluster: %v/%v rollbacked", redisCluster.Namespace, redisCluster.Name)

		//回滚完成
		delete(redisCluster.Annotations, redisAnnoRollbackUpdateIdKey)
		err := rco.frameClient.Update(context.TODO(),redisCluster)
		//redisCluster, err = rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Update(redisCluster)
		if err != nil {
			return err
		}

		_, err = rco.updateRedisClusterStatus(redisCluster, nil, rcerrors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		if err != nil {
			return err
		}
		rco.eventRecorder.LabeledEventf(redisCluster, record.VRollbackEvent, v1.EventTypeNormal, "RollbackClusterSuccess", "vRollback:vRollbackClusterSuccess:Cluster %v is rollback successfully", redisCluster.Name)
		//delete
		redisClusterStopWait.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
		return err
	}

	// 如果是横向扩容失败则会到这里
	if len(redisCluster.Status.Conditions) == 0 {
		return nil
	}

	rco.eventRecorder.LabeledEventf(redisCluster, record.HRollbackEvent, v1.EventTypeNormal, "DeleteData", "hRollback:deleteData:Cluster %v is deleting data", redisCluster.Name)
	//1.删数据
	deleteDataPods := []string{fmt.Sprintf("%v-6", redisCluster.Name), fmt.Sprintf("%v-7", redisCluster.Name),
		fmt.Sprintf("%v-8", redisCluster.Name), fmt.Sprintf("%v-9", redisCluster.Name)}
	err = rco.deleteDataByJobs(redisCluster, deleteDataPods)
	if err != nil {
		return err
	}

	//statefulset没更新为回退后的版本
	if hash != set.Labels[util.OperatorRevisionHashLabelKey] {
		updateSts := rco.buildRedisClusterStatefulset(redisCluster.Namespace, redisCluster.Name, redisCluster)
		util.AddLabel(updateSts.Labels, util.OperatorRevisionHashLabelKey, rv.Labels[util.OperatorRevisionHashLabelKey])
		//update statefulset
		_, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Update(context.TODO(),updateSts,metav1.UpdateOptions{})
		rco.eventRecorder.LabeledEventf(redisCluster, record.HRollbackEvent, v1.EventTypeNormal, "ScalePod", "hRollback:scalePod:Cluster %v is scaling pod", redisCluster.Name)
		//返回,等待下一次同步
		return err
	}

	//2.sts已经更新,检测是否pod已经变为6个
	if set.Status.UpdateRevision != set.Status.CurrentRevision || set.Status.ReadyReplicas != *set.Spec.Replicas {
		//没变为6个,返回等待下一次同步
		return nil
	}

	//实例已经变为6个,开始forget节点
	var infos []*redisNodeInfo
	var podName string
	for _, con := range redisCluster.Status.Conditions {
		if con.Instance != "" && con.Status == "True" {
			podName = con.Name
			existIP := strings.Split(con.Instance, ":")[0]
			infos, err = rco.execClusterNodes(existIP, con.Namespace, con.Name, false)
			if err != nil {
				return err
			}
			break
		}
	}

	if len(infos) == 0 {
		return err
	}

	//获取到加成功的IP,需要执行forget命令.
	var addSuccessInstanceNodeIds, oldClusterInstanceIP []string
	for _, info := range infos {
		isFound := false
		for _, con := range redisCluster.Status.Conditions {
			if con.NodeId != "" {
				//老集群IP集合
				oldClusterInstanceIP = append(oldClusterInstanceIP, strings.Split(con.Instance, ":")[0])
			}
			if con.NodeId == info.NodeId {
				isFound = true
			}
		}

		if !isFound {
			//加成功的nodeId集合
			addSuccessInstanceNodeIds = append(addSuccessInstanceNodeIds, info.NodeId)
		}
	}

	if len(addSuccessInstanceNodeIds) != 0 {
		rco.eventRecorder.LabeledEventf(redisCluster, record.HRollbackEvent, v1.EventTypeNormal, "DeleteNode", "hRollback:deleteNode:Cluster %v is deleting node", redisCluster.Name)
		//3.删除加成功的节点
		err = rco.forgetNodeFromRedisCluster(podName, redisCluster.Namespace, addSuccessInstanceNodeIds, oldClusterInstanceIP)
		if err != nil {
			return err
		}
	}

	klog.V(4).Infof("redisCluster: %v/%v rollbacked", redisCluster.Namespace, redisCluster.Name)

	if err == nil {
		//回滚完成
		delete(redisCluster.Annotations, redisAnnoRollbackUpdateIdKey)
		err := rco.frameClient.Status().Update(context.TODO(),redisCluster)
		rco.frameClient.Get(context.TODO(),types.NamespacedName{redisCluster.Namespace,redisCluster.Name},redisCluster)

		if err != nil {
			return err
		}
		_, err = rco.updateRedisClusterStatus(redisCluster, nil, rcerrors.NewRedisClusterState("", RedisClusterRunning, ""), false)
		//delete
		redisClusterStopWait.Delete(fmt.Sprintf("%v/%v", redisCluster.Namespace, redisCluster.Name))
		rco.eventRecorder.LabeledEventf(redisCluster, record.HRollbackEvent, v1.EventTypeNormal, "RollbackClusterSuccess", "hRollback:hRollbackClusterSuccess:Cluster %v is rollback successfully", redisCluster.Name)
		return err
	}
	return err
}
