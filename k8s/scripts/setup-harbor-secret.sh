#!/bin/bash
set -euo pipefail

# 配置 Harbor Secret 脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${K8S_DIR}/config/deploy.env}"

# 加载配置
if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

REGISTRY="${REGISTRY:-harbor.pullot.com:28443}"
REGISTRY_USERNAME="${REGISTRY_USERNAME:-admin}"
REGISTRY_PASSWORD="${REGISTRY_PASSWORD:-}"
NAMESPACE="${NAMESPACE:-sandbox-system}"
SECRET_NAME="${SECRET_NAME:-harbor-registry-secret}"
SANDBOX_NAMESPACE="${SANDBOX_NAMESPACE:-sandbox}"
NAMESPACES="${NAMESPACES:-${NAMESPACE} ${SANDBOX_NAMESPACE}}"

echo "=== 配置 Harbor Secret ==="
echo "Registry: $REGISTRY"
echo "Namespaces: $NAMESPACES"
echo "Secret Name: $SECRET_NAME"
echo ""

# 如果没有提供密码，提示输入
if [ -z "$REGISTRY_PASSWORD" ]; then
    read -sp "请输入 Harbor 密码: " REGISTRY_PASSWORD
    echo ""
fi

for ns in $NAMESPACES; do
    # 创建 namespace（如果不存在）
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -

    # 创建或更新 secret
    kubectl create secret docker-registry "$SECRET_NAME" \
        --docker-server="$REGISTRY" \
        --docker-username="$REGISTRY_USERNAME" \
        --docker-password="$REGISTRY_PASSWORD" \
        --namespace="$ns" \
        --dry-run=client -o yaml | kubectl apply -f -
done

echo "✓ Harbor secret 已创建/更新"
