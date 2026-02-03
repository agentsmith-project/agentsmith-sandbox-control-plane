#!/bin/bash
set -euo pipefail

# K8s 卸载脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

ENVIRONMENT="${1:-dev}"
OVERLAY_DIR="${K8S_DIR}/overlays/${ENVIRONMENT}"

if [ ! -d "$OVERLAY_DIR" ]; then
    echo "错误: Overlay '$ENVIRONMENT' 不存在"
    exit 1
fi

echo "=== 卸载环境: $ENVIRONMENT ==="
echo ""

# 确认
read -p "确认要删除所有资源吗? (yes/no): " confirm
if [ "$confirm" != "yes" ]; then
    echo "已取消"
    exit 0
fi

# 备份当前配置（可选）
BACKUP_DIR="/tmp/sandbox-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP_DIR"
echo "备份当前配置到: $BACKUP_DIR"
kubectl get all -n sandbox-system -o yaml > "${BACKUP_DIR}/sandbox-system.yaml" 2>/dev/null || true
kubectl get all -n sandbox -o yaml > "${BACKUP_DIR}/sandbox.yaml" 2>/dev/null || true

# 删除资源
echo ""
echo "删除资源..."
kubectl delete -k "$OVERLAY_DIR" --ignore-not-found=true

echo ""
echo "=== 卸载完成 ==="
echo "备份位置: $BACKUP_DIR"
