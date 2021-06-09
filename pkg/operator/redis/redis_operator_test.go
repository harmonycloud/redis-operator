package redis

import (
	"encoding/json"
	"errors"
	"fmt"
	. "harmonycloud.cn/middleware/redis-cluster/api/v1alpha1"
	"harmonycloud.cn/middleware/redis-cluster/util"
	errors2 "harmonycloud.cn/middleware/redis-cluster/util/errors"
	statuserrors "harmonycloud.cn/middleware/redis-cluster/util/errors"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildNodeInfo(t *testing.T) {

	lineInfo := "aaaaa 1.1.1.1:6379 myself,master - 0 0 6 connected 31-12 98-98 102-191 [asa<--asalssakjdhakjhk1h2kjh1j2k]"

	info := strings.Fields(lineInfo)
	//slave没有slot,也可能master没分配slot
	if len(info) >= 9 {
		Slot := strings.Join(info[8:], " ")
		t.Log(Slot)
	}
}

func TestGoFunc1(t *testing.T) {

	for {
		fmt.Println("000")
		create()
		fmt.Println("444")
		time.Sleep(10 * time.Minute)
	}

}

func create() {
	go func() {
		fmt.Println("1111")
		time.Sleep(20 * time.Second)
		fmt.Println("22222")
	}()
}

func TestGoFunc2(t *testing.T) {

	for {
		fmt.Println("000")
		create2()
		fmt.Println("444")
		time.Sleep(2 * time.Minute)
	}

}

func create2() {
	go func() {
		fmt.Println("1111")
		go func() {
			fmt.Println("333")
			time.Sleep(20 * time.Second)
			fmt.Println("555")
		}()
		fmt.Println("22222")
	}()
}

func TestDeferError(t *testing.T) {
	fmt.Println("111111111111")
	deferError()
	fmt.Println("22222222")
}

func deferError() (err error) {
	fmt.Println("3333")
	defer func() {
		fmt.Println(err)
	}()
	fmt.Println("4444")
	err = errors.New("测试defer error")
	return err
}

func TestDefer(t *testing.T) {
	defer fmt.Println("111111111111")
	defer fmt.Println("22222222")
}

func TestIota(t *testing.T) {
	fmt.Println(createCluster)
	fmt.Println(upgradeCluster)
	fmt.Println(dropCluster)
}

func TestIngoreCase(t *testing.T) {
	fmt.Println(strings.EqualFold("foreground", "Foreground"))
	fmt.Println(strings.EqualFold("ForeGround", "Foreground"))
}

func TestAssignMasterSlaveIP(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.102.77-slave"
	c := "10.10.103.155-build"
	d := "10.10.103.152-slave"
	e := "10.10.104.15-slave"
	f := "10.10.105.14-slave"
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.35",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.36",
						NodeName: &b,
					},
					{
						IP:       "10.168.33.119",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.120",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.186",
						NodeName: &d,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
	t.Logf("create masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	oldEndpoints := endpoints
	newEndpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.35",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.36",
						NodeName: &b,
					},
					{
						IP:       "10.168.33.119",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.120",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.186",
						NodeName: &d,
					},
					{
						IP:       "10.168.10.18",
						NodeName: &e,
					},
					{
						IP:       "10.168.11.192",
						NodeName: &f,
					},
					{
						IP:       "10.168.11.5",
						NodeName: &e,
					},
					{
						IP:       "10.168.12.9",
						NodeName: &e,
					},
				},
			},
		},
	}
	endpointAddresses, _ := rco.assignMasterSlaveIPAddress(newEndpoints, oldEndpoints)
	masterIP, slaveIP, err = rco.assignMasterSlaveIP(endpointAddresses)
	t.Logf("upgrade masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
}

func TestDeferSlice(t *testing.T) {

	var slaveInstanceIPs []string

	// print assign info when the method end
	defer func() {
		t.Logf("slaveInstanceIPs: %v", slaveInstanceIPs)
	}()

	slaveInstanceIPs = append(slaveInstanceIPs, "1.1.1.1", "2.2.2.2")
	t.Logf("slaveInstanceIPs: %v", slaveInstanceIPs)
}

func TestDeferErr(t *testing.T) {
	err := errors.New("111")

	defer func() {
		t.Logf("error: %v", err)
	}()
	err = errors.New("222")
}

func TestCreateAssignMasterSlaveIP(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.102.77-slave"
	c := "10.10.102.77-slave"
	d := "10.10.103.155-build"
	e := "10.10.103.155-build"
	f := "10.10.103.152-slave"
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &d,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &e,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &f,
					},
				},
			},
		},
	}

	for i := 0; i < 10; i++ {
		addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("create masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}

func TestCreateAssignMasterSlaveIPOneNode(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}

	//masterIP: [10.168.131.105 10.168.132.44 10.168.132.45]
	//slaveIP: [10.168.33.66 10.168.33.67 10.168.9.134]
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
}

func TestCreateAssignMasterSlaveIPTwoNode(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	// 5a 1b
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}

	//masterIP: [10.168.131.105 10.168.132.44 10.168.132.45]
	//slaveIP: [10.168.33.66 10.168.33.67 10.168.9.134]
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	// 4a 2b
	endpoints = &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}

	//masterIP: [10.168.132.44 10.168.131.105 10.168.132.45]
	//slaveIP: [10.168.33.66 10.168.33.67 10.168.9.134]
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err = rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	// 3a 3b
	endpoints = &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}

	//masterIP: [10.168.131.105 10.168.132.44 10.168.132.45]
	//slaveIP: [10.168.33.67 10.168.33.66 10.168.9.134]
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err = rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
}

func TestCreateAssignMasterSlaveIPThreeNode1(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 3a 2b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &c,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPThreeNode2(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"
	// 4a 1b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPThreeNode3(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 3a 2b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &c,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPThreeNode4(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 2a 2b 2c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &c,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &b,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}

}
func TestCreateAssignMasterSlaveIPThreeNode5(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 2a 3b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}

func TestCreateAssignMasterSlaveIPFourNode(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-bb"
	d := "10.10.103.154-cc"
	// 3a 1b 1c 1d
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &d,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}

	// 2a 2b 1c 1d
	endpoints = &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &d,
					},
				},
			},
		},
	}
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}

	// 2a 2b 1c 1d
	endpoints = &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &c,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &d,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPFourNode1(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-bb"
	d := "10.10.103.154-cc"

	// 2a 2b 1c 1d
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				// c ("10.168.131.105") d("10.168.132.44")
				//a ("10.168.132.45", "10.168.33.66")  b("10.168.33.67", "10.168.9.134" )
				// [10.168.131.105", "10.168.132.44", "10.168.132.45", "10.168.9.134", "10.168.33.67", "10.168.33.66"  ]
				// Rotating the list sometimes helps to get better initial anti-affinity before the optimizer runs.
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &c,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &d,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}

func TestSliceRemoveIndex(t *testing.T) {

	interleaved := []string{"0", "1", "2"}

	removeIndex := 0

	//remove assigned addr
	// if interleaved = ["0", "1", "2"]
	// removeIndex = 0 -- >> interleaved[:0], interleaved[1:]...  -- >> ["1", "2"]
	// removeIndex = 1 -- >> interleaved[:1], interleaved[2:]...  -- >> ["0", "2"]
	// removeIndex = 2 -- >> interleaved[:2], interleaved[3:]...  -- >> ["0", "1"]
	interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)
	t.Logf("interleaved: %v", interleaved)

	interleaved = []string{"0", "1", "2"}
	removeIndex = 1
	interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)
	t.Logf("interleaved: %v", interleaved)

	interleaved = []string{"0", "1", "2"}
	removeIndex = 2
	interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)
	t.Logf("interleaved: %v", interleaved)

	/*
		len(interleaved): 3 cap(interleaved): 3
		interleaved: []
	*/
	interleaved = []string{"0", "1", "2"}
	t.Logf("len(interleaved): %v cap(interleaved): %v", len(interleaved), cap(interleaved))
	t.Logf("interleaved: %v", interleaved[3:])

	/*
		i: 0 v:
		i: 1 v:
		i: 2 v:
	*/
	interleaved = make([]string, 3, 10)
	for i, v := range interleaved {
		t.Logf("i: %v v: %v", i, v)
	}

	/**
	len(interleaved): 3 cap(interleaved): 10
	interleaved: []
	*/
	t.Logf("len(interleaved): %v cap(interleaved): %v", len(interleaved), cap(interleaved))
	t.Logf("interleaved: %v", interleaved[3:])
	// panic: runtime error: slice bounds out of range
	//t.Logf("interleaved: %v", interleaved[4:])

	interleaved = []string{"0", "1", "2", "3", "4", "5", "6"}

	// interleaved: [3 4 5 6]
	interleaved = interleaved[3:]
	t.Logf("interleaved: %v", interleaved)

	// interleaved: [3 4]
	interleaved = interleaved[0:2]
	t.Logf("interleaved: %v", interleaved)
}

func TestLoopMap(t *testing.T) {
	m := make(map[string]string)
	m["hello"] = "echo hello"
	m["world"] = "echo world"
	m["go"] = "echo go"
	m["is"] = "echo is"
	m["cool"] = "echo cool"

	sortedKeys := make([]string, 0)
	for k := range m {
		fmt.Println("k--", k)
		sortedKeys = append(sortedKeys, k)
	}

	// sort 'string' key in increasing order
	sort.Strings(sortedKeys)

	for _, k := range sortedKeys {
		fmt.Printf("k=%v, v=%v\n", k, m[k])
	}
}

func TestDeepEqualExcludeFiled(t *testing.T) {

	tempStatus1 := RedisClusterStatus{
		Conditions: []RedisClusterCondition{
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.122",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "xxxx",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.122",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "qqqqqq",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
		},
	}

	tempStatus2 := RedisClusterStatus{
		Conditions: []RedisClusterCondition{
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.123",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "qqqqqq",
				Name:               "redis-cluster-1",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.1",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "xxxx",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
		},
	}

	sort.SliceStable(tempStatus2.Conditions, func(i, j int) bool {
		name1 := tempStatus2.Conditions[i].Name
		name2 := tempStatus2.Conditions[j].Name
		return name1 < name2
	})

	t.Logf("tempStatus1 equal tempStatus2: %v", util.NotUpdateRedisClusterStatus(tempStatus1, tempStatus2))
}

func TestRedisTribInfoReg(t *testing.T) {
	reg := `([\d.]+):6379 \((\w+)...\) -> (\d+) keys \| (\d+) slots \| (\d+) slaves`
	infos := `10.168.33.80:6379 (9ffde2b6...) -> 0 keys | 5461 slots | 1 slaves.
10.168.9.165:6379 (c9537d65...) -> 0 keys | 5461 slots | 1 slaves.
10.168.32.72:6379 (27288e18...) -> 0 keys | 5462 slots | 1 slaves.
[OK] 0 keys in 3 masters.
0.00 keys per slot on average.`

	compile := regexp.MustCompile(reg)

	submatch := compile.FindStringSubmatch(infos)

	for i, v := range submatch {
		t.Log(i, " -->> ", v)
	}
}

func TestChangeEndpoints(t *testing.T) {
	endpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "10.168.131.105",
						TargetRef: &v1.ObjectReference{
							Name:      "pod3",
							Namespace: "redis",
						},
					},
					{
						IP: "10.168.132.44",
						TargetRef: &v1.ObjectReference{
							Name:      "pod1",
							Namespace: "redis",
						},
					},
					{
						IP: "10.168.132.45",
						TargetRef: &v1.ObjectReference{
							Name:      "pod2",
							Namespace: "redis",
						},
					},
				},
			},
		},
	}

	sortEndpointsByPodName(endpoints)

	t.Log(endpoints)
}

func TestComposeMasterSlaveIP(t *testing.T) {

	nodeName0 := "10.10.103.66-share"
	nodeName1 := "10.10.103.60-master"
	nodeName2 := "10.10.102.43-share"
	nodeName3 := "10.10.103.61-slave"
	nodeName4 := "10.10.103.66-share"
	nodeName5 := "10.10.103.60-master"
	nodeName6 := "10.10.103.66-share"
	nodeName7 := "10.10.102.43-share"
	nodeName8 := "10.10.103.66-share"
	nodeName9 := "10.10.103.60-master"

	newAddresses := []v1.EndpointAddress{
		{
			IP:       "10.168.7.224",
			Hostname: "example000-redis-cluster-0",
			NodeName: &nodeName0,
		},
		{
			IP:       "10.168.131.69",
			Hostname: "example000-redis-cluster-1",
			NodeName: &nodeName1,
		},
		{
			IP:       "10.168.167.89",
			Hostname: "example000-redis-cluster-2",
			NodeName: &nodeName2,
		},
		{
			IP:       "10.168.246.107",
			Hostname: "example000-redis-cluster-3",
			NodeName: &nodeName3,
		},
		{
			IP:       "10.168.7.227",
			Hostname: "example000-redis-cluster-4",
			NodeName: &nodeName4,
		},
		{
			IP:       "10.168.131.70",
			Hostname: "example000-redis-cluster-5",
			NodeName: &nodeName5,
		},
		{
			IP:       "10.168.7.229",
			Hostname: "example000-redis-cluster-6",
			NodeName: &nodeName6,
		},
		{
			IP:       "10.168.167.90",
			Hostname: "example000-redis-cluster-7",
			NodeName: &nodeName7,
		},
		{
			IP:       "10.168.7.193",
			Hostname: "example000-redis-cluster-8",
			NodeName: &nodeName8,
		},
		{
			IP:       "10.168.131.75",
			Hostname: "example000-redis-cluster-9",
			NodeName: &nodeName9,
		},
	}

	existedMasterInstanceIPs := []string{"10.168.167.89", "10.168.131.69", "10.168.246.107"}
	existedSlaveInstanceIPs := []string{"10.168.131.70", "10.168.7.227", "10.168.7.224"}

	masterSlaveConnector := make(map[string]string, 3)
	masterSlaveConnector["10.168.167.89"] = "10.168.131.70"
	masterSlaveConnector["10.168.131.69"] = "10.168.7.227"
	masterSlaveConnector["10.168.246.107"] = "10.168.7.224"

	willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err := composeMasterSlaveIP(newAddresses, existedMasterInstanceIPs, existedSlaveInstanceIPs, masterSlaveConnector)

	t.Logf("\nwillAddClusterMasterIPs: %v \nwillAddClusterSlaveIPs: %v \nslaveParentIps: %v \nerr: %v\n", willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err)
}

func TestComposeMasterSlaveIP1(t *testing.T) {

	nodeName0 := "vm-3"
	nodeName1 := "vm-6"
	nodeName2 := "vm-1"
	nodeName3 := "vm-4"
	nodeName4 := "vm-2"
	nodeName5 := "vm-6"
	nodeName6 := "vm-1"
	nodeName7 := "vm-3"
	nodeName8 := "vm-4"
	nodeName9 := "vm-6"

	newAddresses := []v1.EndpointAddress{
		{
			IP:       "10.168.8.87",
			Hostname: "example000-redis-cluster-0",
			NodeName: &nodeName0,
		},
		{
			IP:       "10.168.175.255",
			Hostname: "example000-redis-cluster-1",
			NodeName: &nodeName1,
		},
		{
			IP:       "10.168.177.180",
			Hostname: "example000-redis-cluster-2",
			NodeName: &nodeName2,
		},
		{
			IP:       "10.168.173.5",
			Hostname: "example000-redis-cluster-3",
			NodeName: &nodeName3,
		},
		{
			IP:       "10.168.12.245",
			Hostname: "example000-redis-cluster-4",
			NodeName: &nodeName4,
		},
		{
			IP:       "10.168.175.196",
			Hostname: "example000-redis-cluster-5",
			NodeName: &nodeName5,
		},
		{
			IP:       "10.168.177.153",
			Hostname: "example000-redis-cluster-6",
			NodeName: &nodeName6,
		},
		{
			IP:       "10.168.8.75",
			Hostname: "example000-redis-cluster-7",
			NodeName: &nodeName7,
		},
		{
			IP:       "10.168.173.52",
			Hostname: "example000-redis-cluster-8",
			NodeName: &nodeName8,
		},
		{
			IP:       "10.168.175.195",
			Hostname: "example000-redis-cluster-9",
			NodeName: &nodeName9,
		},
	}

	existedMasterInstanceIPs := []string{"10.168.177.180", "10.168.12.245", "10.168.8.87"}
	existedSlaveInstanceIPs := []string{"10.168.175.255", "10.168.175.196", "10.168.173.5"}

	masterSlaveConnector := make(map[string]string, 3)
	masterSlaveConnector["10.168.177.180"] = "10.168.175.255"
	masterSlaveConnector["10.168.12.245"] = "10.168.175.196"
	masterSlaveConnector["10.168.8.87"] = "10.168.173.5"

	willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err := composeMasterSlaveIP(newAddresses, existedMasterInstanceIPs, existedSlaveInstanceIPs, masterSlaveConnector)

	//master:["10.168.173.52","10.168.175.195"]
	//slave:["10.168.177.153","10.168.8.75"]
	//parent:["10.168.173.52","10.168.175.195"]

	t.Logf("\nwillAddClusterMasterIPs: %v \nwillAddClusterSlaveIPs: %v \nslaveParentIps: %v \nerr: %v\n", willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err)
}

func TestComposeMasterSlaveIP2(t *testing.T) {

	nodeName0 := "10.10.103.62-slave"
	nodeName1 := "10.10.103.63-share"
	nodeName2 := "10.10.102.43-share"
	nodeName3 := "10.10.103.61-slave"
	nodeName4 := "10.10.101.133-slave"
	nodeName5 := "10.10.103.62-slave"
	nodeName6 := "10.10.101.133-slave"
	nodeName7 := "10.10.102.43-share"
	nodeName8 := "10.10.103.61-slave"
	nodeName9 := "10.10.103.63-share"

	newAddresses := []v1.EndpointAddress{
		{
			IP:       "10.168.214.237",
			Hostname: "example000-redis-cluster-0",
			NodeName: &nodeName0,
		},
		{
			IP:       "10.168.250.203",
			Hostname: "example000-redis-cluster-1",
			NodeName: &nodeName1,
		},
		{
			IP:       "10.168.167.119",
			Hostname: "example000-redis-cluster-2",
			NodeName: &nodeName2,
		},
		{
			IP:       "10.168.246.73",
			Hostname: "example000-redis-cluster-3",
			NodeName: &nodeName3,
		},
		{
			IP:       "10.168.178.128",
			Hostname: "example000-redis-cluster-4",
			NodeName: &nodeName4,
		},
		{
			IP:       "10.168.214.231",
			Hostname: "example000-redis-cluster-5",
			NodeName: &nodeName5,
		},
		{
			IP:       "10.168.178.175",
			Hostname: "example000-redis-cluster-6",
			NodeName: &nodeName6,
		},
		{
			IP:       "10.168.167.120",
			Hostname: "example000-redis-cluster-7",
			NodeName: &nodeName7,
		},
		{
			IP:       "10.168.246.76",
			Hostname: "example000-redis-cluster-8",
			NodeName: &nodeName8,
		},
		{
			IP:       "10.168.250.225",
			Hostname: "example000-redis-cluster-9",
			NodeName: &nodeName9,
		},
	}

	existedMasterInstanceIPs := []string{"10.168.214.237", "10.168.250.203", "10.168.167.119", "10.168.246.73", "10.168.178.128"}
	var existedSlaveInstanceIPs []string
	//existedSlaveInstanceIPs := []string{"10.168.175.255", "10.168.175.196", "10.168.173.5"}

	masterSlaveConnector := make(map[string]string, 3)
	/*masterSlaveConnector["10.168.177.180"] = "10.168.175.255"
	masterSlaveConnector["10.168.12.245"] = "10.168.175.196"
	masterSlaveConnector["10.168.8.87"] = "10.168.173.5"*/

	willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err := composeMasterSlaveIP(newAddresses, existedMasterInstanceIPs, existedSlaveInstanceIPs, masterSlaveConnector)

	/*
		willAddClusterSlaveIPs: [10.168.178.175 10.168.167.120 10.168.246.76 10.168.214.231 10.168.250.225]
		slaveParentIps:         [10.168.214.237 10.168.250.203 10.168.167.119 10.168.246.73 10.168.178.128]
	*/

	t.Logf("\nwillAddClusterMasterIPs: %v \nwillAddClusterSlaveIPs: %v \nslaveParentIps: %v \nerr: %v\n", willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err)
}

const (
	// MaxUint defines the max unsigned int value.
	MaxUint = ^uint(0)
	// MaxInt defines the max signed int value.
	MaxInt = int(MaxUint >> 1)
	// MaxTotalPriority defines the max total priority value.
	MaxTotalPriority = MaxInt
	// MaxPriority defines the max priority value.
	MaxPriority = 10
	// MaxWeight defines the max weight value.
	MaxWeight = MaxInt / MaxPriority
	// DefaultPercentageOfNodesToScore defines the percentage of nodes of all nodes
	// that once found feasible, the scheduler stops looking for more nodes.
	DefaultPercentageOfNodesToScore = 50
)

func TestSchedulerPriority(t *testing.T) {
	t.Log(MaxWeight)
}

func TestMasterSlaveIPAgainstTheRulesCase(t *testing.T) {
	/*nodeName0 := "10.10.103.63-share"
	nodeName1 := "10.10.101.133-slave"
	nodeName2 := "10.10.102.43-share"
	nodeName3 := "10.10.103.61-slave"
	nodeName4 := "10.10.103.63-share"
	nodeName5 := "10.10.101.133-slave"

	nodeName6 := "10.10.103.62-slave"
	nodeName7 := "10.10.103.61-slave"
	nodeName8 := "10.10.102.43-share"
	nodeName9 := "10.10.103.62-share"*/

	nodeNames := []string{"10.10.103.63-share", "10.10.101.133-slave", "10.10.102.43-share", "10.10.103.61-slave", "10.10.103.63-share",
		"10.10.101.133-slave", "10.10.103.62-slave", "10.10.103.61-slave", "10.10.102.43-share", "10.10.103.62-slave"}

	ips := []string{"10.168.250.222", "10.168.178.152", "10.168.167.81", "10.168.246.100", "10.168.250.221", "10.168.178.131",
		"10.168.214.204", "10.168.246.64", "10.168.167.66", "10.168.214.192"}

	ipNodeMaps := make(map[string]string, 10)

	for i, ip := range ips {
		ipNodeMaps[ip] = nodeNames[i]
	}

	createAddresses := []v1.EndpointAddress{
		{
			IP:       "10.168.250.222",
			Hostname: "example000-redis-cluster-0",
			NodeName: &nodeNames[0],
		},
		{
			IP:       "10.168.178.152",
			Hostname: "example000-redis-cluster-1",
			NodeName: &nodeNames[1],
		},
		{
			IP:       "10.168.167.81",
			Hostname: "example000-redis-cluster-2",
			NodeName: &nodeNames[2],
		},
		{
			IP:       "10.168.246.100",
			Hostname: "example000-redis-cluster-3",
			NodeName: &nodeNames[3],
		},
		{
			IP:       "10.168.250.221",
			Hostname: "example000-redis-cluster-4",
			NodeName: &nodeNames[4],
		},
		{
			IP:       "10.168.178.131",
			Hostname: "example000-redis-cluster-5",
			NodeName: &nodeNames[5],
		},
	}

	/*	createEndpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: createAddresses,
			},
		},
	}*/

	rco := &RedisClusterOperator{}

	masterInstanceIps, slaveInstanceIps, err := rco.assignMasterSlaveIP(createAddresses)

	if err != nil {
		t.Fatalf(err.Error())
	}

	t.Logf("masterIPs: %v\nslaveIPs: %v\n", masterInstanceIps, slaveInstanceIps)

	//判断master是否在同一主机
	for _, masterAIP := range masterInstanceIps {

		for _, masterBIP := range masterInstanceIps {

			if masterBIP == masterAIP {
				continue
			}

			if ipNodeMaps[masterAIP] == ipNodeMaps[masterBIP] {
				t.Fatalf("master: %v and master: %v in same node: %v against the rules", masterAIP, masterBIP, ipNodeMaps[masterBIP])
			}
		}
	}

	masterSlaveConnector := make(map[string]string)
	//判断一对主从是否在同一主机
	for i, masterIP := range masterInstanceIps {
		if ipNodeMaps[masterIP] == ipNodeMaps[slaveInstanceIps[i]] {
			t.Fatalf("master: %v and it's slave: %v in same node: %v against the rules", masterIP, slaveInstanceIps[i], ipNodeMaps[masterIP])
		}

		masterSlaveConnector[masterIP] = slaveInstanceIps[i]
	}

	upgradeAddresses := []v1.EndpointAddress{
		{
			IP:       "10.168.250.222",
			Hostname: "example000-redis-cluster-0",
			NodeName: &nodeNames[0],
		},
		{
			IP:       "10.168.178.152",
			Hostname: "example000-redis-cluster-1",
			NodeName: &nodeNames[1],
		},
		{
			IP:       "10.168.167.81",
			Hostname: "example000-redis-cluster-2",
			NodeName: &nodeNames[2],
		},
		{
			IP:       "10.168.246.100",
			Hostname: "example000-redis-cluster-3",
			NodeName: &nodeNames[3],
		},
		{
			IP:       "10.168.250.221",
			Hostname: "example000-redis-cluster-4",
			NodeName: &nodeNames[4],
		},
		{
			IP:       "10.168.178.131",
			Hostname: "example000-redis-cluster-5",
			NodeName: &nodeNames[5],
		},
		{
			IP:       "10.168.214.204",
			Hostname: "example000-redis-cluster-6",
			NodeName: &nodeNames[6],
		},
		{
			IP:       "10.168.246.64",
			Hostname: "example000-redis-cluster-7",
			NodeName: &nodeNames[7],
		},
		{
			IP:       "10.168.167.66",
			Hostname: "example000-redis-cluster-8",
			NodeName: &nodeNames[8],
		},
		{
			IP:       "10.168.214.192",
			Hostname: "example000-redis-cluster-9",
			NodeName: &nodeNames[9],
		},
	}

	newMasterInstanceIPs, newSlaveInstanceIPs, _, err := composeMasterSlaveIP(upgradeAddresses, masterInstanceIps, slaveInstanceIps, masterSlaveConnector)

	if err != nil {
		t.Fatalf(err.Error())
	}

	t.Logf("newMasterInstanceIPs: %v\nnewSlaveInstanceIPs: %v\n", newMasterInstanceIPs, newSlaveInstanceIPs)

	masterInstanceIps = append(masterInstanceIps, newMasterInstanceIPs...)
	slaveInstanceIps = append(slaveInstanceIps, newSlaveInstanceIPs...)

	//判断master是否在同一主机
	for _, masterAIP := range masterInstanceIps {

		for _, masterBIP := range masterInstanceIps {

			if masterBIP == masterAIP {
				continue
			}

			if ipNodeMaps[masterAIP] == ipNodeMaps[masterBIP] {
				t.Fatalf("master: %v and master: %v in same node: %v against the rules", masterAIP, masterBIP, ipNodeMaps[masterBIP])
			}
		}
	}

	//判断一对主从是否在同一主机
	for i, masterIP := range masterInstanceIps {
		if ipNodeMaps[masterIP] == ipNodeMaps[slaveInstanceIps[i]] {
			t.Fatalf("master: %v and it's slave: %v in same node: %v against the rules", masterIP, slaveInstanceIps[i], ipNodeMaps[masterIP])
		}
	}
}

func TestSyncMap(t *testing.T) {

	//rediscluster := "kube-system/redis-cluster-yace-0"

	pod0 := "kube-system/redis-cluster-yace-0"
	pod1 := "kube-system/redis-cluster-yace-1"

	t.Logf("key0: %v", pod1[(len(pod1)-1):])

	key0 := pod0[0:strings.LastIndex(pod0, "-")]
	key1 := pod0[0:strings.LastIndex(pod1, "-")]

	t.Logf("key0: %v", key0)
	t.Logf("key1: %v", key1)

	for i := 0; i < 100; i++ {
		go func(i int) {
			redisClusterRequirePassMap.Store(fmt.Sprintf("rediscluster%v", i), strconv.Itoa(i))
		}(i)
	}

	for i := 0; i < 1000; i++ {
		go func(i int) {
			redisClusterRequirePassMap.Load(fmt.Sprintf("rediscluster%v", i))
		}(i)
	}
}

type aa struct {
	name string
	id   int
	b    *bb
}
type bb struct {
	age    int
	names  []string
	dances *cc
}
type cc struct {
	dances []string
}

func (a *aa) aFunc() {
	a.name = "aa"
	a.b.names = []string{"1", "2", "3"}
	fmt.Println("a=nil : ")
}

func TestCopyPcfg(t *testing.T) {
	a := &aa{}
	a.b = &bb{}
	a.b.names = []string{"1", "2"}
	a.b.dances = &cc{dances: []string{"cc"}}
	a.aFunc()

	aaa := *a.b
	aaa.names = []string{"1", "2", "4"}
	aaa.dances = &cc{dances: []string{"eee"}}

	fmt.Printf("a.b=%#v", a.b)
	fmt.Printf("a.b.dances=%#v", a.b.dances)
}

func TestModifyConditions(t *testing.T) {
	tempStatus1 := RedisClusterStatus{
		Conditions: []RedisClusterCondition{
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.122",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "xxxx",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.122",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "qqqqqq",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
		},
	}

	for _, condition := range tempStatus1.Conditions {
		condition.Status = "True"
	}

	t.Log(tempStatus1.Conditions)

}

func TestStringFold(t *testing.T) {

	if !strings.EqualFold("foreground", finalizersForeGround) {
		t.Fatal("not equal")
	}

	type tree struct {
		val                 int
		lefttree, righttree *tree
	}

	var a tree

	var b = &a

	t.Log(b)
}

func TestGoFuncAndWg(t *testing.T) {

	specificPodNames := []string{"pod1", "pod2", "pod3"}

	var (
		errs    []error
		delPods []string
		mu      = sync.Mutex{}
		wg      = sync.WaitGroup{}
	)
	appendError := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}

	delFunc := func(podName string) {
		t.Log(fmt.Sprintf("  rm -rf %v ", podName))

		appendError(errors.New("err" + podName))
	}

	//起线程执行
	for _, name := range specificPodNames {
		wg.Add(1)
		delPods = append(delPods, name)
		go func(name string) {
			defer wg.Done()
			delFunc(name)
		}(name)
	}
	wg.Wait()
}

func TestError(t *testing.T) {

	err := errors2.NewCreatePodFailedWhenCreateCluster("create failed", RedisClusterFailed)

	t.Log(errors2.Message(err))
	t.Log(errors2.ReasonType(err))
	t.Log(errors2.Phase(err))
	t.Log(errors2.IsCreatePodFailedWhenCreateCluster(err))

	t.Log(testDeferError())
}

func testDeferError() (err error) {

	defer func() {
		err = errors.New("222")
	}()

	err = errors.New("1111")

	initContainerLimits := map[v1.ResourceName]resource.Quantity{
		//50m
		v1.ResourceCPU: resource.MustParse("50m"),
		//100Mi
		v1.ResourceMemory: resource.MustParse("100Mi"),
	}

	a, _ := json.Marshal(initContainerLimits)

	fmt.Println(string(a))
	return
}

func TestSlice(t *testing.T) {

	var redisClusterStatusError error

	redisClusterStatusError = &statuserrors.StatusError{statuserrors.Status{
		Phase:      RedisClusterRollback,
		ReasonType: RedisClusterReasonTypeCreatePodFailedWhenUpgradeCluster,
		Message:    "aaa",
	}}

	copyErr := redisClusterStatusError
	t.Log(statuserrors.Phase(copyErr))

	redisClusterStatusError = errors.New("aaa")

	t.Log(statuserrors.Phase(redisClusterStatusError))

	var successNodes []string

	rlabels := map[string]string{
		"1": "a",
	}
	stsLabel := make(map[string]string)
	labelJson, _ := json.Marshal(rlabels)
	_ = json.Unmarshal(labelJson, &stsLabel)

	t.Log(stsLabel)

	successNodes = append(successNodes, "1")
	test1(&successNodes)
	t.Log(successNodes)
	return
}

func test1(successNodes *[]string) {
	*successNodes = append(*successNodes, "2")
}

func TestMap(t *testing.T) {

	timeout := int32(2)

	time1 := time.Now()
	t.Logf("before time1: %v", time1)
	redisClusterSyncStartTimeMap.Store("a/b", time1)

	va, _ := redisClusterSyncStartTimeMap.Load("a/b")

	t.Logf("after time1: %v", va)

	spendTime := time.Now().Sub(va.(time.Time)).Seconds()

	if spendTime > float64(timeout) {
		t.Log("timeout")
	} else {
		t.Log("0")
	}

	time.Sleep(5 * time.Second)
	spendTime = time.Now().Sub(va.(time.Time)).Seconds()

	if spendTime > float64(timeout) {
		t.Log("timeout")
	} else {
		t.Log("0")
	}
}

func TestRedisClusterStopWait(t *testing.T) {

	spec := `{"pod":[{"affinity":{},"annotations":{"fixed-node-middleware-pod":"true","fixed.ipam.harmonycloud.cn":"true"},"configmap":"redis-yace-0717-config","env":[{"name":"DOMAINNAME","value":"cluster.local"},{"name":"MAXMEMORY","value":"200mb"},{"name":"PATHPREFIX","value":"/data"},{"name":"SYS_CODE","value":"000ff248eca04b95a7dbab4514e45a2c"},{"name":"REDIS_CLUSTER_NAME","value":"redis-yace-0717"}],"initImage":"redis-init:v2","labels":{"harmonycloud.cn/projectId":"5615e83de79e498ab2162f92d51d6d45","harmonycloud.cn/statefulset":"redis-yace-0717","middleware":"redis","nephele/user":"admin"},"middlewareImage":"redis-cli-v5:3.2.6","monitorImage":"redis-exporter:v1","resources":{"limits":{"cpu":"100m","memory":"200Mi"},"requests":{"cpu":"100m","memory":"200Mi"}},"updateStrategy":{},"volumes":{"persistentVolumeClaimName":"redis-yace-0717-claim","type":"nfs"}}],"replicas":6,"repository":"172.22.242.235:30443/k8s-deploy/","updateStrategy":{},"version":"3.2.6"}`

	sspec := RedisClusterSpec{}
	err := json.Unmarshal([]byte(spec), &sspec)
	if err != nil {
		t.Fatal(err)
	}

	if strings.EqualFold(sspec.Finalizers, finalizersForeGround) {
		redisClusterStopWait.Store(fmt.Sprintf("%v/%v", "aa", "bb"), true)
	}

	value, isExist := redisClusterStopWait.Load(fmt.Sprintf("%v/%v","aa", "bb"))
	if isExist {
		if value.(bool) {
			t.Log("true....")
		}
	}
}

func TestGetPatch(t *testing.T) {

	rc := `{
    "apiVersion": "redis.middleware.hc.cn/v1alpha1",
    "kind": "RedisCluster",
    "metadata": {
        "creationTimestamp": "2019-08-21T11:22:57Z",
        "generation": 1,
        "name": "redis-yace-0820",
        "namespace": "kube-system",
        "resourceVersion": "48252533",
        "selfLink": "/apis/redis.middleware.hc.cn/v1alpha1/namespaces/kube-system/redisclusters/redis-yace-0820",
        "uid": "0695de93-c406-11e9-97fb-80b5754dfaa1"
    },
    "spec": {
        "pause": false,
        "pod": [
            {
                "affinity": {
                    "requiredDuringSchedulingIgnoredDuringExecution": {
                        "nodeSelectorTerms": [
                            {
                                "matchExpressions": [
                                    {
                                        "key": "HarmonyCloud_Status",
                                        "operator": "In",
                                        "values": [
                                            "C"
                                        ]
                                    }
                                ]
                            }
                        ]
                    }
                },
                "annotations": {
                    "fixed-node-middleware-pod": "true",
                    "fixed.ipam.harmonycloud.cn": "true"
                },
                "configmap": "redis-yace-0820-config",
                "env": [
                    {
                        "name": "DOMAINNAME",
                        "value": "cluster.local"
                    },
                    {
                        "name": "MAXMEMORY",
                        "value": "200mb"
                    },
                    {
                        "name": "PATHPREFIX",
                        "value": "/data"
                    },
                    {
                        "name": "SYS_CODE",
                        "value": "000ff248eca04b95a7dbab4514e45a2c"
                    },
                    {
                        "name": "REDIS_CLUSTER_NAME",
                        "value": "redis-yace-0820"
                    }
                ],
                "initImage": "redis-init:v2",
                "labels": {
                    "harmonycloud.cn/projectId": "5615e83de79e498ab2162f92d51d6d45",
                    "harmonycloud.cn/statefulset": "redis-yace-0820",
                    "middleware": "redis",
                    "nephele/user": "admin"
                },
                "middlewareImage": "redis-cli-v5:3.2.6",
                "monitorImage": "redis-exporter:v1",
                "resources": {
                    "limits": {
                        "cpu": "200m",
                        "memory": "100Mi"
                    },
                    "requests": {
                        "cpu": "200m",
                        "memory": "100Mi"
                    }
                },
                "updateStrategy": {},
                "volumes": {
                    "persistentVolumeClaimName": "redis-yace-0820-claim",
                    "type": "nfs"
                }
            }
        ],
        "replicas": 6,
        "repository": "172.22.242.235:30443/k8s-deploy/",
        "updateStrategy": {
            "assignStrategies": []
        },
        "version": "3.2.6"
    },
    "status": {
        "conditions": [
            {
                "domainName": "redis-yace-0820-0.redis-yace-0820.kube-system.svc.cs-hua.hpc",
                "hostIP": "172.22.242.237",
                "hostname": "ha-dlzx-l0401-poc",
                "instance": "172.22.242.228:6379",
                "lastTransitionTime": "2019-08-21T11:14:52Z",
                "masterNodeId": "-",
                "name": "redis-yace-0820-0",
                "namespace": "kube-system",
                "nodeId": "5247bd40f21fcb4774756f4717a5418f81f640e9",
                "slots": "0-5460",
                "status": "True",
                "type": "master"
            },
            {
                "domainName": "redis-yace-0820-1.redis-yace-0820.kube-system.svc.cs-hua.hpc",
                "hostIP": "172.22.242.239",
                "hostname": "ha-dlzx-l0403-poc",
                "instance": "172.22.242.192:6379",
                "lastTransitionTime": "2019-08-21T11:14:52Z",
                "masterNodeId": "-",
                "name": "redis-yace-0820-1",
                "namespace": "kube-system",
                "nodeId": "00657da4d022b25b0a6a9092d053bd4d01cebb4e",
                "slots": "5461-10922",
                "status": "True",
                "type": "master"
            },
            {
                "domainName": "redis-yace-0820-2.redis-yace-0820.kube-system.svc.cs-hua.hpc",
                "hostIP": "172.22.242.238",
                "hostname": "ha-dlzx-l0402-poc",
                "instance": "172.22.242.207:6379",
                "lastTransitionTime": "2019-08-21T11:14:52Z",
                "masterNodeId": "-",
                "name": "redis-yace-0820-2",
                "namespace": "kube-system",
                "nodeId": "e870ba9b790ace9dc5d890d1bd24804df69cbabc",
                "slots": "10923-16383",
                "status": "True",
                "type": "master"
            },
            {
                "domainName": "redis-yace-0820-3.redis-yace-0820.kube-system.svc.cs-hua.hpc",
                "hostIP": "172.22.242.237",
                "hostname": "ha-dlzx-l0401-poc",
                "instance": "172.22.242.208:6379",
                "lastTransitionTime": "2019-08-21T11:14:52Z",
                "masterNodeId": "e870ba9b790ace9dc5d890d1bd24804df69cbabc",
                "name": "redis-yace-0820-3",
                "namespace": "kube-system",
                "nodeId": "79a5d74d93f4e8e2c27c77a116aec1bf62fce021",
                "status": "True",
                "type": "slave"
            },
            {
                "domainName": "redis-yace-0820-4.redis-yace-0820.kube-system.svc.cs-hua.hpc",
                "hostIP": "172.22.242.239",
                "hostname": "ha-dlzx-l0403-poc",
                "instance": "172.22.242.215:6379",
                "lastTransitionTime": "2019-08-21T11:14:52Z",
                "masterNodeId": "5247bd40f21fcb4774756f4717a5418f81f640e9",
                "name": "redis-yace-0820-4",
                "namespace": "kube-system",
                "nodeId": "032863dfa5685149984386d1272369031bace0d1",
                "status": "True",
                "type": "slave"
            },
            {
                "domainName": "redis-yace-0820-5.redis-yace-0820.kube-system.svc.cs-hua.hpc",
                "hostIP": "172.22.242.238",
                "hostname": "ha-dlzx-l0402-poc",
                "instance": "172.22.242.221:6379",
                "lastTransitionTime": "2019-08-21T11:14:52Z",
                "masterNodeId": "00657da4d022b25b0a6a9092d053bd4d01cebb4e",
                "name": "redis-yace-0820-5",
                "namespace": "kube-system",
                "nodeId": "f5df6c0874df28f83b00b5d15916289094ea04dd",
                "status": "True",
                "type": "slave"
            }
        ],
        "exporterAddr": "172.22.242.226:19105",
        "exporterDomainName": "redis-yace-0820-exporter-0.redis-yace-0820-exporter.monitoring.svc.cs-hua.hpc",
        "formedClusterBefore": true,
        "phase": "Running",
        "replicas": 6
    }
}`

	redisCluster := RedisCluster{}

	err := json.Unmarshal([]byte(rc), &redisCluster)
	if err != nil {
		t.Fatal(err)
	}

	rcByte, err := getPatch(&redisCluster)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(string(rcByte))
}
