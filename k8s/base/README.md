# Kustomize Base

这是 Sandbox 系统的 Kustomize base 配置，包含所有基础资源。

## 资源说明

1. **namespaces.yaml**: 命名空间定义
2. **resource-quota.yaml**: 资源配额和限制
3. **configmap.yaml**: 配置管理（ConfigMap）
4. **rbac-manager.yaml**: Manager 的 RBAC 配置
5. **manager-deployment.yaml**: Manager Deployment
6. **manager-service.yaml**: Manager Service
7. **network-policy.yaml**: 网络策略
8. **rbac-gc.yaml**: GC 的 RBAC 配置
9. **gc-cronjob.yaml**: GC CronJob

## 镜像引用

Base 中的镜像使用占位符，实际镜像版本在 overlays 中指定：
- `sandbox-manager:1.0.0` - 在 overlays 中覆盖
- `sandbox-runner:1.0.0` - 在 overlays 中覆盖
- `sandbox-gc:1.0.0` - 在 overlays 中覆盖

## 使用

不要直接应用 base，应该使用 overlays：

```bash
kubectl apply -k overlays/production
```
