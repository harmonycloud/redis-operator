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

package v1alpha1

import (
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.



////+kubebuilder:object:root=true
////+kubebuilder:subresource:status
//
//// RedisCluster is the Schema for the RedisCluster API
//type RedisCluster struct {
//	metav1.TypeMeta   `json:",inline"`
//	metav1.ObjectMeta `json:"metadata,omitempty"`
//
//	Spec   RedisClusterSpec   `json:"spec,omitempty"`
//	Status RedisClusterStatus `json:"status,omitempty"`
//}
//
////+kubebuilder:object:root=true
//
//// RedisClusterList contains a list of RedisCluster
//type RedisClusterList struct {
//	metav1.TypeMeta `json:",inline"`
//	metav1.ListMeta `json:"metadata,omitempty"`
//	Items           []RedisCluster `json:"items"`
//}

func init() {
	SchemeBuilder.Register(&RedisCluster{}, &RedisClusterList{})
}

// RedisClusterUpdateStrategyType is a string enumeration type that enumerates
// all possible update strategies for the RedisCluster.
const (
	MiddlewareRedisTypeKey        = "redis.middleware.hc.cn"
	MiddlewareRedisClustersPrefix  = "RedisCluster-"
	MiddlewareRedisClusterNameKey = "middleware.redis.cluster.name"
)

type RedisClusterUpdateStrategyType string

const (
	AssignReceiveStrategyType RedisClusterUpdateStrategyType = "AssignReceive"
	AutoReceiveStrategyType   RedisClusterUpdateStrategyType = "AutoReceive"
)

type RedisClusterConditionType string

const (
	MasterConditionType RedisClusterConditionType = "master"
	SlaveConditionType  RedisClusterConditionType = "slave"
)

type RedisClusterPhase string
type RedisClusterReasonType string

// These are the valid phases of a RedisCluster.
const (
	// RedisClusterReasonTypeCreatePodFailedWhenCreateCluster means the RedisCluster is CreatePodFailedWhenCreateCluster
	RedisClusterReasonTypeCreatePodFailedWhenCreateCluster RedisClusterReasonType = "CreatePodFailedWhenCreateCluster"

	// RedisClusterReasonTypeCreatePodFailedWhenUpgradeCluster means the RedisCluster is CreatePodFailedWhenUpgradeCluster
	RedisClusterReasonTypeCreatePodFailedWhenUpgradeCluster RedisClusterReasonType = "CreatePodFailedWhenUpgradeCluster"

	// RedisClusterReasonTypeInitClusterFailed means the RedisCluster crd is first create InitClusterFailed
	RedisClusterReasonTypeInitClusterFailed RedisClusterReasonType = "InitClusterFailed"
	// RedisClusterReasonTypeAddSlaveFailedWhenCreateCluster means the RedisCluster is AddSlaveFailedWhenCreateCluster
	RedisClusterReasonTypeAddSlaveFailedWhenCreateCluster RedisClusterReasonType = "AddSlaveFailedWhenCreateCluster"

	// RedisClusterReasonTypeAddSlaveFailedWhenUpgradeCluster means the RedisCluster is AddSlaveFailedWhenUpgradeCluster
	RedisClusterReasonTypeAddSlaveFailedWhenUpgradeCluster RedisClusterReasonType = "AddSlaveFailedWhenUpgradeCluster"
	// RedisClusterReasonTypeUpgradeFailed means the RedisCluster is VerticalScaleFailed
	RedisClusterReasonTypeUpgradeFailed RedisClusterReasonType = "UpgradeFailed"
	// RedisClusterReasonTypeAddMasterFailedWhenCreateCluster means the RedisCluster is AddMasterFailedWhenCreateCluster
	RedisClusterReasonTypeAddMasterFailedWhenCreateCluster RedisClusterReasonType = "AddMasterFailedWhenCreateCluster"

	// RedisClusterReasonTypeAddMasterFailedWhenUpgradeCluster means the RedisCluster is AddMasterFailedWhenUpgradeCluster
	RedisClusterReasonTypeAddMasterFailedWhenUpgradeCluster RedisClusterReasonType = "AddMasterFailedWhenUpgradeCluster"

	// RedisClusterReasonTypeCreatePodFailed means the RedisCluster is CreatePodFailed
	RedisClusterReasonTypeCreatePodFailed RedisClusterReasonType = "CreatePodFailed"

	// RedisClusterReasonTypeRebalanceSlotsFailed means the RedisCluster is RebalanceSlotsFailed
	RedisClusterReasonTypeRebalanceSlotsFailed RedisClusterReasonType = "RebalanceSlotsFailed"
	// RedisClusterReasonTypeReshareSlotsFailed means the RedisCluster is ReshareSlotsFailed
	RedisClusterReasonTypeReshareSlotsFailed RedisClusterReasonType = "ReshareSlotsFailed"

	// RedisClusterReasonTypeReshareSlotsFailed means the RedisCluster is ReshareSlotsFailed
	RedisClusterReasonTypeInvalidArgs RedisClusterReasonType = "InvalidArgs"

	// RedisClusterReasonTypeUnexpectedError means the RedisCluster is UnexpectedError
	RedisClusterReasonTypeUnexpectedError RedisClusterReasonType = "UnexpectedError"
)

// These are the valid phases of a RedisCluster.
const (
	// RedisClusterUpgrading means the RedisCluster is Upgrading
	RedisClusterUpgrading RedisClusterPhase = "Upgrading"
	// RedisClusterNone means the RedisCluster crd is first create
	RedisClusterNone RedisClusterPhase = "None"
	// RedisClusterCreating means the RedisCluster is Creating
	RedisClusterCreating RedisClusterPhase = "Creating"
	// RedisClusterRunning means the RedisCluster is Running after RedisCluster create and initialize success
	RedisClusterRunning RedisClusterPhase = "Running"
	// RedisClusterFailed means the RedisCluster is Failed
	RedisClusterFailed RedisClusterPhase = "Failed"
	// RedisClustercaling means the RedisCluster is Scaling
	RedisClusterScaling RedisClusterPhase = "Scaling"
	// RedisClusterRestarting means the RedisCluster is Restarting
	RedisClusterRestarting RedisClusterPhase = "Restarting"
	// RedisClusterDeleting means the RedisCluster is Deleting
	RedisClusterDeleting RedisClusterPhase = "Deleting"

	// RedisClusterRollback means the RedisCluster is Rollback
	RedisClusterRollback RedisClusterPhase = "Rollback"

	// RedisClusterRollbacking means the RedisCluster is Rollbacking
	RedisClusterRollbacking RedisClusterPhase = "Rollbacking"

	RedisClusterError RedisClusterPhase = "Error"
)

// 为当前类型生成客户端
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
type RedisCluster struct {
	metav1.TypeMeta `json:",inline"`
	// Metadata is the standard object metadata.
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification for the behaviour of the rediscluster
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#spec-and-status.
	// +optional
	Spec RedisClusterSpec `json:"spec,omitempty"`

	// Status is the current information about the rediscluster.
	// +optional
	Status RedisClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true
type RedisClusterList struct {
	metav1.TypeMeta `json:",inline"`
	// Metadata is the standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata"`

	// Items is the list of RedisCluster objects.
	Items []RedisCluster `json:"items"`
}

// RedisClusterpec describes the desired functionality of the ComplexPodScale.
type RedisClusterSpec struct {

	// Replicas is the desired number of replicas of the given Template.
	// These are replicas in the sense that they are instantiations of the
	// same Template, but individual replicas also have a consistent identity.
	// If unspecified, defaults to 1.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	Pause                bool                           `json:"pause"`
	Finalizers           string                         `json:"finalizers,omitempty"`
	Repository           string                         `json:"repository,omitempty"`
	Version              string                         `json:"version,omitempty"`
	UpdateStrategy       RedisClusterUpdateStrategy     `json:"updateStrategy,omitempty"`
	Pod                  []RedisClusterPodTemplateSpec  `json:"pod,omitempty"`
	RedisInstancePort    int32                          `json:"redisInstancePort,omitempty"`
	ServicePort          int32                          `json:"servicePort,omitempty"`
	PodManagementPolicy  appsv1.PodManagementPolicyType `json:"podManagementPolicy,omitempty"`
	UpdateId             string                         `json:"updateId,omitempty"`
	Volumes              RedisClusterPodVolume          `json:"volumes"`
	VolumeClaimTemplates []v1.PersistentVolumeClaim     `json:"volumeClaimTemplates,omitempty" protobuf:"bytes,4,rep,name=volumeClaimTemplates"`
	Sentinel             SentinelPodTemplateSpec        `json:"sentinel,omitempty"`
	Type                 string                         `json:"type,omitempty"`
}

type RedisClusterUpdateStrategy struct {
	Type             RedisClusterUpdateStrategyType `json:"type,omitempty"`
	Pipeline         string                         `json:"pipeline,omitempty"`
	AssignStrategies []SlotsAssignStrategy          `json:"assignStrategies,omitempty"`
}

type SlotsAssignStrategy struct {
	Slots *int32 `json:"slots,omitempty"`
	//nodeid
	FromReplicas string `json:"fromReplicas,omitempty"`
}

type RedisClusterPodTemplateSpec struct {
	Configmap string `json:"configmap,omitempty" protobuf:"bytes,4,opt,name=configmap"`

	MonitorImage string `json:"monitorImage,omitempty" protobuf:"bytes,4,opt,name=monitorImage"`

	InitImage string `json:"initImage,omitempty" protobuf:"bytes,4,opt,name=initImage"`

	MiddlewareImage string `json:"middlewareImage,omitempty" protobuf:"bytes,4,opt,name=middlewareImage"`

	RequirePass string `json:"requirepass,omitempty" protobuf:"bytes,4,opt,name=requirepass"`

	ImagePullPolicy v1.PullPolicy `json:"imagePullPolicy,omitempty" protobuf:"bytes,4,opt,name=imagePullPolicy"`

	// List of environment variables to set in the container.
	// Cannot be updated.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	Env []v1.EnvVar `json:"env,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,7,rep,name=env"`
	// Compute Resources required by this container.
	// Cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes#resources
	// +optional
	Resources v1.ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,8,opt,name=resources"`
	// If specified, the pod's scheduling constraints
	// +optional
	Affinity *v1.Affinity `json:"affinity,omitempty" protobuf:"bytes,18,opt,name=affinity"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	// More info: http://kubernetes.io/docs/user-guide/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,12,rep,name=annotations"`

	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// More info: http://kubernetes.io/docs/user-guide/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,11,rep,name=labels"`

	// updateStrategy indicates the StatefulSetUpdateStrategy that will be
	// employed to update Pods in the StatefulSet when a revision is made to
	// Template.
	UpdateStrategy appsv1.StatefulSetUpdateStrategy `json:"updateStrategy,omitempty" protobuf:"bytes,7,opt,name=updateStrategy"`

	HostNetwork bool `json:"hostNetwork,omitempty"`
}

type RedisClusterPodVolume struct {
	Type                      string `json:"type,omitempty" protobuf:"bytes,4,opt,name=type"`
	PersistentVolumeClaimName string `json:"persistentVolumeClaimName,omitempty" protobuf:"bytes,4,opt,name=persistentVolumeClaimName"`
}

type RedisClusterStatus struct {
	// replicas is the number of Pods created by the RedisCluster.
	Replicas         int32 `json:"replicas" protobuf:"varint,2,opt,name=replicas"`
	SentinelReplicas int32 `json:"sentinelReplicas" protobuf:"varint,2,opt,name=replicas"`

	HistoryReasons []Reason `json:"historyReasons,omitempty" protobuf:"bytes,4,opt,name=historyReasons"`

	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`

	// The reasonType for the condition's last transition.
	// +optional
	ReasonType RedisClusterReasonType `json:"reasonType,omitempty" protobuf:"bytes,4,opt,name=reasonType"`

	Phase         RedisClusterPhase `json:"phase"`
	SentinelPhase RedisClusterPhase `json:"sentinelPhase"`

	// Count of hash collisions for the RedisCluster. The RedisCluster operator
	// uses this field as a collision avoidance mechanism when it needs to
	// create the name for the newest ControllerRevision.
	// +optional
	CollisionCount *int32 `json:"collisionCount,omitempty" protobuf:"varint,9,opt,name=collisionCount"`

	Conditions         []RedisClusterCondition `json:"conditions"`
	SentinelConditions []RedisClusterCondition `json:"sentinelConditions"`

	FormedClusterBefore bool `json:"formedClusterBefore"`

	// redis exporter addr 1.1.1.1:19105
	ExporterAddr string `json:"exporterAddr,omitempty" protobuf:"bytes,4,opt,name=exporterAddr"`

	// redis exporter domain name a-0.a.kube-system.svc.caluster.local:19105
	ExporterDomainName string `json:"exporterDomainName,omitempty" protobuf:"bytes,4,opt,name=exporterDomainName"`
}

type Reason struct {
	Phase RedisClusterPhase `json:"phase"`

	//updateId
	ReasonUpdateId string `json:"reasonUpdateId,omitempty" protobuf:"bytes,4,opt,name=reasonUpdateId"`

	//更新时间
	LastTransitionTime metav1.Time `json:"reasonTime,omitempty" protobuf:"bytes,3,opt,name=reasonTime"`

	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`

	// The reasonType for the condition's last transition.
	// +optional
	ReasonType RedisClusterReasonType `json:"reasonType,omitempty" protobuf:"bytes,4,opt,name=reasonType"`
}

type RedisClusterCondition struct {
	Name string `json:"name,omitempty" protobuf:"bytes,4,opt,name=name"`

	Namespace string `json:"namespace,omitempty" protobuf:"bytes,4,opt,name=namespace"`
	// Type of RedisCluster condition.
	Type     RedisClusterConditionType `json:"type"`
	Instance string                    `json:"instance,omitempty" protobuf:"bytes,4,opt,name=instance"`

	NodeId string `json:"nodeId,omitempty" protobuf:"bytes,4,opt,name=nodeId"`

	MasterNodeId string `json:"masterNodeId,omitempty" protobuf:"bytes,4,opt,name=masterNodeId"`

	DomainName string `json:"domainName,omitempty" protobuf:"bytes,4,opt,name=domainName"`

	Slots    string `json:"slots,omitempty" protobuf:"bytes,4,opt,name=slots"`
	Hostname string `json:"hostname,omitempty" protobuf:"bytes,4,opt,name=hostname"`
	HostIP   string `json:"hostIP,omitempty" protobuf:"bytes,4,opt,name=hostIP"`
	// Status of the condition, one of True, False, Unknown.
	Status v1.ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status,casttype=k8s.io/api/core/v1.ConditionStatus"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	// A human readable message indicating details about the transition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

// LeaderElectionConfiguration defines the configuration of leader election
// clients for components that can run with leader election enabled.
type LeaderElectionConfiguration struct {
	// leaderElect enables a leader election client to gain leadership
	// before executing the main loop. Enable this when running replicated
	// components for high availability.
	LeaderElect bool
	// leaseDuration is the duration that non-leader candidates will wait
	// after observing a leadership renewal until attempting to acquire
	// leadership of a led but unrenewed leader slot. This is effectively the
	// maximum duration that a leader can be stopped before it is replaced
	// by another candidate. This is only applicable if leader election is
	// enabled.
	LeaseDuration metav1.Duration
	// renewDeadline is the interval between attempts by the acting master to
	// renew a leadership slot before it stops leading. This must be less
	// than or equal to the lease duration. This is only applicable if leader
	// election is enabled.
	RenewDeadline metav1.Duration
	// retryPeriod is the duration the clients should wait between attempting
	// acquisition and renewal of a leadership. This is only applicable if
	// leader election is enabled.
	RetryPeriod metav1.Duration
	// resourceLock indicates the resource object type that will be used to lock
	// during leader election cycles.
	ResourceLock string
	// LockObjectNamespace defines the namespace of the lock object
	LockObjectNamespace string
	// LockObjectName defines the lock object name
	LockObjectName string
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OperatorManagerConfig struct {
	metav1.TypeMeta

	// Operators is the list of operators to enable or disable
	// '*' means "all enabled by default operators"
	// 'foo' means "enable 'foo'"
	// '-foo' means "disable 'foo'"
	// first item for a particular name wins
	Operators []string

	// ConcurrentRedisClusteryncs is the number of redisCluster objects that are
	// allowed to sync concurrently. Larger number = more responsive RedisCluster,
	// but more CPU (and network) load.
	ConcurrentRedisClusteryncs int32

	//cluster create or upgrade timeout (min)
	ClusterTimeOut int32

	// The number of old history to retain to allow rollback.
	// This is a pointer to distinguish between explicit zero and not specified.
	// Defaults to 2.
	// +optional
	RevisionHistoryLimit int32 `json:"revisionHistoryLimit,omitempty" protobuf:"varint,6,opt,name=revisionHistoryLimit"`

	// How long to wait between starting controller managers
	ControllerStartInterval metav1.Duration

	ResyncPeriod int64
	// leaderElection defines the configuration of leader election client.
	LeaderElection LeaderElectionConfiguration
	// port is the port that the controller-manager's http service runs on.
	Port int32
	// address is the IP address to serve on (set to 0.0.0.0 for all interfaces).
	Address string

	// enableProfiling enables profiling via web interface host:port/debug/pprof/
	EnableProfiling bool
	// contentType is contentType of requests sent to apiserver.
	ContentType string

	// ClusterDomain is ClusterDomain of k8s cluster.
	ClusterDomain string

	// kubeAPIQPS is the QPS to use while talking with kubernetes apiserver.
	KubeAPIQPS float32
	// kubeAPIBurst is the burst to use while talking with kubernetes apiserver.
	KubeAPIBurst int32

	KibanaDomianName string
}

// 哨兵配置
type SentinelPodTemplateSpec struct {
	Enable bool `json:"enable,omitempty"`

	Replicas *int32 `json:"replicas,omitempty"`

	Resources v1.ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,8,opt,name=resources"`

	// Image string `json:"image,omitempty"`

	// InitImage string `json:"initImage,omitempty"`

	Command []string `json:"command,omitempty"`

	// Repository string `json:"repository,omitempty"`
}