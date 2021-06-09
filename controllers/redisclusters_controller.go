/*
Copyright 2021.

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

package controllers

import (
	"context"
	"harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	myErrors "harmonycloud.cn/middleware/redis-cluster/util/errors"
	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	"reflect"
	"runtime/debug"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redismiddlewarehccnv1alpha1 "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/pkg/operator/redis"
)

// RedisClusterReconciler reconciles a RedisCluster object
type RedisClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	Operator *redis.RedisClusterOperator
}

//+kubebuilder:rbac:groups=redis.middleware.hc.cn,resources=RedisCluster,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=redis.middleware.hc.cn,resources=RedisCluster/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=redis.middleware.hc.cn,resources=RedisCluster/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RedisCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.2/pkg/reconcile
func (r *RedisClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("RedisCluster", req.NamespacedName)
	defer func() {
		if err := recover(); err != nil {
			klog.Errorf("recover panic error: %v", err)
			debug.PrintStack()
		}
	}()
	rc := redismiddlewarehccnv1alpha1.RedisCluster{}
	err := r.Client.Get(context.TODO(),req.NamespacedName,&rc)
	if errors.IsNotFound(err) {
		klog.V(2).Infof("RedisCluster %v has been deleted", req.NamespacedName)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	redisCluster := rc.DeepCopy()
	rco := r.Operator
	// 处理异常
	defer func() {
		if err := recover(); err != nil {
			if reflect.TypeOf(err) == reflect.TypeOf(myErrors.NewErrMsg("")) {
				myerr := err.(*myErrors.Err)
				klog.Warningf("painc msg[%s] code[%d]", myerr.Msg, myerr.Code)
				if myerr.Code == myErrors.ERR_NO_MASTER {
					klog.Warningf("set master service null")
					rco.UpdateMasterRedisService(redisCluster)
				}
			} else {
				klog.Warning(err)
			}
		}
	}()

	switch redisCluster.Spec.Type {
	case "sentinel":
		// 主从
		err = rco.SyncReplication(redisCluster)
		if err != nil {
			return ctrl.Result{}, err
		}
		// 等待主从runing以后才开始运行哨兵
		if redisCluster.Status.Phase == v1alpha1.RedisClusterRunning {
			// 哨兵
			err = rco.SyncSentinel(redisCluster)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

	case "replication":
		// 主从
		rco.SyncReplication(redisCluster)
	default:
		// 默认cluster模式
		err = rco.Sync(redisCluster)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redismiddlewarehccnv1alpha1.RedisCluster{}).
		Owns(&v1.StatefulSet{}).
		Complete(r)
}
