package redis

import (
	"errors"
	"sort"
	"strconv"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/pkg/k8s"
	"k8s.io/klog"
)

type RedisFailoverHeal interface {
	MakeMaster(ip string, r *RedisCluster) error
	MakeSlaveOf(master string, slave string, r *RedisCluster) error
	SetOldestAsMaster(rFailover *RedisCluster) error
	SetMasterOnAll(masterIP string, rFailover *RedisCluster) error
	NewSentinelMonitor(ip string, monitor string, rFailover *RedisCluster) error
	RestoreSentinel(ip string) error
}

type RedisFailoverHealer struct {
	k8sService  k8s.Services
	redisClient Client
	// logger      log.Logger
}

func NewRedisFailoverHealer(k8sService k8s.Services, redisClient Client) *RedisFailoverHealer {
	return &RedisFailoverHealer{
		k8sService:  k8sService,
		redisClient: redisClient,
		// logger:      logger,
	}
}

func (r *RedisFailoverHealer) MakeMaster(ip string, rf *RedisCluster) error {
	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}

	return r.redisClient.MakeMaster(ip, password)
}

func (r *RedisFailoverHealer) MakeSlaveOf(master string, slave string, rf *RedisCluster) error {
	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}

	return r.redisClient.MakeSlaveOf(slave, master, password)
}
func GetRedisPassword(s k8s.Services, rf *RedisCluster) (string, error) {
	return rf.Spec.Pod[0].RequirePass, nil
}

// SetOldestAsMaster puts all redis to the same master, choosen by order of appearance
func (r *RedisFailoverHealer) SetOldestAsMaster(rf *RedisCluster) error {
	ssp, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return err
	}
	if len(ssp.Items) < 1 {
		return errors.New("number of redis pods are 0")
	}

	// Order the pods so we start by the oldest one
	sort.Slice(ssp.Items, func(i, j int) bool {
		return ssp.Items[i].CreationTimestamp.Before(&ssp.Items[j].CreationTimestamp)
	})

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}
	newMasterIP := ""
	for _, pod := range ssp.Items {
		if newMasterIP == "" {
			newMasterIP = pod.Status.PodIP
			klog.V(4).Infof("New master is %s with ip %s", pod.Name, newMasterIP)
			if err := r.redisClient.MakeMaster(newMasterIP, password); err != nil {
				return err
			}
		} else {
			klog.V(4).Infof("Making pod %s slave of %s", pod.Name, newMasterIP)
			if err := r.redisClient.MakeSlaveOf(pod.Status.PodIP, newMasterIP, password); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetMasterOnAll puts all redis nodes as a slave of a given master
func (r *RedisFailoverHealer) SetMasterOnAll(masterIP string, rf *RedisCluster) error {
	ssp, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return err
	}

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}

	for _, pod := range ssp.Items {
		if pod.Status.PodIP == masterIP {
			klog.V(4).Infof("Ensure pod %s is master", pod.Name)
			if err := r.redisClient.MakeMaster(masterIP, password); err != nil {
				return err
			}
		} else {
			klog.V(4).Infof("Making pod %s slave of %s", pod.Name, masterIP)
			if err := r.redisClient.MakeSlaveOf(pod.Status.PodIP, masterIP, password); err != nil {
				return err
			}
		}
	}
	return nil
}

func getQuorum(rf *RedisCluster) int32 {
	return *(rf.Spec.Sentinel.Replicas)/2 + 1
}

// NewSentinelMonitor changes the master that Sentinel has to monitor
func (r *RedisFailoverHealer) NewSentinelMonitor(ip string, monitor string, rf *RedisCluster) error {
	klog.V(1).Infof("Sentinel is not monitoring the correct master, changing...")
	quorum := strconv.Itoa(int(getQuorum(rf)))

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}

	return r.redisClient.MonitorRedis(ip, monitor, quorum, password)
}

// RestoreSentinel clear the number of sentinels on memory
func (r *RedisFailoverHealer) RestoreSentinel(ip string) error {
	// r.logger.Debugf("Restoring sentinel %s...", ip)
	return r.redisClient.ResetSentinel(ip)
}

// SetSentinelCustomConfig will call sentinel to set the configuration given in config
func (r *RedisFailoverHealer) SetSentinelCustomConfig(ip string, rf *RedisCluster) error {
	// r.logger.Debugf("Setting the custom config on sentinel %s...", ip)
	return r.redisClient.SetCustomSentinelConfig(ip, []string{})
}
