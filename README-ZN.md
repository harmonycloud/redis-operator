# redis-operator
[English Doc](./README.md)

redis-operator用来帮助运维人员在kubernetes快速部署各种模式的redis集群。目前支持集群模式和哨兵模式。

基于kubernetes平台和operator技术，可以最大程度保证redis的高可用性。

## 快速开始
部署operator
```
# 安装crd
kubectl apply -f chart/redis-operator/redis-crd.json

# 安装operator
helm install redis-operator chart/redis-operator
```
部署redis
```
# 使用kubectl安装
kubectl apply -f example/redis-cluster.yaml
# 使用helm安装
helm install redis chart/redis
```
验证

保证pod全部处于running状态
```
# kubectl get pod
NAME                              READY   STATUS    RESTARTS   AGE
redis-0                           2/2     Running   0          6m42s
redis-1                           2/2     Running   0          6m41s
redis-2                           2/2     Running   0          6m40s
redis-3                           2/2     Running   0          6m35s
redis-4                           2/2     Running   0          6m41s
redis-5                           2/2     Running   0          6m37s
redis-operator-695898677d-xcjl8   1/1     Running   0          6m47s
```
phase为Running表示redis cluster处于正常运行状态
```
kubectl get rediscluster redis -o yaml  |grep phase
phase: Running
```
## 架构
### cluster模式
cluster模式在原生cluster基础上进行了改造
1. 快速部署集群，节点自动初始化加入集群
2. 每个节点通过slots机制，分散reids数据，保证数据可靠性
3. 自动化节点扩缩容
4. k8s service对每个reids实例进行负载均衡访问
![](.img/cluster.png)
   
### 哨兵模式
哨兵模式在原生redis哨兵上做了一定的扩展。
1. redis在创建之初会自动化配置主从集群，包括master和slave角色
2. 上述步骤完成后，启动哨兵开始监听redis master和slave，基于哨兵原生的能力对master进行保障。
3. 用户可以基于哨兵的入口服务访问redis；同时也可以通过redis 主从的读写分离服务访问redis

![](.img/sentinel.png)

## 镜像打包
设置镜像仓库
```
export registry="192.168.56.3:30088/middleware"
export tag=v1.5.6
```
编译源码并打包镜像
```
cd redis-operator
make docker-build  IMG=$registry/redis-operator:$tag
make docker-push IMG=$registry/redis-operator:$tag
```

打包其他镜像
```
cd build 
make docker-build 
make docker-push 
```

## 更多
更多文档，请参考 [详细文档](https://www.yuque.com/nq4era/chqywm/tgbepk)