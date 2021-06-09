package redis

import (
	"errors"
	"fmt"
	"log"
	"time"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/pkg/k8s"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

type RedisFailoverCheck interface {
	CheckRedisNumber(rFailover *RedisCluster) error
	CheckSentinelNumber(rFailover *RedisCluster) error

	CheckAllSlavesFromMaster(master string, rFailover *RedisCluster) error
	CheckSentinelMonitor(sentinel string, monitor ...string) (string, error)

	GetMasterIP(rFailover *RedisCluster) (string, error)
	GetMasterIPs(rf *RedisCluster) ([]string, error)
	GetAllRedisPodwithRoles(rf *RedisCluster) ([]RedisPod, error)
	// 从哨兵中获取master信息
	GetMasterIPFromSentinel(rFailover *RedisCluster) (string, error)
	GetMinimumRedisPodTime(rFailover *RedisCluster) (time.Duration, error)
	GetNumberMasters(rFailover *RedisCluster) (int, error)
	GetRedisesMasterPod(rFailover *RedisCluster) (corev1.Pod, error)
	GetRedisesSlavesPods(rFailover *RedisCluster) ([]corev1.Pod, error)

	GetSentinelsIPs(rFailover *RedisCluster) ([]string, error)
	GetRedisesMasterFromSentinel(redisCluster *RedisCluster) (string, string, error)
	GetPodFromIP(ip string) (corev1.Pod, error)
}
type RedisFailoverChecker struct {
	k8sService  k8s.Services
	redisClient Client
	logger      log.Logger
}

func NewRedisFailoverChecker(k8sService k8s.Services, redisClient Client) *RedisFailoverChecker {
	return &RedisFailoverChecker{
		k8sService:  k8sService,
		redisClient: redisClient,
	}
}

// CheckRedisNumber controlls that the number of deployed redis is the same than the requested on the spec
func (r *RedisFailoverChecker) CheckRedisNumber(rf *RedisCluster) error {
	ss, err := r.k8sService.GetStatefulSet(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return err
	}
	if *rf.Spec.Replicas != *ss.Spec.Replicas {
		return errors.New("number of redis pods differ from specification")
	}
	return nil
}

func (r *RedisFailoverChecker) GetMinimumRedisPodTime(rf *RedisCluster) (time.Duration, error) {
	minTime := 100000 * time.Hour // More than ten years
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return minTime, err
	}
	for _, redisNode := range rps.Items {
		if redisNode.Status.StartTime == nil {
			continue
		}
		start := redisNode.Status.StartTime.Round(time.Second)
		alive := time.Now().Sub(start)
		klog.V(4).Infof("Pod %s has been alive for %.f seconds", redisNode.Status.PodIP, alive.Seconds())
		if alive < minTime {
			minTime = alive
		}
	}
	return minTime, nil
}

type RedisPod struct {
	IsMaster bool
	Pod      corev1.Pod
}

func (r *RedisFailoverChecker) GetAllRedisPodwithRoles(rf *RedisCluster) ([]RedisPod, error) {
	rps := []RedisPod{}
	pods, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return rps, err
	}
	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return rps, err
	}

	for _, pod := range pods.Items {
		master, err := r.redisClient.IsMaster(pod.Status.PodIP, password)
		if err != nil {
			return rps, err
		}
		if master {
			rps = append(rps, RedisPod{Pod: pod, IsMaster: true})
		} else {
			rps = append(rps, RedisPod{Pod: pod, IsMaster: false})

		}
	}
	return rps, err
}

// GetNumberMasters returns the number of redis nodes that are working as a master
func (r *RedisFailoverChecker) GetNumberMasters(rf *RedisCluster) (int, error) {
	nMasters := 0
	rips, err := r.GetRedisesIPs(rf)
	if err != nil {
		return nMasters, err
	}

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return nMasters, err
	}

	for _, rip := range rips {
		master, err := r.redisClient.IsMaster(rip, password)
		if err != nil {
			return nMasters, err
		}
		if master {
			nMasters++
		}
	}
	return nMasters, nil
}

// GetRedisesIPs returns the IPs of the Redis nodes
func (r *RedisFailoverChecker) GetRedisesIPs(rf *RedisCluster) ([]string, error) {
	redises := []string{}
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return nil, err
	}
	for _, rp := range rps.Items {
		if rp.Status.Phase == corev1.PodRunning && rp.DeletionTimestamp == nil { // Only work with running pods
			if len(rp.Status.Conditions) > 1 && rp.Status.Conditions[1].Status == "True" {
				redises = append(redises, rp.Status.PodIP)
			}

		}
	}
	return redises, nil
}

func (r *RedisFailoverChecker) GetRedisesPods(rf *RedisCluster) ([]corev1.Pod, error) {
	redises := []corev1.Pod{}
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return nil, err
	}
	for _, rp := range rps.Items {
		if rp.Status.Phase == corev1.PodRunning && rp.DeletionTimestamp == nil { // Only work with running pods
			redises = append(redises, rp)
		}
	}
	return redises, nil
}

// GetMasterIP connects to all redis and returns the master of the redis failover
func (r *RedisFailoverChecker) GetMasterIP(rf *RedisCluster) (string, error) {

	masters, err := r.GetMasterIPs(rf)
	if err != nil {
		return "", err
	}
	if len(masters) != 1 {
		klog.Warning("number of redis nodes known as master is different than 1")
		return "", nil
	}
	return masters[0], nil
}

func (r *RedisFailoverChecker) GetMasterIPs(rf *RedisCluster) ([]string, error) {
	masters := []string{}
	rips, err := r.GetRedisesIPs(rf)
	if err != nil {
		return masters, err
	}

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return masters, err
	}

	for _, rip := range rips {
		master, err := r.redisClient.IsMaster(rip, password)
		if err != nil {
			return masters, err
		}
		if master {
			masters = append(masters, rip)
		}
	}
	return masters, nil
}

// 检查所有的slave是否都是指向该master
func (r *RedisFailoverChecker) CheckAllSlavesFromMaster(master string, rf *RedisCluster) error {
	rips, err := r.GetRedisesIPs(rf)
	if err != nil {
		return err
	}

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}

	for _, rip := range rips {
		slaveof, err := r.redisClient.GetSlaveOf(rip, password)
		if err != nil {
			return err
		}
		if slaveof != "" && slaveof != master {
			return fmt.Errorf("node %s don't have the master %s, has %s", rip, master, slaveof)
		}
	}
	return nil
}

// GetRedisesMasterPod returns pods names of the Redis slave nodes
func (r *RedisFailoverChecker) GetRedisesMasterPod(rc *RedisCluster) (*corev1.Pod, error) {
	rps, err := r.k8sService.GetStatefulSetPods(rc.Namespace, GetRedisName(rc))
	if err != nil {
		return nil, err
	}

	password, err := GetRedisPassword(r.k8sService, rc)
	if err != nil {
		return nil, err
	}

	for _, rp := range rps.Items {
		if rp.Status.Phase == corev1.PodRunning && rp.DeletionTimestamp == nil { // Only work with running
			master, err := r.redisClient.IsMaster(rp.Status.PodIP, password)
			if err != nil {
				return nil, err
			}
			if master {
				return &rp, nil
			}
		}
	}
	return nil, errors.New("redis nodes known as master not found")
}

// GetRedisesSlavesPods returns pods names of the Redis slave nodes
func (r *RedisFailoverChecker) GetRedisesSlavesPods(rf *RedisCluster) ([]corev1.Pod, error) {
	redises := []corev1.Pod{}
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return nil, err
	}

	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return redises, err
	}

	for _, rp := range rps.Items {
		if rp.Status.Phase == corev1.PodRunning && rp.DeletionTimestamp == nil { // Only work with running
			master, err := r.redisClient.IsMaster(rp.Status.PodIP, password)
			if err != nil {
				return []corev1.Pod{}, err
			}
			if !master {
				redises = append(redises, rp)
			}
		}
	}
	return redises, nil
}

func (r *RedisFailoverChecker) GetRedisesSentinelPod(rf *RedisCluster) ([]corev1.Pod, error) {
	sentinels := []corev1.Pod{}
	rps, err := r.k8sService.GetDeploymentPods(rf.Namespace, GetSentinelName(rf))
	if err != nil {
		return nil, err
	}
	for _, sp := range rps.Items {
		if sp.Status.Phase == corev1.PodRunning && sp.DeletionTimestamp == nil { // Only work with running pods
			sentinels = append(sentinels, sp)
		}
	}
	return sentinels, nil
}

// 从哨兵中获取master
func (r *RedisFailoverChecker) GetRedisesMasterFromSentinel(redisCluster *RedisCluster) (string, string, error) {
	sips, err := r.GetSentinelsIPs(redisCluster)
	if err != nil {
		return "", "", err
	}
	if len(sips) == 0 {
		return "", "", fmt.Errorf("not find sentinel")
	}
	return r.redisClient.GetSentinelMonitor(sips[0])
}

// GetSentinelsIPs returns the IPs of the Sentinel nodes
func (r *RedisFailoverChecker) GetSentinelsIPs(rf *RedisCluster) ([]string, error) {
	sentinels := []string{}
	rps, err := r.k8sService.GetDeploymentPods(rf.Namespace, GetSentinelName(rf))
	if err != nil {
		return nil, err
	}
	for _, sp := range rps.Items {
		if sp.Status.Phase == corev1.PodRunning && sp.DeletionTimestamp == nil { // Only work with running pods
			sentinels = append(sentinels, sp.Status.PodIP)
		}
	}
	return sentinels, nil
}

func (r *RedisFailoverChecker) GetPodFromIP(rf *RedisCluster, ip string) (*corev1.Pod, error) {
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return nil, err
	}
	for _, pod := range rps.Items {
		if pod.Status.PodIP == ip {
			return &pod, nil
		}
	}
	return nil, fmt.Errorf("not find pod from ip")
}

// CheckSentinelMonitor controls if the sentinels are monitoring the expected master
func (r *RedisFailoverChecker) CheckSentinelMonitor(sentinel string, monitor ...string) (string, error) {
	actualMonitorIP := ""
	monitorIP := monitor[0]
	monitorPort := ""
	if len(monitor) > 1 {
		monitorPort = monitor[1]
	}
	actualMonitorIP, actualMonitorPort, err := r.redisClient.GetSentinelMonitor(sentinel)
	if err != nil {
		return actualMonitorIP, err
	}
	if actualMonitorIP != monitorIP || (monitorPort != "" && monitorPort != actualMonitorPort) {
		return actualMonitorIP, fmt.Errorf("the monitor[%s] on the sentinel config does not match with the expected one, actualMonitorIP[%s]", sentinel, actualMonitorIP)
	}
	return actualMonitorIP, nil
}

// CheckSentinelNumberInMemory controls that the provided sentinel has only the living sentinels on its memory.
func (r *RedisFailoverChecker) CheckSentinelNumberInMemory(sentinel string, rf *RedisCluster) error {
	nSentinels, err := r.redisClient.GetNumberSentinelsInMemory(sentinel)
	if err != nil {
		return err
	} else if nSentinels != *rf.Spec.Sentinel.Replicas {
		return errors.New("sentinels in memory mismatch")
	}
	return nil
}

// CheckSentinelSlavesNumberInMemory controls that the provided sentinel has only the expected slaves number.
func (r *RedisFailoverChecker) CheckSentinelSlavesNumberInMemory(sentinel string, rf *RedisCluster) error {
	nSlaves, err := r.redisClient.GetNumberSentinelSlavesInMemory(sentinel)
	if err != nil {
		return err
	} else if nSlaves != *rf.Spec.Replicas-1 {
		return errors.New("redis slaves in sentinel memory mismatch")
	}
	return nil
}

// CheckSentinelNumber controlls that the number of deployed sentinel is the same than the requested on the spec
func (r *RedisFailoverChecker) CheckSentinelNumber(rf *RedisCluster) error {
	d, err := r.k8sService.GetDeployment(rf.Namespace, GetSentinelName(rf))
	if err != nil {
		return err
	}
	if *rf.Spec.Sentinel.Replicas != *d.Spec.Replicas {
		return errors.New("number of sentinel pods differ from specification")
	}
	return nil
}

func (r *RedisFailoverChecker) Set(rf *RedisCluster, ip, key string, v string) error {
	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return err
	}
	return r.redisClient.Set(ip, password, key, v)
}

func (r *RedisFailoverChecker) Get(rf *RedisCluster, ip, key string) (string, error) {
	password, err := GetRedisPassword(r.k8sService, rf)
	if err != nil {
		return "", err
	}
	return r.redisClient.Get(ip, password, key)
}
