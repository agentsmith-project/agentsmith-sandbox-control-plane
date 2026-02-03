# Production Environment Overlay

生产环境配置。

## 特点

- Manager 副本数：3
- 资源限制：较大
- TTL：3600 秒（1 小时）
- 镜像版本：固定版本号
- 安全加固：启用额外安全策略
- 访问配置：包含 Ingress（默认）

## 访问配置

生产环境默认包含 Ingress 配置。如果需要使用其他访问方式：

### 使用 NodePort

修改 `kustomization.yaml`：
```yaml
resources:
  - ../../base
  - access/nodeport.yaml  # 替换 ingress.yaml
```

### 使用 LoadBalancer

修改 `kustomization.yaml`：
```yaml
resources:
  - ../../base
  - access/loadbalancer.yaml  # 替换 ingress.yaml
```

## 使用

```bash
kubectl apply -k overlays/production
```

## 配置 Ingress

编辑 `access/ingress.yaml`，修改 `host` 字段为你的域名。
