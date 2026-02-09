# mbos-sandbox v01 收口检查报告

**项目**: mbos-sandbox v1
**检查日期**: 2025-02-06
**检查范围**: 全项目代码审查，重点关注影响生产稳定运行的Bug和错误
**审查标准**: 能否上线稳定运行（非吹毛求疵，不过度设计）

---

## 执行摘要

本次审查对 mbos-sandbox v1 进行了全面的代码检查，共发现 **37个需要修复的问题**，分为以下几类：

| 类别 | 严重程度 | 数量 | 说明 |
|------|----------|------|------|
| 安全漏洞 | 关键 | 10 | 认证绕过、命令注入、权限提升等 |
| 资源泄漏 | 高 | 12 | Goroutine泄漏、内存泄漏、连接泄漏 |
| 逻辑错误 | 高 | 8 | 最终处理理器失败、会话清理缺失、竞态条件 |
| 脚本错误 | 中 | 7 | 未定义变量、错误处理缺失 |

**建议**: 在上线生产环境前，必须修复所有"关键"级别的问题，并评估"高"级别问题的修复优先级。

---

## 一、安全漏洞 (关键 - 必须修复)

### 1.1 WebSocket 端点完全绕过认证
**严重程度**: 关键 (100)
**位置**: `manager-service/internal/app/app.go:293`

**问题描述**:
WebSocket 端点 `/ws` 没有应用任何认证中间件，允许任何未经认证的客户端：
- 创建新的沙箱会话
- 连接到现有会话
- 在容器中执行命令
- 上传/下载文件

**代码**:
```go
// Add WebSocket route - no auth required for WebSocket
mux.Handle("/ws", m.wsHandler)
```

**影响**: 完全的认证绕过，攻击者可以访问、创建或操作任何沙箱会话。

**修复建议**: 为 WebSocket 端点添加认证中间件。

---

### 1.2 会话 ID 枚举和授权绕过
**严重程度**: 关键 (95)
**位置**: `manager-service/internal/websocket/handler.go:158-166`

**问题描述**:
任何客户端都可以通过猜测或知道 `agent_thread_id` 来连接到任何现有会话。没有：
- 所有权验证
- 会话到用户的绑定
- 授权检查

**影响**: 未授权访问其他用户的沙箱会话，数据泄露，在他人容器中执行命令。

---

### 1.3 命令注入漏洞
**严重程度**: 关键 (90)
**位置**: `manager-service/internal/exec/wrapper.go:94-98`

**问题描述**:
shell 转义函数在特定场景下可能被绕过，导致命令注入。

**影响**: 在容器中以容器用户权限执行任意代码。

---

### 1.4 调试端点暴露存储凭据
**严重程度**: 高 (85)
**位置**: `manager-service/internal/app/app.go:471-501`

**问题描述**:
`/debug/config` 端点默认没有认证，且返回包含存储凭据的完整配置。

**影响**: 如果调试端点可访问，可能导致 S3/MinIO 存储访问密钥泄露。

---

### 1.5 开发模式禁用所有认证
**严重程度**: 高 (85)
**位置**: `manager-service/internal/auth/middleware.go:30-35`

**问题描述**:
如果 `SERVICE_KEYS` 环境变量为空或未设置，所有认证都会被禁用。这很危险，因为：
- 容易在部署时意外遗漏配置
- 没有明确的"开发模式"标志
- 服务会静默允许所有请求

**影响**: 如果配置不正确，生产部署可能没有认证。

---

### 1.6 WebSocket 未应用速率限制
**严重程度**: 高 (95)
**位置**: `manager-service/internal/app/app.go:310`

**问题描述**:
速率限制器仅应用于 `/v1/` HTTP 端点，不应用于 WebSocket 连接。

**影响**: 拒绝服务，资源耗尽。

---

### 1.7 缺少跨沙箱资源隔离
**严重程度**: 高 (90)
**位置**: `manager-service/internal/httpapi/handlers.go:110-150`

**问题描述**:
所有沙箱操作使用 `sessionId` 的哈希值生成 pod 名称，但没有验证认证用户是否"拥有"这个 sessionId。

**影响**: 用户之间的横向权限提升。

---

### 1.8 上传缺少 Content-Type 验证
**严重程度**: 中 (85)
**位置**: `manager-service/internal/httpapi/handlers.go:270-328`

**问题描述**:
上传处理程序不验证 Content-Type 是否与预期格式（tar.gz）匹配。

**影响**: 可能上传恶意文件，绕过配额限制。

---

### 1.9 日志中的信息泄露
**严重程度**: 中 (80)
**位置**: `manager-service/internal/auth/middleware.go:57-61`

**问题描述**:
日志区分"缺少"和"无效"密钥，可能有助于暴力攻击。

**影响**: 信息泄露辅助暴力破解攻击。

---

### 1.10 存储 TLS 证书验证未强制执行
**严重程度**: 中 (80)
**位置**: `manager-service/internal/storage/client.go:19-23`

**问题描述**:
当使用 SSL 时，MinIO 客户端可能不验证 TLS 证书。

**影响**: 在配置错误的环境中，存储连接可能遭受中间人攻击。

---

## 二、资源泄漏 (高 - 影响稳定性)

### 2.1 SnapshotWorkspace Goroutine 泄漏
**严重程度**: 高 (95)
**位置**: `manager-service/internal/k8s/snapshot.go:10-27`

**问题描述**:
当上下文被取消时，`SnapshotWorkspace` 中启动的 goroutine 会泄漏。goroutine 不检查 `ctx.Done()`，会在调用者放弃后继续尝试写入管道。

**影响**: 如果快照上传期间调用者上下文超时或被取消，goroutine 继续运行，消耗资源。随着时间的推移，这将导致 goroutine 泄漏和内存问题。

---

### 2.2 Downloader.Download Goroutine 泄漏
**严重程度**: 高 (95)
**位置**: `manager-service/internal/files/tar.go:308-345`

**问题描述**:
`Download` 中的 goroutine 从 `context.Background()` 创建新上下文，而不是使用传入的 `ctx`，丢失了取消传播。

**影响**: 如果客户端在下载期间断开连接，exec 继续在后台运行长达 120 秒，浪费资源。

---

### 2.3 SnapshotWorkspace 消费者中止读取时的资源泄漏
**严重程度**: 高 (85)
**位置**: `manager-service/internal/k8s/snapshot.go:10-27`

**问题描述**:
如果调用者停止从返回的 `io.ReadCloser` 读取（例如，由于错误），exec goroutine 将永远阻塞尝试写入管道。

**影响**: 如果存储上传中途失败，exec goroutine 无限期阻塞，泄漏 goroutine 和 K8s exec 连接。

---

### 2.4 最终处理程序未在关闭时正确停止
**严重程度**: 高 (90)
**位置**: `manager-service/internal/app/app.go:208`

**问题描述**:
最终处理程序使用 `context.Background()` 启动，关闭时没有优雅停止的机制。

**影响**: 关闭时，最终处理程序 goroutine 继续运行直到进程退出，可能导致快照上传不完整或资源泄漏。

---

### 2.5 HTTP 请求体未在错误路径上关闭
**严重程度**: 中 (90)
**位置**: `manager-service/internal/httpapi/handlers.go`

**问题描述**:
在多个处理程序中，早期返回时 `r.Body` 未关闭。

**影响**: 在高负载下，可能导致文件描述符耗尽或延迟连接重用。

---

### 2.6 DownloadSnapshot 错误时的资源泄漏
**严重程度**: 中 (80)
**位置**: `manager-service/internal/storage/client.go:72-85`

**问题描述**:
如果调用者在完全读取之前遇到错误，对象连接保持打开状态。

**影响**: 泄漏到 MinIO/S3 的连接，最终耗尽连接池。

---

### 2.7 最终处理程序中的 Context 传播问题
**严重程度**: 中 (88)
**位置**: `manager-service/internal/finalizer/handler.go:78-96`

**问题描述**:
最终处理程序的主 goroutine 没有正确将上下文取消传播到 `processPods` 操作。

**影响**:
如果 `processPods` 时间超过检查间隔且上下文在处理期间被取消，当前处理的 pod 快照将在处理程序停止之前完成。

---

### 2.8 缓冲区泄漏 - 会话缓冲区从未清理
**严重程度**: 高 (90)
**位置**: `manager-service/internal/buffer/manager.go`

**问题描述**:
`BufferManager` 有 `Delete` 方法，但在代码库中从未调用。当会话过期或被删除时，它们的关联缓冲区从未被删除。

**影响**: 随着时间的推移导致无界内存增长。

---

### 2.9 Tmux 进程未在上下文取消时终止
**严重程度**: 中 (85)
**位置**: `manager-service/internal/k8s/snapshot.go:13-24`

**问题描述**:
运行 `tar` 命令的 goroutine 在写入管道时不检查上下文是否被取消。

**影响**: 如果 `UploadSnapshot` 失败或超时，tar 进程继续在后台运行，消耗 CPU 和内存。

---

### 2.10 部分快照上传后上下文取消
**严重程度**: 高 (90)
**位置**: `manager-service/internal/finalizer/handler.go:138-141`

**问题描述**:
当快照上下文超时（默认 5 分钟）时，错误被记录但最终处理程序继续并删除最终处理程序，允许 pod 删除。

**影响**: 用户获得损坏的工作区快照，看起来有效但在恢复时失败。

---

### 2.11 Pod 删除时未优雅关闭活动的 Exec 连接
**严重程度**: 中 (82)
**位置**: `manager-service/internal/httpapi/handlers.go:405-423`

**问题描述**:
调用 `HandleDelete` 时，它立即使用宽限期 0 删除 pod，而不检查是否有活动的 WebSocket 连接或 exec 操作。

**影响**: 如果用户有活动的 WebSocket 连接或 exec 正在运行，使用宽限期 0 删除 pod 将立即终止连接，而没有适当的清理。

---

### 2.12 恢复操作没有超时
**严重程度**: 中 (85)
**位置**: `manager-service/internal/websocket/handler.go:295-303`

**问题描述**:
`RestoreWorkspace` 调用没有指定超时。

**影响**: 会话创建无限期挂起，阻塞用户工作区初始化。

---

## 三、逻辑错误 (高 - 影响正确性)

### 3.1 最终处理程序从未删除会话/缓冲区
**严重程度**: 高 (95)
**位置**: `manager-service/internal/websocket/handler.go`

**问题描述**:
当 WebSocket 连接关闭时（第 75、101、385、463 行），代码立即返回而不清理会话或缓冲区。

**影响**: 每个客户端断开连接都会在内存中留下孤立会话和缓冲区。在正常操作下，随着频繁重新连接，这将导致无界内存增长，最终导致 OOM 终止。

---

### 3.2 并发连接时的双重会话创建竞态条件
**严重程度**: 高 (90)
**位置**: `manager-service/internal/websocket/handler.go:156-167`

**问题描述**:
第 158-167 行的检查并创建模式不是原子的。

**影响**:
生产系统中的负载均衡器或重试逻辑会遇到此竞态条件，导致：
- 重复的 Kubernetes pod
- 会话状态损坏
- 一个客户端的消息发送到错误的会话

---

### 3.3 会话状态不一致 - 没有过期会话的后台清理
**严重程度**: 高 (90)
**位置**: `manager-service/internal/session/` (整个包)

**问题描述**:
`Session` 类型有 `IsExpired()` 方法和基于 TTL 的过期逻辑，但**没有后台 goroutine 调用它**。会话管理器从不删除过期会话。

**影响**:
- 会话映射无界增长
- 旧/废弃会话从未清理
- 内存泄漏与会话创建率成正比
- 过期会话仍然可用于附加

---

### 3.4 最终处理程序删除失败 - pod 被卡住
**严重程度**: 高 (90)
**位置**: `manager-service/internal/finalizer/handler.go:136-146`

**问题描述**:
当存储上传期间 `snapshotWorkspace` 失败时，最终处理程序仍然从 pod 中删除。如果 `RemoveFinalizer` 失败（例如，pod 已删除，网络错误），pod 将永久卡住并带有最终处理程序（带有删除时间戳的孤立 pod）。

**影响**:
pod 永远不会被删除，并将保留在集群中消耗资源。

---

### 3.5 Pod 创建失败时未调用会话管理器删除
**严重程度**: 高 (85)
**位置**: `manager-service/internal/websocket/handler.go:254-262`

**问题描述**:
在 `handleCreate` 中，如果 `WaitForPodReady` 失败，会话保留在管理器中，但 pod 可能处于失败状态。会话从未被清理。

**影响**:
如果 pod 已创建但从未就绪（例如，镜像拉取错误，资源限制），会话永远保留在管理器中，导致内存泄漏。pod 本身也永远不会被最终处理程序清理（如果它处于非就绪但未删除的状态）。

---

### 3.6 删除最终处理程序时没有重试
**严重程度**: 中 (82)
**位置**: `manager-service/internal/k8s/finalizer.go:30-60`

**问题描述**:
`RemoveFinalizer` 函数不使用客户端中定义的重试逻辑。

**影响**:
如果没有重试逻辑，这些瞬态错误会导致最终处理程序删除失败，使 pod 带有删除时间戳卡住。

---

### 3.7 缺少 ResourceVersion 检查导致更新冲突
**严重程度**: 中 (85)
**位置**: `manager-service/internal/k8s/finalizer.go:49-56`

**问题描述**:
更新 pod 以删除最终处理程序时，代码不使用 GET 请求中的 pod 的 `ResourceVersion`。

**影响**:
如果 pod 在 GET 和 UPDATE 调用之间被修改（例如，由另一个控制器），Update 将因冲突错误而失败。代码不重试冲突错误。

---

### 3.8 CheckTmux 退出代码逻辑反转
**严重程度**: 中 (85)
**位置**: `manager-service/internal/k8s/snapshot.go:38-50`

**问题描述**:
`CheckTmux` 函数在会话存在时（退出代码 0）返回 `true`，但逻辑是反转的。

**影响**:
当 tmux 会话存在时返回 `false`，当会话不存在时返回 `true`。

---

### 3.9 PatchActivity 错误未处理
**严重程度**: 中 (85)
**位置**: `manager-service/internal/httpapi/handlers.go:254`

**问题描述**:
`PatchActivity` 返回的错误被静默忽略。

**影响**:
失败的活动更新将导致 pod 过早过期，但用户不知道他们的会话为何消失。

---

### 3.10 未使用的 outputChan 导致消息丢失
**严重程度**: 中 (85)
**位置**: `manager-service/internal/websocket/handler.go:362, 435`

**问题描述**:
`outputChan` 被创建，但**从未有任何东西写入它**。

**影响**:
沙箱执行的所有 stdout/stderr 都会丢失。

---

### 3.11 会话指针在没有锁的情况下返回
**严重程度**: 中 (85)
**位置**: `manager-service/internal/session/manager.go:45-50`

**问题描述**:
`Get()` 方法在持有锁的情况下返回指向会话的指针。

**影响**:
当另一个 goroutine 可能正在修改它（例如，`UpdateState`，`MarkClientConnected`）时，调用者获取指向会话的指针。这会在会话字段上创建数据竞争。

---

### 3.12 缺少连接排出 - 关闭后写入竞态
**严重程度**: 中 (85)
**位置**: `manager-service/internal/websocket/handler.go:362-448`

**问题描述**:
两个 goroutine 都写入 `conn`，但在关闭时可能会竞态。

**影响**:
当一个 goroutine 退出并触发 `defer cancel()` 时，另一个可能仍在写入关闭的连接，导致写入错误和日志垃圾邮件。

---

### 3.13 快照上传失败时的潜在 Panic
**严重程度**: 高 (90)
**位置**: `manager-service/internal/storage/client.go:94-95`

**问题描述**:
对于某些错误类型（例如，网络错误、超时错误、上下文取消错误），`minio.ToErrorResponse(err)` 可以返回 `nil`。代码立即访问 `errResp.Code` 而不检查 nil，导致运行时 panic。

**影响**:
当存储不可达或超时时服务崩溃。

---

### 3.14 快照存在检查失败时的静默失败
**严重程度**: 高 (90)
**位置**: `manager-service/internal/websocket/handler.go:285`

**问题描述**:
`exists, _ := h.storageClient.SnapshotExists(ctx, snapshotKey)` 忽略错误。

**影响**:
如果存储在会话初始化期间不可用（网络问题、超时），`exists` 变为 `false`，系统继续而不恢复，可能导致用户数据丢失。

---

### 3.15 快照上传失败导致的快照损坏
**严重程度**: 高 (85)
**位置**: `manager-service/internal/finalizer/handler.go:153-173`

**问题描述**:
当 `UploadSnapshot` 中途失败（例如，网络中断、超时）时，部分/无效的快照可能保留在存储中。

**影响**:
恢复时失败的损坏快照，导致工作区初始化静默失败。

---

### 3.16 ListPodsWithFinalizer 没有超时
**严重程度**: 中 (80)
**位置**: `manager-service/internal/k8s/finalizer.go:13-28`

**问题描述**:
`ListPodsWithFinalizer` 函数不使用客户端的超时上下文。

**影响**:
如果 API 服务器慢，最终处理程序可能会挂起。

---

## 四、脚本错误 (中 - 影响部署和测试)

### 4.1 offline.sh 中未定义的变量
**严重程度**: 关键 (90)
**位置**: `scripts/lib/offline.sh:96, 104`

**问题描述**:
引用了变量 `$local_gc`、`$gc_ver` 和 `$tar_gc`，但从未定义。

**影响**: 任何离线导出操作都将因未定义变量错误而失败。这是离线/气隙部署的生产阻塞 Bug。

---

### 4.2 rollback.sh 中缺少错误处理
**严重程度**: 中 (85)
**位置**: `k8s/scripts/rollback.sh:15`

**问题描述**:
获取 ConfigMaps 的 kubectl 命令没有错误处理。

**影响**: 回滚操作可能静默失败或出现不明确的错误消息，使系统处于不一致状态。

---

### 4.3 smoke-test.sh 中的临时文件清理问题
**严重程度**: 中 (85)
**位置**: `scripts/smoke-test.sh:77-79`

**问题描述**:
清理函数尝试使用 `/tmp/sandbox-pf.pid` 中的 PID 终止进程，但如果此文件已损坏或包含无效数据，kill 命令可能会失败。

**影响**: 端口转发进程可能会泄漏，消耗系统资源并在后续运行中阻塞端口。

---

### 4.4 smoke-test.sh 中的竞态条件
**严重程度**: 中 (85)
**位置**: `scripts/smoke-test.sh:361-367`

**问题描述**:
在 `pkill` 和启动新端口转发之间，另一个进程可能会启动的窗口。

**影响**: 多个端口转发进程可能同时运行，导致混淆和潜在的端口绑定失败。

---

### 4.5 deploy.sh 中缺少错误传播
**严重程度**: 中 (85)
**位置**: `k8s/scripts/deploy.sh:61-72`

**问题描述**:
如果 `kubectl apply` 静默失败（例如，由于 dry-run 验证问题），脚本将继续等待。

**影响**:
部署失败可能无法正确检测，导致误报部署成功报告。

---

### 4.6 offline.sh 中不安全的变量引用
**严重程度**: 中 (85)
**位置**: `scripts/lib/offline.sh:236-258`

**问题描述**:
`offline_write_manifest` 函数使用未定义的变量 `$gc_ver` 和 `$tar_gc`。

**影响**:
manifest.json 文件将不完整，或者函数将在离线导出操作期间失败。

---

### 4.7 setup-harbor-secret.sh 中缺少验证
**严重程度**: 中 (85)
**位置**: `k8s/scripts/setup-harbor-secret.sh:31-33`

**问题描述**:
脚本使用 `read -sp` 提示输入密码，但不验证实际输入的密码（非空）。

**影响**:
将创建无效密钥，导致部署的 pod 中的镜像拉取失败。

---

## 五、修复优先级建议

### 立即修复（上线前必须完成）

1. **WebSocket 认证绕过** (1.1) - 最严重的安全漏洞
2. **会话授权绕过** (1.2) - 允许访问其他用户数据
3. **命令注入** (1.3) - 允许任意代码执行
4. **Goroutine 泄漏** (2.1, 2.2, 2.3) - 将导致内存耗尽
5. **会话清理缺失** (3.1, 3.3) - 将导致内存泄漏
6. **最终处理程序删除失败** (3.4) - 将导致孤立 pod
7. **offline.sh 未定义变量** (4.1) - 离线部署完全失败
8. **存储客户端 Panic** (3.13) - 服务崩溃

### 高优先级（上线后尽快修复）

1. 最终处理程序关闭问题 (2.4)
2. 缓冲区泄漏 (2.8)
3. 开发模式认证禁用 (1.5)
4. WebSocket 速率限制 (1.6)
5. 跨沙箱隔离 (1.7)
6. 调试端点凭据暴露 (1.4)

### 中优先级（计划修复）

1. HTTP 请求体关闭 (2.5)
2. 错误处理改进 (3.9, 3.10, 3.11)
3. 脚本错误处理 (4.2-4.7)
4. 日志信息泄露 (1.9)
5. TLS 验证 (1.10)

---

## 六、总体评估

### 代码质量
- **架构设计**: 良好，模块化清晰，职责分离合理
- **文档**: 完善，API 文档和配置指南都很详细
- **测试**: 有冒烟测试和单元测试，但覆盖率可以进一步提高

### 主要问题类别
1. **安全**: 认证/授权机制不完善，存在绕过风险
2. **资源管理**: Goroutine 和内存泄漏问题较多
3. **错误处理**: 部分关键错误未正确处理
4. **脚本**: 部署和测试脚本存在边界情况处理问题

### 上线建议
- **不建议直接上线**: 当前状态存在关键安全漏洞和资源泄漏问题，不建议直接部署到生产环境
- **修复后上线**: 修复"立即修复"类别中的所有问题后，可以上线运行
- **监控**: 上线后应密切监控内存、goroutine 数量和 pod 数量

---

## 附录：文件路径索引

### 核心服务文件
- `manager-service/cmd/manager/main.go` - 入口点
- `manager-service/internal/app/app.go` - 应用程序设置
- `manager-service/internal/websocket/handler.go` - WebSocket 处理
- `manager-service/internal/session/manager.go` - 会话管理
- `manager-service/internal/httpapi/handlers.go` - HTTP API
- `manager-service/internal/k8s/` - Kubernetes 客户端
- `manager-service/internal/storage/client.go` - 存储客户端
- `manager-service/internal/auth/middleware.go` - 认证中间件

### 脚本文件
- `scripts/lib/offline.sh` - 离线部署脚本
- `scripts/smoke-test.sh` - 冒烟测试
- `k8s/scripts/deploy.sh` - 部署脚本
- `k8s/scripts/rollback.sh` - 回滚脚本
- `k8s/scripts/setup-harbor-secret.sh` - Harbor 密钥设置

---

**报告生成时间**: 2025-02-06
**审查方法**: 静态代码分析 + 多代理并行审查
**置信度**: 80-100%（每个问题已标注）
