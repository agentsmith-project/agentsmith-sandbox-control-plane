# mbos-sandbox v03 收口检查报告

**检查日期**: 2026-02-09
**任务编号**: 8332-mbos-sandbox-v03
**检查范围**: mbos-sandbox 完整代码库
**检查标准**: 上线稳定运行标准（非吹毛求疵）

---

## 概述

本次检查对 mbos-sandbox 项目进行了全面的代码审查，涵盖：
- Manager 服务核心逻辑
- Shell-bridge 集成
- 存储层（MinIO/S3 快照）
- Kubernetes 集成
- 认证与安全
- Cleaner 服务与 TTL
- 错误处理
- 配置管理
- 测试覆盖率

**总计发现**: 28 个问题
- 严重问题 (Critical): 12 个
- 重要问题 (Important): 14 个
- 次要问题 (Minor): 2 个

---

## 一、严重问题 (Critical Issues)

### 1.1 认证绕过 - 无服务密钥时允许所有请求
**文件**: `manager-service/internal/auth/middleware.go:30-35`
**置信度**: 95%

**问题描述**:
当没有配置服务密钥时（`validator.HasKeys()` 返回 false），中间件允许所有请求无需认证通过。这是一个严重的安全漏洞。

**影响**: 如果 SERVICE_KEYS 环境变量配置错误、为空或验证器初始化失败，整个 API 将完全公开访问。

**建议修复**:
```go
// 移除绕过逻辑，或通过单独的配置标志显式启用
if !validator.HasKeys() && !m.cfg.AllowUnauthenticated {
    log.Printf("Auth: no service keys configured, rejecting request")
    http.Error(w, "Service not configured", http.StatusInternalServerError)
    return
}
```

---

### 2.1 Shell-bridge 客户端 nil 指针解引用
**文件**: `manager-service/internal/shellbridge/client.go:58-66`
**置信度**: 95%

**问题描述**:
如果 `Connect()` 未调用或失败，`c.conn` 将为 nil，调用 `ExecCommand()` 会导致 nil 指针解引用 panic。

**影响**: 应用程序崩溃而非优雅处理错误。

**建议修复**:
```go
func (c *Client) ExecCommand(ctx context.Context, shell, command string, env []string) error {
    if c.conn == nil {
        return errors.New("client not connected")
    }
    // ... 其余代码
}
```

---

### 3.1 Shell-bridge 二进制帧解析整数溢出
**文件**: `manager-service/internal/shellbridge/frame.go:38-42`
**置信度**: 90%

**问题描述**:
将 `uint32` 转换为 `int` 时可能溢出。如果 `frame.Length` 是接近 `2^32-1` 的大值，转换为有符号 int 可能导致负值，使边界检查错误通过。

**影响**: 攻击者可以发送恶意帧绕过边界检查，导致越界内存访问或 panic。

**建议修复**:
```go
const maxFrameSize = 10 * 1024 * 1024 // 10MB max
if frame.Length > maxFrameSize {
    return nil, errors.New("frame too large")
}
if len(data) < 5+int(frame.Length) {
    return nil, errors.New("incomplete frame data")
}
frame.Data = data[5 : 5+int(frame.Length)]
```

---

### 4.1 Shell-bridge 服务器实现完全缺失
**文件**: `images/runner/Dockerfile:1-6`
**置信度**: 95%

**问题描述**:
Dockerfile 引用 `shell-bridge/` 目录和 `./cmd/shellb` 源码，但该目录在仓库中不存在。Shell-bridge 服务器实现完全缺失。

**影响**: Docker 构建会在第 4 行失败。整个 shell-bridge 功能无法使用。

**建议修复**: 需要创建或从外部源获取 shell-bridge 服务器实现。

---

### 5.1 WebSocket I/O 转发实现不完整
**文件**: `manager-service/internal/websocket/handler.go:442-446, 508-510`
**置信度**: 95%

**问题描述**:
`forwardIO` 函数包含 I/O 转发的占位符。stdin 数据被接收但从未转发到 shell-bridge，且没有从 shell-bridge 启动 stdout/stderr 流。`outputChan` 和 `errorChan` 被创建但从未写入。

**影响**: WebSocket 连接建立但不实际转发任何 I/O。系统对交互式 shell 会话不可用。

**建议修复**: 需要完整实现：
1. 连接到 shell-bridge WebSocket
2. 转发 stdin 数据到 shell-bridge
3. 从 shell-bridge 读取二进制帧
4. 将输出发送到 `outputChan`

---

### 6.1 Session Manager 并发竞争条件
**文件**: `manager-service/internal/session/manager.go:26-48, 85-111`
**置信度**: 95%

**问题描述**:
虽然 `GetOrCreate` 是原子且正确同步的，但 `Create()` 与 `GetOrCreate()` 并发调用时存在竞争。`Create()` 在创建前不检查现有会话。

**影响**: 可能导致重复会话、数据丢失和会话状态不一致。

**建议修复**: 在 `Create()` 中添加会话存在检查。

---

### 7.1 环形缓冲区无限制内存分配
**文件**: `manager-service/internal/buffer/ring.go:7-11, 29-41`
**置信度**: 80%

**问题描述**:
环形缓冲区限制消息数量（默认 10,000），但没有限制单个消息的大小。`Message.Data` 字段可以任意大。

**影响**: 恶意客户端可以发送多兆字节负载的消息，即使消息数量很少也会导致内存耗尽。

**建议修复**: 添加最大消息大小限制并拒绝/截断超过限制的消息。

---

### 8.1 Finalizer Handler Goroutine 未正确管理
**文件**: `manager-service/internal/finalizer/handler.go:84-102`
**置信度**: 90%

**问题描述**:
`Start()` 方法启动 goroutine 但没有提供等待其完成的方法。没有 `WaitGroup` 或同步原语，无法优雅关闭 goroutine。

**影响**: 程序终止时可能无法确保 goroutine 已退出。

**建议修复**: 添加 `sync.WaitGroup` 到 `Handler` 并提供 `Shutdown()` 方法。

---

### 9.1 未使用的通道导致潜在死锁
**文件**: `manager-service/internal/websocket/handler.go:398-400, 457-506`
**置信度**: 85%

**问题描述**:
`outputChan` 和 `errorChan` 被创建但从未发送数据。如果有任何代码尝试从这些通道接收，将永久阻塞。

**影响**: 实现不完整，指示功能缺失，可能导致死锁。

**建议修复**: 删除未使用的通道或实现实际流式执行并发送到这些通道。

---

### 10.1 Shell-bridge ReceiveOutput 静默错误处理
**文件**: `manager-service/internal/shellbridge/client.go:92-96`
**置信度**: 85%

**问题描述**:
格式错误的帧被静默跳过（`continue`）。如果协议失步或接收到损坏数据，客户端将无限循环，永不返回错误。

**影响**: 无限循环消耗 CPU，调用者永久挂起，协议失步未被检测。

**建议修复**: 返回错误而非继续：
```go
if msgType == websocket.BinaryMessage {
    frame, err := ParseBinaryFrame(data)
    if err != nil {
        return nil, fmt.Errorf("failed to parse binary frame: %w", err)
    }
    return &Output{Type: byte(frame.Type), Data: frame.Data}, nil
}
```

---

### 11.1 Shell-bridge Client 忙等待循环
**文件**: `manager-service/internal/shellbridge/client.go:76-101`
**置信度**: 90%

**问题描述**:
select 语句中的 `default:` 导致无数据可用时的忙等待循环。select 会立即落入 default 并再次循环，等待数据时消耗 100% CPU。

**影响**: 过度 CPU 使用，性能差，移动设备耗电。

**建议修复**: 移除带 default 的 select，使用带超时的读取：
```go
c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
msgType, data, err := c.conn.ReadMessage()
if err != nil {
    if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
        continue
    }
    // 处理真实错误
}
```

---

### 12.1 WebSocket 清理标志非原子操作
**文件**: `manager-service/internal/websocket/handler.go:86-101`
**置信度**: 85%

**问题描述**:
`cleanupDone` 标志用于防止双重清理，但只在 defer 函数中检查。标志不是原子的，存在潜在竞争。

**影响**: 清理可能运行多次。

**建议修复**: 使用 `sync.Mutex` 保护清理操作或使清理幂等。

---

## 二、重要问题 (Important Issues)

### 1.2 WebSocket 认证通过查询参数（安全隐患）
**文件**: `manager-service/internal/app/app.go:306-319`
**置信度**: 90%

**问题描述**:
WebSocket 认证使用查询参数（`service_key`），可能被记录在服务器访问日志、代理日志和浏览器历史中。

**影响**: 服务密钥泄露在日志中，潜在时序攻击，凭据保存在浏览器历史中。

**建议修复**: 使用头部进行认证，实现常量时间比较，确保密钥永不记录。

---

### 2.2 Cleaner 无命名空间验证
**文件**: `manager-service/cmd/cleaner/main.go:22, 52-54`
**置信度**: 85%

**问题描述**:
Cleaner 通过命令行标志接受任何命名空间而不验证。

**影响**: 如果配置错误，cleaner 可能删除生产命名空间中的 pod。

**建议修复**: 根据命名空间白名单验证，需要显式环境变量配置。

---

### 3.2 TTL 解析错误处理跳过清理
**文件**: `manager-service/cmd/cleaner/main.go:73-79`
**置信度**: 85%

**问题描述**:
TTL 解析失败时，pod 被跳过。带有格式错误过期注释的 pod 永远不会被清理。

**影响**: 带有无效注释的孤立 pod 无限期累积，消耗资源。

**建议修复**: 实现回退策略（例如，删除注释超过 X 天的 pod 或标记供手动审查）。

---

### 4.2 Finalizer 快照失败继续 Pod 删除
**文件**: `manager-service/internal/finalizer/handler.go:143-147`
**置信度**: 85%

**问题描述**:
快照创建失败时，仍然移除 finalizer，允许 pod 删除而不保留工作区数据。

**影响**: Pod 终止期间快照失败会导致用户工作区数据永久丢失。

**建议修复**: 为快照实现重试逻辑，或要求对快照失败进行手动干预。

---

### 5.2 存储客户端缺失错误验证
**文件**: `manager-service/internal/storage/client.go:88`
**置信度**: 85%

**问题描述**:
`DeleteSnapshot` 静默忽略错误而没有适当处理或日志记录。

**影响**: 难以调试，操作失败但无日志。

**建议修复**: 添加验证和日志记录。

---

### 6.2 Shell-bridge Client 字段无 Mutex 保护
**文件**: `manager-service/internal/shellbridge/client.go:20-25, 39-46, 58-66`
**置信度**: 85%

**问题描述**:
`Client.conn` 字段从多个 goroutine 访问而没有同步。

**影响**: 未定义行为，潜在崩溃，数据损坏。

**建议修复**: 添加 mutex 保护 `conn` 字段。

---

### 7.2 下载处理程序缺失 Body 关闭
**文件**: `manager-service/internal/httpapi/handlers.go:302-364`
**置信度**: 90%

**问题描述**:
`HandleUpload` 函数不关闭 `r.Body`，不像其他处理程序。

**影响**: 潜在资源泄漏。

**建议修复**: 在函数开头添加 `defer r.Body.Close()`。

---

### 8.2 WithTimeout 未返回 cancel 函数
**文件**: `manager-service/internal/k8s/client.go:234-236`
**置信度**: 85%

**问题描述**:
`WithTimeout` 方法返回带超时的 context 但不返回 cancel 函数给调用者。

**影响**: 即使操作快速完成，与 context 关联的定时器也会保持到超时过期。

**建议修复**: 返回 context 和 cancel 函数：`return context.WithTimeout(parent, c.timeout)`

---

### 9.2 PatchActivity 缺失错误处理
**文件**: `manager-service/internal/k8s/pods.go:307`
**置信度**: 85%

**问题描述**:
`json.Marshal` 错误被静默忽略。

**影响**: 潜在静默失败，patch 发送不完整/无效数据。

**建议修复**: 检查并返回错误。

---

### 10.2 Config 验证缺失上限
**文件**: `manager-service/internal/config/validate.go:234-243, 247-256`
**置信度**: 85%

**问题描述**:
QPS 和 Burst 验证只检查负值，不检查合理上限。

**影响**: 极高的 QPS/Burst 值可能使 Kubernetes API 服务器不堪重负。

**建议修复**: 添加上限（例如 QPS ≤ 1000）。

---

### 11.2 下载函数上下文取消竞争
**文件**: `manager-service/internal/files/tar.go:355-359`
**置信度**: 85%

**问题描述**:
下载 goroutine 在两个 select 检查之间未正确处理上下文取消。

**影响**: 存在竞争条件。

**建议修复**: 在 exec 后添加额外错误检查。

---

### 12.2 WebSocket Handler MarshalJSON 静默错误
**文件**: `manager-service/internal/websocket/handler.go:560-569`
**置信度**: 85%

**问题描述**:
JSON 编码失败时记录错误但返回空 JSON。

**影响**: 客户端接收消息类型的无效数据。

**建议修复**: 返回 JSON 错误消息而非空对象。

---

### 13.2 Session GetOrCreate 创建僵尸会话
**文件**: `manager-service/internal/session/manager.go:85-111`
**置信度**: 80%

**问题描述**:
`GetOrCreate` 方法创建具有最小初始化的"僵尸"会话，但在错误返回路径中未清楚记录。

**影响**: 如果调用者期望完全初始化的会话，将获得部分初始化的会话。

**建议修复**: 考虑添加验证方法检查会话是否完全初始化。

---

### 14.2 Buffer Manager Delete 期间竞争
**文件**: `manager-service/internal/buffer/manager.go:20-37`
**置信度**: 80%

**问题描述**:
虽然 `GetOrCreate` 和 `Delete` 单独是线程安全的，但存在一个竞争，一个 goroutine 调用 `GetOrCreate()` 而另一个调用 `Delete()`。

**影响**: goroutine 可能获得立即被删除的缓冲区引用。

**建议修复**: 这是设计限制。使用引用计数或记录调用者必须确保对已删除缓冲区无并发访问。

---

## 三、次要问题 (Minor Issues)

### 1.3 Debug Config 端点暴露
**文件**: `manager-service/internal/app/app.go:304`
**置信度**: 80%

**问题描述**:
`/debug/config` 端点注册时没有认证中间件。

**影响**: 如果端点可访问，敏感配置信息可能暴露。

**建议修复**: 对 debug 端点应用认证中间件。

---

### 2.3 Config Clone 不验证深拷贝
**文件**: `manager-service/internal/config/load.go:217-230`
**置信度**: 82%

**问题描述**:
Clone 方法使用 YAML marshal/unmarshal 但不验证所有字段都保留。

**影响**: 某些字段可能无法通过 YAML 正确往返。

**建议修复**: 克隆后添加验证。

---

## 四、测试覆盖率问题

### 4.1 缺失配置边缘情况测试
**文件**: `manager-service/internal/config/validate_test.go`

**缺失测试**:
- WebSocket 配置验证
- 存储配置（端点、凭据验证）
- 缓冲区容量边界
- 极端超时值（例如 24 小时）
- HandshakeTimeout 解析错误

### 4.2 缺失 Watcher 并发访问测试
**文件**: `manager-service/internal/config/watch_test.go`

**缺失测试**:
- 尽管设计用于并发使用，但没有并发访问测试

---

## 五、修复优先级建议

### P0 - 必须修复（阻塞上线）
1. Shell-bridge 服务器实现缺失 (4.1)
2. Shell-bridge 客户端 nil 指针解引用 (2.1)
3. 认证绕过漏洞 (1.1)
4. WebSocket I/O 转发实现不完整 (5.1)

### P1 - 高优先级（影响稳定性）
1. Session Manager 并发竞争 (6.1)
2. Shell-bridge 二进制帧解析整数溢出 (3.1)
3. Finalizer Handler Goroutine 管理 (8.1)
4. Shell-bridge ReceiveOutput 静默错误 (10.1)
5. Shell-bridge 忙等待循环 (11.1)

### P2 - 中优先级（建议修复）
1. 环形缓冲区内存限制 (7.1)
2. WebSocket 清理标志竞争 (12.1)
3. WebSocket 认证查询参数 (1.2)
4. Cleaner 命名空间验证 (2.2)
5. TTL 解析错误处理 (3.2)
6. Finalizer 快照失败处理 (4.2)

### P3 - 低优先级（改进项）
1. 其余所有重要和次要问题

---

## 六、总结

mbos-sandbox 项目整体代码质量良好，架构清晰，但存在一些需要在上线前修复的关键问题：

**优势**:
- 清晰的模块化架构
- 良好的测试基础设施
- 合理的 Kubernetes 集成
- 生产就绪的配置管理

**需要关注的领域**:
1. Shell-bridge 功能未完成实现
2. 认证系统安全性需要加强
3. 并发安全需要改进
4. 错误处理需要更完整

**上线建议**:
- 修复所有 P0 和 P1 问题后可以上线
- P2 问题可在后续版本修复
- P3 问题可根据情况安排

---

**检查完成日期**: 2026-02-09
**检查工具**: Claude Code with feature-dev:code-reviewer agent
