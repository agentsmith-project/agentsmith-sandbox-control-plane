# sbx 命令分析与测试指南

> **日期**: 2026-02-04
> **项目**: mbos-sandbox-v1
> **入口点**: `./sbx`

---

## 一、现有 sbx 命令最佳实践分析

### 1.1 命令结构概述

项目使用 `./sbx` 脚本作为单一入口点，采用分组命令结构：

```
./sbx <group> <command> [options]
```

**命令分组**:

| 分组 | 功能 | 命令数量 |
|------|------|----------|
| `images` | 镜像构建/推送/导出/导入/验证 | 4 |
| `offline` | 离线包操作 (images 子命令别名) | 4 |
| `tools` | 工具获取/验证 (kubectl/kustomize/skopeo/jq/yq) | 3 |
| `dev` | Kind 开发集群管理 | 4 |
| `k8s` | Kustomize 部署/验证/镜像更新 | 7 |
| `sandbox` | 沙盒命名空间维护 | 2 |

**总计**: 6 个分组，24 个子命令

---

### 1.2 最佳实践符合度评估

#### ✅ 已实现的最佳实践

| 最佳实践 | 实现方式 | 位置 |
|----------|----------|------|
| **单一入口点** | `./sbx` 统一入口 | `sbx:1-296` |
| **模块化设计** | 命令分组，职责清晰 | `scripts/lib/*.sh` |
| **工具离线化** | 内置工具库，可离线部署 | `tools/bin/linux-amd64/` |
| **代理支持** | `--proxy auto|on|off` 灵活配置 | `scripts/lib/proxy.sh` |
| **本地构建支持** | buildx archive 模式，避免 docker daemon | `scripts/lib/docker.sh` |
| **Kustomize 管理** | overlays 模式，多环境支持 | `k8s/overlays/{dev,staging,production}/` |
| **健康检查** | 独立验证脚本 | `k8s/scripts/verify.sh`, `health-check.sh` |
| **幂等操作** | `--force` 标志显式确认 | `sbx dev up/down` |
| **Dry-run 模式** | 清理操作支持 `--dry-run` | `sandbox cleanup` |
| **版本管理** | VERSION 文件，镜像版本控制 | `*/VERSION` |

#### ⚠️ 部分实现/可改进项

| 项目 | 现状 | 建议 |
|------|------|------|
| **Makefile 集成** | 无 Makefile，纯 Bash 实现 | 可添加 Makefile 作为可选入口 |
| **单元测试** | 无独立单元测试命令 | 添加 `./sbx test unit` |
| **集成测试** | 依赖手动脚本执行 | 添加 `./sbx test integration` |
| **日志级别** | 固定日志输出 | 添加 `--log-level debug|info|warn|error` |
| **配置文件** | 环境变量为主 | 支持 `.sbxrc` 配置文件 |
| **并行构建** | 镜像串行构建 | 支持 `--parallel` 构建选项 |
| **镜像缓存** | 每次 pull | 本地缓存检查逻辑 |

#### ❌ 缺失功能 (按优先级排序)

| 优先级 | 功能 | 命令示例 | 描述 |
|--------|------|----------|------|
| **高** | 一键冒烟测试 | `./sbx test smoke` | 完整流程测试 |
| **高** | 快速启动 | `./sbx quickstart` | 自动化初始化 |
| **中** | 依赖检查 | `./sbx check` | 检查依赖环境 |
| **中** | 清理命令 | `./sbx clean all` | 统一清理资源 |
| **低** | 版本信息 | `./sbx version` | 显示版本号 |
| **低** | 补全支持 | `./sbx completion` | Shell 自动补全 |

---

## 二、冒烟测试必备功能检查

### 2.1 冒烟测试定义

冒烟测试 (Smoke Test) 应验证系统核心功能的可运行性：

1. **环境准备**: 工具可用、集群可达
2. **镜像构建**: 镜像能成功构建
3. **系统部署**: 部署到集群并就绪
4. **API 验证**: 核心 API 端点可访问
5. **沙盒创建**: 能创建并运行沙盒
6. **资源清理**: 能正确清理资源

### 2.2 现有测试资源

#### 1. 工具验证脚本
```bash
./sbx tools verify          # 验证内置工具
```
- **覆盖**: skopeo, kubectl, kustomize, jq, yq
- **输出**: 工具版本/状态

#### 2. 部署验证脚本
```bash
./sbx k8s verify dev        # 验证部署状态
```
- **覆盖**: K8s 连接、Kustomize 配置、资源状态、镜像版本
- **位置**: `k8s/scripts/verify.sh`

#### 3. 健康检查脚本
```bash
./sbx k8s health            # 系统健康检查
```
- **覆盖**: Pods、Service、CronJob、事件、资源使用、健康端点
- **位置**: `k8s/scripts/health-check.sh`

#### 4. Manager API 测试脚本
```bash
./manager-service/scripts/test-manager.sh <manager-url> <service-key> [runner-image]
```
- **覆盖**: 11 个 API 端点测试
- **位置**: `manager-service/scripts/test-manager.sh`

**测试覆盖详情**:
1. `/healthz` - 健康检查 (无认证)
2. `/readyz` - 就绪检查 (无认证)
3. `/metrics` - 指标端点 (无认证)
4. `/debug/config` - 配置调试 (无认证)
5. `PUT /v1/sandboxes/{id}` - 创建沙盒 (需认证)
6. `POST /v1/sandboxes/{id}/touch` - TTL 续期 (需认证)
7. `POST /v1/sandboxes/{id}/exec` - 命令执行 (需认证)
8. `POST /v1/sandboxes/{id}/files/upload` - 文件上传 (需认证)
9. `GET /v1/sandboxes/{id}/files/download` - 文件下载 (需认证)
10. `DELETE /v1/sandboxes/{id}` - 删除沙盒 (需认证)
11. 认证失败测试

### 2.3 冒烟测试必备功能完整性评估

| 功能类别 | 必备项 | 现有覆盖 | 缺口 |
|----------|--------|----------|------|
| **环境检查** | 工具验证 | ✅ `./sbx tools verify` | - |
| **环境检查** | 依赖检查 | ⚠️ 需手动 | 建议添加 `./sbx check` |
| **镜像构建** | 镜像构建 | ✅ `./sbx images build` | - |
| **镜像构建** | 镜像验证 | ⚠️ 需手动 | 建议添加 `--verify` 选项 |
| **集群部署** | 集群创建 | ✅ `./sbx dev up` | - |
| **集群部署** | 应用部署 | ✅ `./sbx k8s deploy` | - |
| **集群部署** | 部署验证 | ✅ `./sbx k8s verify` | - |
| **API 测试** | 健康检查 | ✅ `./sbx k8s health` | - |
| **API 测试** | API 端点 | ✅ `test-manager.sh` | - |
| **沙盒测试** | 创建沙盒 | ✅ `test-manager.sh` | - |
| **沙盒测试** | 执行命令 | ✅ `test-manager.sh` | - |
| **资源清理** | 删除沙盒 | ✅ `test-manager.sh` | - |
| **资源清理** | 清理集群 | ⚠️ 需手动 | `./sbx dev down --force` |

**结论**: 冒烟测试必备功能基本完整，但缺少统一的自动化测试入口。

---

## 三、手动测试客户端操作指南

### 3.1 测试环境要求

#### 硬件要求
- CPU: 4 核心以上
- 内存: 8GB 以上
- 磁盘: 20GB 可用空间

#### 软件依赖
```bash
# 必需软件
- Docker 20.10+ (with buildx)
- kind 0.20+
- Go 1.21+ (仅本地开发需要)

# 可选 (自动下载)
- kubectl
- kustomize
- skopeo
- jq
- yq
```

#### 网络要求
- 可访问 Docker Hub (或配置镜像代理)
- 可访问 GitHub (用于下载工具)
- 如使用 Harbor: 需要访问 Harbor 仓库

---

### 3.2 完整测试流程

#### 阶段 1: 环境准备

```bash
# 1. 进入项目目录
cd /path/to/mbos-sandbox-v1

# 2. 获取内置工具 (推荐使用 vendored tools)
./sbx tools fetch --proxy auto
./sbx tools verify

# 预期输出:
# [tools] ok: skopeo
# [tools] ok: kubectl
# [tools] ok: kustomize
# [tools] ok: jq
# [tools] ok: yq
```

#### 阶段 2: 启动开发环境 (Kind)

```bash
# 选项 A: 使用本地 registry (无外部依赖)
./sbx dev up --force --proxy off

# 选项 B: 使用远程 Harbor
export HARBOR_REGISTRY="harbor.example.com:28443"
export HARBOR_PROJECT="agentsmith"
export HARBOR_USERNAME="username"
export HARBOR_PASSWORD="password"
export NO_PROXY="$HARBOR_REGISTRY,$NO_PROXY"
./sbx dev up --force --proxy off --harbor-ca auto

# 预期输出包含:
# [INFO] Creating kind cluster: sandbox-cluster
# [INFO] Building images into local docker...
# [INFO] Built images: sandbox-manager:2.0.2, sandbox-runner:1.0.0, sandbox-gc:1.0.0
# [INFO] Deploying to dev...
# [INFO] Dev environment ready
```

#### 阶段 3: 验证部署状态

```bash
# 方法 1: 使用 sbx 验证命令
./sbx k8s verify dev

# 预期输出:
# === 验证部署: dev ===
# [1/4] 检查 K8s 集群连接...
#   ✓ 已连接到集群 (版本: v1.33.1)
# [2/4] 验证 Kustomize 配置...
#   ✓ Kustomize 配置有效
# [3/4] 检查资源状态...
#   ✓ Namespace sandbox-system 存在
#   ✓ Namespace sandbox 存在
#   ✓ Manager Deployment 就绪 (3/3)
#   ✓ Manager Service 存在
#   ✓ GC CronJob 存在
# [4/4] 验证镜像版本...
#   Manager: sandbox-manager:2.0.2
#   Runner (default): sandbox-runner:1.0.0

# 方法 2: 健康检查
./sbx k8s health

# 方法 3: 查看 Pod 状态
tools/bin/linux-amd64/kubectl get pods -n sandbox-system
tools/bin/linux-amd64/kubectl get pods -n sandbox
```

#### 阶段 4: API 端到端测试

```bash
# 1. 端口转发 Manager Service
tools/bin/linux-amd64/kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80 &
PF_PID=$!
trap "kill $PF_PID 2>/dev/null || true" EXIT

# 2. 等待服务就绪
for i in {1..60}; do
    if curl -s http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
        echo "Manager is ready"
        break
    fi
    sleep 1
done

# 3. 获取 Service Key
SERVICE_KEY=$(tools/bin/linux-amd64/kubectl -n sandbox-system get secret sandbox-manager-keys \
    -o jsonpath='{.data.SERVICE_KEYS}' | base64 -d | cut -d',' -f1)
echo "Service Key: $SERVICE_KEY"

# 4. 运行 API 测试
./manager-service/scripts/test-manager.sh http://127.0.0.1:8080 "$SERVICE_KEY"

# 预期输出:
# === Sandbox Manager API Test ===
# Manager URL: http://127.0.0.1:8080
# Service Key: test-key-123
# ...
# 1. Testing /healthz (no auth)...
# ✓ /healthz returned 200
# ...
# === All tests passed! ===
```

#### 阶段 5: 手动沙盒测试 (可选)

```bash
# 设置环境变量
export MANAGER_URL="http://127.0.0.1:8080"
export SERVICE_KEY="your-service-key"
export SESSION_ID="manual-test-$(date +%s)"

# 1. 创建沙盒
curl -X PUT "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}" \
    -H "X-Service-Key: ${SERVICE_KEY}" \
    -H "Content-Type: application/json" \
    -d '{
        "ttlSeconds": 900,
        "containerName": "runner",
        "workdir": "/workspace",
        "cpuLimit": "1",
        "memoryLimit": "1Gi",
        "env": {"TEST_VAR": "hello"}
    }'

# 2. 执行命令
curl -X POST "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/exec" \
    -H "X-Service-Key: ${SERVICE_KEY}" \
    -H "Content-Type: application/json" \
    -d '{
        "cmd": ["echo", "Hello from manual test"],
        "timeoutSeconds": 10
    }'

# 3. 查看沙盒状态
tools/bin/linux-amd64/kubectl get pods -n sandbox -l app=llm-sandbox

# 4. 删除沙盒
curl -X DELETE "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}" \
    -H "X-Service-Key: ${SERVICE_KEY}"
```

#### 阶段 6: 清理资源

```bash
# 1. 停止端口转发 (如果后台运行)
kill %1 2>/dev/null || true

# 2. 删除 Kind 集群
./sbx dev down --force

# 3. (可选) 清理本地镜像
docker images | grep 'sandbox-' | awk '{print $1":"$2}' | xargs -r docker rmi -f
```

---

### 3.3 常见问题排查

#### 问题 1: 工具验证失败
```bash
# 症状
[ERROR] Missing lock file: tools/vendor/tools.lock.json

# 解决
./sbx tools fetch --proxy auto
```

#### 问题 2: 镜像构建超时
```bash
# 症状
failed to do request: Head "https://registry-1.docker.io/v2/...": dial tcp: i/o timeout

# 解决方案 A: 使用代理
export HTTP_PROXY="http://proxy.example.com:8080"
export HTTPS_PROXY="http://proxy.example.com:8080"
export NO_PROXY="localhost,127.0.0.1"
./sbx dev up --force --proxy on

# 解决方案 B: 使用 Docker 镜像加速
# 编辑 /etc/docker/daemon.json
{
  "registry-mirrors": ["https://docker.mirrors.ustc.edu.cn"]
}
sudo systemctl restart docker
```

#### 问题 3: Kind 集群创建失败
```bash
# 症状
Failed to create cluster: node creation failed

# 解决
kind delete cluster --name sandbox-cluster 2>/dev/null || true
./sbx dev up --force --proxy off
```

#### 问题 4: Harbor TLS 证书错误
```bash
# 症状
x509: certificate signed by unknown authority

# 解决
./sbx dev up --force --harbor-ca auto

# 或手动下载 CA 证书
mkdir -p secrets
echo -n | openssl s_client -connect harbor.example.com:28443 \
    | sed -ne '/-BEGIN CERTIFICATE-/,/-END CERTIFICATE-/p' > secrets/harbor-ca.crt
```

#### 问题 5: Pod 无法启动
```bash
# 查看 Pod 状态
tools/bin/linux-amd64/kubectl get pods -n sandbox-system
tools/bin/linux-amd64/kubectl describe pod <pod-name> -n sandbox-system

# 查看日志
tools/bin/linux-amd64/kubectl logs <pod-name> -n sandbox-system

# 常见原因
# - ImagePullBackOff: 镜像不存在或认证失败
# - CrashLoopBackOff: 容器启动失败，查看日志
# - OOMKilled: 内存不足
```

---

## 四、改进建议

### 4.1 高优先级建议

#### 1. 添加统一测试命令

```bash
# 建议添加
./sbx test smoke [--env dev|staging|production] [--verbose]
```

**实现要点**:
- 自动运行完整测试流程
- 生成测试报告
- 失败时自动清理

#### 2. 添加环境检查命令

```bash
# 建议添加
./sbx check [docker|kind|tools|network]
```

**检查项**:
- Docker 版本和状态
- kind 版本
- 网络连通性
- 磁盘空间

#### 3. 添加快速启动命令

```bash
# 建议添加
./sbx quickstart [--force] [--proxy auto|on|off]
```

**功能**:
- 自动检测并安装依赖
- 创建配置文件
- 一键启动完整环境

### 4.2 中优先级建议

#### 1. 日志级别控制

```bash
# 建议添加
--log-level debug|info|warn|error
--log-file /path/to/log
```

#### 2. 配置文件支持

```bash
# .sbxrc 示例
REGISTRY=harbor.example.com:28443
HARBOR_PROJECT=agentsmith
PROXY_MODE=auto
LOG_LEVEL=info
```

#### 3. 并行构建支持

```bash
./sbx images build --parallel
```

### 4.3 低优先级建议

#### 1. 版本信息命令

```bash
./sbx version
# sbx version 1.0.0
# manager: 2.0.2
# runner: 1.0.0
# gc: 1.0.0
```

#### 2. Shell 自动补全

```bash
./sbx completion bash
./sbx completion zsh
```

#### 3. Makefile 集成

```makefile
.PHONY: help dev test clean

help: ## Show this help message
    @./sbx --help

dev: ## Start dev environment
    @./sbx dev up --force --proxy off

test: ## Run smoke tests
    @./sbx test smoke

clean: ## Clean all resources
    @./sbx dev down --force
```

---

## 五、总结

### 5.1 现有命令优势

1. **模块化设计**: 清晰的命令分组，职责分离
2. **离线友好**: 内置工具库，支持离线部署
3. **代理支持**: 灵活的代理配置选项
4. **多环境**: Kustomize overlays 支持多环境
5. **验证完善**: 部署验证和健康检查脚本完整
6. **API 测试**: test-manager.sh 覆盖核心 API

### 5.2 主要缺口

1. **统一测试入口**: 缺少 `./sbx test smoke` 命令
2. **环境检查**: 缺少依赖检查命令
3. **快速启动**: 缺少一键初始化命令
4. **日志控制**: 固定日志输出，无级别控制
5. **配置管理**: 纯环境变量，无配置文件支持

### 5.3 冒烟测试评估

**必备功能完整性**: ✅ **85%**

- 核心功能完整: ✅
- 自动化程度: ⚠️ 需要多个命令手动组合
- 测试覆盖: ✅ API 端点覆盖全面
- 文档支持: ✅ 有详细的测试脚本

---

## 附录 A: 快速参考

### A.1 常用命令速查

| 操作 | 命令 |
|------|------|
| 获取工具 | `./sbx tools fetch --proxy auto` |
| 验证工具 | `./sbx tools verify` |
| 启动开发环境 | `./sbx dev up --force --proxy off` |
| 查看状态 | `./sbx dev status` |
| 验证部署 | `./sbx k8s verify dev` |
| 健康检查 | `./sbx k8s health` |
| 测试 API | `./manager-service/scripts/test-manager.sh <url> <key>` |
| 清理环境 | `./sbx dev down --force` |

### A.2 环境变量参考

```bash
# Harbor 配置
export HARBOR_REGISTRY="harbor.example.com:28443"
export HARBOR_PROJECT="agentsmith"
export HARBOR_USERNAME="username"
export HARBOR_PASSWORD="password"

# 代理配置
export HTTP_PROXY="http://proxy.example.com:8080"
export HTTPS_PROXY="http://proxy.example.com:8080"
export NO_PROXY="localhost,127.0.0.1,harbor.example.com"

# Registry 配置
export REGISTRY="$HARBOR_REGISTRY"
```

### A.3 有用的 kubectl 命令

```bash
# 查看 Pods
tools/bin/linux-amd64/kubectl get pods -n sandbox-system
tools/bin/linux-amd64/kubectl get pods -n sandbox

# 查看日志
tools/bin/linux-amd64/kubectl logs -f deployment/sandbox-manager -n sandbox-system

# 端口转发
tools/bin/linux-amd64/kubectl port-forward svc/sandbox-manager 8080:80 -n sandbox-system

# 获取 Service Key
tools/bin/linux-amd64/kubectl get secret sandbox-manager-keys -n sandbox-system \
    -o jsonpath='{.data.SERVICE_KEYS}' | base64 -d

# 执行 Pod 命令
tools/bin/linux-amd64/kubectl exec -it <pod-name> -n sandbox -- bash
```

---

## 附录 B: 测试检查清单

### B.1 环境准备检查清单

- [ ] Docker 已安装且运行中
- [ ] kind 已安装
- [ ] 磁盘空间充足 (>20GB)
- [ ] 网络连接正常 (或配置代理)
- [ ] Harbor 凭证已设置 (如使用远程仓库)

### B.2 功能测试检查清单

- [ ] `./sbx tools verify` 通过
- [ ] `./sbx dev up` 成功创建集群
- [ ] `./sbx k8s verify dev` 所有检查通过
- [ ] `./sbx k8s health` 健康检查通过
- [ ] Manager 服务可访问
- [ ] `test-manager.sh` 所有测试通过
- [ ] 可创建沙盒
- [ ] 可在沙盒中执行命令
- [ ] 可删除沙盒
- [ ] `./sbx dev down --force` 清理成功

### B.3 清理验证检查清单

- [ ] 所有 Pods 已删除
- [ ] kind 集群已删除
- [ ] 无残留 Docker 容器
- [ ] 端口转发已停止
- [ ] 临时文件已清理

---

**文档版本**: 1.0
**最后更新**: 2026-02-04
**维护者**: mbos-sandbox-v1 团队
