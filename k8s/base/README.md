# Kustomize Base

这是 Sandbox 当前生产模型的 Kustomize base，服务于：

- AFSCP workload mount plan 驱动的 JuiceFS CSI PV/PVC 物化
- workload pod 按 AFSCP plan `mount_path` 挂载 binding PVC
- manager 通过 AFSCP heartbeat/release/status 关闭 workload mount 生命周期

## 资源说明

1. **namespaces.yaml**: 命名空间定义
2. **resource-quota.yaml**: `sandbox-workloads` 资源配额和限制
3. **configmap.yaml**: manager 非密钥运行参数（AFSCP internal service endpoint、workload namespace、CSI knobs）
4. **manager-configmap.yaml**: manager YAML 配置
5. **rbac-manager.yaml**: manager ServiceAccount 和动态 PV 权限
6. **workload-rbac.yaml**: workload namespace pod/PVC 权限
7. **manager-deployment.yaml**: manager Deployment
8. **manager-service.yaml**: manager Service
9. **workload-networkpolicy.yaml**: workload 网络策略

`sandbox-manager-secrets` 由部署环境提供，至少包含 `service-keys` 和
`afscp-orchestrator-token`。base 默认指向集群内
`http://afscp-api.agentsmith-system.svc.cluster.local:8080`，环境差异通过 overlay
覆盖。base 不创建存储后端凭据；PV/PVC 所需 Secret ref 只能来自 AFSCP
orchestrator mount plan。

## 使用

不要直接应用 base，应通过 overlays 部署：

```bash
kubectl apply -k overlays/production
```
