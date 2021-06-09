#!/bin/bash

#需要用redis-cli -h {redis实例IP} ping查看redis是否正常
#用redis-cli -c -h {redis实例IP} -a {redis密码} cluster info输出
#的信息解析cluster_state的值是否为ok，以及cluster_known_nodes的值是
#否为1，判断redis集群是否正常；如果redis集群刚创建，cluster_known_nodes
#为1，cluster_state为fail;如果redis集群为纵向扩容(扩CPU、内存)升级重启
#cluster_known_nodes不为1，cluster_state为ok时才认为集群正常，才能重启
#下一个pod，改健康检查脚本旨在维护升级时redis集群状态，不在operator中维护
# 利用好statefulset一个实例ready后重启下一个pod的特性
# 只配置readiness健康检查, 不配置liveness(压力大时redis进程卡住不能重启)

# get redis cluster password from redis.conf
# requirepass myRedispwd
CONFIG_PATH="${PATHPREFIX}/redis/${DOMAINNAME}/${NAMESPACE}/${REDIS_CLUSTER_NAME}/${POD_NAME}/config/redis.conf"
# requirepass abc
# requirepass "abc"
password=$(cat ${CONFIG_PATH} | grep -E "^\s*requirepass" | awk '{print $2}' | sed 's/\"//g')

pingres=$(redis-cli -h ${PODIP} -a "${password}" -p ${REDIS_INSTANCE_PORT} ping)

# cluster_state:ok
# cluster_slots_assigned:16384
# cluster_slots_ok:16384
# cluster_slots_pfail:0
# cluster_slots_fail:0
# cluster_known_nodes:6
# cluster_size:3
# cluster_current_epoch:15
# cluster_my_epoch:12
# cluster_stats_messages_sent:270782059
# cluster_stats_messages_received:270732696
pingres=$(echo "${pingres}" | sed 's?\r??g')
if [[ "$pingres"x = "PONG"x ]]; then
    echo "--2--"
    exit 0
else
    exit 1
fi