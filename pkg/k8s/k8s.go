package k8s

import (
	"k8s.io/client-go/kubernetes"
)

type services struct {
	Service
	ConfigMap
	PodDisruptionBudget
	Deployment
	StatefulSet
	Pod
}

type Services interface {
	Service
	ConfigMap
	PodDisruptionBudget
	Deployment
	StatefulSet
	Pod
}

func New(kubecli kubernetes.Interface) Services {
	return &services{
		Service:             NewServiceService(kubecli),
		ConfigMap:           NewConfigMapService(kubecli),
		PodDisruptionBudget: NewPodDisruptionBudgetService(kubecli),
		Deployment:          NewDeploymentService(kubecli),
		StatefulSet:         NewStatefulSetService(kubecli),
		Pod:                 NewPodService(kubecli),
	}
}
