# 安装Redis集群
```
1. 设置参数type的值为cluster
2. 根据实际场景调整参数redis集群实例数redis.replicas和实例资源redis.resources
3. 安装
   helm upgrade --install redis . --namespace <YOUR-NAMESPACE>
```
# 安装Redis哨兵
```
1. 设置参数type的值为sentinel
2. 根据实际场景调整参数redis主从集群实例数redis.replicas和实例资源redis.resources, 调整哨兵集群的实例数sentinel.replicas和实例资源sentinel.resources
   温馨提示: 假设redis主从集群实例数设置为6, 实际部署的主从集群是一主五从
3. 安装
   helm upgrade --install redis . --namespace <YOUR-NAMESPACE>
```
# 卸载Redis集群
```
helm delete redis --namespace <YOUR-NAMESPACE>
```
# 集群chart 的配置说明

|  参数|  描述| 参数值 |
| --- | --- | --- |
| redisPassword | redis的密码 | dangerous |
| redisServicePort | redis的service端口 | 6379 |
| storageSize | 配置存储的大小 | 5G |
| storageClassName | 配置存储的storageClass名称 | default |
| type | 集群的类型，哨兵/集群 | sentinel/cluster |
| replicaCount | 实例数 | 6, 6代表三主三从，10代表五主五从 |
| image | 配置集群镜像信息 | 见values.yaml |
| redisMaxMemory | 设置redis最大可用物理内存 | 建议设置pod 内存limit 的80%，单位为mb和gb |
| resources | 设置Pod 资源 | 见values.yaml |
| redis | 设置redis集群实例的数目和资源 | 6 |
| sentinel | 设置哨兵集群的实例和资源 | 6 |


# 集群运维说明
## 集群CR 的配置说明

```
apiVersion: redis.middleware.hc.cn/v1alpha1
kind: RedisCluster
metadata:
  labels:
    nephele/user: admin
  name: testredis #Redis 集群的名称
  namespace: kube-system #Redis集群的分区
spec:
  type: cluster #集群的类型, 支持sentinel和cluster
  pod:
  - affinity: {}
    annotations:
      fixed-node-middleware-pod: "true"
      fixed.ipam.harmonycloud.cn: "true"
    configmap: testredis-config #Redis 集群的configmap
    env:
    - name: MAXMEMORY #Redis最大占用内存,建议配置成总内存的80%
      value: 409.6m
    - name: SYS_CODE #Redis所属服务的code name,用于定义redis日志文件的名称
      value: testredis
    - name: REDIS_CLUSTER_NAME #Redis集群名称
      value: testredis
    initImage: redis-init:v3 # init container镜像
    labels:
      harmonycloud.cn/statefulset: testredis
      middleware: redis
      nephele/user: admin
    middlewareImage: redis-cli-v5-port:5.0.8 #redis镜像
    monitorImage: redis-exporter:v1 #redis的 exporter镜像
    requirepass: 59eeccd21986da43987574a8f21aaa10 #Redis密码,需base64加密
    resources:#集群资源配置
      limits:
        cpu: "0.2"
        memory: 512Mi
      requests:
        cpu: "0.2"
        memory: 512Mi
    updateStrategy: {}
  podManagementPolicy: Parallel
  replicas: 6 #Redis 实例数支持的实例数为6和10. 6代表三主三从，10代表五主五从
  repository: 10.1.10.89/k8s-deploy/
  updateStrategy:
    assignStrategies: []
  version: 5.0.8 #Redis的版本
  volumeClaimTemplates: #定义Redis的动态供应存储信息
  - metadata:
      name: redis-data #此值不可修改
    spec:
      accessModes: [ "ReadWriteOnce" ]
      storageClassName: "default"#StorageClass 名称，按实际场景修改
      resources:
        requests:
          storage: 5Gi #每个实例申请的存储大小，按实际场景修改
```


## 哨兵模式
### 功能
1. 主从节点+哨兵节点部署
2. 主从节点数据持久化，哨兵节点没有数据持久化(动态设置监听)
3. 哨兵节点主动节点的监控和master的故障切换
4. 主从节点的扩容和缩容支持
5. 哨兵节点的扩容和缩容支持
6. 哨兵的exporter

###集群健康状态检查
```
kubectl get rediscluster redis -o yaml --namespace <YOUR-NAMESPACE>
查看statue,  当phase 和 sentinelPhase 都是running，说明redis和哨兵均已经初始化完成
```

### 详细流程
redis初始化
1. 首先创建statefulset和service(默认将每个节点都设置为slaveof 127.0.0.1)
2. 判断master数量, 保证master只有1个
  2.1 0个master: 如果只有1个节点，直接设置为master。等待一段时间(远大于哨兵选主时间)，否则将最旧的redis作为master，将其他master改成slave 加入新master
  2.2 1个master，通过
  2.3 多个master，异常
3. 再次判断master数量保证为1，确保刚刚的master是正确的
4. 检查所有的slave是否都设置了该master
5. 如果没有那么设置
6. 初始完成

哨兵初始化
1. 等待主从初始化完成
2. 创建deployment和servcie
3. 获取当前主从集群的master，设置哨兵监控此master
4. 检查哨兵中的数量和slave数量，并且重新载入配置
5. 更新状态

master宕机
1. 哨兵自动从剩下的slave中选出其中一个master，同步修改配置

redis扩容
1. 直接新增slave节点

redis缩容
1. 检查是否涉及到master
2. 如果涉及到master。让哨兵自己选择新的master

哨兵缩容
1. 直接扩容
2. 获取当前主从集群的master，设置新的哨兵监控此master

哨兵缩容
1. 直接缩容

### 初始化
启动主从redis以及哨兵
```
[root@master-212 ~]# kubectl get pod
NAME                                     READY   STATUS      RESTARTS   AGE    IP               NODE         NOMINATED NODE   READINESS GATES
redis-0                                  1/1     Running     0          6m2s    10.168.207.214   slave-214    <none>           <none>
redis-1                                  1/1     Running     0          6m      10.168.152.168   master-212   <none>           <none>
redis-2                                  1/1     Running     0          6m      10.168.145.94    slave-213    <none>           <none>
redis-3                                  1/1     Running     0          6m      10.168.207.217   slave-214    <none>           <none>
redis-4                                  1/1     Running     0          6m      10.168.152.175   master-212   <none>           <none>
redis-5                                  1/1     Running     0          6m      10.168.145.73    slave-213    <none>           <none>
sentinel-redis-7d87bb6b45-b5w5x          2/2     Running     0          5m48s   10.168.207.228   slave-214    <none>           <none>
sentinel-redis-7d87bb6b45-n7wrq          2/2     Running     0          5m48s   10.168.152.167   master-212   <none>           <none>
```
默认会是redis-0 作为master, 进入redis-cli执行` info replication`,
可以发现slaves都已经连接上了，ip 和数量都能对上
```
[root@master-212 ~]# kubectl exec -it r-redis-0 bash
[root@master-212 ~]# redis-cli -h  10.168.207.214 -p 6379 -a "dangerous" -c
10.168.207.236:6379> info replication
# Replication
role:master
connected_slaves:5
slave0:ip=10.168.145.94,port=6379,state=online,offset=1720493,lag=0
slave1:ip=10.168.152.168,port=6379,state=online,offset=1720207,lag=1
slave2:ip=10.168.207.217,port=6379,state=online,offset=1720493,lag=0
slave3:ip=10.168.145.73,port=6379,state=online,offset=1720493,lag=0
slave4:ip=10.168.152.175,port=6379,state=online,offset=1720207,lag=1
master_replid:fb15987a6033e3c7884a2cd6821c4e6f3a43c57f
master_replid2:0000000000000000000000000000000000000000
master_repl_offset:1720493
second_repl_offset:-1
repl_backlog_active:1
repl_backlog_size:268435456
repl_backlog_first_byte_offset:1664713
repl_backlog_histlen:55781
```
进入哨兵，查看监听情况，masterip和slaves数量也都是正确的
```
[root@master-212 ~]# redis-cli -h  10.168.152.163  -p 26379 -a "dangerous" -c
10.168.152.163:26379> info sentinel
# Sentinel
sentinel_masters:1
sentinel_tilt:0
sentinel_running_scripts:0
sentinel_scripts_queue_length:0
sentinel_simulate_failure_flags:0
master0:name=mymaster,status=ok,address=10.168.207.214:6379,slaves=5,sentinels=2
```
查看服务, 目前有master的服务(可读可写), 一般的服务(用来读)和哨兵服务。会自动更新
```
[root@master-212 ~]# kubectl get svc |grep redis
master-redis     ClusterIP   10.96.108.145    <none>        6379/TCP             8m22s
redis            ClusterIP   None             <none>        6379/TCP             8m22s
sentinel-redis   ClusterIP   10.96.207.2      <none>        26379/TCP,9121/TCP   8m8s

[root@master-212 ~]# kubectl get example |grep redis
master-redis     10.168.207.214:6379                                                        8m40s
redis            10.168.145.73:6379,10.168.145.94:6379,10.168.152.168:6379 + 3 more...      8m40s
sentinel-redis   10.168.152.167:9121,10.168.207.228:9121,10.168.152.167:26379 + 1 more...   8m26s
```
查看状态，和上述信息也保持一致
```
...
status:
  conditions:
  - hostIP: 10.1.11.214
    instance: 10.168.207.214:6379
    lastTransitionTime: "2021-01-11T11:18:03Z"
    name: redis-0
    status: "true"
    type: master
  - hostIP: 10.1.11.212
    instance: 10.168.152.168:6379
    lastTransitionTime: "2021-01-11T11:18:06Z"
    name: redis-1
    status: "true"
    type: slave
  - hostIP: 10.1.11.213
    instance: 10.168.145.94:6379
    lastTransitionTime: "2021-01-11T11:18:11Z"
    name: redis-2
    status: "true"
    type: slave
  - hostIP: 10.1.11.214
    instance: 10.168.207.217:6379
    lastTransitionTime: "2021-01-11T11:18:08Z"
    name: redis-3
    status: "true"
    type: slave
  - hostIP: 10.1.11.212
    instance: 10.168.152.175:6379
    lastTransitionTime: "2021-01-11T11:18:09Z"
    name: redis-4
    status: "true"
    type: slave
  - hostIP: 10.1.11.213
    instance: 10.168.145.73:6379
    lastTransitionTime: "2021-01-11T11:18:11Z"
    name: redis-5
    status: "true"
    type: slave
  formedClusterBefore: false
  phase: Running
  replicas: 6
  sentinelConditions:
  - hostIP: 10.1.11.214
    instance: 10.168.207.228:6379
    lastTransitionTime: "2021-01-11T11:18:22Z"
    name: sentinel-redis-7d87bb6b45-b5w5x
    status: "true"
    type: sentinel
  - hostIP: 10.1.11.212
    instance: 10.168.152.167:6379
    lastTransitionTime: "2021-01-11T11:18:17Z"
    name: sentinel-redis-7d87bb6b45-n7wrq
    status: "true"
    type: sentinel
  sentinelPhase: Running
  sentinelReplicas: 2
```
### 模拟master宕机
由哨兵自动选举新master，operator负责新pod的注册和master service的切换
```
[root@master-212 ~]#  kubectl delete pod redis-0
```
查看哨兵日志， master自动从10.168.207.214切换到10.168.145.94
ip 10.168.207.214 被监听列表移除；新的ip 10.168.207.230被加到监听列表
```
1:X 11 Jan 2021 11:28:30.815 # +switch-master mymaster 10.168.207.214 6379 10.168.145.94 6379
1:X 11 Jan 2021 11:28:30.816 * +slave slave 10.168.152.168:6379 10.168.152.168 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:28:30.816 * +slave slave 10.168.145.73:6379 10.168.145.73 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:28:30.816 * +slave slave 10.168.207.217:6379 10.168.207.217 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:28:30.816 * +slave slave 10.168.152.175:6379 10.168.152.175 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:28:30.816 * +slave slave 10.168.207.214:6379 10.168.207.214 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:00.823 # +sdown slave 10.168.207.214:6379 10.168.207.214 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:01.013 * +slave slave 10.168.207.230:6379 10.168.207.230 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:08.130 # +reset-master master mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:09.503 * +sentinel sentinel ec3b59014d777e2980dfc27505f7c03ad70f2b9b 10.168.152.167 26379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:11.062 * +slave slave 10.168.152.168:6379 10.168.152.168 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:11.063 * +slave slave 10.168.145.73:6379 10.168.145.73 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:11.067 * +slave slave 10.168.207.217:6379 10.168.207.217 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:11.070 * +slave slave 10.168.152.175:6379 10.168.152.175 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:29:11.070 * +slave slave 10.168.207.230:6379 10.168.207.230 6379 @ mymaster 10.168.145.94 6379
```
新建的pod
```
redis-0                                  1/1     Running     0          3m29s   10.168.207.230   slave-214    <none>           <none>
```
查看ep, master service也切换到了 10.168.145.94
```
[root@master-212 ~]#  kubectl get ep master-redis
NAME           ENDPOINTS            AGE
master-redis   10.168.145.94:6379   15m
```

### redis扩容
扩容到8个节点, 下面是新加的
```
redis-6                                  1/1     Running     0          2m49s   10.168.207.227   slave-214    <none>           <none>
redis-7                                  1/1     Running     0          2m49s   10.168.152.169   master-212   <none>           <none>
```
查看哨兵日志
```
1:X 11 Jan 2021 11:39:03.342 * +slave slave 10.168.207.227:6379 10.168.207.227 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:39:03.343 * +slave slave 10.168.152.169:6379 10.168.152.169 6379 @ mymaster 10.168.145.94 6379
```
master日志
```
# Replication
role:master
connected_slaves:7
slave0:ip=10.168.152.168,port=6379,state=online,offset=1863399,lag=0
slave1:ip=10.168.145.73,port=6379,state=online,offset=1863399,lag=1
slave2:ip=10.168.207.217,port=6379,state=online,offset=1863399,lag=0
slave3:ip=10.168.152.175,port=6379,state=online,offset=1863399,lag=0
slave4:ip=10.168.207.230,port=6379,state=online,offset=1863399,lag=0
slave5:ip=10.168.207.227,port=6379,state=online,offset=1863399,lag=0
slave6:ip=10.168.152.169,port=6379,state=online,offset=1863399,lag=0
```
### redis缩容
缩容到4个节点
```
redis-0                                  1/1     Running     0          17m    10.168.207.230   slave-214    <none>           <none>
redis-1                                  1/1     Running     0          27m    10.168.152.168   master-212   <none>           <none>
redis-2                                  1/1     Running     0          27m    10.168.145.94    slave-213    <none>           <none>
redis-3                                  1/1     Running     0          27m    10.168.207.217   slave-214    <none>           <none>
```
```
1:X 11 Jan 2021 11:43:52.165 # +reset-master master mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:43:54.099 * +sentinel sentinel ec3b59014d777e2980dfc27505f7c03ad70f2b9b 10.168.152.167 26379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:43:54.557 * +slave slave 10.168.152.168:6379 10.168.152.168 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:43:54.558 * +slave slave 10.168.207.217:6379 10.168.207.217 6379 @ mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:43:54.559 * +slave slave 10.168.207.230:6379 10.168.207.230 6379 @ mymaster 10.168.145.94 6379
```
```
# Replication
role:master
connected_slaves:3
slave0:ip=10.168.152.168,port=6379,state=online,offset=1878889,lag=1
slave1:ip=10.168.207.217,port=6379,state=online,offset=1878889,lag=1
slave2:ip=10.168.207.230,port=6379,state=online,offset=1878889,lag=1
```
### redis缩容到替换master
目前master是redis-2, 尝试缩容到2，将masrer缩没掉.
可以发现master从redis-2变成redis-1，唯一的slaver edis-0还在
```
1:X 11 Jan 2021 11:47:50.382 # -monitor master mymaster 10.168.145.94 6379
1:X 11 Jan 2021 11:47:50.453 # +monitor master mymaster 10.168.152.168 6379 quorum 2
1:X 11 Jan 2021 11:47:50.512 # +set master mymaster 10.168.152.168 6379 auth-pass dangerous
1:X 11 Jan 2021 11:47:51.520 # +reset-master master mymaster 10.168.152.168 6379
1:X 11 Jan 2021 11:47:51.553 * +slave slave 10.168.207.230:6379 10.168.207.230 6379 @ mymaster 10.168.152.168 6379
1:X 11 Jan 2021 11:47:53.058 * +sentinel sentinel ec3b59014d777e2980dfc27505f7c03ad70f2b9b 10.168.152.167 26379 @ mymaster 10.168.152.168 6379
```
### 哨兵扩容
扩容到5个
```
sentinel-redis-7d87bb6b45-b5w5x          2/2     Running     0          40m     10.168.207.228   slave-214    <none>           <none>
sentinel-redis-7d87bb6b45-fmpb4          2/2     Running     0          16s     10.168.207.231   slave-214    <none>           <none>
sentinel-redis-7d87bb6b45-klrdr          2/2     Running     0          5m34s   10.168.145.77    slave-213    <none>           <none>
sentinel-redis-7d87bb6b45-n7wrq          2/2     Running     0          40m     10.168.152.167   master-212   <none>           <none>
sentinel-redis-7d87bb6b45-qw46b          2/2     Running     0          5m34s   10.168.152.166   master-212   <none>           <none>
```
```
1:X 11 Jan 2021 11:58:33.450 * +sentinel sentinel 2ebf1b8697ed53afe397497d2239564becfe6826 10.168.152.166 26379 @ mymaster 10.168.152.168 6379
1:X 11 Jan 2021 11:58:34.034 * +sentinel sentinel 2d3b4c0b6d200b0f4a59bd1e7da1efcc20e3748d 10.168.145.77 26379 @ mymaster 10.168.152.168 6379
1:X 11 Jan 2021 11:58:34.226 * +sentinel sentinel 319dbb9c52d26ce2b2fd2a0436d8531e547ab227 10.168.207.231 26379 @ mymaster 10.168.152.168 6379
1:X 11 Jan 2021 11:58:34.308 * +sentinel sentinel ec3b59014d777e2980dfc27505f7c03ad70f2b9b 10.168.152.167 26379 @ mymaster 10.168.152.168 6379
```
### 缩容到3个
```
sentinel-redis-7d87bb6b45-b5w5x          2/2     Running     0          43m     10.168.207.228   slave-214    <none>           <none>
sentinel-redis-7d87bb6b45-klrdr          2/2     Running     0          8m44s   10.168.145.77    slave-213    <none>           <none>
sentinel-redis-7d87bb6b45-n7wrq          2/2     Running     0          43m     10.168.152.167   master-212   <none>           <none>
```
```
1:X 11 Jan 2021 12:01:08.073 # +reset-master master mymaster 10.168.152.168 6379
1:X 11 Jan 2021 12:01:09.371 * +sentinel sentinel 2d3b4c0b6d200b0f4a59bd1e7da1efcc20e3748d 10.168.145.77 26379 @ mymaster 10.168.152.168 6379
1:X 11 Jan 2021 12:01:09.884 * +sentinel sentinel ec3b59014d777e2980dfc27505f7c03ad70f2b9b 10.168.152.167 26379 @ mymaster 10.168.152.168 6379
```
### exporter
默认export和sentiner容器再同一个pod中，访问sentiner的9121端口就可以查看metrice数据.
```
curl http://10.168.152.167:9121/metrics|grep master |grep -v "#"
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100  9390    0  9390    0     0  32832      0 --:--:-- --:--:-- --:--:-- 32832
redis_sentinel_master_ok_sentinels{master_address="10.168.207.214:6379",master_name="mymaster"} 2
redis_sentinel_master_ok_slaves{master_address="10.168.207.214:6379",master_name="mymaster"} 5
redis_sentinel_master_sentinels{master_address="10.168.207.214:6379",master_name="mymaster"} 2
redis_sentinel_master_slaves{master_address="10.168.207.214:6379",master_name="mymaster"} 5
redis_sentinel_master_status{master_address="10.168.207.214:6379",master_name="mymaster",master_status="ok"} 1
redis_sentinel_masters 1
```



