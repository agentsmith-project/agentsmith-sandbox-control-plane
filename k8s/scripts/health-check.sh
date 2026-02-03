#!/bin/bash
set -euo pipefail

# 健康检查脚本

echo "=== Sandbox 系统健康检查 ==="
echo ""

# 检查 Manager Pods
echo "Manager Pods:"
kubectl get pods -n sandbox-system -l app=sandbox-manager -o wide
echo ""

# 检查 Manager Service
echo "Manager Service:"
kubectl get service sandbox-manager -n sandbox-system
echo ""

# 检查 GC CronJob
echo "GC CronJob:"
kubectl get cronjob sandbox-gc -n sandbox-system
echo ""

# 检查最近的事件
echo "最近事件 (sandbox-system):"
kubectl get events -n sandbox-system --sort-by='.lastTimestamp' | tail -10
echo ""

# 检查资源使用
echo "资源使用情况:"
kubectl top pods -n sandbox-system 2>/dev/null || echo "  (metrics-server 未安装，跳过)"
echo ""

# 检查 Manager 健康端点
echo "Manager 健康检查:"
if kubectl run -it --rm health-check --image=curlimages/curl --restart=Never -n sandbox-system -- curl -s http://sandbox-manager/healthz 2>/dev/null | grep -q "OK"; then
    echo "  ✓ Manager 健康端点正常"
else
    echo "  ✗ Manager 健康端点异常"
fi

echo ""
echo "=== 健康检查完成 ==="
