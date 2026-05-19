# AgentSmith Sandbox Control Plane (ASBCP)

## 概述

ASBCP 是 AgentSmith sandbox workload 的控制面服务，提供基于 workload 的 HTTP API：
- 创建和管理 workload Pod（工作区目录由 ASBCP 分配）
- 在 Pod 中执行命令（exec）
- 客户端 keepalive 续期（过期回收必须走 ASBCP workload delete API）

## 快速开始

### 本地开发

```bash
# 构建 Go 二进制
./scripts/build.sh

# 运行（需要可访问的 K8s 集群：in-cluster 或 KUBECONFIG）
export ASBCP_CONFIG_PATH="$(pwd)/asbcp-config.example.yaml"
export ASBCP_SERVICE_KEYS="test-key-123"
go run ./cmd/asbcp
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
./scripts/test-asbcp-api.sh http://localhost:8080 test-key-123
```

### 代码检查

```bash
./scripts/lint.sh
```

## 配置与环境变量

- `ASBCP_CONFIG_PATH`: ASBCP YAML 配置文件路径（默认：`/etc/asbcp/asbcp-config.yaml`）
- `ASBCP_SERVICE_KEYS`: 逗号分隔的 service keys（用于 API 鉴权）

## API 文档

详细 API 文档请参考项目根目录 `docs/api-reference.md`。

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

1. **Go 版本**：以 `go.mod` 为准（当前 `go 1.25.6`）
2. **依赖下载失败**：配置 GOPROXY
3. **权限问题**：确保有 Docker 权限

### 运行时错误

1. **K8s 连接失败**：检查 KUBECONFIG 或集群内配置
2. **Pod 创建失败**：检查 RBAC 权限
3. **镜像拉取失败**：检查镜像是否存在

## 更多信息

- 当前版本：查看 `VERSION` 文件
- 项目文档：查看项目根目录的 `docs/` 目录
