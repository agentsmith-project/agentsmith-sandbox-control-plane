#!/bin/bash
set -euo pipefail

# 验证镜像内容

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
ROOT_DIR="$(cd "${IMAGE_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${IMAGE_DIR}/config.env}"

# 加载配置
if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

REGISTRY="${REGISTRY:-localhost:5001}"
IMAGE_NAME="${IMAGE_NAME:-agentsmith-sandbox-control-plane}"
VERSION="${VERSION:-$(cat "${ROOT_DIR}/VERSION" 2>/dev/null || echo "dev")}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "[${IMAGE_NAME}] Verifying image: ${FULL_IMAGE}"

# 检查镜像是否存在
if ! docker images "$FULL_IMAGE" | grep -q "$IMAGE_NAME"; then
    echo "  ✗ 镜像不存在: $FULL_IMAGE"
    exit 1
fi
echo "  ✓ 镜像存在"

# 验证容器用户为非 root 且二进制存在
echo "  [验证] 容器运行用户..."
USER_ID=$(docker run --rm "$FULL_IMAGE" id -u 2>/dev/null | tr -d '\r\n')
if [ -z "$USER_ID" ]; then
    echo "  ✗ 无法获取容器用户 ID"
    exit 1
fi
if [ "$USER_ID" = "0" ]; then
    echo "  ✗ 容器以 root 运行（不符合最佳实践）"
    exit 1
fi
echo "  ✓ 非 root 用户: $USER_ID"

echo "  [验证] asbcp 二进制存在且可执行..."
docker run --rm "$FULL_IMAGE" sh -lc 'test -x /app/asbcp' && echo "  ✓ /app/asbcp OK" || {
    echo "  ✗ /app/asbcp 不存在或不可执行"
    exit 1
}

# 显示镜像信息
echo ""
echo "  [镜像信息]"
docker images "$FULL_IMAGE" --format "    ID: {{.ID}}\n    大小: {{.Size}}\n    创建时间: {{.CreatedAt}}"

echo ""
echo "[${IMAGE_NAME}] ✓ Verification completed"
