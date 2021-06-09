# 安装Redis Operator
```
helm upgrade --install redis-operator . --namespace <YOUR-NAMESPACE>
```
# 卸载Redis Operator
```
helm delete reids-operator --namespace <YOUR-NAMESPACE>
```
# Redis Operator chart 的配置说明

|  参数|  描述| 默认值 |
| --- | --- | --- |
| replicaCount | 实例数 | 1 |
| image.repository | 镜像地址 | k8s-deploy/mysql-operator:v1.0.9 |
| image.pullPolicy | 镜像的拉取策略 | IfNotPresent |
| image.tag | 镜像的tag号 | v1.0.9 |
| imagePullSecrets | 镜像拉取的secret | [] |
| nameOverride | 重命名chart的名称 |  |
| fullnameOverride |  |  |
| podAnnotations | 设置pod 的annotation | {} |
| podSecurityContext | 设置Pod 的SecurityContext | 见values.yaml |
| securityContext | 设置容器的SecurityContext | 见values.yaml |
| resources | 设置Pod 的资源 | 见values.yaml |
| tolerations | 配置Pod容忍 | [] |
| affinity | 配置亲和 | {} |


