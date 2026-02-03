# Sandbox Refactor Design v1

**Date:** 2026-02-03
**Status:** Design Complete
**Author:** Claude + User

---

## Overview

Refactor mbos-sandbox-v1 to support:
1. Specifiable container images with configurable command/args
2. Persistent shell session using tmux
3. Bidirectional WebSocket communication with message buffering
4. Automatic workspace snapshot/restore via MinIO
5. TTL-based pod lifecycle management with cleaner

**Core Principle:** `agent_thread_id` ↔ Pod is 1:1 relationship

---

## Architecture

```
┌─────────────────┐         ┌─────────────────┐         ┌─────────────────┐
│     Client      │◄────────►│     Manager     │◄────────►│      Pod        │
│  (WebSocket)    │         │  (Go Service)   │         │ (User Container)│
└─────────────────┘         │                 │         │                 │
                            │  - Session Mgmt  │         │  /workspace     │
                            │  - Ring Buffer   │         │                 │
                            │  - kubectl exec  │         │  tmux session   │
                            └────────┬─────────┘         └─────────────────┘
                                     │
                                     ▼
                            ┌─────────────────┐
                            │      MinIO      │
                            │  (Snapshots)    │
                            └─────────────────┘

Cleaner (CronJob):
  - Scans every 5 minutes
  - Reads Pod annotations for TTL
  - Directly deletes expired Pods
  - Manager Finalizer handles snapshot
```

---

## Design Decisions Summary

| # | Decision | Choice |
|---|----------|--------|
| 1 | Runtime form | Pod (long-running) |
| 2 | Message buffer | Manager-side ring buffer |
| 3 | Stub container | ❌ None, use kubectl exec directly |
| 4 | Snapshot/Restore | kubectl exec + tar.gz |
| 5 | Client connection | WebSocket |
| 6 | Snapshot trigger | Manager Finalizer (auto on delete) |
| 7 | Restore trigger | Manager initiates after Pod ready |
| 8 | Snapshot key | By `agent_thread_id` |
| 9 | Restore strategy | Always restore if exists |
| 10 | Wait/status | WebSocket push (no separate endpoint) |
| 11 | Compression | tar.gz |
| 12 | Cleaner interval | 5 minutes |
| 13 | Snapshot failure | Log and continue deletion |
| 14 | Environment variables | Fixed + client-specified |
| 15 | Resource limits | Configurable defaults, allow override |
| 16 | Network access | Client-specified (`allow_network_access`) |
| 17 | Security controls (Phase 1) | Network, readonly FS, resources, TTL, capabilities, privileged |
| 18 | Delete operation | ❌ No delete API, TTL only |
| 19 | Authentication | ❌ None (internal trusted) |
| 20 | Persistent shell | tmux |
| 21 | Idle detection | Mixed: WebSocket connected OR stdin activity |
| 22 | TTL sync | Pod annotations (Cleaner reads) |
| 23 | Delete hook | Kubernetes Finalizer |

---

## WebSocket Protocol

### Connection Flow

```
1. Client ──WebSocket connect──► Manager
2. Client ──send──► {"type": "create", "agent_thread_id": "...", ...}
3. Manager ──push──► {"type": "status", "state": "creating", "progress": 0.1}
4. Manager ──push──► {"type": "status", "state": "restoring", "progress": 0.6}
5. Manager ──push──► {"type": "status", "state": "ready", "progress": 1.0}
6. Client ──send──► {"type": "stdin", "data": "ls\n"}
7. Manager ──push──► {"type": "stdout", "data": "..."}
```

### Message Types

| Direction | Type | Description |
|-----------|------|-------------|
| C→M | `create` | Create/connect to sandbox |
| C→M | `stdin` | Standard input |
| M→C | `status` | Status update |
| M→C | `stdout` | Standard output |
| M→C | `stderr` | Standard error |
| M→C | `exit` | Process exit code |
| M→C | `error` | Error message |

### Create Request

```json
{
  "type": "create",
  "agent_thread_id": "at_abc123",
  "image": "python:3.11",
  "command": ["/bin/bash"],
  "env": {"MY_VAR": "value"},
  "config": {
    "allow_network_access": false,
    "readonly_filesystem": false,
    "cpu_limit": "2",
    "memory_limit": "1Gi",
    "idle_timeout": "30m",
    "max_lifetime": "24h",
    "drop_all_capabilities": false,
    "allow_privileged": false
  }
}
```

---

## tmux Session Management

### Pod Startup Script

```bash
#!/bin/sh
set -e

# 1. Check tmux exists
if ! which tmux > /dev/null 2>&1; then
  echo "ERROR: tmux is required but not found in this image"
  exit 1
fi

# 2. Create tmux session with user command
if [ -n "$SANDBOX_COMMAND" ]; then
  tmux new-session -d -s sandbox $SANDBOX_COMMAND
else
  tmux new-session -d -s sandbox /bin/bash
fi

# 3. Keep container running
tail -f /dev/null
```

### Manager Attach

```bash
kubectl exec -i <pod> -- tmux attach -t sandbox
```

### Process Status Check

```bash
kubectl exec <pod> -- tmux has-session -t sandbox
echo $?  # 0 = exists, 1 = not found (process exited)
```

---

## Manager Internal Architecture

### Core Components

```go
type Manager struct {
    k8sClient *k8s.Client
    sessions  *session.Store       // agent_thread_id -> Session
    buffers   *buffer.RingBuffer   // agent_thread_id -> CircularBuffer
    storage   *storage.Client      // MinIO/S3
    conns     *websocket.ConnManager
}

type Session struct {
    AgentThreadID     string
    PodName           string
    PodNamespace      string
    State             string  // creating, restoring, ready, offline
    Image             string
    Command           []string
    Env               map[string]string
    Config            SecurityConfig
    CreatedAt         time.Time
    LastActivityAt    time.Time
    ExpiresAt         time.Time
    ClientConnected   bool
}

type SecurityConfig struct {
    AllowNetworkAccess    bool
    ReadonlyFilesystem    bool
    CPULimit              string
    MemoryLimit           string
    IdleTimeout           time.Duration
    MaxLifetime           time.Duration
    DropAllCapabilities   bool
    AllowPrivileged       bool
}
```

### Message Buffer (Ring)

- Per-`agent_thread_id` circular buffer
- Default capacity: 10,000 messages
- Overwrites oldest when full
- Sends backlog on client reconnect

---

## Snapshot & Restore

### Snapshot (Pod Deletion)

```bash
# Manager executes via kubectl exec
kubectl exec <pod> -- tar -czf - /workspace

# Data flow
Pod stdout → Manager receives → Upload to MinIO
# Key: snapshots/{workspace_id}/{project_id}/{agent_thread_id}/workspace.tar.gz
```

### Restore (Pod Ready)

```bash
# 1. Manager downloads from MinIO
# 2. Send to Pod via kubectl exec
kubectl exec -i <pod> -- tar -xzf - -C /workspace
```

### Finalizer Flow

```
1. Cleaner calls Delete Pod
2. Kubernetes detects Finalizer, doesn't delete immediately
3. Manager receives deletionTimestamp event
4. Manager executes snapshot
5. Snapshot complete → Manager removes Finalizer
6. Kubernetes deletes Pod
```

---

## TTL & Activity Tracking

### Activity Update

```go
// Manager updates on:
// - WebSocket connection
// - stdin message received
// - tmux output received

func (m *Manager) UpdateActivity(agentThreadID string) {
    session.LastActivityAt = time.Now()
    session.ClientConnected = true

    // Sync to Pod Annotation
    k8sClient.PatchPodAnnotation(podName, map[string]string{
        "last_activity_at": session.LastActivityAt.Format(time.RFC3339),
        "expires_at":      session.ExpiresAt.Format(time.RFC3339),
    })
}
```

### TTL Calculation

- `max_lifetime`: Hard limit,回收 regardless of activity
- `idle_timeout`: Soft limit,回收 after disconnect + timeout

```go
func (s *Session) IsExpired() bool {
    // Check max lifetime
    if time.Since(s.CreatedAt) > s.Config.MaxLifetime {
        return true
    }

    // Check idle timeout (only when disconnected)
    if !s.ClientConnected && s.Config.IdleTimeout > 0 {
        idleTime := time.Since(s.LastActivityAt)
        return idleTime > s.Config.IdleTimeout
    }

    return false
}
```

### Cleaner Logic

```go
// Cleaner reads Pod annotations directly
func (c *Cleaner) IsExpired(pod *v1.Pod) bool {
    expiresAtStr := pod.Annotations["expires_at"]
    expiresAt, _ := time.Parse(time.RFC3339, expiresAtStr)
    return time.Now().After(expiresAt)
}
```

---

## Pod Lifecycle

```
1. Client sends create message
   ↓
2. Manager checks MinIO for existing snapshot
   ↓
3. Manager creates Pod
   - Labels: agent_thread_id=xxx
   - Annotations: expires_at=xxx, last_activity_at=xxx
   - Env: SANDBOX_COMMAND, etc.
   - SecurityContext: from config
   - Finalizer: sandbox-manager/finalizer
   ↓
4. Pod starts
   - Init script checks tmux
   - No tmux → exit 1 → Manager detects failure → notify Client error
   - Has tmux → create tmux session → tail -f /dev/null
   ↓
5. Manager detects Pod Ready
   - If snapshot exists → kubectl exec tar -xzf to restore
   - Update state: restoring → ready
   ↓
6. Notify Client ready
   ↓
7. Client sends stdin
   - Manager updates last_activity_at
   - Sync to Pod annotation
   - kubectl exec tmux attach to send data
   ↓
8. tmux output → Manager → Client
   - Write to ring buffer
   ↓
9. Client disconnects WebSocket
   - Manager marks ClientConnected=false
   - last_activity_at unchanged (idle计时开始)
   ↓
10. Cleaner scans every 5 min
    - Read Pod annotation expires_at
    - Expired → Delete Pod
    ↓
11. Manager Finalizer triggered
    - kubectl exec tar -czf snapshot
    - Upload MinIO
    - Log result
    - Remove Finalizer
    ↓
12. Pod deleted
```

---

## Security Controls (Phase 1)

| Control | Type | Description |
|---------|------|-------------|
| `allow_network_access` | bool | Allow external network access |
| `readonly_filesystem` | bool | Mount root as read-only (except /workspace) |
| `cpu_limit` | string | CPU limit (e.g., "2") |
| `memory_limit` | string | Memory limit (e.g., "1Gi") |
| `idle_timeout` | duration | Idle timeout before回收 (e.g., "30m") |
| `max_lifetime` | duration | Maximum lifetime (e.g., "24h") |
| `drop_all_capabilities` | bool | Drop all Linux capabilities |
| `allow_privileged` | bool | Allow privileged mode |

**Kubernetes Implementation:**

| Control | K8s Feature |
|---------|-------------|
| Network | NetworkPolicy |
| Readonly FS | securityContext.readOnlyRootFilesystem |
| CPU/Memory | resources.limits |
| Capabilities | securityContext.capabilities.drop |
| Privileged | securityContext.privileged |

**Phase 2 (Future):**
- Domain/IP whitelist
- Filesystem path controls
- Process limits
- Fine-grained capability controls

---

## Error Handling

| Scenario | Handling |
|----------|----------|
| Snapshot fails | Log error, continue deletion |
| Restore fails | Log error, start with empty workspace |
| WebSocket disconnect | Client reconnects, receive backlog |
| Manager restart | Client must re-create (buffer lost) |
| Pod crash | K8s restarts, Manager re-attaches |
| MinIO unavailable | Snapshot fails, system degrades |
| Concurrent create | Return existing session |

---

## API Reference

### WebSocket Endpoint

```
GET /ws
```

### Message Schema

**Create (C→M):**
```json
{
  "type": "create",
  "agent_thread_id": "string",
  "image": "string",
  "command": ["string"],
  "env": {"key": "value"},
  "config": SecurityConfig
}
```

**Stdin (C→M):**
```json
{
  "type": "stdin",
  "data": "base64_encoded_string"
}
```

**Status (M→C):**
```json
{
  "type": "status",
  "state": "creating|restoring|ready|error",
  "message": "string",
  "progress": 0.0-1.0
}
```

**Stdout/Stderr (M→C):**
```json
{
  "type": "stdout|stderr",
  "data": "base64_encoded_string"
}
```

**Exit (M→C):**
```json
{
  "type": "exit",
  "code": 0-255
}
```

**Error (M→C):**
```json
{
  "type": "error",
  "message": "string"
}
```

---

## Storage

### Snapshot Key Pattern

```
{s3_bucket}/snapshots/{workspace_id}/{project_id}/{agent_thread_id}/workspace.tar.gz
```

### Example

```
mbos-sandbox-snapshots/snapshots/ws_abc123/proj_def456/at_ghi789/workspace.tar.gz
```

---

## Cleaner (CronJob)

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: sandbox-cleaner
  namespace: sandbox-system
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: cleaner
            image: sandbox-manager:latest
            command: ["/cleaner"]
            args:
            - --namespace=sandbox
```

---

## Configuration

### Manager Config

```yaml
server:
  port: 8080

k8s:
  namespace: sandbox
  pod_namespace: sandbox

storage:
  endpoint: minio:9000
  access_key: ${MINIO_ACCESS_KEY}
  secret_key: ${MINIO_SECRET_KEY}
  bucket: mbos-sandbox-snapshots
  use_ssl: false

buffer:
  capacity: 10000  # messages per session

defaults:
  idle_timeout: 30m
  max_lifetime: 24h
  cpu_limit: "2"
  memory_limit: "1Gi"

tmux:
  check_on_startup: true
```

---

## Future Enhancements (Phase 2)

1. **Network Controls**
   - Domain whitelist (e.g., `["api.openai.com", "*.github.com"]`)
   - IP CIDR whitelist

2. **Filesystem Controls**
   - Writable path whitelist
   - File size limits
   - Total size limits

3. **Process Controls**
   - Max process count
   - Command whitelist

4. **Metrics & Observability**
   - Prometheus metrics
   - Session lifecycle events
   - Snapshot/restore success rates

5. **Explicit Delete API**
   - Allow clients to terminate sessions early

6. **Multi-manager Support**
   - Shared state via Redis
   - Leader election
