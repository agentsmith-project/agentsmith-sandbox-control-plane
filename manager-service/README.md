# Sandbox Manager Service

## 概述

Manager Service 是 Sandbox 系统的核心服务，提供 HTTP API 用于：
- 创建和管理沙箱 Pod
- 在 Pod 中执行命令
- 文件上传和下载
- 会话管理（TTL、活动跟踪）

## 快速开始

### 本地开发

```bash
# 构建 Go 二进制
./scripts/build.sh

# 运行（需要可访问的 K8s 集群：in-cluster 或 KUBECONFIG）
export CONFIG_PATH="$(pwd)/manager-config.example.yaml"
export SERVICE_KEYS="test-key-123"
./manager
```

### 构建镜像

```bash
# 构建 Docker 镜像
./scripts/build-image.sh

# 构建并加载到 kind
./scripts/build-image.sh -l

# 构建并推送
./scripts/build-image.sh -p
```

### 测试

```bash
./scripts/test.sh
```

### API 冒烟测试（curl）

```bash
./scripts/test-manager.sh http://localhost:8080 test-key-123
```

### 代码检查

```bash
./scripts/lint.sh
```

## 配置与环境变量

- `CONFIG_PATH`: Manager YAML 配置文件路径（默认：`/etc/sandbox-manager/manager-config.yaml`）
- `SERVICE_KEYS`: 逗号分隔的 service keys（用于 API 鉴权）
- `CONFIG_RELOAD_DEBOUNCE` / `CONFIG_RELOAD_MIN_INTERVAL` / `CONFIG_RELOAD_BACKOFF_MAX` / `STRICT_CONFIG_RELOAD`: 配置热加载参数（可选）

## API 文档

详细 API 文档请参考 `../docs/API.md`。

## 版本管理

版本号存储在 `VERSION` 文件中。

## 脚本说明

- `build.sh`: 构建 Go 二进制
- `build-image.sh`: 构建 Docker 镜像
- `test.sh`: 运行测试
- `lint.sh`: 代码检查
- `tag.sh`: 版本标签管理
- `push.sh`: 推送镜像
- 镜像版本更新：使用 `k8s/scripts/update-images.sh` 统一更新
- `verify.sh`: 验证镜像

## 故障排查

### 构建失败

1. **Go 版本**：需要 Go 1.21+
2. **依赖下载失败**：配置 GOPROXY
3. **权限问题**：确保有 Docker 权限

### 运行时错误

1. **K8s 连接失败**：检查 KUBECONFIG 或集群内配置
2. **Pod 创建失败**：检查 RBAC 权限
3. **镜像拉取失败**：检查镜像是否存在

## 更多信息

- 当前版本：查看 `VERSION` 文件
- 项目文档：查看项目根目录的 `docs/` 目录
