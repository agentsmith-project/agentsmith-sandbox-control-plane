#!/bin/bash
set -euo pipefail

# 推送镜像到 registry

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${IMAGE_DIR}/config.env}"

if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

REGISTRY="${REGISTRY:-localhost:5001}"
IMAGE_NAME="${IMAGE_NAME:-sandbox-manager}"
VERSION="${VERSION:-$(cat "${IMAGE_DIR}/VERSION" 2>/dev/null || echo "1.0.0")}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "[${IMAGE_NAME}] Pushing image: ${FULL_IMAGE}"

# 检查镜像是否存在
if ! docker images "$FULL_IMAGE" | grep -q "$IMAGE_NAME"; then
    echo "  ✗ 镜像不存在: $FULL_IMAGE"
    echo "  提示: 请先运行 build.sh 构建镜像"
    exit 1
fi

# 登录（如果需要）
if [ -n "${REGISTRY_USERNAME:-}" ] && [ -n "${REGISTRY_PASSWORD:-}" ]; then
    echo "[${IMAGE_NAME}] Logging in to registry..."
    echo "$REGISTRY_PASSWORD" | docker login "$REGISTRY" -u "$REGISTRY_USERNAME" --password-stdin
fi

# 推送
docker push "$FULL_IMAGE"

echo "[${IMAGE_NAME}] ✓ Pushed successfully"
