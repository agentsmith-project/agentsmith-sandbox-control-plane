#!/bin/bash
set -euo pipefail

# 测试版本更新流程的脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
PROJECT_ROOT="$(cd "${K8S_DIR}/.." && pwd)"

# Source utility libraries
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

echo "=== 测试版本更新流程 ==="
echo ""

# 1. 检查 VERSION 文件
log_info "[1/3] 检查 VERSION 文件..."
ASBCP_VERSION=$(cat "${PROJECT_ROOT}/VERSION" 2>/dev/null || echo "")

if [ -z "$ASBCP_VERSION" ]; then
    log_error "ASBCP VERSION 文件缺失"
    exit 1
fi

log_success "版本文件检查通过: ASBCP=$ASBCP_VERSION"

# 2. 检查 kustomization.yaml 中的镜像版本
log_info "[2/3] 检查 kustomization.yaml 镜像版本..."
for overlay in dev staging production; do
    overlay_dir="${K8S_DIR}/overlays/${overlay}"
    if [ -d "$overlay_dir" ]; then
        kustomization_file="${overlay_dir}/kustomization.yaml"
        if [ -f "$kustomization_file" ]; then
            # Extract versions from kustomization.yaml
            asbcp_tag=$(grep -A 2 "agentsmith-sandbox-control-plane" "$kustomization_file" | grep "newTag" | awk '{print $2}' | tr -d '"' || echo "")
            
            if [ "$asbcp_tag" = "$ASBCP_VERSION" ]; then
                log_success "Overlay $overlay: 版本一致"
            else
                log_warning "Overlay $overlay: 版本不一致 (ASBCP:$asbcp_tag vs $ASBCP_VERSION)"
            fi
        fi
    fi
done

# 3. 检查 ASBCP Deployment 配置
log_info "[3/3] 检查 ASBCP Deployment 配置..."
if kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system &>/dev/null; then
    afscp_source=$(kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="ASBCP_AFSCP_INTERNAL_BASE_URL")].valueFrom.configMapKeyRef.key}' 2>/dev/null || echo "")
    if [ "$afscp_source" = "afscp-internal-base-url" ]; then
        log_success "ASBCP 从 ConfigMap 读取 AFSCP endpoint"
    else
        log_warning "ASBCP 可能未从 ConfigMap 读取 AFSCP endpoint"
    fi
else
    log_warning "ASBCP Deployment 不存在"
fi

echo ""
log_success "=== 测试完成 ==="
