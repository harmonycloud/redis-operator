package ingressview
//
//import (
//	"context"
//	"fmt"
//
//	"harmonycloud.cn/middleware/redis-cluster/pkg/options"
//	v1 "k8s.io/api/core/v1"
//	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
//	"k8s.io/apimachinery/pkg/labels"
//	"k8s.io/apimachinery/pkg/util/runtime"
//	"k8s.io/apimachinery/pkg/util/sets"
//	"k8s.io/apimachinery/pkg/util/wait"
//	coreinformers "k8s.io/client-go/informers/core/v1"
//	clientset "k8s.io/client-go/kubernetes"
//	"k8s.io/client-go/kubernetes/scheme"
//	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
//	corelisters "k8s.io/client-go/listers/core/v1"
//	"k8s.io/client-go/tools/cache"
//	"k8s.io/client-go/tools/record"
//	"k8s.io/client-go/util/workqueue"
//	"k8s.io/klog"
//	//"k8s.io/kubernetes/pkg/controller"
//
//	"reflect"
//	"sync"
//	"time"
//)
//
//const (
//	// Interval of synchronizing service status from apiserver
//	serviceSyncPeriod = 30 * time.Second
//	// Interval of synchronizing node status from apiserver
//	nodeSyncPeriod = 100 * time.Second
//
//	// How long to wait before retrying the processing of a service change.
//	// If this changes, the sleep in hack/jenkins/e2e.sh before downing a cluster
//	// should be changed appropriately.
//	minRetryDelay = 5 * time.Second
//	maxRetryDelay = 300 * time.Second
//)
//
//type cachedService struct {
//	// The cached state of the service
//	state *v1.Service
//}
//
//type serviceCache struct {
//	mu         sync.Mutex // protects serviceMap
//	serviceMap map[string]*cachedService
//}
//
//type ServiceController struct {
//	servicesToUpdate    []*v1.Service
//	knownHosts          []*v1.Node
//	kubeClient          clientset.Interface
//	clusterName         string
//	cache               *serviceCache
//	serviceLister       corelisters.ServiceLister
//	serviceListerSynced cache.InformerSynced
//	nodeLister          corelisters.NodeLister
//	nodeListerSynced    cache.InformerSynced
//	eventBroadcaster    record.EventBroadcaster
//	eventRecorder       record.EventRecorder
//	// services that need to be synced
//	queue   workqueue.RateLimitingInterface
//	options *options.OperatorManagerServer
//}
//
//func New(
//	kubeClient clientset.Interface,
//	serviceInformer coreinformers.ServiceInformer,
//	nodeInformer coreinformers.NodeInformer,
//	opt options.OperatorManagerServer,
//) (*ServiceController, error) {
//	broadcaster := record.NewBroadcaster()
//	broadcaster.StartLogging(klog.Infof)
//	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
//	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "service-controller"})
//
//	s := &ServiceController{
//		kubeClient:       kubeClient,
//		cache:            &serviceCache{serviceMap: make(map[string]*cachedService)},
//		eventBroadcaster: broadcaster,
//		eventRecorder:    recorder,
//		nodeLister:       nodeInformer.Lister(),
//		nodeListerSynced: nodeInformer.Informer().HasSynced,
//		queue:            workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(minRetryDelay, maxRetryDelay), "service"),
//		options:          &opt,
//	}
//
//	serviceInformer.Informer().AddEventHandlerWithResyncPeriod(
//		cache.ResourceEventHandlerFuncs{
//			AddFunc: s.enqueueService,
//			UpdateFunc: func(old, cur interface{}) {
//				oldSvc, ok1 := old.(*v1.Service)
//				curSvc, ok2 := cur.(*v1.Service)
//				if ok1 && ok2 && s.needsUpdate(oldSvc, curSvc) {
//					s.enqueueService(cur)
//				}
//			},
//			DeleteFunc: s.enqueueService,
//		},
//		serviceSyncPeriod,
//	)
//	s.serviceLister = serviceInformer.Lister()
//	s.serviceListerSynced = serviceInformer.Informer().HasSynced
//	return s, nil
//}
//
//// obj could be an *v1.Service, or a DeletionFinalStateUnknown marker item.
//func (s *ServiceController) enqueueService(obj interface{}) {
//	key, err := controller.KeyFunc(obj)
//	if err != nil {
//		klog.Errorf("Couldn't get key for object %#v: %v", obj, err)
//		return
//	}
//	s.queue.Add(key)
//}
//
//func (s *ServiceController) needsUpdate(oldService *v1.Service, newService *v1.Service) bool {
//
//	if len(oldService.Spec.ExternalIPs) != len(newService.Spec.ExternalIPs) {
//		s.eventRecorder.Eventf(newService, v1.EventTypeNormal, "ExternalIP", "Count: %v -> %v",
//			len(oldService.Spec.ExternalIPs), len(newService.Spec.ExternalIPs))
//		return true
//	}
//	for i := range oldService.Spec.ExternalIPs {
//		if oldService.Spec.ExternalIPs[i] != newService.Spec.ExternalIPs[i] {
//			s.eventRecorder.Eventf(newService, v1.EventTypeNormal, "ExternalIP", "Added: %v",
//				newService.Spec.ExternalIPs[i])
//			return true
//		}
//	}
//	if !reflect.DeepEqual(oldService.Annotations, newService.Annotations) {
//		return true
//	}
//	if oldService.UID != newService.UID {
//		s.eventRecorder.Eventf(newService, v1.EventTypeNormal, "UID", "%v -> %v",
//			oldService.UID, newService.UID)
//		return true
//	}
//	if oldService.Spec.ExternalTrafficPolicy != newService.Spec.ExternalTrafficPolicy {
//		s.eventRecorder.Eventf(newService, v1.EventTypeNormal, "ExternalTrafficPolicy", "%v -> %v",
//			oldService.Spec.ExternalTrafficPolicy, newService.Spec.ExternalTrafficPolicy)
//		return true
//	}
//	if oldService.Spec.HealthCheckNodePort != newService.Spec.HealthCheckNodePort {
//		s.eventRecorder.Eventf(newService, v1.EventTypeNormal, "HealthCheckNodePort", "%v -> %v",
//			oldService.Spec.HealthCheckNodePort, newService.Spec.HealthCheckNodePort)
//		return true
//	}
//
//	return false
//}
//
//func (s *ServiceController) processNextWorkItem() bool {
//	key, quit := s.queue.Get()
//	if quit {
//		return false
//	}
//	defer s.queue.Done(key)
//
//	err := s.syncService(key.(string))
//	if err == nil {
//		s.queue.Forget(key)
//		return true
//	}
//
//	runtime.HandleError(fmt.Errorf("error processing service %v (will retry): %v", key, err))
//	s.queue.AddRateLimited(key)
//	return true
//}
//
//// worker runs a worker thread that just dequeues items, processes them, and marks them done.
//// It enforces that the syncHandler is never invoked concurrently with the same key.
//func (s *ServiceController) worker() {
//	for s.processNextWorkItem() {
//	}
//}
//
//// Run starts a background goroutine that watches for changes to services that
//// have (or had) LoadBalancers=true and ensures that they have
//// load balancers created and deleted appropriately.
//// serviceSyncPeriod controls how often we check the cluster's services to
//// ensure that the correct load balancers exist.
//// nodeSyncPeriod controls how often we check the cluster's nodes to determine
//// if load balancers need to be updated to point to a new set.
////
//// It's an error to call Run() more than once for a given ServiceController
//// object.
//func (s *ServiceController) Run(stopCh <-chan struct{}, workers int) {
//	defer runtime.HandleCrash()
//	defer s.queue.ShutDown()
//
//	klog.Info("Starting service controller")
//	defer klog.Info("Shutting down service controller")
//
//	if !cache.WaitForNamedCacheSync("service", stopCh, s.serviceListerSynced, s.nodeListerSynced) {
//		return
//	}
//
//	for i := 0; i < workers; i++ {
//		go wait.Until(s.worker, time.Second, stopCh)
//	}
//
//	go wait.Until(s.nodeSyncLoop, nodeSyncPeriod, stopCh)
//
//	<-stopCh
//}
//
//// invoked concurrently with the same key.
//func (s *ServiceController) syncService(key string) error {
//	startTime := time.Now()
//	//var cachedService *cachedService
//	defer func() {
//		klog.V(4).Infof("Finished syncing service %q (%v)", key, time.Since(startTime))
//	}()
//
//	namespace, name, err := cache.SplitMetaNamespaceKey(key)
//	if err != nil {
//		return err
//	}
//
//	// service holds the latest service info from apiserver
//	//service, err := s.serviceLister.Services(namespace).Get(name)
//	//s.serviceLister.List()
//	print(name)
//	service, err := s.serviceLister.Services(namespace).Get(name)
//	if service != nil {
//		if service.Annotations[resourceLabel] != "" {
//			resource := service.Annotations[resourceLabel]
//			ing, _ := s.kubeClient.ExtensionsV1beta1().Ingresses(namespace).Get(context.TODO(),resource+"-ingress", metav1.GetOptions{})
//			if ing.Name != "" {
//				test3 := map[string]string{
//					"visual-resource": resource,
//				}
//				sel := labels.SelectorFromSet(labels.Set(test3))
//				opts := metav1.ListOptions{LabelSelector: sel.String()}
//				svcs, _ := s.kubeClient.CoreV1().Services(namespace).List(context.TODO(),opts)
//				if len(svcs.Items) != 0 {
//					resourceIngress := newIngress(svcs.Items, namespace, s.options.KibanaDomianName)
//					ing, err := s.kubeClient.ExtensionsV1beta1().Ingresses(namespace).Update(context.TODO(),resourceIngress,metav1.UpdateOptions{})
//					print(ing, err)
//				}
//			} else {
//				test3 := map[string]string{
//					"visual-resource": resource,
//				}
//				sel := labels.SelectorFromSet(labels.Set(test3))
//				opts := metav1.ListOptions{LabelSelector: sel.String()}
//				svcs, _ := s.kubeClient.CoreV1().Services(namespace).List(context.TODO(),opts)
//				if len(svcs.Items) != 0 {
//					resourceIngress := newIngress(svcs.Items, namespace, s.options.KibanaDomianName)
//					ing, err := s.kubeClient.ExtensionsV1beta1().Ingresses(namespace).Create(context.TODO(),resourceIngress,metav1.CreateOptions{})
//					print(ing, err)
//				}
//			}
//		}
//	}
//	return nil
//}
//
//// nodeSyncLoop handles updating the hosts pointed to by all load
//// balancers whenever the set of nodes in the cluster changes.
//func (s *ServiceController) nodeSyncLoop() {
//	newHosts, err := s.nodeLister.ListWithPredicate(getNodeConditionPredicate())
//	if err != nil {
//		klog.Errorf("Failed to retrieve current set of nodes from node lister: %v", err)
//		return
//	}
//
//	klog.Infof("Detected change in list of current cluster nodes. New node set: %v",
//		nodeNames(newHosts))
//
//	// Try updating all services, and save the ones that fail to try again next
//	// round.
//	s.servicesToUpdate = s.cache.allServices()
//	numServices := len(s.servicesToUpdate)
//	klog.Infof("Successfully updated %d out of %d load balancers to direct traffic to the updated set of nodes",
//		numServices-len(s.servicesToUpdate), numServices)
//
//	s.knownHosts = newHosts
//}
//
//// ListKeys implements the interface required by DeltaFIFO to list the keys we
//// already know about.
//func (s *serviceCache) allServices() []*v1.Service {
//	s.mu.Lock()
//	defer s.mu.Unlock()
//	services := make([]*v1.Service, 0, len(s.serviceMap))
//	for _, v := range s.serviceMap {
//		services = append(services, v.state)
//	}
//	return services
//}
//
//func nodeNames(nodes []*v1.Node) sets.String {
//	ret := sets.NewString()
//	for _, node := range nodes {
//		ret.Insert(node.Name)
//	}
//	return ret
//}
//
//func getNodeConditionPredicate() corelisters.NodeConditionPredicate {
//	return func(node *v1.Node) bool {
//		// We add the master to the node list, but its unschedulable.  So we use this to filter
//		// the master.
//		if node.Spec.Unschedulable {
//			return false
//		}
//
//		// If we have no info, don't accept
//		if len(node.Status.Conditions) == 0 {
//			return false
//		}
//		for _, cond := range node.Status.Conditions {
//			// We consider the node for load balancing only when its NodeReady condition status
//			// is ConditionTrue
//			if cond.Type == v1.NodeReady && cond.Status != v1.ConditionTrue {
//				klog.V(4).Infof("Ignoring node %v with %v condition status %v", node.Name, cond.Type, cond.Status)
//				return false
//			}
//		}
//		return true
//	}
//}
