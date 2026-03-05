# Kustomize Base

这是 Sandbox 系统的 Kustomize base 配置，包含所有基础资源。

## 资源说明

1. **namespaces.yaml**: 命名空间定义
2. **resource-quota.yaml**: 资源配额和限制
3. **configmap.yaml** / **manager-configmap.yaml**: 配置
4. **manager-secret.yaml**: 密钥（如 SERVICE_KEYS）
5. **rbac-manager.yaml**: Manager 的 RBAC 配置
6. **manager-deployment.yaml**: Manager Deployment
7. **manager-service.yaml**: Manager Service
8. **cleaner-cronjob.yaml**: 过期 Pod 清理 CronJob（无 snapshot/GC）
9. **juicefs-pvc.yaml**: JuiceFS PVC（工作区存储）
10. **workload-networkpolicy.yaml**: Workload 网络策略
11. **workload-rbac.yaml**: Workload 相关 RBAC

## 镜像引用

Base 中的镜像使用占位符，实际镜像版本在 overlays 中指定：
- `sandbox-manager` - Manager 与 Cleaner 共用同一镜像（overlays 中覆盖 tag）

## 使用

不要直接应用 base，应该使用 overlays：

```bash
kubectl apply -k overlays/production
```
