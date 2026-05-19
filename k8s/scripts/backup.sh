#!/bin/bash
set -euo pipefail

# 备份配置脚本

BACKUP_DIR="${BACKUP_DIR:-/tmp/sandbox-backup-$(date +%Y%m%d-%H%M%S)}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "=== 备份 Sandbox 配置 ==="
echo "备份目录: $BACKUP_DIR"
echo ""

mkdir -p "$BACKUP_DIR"

# 备份 K8s 资源
echo "备份 K8s 资源..."
kubectl get all -n sandbox-system -o yaml > "${BACKUP_DIR}/sandbox-system-resources.yaml" 2>/dev/null || true
kubectl get all -n sandbox-workloads -o yaml > "${BACKUP_DIR}/sandbox-workloads-resources.yaml" 2>/dev/null || true
kubectl get configmap -n sandbox-system -o yaml > "${BACKUP_DIR}/sandbox-system-configmaps.yaml" 2>/dev/null || true

# 备份 Kustomize 配置
echo "备份 Kustomize 配置..."
cp -r "${K8S_DIR}" "${BACKUP_DIR}/k8s-config"

# 备份版本信息
echo "备份版本信息..."
cat > "${BACKUP_DIR}/versions.txt" <<EOF
# Sandbox 版本信息
# 备份时间: $(date)

ASBCP: $(kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "unknown")
EOF

echo ""
echo "=== 备份完成 ==="
echo "备份位置: $BACKUP_DIR"
echo ""
echo "备份内容:"
ls -lh "$BACKUP_DIR"
