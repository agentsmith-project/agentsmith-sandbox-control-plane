# mbos-sandbox I05 Improvements Design Document

**Date**: 2026-02-09
**Task**: I05 - mbos-sandbox改进设计和编码实现
**Based on**: v03收口检查报告 (8332-mbos-sandbox-v03.md)
**Approach**: Full cleanup (Option C) - Fix all 28 issues before completion

---

## Overview

### Objective

Address all 28 issues from the v03 verification report to prepare mbos-sandbox for production release.

### Scope

- **28 total issues**: 12 Critical, 14 Important, 2 Minor
- **Priority approach**: P0 (4) → P1 (5) → P2 (6) → P3 (13)
- **Testing strategy**: Critical-path TDD for P0/P1, test-after for P2/P3

### Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Shell-bridge implementation | Minimal internal implementation | Protocol already defined, client written |
| PTY sessions | Single session per server | Matches one-pod-per-session architecture |
| Authentication | Fail-closed with dev mode | Security + developer experience |
| Testing | Critical-path TDD | Ensures correctness for critical fixes |

### Estimated Timeline

7-10 days for full completion across 7 phases.

---

## Issue Breakdown

### P0 - Critical Issues (Blocking Release)

**P0-1: Authentication Bypass**
- **File**: `manager-service/internal/auth/middleware.go:30-35`
- **Problem**: When no service keys configured, all requests pass without authentication
- **Fix**: Remove implicit bypass, add `ALLOW_UNAUTHENTICATED` environment variable
- **Test**: Unauthenticated request returns 401 when keys missing and dev mode off

**P0-2: Shell-Bridge Server Missing**
- **File**: `images/runner/Dockerfile:1-6`
- **Problem**: Dockerfile references non-existent `shell-bridge/` directory
- **Fix**: Create complete shell-bridge server implementation
- **Structure**:
  - `shell-bridge/cmd/shellb/main.go` - entry point
  - `shell-bridge/internal/pty/pty.go` - PTY session management
  - `shell-bridge/internal/protocol/frame.go` - binary frame protocol
  - `shell-bridge/internal/server/server.go` - WebSocket handler

**P0-3: Shell-Bridge Client Nil Pointer**
- **File**: `manager-service/internal/shellbridge/client.go:58-66`
- **Problem**: `c.conn` nil causes panic
- **Fix**: Add connection state validation

**P0-4: WebSocket I/O Incomplete**
- **File**: `manager-service/internal/websocket/handler.go:442-446, 508-510`
- **Problem**: I/O forwarding not implemented, channels never written
- **Fix**: Complete `forwardIO` function to bridge client ↔ shell-bridge
- **Note**: Fixes 9.1 (unused channels) as a side effect

**P0-5: Binary Frame Integer Overflow** (elevated from P1)
- **File**: `manager-service/internal/shellbridge/frame.go:38-42`
- **Problem**: `uint32` to `int` conversion can overflow
- **Fix**: Add max frame size limit (10MB), validate before conversion

---

### P1 - High Priority (Stability)

**P1-1: Session Manager Race Condition**
- **File**: `manager-service/internal/session/manager.go:26-48, 85-111`
- **Problem**: `Create()` vs `GetOrCreate()` concurrency causes duplicates
- **Fix**: Add existence check in `Create()`

**P1-2: Finalizer Goroutine Management**
- **File**: `manager-service/internal/finalizer/handler.go:84-102`
- **Problem**: No `WaitGroup`, cannot gracefully shutdown
- **Fix**: Add `sync.WaitGroup` and `Shutdown()` method

**P1-3: Shell-Bridge ReceiveOutput Silent Errors**
- **File**: `manager-service/internal/shellbridge/client.go:92-96`
- **Problem**: Malformed frames silently skipped, infinite loop
- **Fix**: Return error instead of `continue`

**P1-4: Shell-Bridge Busy-Wait Loop**
- **File**: `manager-service/internal/shellbridge/client.go:76-101`
- **Problem**: `select` with `default` causes 100% CPU
- **Fix**: Use deadline-based `ReadMessage()`

---

### P2 - Medium Priority (Reliability)

**P2-1: Ring Buffer Memory Limits**
- **File**: `manager-service/internal/buffer/ring.go:7-11, 29-41`
- **Problem**: Limits message count but not individual message size
- **Fix**: Add max message size limit (1MB)

**P2-2: WebSocket Cleanup Flag Race**
- **File**: `manager-service/internal/websocket/handler.go:86-101`
- **Problem**: `cleanupDone` flag not atomic
- **Fix**: Use `sync.Mutex` or make cleanup idempotent

**P2-3: WebSocket Auth Query Parameters**
- **File**: `manager-service/internal/app/app.go:306-319`
- **Problem**: Auth via query params leaks in logs
- **Fix**: Use HTTP headers, constant-time comparison

**P2-4: Cleaner Namespace Validation**
- **File**: `manager-service/cmd/cleaner/main.go:22, 52-54`
- **Problem**: Accepts any namespace without validation
- **Fix**: Add namespace whitelist validation

**P2-5: TTL Parse Error Handling**
- **File**: `manager-service/cmd/cleaner/main.go:73-79`
- **Problem**: Parse failures skip pods forever
- **Fix**: Implement fallback (delete pods older than X days)

**P2-6: Finalizer Snapshot Failure Handling**
- **File**: `manager-service/internal/finalizer/handler.go:143-147`
- **Problem**: Snapshot failure still removes finalizer, data loss
- **Fix**: Add snapshot retry logic

---

### P3 - Low Priority (Improvements)

1. Storage Client Error Validation (5.2)
2. Shell-Bridge Client Mutex Protection (6.2)
3. Upload Handler Body Close (7.2)
4. WithTimeout Cancel Function (8.2)
5. PatchActivity Error Handling (9.2)
6. Config Validation Upper Bounds (10.2)
7. Download Context Cancellation Race (11.2)
8. WebSocket MarshalJSON Error (12.2)
9. Session GetOrCreate Zombie Sessions (13.2)
10. Buffer Manager Delete Race (14.2)
11. Debug Config Endpoint Exposure (1.3)
12. Config Clone Deep Copy Validation (2.3)
13. Config Edge Case Tests (4.1, 4.2)

---

## Architecture

### Shell-Bridge Component Architecture

```
┌─────────────────┐    WebSocket    ┌──────────────────┐
│  Manager Svc    │◄────────────────►│  Shell-Bridge    │
│  (Client)       │   JSON+Binary    │  (Server)        │
└────────┬────────┘                  └────────┬─────────┘
         │                                    │
         │ sessionManager                     │ PTY
         ▼                                    ▼
┌─────────────────┐                  ┌──────────────────┐
│  WebSocket      │                  │  Shell Process   │
│  Handler        │                  │  (bash/zsh/etc)  │
└─────────────────┘                  └──────────────────┘
```

### Session Lifecycle Flow

1. **Pod Creation**: Manager creates pod with `ShellType`
2. **Shell-Bridge Start**: Pod starts, server listens on `:8080`
3. **WebSocket Connect**: Handler receives client connection
4. **Session Lookup**: `sessionManager.GetOrCreate(sessionID)`
5. **Shell-Bridge Connect**: Handler connects to `podIP:8080`
6. **Exec Command**: Client sends `{"type": "exec", ...}`
7. **I/O Forwarding**: `forwardIO` bridges client ↔ shell-bridge
8. **Session Cleanup**: Cleanup goroutine removes finalizers

### Concurrent Access Protection

- Session Manager: `sync.RWMutex` on sessions map
- Shell-Bridge Client: `sync.Mutex` on `conn` field
- Buffer Manager: `sync.RWMutex` on buffers map
- WebSocket Handler: `sync.Mutex` on cleanup operations

---

## Error Handling Strategy

### P0/P1 Issues - Critical Path (TDD)

For each critical issue, write tests first:

1. **Authentication Bypass**: Fail-closed with dev mode
2. **Shell-Bridge Server**: Complete implementation from scratch
3. **Binary Frame Overflow**: Size validation
4. **Client Nil Pointer**: Connection state validation
5. **Session Manager Race**: Concurrent access protection
6. **ReceiveOutput Errors**: Return errors immediately
7. **Busy-Wait Loop**: Deadline-based reads

### P2/P3 Issues - Test-After

Implement fix first, then write regression tests.

---

## Implementation Phases

### Phase 1: Shell-Bridge Foundation (Days 1-2)
1. Create shell-bridge server structure
2. Implement PTY session management
3. Implement WebSocket handler with binary protocol
4. Add flags and entry point
5. Integration test: server + client communication

### Phase 2: Critical Security Fixes (Days 2-3)
1. Fix authentication bypass with dev mode flag
2. Add binary frame size validation
3. Fix client nil pointer issues
4. Add client mutex protection
5. Security tests

### Phase 3: WebSocket I/O Completion (Days 3-4)
1. Complete `forwardIO` function implementation
2. Connect shell-bridge WebSocket from handler
3. Stream stdin/stdout/stderr correctly
4. Handle connection errors and reconnection
5. End-to-end integration tests

### Phase 4: Stability & Concurrency (Days 4-5)
1. Fix session manager race conditions
2. Add finalizer goroutine shutdown
3. Fix receiveOutput silent errors
4. Fix busy-wait loop
5. Add mutex protection throughout
6. Concurrency stress tests

### Phase 5: Reliability Improvements (Days 5-6)
1. Add ring buffer message size limits
2. Fix WebSocket cleanup flag race
3. Move auth to headers
4. Add cleaner namespace validation
5. Implement TTL parse fallback
6. Add snapshot retry logic
7. Resource limit tests

### Phase 6: Code Quality & Polish (Days 6-7)
1. Fix remaining P3 issues
2. Add missing error logging
3. Fix config validation gaps
4. Add missing tests
5. Documentation updates

### Phase 7: Verification & Release Prep (Day 7)
1. Full test suite run
2. Integration testing with real Kubernetes
3. Security audit verification
4. Performance testing
5. Release notes generation

---

## Risk Mitigation

### High-Risk Areas

| Risk | Mitigation |
|------|------------|
| Shell-Bridge creation from scratch | TDD approach, comprehensive protocol tests |
| Concurrent access fixes | Existing code review patterns, stress testing |
| WebSocket I/O completion | Integration tests with real pods |
| Authentication changes | Feature flags, canary deployment |

### Rollback Strategy

- Keep feature flags for major changes
- Deploy to canary environment first
- Monitor error rates and latency
- Document rollback procedure

---

## Success Criteria

- All 28 issues addressed
- All tests passing
- Integration tests pass with real Kubernetes cluster
- Security audit verification complete
- No performance regression
- Documentation updated

---

**Design Complete**: 2026-02-09
**Next Step**: Create implementation plan using superpowers:writing-plans
