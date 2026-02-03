#!/bin/bash
set -euo pipefail

# Runner 镜像构建脚本
# 功能：构建 Docker 镜像，支持缓存、自动加载到 kind、自动推送

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${IMAGE_DIR}/config.env}"

# 加载配置（但保留已设置的环境变量）
SAVED_HTTP_PROXY="${HTTP_PROXY:-}"
SAVED_HTTPS_PROXY="${HTTPS_PROXY:-}"
SAVED_http_proxy="${http_proxy:-}"
SAVED_https_proxy="${https_proxy:-}"
SAVED_NO_PROXY="${NO_PROXY:-}"
SAVED_no_proxy="${no_proxy:-}"

if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

# 恢复环境变量（如果已设置）
if [ -n "$SAVED_HTTP_PROXY" ]; then
    HTTP_PROXY="$SAVED_HTTP_PROXY"
fi
if [ -n "$SAVED_HTTPS_PROXY" ]; then
    HTTPS_PROXY="$SAVED_HTTPS_PROXY"
fi
if [ -n "$SAVED_http_proxy" ]; then
    http_proxy="$SAVED_http_proxy"
fi
if [ -n "$SAVED_https_proxy" ]; then
    https_proxy="$SAVED_https_proxy"
fi
if [ -n "$SAVED_NO_PROXY" ]; then
    NO_PROXY="$SAVED_NO_PROXY"
fi
if [ -n "$SAVED_no_proxy" ]; then
    no_proxy="$SAVED_no_proxy"
fi

# 默认值
REGISTRY="${REGISTRY:-localhost:5001}"
IMAGE_NAME="${IMAGE_NAME:-sandbox-runner}"
VERSION="${VERSION:-$(cat "${IMAGE_DIR}/VERSION" 2>/dev/null || echo "1.0.0")}"
BUILDX_BUILDER="${BUILDX_BUILDER:-sandbox-builder}"
CACHE_DIR="${CACHE_DIR:-/tmp/.buildx-cache-${IMAGE_NAME}}"
PLATFORM="${PLATFORM:-linux/amd64}"

# 参数解析
while [[ $# -gt 0 ]]; do
    case $1 in
        -r|--registry)
            REGISTRY="$2"
            shift 2
            ;;
        -t|--tag)
            VERSION="$2"
            shift 2
            ;;
        -c|--config)
            CONFIG_FILE="$2"
            source "$CONFIG_FILE"
            shift 2
            ;;
        -l|--load-to-kind)
            LOAD_TO_KIND=true
            shift
            ;;
        -p|--push)
            PUSH=true
            shift
            ;;
        -h|--help)
            cat <<EOF
Usage: $0 [OPTIONS]

Build Docker image for ${IMAGE_NAME}

Options:
  -r, --registry REGISTRY    Image registry (default: ${REGISTRY})
  -t, --tag TAG              Image tag/version (default: ${VERSION})
  -c, --config FILE          Config file path
  -l, --load-to-kind         Auto-load to kind cluster
  -p, --push                 Auto-push to registry
  -h, --help                 Show this help

Examples:
  $0                          # Build with defaults
  $0 -t 1.2.0 -l             # Build tag 1.2.0 and load to kind
  $0 -r my-registry.com -p   # Build and push to registry
EOF
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# 构建逻辑
echo "[${IMAGE_NAME}] Building image: ${REGISTRY}/${IMAGE_NAME}:${VERSION}"

# 调试：打印环境变量
echo "[${IMAGE_NAME}] DEBUG: HTTP_PROXY='${HTTP_PROXY:-}' HTTPS_PROXY='${HTTPS_PROXY:-}'"

# 检查是否使用代理，如果使用代理则使用 docker 驱动
USING_PROXY=false
if [ -n "${HTTP_PROXY:-}" ] || [ -n "${HTTPS_PROXY:-}" ]; then
    echo "[${IMAGE_NAME}] Proxy detected, using docker driver for BuildKit"
    # 使用默认 docker driver (继承 daemon 代理设置)
    docker buildx use default 2>/dev/null || true
    USING_PROXY=true
else
    echo "[${IMAGE_NAME}] No proxy detected, using docker-container driver"
    # 确保 buildx builder 存在
    if ! docker buildx inspect "$BUILDX_BUILDER" &>/dev/null; then
        echo "[${IMAGE_NAME}] Creating buildx builder: $BUILDX_BUILDER"
        docker buildx create --name "$BUILDX_BUILDER" --driver docker-container --use
        docker buildx inspect --bootstrap
    else
        docker buildx use "$BUILDX_BUILDER"
    fi
fi

# 构建镜像
echo "[${IMAGE_NAME}] Starting build..."
BUILD_CMD="docker buildx build \
    --platform $PLATFORM \
    --tag ${REGISTRY}/${IMAGE_NAME}:${VERSION} \
    --load"

# 仅在非代理模式下添加缓存（docker 驱动不支持缓存导出）
if [ "$USING_PROXY" = "false" ]; then
    BUILD_CMD="$BUILD_CMD \
    --cache-from type=local,src=$CACHE_DIR \
    --cache-to type=local,dest=$CACHE_DIR,mode=max"
fi

# 添加代理参数（用于 Dockerfile 内部使用）
BUILD_CMD="$BUILD_CMD \
    ${HTTP_PROXY:+--build-arg HTTP_PROXY=$HTTP_PROXY} \
    ${HTTPS_PROXY:+--build-arg HTTPS_PROXY=$HTTPS_PROXY} \
    ${http_proxy:+--build-arg http_proxy=$http_proxy} \
    ${https_proxy:+--build-arg https_proxy=$https_proxy} \
    --progress=plain \
    ${IMAGE_DIR}"

eval $BUILD_CMD

echo "[${IMAGE_NAME}] Build completed: ${REGISTRY}/${IMAGE_NAME}:${VERSION}"

# 加载到 kind（如果指定）
if [ "${LOAD_TO_KIND:-false}" = "true" ]; then
    KIND_CLUSTER="${KIND_CLUSTER:-sandbox-cluster}"
    echo "[${IMAGE_NAME}] Loading to kind cluster: $KIND_CLUSTER"
    kind load docker-image "${REGISTRY}/${IMAGE_NAME}:${VERSION}" --name "$KIND_CLUSTER"
    echo "[${IMAGE_NAME}] ✓ Loaded to kind"
fi

# 推送到 registry（如果指定）
if [ "${PUSH:-false}" = "true" ]; then
    echo "[${IMAGE_NAME}] Pushing to registry: ${REGISTRY}"
    if [ -n "${REGISTRY_USERNAME:-}" ] && [ -n "${REGISTRY_PASSWORD:-}" ]; then
        echo "$REGISTRY_PASSWORD" | docker login "$REGISTRY" -u "$REGISTRY_USERNAME" --password-stdin
    fi
    docker push "${REGISTRY}/${IMAGE_NAME}:${VERSION}"
    echo "[${IMAGE_NAME}] ✓ Pushed to registry"
fi

echo "[${IMAGE_NAME}] ✓ Build process completed"
