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
IMAGE_NAME="${IMAGE_NAME:-sandbox-runner}"
VERSION="${VERSION:-$(cat "${IMAGE_DIR}/VERSION" 2>/dev/null || echo "1.0.0")}"
FULL_IMAGE="${REGISTRY}/${IMAGE_NAME}:${VERSION}"

echo "[${IMAGE_NAME}] Verifying image: ${FULL_IMAGE}"

# 检查镜像是否存在
if ! docker images "$FULL_IMAGE" | grep -q "$IMAGE_NAME"; then
    echo "  ✗ 镜像不存在: $FULL_IMAGE"
    exit 1
fi
echo "  ✓ 镜像存在"

# 验证 Node.js 版本
echo "  [验证] Node.js 版本..."
NODE_VERSION=$(docker run --rm "$FULL_IMAGE" node --version 2>&1 | head -1)
if [[ "$NODE_VERSION" == *"v20"* ]]; then
    echo "  ✓ Node.js 版本正确: $NODE_VERSION"
else
    echo "  ✗ Node.js 版本可能不正确: $NODE_VERSION"
    exit 1
fi

# 验证 Python 版本
echo "  [验证] Python 版本..."
PYTHON_VERSION=$(docker run --rm "$FULL_IMAGE" python3 --version 2>&1 | head -1)
echo "  ✓ Python: $PYTHON_VERSION"

# 验证中文字体
echo "  [验证] 中文字体..."
FONT_COUNT=$(docker run --rm "$FULL_IMAGE" fc-list | grep -i "noto\|wqy" | wc -l)
if [ "$FONT_COUNT" -gt 0 ]; then
    echo "  ✓ 中文字体已安装 ($FONT_COUNT 个)"
else
    echo "  ✗ 中文字体未找到"
    exit 1
fi

# 显示镜像信息
echo ""
echo "  [镜像信息]"
docker images "$FULL_IMAGE" --format "    ID: {{.ID}}\n    大小: {{.Size}}\n    创建时间: {{.CreatedAt}}"

echo ""
echo "[${IMAGE_NAME}] ✓ Verification completed"
