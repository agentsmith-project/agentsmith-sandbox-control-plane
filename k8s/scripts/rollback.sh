#!/bin/bash
set -euo pipefail

#=============================================================================
# Rollback Script - Rollback deployment to a previous version
#
# Usage: ./rollback.sh [environment] [version]
#   environment: dev, staging, production (default: production)
#   version: Target version to rollback to (prompts if not provided)
#=============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Source utility libraries
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

# Parse arguments
ENVIRONMENT="${1:-production}"
VERSION="${2:-}"

log_info "=== 回滚部署 ==="
log_info "环境: $ENVIRONMENT"
echo ""

# Validate environment
VALID_ENVIRONMENTS=("dev" "staging" "production")
if [[ ! " ${VALID_ENVIRONMENTS[*]} " =~ " ${ENVIRONMENT} " ]]; then
    log_error "无效环境: $ENVIRONMENT"
    log_error "可用环境: ${VALID_ENVIRONMENTS[*]}"
    exit 1
fi

# Check kubectl connection
if ! check_k8s_connection; then
    log_error "无法连接到 Kubernetes 集群"
    log_error "请检查 kubectl 配置"
    exit 1
fi

# Check if namespace exists
if ! kubectl get namespace sandbox-system &>/dev/null; then
    log_error "命名空间 sandbox-system 不存在"
    exit 1
fi

# Get version if not provided
if [ -z "$VERSION" ]; then
    log_info "可用版本（从 ConfigMap 获取）:"
    if ! VERSIONS=$(kubectl get configmap -n sandbox-system -l app.kubernetes.io/name=sandbox-version -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null); then
        log_warning "无法获取版本列表"
        VERSIONS=""
    fi

    if [ -n "$VERSIONS" ]; then
        echo "$VERSIONS" | while read -r ver; do
            [ -n "$ver" ] && echo "  - $ver"
        done
    else
        echo "  (无版本记录)"
    fi
    echo ""

    read -rp "请输入要回滚的版本: " VERSION
fi

# Validate version
if [ -z "$VERSION" ]; then
    log_error "未指定版本"
    exit 1
fi

# Validate version format (basic check)
if [[ ! "$VERSION" =~ ^[a-zA-Z0-9._-]+$ ]]; then
    log_error "无效的版本格式: $VERSION"
    log_error "版本只能包含字母、数字、点、下划线和连字符"
    exit 1
fi

log_info "回滚到版本: $VERSION"
echo ""

log_warning "注意: 此脚本需要手动更新 Kustomize overlays 中的镜像版本"
log_info "然后运行: kubectl apply -k overlays/${ENVIRONMENT}"
echo ""
log_info "或者使用 k8s/scripts/update-images.sh 脚本自动更新"
echo ""

# Confirm rollback
read -rp "确认回滚到版本 $VERSION? (y/N): " CONFIRM
if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    log_info "回滚已取消"
    exit 0
fi

log_info "回滚已确认，请手动执行以下命令:"
echo "  1. 更新镜像版本: ./k8s/scripts/update-images.sh $ENVIRONMENT $VERSION"
echo "  2. 应用配置: kubectl apply -k k8s/overlays/${ENVIRONMENT}"
