# Production Environment Overlay

生产环境配置。

## 特点

- ASBCP 副本数：3
- 资源限制：较大
- 镜像版本：固定版本号
- 安全加固：启用额外安全策略
- 访问配置：internal-only by default，只渲染 ClusterIP 服务

## 访问配置

默认 production kustomization 不暴露 `agentsmith-sandbox-control-plane` 到 Ingress、NodePort 或 LoadBalancer。
`access/` 下的样例只作为 private operator opt-in，必须由集群 operator 显式审查后单独引用。

## 使用

```bash
kubectl apply -k overlays/production
```
