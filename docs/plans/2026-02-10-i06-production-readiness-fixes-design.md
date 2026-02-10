# MBOS-Sandbox I06: Production Readiness Fixes - Design Document

**Date**: 2026-02-10
**Task**: I06 - mbos-sandbox改进设计和编码实现
**Version**: V04
**Author**: Claude Code Agent

---

## 1. Overview

### 1.1 Objective

Fix critical blockers identified in V04 verification (docs/verifications/5397-mbos-sandbox-v04.md) to make mbos-sandbox production-ready.

### 1.2 Scope

This fix addresses **7 issues** across Critical and Important severity levels:

| ID | Severity | Component | Issue | Impact |
|----|----------|-----------|-------|--------|
| 2.1.1 | Critical | Cleaner | Pod label selector mismatch (`sandbox` vs `llm-sandbox`) | Cleaner never finds pods to clean |
| 2.1.2 | Critical | Cleaner | Duplicate namespace args (Go flag limitation) | `sandbox-system` never scanned |
| 2.1.3 | Critical | Cleaner | Namespace whitelist doesn't include target namespaces | Cleaner rejected from target namespaces |
| 2.2 | Critical | K8s Exec | Default container name mismatch (`runner` vs `sandbox`) | All exec operations fail |
| 2.3 | Critical | Config Watch | Integer overflow in backoff calculation | Potential retry loop after 63 failures |
| 2.4 | Critical | WebSocket | No message size limit | DoS vulnerability (memory exhaustion) |
| 2.5 | Critical | Rate Limiter | Memory leak (no cleanup of per-IP/per-session limiters) | OOM in production |
| 3.2 | Important | WebSocket | Pod IP not validated after ready | Potential connection failures |

**Excluded**: Moderate and Low priority issues (YAGNI principle).

### 1.3 Design Principles

- **KISS**: Simple fixes, no over-engineering
- **DRY**: Centralize configuration values, avoid duplication
- **SOLID**: Single responsibility, clear separation of concerns
- **YAGNI**: Only fix what's needed

---

## 2. Architecture Approach

### 2.1 Overall Strategy

Minimal invasive fixes following SOLID principles. Each fix is localized and doesn't require architectural changes.

### 2.2 Fix Strategy Summary

| Fix | Strategy | Files Changed |
|-----|----------|---------------|
| Cleaner (2.1.*) | Dual CronJobs + Config update | k8s manifests, `cmd/cleaner/main.go` |
| Exec Container (2.2) | Config-driven default | `internal/k8s/*` |
| Integer Overflow (2.3) | Bounded exponential backoff | `internal/config/watch.go` |
| WebSocket Size (2.4) | Configurable limit + early validation | `internal/config/types.go`, `internal/websocket/handler.go` |
| Rate Limiter (2.5) | Time-based eviction with sync.Map | `internal/ratelimit/limiter.go` |
| Pod IP Validation (3.2) | Retry + format validation | `internal/websocket/handler.go` |

---

## 3. Detailed Design

### 3.1 Fix 1: Cleaner Service (Issues 2.1.1, 2.1.2, 2.1.3)

#### 3.1.1 Problem Analysis

The Cleaner Service has three interconnected issues:
1. Label selector uses `app=sandbox` but pods have `app=llm-sandbox`
2. Go's flag package only keeps the last `--namespace` value
3. Namespace whitelist doesn't include target namespaces

#### 3.1.2 Solution: Dual CronJobs

**Architecture Decision**: Create two separate CronJobs instead of refactoring code to support multiple namespaces.

**Rationale**:
- **KISS**: No code changes for multi-namespace support
- **Isolation**: Each namespace cleaner runs independently
- **Observability**: Can monitor each namespace separately
- **Kubernetes Best Practice**: One CronJob per concern

#### 3.1.3 Implementation

**Kubernetes Manifest Changes**:

1. Delete: `k8s/base/cleaner-cronjob.yaml`
2. Create: `k8s/base/cleaner-cronjob-sandbox-system.yaml`
3. Create: `k8s/base/cleaner-cronjob-sandbox-workspaces.yaml`

Each CronJob has a single namespace arg:
```yaml
args:
- --namespace=sandbox-system  # or sandbox-workspaces
```

**Code Changes** (`manager-service/cmd/cleaner/main.go`):

```go
// Line ~20: Fix label selector constant
const sandboxAppLabel = "llm-sandbox"  // was: "sandbox"

// Line ~26: Update allowed namespaces
var allowedNamespaces = map[string]bool{
    "sandbox-system":     true,
    "sandbox-workspaces": true,
}
```

#### 3.1.4 Kustomization Update

Update all overlays (`k8s/overlays/*/kustomization.yaml`):
```yaml
resources:
  # - cleaner-cronjob.yaml  # Remove
  - cleaner-cronjob-sandbox-system.yaml    # Add
  - cleaner-cronjob-sandbox-workspaces.yaml  # Add
```

---

### 3.2 Fix 2: Exec Container Name (Issue 2.2)

#### 3.2.1 Problem Analysis

The `Executor` hardcodes `"runner"` as default container name, but the actual container name from config is `"sandbox"`.

#### 3.2.2 Solution: Config-Driven Default

Store the default container name in the `Client` struct and use it when `Container` is empty in `ExecOptions`.

#### 3.2.3 Implementation

**File**: `manager-service/internal/k8s/client.go`

```go
type Client struct {
    config     *rest.Config
    clientset  *kubernetes.Clientset
    namespace  string
    defaultContainer string  // Add: Default container name
}

func NewClient(cfg *rest.Config, namespace string, defaultContainer string) (*Client, error) {
    // ... existing code ...
    return &Client{
        config:           cfg,
        clientset:        clientset,
        namespace:        namespace,
        defaultContainer: defaultContainer,  // Store default
    }, nil
}
```

**File**: `manager-service/internal/k8s/exec.go`

```go
func NewExecutor(client *Client) *Executor {
    return &Executor{
        config:            client.config,
        clientset:         client.clientset,
        restClient:        client.clientset.CoreV1().RESTClient(),
        namespace:         client.namespace,
        defaultContainer:  client.defaultContainer,  // Add
    }
}

type Executor struct {
    config            *rest.Config
    clientset         *kubernetes.Clientset
    restClient        rest.Interface
    namespace         string
    defaultContainer  string  // Add: Default container name
}

func (e *Executor) Exec(ctx context.Context, podName string, opts *ExecOptions) (*ExecResult, error) {
    if opts == nil {
        opts = &ExecOptions{}
    }

    if opts.Container == "" {
        opts.Container = e.defaultContainer  // Use default from config
    }
    // ... rest of function ...
}
```

**File**: `manager-service/internal/app/app.go`

Update `NewK8sClient()` call to pass container name from config:
```go
k8sClient, err := k8s.NewClient(
    restConfig,
    cfg.Sandbox.Defaults.Namespace,
    cfg.Sandbox.Defaults.ContainerName,  // Add: Pass container name
)
```

---

### 3.3 Fix 3: Integer Overflow (Issue 2.3)

#### 3.3.1 Problem Analysis

`1<<uint(w.consecutiveFailures-1)` overflows when `consecutiveFailures > 63`, causing the backoff to reset to a small value and potentially creating a retry loop.

#### 3.3.2 Solution: Bounded Exponential Backoff

Cap the exponent at a safe value (60) before bit shifting.

#### 3.3.3 Implementation

**File**: `manager-service/internal/config/watch.go:400-409`

```go
func (w *Watcher) calculateBackoff() time.Duration {
    // Exponential backoff: 2^n seconds, capped at maxBackoff
    exp := w.consecutiveFailures - 1
    const maxExp = 60  // Prevent overflow (2^60 >> universe age)
    if exp > maxExp {
        exp = maxExp
    }
    backoff := time.Duration(1<<uint(exp)) * time.Second

    if backoff > w.maxBackoff {
        backoff = w.maxBackoff
    }
    if backoff < w.minInterval {
        backoff = w.minInterval
    }
    return backoff
}
```

---

### 3.4 Fix 4: WebSocket Message Size (Issue 2.4)

#### 3.4.1 Problem Analysis

WebSocket handler doesn't validate message size before base64 decoding, allowing malicious clients to send oversized messages that exhaust memory.

#### 3.4.2 Solution: Configurable Limit + Early Validation

Add `MaxMessageSize` to WebSocketConfig and validate before decoding.

#### 3.4.3 Implementation

**File**: `manager-service/internal/config/types.go`

```go
type WebSocketConfig struct {
    ReadBufferSize          int      `yaml:"readBufferSize"`
    WriteBufferSize         int      `yaml:"writeBufferSize"`
    AllowedOrigins          []string `yaml:"allowedOrigins"`
    AllowNonBrowserRequests bool     `yaml:"allowNonBrowserRequests"`
    HandshakeTimeout        string   `yaml:"handshakeTimeout"`
    MaxMessageSize          int64    `yaml:"maxMessageSize"`  // Add
}
```

**File**: `manager-service/internal/config/types.go` (DefaultConfig)

```go
WebSocket: WebSocketConfig{
    ReadBufferSize:          1024,
    WriteBufferSize:         1024,
    AllowedOrigins:          []string{"http://localhost:3000"},
    AllowNonBrowserRequests: true,
    HandshakeTimeout:        "10s",
    MaxMessageSize:          10 << 20,  // 10MB (Add)
}
```

**File**: `manager-service/internal/websocket/handler.go:542-546`

```go
case TypeStdin:
    payload, err := h.parseStdin(msg.Data)
    if err != nil {
        h.logger.Error("Failed to parse stdin: %v", err)
        continue
    }

    // Validate size before decoding (base64 expands ~4/3x)
    const maxEncodedSize = (10 << 20) * 4 / 3  // 10MB decoded ≈ 13.3MB encoded
    if len(payload.Data) > maxEncodedSize {
        h.logger.Error("Stdin data too large: %d bytes (max %d)", len(payload.Data), maxEncodedSize)
        continue
    }

    data, err := base64.StdEncoding.DecodeString(payload.Data)
    if err != nil {
        h.logger.Error("Failed to decode stdin data: %v", err)
        continue
    }
    // ... rest of handler ...
```

**Note**: Use config value `h.cfg.MaxMessageSize` instead of constant for production.

---

### 3.5 Fix 5: Rate Limiter Memory Leak (Issue 2.5)

#### 3.5.1 Problem Analysis

The `Limiter` creates new limiters for each unique IP/session but never cleans them up. The `stopCleanup` channel is defined but never used.

#### 3.5.2 Solution: Time-Based Eviction with sync.Map

Implement a cleanup goroutine that removes stale entries based on last access time.

#### 3.5.3 Implementation

**File**: `manager-service/internal/ratelimit/limiter.go`

```go
package ratelimit

import (
    "context"
    "net/http"
    "sync"
    "time"

    "golang.org/x/time/rate"
)

// limiterEntry wraps a rate limiter with last access time
type limiterEntry struct {
    limiter    *rate.Limiter
    lastAccess time.Time
}

// Limiter implements three-tier rate limiting (global + per-IP + per-session)
type Limiter struct {
    global     *rate.Limiter
    perIP      sync.Map  // map[string]*limiterEntry
    perSession sync.Map  // map[string]*limiterEntry
    cfg        *Config

    // cleanup management
    stopCleanup chan struct{}
    wg          sync.WaitGroup  // For graceful shutdown
}

// NewLimiter creates a new rate limiter with the given configuration
func NewLimiter(cfg *Config) *Limiter {
    l := &Limiter{
        global:      rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalBurst),
        cfg:         cfg,
        stopCleanup: make(chan struct{}),
    }
    l.startCleanup()
    return l
}

// startCleanup begins the background cleanup goroutine
func (l *Limiter) startCleanup() {
    l.wg.Add(1)
    go func() {
        defer l.wg.Done()
        ticker := time.NewTicker(l.cfg.CleanupInterval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                l.cleanupStaleEntries()
            case <-l.stopCleanup:
                return
            }
        }
    }()
}

// cleanupStaleEntries removes limiters that haven't been accessed recently
func (l *Limiter) cleanupStaleEntries() {
    cutoff := time.Now().Add(-3 * l.cfg.CleanupInterval)

    l.perIP.Range(func(key, value interface{}) bool {
        entry := value.(*limiterEntry)
        if entry.lastAccess.Before(cutoff) {
            l.perIP.Delete(key)
        }
        return true
    })

    l.perSession.Range(func(key, value interface{}) bool {
        entry := value.(*limiterEntry)
        if entry.lastAccess.Before(cutoff) {
            l.perSession.Delete(key)
        }
        return true
    })
}

// Allow checks if a request should be allowed based on the rate limits
func (l *Limiter) Allow(ctx context.Context, ip, sessionID string) bool {
    // Check global limit first (cheap)
    if !l.global.Allow() {
        return false
    }

    // Check per-IP limit
    if ip != "" && l.cfg.PerIPRPS > 0 {
        entry, _ := l.perIP.LoadOrStore(ip, &limiterEntry{
            limiter:    rate.NewLimiter(rate.Limit(l.cfg.PerIPRPS), l.cfg.PerIPBurst),
            lastAccess: time.Now(),
        })
        e := entry.(*limiterEntry)
        e.lastAccess = time.Now()

        if !e.limiter.Allow() {
            return false
        }
    }

    // Check per-session limit
    if sessionID != "" && l.cfg.PerSessionRPS > 0 {
        entry, _ := l.perSession.LoadOrStore(sessionID, &limiterEntry{
            limiter:    rate.NewLimiter(rate.Limit(l.cfg.PerSessionRPS), l.cfg.PerSessionBurst),
            lastAccess: time.Now(),
        })
        e := entry.(*limiterEntry)
        e.lastAccess = time.Now()

        if !e.limiter.Allow() {
            return false
        }
    }

    return true
}

// Stop gracefully shuts down the rate limiter
func (l *Limiter) Stop() {
    close(l.stopCleanup)
    l.wg.Wait()
}

// Middleware returns an HTTP middleware that enforces rate limiting
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

// DefaultConfig returns the default rate limiter configuration
func DefaultConfig() *Config {
    return &Config{
        GlobalRPS:       100,
        GlobalBurst:     200,
        PerIPRPS:        10,
        PerIPBurst:      20,
        PerSessionRPS:   5,
        PerSessionBurst: 10,
        CleanupInterval: 5 * time.Minute,
    }
}
```

**File**: `manager-service/internal/app/app.go` (Add graceful shutdown)

```go
func (a *App) Shutdown(ctx context.Context) error {
    a.logger.Info("Shutting down application")

    // Stop rate limiter cleanup
    if a.rateLimiter != nil {
        a.rateLimiter.Stop()
    }

    // ... existing shutdown code ...
}
```

---

### 3.6 Fix 6: Pod IP Validation (Issue 3.2)

#### 3.6.1 Problem Analysis

After `WaitForPodReady()` returns success, the code assumes `PodIP` is valid, but the IP field may be temporarily empty or invalid.

#### 3.6.2 Solution: Retry + Format Validation

Add IP validation with retry logic after pod is ready.

#### 3.6.3 Implementation

**File**: `manager-service/internal/websocket/handler.go`

```go
import (
    "net"
    // ... existing imports ...
)

// validatePodIP checks that the pod has a valid IP address
func (h *Handler) validatePodIP(pod *v1.Pod) error {
    if pod.Status.PodIP == "" {
        return fmt.Errorf("pod IP not ready")
    }

    // Validate IP format
    ip := net.ParseIP(pod.Status.PodIP)
    if ip == nil {
        return fmt.Errorf("invalid pod IP format: %s", pod.Status.PodIP)
    }

    return nil
}

// waitForPodWithIP waits for pod to be ready AND have a valid IP
func (h *Handler) waitForPodWithIP(ctx context.Context, sess *session.Session) (*v1.Pod, error) {
    pod, err := h.waitForPodReady(ctx, sess)
    if err != nil {
        return nil, err
    }

    // Validate IP with retry
    maxRetries := 5
    retryInterval := 500 * time.Millisecond

    for i := 0; i < maxRetries; i++ {
        if err := h.validatePodIP(pod); err == nil {
            return pod, nil
        }

        h.logger.Debug("Pod IP validation failed (attempt %d/%d): %v", i+1, maxRetries, err)

        // Refresh pod to get updated status
        pod, err = h.k8sClient.GetPod(ctx, sess.PodName)
        if err != nil {
            return nil, fmt.Errorf("failed to refresh pod: %w", err)
        }

        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(retryInterval):
            // Continue to next retry
        }
    }

    return nil, fmt.Errorf("pod IP validation failed after %d attempts", maxRetries)
}
```

Update callers to use `waitForPodWithIP` instead of `waitForPodReady`.

---

## 4. Testing Strategy

### 4.1 Unit Tests

| Fix | Test Coverage |
|-----|---------------|
| Cleaner | Test label selector, namespace validation |
| Exec Container | Test default container resolution |
| Integer Overflow | Test backoff calculation with high failure counts |
| WebSocket Size | Test message size validation |
| Rate Limiter | Test cleanup, entry eviction, Stop() |
| Pod IP | Test IP validation with retry |

### 4.2 Integration Tests

- Cleaner: Deploy dual CronJobs, verify pod cleanup
- Exec: Verify commands execute with default container
- Rate Limiter: Verify memory doesn't grow unbounded

### 4.3 Manual Verification

1. Deploy to dev environment
2. Create test sandboxes
3. Verify cleaner removes expired pods
4. Verify exec commands work
5. Load test WebSocket and rate limiter

---

## 5. Risk Assessment

| Fix | Risk | Mitigation |
|-----|------|------------|
| Cleaner | Low | Straightforward manifest changes |
| Exec Container | Low | Config-driven, backward compatible |
| Integer Overflow | Low | Pure bug fix, no behavior change for normal cases |
| WebSocket Size | Low | Configurable, default 10MB is generous |
| Rate Limiter | Medium | New goroutine, need graceful shutdown |
| Pod IP | Low | Adds retry, improves reliability |

**Overall Risk**: Low to Medium

---

## 6. Rollback Plan

If issues arise after deployment:

1. **Cleaner**: Revert to single CronJob (will only scan one namespace)
2. **Exec Container**: Revert to hardcoded "runner" (will break exec)
3. **Integer Overflow**: Revert to original (theoretical issue)
4. **WebSocket Size**: Remove size check (DoS vulnerability returns)
5. **Rate Limiter**: Remove cleanup (memory leak returns)
6. **Pod IP**: Remove validation (potential connection failures)

**Recommended Rollback Strategy**: Each fix can be independently reverted.

---

## 7. Summary

This design document outlines fixes for **7 critical/important issues** that block production deployment. All fixes follow KISS, DRY, SOLID, and YAGNI principles.

**Key Decisions**:
1. Dual CronJobs for multi-namespace cleaner (simplest solution)
2. Config-driven defaults for container name (DRY)
3. Bounded exponential backoff (safe, no behavior change)
4. Configurable WebSocket limits (flexible, secure)
5. Time-based rate limiter cleanup (prevents OOM)
6. Pod IP validation with retry (improves reliability)

**Next Steps**:
1. Create detailed implementation plan
2. Execute fixes in isolated worktree
3. Write tests
4. Deploy to dev for verification
5. Merge to main

---

**Design Status**: Approved
**Ready for Implementation**: Yes
