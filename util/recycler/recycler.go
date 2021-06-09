/*
Copyright 2018 The Kubernetes Authors.

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

package recycler

import (
	"context"
	"fmt"
	"sync"

	batch "k8s.io/api/batch/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"time"
)

type RecycleEventRecorder func(eventtype, message string)

// RecycleVolumeByWatchingPodUntilCompletion is intended for use with volume
// Recyclers. This function will save the given Pod to the API and watch it
// until it completes, fails, or the pod's ActiveDeadlineSeconds is exceeded,
// whichever comes first. An attempt to delete a recycler pod is always
// attempted before returning.
//
// In case there is a pod with the same namespace+name already running, this
// function deletes it as it is not able to judge if it is an old recycler
// or user has forged a fake recycler to block Kubernetes from recycling.//
//
//  pod - the pod designed by a volume plugin to recycle the volume. pod.Name
//        will be overwritten with unique name based on PV.Name.
//	client - kube client for API operations.
func RecycleVolumeByWatchingPodUntilCompletion(pod *v1.Pod, kubeClient clientset.Interface, recorder RecycleEventRecorder) error {
	return internalRecycleMiddleWareDataByWatchingPodUntilCompletion(pod, newRecyclerClient(kubeClient, recorder))
}

// same as above func comments, except 'recyclerClient' is a narrower pod API
// interface to ease testing
func internalRecycleMiddleWareDataByWatchingPodUntilCompletion(pod *v1.Pod, recyclerClient recyclerClient) error {
	klog.V(5).Infof("creating recycler pod for middleware data %s\n", pod.Name)

	// Generate unique name for the recycler pod - we need to get "already
	// exists" error when a previous controller has already started recycling
	// the volume. Here we assume that pv.Name is already unique.
	if pod.Name == "" {
		pod.Name = "recycler-data-" + fmt.Sprintf("%v", time.Now().Nanosecond())
	}
	//pod.GenerateName = ""

	stopChannel := make(chan struct{})
	defer close(stopChannel)
	podCh, err := recyclerClient.WatchPod(pod.Name, pod.Namespace, stopChannel)
	if err != nil {
		klog.V(4).Infof("cannot start watcher for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return err
	}

	// Start the pod
	_, err = recyclerClient.CreatePod(pod)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			deleteErr := recyclerClient.DeletePod(pod.Name, pod.Namespace)
			if deleteErr != nil {
				return fmt.Errorf("failed to delete old recycler pod %s/%s: %s", pod.Namespace, pod.Name, deleteErr)
			}
			// Recycler will try again and the old pod will be hopefully deleted
			// at that time.
			return fmt.Errorf("old recycler pod found, will retry later")
		}
		return fmt.Errorf("unexpected error creating recycler pod:  %+v", err)
	}
	err = waitForPod(pod, recyclerClient, podCh)

	// In all cases delete the recycler pod and log its result.
	klog.V(2).Infof("deleting recycler pod %s/%s", pod.Namespace, pod.Name)
	deleteErr := recyclerClient.DeletePod(pod.Name, pod.Namespace)
	if deleteErr != nil {
		klog.Errorf("failed to delete recycler pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}

	// Returning recycler error is preferred, the pod will be deleted again on
	// the next retry.
	if err != nil {
		return fmt.Errorf("failed to recycle middleware data: %s", err)
	}

	// Recycle succeeded but we failed to delete the recycler pod. Report it,
	// the controller will re-try recycling the PV again shortly.
	if deleteErr != nil {
		return fmt.Errorf("failed to delete recycler pod: %s", deleteErr)
	}

	return nil
}

// waitForPod watches the pod it until it finishes and send all events on the
// pod to the PV.
func waitForPod(pod *v1.Pod, recyclerClient recyclerClient, podCh <-chan watch.Event) error {
	for {
		event, ok := <-podCh
		if !ok {
			return fmt.Errorf("recycler pod %q watch channel had been closed", pod.Name)
		}
		switch event.Object.(type) {
		case *v1.Pod:
			// POD changed
			pod := event.Object.(*v1.Pod)
			klog.V(4).Infof("recycler pod update received: %s %s/%s %s", event.Type, pod.Namespace, pod.Name, pod.Status.Phase)
			switch event.Type {
			case watch.Added, watch.Modified:
				if pod.Status.Phase == v1.PodSucceeded {
					// Recycle succeeded.
					return nil
				}
				if pod.Status.Phase == v1.PodFailed {
					if pod.Status.Message != "" {
						return fmt.Errorf(pod.Status.Message)
					} else {
						return fmt.Errorf("pod failed, pod.Status.Message unknown.")
					}
				}

			case watch.Deleted:
				return fmt.Errorf("recycler pod was deleted")

			case watch.Error:
				return fmt.Errorf("recycler pod watcher failed")
			}

		case *v1.Event:
			// Event received
			podEvent := event.Object.(*v1.Event)
			klog.V(4).Infof("recycler event received: %s %s/%s %s/%s %s", event.Type, podEvent.Namespace, podEvent.Name, podEvent.InvolvedObject.Namespace, podEvent.InvolvedObject.Name, podEvent.Message)
			if event.Type == watch.Added {
				//recyclerClient.Event(podEvent.Type, podEvent.Message)
			}
		}
	}
}

// recyclerClient abstracts access to a Pod by providing a narrower interface.
// This makes it easier to mock a client for testing.
type recyclerClient interface {
	CreatePod(pod *v1.Pod) (*v1.Pod, error)
	GetPod(name, namespace string) (*v1.Pod, error)
	DeletePod(name, namespace string) error
	// WatchPod returns a ListWatch for watching a pod.  The stopChannel is used
	// to close the reflector backing the watch.  The caller is responsible for
	// derring a close on the channel to stop the reflector.
	WatchPod(name, namespace string, stopChannel chan struct{}) (<-chan watch.Event, error)
	// Event sends an event to the volume that is being recycled.
	Event(eventtype, message string)
}

func newRecyclerClient(client clientset.Interface, recorder RecycleEventRecorder) recyclerClient {
	return &realRecyclerClient{
		client,
		recorder,
	}
}

type realRecyclerClient struct {
	client   clientset.Interface
	recorder RecycleEventRecorder
}

func (c *realRecyclerClient) CreatePod(pod *v1.Pod) (*v1.Pod, error) {
	return c.client.CoreV1().Pods(pod.Namespace).Create(context.TODO(),pod,metav1.CreateOptions{})
}

func (c *realRecyclerClient) GetPod(name, namespace string) (*v1.Pod, error) {
	return c.client.CoreV1().Pods(namespace).Get(context.TODO(),name, metav1.GetOptions{})
}

func (c *realRecyclerClient) DeletePod(name, namespace string) error {
	return c.client.CoreV1().Pods(namespace).Delete(context.TODO(),name, metav1.DeleteOptions{})
}

func (c *realRecyclerClient) Event(eventtype, message string) {
	c.recorder(eventtype, message)
}

// WatchPod watches a pod and events related to it. It sends pod updates and events over the returned channel
// It will continue until stopChannel is closed
func (c *realRecyclerClient) WatchPod(name, namespace string, stopChannel chan struct{}) (<-chan watch.Event, error) {
	podSelector, err := fields.ParseSelector("metadata.name=" + name)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{
		FieldSelector: podSelector.String(),
		Watch:         true,
	}

	podWatch, err := c.client.CoreV1().Pods(namespace).Watch(context.TODO(),options)
	if err != nil {
		return nil, err
	}

	eventSelector, _ := fields.ParseSelector("involvedObject.name=" + name)
	eventWatch, err := c.client.CoreV1().Events(namespace).Watch(context.TODO(),metav1.ListOptions{
		FieldSelector: eventSelector.String(),
		Watch:         true,
	})
	if err != nil {
		podWatch.Stop()
		return nil, err
	}

	eventCh := make(chan watch.Event, 30)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer close(eventCh)
		wg.Wait()
	}()

	go func() {
		defer eventWatch.Stop()
		defer wg.Done()
		for {
			select {
			case _ = <-stopChannel:
				return
			case eventEvent, ok := <-eventWatch.ResultChan():
				if !ok {
					return
				} else {
					eventCh <- eventEvent
				}
			}
		}
	}()

	go func() {
		defer podWatch.Stop()
		defer wg.Done()
		for {
			select {
			case <-stopChannel:
				return

			case podEvent, ok := <-podWatch.ResultChan():
				if !ok {
					return
				} else {
					eventCh <- podEvent
				}
			}
		}
	}()

	return eventCh, nil
}

func NewMiddlewareDataRecyclerPodTemplate(mountPath, affinityNodeName, command, repository string) (*v1.Pod, error) {

	/*if !strings.HasPrefix(mountPath, "/data") {
		return nil, fmt.Errorf("just rm contain /data prefix dir is permitted, %v is limited", rmPath)
	}*/

	affinity := &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{
								Key:      "kubernetes.io/hostname",
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{affinityNodeName},
							},
						},
					},
				},
			},
		},
	}

	timeout := int64(60)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "middleware-recycler-",
			Namespace:    metav1.NamespaceDefault,
		},
		Spec: v1.PodSpec{
			Affinity:              affinity,
			ActiveDeadlineSeconds: &timeout,
			RestartPolicy:         v1.RestartPolicyNever,
			HostNetwork:           true,
			Volumes: []v1.Volume{
				{
					Name: "vol",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: mountPath,
						},
					},
				},
			},
			Containers: []v1.Container{
				{
					Name:    "data-recycler",
					Image:   repository + "busybox",
					Command: []string{"/bin/sh"},
					Args:    []string{"-c", command},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "vol",
							MountPath: mountPath,
						},
					},
				},
			},
		},
	}
	return pod, nil
}

func NewMiddlewareDataRecyclerJobTemplate(mountPath, affinityNodeName, command string) (*batch.Job, error) {
	var one int32 = 1
	var six int32 = 6

	affinity := &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{
								Key:      "kubernetes.io/hostname",
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{affinityNodeName},
							},
						},
					},
				},
			},
		},
	}

	timeout := int64(60)
	job := &batch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:         "recycler-data-" + fmt.Sprintf("%v", time.Now().UnixNano()),
			GenerateName: "middleware-recycler-",
			Namespace:    metav1.NamespaceDefault,
		},
		Spec: batch.JobSpec{
			Parallelism:  &one,
			Completions:  &one,
			BackoffLimit: &six,
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Affinity:              affinity,
					ActiveDeadlineSeconds: &timeout,
					RestartPolicy:         v1.RestartPolicyOnFailure,
					HostNetwork:           true,
					Volumes: []v1.Volume{
						{
							Name: "vol",
							VolumeSource: v1.VolumeSource{
								HostPath: &v1.HostPathVolumeSource{
									Path: mountPath,
								},
							},
						},
					},
					Containers: []v1.Container{
						{
							Name:    "data-recycler",
							Image:   "172.22.242.235:30443/k8s-deploy/busybox",
							Command: []string{"/bin/sh"},
							Args:    []string{"-c", command},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "vol",
									MountPath: mountPath,
								},
							},
						},
					},
				},
			},
		},
	}
	return job, nil
}

func internalRecycleMiddleWareDataByWatchingJobUntilCompletion(job *batch.Job, defaultClient clientset.Interface) error {
	/*klog.V(5).Infof("creating recycler job for middleware data %s\n", job.Name)

	// Generate unique name for the recycler pod - we need to get "already
	// exists" error when a previous controller has already started recycling
	// the volume. Here we assume that pv.Name is already unique.
	job.Name = "recycler-data-" + fmt.Sprintf("%v", time.Now().UnixNano())
	job.GenerateName = ""

	stopChannel := make(chan struct{})
	defer close(stopChannel)
	//createdJob, err := defaultClient.BatchV1().Jobs(job.Namespace).Create(job)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			deleteErr := recyclerClient.DeletePod(job.Name, job.Namespace)
			if deleteErr != nil {
				return fmt.Errorf("failed to delete old recycler pod %s/%s: %s", pod.Namespace, pod.Name, deleteErr)
			}
			// Recycler will try again and the old pod will be hopefully deleted
			// at that time.
			return fmt.Errorf("old recycler pod found, will retry later")
		}
		return fmt.Errorf("unexpected error creating recycler pod:  %+v", err)
	}
	if err != nil {
		klog.V(4).Infof("cannot start create for job %s/%s: %v", job.Namespace, job.Name, err)
		return err
	}

	// Start the pod
	_, err = recyclerClient.CreatePod(pod)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			deleteErr := recyclerClient.DeletePod(pod.Name, pod.Namespace)
			if deleteErr != nil {
				return fmt.Errorf("failed to delete old recycler pod %s/%s: %s", pod.Namespace, pod.Name, deleteErr)
			}
			// Recycler will try again and the old pod will be hopefully deleted
			// at that time.
			return fmt.Errorf("old recycler pod found, will retry later")
		}
		return fmt.Errorf("unexpected error creating recycler pod:  %+v", err)
	}
	err = waitForPod(pod, recyclerClient, podCh)

	// In all cases delete the recycler pod and log its result.
	klog.V(2).Infof("deleting recycler pod %s/%s", pod.Namespace, pod.Name)
	deleteErr := recyclerClient.DeletePod(pod.Name, pod.Namespace)
	if deleteErr != nil {
		klog.Errorf("failed to delete recycler pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}

	// Returning recycler error is preferred, the pod will be deleted again on
	// the next retry.
	if err != nil {
		return fmt.Errorf("failed to recycle middleware data: %s", err)
	}

	// Recycle succeeded but we failed to delete the recycler pod. Report it,
	// the controller will re-try recycling the PV again shortly.
	if deleteErr != nil {
		return fmt.Errorf("failed to delete recycler pod: %s", deleteErr)
	}*/

	return nil
}
