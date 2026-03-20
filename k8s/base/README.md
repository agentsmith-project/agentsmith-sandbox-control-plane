# Kustomize Base

这是 Sandbox 当前生产模型的 Kustomize base，服务于：

- JuiceFS CSI workspace binding
- workload pod `/workspace` 挂载
- 过期 workload reclaim

## 资源说明

1. **namespaces.yaml**: 命名空间定义
2. **resource-quota.yaml**: 资源配额和限制
3. **configmap.yaml** / **manager-configmap.yaml**: manager 配置
4. **manager-secret.yaml**: service key 等密钥
5. **rbac-manager.yaml**: manager 的 RBAC
6. **manager-deployment.yaml**: manager Deployment
7. **manager-service.yaml**: manager Service
8. **cleaner-cronjob.yaml**: 过期 workload pod 清理 CronJob
9. **juicefs-pvc.yaml**: JuiceFS CSI 相关基础资源
10. **workload-networkpolicy.yaml**: workload 网络策略
11. **workload-rbac.yaml**: workload 相关 RBAC

## 使用

不要直接应用 base，应通过 overlays 部署：

```bash
kubectl apply -k overlays/production
```
