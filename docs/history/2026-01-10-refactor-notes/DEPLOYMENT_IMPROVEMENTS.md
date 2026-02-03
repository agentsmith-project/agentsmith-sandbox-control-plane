# 部署改进说明

## 改进日期

2026-01-10

## 改进内容

### 1. CA 证书支持

**问题**: 之前需要手动配置 containerd 跳过 TLS 验证，不符合最佳实践。

**解决方案**:
- 将 CA 证书复制到 `k8s/cluster/kind/certs/`
- 在 `create.sh` 中自动配置 CA 证书
- 自动添加到 Kind 节点的信任存储
- 自动配置 containerd 使用 CA 证书

**配置方式**:
```bash
# k8s/cluster/kind/config.env
USE_HARBOR_CA=true
HARBOR_CA_DIR=./certs
```

### 2. HTTP 代理配置

**问题**: 之前需要手动设置/取消环境变量，容易出错。

**解决方案**:
- 通过配置文件统一管理代理设置
- 支持开关控制（`USE_PROXY=true/false`）
- 自动配置 `NO_PROXY` 列表

**配置方式**:
```bash
# k8s/cluster/kind/config.env
USE_PROXY=false  # 或 true
HTTP_PROXY=http://127.0.0.1:8889
HTTPS_PROXY=http://127.0.0.1:8889
NO_PROXY=localhost,127.0.0.1,harbor.pullot.com
```

### 3. 代码清理

**移除的 Workaround**:
- ❌ 手动配置 containerd 跳过 TLS 验证
- ❌ 手动设置环境变量禁用代理
- ❌ 手动 patch Deployment 和 CronJob 镜像引用
- ❌ 手动配置 Kind 节点的 containerd

**改为**:
- ✅ 通过配置文件统一管理
- ✅ 脚本自动处理 CA 证书
- ✅ 脚本自动处理代理配置
- ✅ 使用 Kustomize 管理镜像引用

### 4. 配置文件标准化

创建了统一的配置文件模板：
- `k8s/cluster/kind/config.env.example` - Kind 集群配置
- `k8s/config/deploy.env.example` - 部署配置

所有脚本都支持从配置文件加载参数。

### 5. 离线部署改进

- 修复镜像清单生成（正确处理 Harbor project）
- 修复镜像路径处理（避免重复添加 registry）
- 支持自动生成镜像清单

## 使用方式

### 创建集群（带 CA）

```bash
cd k8s/cluster/kind
cp config.env.example config.env
# 编辑 config.env: USE_HARBOR_CA=true
CONFIG_FILE=./config.env ./create.sh
```

### 创建集群（带代理）

```bash
# 编辑 config.env: USE_PROXY=true, 配置代理地址
CONFIG_FILE=./config.env ./create.sh
```

### 部署

```bash
cd k8s
cp config/deploy.env.example config/deploy.env
# 编辑 deploy.env
CONFIG_FILE=./config/deploy.env ./scripts/deploy.sh dev
```

## 最佳实践

1. **使用配置文件**: 不要硬编码，使用 `.env` 文件
2. **版本控制**: 将 `.example` 文件提交到 Git，`.env` 文件加入 `.gitignore`
3. **CA 证书**: 生产环境使用 CA 证书，不要跳过验证
4. **代理配置**: 通过配置文件管理，便于切换环境
