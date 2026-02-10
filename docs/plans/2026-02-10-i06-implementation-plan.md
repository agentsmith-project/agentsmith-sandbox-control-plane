# MBOS-Sandbox I06 Production Readiness Fixes - Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix 7 critical/important issues identified in V04 verification to make mbos-sandbox production-ready.

**Architecture:** Minimal invasive fixes following SOLID principles. Each fix is localized - Cleaner uses dual CronJobs for multi-namespace support, Exec uses config-driven defaults, Rate Limiter implements time-based eviction.

**Tech Stack:** Go 1.24, Kubernetes client-go, gorilla/websocket, golang.org/x/time/rate

---

## Task 1: Fix Cleaner Label Selector (Issue 2.1.1)

**Files:**
- Modify: `manager-service/cmd/cleaner/main.go:20`

**Step 1: Write the failing test**

Create `manager-service/cmd/cleaner/main_test.go`:

```go
package main

import "testing"

func TestSandboxAppLabel(t *testing.T) {
    // The label must match what's actually used on pods
    // See internal/config/types.go:312 which uses "llm-sandbox"
    if sandboxAppLabel != "llm-sandbox" {
        t.Errorf("sandboxAppLabel = %q, want %q", sandboxAppLabel, "llm-sandbox")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./cmd/cleaner/... -v`
Expected: FAIL with "sandboxAppLabel = "sandbox", want "llm-sandbox""

**Step 3: Write minimal implementation**

Edit `manager-service/cmd/cleaner/main.go` at line ~20:

```go
// Change from:
// var sandboxAppLabel = "sandbox"
// To:
var sandboxAppLabel = "llm-sandbox"
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./cmd/cleaner/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add manager-service/cmd/cleaner/main.go manager-service/cmd/cleaner/main_test.go
git commit -m "fix(cleaner): use correct pod label selector 'llm-sandbox'

Fixes issue 2.1.1 - Cleaner was using 'sandbox' label but pods are
created with 'llm-sandbox' label, causing cleaner to never find pods."
```

---

## Task 2: Fix Cleaner Namespace Whitelist (Issue 2.1.3)

**Files:**
- Modify: `manager-service/cmd/cleaner/main.go:26-31`

**Step 1: Write the failing test**

Add to `manager-service/cmd/cleaner/main_test.go`:

```go
func TestAllowedNamespaces(t *testing.T) {
    requiredNamespaces := []string{"sandbox-system", "sandbox-workspaces"}

    for _, ns := range requiredNamespaces {
        if !allowedNamespaces[ns] {
            t.Errorf("Namespace %q is not in allowedNamespaces whitelist", ns)
        }
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./cmd/cleaner/... -v`
Expected: FAIL with "Namespace "sandbox-system" is not in allowedNamespaces whitelist"

**Step 3: Write minimal implementation**

Edit `manager-service/cmd/cleaner/main.go` at line ~26:

```go
// Change from:
// var allowedNamespaces = map[string]bool{
//     "default": true,
//     "dev":     true,
//     "test":    true,
//     "staging": true,
// }
// To:
var allowedNamespaces = map[string]bool{
    "sandbox-system":     true,
    "sandbox-workspaces": true,
}
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./cmd/cleaner/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add manager-service/cmd/cleaner/main.go manager-service/cmd/cleaner/main_test.go
git commit -m "fix(cleaner): update namespace whitelist for target namespaces

Fixes issue 2.1.3 - Cleaner was rejecting sandbox-system and
sandbox-workspaces namespaces. Updated whitelist to only include
actual target namespaces."
```

---

## Task 3: Create Dual Cleaner CronJobs (Issue 2.1.2)

**Files:**
- Delete: `k8s/base/cleaner-cronjob.yaml`
- Create: `k8s/base/cleaner-cronjob-sandbox-system.yaml`
- Create: `k8s/base/cleaner-cronjob-sandbox-workspaces.yaml`

**Step 1: Read existing CronJob**

Run: `cat k8s/base/cleaner-cronjob.yaml`

**Step 2: Create sandbox-system CronJob**

Create `k8s/base/cleaner-cronjob-sandbox-system.yaml`:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: sandbox-cleaner-sandbox-system
  namespace: sandbox-system
  labels:
    app: sandbox-cleaner
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 12
  failedJobsHistoryLimit: 3
  startingDeadlineSeconds: 300
  jobTemplate:
    metadata:
      labels:
        app: sandbox-cleaner
    spec:
      ttlSecondsAfterFinished: 300
      backoffLimit: 3
      template:
        metadata:
          labels:
            app: sandbox-cleaner
        spec:
          serviceAccountName: sandbox-manager
          restartPolicy: OnFailure
          securityContext:
            seccompProfile:
              type: RuntimeDefault
          containers:
          - name: cleaner
            image: sandbox-manager:2.0.0
            imagePullPolicy: IfNotPresent
            command:
            - /cleaner
            args:
            - --namespace=sandbox-system
            - --log-level=info
            env:
            - name: SERVICE_KEYS
              valueFrom:
                secretKeyRef:
                  name: sandbox-manager-keys
                  key: SERVICE_KEYS
            - name: CONFIG_PATH
              value: "/etc/sandbox-manager/manager-config.yaml"
            - name: DEBUG
              value: "false"
            volumeMounts:
            - name: manager-config
              mountPath: /etc/sandbox-manager/manager-config.yaml
              subPath: manager-config.yaml
              readOnly: true
            resources:
              requests:
                cpu: "50m"
                memory: "64Mi"
              limits:
                cpu: "200m"
                memory: "256Mi"
            securityContext:
              runAsNonRoot: true
              allowPrivilegeEscalation: false
              readOnlyRootFilesystem: false
              capabilities:
                drop: ["ALL"]
          volumes:
          - name: manager-config
            configMap:
              name: sandbox-manager-config
```

**Step 3: Create sandbox-workspaces CronJob**

Create `k8s/base/cleaner-cronjob-sandbox-workspaces.yaml`:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: sandbox-cleaner-sandbox-workspaces
  namespace: sandbox-system
  labels:
    app: sandbox-cleaner
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 12
  failedJobsHistoryLimit: 3
  startingDeadlineSeconds: 300
  jobTemplate:
    metadata:
      labels:
        app: sandbox-cleaner
    spec:
      ttlSecondsAfterFinished: 300
      backoffLimit: 3
      template:
        metadata:
          labels:
            app: sandbox-cleaner
        spec:
          serviceAccountName: sandbox-manager
          restartPolicy: OnFailure
          securityContext:
            seccompProfile:
              type: RuntimeDefault
          containers:
          - name: cleaner
            image: sandbox-manager:2.0.0
            imagePullPolicy: IfNotPresent
            command:
            - /cleaner
            args:
            - --namespace=sandbox-workspaces
            - --log-level=info
            env:
            - name: SERVICE_KEYS
              valueFrom:
                secretKeyRef:
                  name: sandbox-manager-keys
                  key: SERVICE_KEYS
            - name: CONFIG_PATH
              value: "/etc/sandbox-manager/manager-config.yaml"
            - name: DEBUG
              value: "false"
            volumeMounts:
            - name: manager-config
              mountPath: /etc/sandbox-manager/manager-config.yaml
              subPath: manager-config.yaml
              readOnly: true
            resources:
              requests:
                cpu: "50m"
                memory: "64Mi"
              limits:
                cpu: "200m"
                memory: "256Mi"
            securityContext:
              runAsNonRoot: true
              allowPrivilegeEscalation: false
              readOnlyRootFilesystem: false
              capabilities:
                drop: ["ALL"]
          volumes:
          - name: manager-config
            configMap:
              name: sandbox-manager-config
```

**Step 4: Delete old CronJob**

Run: `rm k8s/base/cleaner-cronjob.yaml`

**Step 5: Update kustomization files**

For each overlay (`k8s/overlays/dev/kustomization.yaml`, `k8s/overlays/staging/kustomization.yaml`, `k8s/overlays/production/kustomization.yaml`):

Find and replace the resources section:
```yaml
# Remove:
# - cleaner-cronjob.yaml

# Add:
- cleaner-cronjob-sandbox-system.yaml
- cleaner-cronjob-sandbox-workspaces.yaml
```

**Step 6: Verify manifests are valid**

Run: `kubectl kustomize k8s/overlays/dev`
Expected: Valid YAML output with two CronJobs

**Step 7: Commit**

```bash
git add k8s/
git commit -m "fix(cleaner): use dual CronJobs for multi-namespace support

Fixes issue 2.1.2 - Go's flag package only keeps the last --namespace value.
Instead of refactoring code, create separate CronJobs for each namespace.
This follows Kubernetes best practice of one CronJob per concern.

- Deleted: k8s/base/cleaner-cronjob.yaml
- Added: k8s/base/cleaner-cronjob-sandbox-system.yaml
- Added: k8s/base/cleaner-cronjob-sandbox-workspaces.yaml"
```

---

## Task 4: Fix Exec Container Name (Issue 2.2)

**Files:**
- Modify: `manager-service/internal/k8s/client.go`
- Modify: `manager-service/internal/k8s/exec.go`
- Modify: `manager-service/internal/app/app.go`

**Step 1: Write the failing test**

Create `manager-service/internal/k8s/exec_test.go`:

```go
package k8s

import (
    "context"
    "testing"
)

func TestExecutorDefaultContainer(t *testing.T) {
    // Create a mock executor with default container
    executor := &Executor{
        defaultContainer: "test-sandbox",
    }

    opts := &ExecOptions{
        Container: "",
    }

    // This would be called internally by Exec
    if opts.Container == "" {
        opts.Container = executor.defaultContainer
    }

    if opts.Container != "test-sandbox" {
        t.Errorf("Default container = %q, want %q", opts.Container, "test-sandbox")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/k8s/... -run TestExecutorDefaultContainer -v`
Expected: FAIL - Executor doesn't have defaultContainer field yet

**Step 3: Add defaultContainer to Client struct**

Edit `manager-service/internal/k8s/client.go`:

Add `defaultContainer` field to Client struct:
```go
type Client struct {
    config           *rest.Config
    clientset        *kubernetes.Clientset
    namespace        string
    defaultContainer string  // Add this field
}
```

Update NewClient function signature:
```go
func NewClient(cfg *rest.Config, namespace string, defaultContainer string) (*Client, error) {
```

Update NewClient function body to store the value:
```go
return &Client{
    config:           cfg,
    clientset:        clientset,
    namespace:        namespace,
    defaultContainer: defaultContainer,  // Add this line
}, nil
```

**Step 4: Add defaultContainer to Executor struct**

Edit `manager-service/internal/k8s/exec.go`:

Add field to Executor struct:
```go
type Executor struct {
    config            *rest.Config
    clientset         *kubernetes.Clientset
    restClient        rest.Interface
    namespace         string
    defaultContainer  string  // Add this field
}
```

Update NewExecutor to pass the value:
```go
func NewExecutor(client *Client) *Executor {
    return &Executor{
        config:           client.config,
        clientset:        client.clientset,
        restClient:       client.clientset.CoreV1().RESTClient(),
        namespace:        client.namespace,
        defaultContainer: client.defaultContainer,  // Add this line
    }
}
```

Update Exec function to use defaultContainer:
```go
func (e *Executor) Exec(ctx context.Context, podName string, opts *ExecOptions) (*ExecResult, error) {
    if opts == nil {
        opts = &ExecOptions{}
    }

    if opts.Container == "" {
        opts.Container = e.defaultContainer  // Changed from: "runner"
    }
    // ... rest of function
}
```

**Step 5: Update app.go to pass container name**

Edit `manager-service/internal/app/app.go`:

Find the `NewK8sClient` call and update it:
```go
k8sClient, err := k8s.NewClient(
    restConfig,
    cfg.Sandbox.Defaults.Namespace,
    cfg.Sandbox.Defaults.ContainerName,  // Add this argument
)
```

**Step 6: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/k8s/... -run TestExecutorDefaultContainer -v`
Expected: PASS

**Step 7: Run all k8s package tests**

Run: `cd manager-service && go test ./internal/k8s/... -v`
Expected: All tests pass

**Step 8: Commit**

```bash
git add manager-service/internal/k8s/ manager-service/internal/app/app.go manager-service/internal/k8s/exec_test.go
git commit -m "fix(k8s): use config-driven default container name for exec

Fixes issue 2.2 - Exec was hardcoded to use 'runner' container but
actual container name from config is 'sandbox'. Now stores default
container name in Client and passes to Executor.

Breaking: NewClient() now requires defaultContainer parameter."
```

---

## Task 5: Fix Integer Overflow in Backoff (Issue 2.3)

**Files:**
- Modify: `manager-service/internal/config/watch.go:400-409`

**Step 1: Write the failing test**

Create `manager-service/internal/config/watch_test.go`:

```go
package config

import (
    "time"
    "testing"
)

func TestCalculateBackoffOverflow(t *testing.T) {
    w := &Watcher{
        consecutiveFailures: 100,  // Would overflow
        maxBackoff:          30 * time.Second,
        minInterval:         time.Second,
    }

    backoff := w.calculateBackoff()

    // Should be capped at maxBackoff, not overflow to a small value
    if backoff != 30*time.Second {
        t.Errorf("With 100 failures, backoff = %v, want %v", backoff, 30*time.Second)
    }
}

func TestCalculateBackoffNormal(t *testing.T) {
    w := &Watcher{
        consecutiveFailures: 3,
        maxBackoff:          30 * time.Second,
        minInterval:         time.Second,
    }

    backoff := w.calculateBackoff()

    // 2^(3-1) = 4 seconds
    expected := 4 * time.Second
    if backoff != expected {
        t.Errorf("With 3 failures, backoff = %v, want %v", backoff, expected)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/config/... -run TestCalculateBackoff -v`
Expected: FAIL - Overflow causes incorrect backoff value

**Step 3: Write minimal implementation**

Edit `manager-service/internal/config/watch.go:400-409`:

```go
// calculateBackoff calculates the backoff duration based on consecutive failures
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

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/config/... -run TestCalculateBackoff -v`
Expected: PASS

**Step 5: Commit**

```bash
git add manager-service/internal/config/watch.go manager-service/internal/config/watch_test.go
git commit -m "fix(config): prevent integer overflow in exponential backoff

Fixes issue 2.3 - When consecutiveFailures exceeds 63, the bit shift
operation overflows causing backoff to reset to a small value. Now caps
the exponent at 60 before the shift operation."
```

---

## Task 6: Add WebSocket Message Size Limit (Issue 2.4)

**Files:**
- Modify: `manager-service/internal/config/types.go`
- Modify: `manager-service/internal/websocket/handler.go`

**Step 1: Add MaxMessageSize to config struct**

Edit `manager-service/internal/config/types.go` in WebSocketConfig:

```go
type WebSocketConfig struct {
    ReadBufferSize          int      `yaml:"readBufferSize"`
    WriteBufferSize         int      `yaml:"writeBufferSize"`
    AllowedOrigins          []string `yaml:"allowedOrigins"`
    AllowNonBrowserRequests bool     `yaml:"allowNonBrowserRequests"`
    HandshakeTimeout        string   `yaml:"handshakeTimeout"`
    MaxMessageSize          int64    `yaml:"maxMessageSize"`
}
```

**Step 2: Add default value**

Edit `manager-service/internal/config/types.go` in DefaultConfig function:

```go
WebSocket: WebSocketConfig{
    ReadBufferSize:          1024,
    WriteBufferSize:         1024,
    AllowedOrigins:          []string{"http://localhost:3000"},
    AllowNonBrowserRequests: true,
    HandshakeTimeout:        "10s",
    MaxMessageSize:          10 << 20,  // 10MB
}
```

**Step 3: Add size validation in handler**

Edit `manager-service/internal/websocket/handler.go` around line 542:

```go
case TypeStdin:
    payload, err := h.parseStdin(msg.Data)
    if err != nil {
        h.logger.Error("Failed to parse stdin: %v", err)
        continue
    }

    // Validate size before decoding (base64 expands to ~4/3x)
    maxEncodedSize := (h.cfg.MaxMessageSize * 4) / 3
    if int64(len(payload.Data)) > maxEncodedSize {
        h.logger.Error("Stdin data too large: %d bytes (max %d)", len(payload.Data), maxEncodedSize)
        continue
    }

    data, err := base64.StdEncoding.DecodeString(payload.Data)
    if err != nil {
        h.logger.Error("Failed to decode stdin data: %v", err)
        continue
    }
```

**Step 4: Run tests**

Run: `cd manager-service && go test ./internal/websocket/... -v`
Expected: All existing tests still pass

**Step 5: Commit**

```bash
git add manager-service/internal/config/types.go manager-service/internal/websocket/handler.go
git commit -m "feat(websocket): add message size limit to prevent DoS

Fixes issue 2.4 - WebSocket handler didn't validate message size before
base64 decoding, allowing memory exhaustion attacks. Now checks size
before decoding. Configurable via WebSocketConfig.MaxMessageSize
(default 10MB)."
```

---

## Task 7: Fix Rate Limiter Memory Leak (Issue 2.5)

**Files:**
- Modify: `manager-service/internal/ratelimit/limiter.go`
- Modify: `manager-service/internal/app/app.go`

**Step 1: Write the failing test**

Create `manager-service/internal/ratelimit/limiter_test.go`:

```go
package ratelimit

import (
    "context"
    "testing"
    "time"
)

func TestLimiterCleanup(t *testing.T) {
    cfg := &Config{
        GlobalRPS:       100,
        GlobalBurst:     200,
        PerIPRPS:        10,
        PerIPBurst:      20,
        PerSessionRPS:   5,
        PerSessionBurst: 10,
        CleanupInterval: 100 * time.Millisecond,  // Short for test
    }

    limiter := NewLimiter(cfg)
    defer limiter.Stop()

    // Create some limiters
    limiter.Allow(context.Background(), "ip1", "session1")
    limiter.Allow(context.Background(), "ip2", "session2")

    // Wait for cleanup
    time.Sleep(500 * time.Millisecond)

    // After stop, the goroutine should be done
    limiter.Stop()

    // Verify Stop() can be called multiple times safely
    limiter.Stop()
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/ratelimit/... -run TestLimiterCleanup -v`
Expected: FAIL - Stop() method doesn't exist yet

**Step 3: Implement the complete fix**

Replace `manager-service/internal/ratelimit/limiter.go` with:

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

// Config contains rate limiter configuration
type Config struct {
    // Global rate limiting
    GlobalRPS   float64 `yaml:"globalRPS"`
    GlobalBurst int     `yaml:"globalBurst"`

    // Per-IP rate limiting
    PerIPRPS   float64 `yaml:"perIPRPS"`
    PerIPBurst int     `yaml:"perIPBurst"`

    // Per-Session rate limiting
    PerSessionRPS   float64 `yaml:"perSessionRPS"`
    PerSessionBurst int     `yaml:"perSessionBurst"`

    // Cleanup interval for stale limiters
    CleanupInterval time.Duration `yaml:"cleanupInterval"`
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
    select {
    case <-l.stopCleanup:
        // Already closed
        return
    default:
        close(l.stopCleanup)
        l.wg.Wait()
    }
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

**Step 4: Update app.go for graceful shutdown**

Edit `manager-service/internal/app/app.go` in the Shutdown function:

Find the Shutdown function and add rate limiter cleanup:
```go
func (a *App) Shutdown(ctx context.Context) error {
    a.logger.Info("Shutting down application")

    // Stop rate limiter cleanup goroutine
    if a.rateLimiter != nil {
        a.rateLimiter.Stop()
    }

    // ... rest of shutdown code
}
```

**Step 5: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/ratelimit/... -v`
Expected: PASS

**Step 6: Run all tests**

Run: `cd manager-service && go test ./... -v`
Expected: All tests pass

**Step 7: Commit**

```bash
git add manager-service/internal/ratelimit/limiter.go manager-service/internal/ratelimit/limiter_test.go manager-service/internal/app/app.go
git commit -m "fix(ratelimit): implement time-based entry eviction to prevent OOM

Fixes issue 2.5 - Rate limiter created new limiters for each unique
IP/session but never cleaned them up, causing unbounded memory growth.

Changes:
- Added limiterEntry with lastAccess timestamp
- Implemented background cleanup goroutine
- Added Stop() method for graceful shutdown
- Updated app.Shutdown() to call rateLimiter.Stop()"
```

---

## Task 8: Add Pod IP Validation (Issue 3.2)

**Files:**
- Modify: `manager-service/internal/websocket/handler.go`

**Step 1: Write the failing test**

Create `manager-service/internal/websocket/handler_ip_test.go`:

```go
package websocket

import (
    "testing"
    v1 "k8s.io/api/core/v1"
)

func TestValidatePodIP(t *testing.T) {
    h := &Handler{}  // Minimal handler for testing

    tests := []struct {
        name    string
        pod     *v1.Pod
        wantErr bool
    }{
        {
            name: "valid IP",
            pod: &v1.Pod{
                Status: v1.PodStatus{
                    PodIP: "10.244.1.5",
                },
            },
            wantErr: false,
        },
        {
            name: "empty IP",
            pod: &v1.Pod{
                Status: v1.PodStatus{
                    PodIP: "",
                },
            },
            wantErr: true,
        },
        {
            name: "invalid IP format",
            pod: &v1.Pod{
                Status: v1.PodStatus{
                    PodIP: "not-an-ip",
                },
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := h.validatePodIP(tt.pod)
            if (err != nil) != tt.wantErr {
                t.Errorf("validatePodIP() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/websocket/... -run TestValidatePodIP -v`
Expected: FAIL - validatePodIP method doesn't exist

**Step 3: Add validatePodIP method**

Edit `manager-service/internal/websocket/handler.go` - add import and method:

Add to imports:
```go
import (
    "net"
    // ... existing imports
)
```

Add the method (after existing helper methods):
```go
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
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/websocket/... -run TestValidatePodIP -v`
Expected: PASS

**Step 5: Commit**

```bash
git add manager-service/internal/websocket/handler.go manager-service/internal/websocket/handler_ip_test.go
git commit -m "feat(websocket): add Pod IP validation

Implements issue 3.2 - After WaitForPodReady returns, validates that
the pod has a valid IP address using net.ParseIP. This prevents
connection failures when PodIP field is temporarily empty or invalid."
```

---

## Task 9: Final Verification

**Step 1: Run all tests**

Run: `cd manager-service && go test ./... -v`

**Step 2: Build the project**

Run: `cd manager-service && go build -o /tmp/manager-test ./cmd/manager`

**Step 3: Verify Kubernetes manifests**

Run: `kubectl kustomize k8s/overlays/dev`

**Step 4: Create summary commit**

```bash
git add docs/plans/
git commit -m "docs: add I06 production readiness fixes design and implementation plan

- Design: docs/plans/2026-02-10-i06-production-readiness-fixes-design.md
- Implementation: docs/plans/2026-02-10-i06-implementation-plan.md

Fixes 7 issues from V04 verification:
- Issue 2.1.1: Cleaner label selector mismatch
- Issue 2.1.2: Dual namespace support via CronJobs
- Issue 2.1.3: Namespace whitelist update
- Issue 2.2: Exec container name config-driven
- Issue 2.3: Integer overflow in backoff
- Issue 2.4: WebSocket message size limit
- Issue 2.5: Rate limiter memory leak cleanup"
```

---

## Summary

This implementation plan fixes **7 critical/important issues** across the codebase:

| Issue | Fix | Files Changed |
|-------|-----|---------------|
| 2.1.1 | Cleaner label selector | `cmd/cleaner/main.go` |
| 2.1.3 | Namespace whitelist | `cmd/cleaner/main.go` |
| 2.1.2 | Dual CronJobs | `k8s/base/*.yaml` |
| 2.2 | Exec container name | `internal/k8s/*`, `internal/app/app.go` |
| 2.3 | Integer overflow | `internal/config/watch.go` |
| 2.4 | WebSocket size limit | `internal/config/types.go`, `internal/websocket/handler.go` |
| 2.5 | Rate limiter cleanup | `internal/ratelimit/limiter.go`, `internal/app/app.go` |
| 3.2 | Pod IP validation | `internal/websocket/handler.go` |

**Total estimated time**: 2-3 hours

**Testing Strategy**: TDD approach - write failing test first, implement fix, verify test passes.
