# Sandbox GC Image

## 概述

GC（Garbage Collector）镜像用于定期清理过期的沙箱 Pod。作为 CronJob 运行，每分钟执行一次清理任务。

## 快速开始

### 构建镜像

```bash
# 使用默认配置
./scripts/build.sh

# 指定版本和 registry
./scripts/build.sh -t 1.0.1 -r my-registry.com

# 构建并自动加载到 kind
./scripts/build.sh -l
```

### 验证镜像

```bash
./scripts/verify.sh
```

## 配置

复制 `config.env.example` 为 `config.env` 并修改。

## 脚本说明

所有脚本与 Runner 镜像相同，请参考 `images/runner/README.md`。

## 故障排查

### kubectl 下载失败

kubectl 现在在 Dockerfile 中直接下载。如果下载失败：

1. 检查网络连接
2. 检查代理配置（如果使用代理）
3. 可以通过 `--build-arg KUBECTL_VERSION=v1.29.0` 指定版本
