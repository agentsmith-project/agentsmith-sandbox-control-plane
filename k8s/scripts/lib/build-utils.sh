#!/bin/bash
# Build utility functions

# Source common functions
# shellcheck source=lib/common.sh
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Setup buildx builder
setup_buildx_builder() {
    local builder_name="${1:-sandbox-builder}"
    
    if ! docker buildx inspect "$builder_name" &>/dev/null; then
        log_info "创建 buildx builder: $builder_name"
        docker buildx create --name "$builder_name" --driver docker-container --use
        docker buildx inspect --bootstrap
    else
        log_info "使用现有 buildx builder: $builder_name"
        docker buildx use "$builder_name"
    fi
}

# Build image with buildx
build_image_with_buildx() {
    local image_dir="$1"
    local registry="$2"
    local image_name="$3"
    local version="$4"
    local cache_name="${5:-${image_name}}"
    
    local full_image="${registry}/${image_name}:${version}"
    local cache_dir="/tmp/.buildx-cache-${cache_name}"
    
    log_info "构建镜像: $full_image"
    log_info "镜像目录: $image_dir"
    
    cd "$image_dir" || {
        log_error "无法进入目录: $image_dir"
        return 1
    }
    
    # 设置代理（如果提供）
    local http_proxy="${HTTP_PROXY:-}"
    local https_proxy="${HTTPS_PROXY:-}"
    
    docker buildx build \
        --platform linux/amd64 \
        --tag "$full_image" \
        --load \
        --cache-from type=local,src="$cache_dir" \
        --cache-to type=local,dest="$cache_dir",mode=max \
        ${http_proxy:+--build-arg HTTP_PROXY="$http_proxy"} \
        ${https_proxy:+--build-arg HTTPS_PROXY="$https_proxy"} \
        ${http_proxy:+--build-arg http_proxy="$http_proxy"} \
        ${https_proxy:+--build-arg https_proxy="$https_proxy"} \
        --progress=plain \
        . || {
        log_error "镜像构建失败: $full_image"
        return 1
    }
    
    log_success "镜像构建成功: $full_image"
    return 0
}

# Load image to kind cluster
load_image_to_kind() {
    local image="$1"
    local cluster_name="${2:-sandbox-cluster}"
    
    log_info "加载镜像到 Kind 集群: $image"
    kind load docker-image "$image" --name "$cluster_name" || {
        log_error "镜像加载失败: $image"
        return 1
    }
    
    log_success "镜像已加载到集群: $image"
    return 0
}

# Build and load image to kind
build_and_load_image() {
    local image_dir="$1"
    local registry="$2"
    local image_name="$3"
    local version="$4"
    local cluster_name="${5:-sandbox-cluster}"
    local cache_name="${6:-${image_name}}"
    
    local full_image="${registry}/${image_name}:${version}"
    
    # Setup builder
    setup_buildx_builder "sandbox-builder" || return 1
    
    # Build image
    build_image_with_buildx "$image_dir" "$registry" "$image_name" "$version" "$cache_name" || return 1
    
    # Load to kind
    load_image_to_kind "$full_image" "$cluster_name" || return 1
    
    return 0
}
