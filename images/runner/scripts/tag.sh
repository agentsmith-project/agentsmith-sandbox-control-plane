#!/bin/bash
set -euo pipefail

# 标签管理脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${IMAGE_DIR}/config.env}"

if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

REGISTRY="${REGISTRY:-localhost:5001}"
IMAGE_NAME="${IMAGE_NAME:-sandbox-runner}"
OLD_VERSION="${OLD_VERSION:-$(cat "${IMAGE_DIR}/VERSION" 2>/dev/null || echo "1.0.0")}"
NEW_VERSION="${1:-}"

if [ -z "$NEW_VERSION" ]; then
    echo "Usage: $0 <new-version>"
    echo "Current version: $OLD_VERSION"
    exit 1
fi

FULL_IMAGE_OLD="${REGISTRY}/${IMAGE_NAME}:${OLD_VERSION}"
FULL_IMAGE_NEW="${REGISTRY}/${IMAGE_NAME}:${NEW_VERSION}"

echo "[${IMAGE_NAME}] Tagging: ${FULL_IMAGE_OLD} -> ${FULL_IMAGE_NEW}"

# 检查源镜像是否存在
if ! docker images "$FULL_IMAGE_OLD" | grep -q "$IMAGE_NAME"; then
    echo "  ✗ 源镜像不存在: $FULL_IMAGE_OLD"
    exit 1
fi

# 创建新标签
docker tag "$FULL_IMAGE_OLD" "$FULL_IMAGE_NEW"

# 更新 VERSION 文件
echo "$NEW_VERSION" > "${IMAGE_DIR}/VERSION"

echo "[${IMAGE_NAME}] ✓ Tagged successfully"
echo "[${IMAGE_NAME}] VERSION file updated to: $NEW_VERSION"
