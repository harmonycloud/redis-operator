package util

import (
	"harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
)

//确定是否需要更新status,防止频繁更新
func NotUpdateRedisClusterStatus(a, b v1alpha1.RedisClusterStatus) bool {

	if a.Replicas != b.Replicas || a.Phase != b.Phase || a.ExporterAddr != b.ExporterAddr || a.ExporterDomainName != b.ExporterDomainName {
		return false
	}

	//新的condition长度小于旧的则不更新,避免把创建好的集群清除掉
	if len(a.Conditions) < len(b.Conditions) {
		return true
	}

	if len(a.Conditions) != len(b.Conditions) {
		return false
	}

	if len(a.Conditions) == 0 {
		return true
	}

	for i, v := range a.Conditions {
		if v.Reason != b.Conditions[i].Reason ||
			v.DomainName != b.Conditions[i].DomainName ||
			v.Status != b.Conditions[i].Status ||
			v.Name != b.Conditions[i].Name ||
			v.Namespace != b.Conditions[i].Namespace ||
			v.NodeId != b.Conditions[i].NodeId ||
			v.Slots != b.Conditions[i].Slots ||
			v.Type != b.Conditions[i].Type ||
			v.HostIP != b.Conditions[i].HostIP ||
			v.Message != b.Conditions[i].Message ||
			v.MasterNodeId != b.Conditions[i].MasterNodeId ||
			v.Hostname != b.Conditions[i].Hostname ||
			v.Instance != b.Conditions[i].Instance {
			return false
		}
	}

	//其他字段都相等,新Reason为空,则不去更新status,避免覆盖老的Reason
	if len(a.Reason) == 0 {
		return true
	}

	//其他字段都相等,新Reason不为空,且新旧不同,则去更新status
	if a.Reason != b.Reason {
		return false
	}

	//其他字段都相等,新ReasonType为空,则不去更新status,避免覆盖老的ReasonType
	if len(a.ReasonType) == 0 {
		return true
	}

	//其他字段都相等,新ReasonType不为空,且新旧不同,则去更新status
	if a.ReasonType != b.ReasonType {
		return false
	}

	return true
}

func IsRedisClusterPasswordChanged(newRedisClusterSpec, oldRedisClusterSpec *v1alpha1.RedisClusterSpec) bool {
	if newRedisClusterSpec == nil || oldRedisClusterSpec == nil {
		return true
	}

	if newRedisClusterSpec.Pod[0].RequirePass != oldRedisClusterSpec.Pod[0].RequirePass {
		return true
	}

	return false
}
