#!/bin/bash
set -euo pipefail

# 回滚脚本

ENVIRONMENT="${1:-production}"
VERSION="${2:-}"

echo "=== 回滚部署 ==="
echo "环境: $ENVIRONMENT"
echo ""

if [ -z "$VERSION" ]; then
    echo "可用版本（从 ConfigMap 获取）:"
    kubectl get configmap -n sandbox-system -l app.kubernetes.io/name=sandbox-version -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || echo "  (无版本记录)"
    echo ""
    read -p "请输入要回滚的版本: " VERSION
fi

if [ -z "$VERSION" ]; then
    echo "错误: 未指定版本"
    exit 1
fi

echo "回滚到版本: $VERSION"
echo ""
echo "注意: 此脚本需要手动更新 Kustomize overlays 中的镜像版本"
echo "然后运行: kubectl apply -k overlays/${ENVIRONMENT}"
echo ""
echo "或者使用 k8s/scripts/update-images.sh 脚本自动更新"
