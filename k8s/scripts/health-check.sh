#!/bin/bash
set -euo pipefail

# 健康检查脚本

echo "=== Sandbox 系统健康检查 ==="
echo ""

# 检查 ASBCP Pods
echo "ASBCP Pods:"
kubectl get pods -n sandbox-system -l app=agentsmith-sandbox-control-plane -o wide
echo ""

# 检查 ASBCP Service
echo "ASBCP Service:"
kubectl get service agentsmith-sandbox-control-plane -n sandbox-system
echo ""

# 检查最近的事件
echo "最近事件 (sandbox-system):"
kubectl get events -n sandbox-system --sort-by='.lastTimestamp' | tail -10
echo ""

echo "最近事件 (sandbox-workloads):"
kubectl get events -n sandbox-workloads --sort-by='.lastTimestamp' | tail -10
echo ""

# 检查资源使用
echo "资源使用情况:"
kubectl top pods -n sandbox-system 2>/dev/null || echo "  (metrics-server 未安装，跳过)"
echo ""

# 检查 ASBCP 健康端点
echo "ASBCP 健康检查:"
if kubectl run -it --rm health-check --image=curlimages/curl --restart=Never -n sandbox-system -- curl -s http://agentsmith-sandbox-control-plane/healthz 2>/dev/null | grep -q "OK"; then
    echo "  ✓ ASBCP 健康端点正常"
else
    echo "  ✗ ASBCP 健康端点异常"
fi

echo ""
echo "=== 健康检查完成 ==="
