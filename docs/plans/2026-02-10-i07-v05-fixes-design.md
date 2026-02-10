# mbos-sandbox I07@V05 Fixes Design

## Overview

This document describes the fixes implemented for mbos-sandbox V05 based on the verification checklist in `docs/verifications/2474-mbos-sandbox-v05.md`.

**Guiding Principles:**
- KISS (Keep It Simple, Stupid)
- DRY (Don't Repeat Yourself)
- SOLID (Single Responsibility, Open/Closed, Liskov Substitution, Interface Segregation, Dependency Inversion)
- YAGNI (You Aren't Gonna Need It)
- No backward compatibility concerns (project not yet released)

## Architecture Decision: Remove shellbridge.K8sAdapter

### Problem

The verification checklist identified that `shellbridge.NewK8sAdapter()` was called but the function didn't exist. This was a fundamental architectural issue - the `shellbridge` package was intended to be a WebSocket client, but a `K8sAdapter` type was being referenced.

### Solution

Rather than creating a new `K8sAdapter` type in the `shellbridge` package, we recognized that:

1. The `k8s.Executor` type already exists and provides all necessary functionality
2. `Executor.Exec()` and `Executor.ExecWithExitCode()` methods match the required interface
3. Adding another abstraction layer would violate YAGNI

### Changes

**Before:**
```go
k8sExecutor := shellbridge.NewK8sAdapter(k8sClient)
```

**After:**
```go
k8sExecutor := k8s.NewExecutor(k8sClient)
```

**Files Modified:**
- `manager-service/internal/app/app.go` - Updated type and initialization
- `manager-service/internal/httpapi/handlers.go` - Updated interface and field types
- `manager-service/internal/httpapi/handlers_validation_test.go` - Updated mock types
- `manager-service/integration/runner_test.go` - Updated test code
- Removed `shellbridge` import from `handlers.go` and `runner_test.go`

---

## P0 Fixes (Blocking Issues)

### P0#1: Missing K8sAdapter Implementation

**Status:** ✅ Fixed by architectural refactoring above

### P0#2: Shell-bridge Binary Path

**File:** `manager-service/internal/k8s/pods.go:383`

**Problem:** Container command referenced `/shellb` but Dockerfile copies binary to `/usr/local/bin/shellb`

**Fix:**
```go
// Before
container.Command = []string{"/shellb"}

// After
container.Command = []string{"/usr/local/bin/shellb"}
```

### P0#3: Hardcoded Container Name in Snapshot

**File:** `manager-service/internal/k8s/snapshot.go`

**Problem:** Container name hardcoded as "runner" instead of using configured default

**Fix:**
```go
// Before
err := c.Exec(execCtx, namespace, podName, "runner", []string{
    "tar", "czf", "-", "-C", "/workspace", ".",
}, StreamOptions{...})

// After
err := c.Exec(execCtx, namespace, podName, c.defaultContainer, []string{
    "tar", "czf", "-", "-C", "/workspace", ".",
}, StreamOptions{...})
```

Applied to both `SnapshotWorkspace()` and `RestoreWorkspace()` methods.

### P0#4: Rate Limiter Stop() Concurrent Safety

**File:** `manager-service/internal/ratelimit/limiter.go`

**Problem:** Multiple calls to `Stop()` could panic when trying to close an already-closed channel

**Fix:** Added `stoppedFlag` boolean field to track stop state:

```go
type Limiter struct {
    // ... existing fields ...
    stopped     sync.Mutex
    stoppedFlag bool // NEW: tracks whether Stop() has been called
}

func (l *Limiter) Stop() {
    l.stopped.Lock()
    defer l.stopped.Unlock()

    if l.stoppedFlag {
        // Already stopped
        return
    }

    l.stoppedFlag = true
    close(l.stopCleanup)
    l.wg.Wait()
}
```

### P0#5: Shell-bridge Session Concurrent Access

**File:** `shell-bridge/internal/pty/session.go`

**Problem:** Session methods (`Write`, `Read`, `Close`, `Resize`) could race when accessed concurrently after Close()

**Fix:** Added `closed` flag and mutex to `Session`:

```go
type Session struct {
    cmd     *exec.Cmd
    pty     *os.File
    shell   string
    workdir string
    closed  bool  // NEW: track closed state
    mu      sync.Mutex // NEW: protect all operations
}

func (s *Session) Write(data []byte) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.closed || s.pty == nil {
        return 0, io.ErrClosedPipe
    }
    return s.pty.Write(data)
}

func (s *Session) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.closed {
        return nil // Already closed
    }
    s.closed = true
    // ... rest of close logic
}
```

Applied to all session methods for consistency.

---

## P1 Fixes (Resource Leaks and Race Conditions)

### P1#6: WebSocket Handler Goroutine Leak

**Status:** ✅ Already correctly handled

The existing `defer` block in `handleConnection()` ensures cleanup runs on all return paths. The `cleanupDone` flag prevents double cleanup. No changes needed.

### P1#7: Session Manager Lifecycle

**Status:** ✅ Design is reasonable

The `Delete()` method directly removes the session from the map. Subsequent `Get()` calls return "not found". This is simpler than maintaining a "terminating" state and follows KISS principle.

### P1#8: K8s Client CheckReady

**Status:** ⚭ Skipped (optional enhancement)

Adding an exec test would increase startup time and complexity. The current Pod list check provides reasonable validation of K8s connectivity. YAGNI - defer until proven necessary.

### P1#9: Rate Limiter Concurrent Access

**File:** `manager-service/internal/ratelimit/limiter.go`

**Problem:** `lastAccess` field in `limiterEntry` was updated without synchronization, causing potential data races

**Fix:** Added mutex to `limiterEntry`:

```go
type limiterEntry struct {
    limiter     *rate.Limiter
    lastAccess  time.Time
    mu          sync.Mutex // NEW: Protects lastAccess
}

// In Allow() method:
limiterEntry.mu.Lock()
limiterEntry.lastAccess = time.Now()
limiterEntry.mu.Unlock()

// In cleanupStaleEntries() method:
entry.mu.Lock()
stale := entry.lastAccess.Before(cutoff)
entry.mu.Unlock()
```

### P1#10: Storage Client Error Handling

**File:** `manager-service/internal/storage/client.go`

**Problem:** `minio.ToErrorResponse()` returns zero value for non-MinIO errors, with `Code == ""`, which could be misinterpreted

**Fix:** Use type assertion instead of `ToErrorResponse()`:

```go
// Before
errResp := minio.ToErrorResponse(err)
if errResp.Code == "NoSuchKey" {
    return false, nil
}

// After
errResp, ok := err.(minio.ErrorResponse)
if ok && errResp.Code == "NoSuchKey" {
    return false, nil
}
```

---

## P2 Fixes (Edge Cases and Optimizations)

### P2#11: PodName Hash Collision

**Status:** ✅ Acceptable risk

Using 10 hex characters (40 bits) from SHA256 gives collision probability of ~1/1 trillion. This is acceptable for production use. No changes needed.

### P2#12: Workdir Validation on Windows

**Status:** ✅ Not applicable

Service runs in Linux containers. Windows path handling is not a concern. No changes needed.

### P2#13: WebSocket Ping Interval

**Status:** ✅ Not a problem

Initial read deadline (30s) is only used during create message waiting. Once connected, read deadline is cleared. Ping ticker (30s) runs in the I/O loop. No conflict exists.

### P2#14: Snapshot Retry Backoff

**Status:** ✅ Acceptable as-is

With `maxSnapshotRetries = 3` and `snapshotBaseBackoff = 500ms`:
- Retry 1: 500ms wait
- Retry 2: 1s wait
- Retry 3: 2s wait
- Total: 3.5s

This is reasonable and doesn't grow indefinitely due to the retry limit.

### P2#15: Buffer Manager Delete Return Value

**File:** `manager-service/internal/buffer/manager.go`

**Enhancement:** Return bool to indicate whether deletion actually happened

```go
// Before
func (m *Manager) Delete(agentThreadID string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.buffers, agentThreadID)
}

// After
// Delete removes the buffer for the given agent thread ID.
// Returns true if the buffer was found and deleted, false if it didn't exist.
func (m *Manager) Delete(agentThreadID string) bool {
    m.mu.Lock()
    defer m.mu.Unlock()
    _, existed := m.buffers[agentThreadID]
    delete(m.buffers, agentThreadID)
    return existed
}
```

---

## Summary of Changes

| Issue | Status | Files Changed |
|-------|--------|---------------|
| P0#1 | ✅ Fixed | app.go, handlers.go, tests |
| P0#2 | ✅ Fixed | pods.go |
| P0#3 | ✅ Fixed | snapshot.go |
| P0#4 | ✅ Fixed | limiter.go |
| P0#5 | ✅ Fixed | session.go |
| P1#6 | ✅ OK | - |
| P1#7 | ✅ OK | - |
| P1#8 | ⚭ Skipped | - |
| P1#9 | ✅ Fixed | limiter.go |
| P1#10 | ✅ Fixed | client.go |
| P2#11 | ✅ OK | - |
| P2#12 | ✅ OK | - |
| P2#13 | ✅ OK | - |
| P2#14 | ✅ OK | - |
| P2#15 | ✅ Fixed | manager.go |

---

## Testing Strategy

1. **Unit Tests**: Run existing unit tests to verify no regressions
   ```bash
   cd manager-service && go test ./internal/...
   ```

2. **Integration Tests**: Run Kubernetes integration tests
   ```bash
   cd manager-service && go test -tags=Integration ./integration/...
   ```

3. **Race Detection**: Run tests with race detector
   ```bash
   go test -race ./internal/...
   ```

4. **Build Verification**: Ensure project builds successfully
   ```bash
   cd manager-service && go build ./cmd/manager
   ```

---

## Design Principles Applied

1. **KISS**: Direct use of `k8s.Executor` instead of creating wrapper
2. **DRY**: No duplicate code - leveraged existing implementations
3. **SOLID**:
   - Single Responsibility: Each fix addresses a specific concern
   - Open/Closed: Extended behavior where needed without modifying core logic
4. **YAGNI**: Skipped optional enhancements that weren't immediately necessary
