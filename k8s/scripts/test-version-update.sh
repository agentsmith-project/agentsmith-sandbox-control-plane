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
log_info "[1/5] 检查 VERSION 文件..."
MANAGER_VERSION=$(cat "${PROJECT_ROOT}/manager-service/VERSION" 2>/dev/null || echo "")
RUNNER_VERSION=$(cat "${PROJECT_ROOT}/images/runner/VERSION" 2>/dev/null || echo "")
GC_VERSION=$(cat "${PROJECT_ROOT}/images/gc/VERSION" 2>/dev/null || echo "")

if [ -z "$MANAGER_VERSION" ] || [ -z "$RUNNER_VERSION" ] || [ -z "$GC_VERSION" ]; then
    log_error "VERSION 文件缺失或不完整"
    exit 1
fi

log_success "版本文件检查通过: Manager=$MANAGER_VERSION, Runner=$RUNNER_VERSION, GC=$GC_VERSION"

# 2. 检查 kustomization.yaml 中的镜像版本
log_info "[2/5] 检查 kustomization.yaml 镜像版本..."
for overlay in dev staging production; do
    overlay_dir="${K8S_DIR}/overlays/${overlay}"
    if [ -d "$overlay_dir" ]; then
        kustomization_file="${overlay_dir}/kustomization.yaml"
        if [ -f "$kustomization_file" ]; then
            # Extract versions from kustomization.yaml
            manager_tag=$(grep -A 2 "sandbox-manager" "$kustomization_file" | grep "newTag" | awk '{print $2}' | tr -d '"' || echo "")
            runner_tag=$(grep -A 2 "sandbox-runner" "$kustomization_file" | grep "newTag" | awk '{print $2}' | tr -d '"' || echo "")
            gc_tag=$(grep -A 2 "sandbox-gc" "$kustomization_file" | grep "newTag" | awk '{print $2}' | tr -d '"' || echo "")
            
            if [ "$manager_tag" = "$MANAGER_VERSION" ] && [ "$runner_tag" = "$RUNNER_VERSION" ] && [ "$gc_tag" = "$GC_VERSION" ]; then
                log_success "Overlay $overlay: 版本一致"
            else
                log_warning "Overlay $overlay: 版本不一致 (Manager:$manager_tag vs $MANAGER_VERSION, Runner:$runner_tag vs $RUNNER_VERSION, GC:$gc_tag vs $GC_VERSION)"
            fi
        fi
    fi
done

# 3. 检查 ConfigMap patch 中的 runner 镜像
log_info "[3/5] 检查 ConfigMap patch 中的 runner 镜像..."
for overlay in dev staging production; do
    patch_file="${K8S_DIR}/overlays/${overlay}/patches/configmap-runner-image.yaml"
    if [ -f "$patch_file" ]; then
        runner_image=$(grep "runner-image-default:" "$patch_file" | awk '{print $2}' | tr -d '"' || echo "")
        expected_image="harbor.pullot.com:28443/agentsmith/sandbox-runner:${RUNNER_VERSION}"
        if [[ "$runner_image" == *":${RUNNER_VERSION}" ]]; then
            log_success "Overlay $overlay: ConfigMap runner 镜像版本正确"
        else
            log_warning "Overlay $overlay: ConfigMap runner 镜像版本可能不匹配 ($runner_image vs $expected_image)"
        fi
    else
        log_warning "Overlay $overlay: ConfigMap patch 不存在"
    fi
done

# 4. 检查部署的 ConfigMap
log_info "[4/5] 检查部署的 ConfigMap..."
if kubectl get configmap sandbox-config -n sandbox-system &>/dev/null; then
    deployed_runner_image=$(kubectl get configmap sandbox-config -n sandbox-system -o jsonpath='{.data.runner-image-default}' 2>/dev/null || echo "")
    if [ -n "$deployed_runner_image" ]; then
        log_success "ConfigMap 中的 runner 镜像: $deployed_runner_image"
        if [[ "$deployed_runner_image" == *":${RUNNER_VERSION}" ]]; then
            log_success "部署的 runner 镜像版本正确"
        else
            log_warning "部署的 runner 镜像版本可能不匹配"
        fi
    else
        log_warning "ConfigMap 中未找到 runner-image-default"
    fi
else
    log_warning "ConfigMap sandbox-config 不存在"
fi

# 5. 检查 Manager Deployment 配置
log_info "[5/5] 检查 Manager Deployment 配置..."
if kubectl get deployment sandbox-manager -n sandbox-system &>/dev/null; then
    env_source=$(kubectl get deployment sandbox-manager -n sandbox-system -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="RUNNER_IMAGE_DEFAULT")].valueFrom.configMapKeyRef.key}' 2>/dev/null || echo "")
    if [ "$env_source" = "runner-image-default" ]; then
        log_success "Manager 从 ConfigMap 读取 runner 镜像"
    else
        log_warning "Manager 可能未从 ConfigMap 读取 runner 镜像"
    fi
else
    log_warning "Manager Deployment 不存在"
fi

echo ""
log_success "=== 测试完成 ==="
