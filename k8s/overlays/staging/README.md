# Staging Environment Overlay

预发布环境配置。

## 特点

- Manager 副本数：2
- 资源限制：中等
- TTL：1800 秒（30 分钟）
- 镜像版本：固定版本号

## 使用

```bash
kubectl apply -k overlays/staging
```
