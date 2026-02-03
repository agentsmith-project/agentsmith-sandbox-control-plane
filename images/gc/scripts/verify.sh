#!/bin/bash
set -euo pipefail

# 验证镜像内容

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${IMAGE_DIR}/config.env}"

# 加载配置
if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

REGISTRY="${REGISTRY:-localhost:5001}"
IMAGE_NAME="${IMAGE_NAME:-sandbox-gc}"
VERSION="${VERSION:-$(cat "${IMAGE_DIR}/VERSION" 2>/dev/null || echo "1.0.0")}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "[${IMAGE_NAME}] Verifying image: ${FULL_IMAGE}"

# 检查镜像是否存在
if ! docker images "$FULL_IMAGE" | grep -q "$IMAGE_NAME"; then
    echo "  ✗ 镜像不存在: $FULL_IMAGE"
    exit 1
fi
echo "  ✓ 镜像存在"

# 验证关键依赖（kubectl/jq）和入口脚本存在
echo "  [验证] kubectl 可用..."
KUBECTL_VERSION=$(docker run --rm "$FULL_IMAGE" kubectl version --client --short 2>/dev/null | head -1)
if [ -z "$KUBECTL_VERSION" ]; then
    echo "  ✗ kubectl 不可用"
    exit 1
fi
echo "  ✓ $KUBECTL_VERSION"

echo "  [验证] jq 可用..."
JQ_VERSION=$(docker run --rm "$FULL_IMAGE" jq --version 2>/dev/null | head -1)
if [ -z "$JQ_VERSION" ]; then
    echo "  ✗ jq 不可用"
    exit 1
fi
echo "  ✓ $JQ_VERSION"

echo "  [验证] gc.sh 存在且可执行..."
docker run --rm "$FULL_IMAGE" sh -lc 'test -x /gc.sh' && echo "  ✓ /gc.sh OK" || {
    echo "  ✗ /gc.sh 不存在或不可执行"
    exit 1
}

# 显示镜像信息
echo ""
echo "  [镜像信息]"
docker images "$FULL_IMAGE" --format "    ID: {{.ID}}\n    大小: {{.Size}}\n    创建时间: {{.CreatedAt}}"

echo ""
echo "[${IMAGE_NAME}] ✓ Verification completed"
