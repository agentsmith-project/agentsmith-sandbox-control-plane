#!/bin/bash
set -euo pipefail

# 批量更新镜像引用脚本
# 使用统一的工具库和最佳实践

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
# Calculate project root: k8s/scripts -> k8s -> sandbox
PROJECT_ROOT="$(cd "${K8S_DIR}/.." && pwd)"

# Source utility libraries
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"
# shellcheck source=lib/kustomize-utils.sh
source "${SCRIPT_DIR}/lib/kustomize-utils.sh"

# Caller-provided REGISTRY (e.g. from sbx dev up) wins over config file
REGISTRY_FROM_CALLER="${REGISTRY:-}"

# Load configuration
CONFIG_FILE="${CONFIG_FILE:-${K8S_DIR}/config/deploy.env}"
if ! load_config "$CONFIG_FILE"; then
    log_warning "配置文件不存在，使用默认值: $CONFIG_FILE"
fi

# Restore caller REGISTRY so dev up with local build uses short names in dev overlay
[ -n "$REGISTRY_FROM_CALLER" ] && REGISTRY="$REGISTRY_FROM_CALLER"
REGISTRY="${REGISTRY:-localhost:5001}"
HARBOR_PROJECT="${HARBOR_PROJECT:-agentsmith}"

# Build full registry path
FULL_REGISTRY=$(build_registry_path "$REGISTRY" "$HARBOR_PROJECT")

log_info "=== 批量更新镜像版本 ==="
log_info "Registry: $FULL_REGISTRY"
echo ""

# Read versions from VERSION files
MANAGER_VERSION=$(read_version "${PROJECT_ROOT}/manager-service/VERSION")
RUNNER_VERSION=$(read_version "${PROJECT_ROOT}/images/runner/VERSION")

# Use defaults if versions are empty
MANAGER_VERSION="${MANAGER_VERSION:-1.0.0}"
RUNNER_VERSION="${RUNNER_VERSION:-1.0.0}"

log_info "镜像版本:"
echo "  Manager: $MANAGER_VERSION"
echo "  Runner: $RUNNER_VERSION"
echo ""

# Build full image paths
MANAGER_IMAGE="${FULL_REGISTRY}/sandbox-manager:${MANAGER_VERSION}"
RUNNER_IMAGE="${FULL_REGISTRY}/sandbox-runner:${RUNNER_VERSION}"

# For dev overlay with local registry, use short image names so Kind uses locally loaded images
use_short_names_for_dev() {
    [[ "$REGISTRY" == "localhost:"* ]] || [[ "$REGISTRY" == "127.0.0.1:"* ]]
}

# Update all overlays
SUCCESS_COUNT=0
FAIL_COUNT=0

for overlay in dev staging production; do
    overlay_dir="${K8S_DIR}/overlays/${overlay}"
    
    if [ ! -d "$overlay_dir" ]; then
        log_warning "Overlay 不存在，跳过: $overlay"
        continue
    fi
    
    log_info "更新 overlay: $overlay"
    
    manager_img="$MANAGER_IMAGE"
    runner_img="$RUNNER_IMAGE"
    if [ "$overlay" = "dev" ] && use_short_names_for_dev; then
        manager_img="sandbox-manager:${MANAGER_VERSION}"
        runner_img="sandbox-runner:${RUNNER_VERSION}"
    fi
    
    # Update kustomization.yaml images
    if update_kustomization_images "$overlay_dir" "$manager_img" "$runner_img"; then
        log_success "Kustomization 镜像已更新"
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        log_error "Kustomization 镜像更新失败"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        continue
    fi
done

echo ""
if [ $FAIL_COUNT -eq 0 ]; then
    log_success "=== 更新完成 ==="
    log_success "成功更新 $SUCCESS_COUNT 个 overlay"
    exit 0
else
    log_error "=== 更新部分失败 ==="
    log_error "成功: $SUCCESS_COUNT, 失败: $FAIL_COUNT"
    exit 1
fi
