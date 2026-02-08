# I01@A01 实施设计方案

> **设计日期**: 2026-02-06
> **基于**: CODE_REVIEW_REPORT.md (A01)
> **实施范围**: P0 + P1-1 + P1-2
> **预估工期**: 2-3 周

---

## 1. 概述

### 1.1 目标

基于 A01 代码评审报告，实施关键安全和稳定性改进，将项目从 **7.5/10 (良好)** 提升至 **生产就绪** 状态。

### 1.2 改进项清单

| 改进项 | 类型 | 优先级 | 文件数 | 工作量 |
|--------|------|--------|--------|--------|
| P0-1: WebSocket 安全修复 | 安全 | P0 | 4 | 0.5 天 |
| P0-3: Context 取消传播 | 稳定性 | P0 | 4 | 1 天 |
| P0-4: 测试覆盖率提升 | 质量 | P0 | 15+ | 5 天 |
| P0-2: Smoke 测试命令 | 质量 | P0 | 8 | 2 天 |
| P1-1: API 速率限制 | 安全 | P1 | 4 | 2 天 |
| P1-2: 改进秘密管理 | 安全 | P1 | 5 | 1 天 |

**总计**: 约 40+ 文件，11.5 天工作量

### 1.3 架构原则

- **最小侵入**: 复用现有结构，新增包而非重构
- **配置驱动**: 所有新功能可通过配置启用/禁用
- **渐进式**: 每个改进可独立测试和验证
- **向后兼容**: 新功能默认关闭或使用安全默认值

---

## 2. P0-1: WebSocket 安全修复

### 2.1 问题

`CheckOrigin: return true` 允许任何网站发起 WebSocket 连接，存在 CSRF 风险。

### 2.2 解决方案

配置化 Origin 白名单，支持环境变量覆盖。

### 2.3 架构变更

```
新增文件:
├── internal/websocket/config.go
└── internal/config/types.go (扩展)

修改文件:
├── internal/websocket/handler.go
├── internal/config/loader.go
└── k8s/base/manager-config.yaml
```

### 2.4 核心实现

```go
// internal/websocket/config.go
package websocket

import (
    "errors"
    "net/http"
    "time"

    "github.com/gorilla/websocket"
)

type Config struct {
    ReadBufferSize          int
    WriteBufferSize         int
    AllowedOrigins          []string
    AllowNonBrowserRequests bool
    HandshakeTimeout        time.Duration
}

func (c *Config) Validate() error {
    if len(c.AllowedOrigins) == 0 {
        return errors.New("at least one allowed origin required")
    }
    return nil
}

func (c *Config) Upgrader() *websocket.Upgrader {
    originSet := make(map[string]bool)
    for _, origin := range c.AllowedOrigins {
        originSet[origin] = true
    }

    return &websocket.Upgrader{
        ReadBufferSize:    c.ReadBufferSize,
        WriteBufferSize:   c.WriteBufferSize,
        HandshakeTimeout:  c.HandshakeTimeout,
        CheckOrigin: func(r *http.Request) bool {
            origin := r.Header.Get("Origin")
            if origin == "" {
                return c.AllowNonBrowserRequests
            }
            return originSet[origin]
        },
    }
}
```

### 2.5 配置方式

**默认配置** (`k8s/base/manager-config.yaml`):
```yaml
websocket:
  allowedOrigins:
    - "http://localhost:3000"
    - "https://example.com"
  allowNonBrowserRequests: true
  readBufferSize: 1024
  writeBufferSize: 1024
  handshakeTimeout: 10s
```

**环境变量覆盖**:
```bash
ALLOWED_ORIGINS="https://prod.example.com,https://staging.example.com"
```

---

## 3. P0-3: Context 取消传播

### 3.1 问题

长时间运行的操作（Pod 轮询等待、文件上传、WebSocket I/O 转发）没有正确处理 context 取消，可能导致资源泄漏。

### 3.2 解决方案

统一的 Poller 模式，在所有轮询场景中正确传播 context 取消。

### 3.3 架构变更

```
新增文件:
└── internal/observability/
    ├── context.go
    └── poller_test.go

修改文件:
├── internal/k8s/pods.go
├── internal/api/handlers.go
└── internal/storage/client.go
```

### 3.4 核心实现

```go
// internal/observability/context.go
package observability

import (
    "context"
    "fmt"
    "time"
)

type Poller struct {
    interval time.Duration
    timeout  time.Duration
}

func NewPoller(interval, timeout time.Duration) *Poller {
    return &Poller{
        interval: interval,
        timeout:  timeout,
    }
}

func (p *Poller) Poll(ctx context.Context, check func() (bool, error)) error {
    ctx, cancel := context.WithTimeout(ctx, p.timeout)
    defer cancel()

    ticker := time.NewTicker(p.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("poll canceled: %w", ctx.Err())
        case <-ticker.C:
            done, err := check()
            if err != nil {
                return err
            }
            if done {
                return nil
            }
        }
    }
}
```

### 3.5 应用示例

**修改前**:
```go
for {
    pod, err := m.k8sClient.GetPod(ctx, podName)
    if err != nil {
        return err
    }
    if isPodReady(pod) {
        break
    }
    time.Sleep(2 * time.Second)
}
```

**修改后**:
```go
poller := observability.NewPoller(2*time.Second, 5*time.Minute)
err := poller.Poll(ctx, func() (bool, error) {
    pod, err := m.k8sClient.GetPod(ctx, podName)
    if err != nil {
        return false, err
    }
    return isPodReady(pod), nil
})
```

---

## 4. P0-4: 测试覆盖率提升

### 4.1 目标

从 40% 提升到 70%+。

### 4.2 架构变更

```
新增文件:
├── internal/testutils/
│   ├── mock.go
│   └── builder.go
└── internal/**/*_test.go (15+ 文件)

修改文件:
├── go.mod
└── Makefile
```

### 4.3 测试基础设施

```go
// internal/testutils/mock.go
package testutils

import (
    "context"
    "io"

    "github.com/stretchr/testify/mock"
    v1 "k8s.io/api/core"
)

type MockK8sClient struct {
    mock.Mock
}

func (m *MockK8sClient) GetPod(ctx context.Context, name string) (*v1.Pod, error) {
    args := m.Called(ctx, name)
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    return args.Get(0).(*v1.Pod), args.Error(1)
}

type MockStorageClient struct {
    mock.Mock
}

func (m *MockStorageClient) Upload(ctx context.Context, key string, r io.Reader) error {
    args := m.Called(ctx, key, r)
    return args.Error(0)
}
```

### 4.4 优先测试文件

| 文件 | 当前覆盖 | 目标 | 优先级 |
|------|---------|------|--------|
| `config/validate.go` | 20% | 80% | 高 |
| `buffer/ring.go` | 30% | 90% | 高 |
| `session/manager.go` | 40% | 75% | 高 |
| `observability/context.go` | 0% | 90% | 高 |
| `websocket/config.go` | 0% | 90% | 高 |
| `k8s/pods.go` | 50% | 70% | 中 |
| `httpapi/errors.go` | 10% | 85% | 中 |

### 4.5 Makefile 目标

```makefile
.PHONY: test-cover
test-cover:
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: test-cover-func
test-cover-func:
	@echo "Function-level coverage:"
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | grep -v "100.0%"
```

---

## 5. P0-2: Smoke 测试命令

### 5.1 问题

当前测试需手动组合多个命令，容易遗漏步骤，无统一测试入口。

### 5.2 解决方案

场景化测试脚本，`./sbx test smoke` 一键运行完整测试流程。

### 5.3 架构变更

```
新增文件:
├── scripts/lib/test.sh
├── scripts/test/lib/
│   ├── runner.sh
│   ├── scenarios.sh
│   └── assertions.sh
└── scripts/test/smoke/
    ├── 01-environment.sh
    ├── 02-create-sandbox.sh
    ├── 03-websocket-connection.sh
    ├── 04-snapshot-restore.sh
    └── 05-cleanup.sh

修改文件:
├── sbx
└── Makefile
```

### 5.4 测试框架

```bash
# scripts/test/lib/runner.sh
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_RED='\033[0;31m'
readonly COLOR_YELLOW='\033[0;33m'
readonly COLOR_NC='\033[0m'

TEST_PASSED=0
TEST_FAILED=0
TEST_SKIPPED=0

run_scenario() {
    local scenario=$1
    local description=$2

    echo -e "${COLOR_YELLOW}Running: ${description}${COLOR_NC}"

    if bash "${scenario}"; then
        echo -e "${COLOR_GREEN}✓ PASSED: ${description}${COLOR_NC}"
        ((TEST_PASSED++))
        return 0
    else
        echo -e "${COLOR_RED}✗ FAILED: ${description}${COLOR_NC}"
        ((TEST_FAILED++))
        return 1
    fi
}

print_report() {
    echo ""
    echo "================================"
    echo "Smoke Test Report"
    echo "================================"
    echo -e "Passed:  ${COLOR_GREEN}${TEST_PASSED}${COLOR_NC}"
    echo -e "Failed:  ${COLOR_RED}${TEST_FAILED}${COLOR_NC}"
    echo -e "Skipped: ${COLOR_YELLOW}${TEST_SKIPPED}${COLOR_NC}"
    echo "================================"

    if [ ${TEST_FAILED} -eq 0 ]; then
        echo -e "${COLOR_GREEN}All tests passed!${COLOR_NC}"
        return 0
    else
        echo -e "${COLOR_RED}Some tests failed!${COLOR_NC}"
        return 1
    fi
}
```

### 5.5 CLI 集成

```bash
# sbx 新增
cmd_test() {
    local action=${1:-smoke}

    case "${action}" in
        smoke)
            source "scripts/test/lib/runner.sh"
            source "scripts/test/lib/assertions.sh"

            echo "Running smoke tests..."

            run_scenario "scripts/test/smoke/01-environment.sh" "Environment Check"
            run_scenario "scripts/test/smoke/02-create-sandbox.sh" "Create Sandbox"
            run_scenario "scripts/test/smoke/03-websocket-connection.sh" "WebSocket Connection"
            run_scenario "scripts/test/smoke/04-snapshot-restore.sh" "Snapshot & Restore"
            run_scenario "scripts/test/smoke/05-cleanup.sh" "Cleanup Resources"

            print_report
            ;;
        unit)
            make test-unit
            ;;
        cover)
            make test-cover
            ;;
        *)
            echo "Unknown test type: ${action}"
            echo "Available: smoke, unit, cover"
            exit 1
            ;;
    esac
}
```

---

## 6. P1-1: API 速率限制

### 6.1 问题

无 API 速率限制，可能导致资源耗尽攻击。

### 6.2 解决方案

三层速率限制（全局 + Per-IP + Per-Session），使用 Token Bucket 算法。

### 6.3 架构变更

```
新增文件:
├── internal/ratelimit/
│   ├── limiter.go
│   ├── config.go
│   └── limiter_test.go
└── internal/config/types.go (扩展)

修改文件:
├── internal/api/handlers.go
└── k8s/base/manager-config.yaml
```

### 6.4 核心实现

```go
// internal/ratelimit/limiter.go
package ratelimit

import (
    "context"
    "net/http"
    "sync"
    "time"

    "golang.org/x/time/rate"
)

type Config struct {
    GlobalRPS        float64
    GlobalBurst      int
    PerIPRPS         float64
    PerIPBurst       int
    PerSessionRPS    float64
    PerSessionBurst  int
    CleanupInterval  time.Duration
}

type Limiter struct {
    global     *rate.Limiter
    perIP      sync.Map
    perSession sync.Map
    cfg        *Config
}

func NewLimiter(cfg *Config) *Limiter {
    return &Limiter{
        global: rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalBurst),
        cfg:    cfg,
    }
}

func (l *Limiter) Allow(ctx context.Context, ip, sessionID string) bool {
    if !l.global.Allow() {
        return false
    }

    if ip != "" {
        ipLimiter, _ := l.perIP.LoadOrStore(ip,
            rate.NewLimiter(rate.Limit(l.cfg.PerIPRPS), l.cfg.PerIPBurst))
        if !ipLimiter.(*rate.Limiter).Allow() {
            return false
        }
    }

    if sessionID != "" {
        sessionLimiter, _ := l.perSession.LoadOrStore(sessionID,
            rate.NewLimiter(rate.Limit(l.cfg.PerSessionRPS), l.cfg.PerSessionBurst))
        if !sessionLimiter.(*rate.Limiter).Allow() {
            return false
        }
    }

    return true
}

func (l *Limiter) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ip := r.RemoteAddr
        sessionID := r.URL.Query().Get("id")

        if !l.Allow(r.Context(), ip, sessionID) {
            http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

### 6.5 配置方式

**默认配置** (`k8s/base/manager-config.yaml`):
```yaml
rateLimit:
  global:
    rps: 100
    burst: 200
  perIP:
    rps: 10
    burst: 20
  perSession:
    rps: 5
    burst: 10
  cleanupInterval: 5m
```

---

## 7. P1-2: 改进秘密管理

### 7.1 问题

MinIO 凭据通过环境变量明文传递，存在泄露风险。

### 7.2 解决方案

使用 K8s Secrets + Volume Mount，凭据从文件读取。

### 7.3 架构变更

```
新增文件:
├── k8s/base/minio-secret.yaml
└── internal/storage/credentials.go

修改文件:
├── k8s/base/manager-deployment.yaml
├── internal/storage/client.go
└── internal/config/loader.go
```

### 7.4 K8s Secret

```yaml
# k8s/base/minio-secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: minio-credentials
  namespace: default
type: Opaque
stringData:
  access.key: "${MINIO_ACCESS_KEY}"
  secret.key: "${MINIO_SECRET_KEY}"
```

### 7.5 凭据加载器

```go
// internal/storage/credentials.go
package storage

import (
    "os"
    "path/filepath"
    "strings"
)

type Credentials struct {
    AccessKey string
    SecretKey string
    Endpoint  string
    Bucket    string
}

func LoadCredentials() (*Credentials, error) {
    if credPath := os.Getenv("STORAGE_CREDENTIALS_PATH"); credPath != "" {
        return loadFromFile(credPath)
    }
    return loadFromEnv()
}

func loadFromFile(path string) (*Credentials, error) {
    accessKey, err := os.ReadFile(filepath.Join(path, "access.key"))
    if err != nil {
        return nil, err
    }

    secretKey, err := os.ReadFile(filepath.Join(path, "secret.key"))
    if err != nil {
        return nil, err
    }

    return &Credentials{
        AccessKey: strings.TrimSpace(string(accessKey)),
        SecretKey: strings.TrimSpace(string(secretKey)),
        Endpoint:  os.Getenv("STORAGE_ENDPOINT"),
        Bucket:    os.Getenv("STORAGE_BUCKET"),
    }, nil
}

func loadFromEnv() (*Credentials, error) {
    return &Credentials{
        AccessKey: os.Getenv("STORAGE_ACCESS_KEY"),
        SecretKey: os.Getenv("STORAGE_SECRET_KEY"),
        Endpoint:  os.Getenv("STORAGE_ENDPOINT"),
        Bucket:    os.Getenv("STORAGE_BUCKET"),
    }, nil
}
```

---

## 8. 实施检查点

### 8.1 P0 验收标准

- [ ] WebSocket 只接受白名单来源
- [ ] Context 取消能立即终止操作
- [ ] `go test -cover` 显示 70%+ 覆盖
- [ ] `./sbx test smoke` 全部通过

### 8.2 P1 验收标准

- [ ] API 速率限制生效
- [ ] MinIO 凭据从 K8s Secret 读取
- [ ] Prometheus 指标正确暴露

---

## 9. 文件清单

### 9.1 新增文件

```
internal/websocket/config.go
internal/observability/context.go
internal/observability/poller_test.go
internal/testutils/mock.go
internal/testutils/builder.go
internal/ratelimit/limiter.go
internal/ratelimit/config.go
internal/ratelimit/limiter_test.go
internal/storage/credentials.go
scripts/lib/test.sh
scripts/test/lib/runner.sh
scripts/test/lib/assertions.sh
scripts/test/lib/scenarios.sh
scripts/test/smoke/01-environment.sh
scripts/test/smoke/02-create-sandbox.sh
scripts/test/smoke/03-websocket-connection.sh
scripts/test/smoke/04-snapshot-restore.sh
scripts/test/smoke/05-cleanup.sh
k8s/base/minio-secret.yaml
*_test.go (15+ 单元测试)
```

### 9.2 修改文件

```
internal/websocket/handler.go
internal/k8s/pods.go
internal/api/handlers.go
internal/storage/client.go
internal/config/types.go
internal/config/loader.go
k8s/base/manager-config.yaml
k8s/base/manager-deployment.yaml
sbx
Makefile
```

---

**设计文档结束**
