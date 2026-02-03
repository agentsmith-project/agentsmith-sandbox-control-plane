# Kubernetes 配置和工具

完整的 Kubernetes 部署配置，使用 Kustomize 管理。

## 快速开始

### 创建 Kind 集群

```bash
./cluster/kind/create.sh sandbox-cluster
```

### 部署到环境

```bash
# 开发环境
./scripts/deploy.sh dev

# 预发布环境
./scripts/deploy.sh staging

# 生产环境
./scripts/deploy.sh production
```

## 目录结构

- `base/` - Kustomize base 配置
- `overlays/` - 环境特定配置（dev/staging/production）
- `scripts/` - 部署和管理脚本
- `cluster/kind/` - Kind 集群管理

## 详细文档

请查看各子目录的 README 文件。
