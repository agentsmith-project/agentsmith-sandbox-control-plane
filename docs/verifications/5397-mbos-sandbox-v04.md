# mbos-sandbox 收口检查文档 (V04)

**检查日期**: 2026-02-10
**任务编号**: 5397-mbos-sandbox-v04
**检查范围**: mbos-sandbox 项目完整代码审查

---

## 一、检查概述

本次审查对 mbos-sandbox 项目进行了全面的代码审查，重点关注可能影响生产稳定性的 bug 和错误。审查标准以"能上线稳定运行"为目标，不追求完美主义，避免过度设计和范围蔓延。

### 审查覆盖的组件

| 组件 | 文件路径 |
|------|----------|
| Manager Service | `manager-service/cmd/manager/` |
| Cleaner Service | `manager-service/cmd/cleaner/` |
| WebSocket Handler | `manager-service/internal/websocket/` |
| Session Management | `manager-service/internal/session/` |
| Kubernetes Client | `manager-service/internal/k8s/` |
| Shell Bridge Client | `manager-service/internal/shellbridge/` |
| Configuration | `manager-service/internal/config/` |
| Buffer | `manager-service/internal/buffer/` |
| Authentication | `manager-service/internal/auth/` |
| Files | `manager-service/internal/files/` |
| Finalizer | `manager-service/internal/finalizer/` |
| HTTP API | `manager-service/internal/httpapi/` |
| Storage | `manager-service/internal/storage/` |
| Observability | `manager-service/internal/observability/` |
| Rate Limiting | `manager-service/internal/ratelimit/` |

---

## 二、严重问题 (Critical - 必须修复)

### 2.1 Cleaner Service 完全无法工作

**文件**: `k8s/base/cleaner-cronjob.yaml:44-46`, `manager-service/cmd/cleaner/main.go:85`

**问题描述**: Cleaner Service 存在多个配置错误，导致其完全无法执行清理任务。

#### 问题 2.1.1: 标签选择器不匹配

**位置**: `cmd/cleaner/main.go:85`

```go
LabelSelector: fmt.Sprintf("app=%s", sandboxAppLabel),
```

其中 `sandboxAppLabel = "sandbox"`，但实际 Pod 创建时使用的标签是 `app=llm-sandbox`（见 `internal/config/types.go:312`）。

**影响**: Cleaner 永远找不到需要清理的 Pod，每次运行都会显示 "Found 0 pods"。

**修复建议**:
```go
// 修改为与实际 Pod 标签一致
var sandboxAppLabel = "llm-sandbox"
```

#### 问题 2.1.2: 重复的 namespace 参数

**位置**: `k8s/base/cleaner-cronjob.yaml:44-46`

```yaml
args:
- --namespace=sandbox-system
- --namespace=sandbox-workspaces
```

**影响**: Go 的 flag 包只会使用最后一个值，导致 `sandbox-system` 命名空间永远不会被扫描。

**修复建议**: 需要重新设计架构以支持多命名空间扫描，或为每个命名空间创建独立的 CronJob。

#### 问题 2.1.3: 命名空间白名单不包含目标命名空间

**位置**: `cmd/cleaner/main.go:26-31`

```go
var allowedNamespaces = map[string]bool{
    "default": true,
    "dev":     true,
    "test":    true,
    "staging": true,
}
```

**影响**: Cleaner 无法在 `sandbox-system` 和 `sandbox-workspaces` 命名空间中运行。

**修复建议**:
```go
var allowedNamespaces = map[string]bool{
    "default":            true,
    "sandbox-system":     true,
    "sandbox-workspaces": true,
}
```

---

### 2.2 Exec 容器名称不匹配

**文件**: `manager-service/internal/k8s/exec.go:65-67`

**问题描述**:

```go
if opts.Container == "" {
    opts.Container = "runner" // default container name
}
```

但实际 Pod 创建时使用的容器名是 `sandbox`（见 `internal/config/types.go` 中的默认配置）。

**影响**: 所有 exec 操作将失败，返回 "container not found" 错误。

**修复建议**:
```go
if opts.Container == "" {
    opts.Container = "sandbox" // 与实际容器名一致
}
```

---

### 2.3 整数溢出导致退避计算错误

**文件**: `manager-service/internal/config/watch.go:400-409`

**问题描述**:

```go
func (w *Watcher) calculateBackoff() time.Duration {
    backoff := time.Duration(1<<uint(w.consecutiveFailures-1)) * time.Second
    // ...
}
```

当 `w.consecutiveFailures` 超过 63 时，`1<<uint(w.consecutiveFailures-1)` 会溢出。

**影响**: 在配置文件反复加载失败 63 次后，退避时间会异常重置为最小值，可能导致循环重试。

**修复建议**:
```go
func (w *Watcher) calculateBackoff() time.Duration {
    exp := w.consecutiveFailures - 1
    if exp > 60 { // 防止溢出
        exp = 60
    }
    backoff := time.Duration(1<<uint(exp)) * time.Second
    // ...
}
```

---

### 2.4 WebSocket 消息大小未限制

**文件**: `manager-service/internal/websocket/handler.go:542-546`

**问题描述**:

```go
data, err := base64.StdEncoding.DecodeString(payload.Data)
if err != nil {
    h.logger.Error("Failed to decode stdin data: %v", err)
    continue
}
// 没有检查 data 的大小
```

**影响**: 恶意客户端可以发送超大的 base64 字符串，导致内存耗尽（DoS 攻击）。

**修复建议**:
```go
const maxStdinSize = 10 * 1024 * 1024 // 10MB

// 解码前先检查大小
if len(payload.Data) > maxStdinSize*4/3 { // base64 编码后约为 4/3
    h.logger.Error("Stdin data too large: %d bytes", len(payload.Data))
    continue
}

data, err := base64.StdEncoding.DecodeString(payload.Data)
// ...
```

---

### 2.5 速率限制器内存泄漏

**文件**: `manager-service/internal/ratelimit/limiter.go:31-79`

**问题描述**:

```go
type Limiter struct {
    global     *rate.Limiter
    perIP      sync.Map // map[string]*rate.Limiter  // 从不清理
    perSession sync.Map // map[string]*rate.Limiter  // 从不清理
    cfg        *Config
    stopCleanup chan struct{}  // 定义了但从未使用
}
```

代码定义了 `CleanupInterval` 和 `stopCleanup`，但从未实现清理逻辑。每个唯一的 IP 或 session ID 都会创建新的 limiter 并永久驻留在内存中。

**影响**: 在有大量唯一 IP 或 session ID 的系统中，内存会无限增长直到 OOM。

**修复建议**: 实现清理逻辑，使用基于时间的淘汰策略，或使用带有内置淘汰功能的库。

---

## 三、重要问题 (Important - 建议修复)

### 3.1 Shell Bridge Client 竞态条件

**文件**: `manager-service/internal/shellbridge/client.go:139-154`

**问题描述**: `Close()` 方法只持有 `connMu` 进行关闭，但 `ExecCommand()` 和 `ReceiveOutput()` 可以并发调用，存在潜在的 use-after-free 风险。

**修复建议**: 使用状态机和适当的同步机制。

---

### 3.2 Pod IP 未验证

**文件**: `manager-service/internal/websocket/handler.go:295-305`

**问题描述**: 在 `WaitForPodReady()` 返回成功后，代码假设 Pod 有 IP，但 Pod 可能暂时失去 IP 或 `PodIP` 字段为空。

**修复建议**: 添加重试逻辑和 IP 格式验证。

---

### 3.3 MinIO 下载流资源泄漏

**文件**: `manager-service/internal/storage/client.go:72-84`

**问题描述**: 当 `DownloadSnapshot()` 的调用者在错误路径上未正确关闭返回的 ReadCloser 时，会造成资源泄漏。

**修复建议**: 在调用处确保错误时也关闭流。

---

### 3.4 直方图计数逻辑错误

**文件**: `manager-service/internal/observability/metrics.go:113-124`

**问题描述**: 直方图递增所有匹配的 bucket，而不是使用正确的累积计数。

**修复建议**: 按照 Prometheus 直方图语义实现累积计数。

---

### 3.5 JSON 编码错误未检查

**文件**: 多个位置

**问题描述**: 多处调用 `json.NewEncoder(w).Encode(resp)` 但未检查错误。

**影响**: 如果 JSON 编码失败（如管道损坏），客户端可能收到不完整的响应。

**修复建议**: 检查并处理编码错误。

---

## 四、中等问题 (Moderate - 可选修复)

### 4.1 请求体大小验证不完整

**文件**: `manager-service/internal/httpapi/handlers.go:338-342`

**问题描述**: ContentLength 检查可以通过不发送 Content-Length 头来绕过。

---

### 4.2 Context 传播不完整

**文件**: `manager-service/internal/files/tar.go:326-356`

**问题描述**: 下载处理中的 Context 取消未正确传播到 exec 操作。

---

### 4.3 存储操作缺少超时

**文件**: `manager-service/internal/storage/client.go`

**问题描述**: 存储客户端方法没有传递带超时的 context。

---

### 4.4 Session ID 提取脆弱

**文件**: `manager-service/internal/httpapi/handlers.go:614-621`

**问题描述**: `extractSessionId` 函数没有验证返回的 ID 是否有效。

---

### 4.5 Finalizer 处理器关闭不完整

**文件**: `manager-service/internal/app/app.go:219-220`

**问题描述**: finalizer handler goroutine 在关闭时可能不会立即退出。

---

## 五、低优先级问题

### 5.1 健康检查名称无意义

**文件**: `manager-service/internal/observability/health.go:96-101`

**问题描述**: `getCheckName()` 生成 "check_A"、"check_B" 等无意义的名称。

---

### 5.2 错误响应处理不一致

**文件**: `manager-service/internal/httpapi/errors.go:220-251`

**问题描述**: `APIError.Write()` 使用 `log.Printf` 而不是结构化日志。

---

### 5.3 TTL 比较使用严格不等式

**文件**: `manager-service/cmd/cleaner/main.go:148`

**问题描述**: 使用 `now.After(expiresAt)` 可能导致 Pod 比预期多存活 5 分钟。

---

## 六、问题统计

| 严重程度 | 数量 | 类别 |
|----------|------|------|
| **Critical** | 5 | Cleaner失效、Exec失败、整数溢出、DoS漏洞、内存泄漏 |
| **Important** | 5 | 竞态条件、资源泄漏、逻辑错误 |
| **Moderate** | 5 | 验证不完整、资源管理 |
| **Low** | 3 | 可用性问题 |

**总计**: 18 个问题

---

## 七、修复优先级建议

### 立即修复 (上线前必须)

1. **Cleaner Service 标签选择器** (2.1.1) - 否则 Pod 永远不会被清理
2. **Cleaner 命名空间白名单** (2.1.3) - 否则 Cleaner 无法运行
3. **Exec 容器名称** (2.2) - 否则所有 exec 操作失败
4. **整数溢出** (2.3) - 可能导致异常重试循环
5. **速率限制器内存泄漏** (2.5) - 生产环境会 OOM

### 尽快修复 (上线后)

1. **WebSocket 消息大小限制** (2.4) - DoS 防护
2. **Pod IP 验证** (3.2) - 提高稳定性
3. **MinIO 资源泄漏** (3.3) - 避免连接泄漏

### 下一版本修复

1. 其余 Important 和 Moderate 问题
2. Low 优先级问题

---

## 八、检查结论

mbos-sandbox 项目整体架构良好，代码组织清晰，测试覆盖率较高。但存在 **5 个 Critical 级别的问题** 需要在上线前修复，特别是 **Cleaner Service 完全无法工作** 的问题最为严重。

建议按优先级修复上述问题后，再进行一轮回归测试，确保所有修复正确且未引入新问题。

---

**检查人**: Claude Code Agent
**检查工具**: 代码审查 + 静态分析
**检查日期**: 2026-02-10
