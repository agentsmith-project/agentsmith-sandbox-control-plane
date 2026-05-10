#!/bin/bash
set -euo pipefail

#=============================================================================
# K8s 部署脚本（使用 Kustomize）
#
# Usage: ./deploy.sh [environment] [--dry-run]
#   environment: dev, staging, production (default: dev)
#
# Environment Variables:
#   DRY_RUN: Set to "true" for dry-run mode
#   CONFIG_FILE: Path to configuration file
#=============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Source utility libraries
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

# Load configuration
CONFIG_FILE="${CONFIG_FILE:-${K8S_DIR}/config/deploy.env}"
if ! load_config "$CONFIG_FILE"; then
    log_warning "配置文件不存在，使用默认值: $CONFIG_FILE"
fi

# Parse arguments
ENVIRONMENT="${1:-${ENVIRONMENT:-dev}}"  # dev, staging, production
DRY_RUN="${DRY_RUN:-false}"
OVERLAY_DIR="${K8S_DIR}/overlays/${ENVIRONMENT}"

# Validate environment
VALID_ENVIRONMENTS=("dev" "staging" "production")
if [[ ! " ${VALID_ENVIRONMENTS[*]} " =~ " ${ENVIRONMENT} " ]]; then
    log_error "无效环境: $ENVIRONMENT"
    log_error "可用环境: ${VALID_ENVIRONMENTS[*]}"
    exit 1
fi

# Validate overlay directory
if ! validate_overlay "$OVERLAY_DIR"; then
    log_error "可用环境: ${VALID_ENVIRONMENTS[*]}"
    exit 1
fi

# Check kustomize availability
KUSTOMIZE_CMD=$(check_kustomize)
if [ -z "$KUSTOMIZE_CMD" ]; then
    log_error "未找到 kustomize 命令"
    log_error "请安装 kustomize 或使用支持 kustomize 的 kubectl 版本"
    exit 1
fi

log_info "=== 部署到环境: $ENVIRONMENT ==="
log_info "Overlay 目录: $OVERLAY_DIR"
echo ""

# Dry-run mode
if [ "$DRY_RUN" = "true" ]; then
    log_info "Dry-run 模式：预览变更"
    if ! $KUSTOMIZE_CMD "$OVERLAY_DIR" | kubectl apply --dry-run=client -f -; then
        log_error "Dry-run 失败，请检查配置"
        exit 1
    fi
    echo ""
    log_success "Dry-run 完成（未实际应用）"
    exit 0
fi

# Check K8s connection
log_info "检查 K8s 集群连接..."
if ! check_k8s_connection; then
    log_error "无法连接到 Kubernetes 集群"
    log_error "请检查:"
    log_error "  1. kubectl 配置是否正确"
    log_error "  2. 集群是否可访问"
    exit 1
fi
log_success "已连接到集群"

# Check current context
CURRENT_CONTEXT=$(kubectl config current-context 2>/dev/null || echo "unknown")
log_info "当前上下文: $CURRENT_CONTEXT"

# Verify namespaces exist or create them
log_info "验证命名空间..."
for ns in sandbox-system sandbox-workloads; do
    if ! kubectl get namespace "$ns" &>/dev/null; then
        log_info "创建命名空间: $ns"
        if ! kubectl create namespace "$ns"; then
            log_error "创建命名空间失败: $ns"
            exit 1
        fi
    fi
done
log_success "命名空间已就绪"

# 使用 Kustomize 构建并应用
echo ""
log_info "应用 Kustomize 配置..."
if ! kubectl apply -k "$OVERLAY_DIR"; then
    log_error "应用配置失败"
    log_error "请检查 kustomize 配置和集群状态"
    exit 1
fi
log_success "配置已应用"

# 等待 Manager 就绪
echo ""
log_info "等待 Manager Deployment 就绪..."
if ! kubectl wait --for=condition=available \
    --timeout=120s \
    deployment/sandbox-manager -n sandbox-system; then
    log_error "Manager Deployment 未在 120 秒内就绪"
    log_error "检查状态: kubectl get pods -n sandbox-system"
    log_error "查看日志: kubectl logs -n sandbox-system -l app=sandbox-manager --tail=50"
    exit 1
fi
log_success "Manager Deployment 已就绪"

echo ""
log_success "=== 部署完成 ==="
echo ""
log_info "检查部署状态:"
kubectl get pods -n sandbox-system
kubectl get pods -n sandbox-workloads

echo ""
log_info "访问 Manager:"
echo "  本地开发: kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80"
echo "  生产环境: 查看 Ingress/NodePort/LoadBalancer 配置"
echo ""
log_info "查看日志: kubectl logs -n sandbox-system -l app=sandbox-manager --tail=50 -f"
