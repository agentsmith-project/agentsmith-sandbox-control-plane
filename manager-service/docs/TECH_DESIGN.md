# Manager-Service 技术设计（ConfigMap 热加载 + 业务接口 Service Key）

本文档定义将 `manager-service/` 提升到企业级可用性的技术方案：保留现有业务 API 形态，新增“集群内部简易鉴权（service key）”，并以 **ConfigMap 文件挂载**实现 **运行时热加载**；同时修复关键可靠性问题（Exec 可信退出码、文件协议统一）并补齐可运维能力（错误模型、request-id、readyz/metrics/debug）。

---

## 1. 目标与范围

### 1.1 目标
- **鉴权**：仅对业务接口强制校验 `service key`（内部简易鉴权）。
- **配置**：配置来源为 K8s ConfigMap，支持**热加载**（不重启生效）。
- **可靠性**：
  - `exec` 返回**可信 `exitCode`**。
  - 文件上传/下载统一为 **tar.gz**（不兼容旧协议）。
  - 请求超时、输出截断、错误分类清晰、幂等行为稳定。
- **可运维**：标准错误结构、request-id、`/healthz`、`/readyz`、`/metrics`、`/debug/config`。

### 1.2 非目标
- 不做：多租户、配额/限流、审计、复杂鉴权/授权（由上层业务 proxy 实现）。
- 不使用：sqlite 或其他持久化数据库。
- 不保证：与旧 upload/download 协议兼容。

---

## 2. API 设计（接口定义与鉴权范围）

### 2.1 业务接口（强制 Service Key）
保持现有路径集合（便于上层 proxy 对接），但修正行为与协议：
- `PUT /v1/sandboxes/{sessionId}`：创建/确保 Pod（幂等）
- `POST /v1/sandboxes/{sessionId}/touch`：续期（幂等）
- `POST /v1/sandboxes/{sessionId}/exec`：执行命令（返回可信 `exitCode`）
- `POST /v1/sandboxes/{sessionId}/files/upload?dest=`：上传（**tar.gz** 请求体）
- `GET /v1/sandboxes/{sessionId}/files/download?src=`：下载（**tar.gz** 响应体）
- `DELETE /v1/sandboxes/{sessionId}`：删除 Pod（建议 NotFound 视作成功，可配置）

鉴权：以上全部必须携带有效 service key。

### 2.2 运维接口（不强制 Service Key）
- `GET /healthz`：进程存活
- `GET /readyz`：配置已成功加载 + K8s client 初始化成功
- `GET /metrics`：Prometheus 指标（默认开启，可配置）
- `GET /debug/config`：当前生效配置（脱敏）+ hash + 最近加载错误

> 说明：你要求“只对业务接口强制”，因此运维接口默认不鉴权；但保留开关可对 `/metrics` 启用 key 校验。

---

## 3. 鉴权设计（Service Key）

### 3.1 请求格式
- Header：`X-Service-Key: <key>`（可配置 header 名）
- 可选兼容：`Authorization: ServiceKey <key>`（可配置 scheme 与是否启用）

### 3.2 Key 存储与轮换
- **禁止**将 key 明文放入 ConfigMap（防误用/泄露）。
- 使用 **K8s Secret** 注入 env：
  - `SERVICE_KEYS=key1,key2,...`（逗号分隔，多 key 便于轮换窗口）

### 3.3 校验策略
- 仅做等值匹配（constant-time compare 建议）。
- 失败返回：
  - `401` + 标准错误结构（见第 6 节）

---

## 4. 配置体系（ConfigMap 文件挂载 + 热加载）

### 4.1 配置来源
- ConfigMap 中提供键：`manager-config.yaml`
- 挂载到容器路径（只读）：`/etc/sandbox-manager/manager-config.yaml`
- 通过热加载机制在运行时更新内存配置快照

### 4.2 热加载机制
推荐实现：**fsnotify + debounce + hash gate + failure backoff**。
- 监听配置目录/文件变化（需兼容 ConfigMap 的 symlink/rename 更新方式）
- Debounce（防抖）：默认 `300ms`，多事件合并一次 reload
- Min interval（节流）：默认 `1s`，避免频繁 reload
- Hash gate：若内容 hash 与当前一致，忽略 reload
- Failure backoff：失败后按 1/2/4/8… 秒退避，最大 30s；成功后清空退避

### 4.3 成功/失败策略
- **成功**：原子替换当前配置快照，立即对新请求生效
- **失败**：保留最后一次成功配置继续服务；记录 `lastError`（`/debug/config` 可见），指标计数
- **从未成功加载过配置**：`/readyz` 返回 503；业务接口返回 503 `CONFIG_NOT_LOADED`

### 4.4 Boot 配置 vs Runtime 配置
为避免 runtime 配置损坏导致热加载器失效，以下建议作为 **boot 配置（env/flag，不热更）**：
- `CONFIG_PATH`（默认 `/etc/sandbox-manager/manager-config.yaml`）
- `CONFIG_RELOAD_DEBOUNCE`（默认 `300ms`）
- `CONFIG_RELOAD_MIN_INTERVAL`（默认 `1s`）
- `CONFIG_RELOAD_BACKOFF_MAX`（默认 `30s`）
- `STRICT_CONFIG_RELOAD`（默认 `false`，见 4.5）
- `SERVICE_KEYS`（Secret 注入）

Runtime 配置（本文件）负责：sandbox 默认值、exec/files 规则、可观测接口开关、鉴权 header 名等。

### 4.5 严格模式（可选）
提供 boot 开关 `STRICT_CONFIG_RELOAD=true`：
- 一旦发生 reload 失败，业务接口返回 503（不推荐默认启用）
- 默认：reload 失败不影响业务接口（继续用旧配置）

---

## 5. 运行时配置（`manager-config.yaml` 完整样例）

> 说明：该配置用于 ConfigMap；service key 明文不在此文件中。

```yaml
version: 1

server:
  httpPort: 8080
  requestIdHeader: X-Request-Id
  timeouts:
    readHeader: 5s
    read: 30s
    write: 60s
    idle: 120s
  maxHeaderBytes: 1048576
  metrics:
    enabled: true
    path: /metrics
    requireServiceKey: false
  debug:
    configPath: /debug/config
    enablePprof: false

auth:
  enabled: true
  headerName: X-Service-Key
  acceptAuthorization: true
  authorizationScheme: ServiceKey
  failStatusCode: 401

kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s
  retry:
    enabled: true
    maxAttempts: 3
    baseBackoff: 200ms
    maxBackoff: 2s

sandbox:
  defaults:
    namespace: sandbox
    runnerImage: sandbox-runner:1.0.0
    imagePullPolicy: IfNotPresent
    ttlSeconds: 900
    podReadyWait: 30s
    podPollInterval: 500ms
    terminationGraceSeconds: 1
    activeDeadlineSeconds: 0
    containerName: runner
    workdir: /workspace
    volumes:
      workspace:
        name: workspace
        mountPath: /workspace
        sizeLimit: 0
      tmp:
        name: tmp
        mountPath: /tmp
        sizeLimit: 256Mi
    resources:
      requests:
        cpu: 100m
        memory: 256Mi
      limits:
        cpu: "1"
        memory: 1Gi
        ephemeralStorage: 2Gi
    labels:
      app: llm-sandbox
    annotations: {}

exec:
  defaultTimeout: 30s
  maxTimeout: 300s
  stdoutMaxBytes: 1048576
  stderrMaxBytes: 1048576
  preserveTailBytes: 4096
  exitCodeMarker:
    key: "__SBX_EXIT_CODE__"
    stream: "stderr"
  shell:
    bin: sh
    args: ["-lc"]
  env:
    allowRegex: "^[A-Z_][A-Z0-9_]*$"
  workdir:
    allowedPrefixes: ["/workspace"]

files:
  rootPrefix: /workspace
  upload:
    defaultDest: /workspace
    maxBytes: 52428800
    format: tar.gz
  download:
    defaultSrc: /workspace
    format: tar.gz
  tar:
    bin: tar
    rejectSymlinks: true
```

---

## 6. 错误模型与错误码

### 6.1 标准错误响应
所有错误返回统一结构（业务与运维接口一致）：
```json
{
  "error": {
    "code": "SOME_ERROR_CODE",
    "message": "human readable message",
    "requestId": "..."
  }
}
```

### 6.2 错误码清单（建议定稿）
鉴权：
- `SERVICE_KEY_MISSING`（401）
- `SERVICE_KEY_INVALID`（401）

配置/就绪：
- `CONFIG_NOT_LOADED`（503）：从未加载过成功配置
- `NOT_READY`（503）：readyz 不就绪（例如 K8s client 初始化失败）

请求校验：
- `BAD_REQUEST`（400）：JSON/参数格式
- `INVALID_ENV_KEY`（422）
- `INVALID_WORKDIR`（422）
- `INVALID_PATH`（422）
- `UPLOAD_TOO_LARGE`（413）

Kubernetes/沙箱：
- `POD_CREATE_FAILED`（500）
- `POD_GET_FAILED`（500）
- `POD_NOT_FOUND`（404）
- `POD_NOT_READY`（503）
- `POD_READY_TIMEOUT`（504）
- `POD_PATCH_FAILED`（500）
- `POD_DELETE_FAILED`（500）
- `K8S_EXEC_FAILED`（500）

Exec：
- `EXEC_TIMEOUT`（504）
- `EXEC_EXITCODE_UNAVAILABLE`（500）

Files：
- `UPLOAD_EXEC_FAILED`（500）
- `DOWNLOAD_EXEC_FAILED`（500）

### 6.3 配置加载错误（内部/调试）
配置热加载失败不必影响业务（默认），但必须可观测。建议在 `/debug/config` 返回：
- `code`: `CONFIG_READ_FAILED` | `CONFIG_PARSE_FAILED` | `CONFIG_SCHEMA_UNSUPPORTED` | `CONFIG_VALIDATION_FAILED` | `CONFIG_APPLY_FAILED`
- `fieldPath`、`ruleId`、`rule`、`message`

日志字段建议：
- `event=config.load|config.reload`
- `result=success|failure`
- `config_hash`
- `error_code`、`error_field`、`error_rule`、`error_message`
- `reload_id`

---

## 7. 关键可靠性改造点

### 7.1 Exec：可信 `exitCode`
K8s SPDY exec 无法稳定获得进程退出码，必须使用 wrapper：
- 用 `sh -lc` 执行“设置 env + cd workdir + 执行命令”
- 命令结束后输出固定 marker（建议输出到 **stderr**）：
  - `__SBX_EXIT_CODE__=<n>`
- 服务端解析 marker，填充 `ExecResponse.exitCode`，并从 stdout/stderr 中移除 marker

输出截断必须保证 marker 不被截掉：
- 使用“保留末尾 N 字节”的输出 writer（tail buffer）
- `preserveTailBytes` 必须 `<= min(stdoutMaxBytes, stderrMaxBytes)`

### 7.2 Files：统一 tar.gz（不兼容旧）
- upload：Pod 内执行 `tar -xzf - -C <dest>`
- download：Pod 内执行 `tar -czf - -C <src> .`
- `dest/src` 必须是绝对路径且位于 `files.rootPrefix` 下（最佳实践；避免误伤容器其它路径）
- upload body 限制：`files.upload.maxBytes`
- 视需要实现 `rejectSymlinks`：避免 tar 解包通过 symlink 穿越 `rootPrefix`

### 7.3 ensurePod：幂等与并发
- 并发创建同 session：`Create` 遇到 AlreadyExists 时转为 `Get + waitReady`
- Ready wait：使用统一 `podReadyWait/podPollInterval`，并对错误分类（NotFound/Forbidden/Timeout）

---

## 8. 可观测性与健康检查

### 8.1 Request ID
- 若请求已携带 `X-Request-Id`（可配置 header），沿用
- 否则生成 UUID/随机 ID，并回写响应头

### 8.2 `healthz` / `readyz`
- `/healthz`：只要进程存活即 200
- `/readyz`：至少满足：
  - K8s client 初始化成功
  - 已加载过至少一次成功 runtime 配置

### 8.3 `/metrics`
建议最小指标：
- HTTP：按路由/状态码请求计数、延迟直方图
- 业务：create/touch/exec/upload/download/delete 次数
- 配置：reload 成功/失败计数、当前 hash info、最近成功/失败时间
- K8s：API 调用失败计数（get/create/patch/delete/exec）

### 8.4 `/debug/config`
返回：
- `meta`: `schemaVersion/sourcePath/currentHash/loadedAt/reloadCount/lastError`
- `config`: 当前生效配置（脱敏；不包含任何 key）
- `meta.boot`: 可选包含 boot 参数（不含 `SERVICE_KEYS` 明文）

---

## 9. 配置校验规则（摘要）

为避免遗留功能/隐式行为，所有字段必须严格校验：
- `version` 仅允许 `1`
- duration：必须在合理范围内（例如 `exec.maxTimeout` 不超过 1h）
- bytes：必须在合理范围内（例如 `stdoutMaxBytes` 最多 64MiB）
- path：必须是绝对路径，且 `workdir/dest/src` 必须在允许前缀下
- env key：必须匹配 `exec.env.allowRegex`
- Quantity：必须可解析，且 request 不得大于 limit
- `preserveTailBytes` 关系约束：`<= min(stdoutMaxBytes, stderrMaxBytes)`
- `files.upload.defaultDest`、`files.download.defaultSrc` 必须位于 `files.rootPrefix` 下

配置校验失败时，必须输出：
- `fieldPath`（点路径，如 `exec.defaultTimeout`）
- `ruleId`（`REQUIRED|RANGE|ENUM|FORMAT|QUANTITY_PARSE|RELATION|PREFIX`）
- `rule`（人类可读约束）
- `message`（含关键上下文，不含敏感信息）

---

## 10. K8s 清单变更（热加载所需）

### 10.1 ConfigMap
在 `k8s/base/configmap.yaml` 增加：
- `manager-config.yaml: |-`（第 5 节配置内容）

### 10.2 Deployment
在 `k8s/base/manager-deployment.yaml` 增加：
- volume：ConfigMap 挂载到 `/etc/sandbox-manager/manager-config.yaml`（建议 `subPath`）
- env：从 Secret 注入 `SERVICE_KEYS`
- 可选 env：`CONFIG_PATH=/etc/sandbox-manager/manager-config.yaml`

### 10.3 RBAC
本设计采用“文件挂载热加载”，无需新增 RBAC。
（若未来改为 watch ConfigMap API，则需要 `get/list/watch` configmaps 权限，当前不做。）

---

## 11. 建议代码结构（不写代码，但作为任务拆分标准）

在 `manager-service/` 内做模块化重构（示例）：
- `cmd/manager/main.go`：启动、boot 配置、server lifecycle、优雅退出
- `internal/config/`：types/load/validate/watch（热加载）
- `internal/auth/`：service key 校验与 middleware（仅 `/v1`）
- `internal/httpapi/`：router、handlers、types、errors（标准错误结构）
- `internal/k8s/`：client 初始化、pods 操作、exec 封装
- `internal/exec/`：wrapper 构造、marker 解析、tail output
- `internal/files/`：tar.gz upload/download、路径校验
- `internal/observability/`：logging、metrics

---

## 12. 任务拆分（按文件/接口/验收）

> 约定：每个任务都必须具备可验证的验收点；不写代码时也要保证“实现无二义性”。

### Task A：结构化配置 + 校验 + `/debug/config`
- 新增：`internal/config/{types,load,validate}.go`，定义 schema 与默认值
- 新增：`/debug/config` 返回 `meta + config`
验收：
- 启动日志包含 `configHash`
- `/debug/config` 可显示当前 hash、loadedAt、lastError

### Task B：ConfigMap 文件热加载（debounce/throttle/hash/backoff）
- 新增：`internal/config/watch.go`
- Boot 参数：`CONFIG_RELOAD_DEBOUNCE/MIN_INTERVAL/BACKOFF_MAX`
验收：
- 修改 ConfigMap 后无需重启生效（`/debug/config` hash 变化）
- 多事件合并只 reload 一次；非法配置不覆盖旧配置；backoff 生效

### Task C：Service Key（仅业务接口）middleware
- 新增：`internal/auth/servicekey.go`、`internal/auth/middleware.go`
- 仅包裹 `/v1/*` 业务路由
验收：
- 无 key/错 key 业务接口 401；运维接口不受影响

### Task D：Exec exitCode 可信化 + 输出截断保证 marker
- 新增：`internal/exec/{wrapper,output}.go`
- `ExecResponse.exitCode` 必须来自 marker；解析失败报 `EXEC_EXITCODE_UNAVAILABLE`
验收：
- `exit 7` 返回 `exitCode=7`
- 超大输出仍可解析退出码（marker 不丢）

### Task E：upload/download 统一 tar.gz + 路径约束 + upload 大小限制
- 新增：`internal/files/tar.go`
- upload/download 均为 tar.gz；`dest/src` 需在 rootPrefix；大文件返回 413
验收：
- 上传/下载可互通；非法路径被拒绝；上传超限 413

### Task F：HTTP 工业化（错误模型、request-id、recover、优雅退出）
- 新增：`internal/httpapi/errors.go`（标准错误结构与错误码）
- 新增：请求级 request-id 与结构化日志
- `http.Server` 超时与 SIGTERM 优雅退出
验收：
- 所有错误统一 JSON；响应头携带 request-id；SIGTERM 优雅关闭

### Task G：`/readyz` + `/metrics`
- `/readyz` 判断：已成功加载配置 + K8s client OK
- `/metrics` 暴露最小指标集（HTTP/业务/K8s/配置）
验收：
- 未加载配置时 readyz 非就绪；metrics 可抓取且包含关键项

---

## 13. 风险与边界说明
- ConfigMap 更新传播存在延迟：热加载生效时间依赖 kubelet 刷新周期与事件触发；通过 debounce/minInterval/backoff 控制抖动。
- 修改 runtime 配置可能影响新请求行为，但不会回溯影响已执行中的请求（例如 exec）。
- 不兼容旧文件协议（tar vs tar.gz）：上层 proxy/调用方需要同步升级。

