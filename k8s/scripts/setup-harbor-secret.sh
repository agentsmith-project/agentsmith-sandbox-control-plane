#!/bin/bash
set -euo pipefail

#=============================================================================
# Harbor Secret 配置脚本
#
# Usage: ./setup-harbor-secret.sh [--namespace ns1,ns2] [--secret-name name]
#
# Environment Variables:
#   REGISTRY: Harbor registry URL (default: harbor.pullot.com:28443)
#   REGISTRY_USERNAME: Harbor username (default: admin)
#   REGISTRY_PASSWORD: Harbor password (prompts if not set)
#   NAMESPACE: Primary namespace (default: sandbox-system)
#   SECRET_NAME: Secret name (default: harbor-registry-secret)
#   NAMESPACES: Comma-separated list of namespaces
#=============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${K8S_DIR}/config/deploy.env}"

# Source utility libraries if available
# shellcheck source=lib/common.sh
if [ -f "${SCRIPT_DIR}/lib/common.sh" ]; then
    source "${SCRIPT_DIR}/lib/common.sh"
else
    # Minimal logging functions if common.sh is not available
    log_info() { echo -e "[INFO] $*"; }
    log_success() { echo -e "[✓] $*"; }
    log_warning() { echo -e "[!] $*"; }
    log_error() { echo -e "[✗] $*" >&2; }
fi

# Load configuration from file
if [ -f "$CONFIG_FILE" ]; then
    # shellcheck source=/dev/null
    source "$CONFIG_FILE"
fi

# Default values
REGISTRY="${REGISTRY:-harbor.pullot.com:28443}"
REGISTRY_USERNAME="${REGISTRY_USERNAME:-admin}"
REGISTRY_PASSWORD="${REGISTRY_PASSWORD:-}"
NAMESPACE="${NAMESPACE:-sandbox-system}"
SECRET_NAME="${SECRET_NAME:-harbor-registry-secret}"
SANDBOX_NAMESPACE="${SANDBOX_NAMESPACE:-sandbox}"
NAMESPACES="${NAMESPACES:-${NAMESPACE} ${SANDBOX_NAMESPACE}}"

log_info "=== 配置 Harbor Secret ==="
log_info "Registry: $REGISTRY"
log_info "Username: $REGISTRY_USERNAME"
log_info "Namespaces: $NAMESPACES"
log_info "Secret Name: $SECRET_NAME"
echo ""

# Validate required parameters
if [ -z "$REGISTRY" ]; then
    log_error "REGISTRY 环境变量未设置"
    exit 1
fi

if [ -z "$REGISTRY_USERNAME" ]; then
    log_error "REGISTRY_USERNAME 环境变量未设置"
    exit 1
fi

# Get password if not provided
if [ -z "$REGISTRY_PASSWORD" ]; then
    read -rsp "请输入 Harbor 密码: " REGISTRY_PASSWORD
    echo ""
fi

# Validate password was provided
if [ -z "$REGISTRY_PASSWORD" ]; then
    log_error "未提供 Harbor 密码"
    exit 1
fi

# Check kubectl connection
log_info "检查 Kubernetes 连接..."
if ! kubectl cluster-info &>/dev/null; then
    log_error "无法连接到 Kubernetes 集群"
    log_error "请检查 kubectl 配置"
    exit 1
fi
log_success "已连接到集群"

# Process each namespace
SUCCESS_COUNT=0
FAIL_COUNT=0

for ns in $NAMESPACES; do
    log_info "处理命名空间: $ns"

    # Create namespace if it doesn't exist
    if ! kubectl get namespace "$ns" &>/dev/null; then
        log_info "创建命名空间: $ns"
        if ! kubectl create namespace "$ns"; then
            log_error "创建命名空间失败: $ns"
            ((FAIL_COUNT++)) || true
            continue
        fi
    fi

    # Create or update secret
    if kubectl create secret docker-registry "$SECRET_NAME" \
        --docker-server="$REGISTRY" \
        --docker-username="$REGISTRY_USERNAME" \
        --docker-password="$REGISTRY_PASSWORD" \
        --namespace="$ns" \
        --dry-run=client -o yaml | kubectl apply -f -; then
        log_success "Secret 已创建/更新: $ns/$SECRET_NAME"
        ((SUCCESS_COUNT++)) || true
    else
        log_error "Secret 创建失败: $ns/$SECRET_NAME"
        ((FAIL_COUNT++)) || true
    fi
done

echo ""
log_info "=== 操作结果 ==="
log_info "成功: $SUCCESS_COUNT 个命名空间"
if [ "$FAIL_COUNT" -gt 0 ]; then
    log_warning "失败: $FAIL_COUNT 个命名空间"
    exit 1
else
    log_success "所有操作成功完成"
fi
