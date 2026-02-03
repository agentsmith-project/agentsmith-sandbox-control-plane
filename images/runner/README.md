# Sandbox Runner Image

## 概述

Runner 镜像用于执行用户代码的容器环境。包含完整的开发工具链：
- Python 3.x + 科学计算库（numpy, pandas, scikit-learn 等）
- Node.js 20.18.0 LTS + 开发工具
- 深度学习框架（PyTorch, TensorFlow）
- 中文字体支持
- 现代命令行工具（ripgrep, fd, bat）

## 快速开始

### 构建镜像

```bash
# 使用默认配置
./scripts/build.sh

# 指定版本和 registry
./scripts/build.sh -t 1.2.0 -r my-registry.com

# 构建并自动加载到 kind
./scripts/build.sh -l

# 构建并推送到 registry
./scripts/build.sh -p
```

### 验证镜像

```bash
./scripts/verify.sh
```

### 更新版本

```bash
# 更新 VERSION 文件
echo "1.2.1" > VERSION

# 构建新版本
./scripts/build.sh -t 1.2.1

# 更新 Kustomize 引用（使用统一脚本）
cd ../../k8s && ./scripts/update-images.sh
```

## 配置

复制 `config.env.example` 为 `config.env` 并修改：

```bash
cp config.env.example config.env
# 编辑 config.env
```

### 配置项说明

- `REGISTRY`: 镜像仓库地址
- `VERSION`: 镜像版本（默认从 VERSION 文件读取）
- `BUILDX_BUILDER`: buildx builder 名称
- `CACHE_DIR`: 构建缓存目录
- `HTTP_PROXY`: HTTP 代理（可选）
- `AUTO_LOAD_TO_KIND`: 是否自动加载到 kind
- `AUTO_PUSH`: 是否自动推送到 registry

## 版本管理

版本号存储在 `VERSION` 文件中，遵循语义化版本：
- MAJOR: 不兼容的 API 修改
- MINOR: 向后兼容的功能新增
- PATCH: 向后兼容的问题修复

## 脚本说明

### build.sh
构建 Docker 镜像，支持：
- 使用 buildx 和缓存加速构建
- 自动加载到 kind 集群
- 自动推送到 registry

### verify.sh
验证镜像内容：
- 检查镜像是否存在
- 验证 Node.js 版本
- 验证 Python 版本
- 验证中文字体

### tag.sh
为镜像打新标签并更新 VERSION 文件

### push.sh
推送镜像到 registry

### 镜像版本更新
更新 Kustomize overlays 中的镜像版本引用

## 故障排查

### 构建失败

1. **网络问题**：配置 HTTP_PROXY 和 HTTPS_PROXY
2. **磁盘空间不足**：清理 Docker 镜像和缓存
3. **buildx builder 问题**：删除并重新创建 builder

### 验证失败

1. **镜像不存在**：先运行 build.sh
2. **Node.js 版本不正确**：检查 Dockerfile 中的 NODE_VERSION
3. **中文字体缺失**：检查 Dockerfile 中的字体安装步骤

## 更多信息

- 当前版本：查看 `VERSION` 文件
- 变更日志：查看 `CHANGELOG.md`（如果存在）
- 项目文档：查看项目根目录的 `docs/` 目录
