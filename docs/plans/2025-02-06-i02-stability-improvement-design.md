# mbos-sandbox v1 I02 完整稳定性改进设计文档

**项目**: mbos-sandbox v1
**改进编号**: I02
**设计日期**: 2025-02-06
**目标**: 修复收口检查中发现的全部 37 个问题，达到生产稳定运行标准

---

## 一、架构边界定义

### 1.1 网络架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           外部网络                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   End User                                                                  │
│        │                                                                    │
│        ▼                                                                    │
│  ┌────────────────────────────────────────────────────────────────────┐     │
│  │                    对外服务                       │     │
│  │  • End User 鉴权                                                      │     │
│  │  • End User 速率限制                                                  │     │
│  │  • 使用 Sandbox Service Key 调用 mbos-sandbox                        │     │
│  └────────────────────────────────────────────────────────────────────┘     │
│                              │                                               │
│                              │ Service Key (由系统管理员手动签发)            │
│                              ▼                                               │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                   内部网络                      │   │
│  │  ┌──────────────────────────────────────────────────────────────┐   │   │
│  │  │              mbos-sandbox manager-service                     │   │   │
│  │  │  • 验证 Service Key（环境变量配置）                            │   │   │
│  │  │  • 不关心 End User                                            │   │   │
│  │  │  • 不做 End User 级别的鉴权和速率限制                          │   │   │
│  │  │  • Service Key 由系统管理员手动配置（无需自动签发逻辑）        │   │   │
│  │  └──────────────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 服务职责边界

| 职责 | Sandbox Service | 对外服务 |
|------|----------------|----------|
| End User 鉴权 | ❌ | ✅ |
| End User 速率限制 | ❌ | ✅ |
| Service Key 验证 | ✅ | ❌ |
| 会话所有权验证 | ❌（内部网络信任）| ✅ |
| Service Key 签发 | ❌（系统管理员手动）| ❌ |

### 1.3 Service Key 配置

- **配置方式**: 环境变量 `SERVICE_KEYS`
- **配置主体**: 系统管理员
- **验证逻辑**: 简单的 API Key 验证（`X-Service-Key` header）
- **启动要求**: `SERVICE_KEYS` 不能为空，否则服务启动失败

---

## 二、问题分类与优先级

### 2.1 问题汇总（来自 v01-mbos-sandbox-closing-review.md）

| 类别 | 数量 | 说明 |
|------|------|------|
| 安全漏洞 | 10 | 认证绕过、命令注入、凭据暴露等 |
| 资源泄漏 | 12 | Goroutine泄漏、内存泄漏、连接泄漏 |
| 逻辑错误 | 14 | 清理缺失、竞态条件、panic风险 |
| 脚本错误 | 7 | 未定义变量、错误处理缺失 |

### 2.2 立即修复（上线前必须）

1. WebSocket 认证绕过 (1.1)
2. 会话授权绕过 (1.2) → 简化为：WebSocket 必须验证 Service Key
3. 命令注入 (1.3)
4. Goroutine 泄漏 (2.1, 2.2, 2.3)
5. 会话清理缺失 (3.1, 3.3)
6. 最终处理程序删除失败 (3.4)
7. offline.sh 未定义变量 (4.1)
8. 存储客户端 Panic (3.13)

### 2.3 高优先级（上线后尽快）

1. 最终处理程序关闭问题 (2.4)
2. 缓冲区泄漏 (2.8)
3. 开发模式认证禁用 (1.5)
4. WebSocket 速率限制 (1.6) → 简化为：保留现有基础限制
5. 调试端点凭据暴露 (1.4)

### 2.4 中优先级（计划修复）

1. HTTP 请求体关闭 (2.5)
2. 错误处理改进 (3.9, 3.10, 3.11)
3. 脚本错误处理 (4.2-4.7)
4. 日志信息泄露 (1.9)
5. TLS 验证 (1.10)

---

## 三、设计方案

### 3.1 认证修复（安全漏洞）

#### 3.1.1 WebSocket 认证绕过修复

**问题**: `/ws` 端点完全绕过认证中间件

**修复方案**:
```go
// manager-service/internal/app/app.go

// 修改前
mux.Handle("/ws", m.wsHandler)

// 修改后
// WebSocket 需要从 URL 参数中获取 service key
// ws://host/ws?service_key=xxx
mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
    // 验证 service key
    if !m.authValidator.ValidateServiceKey(r.URL.Query().Get("service_key")) {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }
    // 调用 WebSocket handler
    m.wsHandler.ServeHTTP(w, r)
})
```

#### 3.1.2 Service Key 必填验证

**问题**: `SERVICE_KEYS` 为空时静默禁用所有认证

**修复方案**:
```go
// manager-service/internal/auth/servicekey.go

// 修改前
if len(keys) == 0 {
    log.Println("No service keys configured, allowing all requests")
    return &Validator{keys: nil}
}

// 修改后
if len(keys) == 0 {
    return nil, fmt.Errorf("SERVICE_KEYS cannot be empty: service requires authentication")
}
```

#### 3.1.3 调试端点凭据暴露修复

**问题**: `/debug/config` 端点返回包含存储凭据的完整配置

**修复方案**:
```go
// manager-service/internal/app/app.go

// 修改前
func (m *Manager) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(m.config)
}

// 修改后
func (m *Manager) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
    // 创建安全配置副本，移除敏感信息
    safeConfig := map[string]interface{}{
        "k8s_namespace": m.config.K8sNamespace,
        "storage_endpoint": m.config.StorageEndpoint,
        "storage_bucket": m.config.StorageBucket,
        // 不返回 storage_access_key, storage_secret_key
    }
    json.NewEncoder(w).Encode(safeConfig)
}
```

### 3.2 资源泄漏修复

#### 3.2.1 Context 传播模式

**核心原则**: 所有 long-running 操作必须：
1. 接受 `context.Context` 参数
2. 使用 `context.WithTimeout` 设置超时
3. Goroutine 内部监听 `ctx.Done()` 并退出

**标准模式**:
```go
func doWork(ctx context.Context) error {
    // 设置超时
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    resultCh := make(chan error, 1)

    // 启动 worker goroutine
    go func() {
        // worker 必须检查 ctx.Done()
        select {
        case <-ctx.Done():
            resultCh <- ctx.Err()
        case result := <-workCh:
            resultCh <- result
        }
    }()

    // 等待结果或取消
    select {
    case <-ctx.Done():
        return ctx.Err()
    case err := <-resultCh:
        return err
    }
}
```

#### 3.2.2 SnapshotWorkspace Goroutine 泄漏修复

**问题**: Goroutine 不检查 `ctx.Done()`，调用者取消后继续运行

**修复方案**:
```go
// manager-service/internal/k8s/snapshot.go

func (c *Client) SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error) {
    pr, pw := io.Pipe()

    go func() {
        defer pw.Close()

        // 检查上下文取消
        select {
        case <-ctx.Done():
            pw.CloseWithError(ctx.Err())
            return
        default:
        }

        // 执行 tar 命令
        execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
        defer cancel()

        // ... exec 逻辑
    }()

    return pr, nil
}
```

#### 3.2.3 最终处理程序关闭问题修复

**问题**: 最终处理程序使用 `context.Background()` 启动，无法优雅停止

**修复方案**:
```go
// manager-service/internal/app/app.go

// 修改前
go m.finalizerHandler.Start(context.Background())

// 修改后
go m.finalizerHandler.Start(m.ctx) // m.ctx 在 shutdown 时被 cancel
```

#### 3.2.4 会话清理缺失修复

**问题 1**: WebSocket 关闭时不清理会话和缓冲区

**修复方案**:
```go
// manager-service/internal/websocket/handler.go

func (h *Handler) handleCreate(wsConn *websocket.Conn, createMsg Message) {
    // ... 创建会话 ...

    // 添加 defer 清理
    defer func() {
        h.sessionManager.Delete(agentThreadID)
        h.bufferManager.Delete(agentThreadID)
    }()

    // ... 处理消息 ...
}
```

**问题 2**: 会话管理器没有后台清理过期会话

**修复方案**:
```go
// manager-service/internal/session/manager.go

func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            m.cleanupExpired()
        }
    }
}

func (m *Manager) cleanupExpired() {
    m.mu.Lock()
    defer m.mu.Unlock()

    now := time.Now()
    for id, session := range m.sessions {
        if session.IsExpired(now) {
            delete(m.sessions, id)
        }
    }
}
```

### 3.3 逻辑错误修复

#### 3.3.1 存储客户端 Panic 修复

**问题**: `minio.ToErrorResponse(err)` 可能返回 `nil`，导致 panic

**修复方案**:
```go
// manager-service/internal/storage/client.go

func (c *Client) UploadSnapshot(ctx context.Context, key string, reader io.Reader, size int64) error {
    // ... 上传逻辑 ...

    if err != nil {
        errResp := minio.ToErrorResponse(err)
        if errResp != nil {
            // 安全访问 errResp.Code
            return fmt.Errorf("upload failed: %s (code: %s)", errResp.Message, errResp.Code)
        }
        // 非 minio 错误类型
        return fmt.Errorf("upload failed: %w", err)
    }

    return nil
}
```

#### 3.3.2 最终处理程序删除失败修复

**问题**: 快照失败后仍然删除最终处理程序，可能导致孤立 pod

**修复方案**:
```go
// manager-service/internal/finalizer/handler.go

func (h *Handler) processPod(ctx context.Context, pod *v1.Pod) error {
    // ... 快照逻辑 ...

    if err := h.snapshotWorkspace(snapshotCtx, podName, workspaceID, projectID, agentThreadID); err != nil {
        log.Printf("Finalizer: snapshot failed for pod %s: %v", podName, err)
        // 标记 pod 为快照失败，但不阻止删除
        // 可以考虑添加 annotation 标记失败状态
    }

    // 移除最终处理程序（即使快照失败也要移除，否则 pod 会卡住）
    if err := h.k8sClient.RemoveFinalizer(ctx, h.namespace, podName, SnapshotFinalizer); err != nil {
        return fmt.Errorf("failed to remove finalizer: %w", err)
    }

    return nil
}
```

#### 3.3.3 双重会话创建竞态条件修复

**问题**: 检查并创建模式不是原子的

**修复方案**:
```go
// manager-service/internal/websocket/handler.go

func (m *Manager) GetOrCreate(agentThreadID string) (*Session, bool, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 先检查
    if session, exists := m.sessions[agentThreadID]; exists {
        return session, false, nil
    }

    // 创建新会话（持有锁期间，原子操作）
    session := NewSession(agentThreadID, m.defaultTTL)
    m.sessions[agentThreadID] = session
    return session, true, nil
}
```

### 3.4 脚本错误修复

#### 3.4.1 offline.sh 未定义变量修复

**问题**: 引用未定义的 `$local_gc`, `$gc_ver`, `$tar_gc`

**修复方案**:
```bash
# scripts/lib/offline.sh

# 添加缺失的变量定义
local_gc="go"
gc_ver="1.21"
tar_gc="tar"

# 或移除这些未使用的变量引用
```

### 3.5 测试策略

#### 3.5.1 单元测试

为每个修复添加单元测试：
- 认证中间件测试
- Context 取消测试
- 会话清理测试
- 错误处理测试

#### 3.5.2 集成测试

添加端到端集成测试：
- WebSocket 连接完整流程
- Pod 创建和删除流程
- 快照上传和下载流程
- 认证失败场景

---

## 四、实施计划

### 4.1 修复顺序

```
Phase 1: 安全基础（阻塞发布）
├── 1.1 Service Key 必填验证
├── 1.2 WebSocket 认证
├── 1.3 调试端点凭据隐藏
└── 1.4 命令注入修复

Phase 2: 资源泄漏（影响稳定性）
├── 2.1 Context 传播模式建立
├── 2.2 SnapshotWorkspace 修复
├── 2.3 最终处理程序修复
├── 2.4 会话清理机制
└── 2.5 缓冲区清理机制

Phase 3: 逻辑错误（影响正确性）
├── 3.1 Panic 风险修复
├── 3.2 竞态条件修复
├── 3.3 错误处理改进
└── 3.4 最终处理程序重试

Phase 4: 脚本和收尾
├── 4.1 脚本变量修复
├── 4.2 错误处理完善
└── 4.3 集成测试补充
```

### 4.2 验收标准

- [ ] 所有单元测试通过
- [ ] 集成测试通过
- [ ] 冒烟测试通过
- [ ] 无内存泄漏（运行压力测试验证）
- [ ] 无 goroutine 泄漏（运行 `runtime.NumGoroutine()` 监控）
- [ ] 服务启动时 `SERVICE_KEYS` 为空会失败
- [ ] WebSocket 连接必须提供有效 service key

---

## 五、技术决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| 认证方式 | 静态 Service Key | 内部网络，系统管理员手动配置 |
| 会话所有权 | 无需验证 | 内部网络信任，由对外服务控制 |
| Context 策略 | 传播 + 超时 | Go 最佳实践，防御深度 |
| 向后兼容 | 无需考虑 | Greenfield 项目 |
| 测试策略 | 单元 + 集成 | 确保正确性和端到端流程 |
| 特性开关 | 不需要 | 直接修改，依赖测试验证 |

---

## 六、风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Context 传播引入新 bug | 高 | 充分测试，代码审查 |
| WebSocket 认证破坏现有客户端 | 低 | 无现有部署 |
| 会话清理影响性能 | 低 | 定期清理，批量操作 |
| 脚本修改影响部署 | 中 | 充分测试部署流程 |

---

**文档版本**: 1.0
**最后更新**: 2025-02-06
**状态**: 设计完成，待审批
