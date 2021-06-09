package options

import (
	"github.com/spf13/pflag"
	//"harmonycloud.cn/middleware/redis-cluster/config"
	"harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"time"
)

type OperatorManagerServer struct {
	v1alpha1.OperatorManagerConfig
	Master     string
	Kubeconfig string
}

const operatorManagerPort = 10880

func NewOMServer() *OperatorManagerServer {
	s := OperatorManagerServer{
		OperatorManagerConfig: v1alpha1.OperatorManagerConfig{
			ControllerStartInterval:     metav1.Duration{Duration: 0 * time.Second},
			Operators:                   []string{"*"},
			ClusterDomain:               "cluster.local",
			//LeaderElection:              config.DefaultLeaderElectionConfiguration(),
			//ConcurrentRedisClusterSyncs: 1,
			Port:                        operatorManagerPort,
			Address:                     "0.0.0.0",
			ResyncPeriod:                60,
			ClusterTimeOut:              20,
			//controller-manager的类型为"application/vnd.kubernetes.protobuf"但在这里有问题,导致同步事件AddFunc、UpdateFunc、delFunc出问题，
			// 不能加,后续细细研究,用于构建kubeConfig
			//ContentType:     "application/vnd.kubernetes.protobuf",
			KubeAPIQPS:           20.0,
			KubeAPIBurst:         30,
			EnableProfiling:      true,
			RevisionHistoryLimit: 3,
			KibanaDomianName:     "beta.kibana.svc.com",
		},
	}
	return &s
}

func (s *OperatorManagerServer) AddFlags(fs *pflag.FlagSet) {
	//fs.StringSliceVar(&s.Operators, "operators", s.Operators, fmt.Sprintf(""+
	//	"A list of operators to enable.  '*' enables all on-by-default operators, 'foo' enables the operator "+
	//	"named 'foo', '-foo' disables the operator named 'foo'.\nAll operators: %s", strings.Join(allOperators, ", ")))
	fs.StringVar(&s.Master, "master", s.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&s.KibanaDomianName, "kibanaDomainName", s.KibanaDomianName, "Kibana domain name)")
	fs.StringVar(&s.Kubeconfig, "kubeconfig", s.Kubeconfig, "Path to kubeconfig file with authorization and master location information.")
	fs.StringVar(&s.ClusterDomain, "cluster.local", s.ClusterDomain, "The k8s cluster domain in /var/lib/kubelet/config.yaml")
	fs.Int64Var(&s.ResyncPeriod, "resync-period", s.ResyncPeriod, "resync frequency in seconds")
	fs.Int32Var(&s.ClusterTimeOut, "clusterTimeOut", s.ClusterTimeOut, "cluster create or upgrade timeout in minutes")
	fs.Int32Var(&s.RevisionHistoryLimit, "revisionHistoryLimit", s.RevisionHistoryLimit, "middleware crd revision history limit")
	fs.Int32Var(&s.Port, "port", s.Port, "port of middleware operator")
	fs.DurationVar(&s.ControllerStartInterval.Duration, "operator-start-interval", s.ControllerStartInterval.Duration, "Interval between starting operator managers.")
	//config.BindFlags(&s.LeaderElection, fs)
	utilfeature.DefaultMutableFeatureGate.AddFlag(fs)
}
