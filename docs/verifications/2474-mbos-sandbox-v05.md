# mbos-sandbox V05 收口检查清单

## 概述

本检查清单是对 mbos-sandbox V05 版本的完整代码审查，重点查找可能影响系统稳定运行的明显bug和错误。

审查标准：**以能否稳定上线运行为目标，不吹毛求疵，避免范围蔓延和过度设计。**

---

## 严重问题 (P0 - 必须修复)

### 1. **缺失 K8sAdapter 实现**
**位置**: `manager-service/internal/app/app.go:106`
**问题**: `shellbridge.NewK8sAdapter(k8sClient)` 被调用，但该函数在 shellbridge 包中不存在
```go
k8sExecutor := shellbridge.NewK8sAdapter(k8sClient)
```
**影响**: 服务启动时编译失败
**建议**: 创建 `manager-service/internal/shellbridge/adapter.go` 文件，实现 `K8sAdapter` 类型

### 2. **shell-bridge 二进制文件未在容器中正确引用**
**位置**: `manager-service/internal/k8s/pods.go:383`
```go
container.Command = []string{"/shellb"}
```
**问题**: 引用了 `/shellb` 二进制，但需要确认 Dockerfile 中是否正确 COPY 了 shell-bridge 二进制到 `/shellb`
**影响**: Pod 启动失败，找不到 `/shellb` 命令
**建议**: 检查 `images/runner/Dockerfile` 确保 shell-bridge 二进制被正确复制

### 3. **snapshot 容器名称硬编码为 "runner"**
**位置**: `manager-service/internal/k8s/snapshot.go:31`
```go
err := c.Exec(execCtx, namespace, podName, "runner", []string{
```
**问题**: 容器名称硬编码为 "runner"，但配置中使用的是 "sandbox"
**影响**: Snapshot 失败，容器未找到
**建议**: 使用 `c.defaultContainer` 替代硬编码

### 4. **finalizer Stop 方法可能重复调用导致 panic**
**位置**: `manager-service/internal/ratelimit/limiter.go:165-177`
```go
func (l *Limiter) Stop() {
    l.stopped.Lock()
    defer l.stopped.Unlock()
    select {
    case <-l.stopCleanup:
        return
    default:
        close(l.stopCleanup)
    }
}
```
**问题**: 如果 `Stop()` 被多次调用，第二次会 panic (close closed channel)
**影响**: 程序崩溃
**建议**: 添加 `l.stopped` 标志检查，避免重复 close

### 5. **Shell-bridge server 中 session 的并发访问问题**
**位置**: `shell-bridge/internal/server/server.go:34-35`
```go
type Server struct {
    session *pty.Session
    mu      sync.Mutex // Protects concurrent access to session
}
```
**问题**: Session 在 WebSocket 连接关闭时被 Close，但如果多个连接同时操作 session，可能导致 session 被关闭后仍被使用
**影响**: 潜在的 panic
**建议**: 考虑使用连接级别的 session 而不是服务器级别

---

## 中等问题 (P1 - 建议修复)

### 6. **WebSocket handler 中的 goroutine 泄漏风险**
**位置**: `manager-service/internal/websocket/handler.go:99-110`
```go
defer func() {
    cleanupMu.Lock()
    alreadyCleaned := cleanupDone
    cleanupDone = true
    cleanupMu.Unlock()

    if agentThreadID != "" && isNewSession && !alreadyCleaned {
        h.sessionManager.Delete(agentThreadID)
        h.bufferManager.Delete(agentThreadID)
    }
}()
```
**问题**: 如果 `forwardIO` 在错误路径提前返回，cleanup 可能不会执行
**影响**: 会话和缓冲区泄漏
**建议**: 确保所有错误路径都正确执行清理

### 7. **session.Manager 没有实现完整的 session 生命周期管理**
**位置**: `manager-service/internal/session/manager.go`
**问题**: `Delete()` 方法被调用但 session 状态没有更新，可能导致已删除的 session 被误用
**影响**: 潜在的 use-after-free 问题
**建议**: 考虑在 Delete 前先标记 session 状态为 terminating

### 8. **K8s 客户端 CheckReady 只检查 Pod 列表，不够全面**
**位置**: `manager-service/internal/k8s/client.go:152-167`
```go
func (c *Client) CheckReady(ctx context.Context) error {
    _, err := c.clientset.CoreV1().Pods(c.namespace).List(timeoutCtx, metav1.ListOptions{Limit: 1})
    ...
}
```
**问题**: 只检查了能否列出 Pod，没有检查实际执行权限
**影响**: 服务启动后可能无法执行命令
**建议**: 添加一个简单的 exec 测试来验证完整功能

### 9. **Rate limiter 的 Allow 方法在并发修改时存在竞态**
**位置**: `manager-service/internal/ratelimit/limiter.go:117-124`
```go
entry, _ := l.perIP.LoadOrStore(ip, &limiterEntry{
    limiter:    rate.NewLimiter(rate.Limit(l.cfg.PerIPRPS), l.cfg.PerIPBurst),
    lastAccess: time.Now(),
})
limiterEntry := entry.(*limiterEntry)
limiterEntry.lastAccess = time.Now() // 竞态!
```
**问题**: `lastAccess` 字段的更新没有锁保护，可能导致脏读
**影响**: 清理逻辑可能误删正在使用的 limiter
**建议**: 使用 `atomic.Value` 或加锁保护 lastAccess

### 10. **Storage Client 的 SnapshotExists 错误处理不完整**
**位置**: `manager-service/internal/storage/client.go:95-108`
```go
errResp := minio.ToErrorResponse(err)
if errResp.Code == "NoSuchKey" {
    return false, nil
}
return false, err
```
**问题**: 对于非 MinIO 错误（如网络错误），`ToErrorResponse` 返回零值，`Code == ""` 会被误判
**影响**: 网络错误被误判为文件不存在
**建议**: 先检查 `err` 是否是 `minio.ErrorResponse` 类型

---

## 轻微问题 (P2 - 可选优化)

### 11. **PodName 生成的哈希碰撞风险**
**位置**: `manager-service/internal/k8s/pods.go:326-328`
```go
func PodName(sessionID string) string {
    hash := sha256.Sum256([]byte(sessionID))
    return "sbx-" + hex.EncodeToString(hash[:])[:10]
}
```
**问题**: 只使用了哈希的前 10 个字符（40 bits），存在碰撞可能
**影响**: 极低概率下不同 sessionID 可能生成相同的 podName
**建议**: 对于生产环境，考虑使用更长的哈希或添加冲突检测

### 12. **配置验证中 workdir 验证使用 filepath.Rel 可能被绕过**
**位置**: `manager-service/internal/config/validate.go:788-800`
```go
func (c *Config) ValidateWorkdir(workdir string) bool {
    for _, prefix := range c.Exec.Workdir.AllowedPrefixes {
        rel, err := filepath.Rel(prefix, workdir)
        if err == nil && !strings.HasPrefix(rel, "..") {
            return true
        }
    }
    return false
}
```
**问题**: `filepath.Rel` 对于 Windows 路径处理可能不正确
**影响**: Windows 环境下路径验证可能不准确
**建议**: 添加额外的路径规范化检查

### 13. **WebSocket Ping 间隔与 ReadTimeout 相同**
**位置**: `manager-service/internal/websocket/handler.go:513`
```go
case <-pingTicker.C:
    if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
```
**问题**: Ping ticker 是 30 秒，但 ReadTimeout 也是 30 秒，可能导致 timeout
**影响**: 连接可能被意外断开
**建议**: 将 ping 间隔设置为 ReadTimeout 的一半（如 15 秒）

### 14. **Finalizer 中 snapshot 重试没有指数退避上限**
**位置**: `manager-service/internal/finalizer/handler.go:231-268`
```go
for attempt := 1; attempt <= maxSnapshotRetries; attempt++ {
    ...
    select {
    case <-time.After(backoff):
    }
    backoff *= 2  // 无限增长!
}
```
**问题**: backoff 没有上限，3 次重试可能导致很长的等待
**影响**: Pod 删除可能被长时间阻塞
**建议**: 添加最大 backoff 限制

### 15. **Buffer Manager 的 Delete 不是幂等的**
**位置**: `manager-service/internal/buffer/manager.go:40-44`
```go
func (m *Manager) Delete(agentThreadID string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.buffers, agentThreadID)
}
```
**问题**: 多次调用 Delete 没有问题，但返回值应该指示是否实际删除了
**影响**: 调用者无法知道是否真正删除了资源
**建议**: 返回 bool 表示是否实际删除

---

## 潜在风险 (需关注)

### 16. **Shell-bridge WebSocket 客户端没有设置 Pong 处理**
**位置**: `manager-service/internal/shellbridge/client.go`
**问题**: 客户端发送 Ping 但没有处理 Pong 回调，无法检测连接存活状态
**影响**: 无法及时检测断开的连接
**建议**: 设置 `conn.SetPongHandler`

### 17. **Session Manager 的 cleanup 间隔是硬编码的**
**位置**: `manager-service/internal/app/app.go:224`
```go
go mgr.sessionManager.StartCleanup(mgr.ctx, 5*time.Minute)
```
**问题**: 清理间隔硬编码，不可配置
**影响**: 无法根据实际需求调整
**建议**: 添加到配置文件

### 18. **HTTP 服务器 ReadHeaderTimeout 使用默认值可能导致慢速攻击**
**位置**: `manager-service/internal/app/app.go:380-395`
**问题**: 虽然配置了 ReadHeaderTimeout，但默认可能太长
**影响**: 可能被慢速请求占用连接
**建议**: 确认配置值是否合理（建议 5-10 秒）

### 19. **Exec 中的 ExitCode 提取可能失败但没有明确的错误提示**
**位置**: `manager-service/internal/exec/wrapper.go` (未在本次审查中完整查看)
**问题**: 提取 exit code 失败时返回 -1，调用者难以区分是没有执行还是提取失败
**影响**: 调试困难
**建议**: 添加日志记录

### 20. **Storage Client UploadSnapshot 使用 size=-1**
**位置**: `manager-service/internal/finalizer/handler.go:285`
```go
if err := h.storageClient.UploadSnapshot(ctx, key, snapshot, -1); err != nil {
```
**问题**: size=-1 可能导致某些存储实现无法正确分块上传
**影响**: 大文件上传可能失败或效率低
**建议**: 如果知道大小，传递正确的值

---

## 建议的修复优先级

1. **立即修复 (P0)**: 问题 1-5，这些是会导致服务无法正常运行或崩溃的严重问题
2. **尽快修复 (P1)**: 问题 6-10，这些可能导致资源泄漏或功能异常
3. **计划修复 (P2)**: 问题 11-15，这些是边界情况下的潜在问题
4. **持续关注**: 问题 16-20，这些是架构层面的改进建议

---

## 测试建议

1. **单元测试覆盖**: 为 K8sAdapter、Rate limiter 等关键组件添加完整的单元测试
2. **集成测试**: 添加端到端的 WebSocket 会话创建、执行、断开重连测试
3. **压力测试**: 测试高并发下的 session 创建和销毁
4. **故障恢复测试**: 模拟 K8s API 不可用、存储不可用等场景

---

## 总结

mbos-sandbox V05 整体架构设计合理，代码组织清晰。主要问题集中在：

1. **缺失的 K8sAdapter 实现** - 阻塞性问题，必须立即解决
2. **硬编码的容器名称** - 导致功能异常
3. **并发安全问题** - 可能导致竞态和资源泄漏
4. **错误处理不完整** - 边界情况下可能出现意外行为

建议优先修复 P0 级别的问题后再进行上线部署。其他问题可以在后续迭代中逐步改进。
