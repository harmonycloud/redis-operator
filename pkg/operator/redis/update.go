/*
Copyright 2017 The Kubernetes Authors.

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

package redis

import (
	"bytes"
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sort"

	"k8s.io/klog"
	//labelsutil "k8s.io/kubernetes/pkg/util/labels"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	//labelsutil "k8s.io/kubernetes/pkg/util/labels"
)

func (rco *RedisClusterOperator) isHorizontalScale(curRedisClusterSpec, oldRedisClusterSpec *RedisClusterSpec) bool {
	if *curRedisClusterSpec.Replicas != *oldRedisClusterSpec.Replicas {
		return true
	}

	//先不支持缩容
	return false
}

func (rco *RedisClusterOperator) isVerticalScale(curRedisClusterSpec, oldRedisClusterSpec *RedisClusterSpec) bool {
	var curMaxMemory string
	for _, env := range curRedisClusterSpec.Pod[0].Env {
		if env.Name == "MAXMEMORY" {
			curMaxMemory = env.Value
			break
		}
	}

	var oldMaxMemory string
	for _, env := range oldRedisClusterSpec.Pod[0].Env {
		if env.Name == "MAXMEMORY" {
			oldMaxMemory = env.Value
			break
		}
	}
	// 需要纵向扩容
	// TODO pv容量怎么判断变化?
	if !reflect.DeepEqual(curRedisClusterSpec.Pod[0].Resources, oldRedisClusterSpec.Pod[0].Resources) ||
		curMaxMemory != oldMaxMemory {
		return true
	}

	return false
}

func (rco *RedisClusterOperator) convertControllerRevision(rc *RedisCluster, cur *apps.ControllerRevision, olds []*apps.ControllerRevision) (*RedisClusterSpec, *RedisClusterSpec, error) {

	//看最新statefulset用的是哪个版本
	sts, err := rco.getStatefulSetForRedisCluster(rc)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't get statefulset for redis cluster %q: %v", rc.Name, err)
	}

	/*sort.SliceStable(old, func(i, j int) bool {
		revision1 := old[i].Revision
		revision2 := old[j].Revision
		return revision1 > revision2
	})*/

	//判断statefulset使用的是哪个rediscluster生成的
	var liveStsRevision *apps.ControllerRevision
	for _, old := range olds {
		if old.Labels[util.OperatorRevisionHashLabelKey] == sts.Labels[util.OperatorRevisionHashLabelKey] {
			liveStsRevision = old
			break
		}
	}

	//转换cur到RedisClusterSpec
	curSpec, err := controllerRevisionToRedisClusterSpec(cur)
	if err != nil {
		return nil, nil, err
	}

	//说明最新statefulset用的是cur版本
	if liveStsRevision == nil {
		return curSpec, curSpec, nil
	}

	//转换old到RedisClusterSpec
	oldSpec, err := controllerRevisionToRedisClusterSpec(liveStsRevision)
	if err != nil {
		return nil, nil, err
	}

	//map转struct会报错
	/*	err = mapstructure.Decode((curObjCopy["spec"].(map[string]interface{}))["spec"], &curRedisClusterSpec)
		if err != nil {
			return nil, nil, err
		}

		err = mapstructure.Decode((oldObjCopy["spec"].(map[string]interface{}))["spec"], &oldRedisClusterSpec)
		if err != nil {
			return nil, nil, err
		}*/

	return curSpec, oldSpec, nil
}

//转换ControllerRevision到RedisClusterSpec
func controllerRevisionToRedisClusterSpec(rev *apps.ControllerRevision) (rcs *RedisClusterSpec, err error) {
	oldObjCopy := make(map[string]interface{})
	rcs = &RedisClusterSpec{}
	err = json.Unmarshal(rev.Data.Raw, &oldObjCopy)
	if err != nil {
		return rcs, err
	}

	tempSpec, err := json.Marshal((oldObjCopy["spec"].(map[string]interface{}))["spec"])
	if err != nil {
		klog.Errorf("RedisCluster: %v/%v error: %v ", rev.Namespace, rev.Annotations[MiddlewareRedisClusterNameKey], err)
		return rcs, err
	}

	err = json.Unmarshal(tempSpec, rcs)
	if err != nil {
		klog.Errorf("RedisCluster: %v/%v error: %v ", rev.Namespace, rev.Annotations[MiddlewareRedisClusterNameKey], err)
		return rcs, err
	}
	return
}

// constructHistory finds all histories controlled by the given RedisCluster, and
// update current history revision number, or create current history if need to.
// It also deduplicates current history, and adds missing unique labels to existing histories.
// constructHistory查找由给定RedisCluster控制的所有历史记录，并
// 更新当前历史记录修订版号，或者在需要时创建当前历史记录。
// 它还会对当前历史记录进行重复数据删除，并为现有历史记录添加缺少的唯一标签。
func (rco *RedisClusterOperator) constructHistory(rc *RedisCluster) (cur *apps.ControllerRevision, old []*apps.ControllerRevision, err error) {
	var histories []*apps.ControllerRevision
	var currentHistories []*apps.ControllerRevision
	histories, err = rco.controlledHistories(rc)
	if err != nil {
		return nil, nil, err
	}
	for _, history := range histories {
		// Add the unique label if it's not already added to the history
		// We use history name instead of computing hash, so that we don't need to worry about hash collision
		//如果尚未添加到历史记录中，请添加唯一标签
		// 我们使用历史名称而不是计算哈希值，这样我们就不必担心哈希冲突了
		if _, ok := history.Labels[util.OperatorRevisionHashLabelKey]; !ok {
			toUpdate := history.DeepCopy()
			toUpdate.Labels[util.OperatorRevisionHashLabelKey] = toUpdate.Name
			history, err = rco.defaultClient.AppsV1().ControllerRevisions(rc.Namespace).Update(context.TODO(),toUpdate,metav1.UpdateOptions{})
			if err != nil {
				return nil, nil, err
			}
		}
		// Compare histories with ds to separate cur and old history
		//将历史与ds进行比较以分离历史和旧历史
		found := false
		found, err = Match(rc, history)
		if err != nil {
			return nil, nil, err
		}
		if found {
			currentHistories = append(currentHistories, history)
		} else {
			old = append(old, history)
		}
	}

	//当前revision应该等于old的max revision加1
	max, _ := maxRevision(old)
	currRevision := max + 1
	switch len(currentHistories) {
	case 0:
		// Create a new history if the current one isn't found
		cur, err = rco.snapshot(rc, currRevision)
		if err != nil {
			return nil, nil, err
		}
	default:
		cur, err = rco.dedupCurHistories(rc, currentHistories)
		if err != nil {
			return nil, nil, err
		}
		// Update revision number if necessary
		if cur.Revision < currRevision {
			toUpdate := cur.DeepCopy()
			toUpdate.Revision = currRevision
			_, err = rco.defaultClient.AppsV1().ControllerRevisions(rc.Namespace).Update(context.TODO(),toUpdate,metav1.UpdateOptions{})
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return cur, old, err
}

//cur表示和当前redisCluster一样的版本spec记录
//old代表所有和当前redisCluster不一样的版本spec记录
func (rco *RedisClusterOperator) cleanupHistory(rc *RedisCluster, old []*apps.ControllerRevision) error {

	sts, err := rco.getStatefulSetForRedisCluster(rc)
	if err != nil {
		return fmt.Errorf("couldn't get statefulset for redis cluster %q: %v", rc.Name, err)
	}

	if sts == nil {
		klog.Warningf("RedisCluster: %v/%v statefulset is not exist", rc.Namespace, rc.Name)
		return nil
	}

	//获取历史记录最高个数
	toKeep := int(20)
	toKill := len(old) - toKeep
	if toKill <= 0 {
		return nil
	}

	// Find all hashes of live pods
	liveHashes := make(map[string]bool)
	if hash := sts.Labels[util.OperatorRevisionHashLabelKey]; len(hash) > 0 {
		liveHashes[hash] = true
	}

	// Find all live history with the above hashes
	liveHistory := make(map[string]bool)
	for _, history := range old {
		if hash := history.Labels[util.OperatorRevisionHashLabelKey]; liveHashes[hash] {
			liveHistory[history.Name] = true
		}
	}

	// Clean up old history from smallest to highest revision (from oldest to newest)
	//revision从小到大排列
	sort.Sort(historiesByRevision(old))
	for _, history := range old {
		if toKill <= 0 {
			break
		}
		if liveHistory[history.Name] {
			continue
		}
		// Clean up
		err := rco.defaultClient.AppsV1beta1().ControllerRevisions(rc.Namespace).Delete(context.TODO(),history.Name, metav1.DeleteOptions{})
		if err != nil {
			return err
		}
		toKill--
	}
	return nil
}

// maxRevision returns the max revision number of the given list of histories
func maxRevision(histories []*apps.ControllerRevision) (int64, *apps.ControllerRevision) {
	max := int64(0)
	maxControllerRevision := &apps.ControllerRevision{}
	if len(histories) != 0 {
		maxControllerRevision = histories[0]
	}
	for _, history := range histories {
		if history.Revision > max {
			max = history.Revision
			maxControllerRevision = history
		}
	}
	return max, maxControllerRevision
}

func (rco *RedisClusterOperator) dedupCurHistories(rc *RedisCluster, curHistories []*apps.ControllerRevision) (*apps.ControllerRevision, error) {
	if len(curHistories) == 1 {
		return curHistories[0], nil
	}
	var maxRevision int64
	var keepCur *apps.ControllerRevision
	for _, cur := range curHistories {
		if cur.Revision >= maxRevision {
			keepCur = cur
			maxRevision = cur.Revision
		}
	}
	// Clean up duplicates and relabel sts
	for _, cur := range curHistories {
		if cur.Name == keepCur.Name {
			continue
		}
		// Relabel sts before dedup
		sts, err := rco.getStatefulSetForRedisCluster(rc)
		if err != nil {
			return nil, err
		}

		if sts.Labels[util.OperatorRevisionHashLabelKey] != keepCur.Labels[util.OperatorRevisionHashLabelKey] {
			toUpdate := sts.DeepCopy()
			if toUpdate.Labels == nil {
				toUpdate.Labels = make(map[string]string)
			}
			toUpdate.Labels[util.OperatorRevisionHashLabelKey] = keepCur.Labels[util.OperatorRevisionHashLabelKey]
			_, err = rco.defaultClient.AppsV1().StatefulSets(rc.Namespace).Update(context.TODO(),toUpdate,metav1.UpdateOptions{})
			if err != nil {
				return nil, err
			}
		}
		// Remove duplicates
		err = rco.defaultClient.AppsV1beta1().ControllerRevisions(rc.Namespace).Delete(context.TODO(),cur.Name, metav1.DeleteOptions{})
		if err != nil {
			return nil, err
		}
	}
	return keepCur, nil
}

// controlledHistories returns all ControllerRevisions controlled by the given RedisCluster.
// This also reconciles ControllerRef by adopting/orphaning.
// Note that returned histories are pointers to objects in the cache.
// If you want to modify one, you need to deep-copy it first.
// controlledHistories返回由给定RedisCluster控制的所有ControllerRevisions。
// 这也通过采用/孤立来协调ControllerRef。
// 请注意，返回的历史记录是指向缓存中对象的指针。
// 如果要修改一个，则需要先对其进行深层复制。
func (rco *RedisClusterOperator) controlledHistories(rc *RedisCluster) ([]*apps.ControllerRevision, error) {

	// List all histories to include those that don't match the selector anymore
	// but have a ControllerRef pointing to the controller.
	//列出所有历史记录以包含那些与选择器不匹配的历史记录
	// 但是有一个指向控制器的ControllerRef。
	histories, err := rco.defaultClient.AppsV1().ControllerRevisions(rc.Namespace).List(context.TODO(),metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var historiesCR []*apps.ControllerRevision

	for _, history := range histories.Items {
		controllerRef := metav1.GetControllerOf(&history)
		if controllerRef == nil || controllerRef.Kind != controllerKind.Kind {
			continue
		}

		if controllerRef.Name == rc.Name {
			historiesCR = append(historiesCR, &history)
		}
	}

	return historiesCR, nil
}

// Match check if the given RedisCluster's template matches the template stored in the given history.
func Match(rc *RedisCluster, history *apps.ControllerRevision) (bool, error) {
	patch, err := getPatch(rc)
	if err != nil {
		return false, err
	}
	return bytes.Equal(patch, history.Data.Raw), nil
}

// getPatch returns a strategic merge patch that can be applied to restore a RedisCluster to a
// previous version. If the returned error is nil the patch is valid. The current state that we save is just the
// PodSpecTemplate. We can modify this later to encompass more state (or less) and remain compatible with previously
// recorded patches.
func getPatch(rc *RedisCluster) ([]byte, error) {
	rcBytes, err := json.Marshal(rc)
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	err = json.Unmarshal(rcBytes, &raw)
	if err != nil {
		return nil, err
	}
	objCopy := make(map[string]interface{})
	specCopy := make(map[string]interface{})

	specCopy["spec"] = raw["spec"].(map[string]interface{})
	specCopy["$patch"] = "replace"
	objCopy["spec"] = specCopy
	patch, err := json.Marshal(objCopy)
	return patch, err
}

func (rco *RedisClusterOperator) snapshot(rc *RedisCluster, revision int64) (*apps.ControllerRevision, error) {
	patch, err := getPatch(rc)
	if err != nil {
		return nil, err
	}

	labels := make(map[string]string)
	if rc.Spec.UpdateId != "" {
		labels["updateId"] = rc.Spec.UpdateId
	}

	hash := util.ComputeHash(&rc.Spec, rc.Status.CollisionCount)
	name := rc.Name + "-" + hash
	history := &apps.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       rc.Namespace,
			Labels:          util.AddLabel(labels, util.OperatorRevisionHashLabelKey, hash),
			Annotations:     map[string]string{MiddlewareRedisClusterNameKey: rc.Name},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rc, controllerKind)},
		},
		Data:     runtime.RawExtension{Raw: patch},
		Revision: revision,
	}

	history, err = rco.defaultClient.AppsV1().ControllerRevisions(rc.Namespace).Create(context.TODO(),history,metav1.CreateOptions{})
	if outerErr := err; errors.IsAlreadyExists(outerErr) {
		// TODO: Is it okay to get from historyLister?
		existedHistory, getErr := rco.defaultClient.AppsV1().ControllerRevisions(rc.Namespace).Get(context.TODO(),name, metav1.GetOptions{})
		if getErr != nil {
			return nil, getErr
		}
		// Check if we already created it
		done, matchErr := Match(rc, existedHistory)
		if matchErr != nil {
			return nil, matchErr
		}
		if done {
			return existedHistory, nil
		}

		// Handle name collisions between different history
		// Get the latest RedisCluster from the API server to make sure collision count is only increased when necessary
		currRC := &RedisCluster{}
		getErr = rco.frameClient.Get(context.TODO(),types.NamespacedName{rc.Namespace,name},currRC)
		//currRC, getErr := rco.customCRDClient.CrV1alpha1().RedisClusters(rc.Namespace).Get(rc.Name, metav1.GetOptions{})
		if getErr != nil {
			return nil, getErr
		}
		// If the collision count used to compute hash was in fact stale, there's no need to bump collision count; retry again
		if !reflect.DeepEqual(currRC.Status.CollisionCount, rc.Status.CollisionCount) {
			return nil, fmt.Errorf("found a stale collision count (%d, expected %d) of RedisCluster %q while processing; will retry until it is updated", rc.Status.CollisionCount, currRC.Status.CollisionCount, rc.Name)
		}
		if currRC.Status.CollisionCount == nil {
			currRC.Status.CollisionCount = new(int32)
		}
		*currRC.Status.CollisionCount++
		updateErr := rco.frameClient.Status().Update(context.TODO(),currRC)
		//_, updateErr := rco.customCRDClient.CrV1alpha1().RedisClusters(rc.Namespace).UpdateStatus(currRC)
		if updateErr != nil {
			return nil, updateErr
		}
		klog.V(2).Infof("Found a hash collision for RedisCluster %q - bumping collisionCount to %d to resolve it", rc.Name, *currRC.Status.CollisionCount)
		return nil, outerErr
	}
	return history, err
}

/*func (rco *RedisClusterOperator) getAllRedisClusterPods(rc *RedisCluster, nodeToDaemonPods map[string][]*v1.Pod, hash string) ([]*v1.Pod, []*v1.Pod) {
	var newPods []*v1.Pod
	var oldPods []*v1.Pod

	for _, pods := range nodeToDaemonPods {
		for _, pod := range pods {
			// If the returned error is not nil we have a parse error.
			// The controller handles this via the hash.
			generation, err := util.GetTemplateGeneration(rc)
			if err != nil {
				generation = nil
			}
			if util.IsPodUpdated(pod, hash, generation) {
				newPods = append(newPods, pod)
			} else {
				oldPods = append(oldPods, pod)
			}
		}
	}
	return newPods, oldPods
}*/
/*
func (rco *RedisClusterOperator) getUnavailableNumbers(ds *RedisCluster, nodeToDaemonPods map[string][]*v1.Pod) (int, int, error) {
	klog.V(4).Infof("Getting unavailable numbers")
	// TODO: get nodeList once in syncRedisCluster and pass it to other functions
	nodeList, err := rco.nodeLister.List(labels.Everything())
	if err != nil {
		return -1, -1, fmt.Errorf("couldn't get list of nodes during rolling update of redis cluster %#v: %v", ds, err)
	}

	var numUnavailable, desiredNumberScheduled int
	for i := range nodeList {
		node := nodeList[i]
		wantToRun, _, _, err := rco.nodeShouldRunDaemonPod(node, ds)
		if err != nil {
			return -1, -1, err
		}
		if !wantToRun {
			continue
		}
		desiredNumberScheduled++
		daemonPods, exists := nodeToDaemonPods[node.Name]
		if !exists {
			numUnavailable++
			continue
		}
		available := false
		for _, pod := range daemonPods {
			//for the purposes of update we ensure that the Pod is both available and not terminating
			if podutil.IsPodAvailable(pod, ds.Spec.MinReadySeconds, metav1.Now()) && pod.DeletionTimestamp == nil {
				available = true
				break
			}
		}
		if !available {
			numUnavailable++
		}
	}
	maxUnavailable, err := intstrutil.GetValueFromIntOrPercent(ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable, desiredNumberScheduled, true)
	if err != nil {
		return -1, -1, fmt.Errorf("Invalid value for MaxUnavailable: %v", err)
	}
	klog.V(4).Infof(" RedisCluster %s/%s, maxUnavailable: %d, numUnavailable: %d", ds.Namespace, ds.Name, maxUnavailable, numUnavailable)
	return maxUnavailable, numUnavailable, nil
}*/

type historiesByRevision []*apps.ControllerRevision

func (h historiesByRevision) Len() int      { return len(h) }
func (h historiesByRevision) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h historiesByRevision) Less(i, j int) bool {
	return h[i].Revision < h[j].Revision
}
