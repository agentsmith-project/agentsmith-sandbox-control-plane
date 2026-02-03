#!/bin/bash
set -euo pipefail

# K8s 部署脚本（使用 Kustomize）

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Source utility libraries
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

# Load configuration
CONFIG_FILE="${CONFIG_FILE:-${K8S_DIR}/config/deploy.env}"
load_config "$CONFIG_FILE" || log_warning "配置文件不存在，使用默认值: $CONFIG_FILE"

ENVIRONMENT="${1:-${ENVIRONMENT:-dev}}"  # dev, staging, production
DRY_RUN="${DRY_RUN:-false}"
OVERLAY_DIR="${K8S_DIR}/overlays/${ENVIRONMENT}"

# Validate overlay
if ! validate_overlay "$OVERLAY_DIR"; then
    log_error "可用环境: dev, staging, production"
    exit 1
fi

# Check kustomize
KUSTOMIZE_CMD=$(check_kustomize)
if [ -z "$KUSTOMIZE_CMD" ]; then
    log_error "未找到 kustomize 命令"
    log_error "请安装 kustomize 或使用支持 kustomize 的 kubectl 版本"
    exit 1
fi

log_info "=== 部署到环境: $ENVIRONMENT ==="
log_info "Overlay 目录: $OVERLAY_DIR"
echo ""

# Dry-run 模式
if [ "$DRY_RUN" = "true" ]; then
    log_info "Dry-run 模式：预览变更"
    $KUSTOMIZE_CMD "$OVERLAY_DIR" | kubectl apply --dry-run=client -f -
    echo ""
    log_success "Dry-run 完成（未实际应用）"
    exit 0
fi

# Check K8s connection
log_info "检查 K8s 集群连接..."
if ! check_k8s_connection; then
    log_error "请检查:"
    log_error "  1. kubectl 配置是否正确"
    log_error "  2. 集群是否可访问"
    exit 1
fi
log_success "已连接到集群"

# 使用 Kustomize 构建并应用
echo ""
echo "应用 Kustomize 配置..."
kubectl apply -k "$OVERLAY_DIR"

# 等待 Manager 就绪
echo ""
echo "等待 Manager Deployment 就绪..."
kubectl wait --for=condition=available \
    --timeout=120s \
    deployment/sandbox-manager -n sandbox-system || {
    echo "警告: Manager Deployment 未在 120 秒内就绪"
    echo "检查状态: kubectl get pods -n sandbox-system"
    exit 1
}

echo ""
echo "=== 部署完成 ==="
echo ""
echo "检查部署状态:"
kubectl get pods -n sandbox-system
kubectl get pods -n sandbox

echo ""
echo "访问 Manager:"
echo "  本地开发: kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80"
echo "  生产环境: 查看 Ingress/NodePort/LoadBalancer 配置"
