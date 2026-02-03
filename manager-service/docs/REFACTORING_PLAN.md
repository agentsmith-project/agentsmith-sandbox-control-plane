# Manager-Service 代码评审与改进计划

## 执行摘要

经过代码库探索和专家评审文档的分析，我同意技术专家们的评估。当前系统**可以运行**（功能完整度7.5/10），但在长期维护性、回归成本和生产级稳定性方面存在明显短板。

## 分析观点

### 与专家评估的一致观点

| 问题类别 | 严重程度 | 影响 |
|---------|---------|------|
| 缺少Go单元测试 | 🔴 高 | 定位慢、回归慢、边界条件难覆盖 |
| HTTP metrics不准确 | 🔴 高 | 线上排障被误导，无法快速归因 |
| 上传解包安全策略 | 🟡 中 | symlink/hardlink等风险未明确处理 |
| 路由匹配脆弱 | 🟡 中 | 新增API时容易误匹配 |

### 补充发现

1. **根目录 `executor.go` 是遗留代码**
   - 文件存在但未被引用，应清理

2. **代码重复：Auto-creation 逻辑**
   - `Touch`, `Exec`, `Upload`, `Download` handler 都有类似的 "if not exists, create" 逻辑
   - 应抽取到 `internal/k8s` 层的 `EnsurePod` 方法

3. **Configuration Validation 可增强**
   - 缺少对 image name 格式的验证
   - resource units (如 "100m", "128Mi") 未验证

## 改进优先级

**用户决策**: Phase 1 (重构) 优先，90%+ 测试覆盖率，允许相对 symlinks

### Phase 1: 可维护性提升 (P1, 1-2天) ⭐ 优先

**目标：重构路由与 handler 组织，为后续测试打基础**

1. **路由匹配重构**
   - 替换 `strings.Contains/HasSuffix` 为明确路由表
   - 统一 `/v1/sandboxes/{id}` 相关路由
   - 文件: `internal/httpapi/handlers.go`

2. **抽取 Auto-creation 逻辑**
   - 将重复的 EnsurePod 逻辑抽取到 k8s 层
   - 文件: `internal/k8s/pods.go`, `internal/httpapi/handlers.go`

3. **清理遗留代码**
   - 删除根目录 `executor.go`
   - 验证无引用

### Phase 2: 基础质量保障 (P0, 2-3天)

**目标：建立 90%+ 覆盖率的测试体系，修正观测数据**

1. **为 `internal/k8s` 增加单测** (目标: 90%+)
   - `exec_test.go`: exec URL 构建参数验证
   - `pods_test.go`: EnsurePod, AutoCreate 逻辑, TTL 更新
   - `client_test.go`: Retry 逻辑, 错误处理

2. **为 `internal/files` 增加单测** (目标: 90%+)
   - 路径校验逻辑
   - maxBytes 限制
   - tar stderr 错误判定
   - **相对 symlink 处理** (允许) vs 绝对 symlink 拒绝
   - 路径穿越检测

3. **为 `internal/httpapi` 增加组件测试** (目标: 90%+)
   - 使用 `httptest` 覆盖所有 handler
   - 错误码与输入校验 (invalid path, upload too large, timeout)
   - Auto-creation 逻辑验证

4. **修正 HTTP metrics**
   - 添加 `ResponseWriter` wrapper 捕获真实 status code
   - Path pattern 化避免高基数
   - 文件: `internal/observability/metrics.go`

### Phase 3: 安全加固 (P1, 2-3天)

**目标：加固上传/解包安全策略，允许相对 symlinks**

1. **明确 tar 解包策略**
   - **允许**: 相对 symlink (沙盒内), 普通文件/目录
   - **拒绝**: 绝对 symlink, hardlink, 绝对路径, `..` 路径穿越, 特殊文件 (device, fifo 等)
   - 两段式解包: 先扫描 entry，再解包
   - 文件: `internal/files/tar.go`

2. **补充 E2E 测试**
   - 验证相对 symlink 被允许
   - 验证绝对 symlink 被拒绝
   - 验证路径穿越被拒绝
   - 文件: `manager-service/scripts/e2e-test.sh`

## 关键文件清单

### 需要修改的文件 (按 Phase 顺序)

```
# Phase 1: Refactoring (优先)
manager-service/
├── executor.go                          # [删除] 遗留代码
├── internal/
│   ├── k8s/
│   │   └── pods.go                      # [修改] 抽取 EnsurePod 逻辑
│   └── httpapi/
│       └── handlers.go                  # [修改] 路由重构 + 移除重复逻辑

# Phase 2: Testing (90%+ 覆盖率)
manager-service/internal/
├── k8s/
│   ├── exec_test.go                     # [新增] exec URL 构建测试
│   ├── pods_test.go                     # [新增] EnsurePod, TTL 测试
│   └── client_test.go                   # [新增] Retry 逻辑测试
├── files/
│   └── tar_test.go                      # [新增] 路径校验, symlink 测试
├── httpapi/
│   └── handlers_test.go                 # [新增] Handler 组件测试
└── observability/
    └── metrics.go                       # [修改] ResponseWriter wrapper

# Phase 3: Security
manager-service/
├── internal/files/
│   └── tar.go                           # [修改] 相对 symlink 允许, 绝对拒绝
└── scripts/
    └── e2e-test.sh                      # [修改] 添加 symlink 安全测试
```

## 验证计划

### 测试策略

1. **单元测试 (90%+ 覆盖率)**
   ```bash
   cd manager-service
   go test ./internal/k8s/... -v -cover
   go test ./internal/files/... -v -cover
   go test ./internal/httpapi/... -v -cover
   go test ./... -coverprofile=coverage.out
   go tool cover -html=coverage.out  # 查看覆盖率详情
   ```

2. **E2E 测试**
   ```bash
   cd manager-service
   ./scripts/e2e-test.sh
   ```

3. **Metrics 验证**
   ```bash
   kubectl port-forward -n sandbox-system svc/sandbox-manager 8080:80
   curl http://localhost:8080/metrics | grep http_requests
   ```

### 验收标准

#### Phase 1 (Refactoring)
- [ ] 根目录 `executor.go` 已删除
- [ ] 路由匹配使用明确路由表，无 `Contains/HasSuffix`
- [ ] Auto-creation 逻辑抽取到 `k8s.EnsurePod`
- [ ] 所有现有功能正常工作 (E2E 通过)

#### Phase 2 (Testing)
- [ ] 所有新增单测通过
- [ ] **总覆盖率 >= 90%**
- [ ] `internal/k8s/` 覆盖率 >= 90%
- [ ] `internal/files/` 覆盖率 >= 90%
- [ ] `internal/httpapi/` 覆盖率 >= 90%

#### Phase 3 (Security)
- [ ] E2E 测试通过
- [ ] **相对 symlink 被允许** (测试验证)
- [ ] **绝对 symlink 被拒绝** (测试验证)
- [ ] **路径穿越被拒绝** (测试验证)
- [ ] Metrics 显示正确的 status code distribution

## 用户决策记录

| 决策项 | 选择 | 说明 |
|--------|------|------|
| 优先顺序 | Phase 1 (重构) 优先 | 先改善代码结构，为测试打基础 |
| 测试覆盖率 | 90%+ | 高质量测试保障长期可维护性 |
| Symlink 策略 | 允许相对 symlinks | 支持沙盒内相对链接，拒绝绝对链接 |
