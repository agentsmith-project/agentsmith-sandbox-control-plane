#!/bin/bash
set -euo pipefail

# 验证部署脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
K8S_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

ENVIRONMENT="${1:-dev}"
OVERLAY_DIR="${K8S_DIR}/overlays/${ENVIRONMENT}"

echo "=== 验证部署: $ENVIRONMENT ==="
echo ""

# 检查 K8s 连接
echo "[1/4] 检查 K8s 集群连接..."
if ! kubectl cluster-info &> /dev/null; then
    echo "  ✗ 无法连接到集群"
    exit 1
fi
K8S_VERSION=$(kubectl version --short 2>/dev/null | grep "Server Version" | awk '{print $3}' || echo "unknown")
echo "  ✓ 已连接到集群 (版本: $K8S_VERSION)"

# 验证 Kustomize 配置
echo ""
echo "[2/4] 验证 Kustomize 配置..."
if command -v kustomize &> /dev/null; then
    if kustomize build "$OVERLAY_DIR" > /dev/null 2>&1; then
        echo "  ✓ Kustomize 配置有效"
    else
        echo "  ✗ Kustomize 配置无效"
        kustomize build "$OVERLAY_DIR" 2>&1 | head -20
        exit 1
    fi
else
    echo "  ⚠ kustomize 未安装，跳过配置验证"
fi

# 检查资源状态
echo ""
echo "[3/4] 检查资源状态..."

# 检查 Namespace
if kubectl get namespace sandbox-system &> /dev/null; then
    echo "  ✓ Namespace sandbox-system 存在"
else
    echo "  ✗ Namespace sandbox-system 不存在"
fi

if kubectl get namespace sandbox-workloads &> /dev/null; then
    echo "  ✓ Namespace sandbox-workloads 存在"
else
    echo "  ✗ Namespace sandbox-workloads 不存在"
fi

# 检查 ASBCP Deployment
if kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system &> /dev/null; then
    READY=$(kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
    DESIRED=$(kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    if [ "$READY" = "$DESIRED" ] && [ "$READY" != "0" ]; then
        echo "  ✓ ASBCP Deployment 就绪 ($READY/$DESIRED)"
    else
        echo "  ✗ ASBCP Deployment 未就绪 ($READY/$DESIRED)"
    fi
else
    echo "  ✗ ASBCP Deployment 不存在"
fi

# 检查 Service
if kubectl get service agentsmith-sandbox-control-plane -n sandbox-system &> /dev/null; then
    echo "  ✓ ASBCP Service 存在"
else
    echo "  ✗ ASBCP Service 不存在"
fi

# 验证镜像版本
echo ""
echo "[4/4] 验证镜像版本..."
ASBCP_IMAGE=$(kubectl get deployment agentsmith-sandbox-control-plane -n sandbox-system -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
if [ -n "$ASBCP_IMAGE" ]; then
    echo "  ASBCP: $ASBCP_IMAGE"
else
    echo "  ✗ 无法获取 ASBCP 镜像"
fi

echo ""
echo "=== 验证完成 ==="
