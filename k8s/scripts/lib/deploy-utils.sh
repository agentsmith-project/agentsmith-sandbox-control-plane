#!/bin/bash
# Deployment utility functions

# Source common functions
# shellcheck source=lib/common.sh
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Update image versions in Kustomize
update_image_versions() {
    local registry="${1:-localhost:5001}"
    local harbor_project="${2:-}"
    local k8s_dir="${3:-$(cd "${SCRIPT_DIR}/../.." && pwd)}"
    
    log_info "更新镜像版本..."
    log_info "Registry: $registry"
    
    cd "$k8s_dir" || {
        log_error "无法进入目录: $k8s_dir"
        return 1
    }
    
    export REGISTRY="$registry"
    export HARBOR_PROJECT="$harbor_project"
    
    if ! ./scripts/update-images.sh; then
        log_error "镜像版本更新失败"
        return 1
    fi
    
    log_success "镜像版本已更新"
    return 0
}

# Deploy to environment
deploy_to_environment() {
    local environment="${1:-dev}"
    local k8s_dir="${2:-$(cd "${SCRIPT_DIR}/../.." && pwd)}"
    
    log_info "部署到环境: $environment"
    
    cd "$k8s_dir" || {
        log_error "无法进入目录: $k8s_dir"
        return 1
    }
    
    if ! ./scripts/deploy.sh "$environment"; then
        log_error "部署失败"
        return 1
    fi
    
    log_success "部署成功"
    return 0
}

# Clean up old pods
cleanup_old_pods() {
    local namespace="${1:-sandbox}"
    local label_selector="${2:-}"
    
    log_info "清理旧的 pods (namespace: $namespace)"
    
    local delete_cmd="kubectl delete pods -n $namespace"
    if [ -n "$label_selector" ]; then
        delete_cmd="$delete_cmd -l $label_selector"
    else
        delete_cmd="$delete_cmd --all"
    fi
    
    $delete_cmd --force --grace-period=0 2>&1 | grep -v "Warning" || {
        log_info "没有 pod 需要删除"
    }
    
    log_success "清理完成"
}

# Clean up old images in kind cluster
cleanup_kind_images() {
    local cluster_name="${1:-sandbox-cluster}"
    local registry="${2:-localhost:5001}"
    local image_pattern="${3:-sandbox-runner}"
    local old_tags="${4:-}"
    
    log_info "清理 Kind 集群中的旧镜像: $image_pattern"
    
    if [ -n "$old_tags" ]; then
        for tag in $old_tags; do
            docker exec "${cluster_name}-control-plane" crictl rmi \
                "${registry}/${image_pattern}:${tag}" \
                2>&1 | grep -v "no such image" || true
        done
    fi
    
    log_success "镜像清理完成"
}
