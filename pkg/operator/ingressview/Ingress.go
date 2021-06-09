package ingressview

//const (
//	ingresscontroller    = "nginx.ingress.kubernetes.io/rewrite-target"
//	ingresscontrollerref = "/$2"
//	resourceLabel        = "visual-resource"
//	hostLabel            = "visual-host"
//	clusterLabel         = "visual-clustername"
//)
//
////init resource ingress
//func newIngress(services []v1.Service, namespace string, domain string) *apps.Ingress {
//	str := map[string]string{
//		ingresscontroller: ingresscontrollerref,
//	}
//	ing := &apps.Ingress{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:        services[0].Annotations[resourceLabel] + "-ingress",
//			Annotations: str,
//			Labels: map[string]string{
//				"app": services[0].Annotations[resourceLabel] + "-ingress",
//			},
//			Namespace: namespace,
//		},
//		Spec: makeIngressSpec(services, domain),
//	}
//	return ing
//
//}
//
////make resource ingressspec
//func makeIngressSpec(services []v1.Service, domain string) apps.IngressSpec {
//	var rules []apps.IngressRule
//	ingressValues := makeIngressPath(services)
//	ingressRule := apps.IngressRule{
//		Host:             domain,
//		IngressRuleValue: *ingressValues,
//	}
//	rules = append(rules, ingressRule)
//	ingressSpec := apps.IngressSpec{
//		Rules: rules,
//	}
//
//	return ingressSpec
//}
//
////make httppath from service array
//func makeIngressPath(services []v1.Service) *apps.IngressRuleValue {
//	var ingressPaths []apps.HTTPIngressPath
//	for _, svc := range services {
//		ingressPath := apps.HTTPIngressPath{
//			Path: "/" + svc.Annotations[clusterLabel] + "(/|$)(.*)",
//			Backend: apps.IngressBackend{
//				ServiceName: svc.Name,
//				ServicePort: svc.Spec.Ports[0].TargetPort,
//			},
//		}
//		ingressPaths = append(ingressPaths, ingressPath)
//	}
//	value := &apps.IngressRuleValue{
//		HTTP: &apps.HTTPIngressRuleValue{
//			Paths: ingressPaths,
//		},
//	}
//
//	return value
//}
