# Kustomize Base

这是 ASBCP 当前生产模型的 Kustomize base，服务于：

- AFSCP workload mount plan 驱动的 JuiceFS CSI PV/PVC 物化
- workload pod 按 AFSCP plan `mount_path` 挂载 binding PVC
- ASBCP 通过 AFSCP heartbeat/release/status 关闭 workload mount 生命周期

## 资源说明

Legacy filename compatibility: `rbac-manager.yaml`, `manager-deployment.yaml`,
and `manager-service.yaml` are retained only to avoid a broad file-rename slice.
Their rendered resource identity is ASBCP (`agentsmith-sandbox-control-plane` /
`app.kubernetes.io/component=asbcp`), and new active docs should use ASBCP names.

1. **namespaces.yaml**: 命名空间定义
2. **resource-quota.yaml**: `sandbox-workloads` 资源配额和限制
3. **configmap.yaml**: ASBCP 非密钥运行参数（AFSCP internal service endpoint、workload namespace、CSI knobs）
4. **asbcp-configmap.yaml**: ASBCP YAML 配置
5. **rbac-manager.yaml**: ASBCP ServiceAccount 和动态 PV 权限
6. **workload-rbac.yaml**: workload namespace pod/PVC 权限
7. **manager-deployment.yaml**: ASBCP Deployment
8. **manager-service.yaml**: ASBCP Service
9. **workload-networkpolicy.yaml**: workload 网络策略

`agentsmith-sandbox-control-plane-secrets` 由部署环境提供，至少包含 `service-keys` 和
`afscp-orchestrator-token`。base 默认指向集群内
`http://afscp-api.agentsmith-system.svc.cluster.local:8080`，环境差异通过 overlay
覆盖。base 不创建存储后端凭据；PV/PVC 所需 Secret ref 只能来自 AFSCP
orchestrator mount plan。

## 使用

不要直接应用 base，应通过 overlays 部署：

```bash
kubectl apply -k overlays/production
```
