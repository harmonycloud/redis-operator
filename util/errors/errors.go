/*
Copyright 2014 The Kubernetes Authors.

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

package errors

import (
	"encoding/json"
	"fmt"
	"net/http"

	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// StatusTooManyRequests means the server experienced too many requests within a
	// given window and that the client must wait to perform the action again.
	StatusTooManyRequests = 429
)

type Status struct {
	// Status of the operation.
	// One of: "Success" or "Failure".
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#spec-and-status
	// +optional
	Phase RedisClusterPhase `json:"phase,omitempty" protobuf:"bytes,2,opt,name=phase"`
	// A human-readable description of the status of this operation.
	// +optional
	Message    string                 `json:"message,omitempty" protobuf:"bytes,3,opt,name=message"`
	ReasonType RedisClusterReasonType `json:"reasonType,omitempty" protobuf:"bytes,4,opt,name=reasonType"`
	// Suggested HTTP return code for this status, 0 if not set.
	// +optional
	Code int32 `json:"code,omitempty" protobuf:"varint,6,opt,name=code"`

	//成功加入集群的实例IP集合
	SuccessAddInstances []string `json:"successAddInstances,omitempty" protobuf:"varint,6,opt,name=successAddInstances"`

	//升级前已经存在于redis集群的实例IP
	ExistInstanceIps []string `json:"existInstanceIps,omitempty" protobuf:"varint,6,opt,name=existInstanceIps"`
}

// RedisClusterReasonStatus is exposed by errors that can be converted to an api.Status object
// for finer grained details.
type RedisClusterReasonStatus interface {
	Status() Status
}

// StatusError is an error intended for consumption by a REST API server; it can also be
// reconstructed by clients from a REST response. Public to allow easy type switches.
type StatusError struct {
	ErrStatus Status
}

var _ error = &StatusError{}

// Error implements the Error interface.
func (e *StatusError) Error() string {
	return e.ErrStatus.Message
}

// Status allows access to e's status without having to know the detailed workings
// of StatusError.
func (e *StatusError) Status() Status {
	return e.ErrStatus
}

// DebugError reports extended info about the error to debug output.
func (e *StatusError) DebugError() (string, []interface{}) {
	if out, err := json.MarshalIndent(e.ErrStatus, "", "  "); err == nil {
		return "server response object: %s", []interface{}{string(out)}
	}
	return "server response object: %#v", []interface{}{e.ErrStatus}
}

// UnexpectedObjectError can be returned by FromObject if it's passed a non-status object.
type UnexpectedObjectError struct {
	Object runtime.Object
}

// Error returns an error message describing 'u'.
func (u *UnexpectedObjectError) Error() string {
	return fmt.Sprintf("unexpected object: %v", u.Object)
}

// NewInvalid returns an error indicating the item is invalid and cannot be processed.
func NewUnexpectedError(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeUnexpectedError,
		Message:    message,
	}}
}

// NewRedisClusterState returns an error indicating the item is invalid and cannot be processed.
func NewRedisClusterState(message string, phase RedisClusterPhase, reasonType RedisClusterReasonType) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: reasonType,
		Message:    message,
	}}
}

// NewInvalid creates an error that indicates that the request is invalid and can not be processed.
func NewInvalid(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		Code:       http.StatusBadRequest,
		ReasonType: RedisClusterReasonTypeInvalidArgs,
		Message:    message,
	}}
}

// NewTooManyRequests creates an error that indicates that the client must try again later because
// the specified endpoint is not accepting requests. More specific details should be provided
// if client should know why the failure was limited4.
func NewReshareSlotsFailed(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeReshareSlotsFailed,
		Message:    message,
	}}
}

// NewServiceUnavailable creates an error that indicates that the requested service is unavailable.
func NewRebalanceSlotsFailed(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeRebalanceSlotsFailed,
		Message:    message,
	}}
}

// NewMethodNotSupported returns an error indicating the requested action is not supported on this kind.
func NewAddMasterFailedWhenCreateRedisCluster(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeAddMasterFailedWhenCreateCluster,
		Message:    message,
	}}
}

// NewMethodNotSupported returns an error indicating the requested action is not supported on this kind.
func NewAddMasterFailedWhenUpgradeRedisCluster(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeAddMasterFailedWhenUpgradeCluster,
		Message:    message,
	}}
}

// NewServerTimeout returns an error indicating the requested action could not be completed due to a
// transient error, and the client should try again.
func NewUpgradeFailed(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeUpgradeFailed,
		Message:    message,
	}}
}

// NewAddSlaveFailedWhenCreateCluster returns an error indicating the item is invalid and cannot be processed.
func NewAddSlaveFailedWhenCreateCluster(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeAddSlaveFailedWhenCreateCluster,
		Message:    message,
	}}
}

// NewAddSlaveFailedWhenUpgradeCluster returns an error indicating the item is invalid and cannot be processed.
func NewAddSlaveFailedWhenUpgradeCluster(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeAddSlaveFailedWhenUpgradeCluster,
		Message:    message,
	}}
}

// NewTimeoutError returns an error indicating that a timeout occurred before the request
// could be compleRedisClusterReasonTypeInitClusterFailedted.  Clients may retry, but the operation may still complete.
func NewInitClusterFailed(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeInitClusterFailed,
		Message:    message,
	}}
}

// NewCreatePodFailedWhenCreateCluster returns an error indicating that the request was rejected because
// the server has received too many requests. Client should wait and retry. But if the request
// is perishable, then the client should not retry the request.
func NewCreatePodFailedWhenCreateCluster(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeCreatePodFailedWhenCreateCluster,
		Message:    message,
	}}
}

func NewCreatePodFailedWhenUpgradeCluster(message string, phase RedisClusterPhase) *StatusError {
	return &StatusError{Status{
		Phase:      phase,
		ReasonType: RedisClusterReasonTypeCreatePodFailedWhenUpgradeCluster,
		Message:    message,
	}}
}

func NewUpgradeFailedErrorInfo(message string, phase RedisClusterPhase, reasonType RedisClusterReasonType, successAddInstances []string, existInstanceIps []string) *StatusError {
	return &StatusError{Status{
		Phase:               phase,
		ReasonType:          reasonType,
		Message:             message,
		SuccessAddInstances: successAddInstances,
		ExistInstanceIps:    existInstanceIps,
	}}
}

// IsUnexpectedError determines if the err is an error which indicates the provided action could not
// be performed because it is not supported by the server.
func IsUnexpectedError(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeUnexpectedError
}

// IsInvalidArgs is true if the error indicates the underlying service is no longer available.
func IsInvalidArgs(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeInvalidArgs
}

// IsReshareSlotsFailed determines if err is an error which indicates that the request is invalid.
func IsReshareSlotsFailed(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeReshareSlotsFailed
}

// IsRebalanceSlotsFailed determines if err is an error which indicates that the request is unauthorized and
// requires authentication by the user.
func IsRebalanceSlotsFailed(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeRebalanceSlotsFailed
}

// IsAddMasterFailed determines if err is an error which indicates that the request is forbidden and cannot
// be completed as requested.
func IsAddMasterFailed(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeAddMasterFailedWhenUpgradeCluster
}

// IsUpgradeFailed determines if err is an error which indicates that request times out due to long
// processing.
func IsUpgradeFailed(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeUpgradeFailed
}

// IsAddSlaveFailedWhenCreateCluster determines if err is an error which indicates that the request needs to be retried
// by the client.
func IsAddSlaveFailedWhenCreateCluster(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeAddSlaveFailedWhenCreateCluster
}

// IsAddSlaveFailedWhenUpgradeCluster determines if err is an error which indicates that the request needs to be retried
// by the client.
func IsAddSlaveFailedWhenUpgradeCluster(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeAddSlaveFailedWhenUpgradeCluster
}

// IsInitClusterFailed determines if err is an error which indicates an internal server error.
func IsInitClusterFailed(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeInitClusterFailed
}

// IsCreatePodFailedWhenCreateCluster determines if err is an error which indicates that there are too many requests
// that the server cannot handle.
func IsCreatePodFailedWhenCreateCluster(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeCreatePodFailedWhenCreateCluster
}

// IsCreatePodFailedWhenUpgradeCluster determines if err is an error which indicates that there are too many requests
// that the server cannot handle.
func IsCreatePodFailedWhenUpgradeCluster(err error) bool {
	return ReasonForError(err) == RedisClusterReasonTypeCreatePodFailedWhenUpgradeCluster
}

// IsUnexpectedObjectError determines if err is due to an unexpected object from the master.
func IsUnexpectedObjectError(err error) bool {
	_, ok := err.(*UnexpectedObjectError)
	return err != nil && ok
}

// ReasonForError returns the HTTP status for a particular error.
func ReasonForError(err error) RedisClusterReasonType {
	switch t := err.(type) {
	case RedisClusterReasonStatus:
		return t.Status().ReasonType
	}
	return RedisClusterReasonTypeUnexpectedError
}

// Phase returns the HTTP status for a particular error.
func Phase(err error) RedisClusterPhase {
	switch t := err.(type) {
	case RedisClusterReasonStatus:
		return t.Status().Phase
	}
	return RedisClusterFailed
}

// SuccessAddInstances returns the HTTP status for a particular error.
func SuccessAddInstances(err error) []string {
	switch t := err.(type) {
	case RedisClusterReasonStatus:
		return t.Status().SuccessAddInstances
	}
	return nil
}

// ExistInstanceIps returns the HTTP status for a particular error.
func ExistInstanceIps(err error) []string {
	switch t := err.(type) {
	case RedisClusterReasonStatus:
		return t.Status().ExistInstanceIps
	}
	return nil
}

// Message returns the HTTP status for a particular error.
func Message(err error) string {
	switch t := err.(type) {
	case RedisClusterReasonStatus:
		return t.Status().Message
	}
	return "Unexpected Message"
}

// ReasonType returns the HTTP status for a particular error.
func ReasonType(err error) RedisClusterReasonType {
	switch t := err.(type) {
	case RedisClusterReasonStatus:
		return t.Status().ReasonType
	}
	return RedisClusterReasonTypeUnexpectedError
}

func IsRedisClusterReasonStatus(err error) bool {
	switch err.(type) {
	case RedisClusterReasonStatus:
		return true
	}
	return false
}
