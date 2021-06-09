module harmonycloud.cn/middleware/redis-cluster

go 1.16

require (
	github.com/davecgh/go-spew v1.1.1
	github.com/go-logr/logr v0.3.0
	github.com/go-redis/redis v6.15.9+incompatible
	github.com/golang/groupcache v0.0.0-20191227052852-215e87163ea7
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/apiserver v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.2.0
	sigs.k8s.io/controller-runtime v0.7.2
)

replace k8s.io/api => k8s.io/api v0.19.2
