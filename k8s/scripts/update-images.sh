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

# Load configuration
CONFIG_FILE="${CONFIG_FILE:-${K8S_DIR}/config/deploy.env}"
if ! load_config "$CONFIG_FILE"; then
    log_warning "配置文件不存在，使用默认值: $CONFIG_FILE"
fi

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
GC_VERSION=$(read_version "${PROJECT_ROOT}/images/gc/VERSION")

# Use defaults if versions are empty
MANAGER_VERSION="${MANAGER_VERSION:-1.0.0}"
RUNNER_VERSION="${RUNNER_VERSION:-1.0.0}"
GC_VERSION="${GC_VERSION:-1.0.0}"

log_info "镜像版本:"
echo "  Manager: $MANAGER_VERSION"
echo "  Runner: $RUNNER_VERSION"
echo "  GC: $GC_VERSION"
echo ""

# Build full image paths
MANAGER_IMAGE="${FULL_REGISTRY}/sandbox-manager:${MANAGER_VERSION}"
RUNNER_IMAGE="${FULL_REGISTRY}/sandbox-runner:${RUNNER_VERSION}"
GC_IMAGE="${FULL_REGISTRY}/sandbox-gc:${GC_VERSION}"

# Function to update ConfigMap patch for runner image
update_configmap_runner_image() {
    local overlay_dir="$1"
    local runner_image="$2"
    local patch_file="${overlay_dir}/patches/configmap-runner-image.yaml"
    
    if [ ! -f "$patch_file" ]; then
        log_warning "ConfigMap patch 不存在，创建: $patch_file"
        mkdir -p "${overlay_dir}/patches"
        cat > "$patch_file" <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: sandbox-config
  namespace: sandbox-system
data:
  runner-image-default: "${runner_image}"
EOF
    else
        # Update existing patch file
        if command -v python3 &> /dev/null; then
            python3 <<EOF
import yaml
import sys

with open('${patch_file}', 'r') as f:
    data = yaml.safe_load(f)

if 'data' not in data:
    data['data'] = {}

data['data']['runner-image-default'] = '${runner_image}'

with open('${patch_file}', 'w') as f:
    yaml.dump(data, f, default_flow_style=False, sort_keys=False, allow_unicode=True)
EOF
        else
            # Fallback to sed
            sed -i "s|runner-image-default:.*|runner-image-default: \"${runner_image}\"|g" "$patch_file"
        fi
    fi
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
    
    # Update kustomization.yaml images
    if update_kustomization_images "$overlay_dir" "$MANAGER_IMAGE" "$RUNNER_IMAGE" "$GC_IMAGE"; then
        log_success "Kustomization 镜像已更新"
    else
        log_error "Kustomization 镜像更新失败"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        continue
    fi
    
    # Update ConfigMap patch for runner image
    if update_configmap_runner_image "$overlay_dir" "$RUNNER_IMAGE"; then
        log_success "ConfigMap runner 镜像已更新"
        
        # Ensure patch is included in kustomization.yaml
        if ! grep -q "configmap-runner-image.yaml" "${overlay_dir}/kustomization.yaml" 2>/dev/null; then
            log_warning "添加 configmap-runner-image.yaml 到 kustomization.yaml"
            if command -v python3 &> /dev/null; then
                python3 <<EOF
import yaml

with open('${overlay_dir}/kustomization.yaml', 'r') as f:
    data = yaml.safe_load(f)

if 'patches' not in data:
    data['patches'] = []

patch_path = 'patches/configmap-runner-image.yaml'
if patch_path not in data['patches']:
    # Insert after configmap-ttl.yaml if exists
    if 'patches/configmap-ttl.yaml' in data['patches']:
        idx = data['patches'].index('patches/configmap-ttl.yaml')
        data['patches'].insert(idx + 1, patch_path)
    else:
        data['patches'].append(patch_path)

with open('${overlay_dir}/kustomization.yaml', 'w') as f:
    yaml.dump(data, f, default_flow_style=False, sort_keys=False, allow_unicode=True)
EOF
            fi
        fi
        
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
        log_error "ConfigMap runner 镜像更新失败"
        FAIL_COUNT=$((FAIL_COUNT + 1))
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
