#!/bin/bash

# redis init container init.sh exec use /bin/bash instead of /bin/sh

# 1. 获取初始化环境变量

# 1.1 获取
#POD_NAME=$(hostname)
DOMAIN_NAME=${DOMAINNAME}
POD_IP=${PODIP}
MAX_MEMORY=${MAXMEMORY}
PATHPREFIX=${PATHPREFIX}
REDIS_CLUSTER_NAME=${REDIS_CLUSTER_NAME}
SYS_CODE=${SYS_CODE}
REDIS_PASS=${REDIS_PASS}

# 1.2 检查环境变量
if [[ -z ${DOMAIN_NAME} ]]; then
    echo "env : DOMAINNAME do not set !"
    exit 1
fi

if [[ -z ${REDIS_CLUSTER_NAME} ]]; then
    echo "env : REDIS_CLUSTER_NAME do not set !"
    exit 1
fi

if [[ -z ${SYS_CODE} ]]; then
    echo "env : SYS_CODE do not set !"
    SYS_CODE=${DOMAIN_NAME}
fi

if [[ -z ${POD_NAME} ]]; then
    echo "env : hostname do not set !"
    exit 1
fi

if [[ -z ${POD_IP} ]]; then
    echo "env : PODIP do not set !"
    exit 1
fi

if [[ -z ${MAX_MEMORY} ]]; then
    MAX_MEMORY="409.6mb"
    echo "env : MAXMEMORY do not set !"
fi

if [[ -z ${PATHPREFIX} ]]; then
    echo "env : PATHPREFIX do not set, set default path prefix /nfs"
    PATHPREFIX="/nfs"
fi

if [[ -z ${REDIS_PASS} ]]; then
    echo "env : REDIS_PASS do not set, set default redis password 123456"
    REDIS_PASS="123456"
fi

BIN_PATH="${PATHPREFIX}/redis/${DOMAIN_NAME}/${NAMESPACE}/${REDIS_CLUSTER_NAME}/${POD_NAME}/bin"
CONFIG_PATH="${PATHPREFIX}/redis/${DOMAIN_NAME}/${NAMESPACE}/${REDIS_CLUSTER_NAME}/${POD_NAME}/config"
LOG_PATH="${PATHPREFIX}/redis/${DOMAIN_NAME}/${NAMESPACE}/${REDIS_CLUSTER_NAME}/${POD_NAME}/log"
DATA_PATH="${PATHPREFIX}/redis/${DOMAIN_NAME}/${NAMESPACE}/${REDIS_CLUSTER_NAME}/${POD_NAME}/data"

echo "------${CONFIG_PATH}"

# 2. 目录创建
mkdir -p $BIN_PATH
mkdir -p $CONFIG_PATH
mkdir -p $LOG_PATH
mkdir -p $DATA_PATH
chmod -R 644 ${PATHPREFIX}/redis/${DOMAIN_NAME}

# 3. pid验证
if [[ -f ${LOG_PATH}/redis-${REDIS_INSTANCE_PORT}.pid ]]; then
    echo "redis-${REDIS_INSTANCE_PORT}.pid already exists, the redis is running!"
fi

# 4. 拷贝configmaps配置进来的redis.conf文件到工作目录
cp /config/redis.conf ${CONFIG_PATH}/redis.conf

# 5. 填充占位符
sed -i "s/{DOMAIN_NAME}/${DOMAIN_NAME}/g" ${CONFIG_PATH}/redis.conf
sed -i "s/{POD_NAME}/${POD_NAME}/g" ${CONFIG_PATH}/redis.conf
sed -i "s/{PODIP}/${POD_IP}/g" ${CONFIG_PATH}/redis.conf
sed -i "s/{MAX_MEMORY}/${MAX_MEMORY}/g" ${CONFIG_PATH}/redis.conf
sed -i "s/{NAMESPACE}/${NAMESPACE}/g" ${CONFIG_PATH}/redis.conf
sed -i "s?{PATH_PREFIX}?${PATHPREFIX}?g" ${CONFIG_PATH}/redis.conf
sed -i "s?{REDIS_CLUSTER_NAME}?${REDIS_CLUSTER_NAME}?g" ${CONFIG_PATH}/redis.conf
sed -i "s?{SYS_CODE}?${SYS_CODE}?g" ${CONFIG_PATH}/redis.conf
sed -i "s?{REDIS_INSTANCE_PORT}?${REDIS_INSTANCE_PORT}?g" ${CONFIG_PATH}/redis.conf
sed -i "s?{REDIS_PASS}?${REDIS_PASS}?g" ${CONFIG_PATH}/redis.conf