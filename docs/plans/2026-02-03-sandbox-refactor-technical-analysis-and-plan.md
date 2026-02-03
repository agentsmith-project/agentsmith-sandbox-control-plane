# Sandbox Refactor: Technical Analysis Report and Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor mbos-sandbox-v1 to support specifiable container images, bidirectional stub communication with manager, forwarding service for clients, MinIO workspace persistence, and cleaner-based pod lifecycle management.

**Architecture:** Move from "static sandbox for exec" to "dynamic session runtime manager" with stub-agent communication pattern, workspace snapshot/restore via MinIO, and session-based pod lifecycle with TTL and cleaner回收.

**Tech Stack:** Go 1.21, Kubernetes client-go, MinIO/S3 SDK, WebSocket/spdy for streaming, gRPC/HTTP for stub communication.

---

# Part 1: Technical Analysis Report

## 1. Current State Analysis

### 1.1 Current Architecture (mbos-sandbox-v1)

**Component Structure:**
- `manager-service/cmd/manager/main.go` - Entry point
- `internal/app/app.go` - Application orchestrator
- `internal/k8s/` - Kubernetes client wrapper, pod management, exec
- `internal/httpapi/` - REST API handlers
- `internal/auth/` - Service key authentication

**Current Capabilities:**
1. HTTP REST API for sandbox CRUD operations
2. Kubernetes pod lifecycle management (create/delete/wait)
3. Command execution via `kubectl exec` wrapper
4. File upload/download via tar.gz
5. TTL-based pod expiration with activity tracking
6. Service key authentication
7. Prometheus metrics and health checks

**Key Limitations (Gap Analysis):**

| Requirement | Current State | Gap |
|-------------|---------------|-----|
| Specifiable container image | Hardcoded `tail -f /dev/null` | ❌ Need dynamic command/args support |
| Stub service in pod | No stub implementation | ❌ Need stub agent that communicates with manager |
| Bidirectional shell-like I/O | One-shot exec commands | ❌ Need continuous stdin/stdout/stderr streaming |
| Client forwarding service | No WebSocket/spdy streaming | ❌ Need bidirectional streaming endpoint |
| Session-based connection | Simple sessionId→pod mapping | ❌ Need session state management with reconnection |
| MinIO workspace persistence | No external storage integration | ❌ Need MinIO/S3 client for snapshot/restore |
| Cleaner-based回收 | TTL-based but no offline detection | ❌ Need cleaner with client disconnection detection |
| Status during wait | Pod ready only | ❌ Need granular status (creating/resturing/ready) |

### 1.2 Broader Context (mbos-server-v1)

**Related Components:**
- `mbos_backend-v1` - Control plane for agent threads, sessions, checkpoints
- `mbos-edge-v1` - WebSocket gateway for agent connections
- `mbos-shared-v1` - Shared types and utilities

**Relevant Design Decisions:**
1. **Session Model**: agent_thread (logic) → turn (per-round) → session (runtime)
2. **Storage**: Postgres for critical data, MinIO/S3 for large objects (snapshots)
3. **Connect Token**: One-time/short-lived token for session binding
4. **Checkpoint**: Per-agent-thread latest recovery point in MinIO
5. **Network**: Internal agents outbound-connect to edge (not inbound from manager)

**Integration Points:**
- Backend calls manager to create/delete session runtimes
- Backend orchestrates snapshot via manager's `/files/download` API
- Manager must support command/args for long-running agent processes
- Connect token injection via env/secret for security

---

## 2. Proposed Architecture

### 2.1 Component Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           mbos-server-v1 Platform                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌──────────────┐         ┌──────────────┐         ┌──────────────┐       │
│  │   Frontend   │         │   Backend    │         │     Edge     │       │
│  │   (Client)   │◄────────►│  (Control)   │◄────────►│   (WS GW)    │       │
│  └──────────────┘         └──────────────┘         └──────────────┘       │
│          │                          │                         │            │
│          │     1. Connect           │                         │            │
│          ├─────────────────────────►│                         │            │
│          │   (image, config)        │                         │            │
│          │                          │                         │            │
│          │     2. Session ID        │                         │            │
│          │◄─────────────────────────┤                         │            │
│          │                          │                         │            │
│          │     3. Wait/Status       │                         │            │
│          ├─────────────────────────►│                         │            │
│          │                          │                         │            │
│          │     4. Shell I/O         │                         │            │
│          ├─────────────────────────►│                         │            │
│          │   (WebSocket/spdy)       │                         │            │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 5. Create/Delete Pod
                                    │    Snapshot/Restore
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        mbos-sandbox-v1 (manager-service)                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │                     HTTP API Layer                                   │  │
│  │  POST /v1/sessions          - Create session (return session_id)     │  │
│  │  GET  /v1/sessions/{id}/wait - Wait for ready (stream status)       │  │
│  │  GET  /v1/sessions/{id}/shell - Shell I/O (WebSocket/spdy upgrade)  │  │
│  │  POST /v1/sessions/{id}/touch - Extend TTL                          │  │
│  │  DELETE /v1/sessions/{id}    - Delete session                       │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                    │                                        │
│  ┌────────────────────────────────▼─────────────────────────────────────┐  │
│  │                     Session Manager                                  │  │
│  │  - Session state (creating/restoring/ready/terminating)             │  │
│  │  - Client connection tracking                                        │  │
│  │  - TTL management                                                    │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                    │                                        │
│  ┌────────────────────────────────▼─────────────────────────────────────┐  │
│  │                     Stub Client                                      │  │
│  │  - Connects to stub in pod via gRPC/HTTP                             │  │
│  │  - Multiplexes client shell I/O to stub                              │  │
│  │  - Streams stdin/stdout/stderr                                       │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                    │                                        │
│  ┌────────────────────────────────▼─────────────────────────────────────┐  │
│  │                     Storage Manager                                  │  │
│  │  - MinIO/S3 client                                                   │  │
│  │  - Snapshot: /workspace → tar.gz → MinIO                             │  │
│  │  - Restore: MinIO → tar.gz → /workspace                              │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                    │                                        │
│  ┌────────────────────────────────▼─────────────────────────────────────┐  │
│  │                     Kubernetes Client                                │  │
│  │  - Create pod with specifiable image/command/args                    │  │
│  │  - Inject env: SESSION_ID, CONNECT_TOKEN, STUB_ENDPOINT             │  │
│  │  - Pod lifecycle management                                          │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ Kubernetes API
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Kubernetes Cluster                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  Pod (per session)                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐    │   │
│  │  │ Container (user-specified image)                            │    │   │
│  │  │  ┌───────────────────────────────────────────────────────┐  │    │   │
│  │  │  │ User Agent/Process                                     │  │    │   │
│  │  │  │ - Runs in /workspace                                  │  │    │   │
│  │  │  │ - stdin/stdout/stderr connected to stub               │  │    │   │
│  │  │  └───────────────────────────────────────────────────────┘  │    │   │
│  │  │                                                                   │    │   │
│  │  │  ┌───────────────────────────────────────────────────────┐  │    │   │
│  │  │  │ Stub Service (sidecar or init-container)              │  │    │   │
│  │  │  │ - gRPC/HTTP server                                     │  │    │   │
│  │  │  │ - Connects to manager on startup                      │  │    │   │
│  │  │  │ - Pipes: manager ↔ user process                       │  │    │   │
│  │  │  │ - Snapshot/restore coordination                        │  │    │   │
│  │  │  └───────────────────────────────────────────────────────┘  │    │   │
│  │  └─────────────────────────────────────────────────────────────┘    │   │
│  │                                                                       │   │
│  │  Volume: /workspace (emptyDir)                                        │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  Cleaner CronJob:                                                           │
│  - Scans pods for offline sessions                                          │
│  - Deletes pods exceeding idle TTL                                         │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ Snapshot/Restore
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                              MinIO / S3                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│  Bucket: mbos-sandbox-snapshots                                              │
│  Key Pattern: {workspace_id}/{project_id}/{session_id}/workspace.tar.gz    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Protocol Flow

**User Story Flow:**

```
Client                    Manager                Kubernetes                    MinIO
  │                          │                         │                         │
  │ 1. POST /v1/sessions      │                         │                         │
  │    (image, config)        │                         │                         │
  ├─────────────────────────►│                         │                         │
  │                          │ 2. Create Pod            │                         │
  │                          │    (image, env)          │                         │
  │                          ├─────────────────────────►│                         │
  │                          │                         │ 3. Stub starts           │
  │                          │                         │    connects to manager   │
  │                          │◄─────────────────────────┤                         │
  │ 4. Return session_id     │                         │                         │
  │◄─────────────────────────┤                         │                         │
  │                          │                         │ 5. Check for snapshot   │
  │                          │─────────────────────────►│                         │
  │                          │    If exists:            │                         │
  │                          │                         │ 6. Download              │
  │                          │                         │◄─────────────────────────┤
  │                          │                         │ 7. Restore /workspace    │
  │                          │                         │    (untar)               │
  │                          │                         │ 8. Notify: Ready         │
  │                          │◄─────────────────────────┤                         │
  │                          │                         │ 9. Start user process    │
  │                          │                         │    connect pipes         │
  │ 10. GET /v1/sessions/{id}/wait                       │                         │
  ├─────────────────────────►│                         │                         │
  │    (stream status)        │ Status: creating         │                         │
  │◄─────────────────────────┤                         │                         │
  │    (stream status)        │ Status: restoring        │                         │
  │◄─────────────────────────┤                         │                         │
  │    (stream status)        │ Status: ready            │                         │
  │◄─────────────────────────┤                         │                         │
  │                          │                         │                         │
  │ 11. GET /v1/sessions/{id}/shell (upgrade to WS/spdy)                     │
  ├─────────────────────────►│                         │                         │
  │                          │ 12. Connect to stub      │                         │
  │                          ├─────────────────────────►│                         │
  │ 13. Bidirectional I/O    │                         │                         │
  │◄─────────────────────────┤                         │                         │
  │    (stdin/stdout/stderr) │                         │                         │
  │                          │                         │                         │
  │ 14. Client disconnects    │                         │                         │
  ├─────────────────────────►│                         │                         │
  │                          │ Mark session offline     │                         │
  │                          │ (Pod continues running)  │                         │
  │                          │                         │                         │
  │ ... (time passes) ...    │                         │                         │
  │                          │                         │                         │
  │ 15. Cleaner runs         │                         │                         │
  │                          │ Scans offline pods       │                         │
  │                          ├─────────────────────────►│                         │
  │                          │ Found offline > TTL      │                         │
  │                          │ 16. Trigger snapshot     │                         │
  │                          ├─────────────────────────►│                         │
  │                          │ 17. Upload /workspace    │                         │
  │                          │────────────────────────────────────────────────►│
  │                          │ 18. Delete pod           │                         │
  │                          ├─────────────────────────►│                         │
  │                          │                         │                         │
  │ 16. Client reconnects     │                         │                         │
  │    with session_id        │                         │                         │
  ├─────────────────────────►│                         │                         │
  │                          │ Pod not found            │                         │
  │                          │ 17. Create new pod       │                         │
  │                          ├─────────────────────────►│                         │
  │                          │                         │ 18. Download snapshot   │
  │                          │                         │◄─────────────────────────┤
  │                          │                         │ 19. Restore /workspace  │
  │                          │                         │ 20. Ready                │
  │ 21. Ready to reconnect    │                         │                         │
  │◄─────────────────────────┤                         │                         │
```

### 2.3 Data Model

**Session State (in-memory + Postgres for persistence):**

```go
type SessionState string

const (
    SessionStateCreating   SessionState = "creating"   // Pod being created
    SessionStateRestoring  SessionState = "restoring"  // Downloading/extracting snapshot
    SessionStateReady      SessionState = "ready"      // Ready for client connection
    SessionStateAttached   SessionState = "attached"   // Client connected
    SessionStateOffline    SessionState = "offline"    // Client disconnected, pod running
    SessionStateTerminating SessionState = "terminating" // Being deleted
)

type Session struct {
    ID               string            `json:"session_id"`
    WorkspaceID      string            `json:"workspace_id"`
    ProjectID        string            `json:"project_id"`
    AgentThreadID    string            `json:"agent_thread_id,omitempty"`
    EndUserID        string            `json:"end_user_id,omitempty"`
    Image            string            `json:"image"`
    Command          []string          `json:"command,omitempty"`
    Args             []string          `json:"args,omitempty"`
    Env              map[string]string `json:"env,omitempty"`
    Config           SessionConfig     `json:"config"`

    // Runtime state
    State            SessionState      `json:"state"`
    PodName          string            `json:"pod_name,omitempty"`
    PodNamespace     string            `json:"pod_namespace"`
    StubEndpoint     string            `json:"stub_endpoint,omitempty"`

    // Client tracking
    ClientConnected  bool              `json:"client_connected"`
    ClientConnectedAt *time.Time       `json:"client_connected_at,omitempty"`
    ClientDisconnectedAt *time.Time    `json:"client_disconnected_at,omitempty"`

    // TTL
    CreatedAt        time.Time         `json:"created_at"`
    LastActivityAt   time.Time         `json:"last_activity_at"`
    ExpiresAt        time.Time         `json:"expires_at"`

    // Snapshot
    SnapshotRef      *string           `json:"snapshot_ref,omitempty"`
    RestoredFrom     *string           `json:"restored_from,omitempty"`
}

type SessionConfig struct {
    // Network policy
    AllowNetworkAccess bool             `json:"allow_network_access"`
    NetworkPolicy       NetworkPolicy   `json:"network_policy,omitempty"`

    // Resources
    ResourceRequests    v1.ResourceList `json:"resource_requests,omitempty"`
    ResourceLimits      v1.ResourceList `json:"resource_limits,omitempty"`

    // Storage
    WorkspaceSize      string           `json:"workspace_size,omitempty"` // e.g., "1Gi"

    // TTL
    IdleTimeout        time.Duration    `json:"idle_timeout,omitempty"`    // default 30m
    MaxLifetime        time.Duration    `json:"max_lifetime,omitempty"`   // default 24h
}
```

**Snapshot Metadata (in MinIO/S3):**

```go
type SnapshotMetadata struct {
    WorkspaceID      string    `json:"workspace_id"`
    ProjectID        string    `json:"project_id"`
    AgentThreadID    string    `json:"agent_thread_id"`
    SessionID        string    `json:"session_id"`      // Source session
    CreatedAt        time.Time `json:"created_at"`
    SizeBytes        int64     `json:"size_bytes"`
    Checksum         string    `json:"checksum"`        // SHA256
    Version          string    `json:"version"`         // Snapshot format version
}
```

---

## 3. Key Design Decisions

### 3.1 Stub Service Architecture

**Decision:** Stub runs as a **sidecar container** in the same pod.

**Rationale:**
- Sidecar has access to pod's emptyDir /workspace volume
- Can communicate with manager via Kubernetes service (cluster networking)
- Lifecycle tied to pod (auto-terminates with pod)
- No need for complex init-container patterns

**Alternative Considered:** Init-container that execs into main container.
**Rejected:** Race conditions during startup, harder to manage streaming I/O.

### 3.2 Stub ↔ Manager Communication Protocol

**Decision:** **gRPC with bidirectional streaming** for shell I/O.

**Rationale:**
- Strong typing, code generation
- Built-in streaming support
- Efficient binary protocol
- Better than raw WebSocket for service-to-service communication

**Fallback Option:** HTTP/2 with custom streaming protocol.

### 3.3 Client ↔ Manager Communication Protocol

**Decision:** **WebSocket with spdy fallback** for client shell I/O.

**Rationale:**
- Native browser support
- Bidirectional streaming
- Upgrade from HTTP (easy integration with existing API)
- spdy as fallback for CLI tools

### 3.4 Storage Architecture for Snapshots

**Decision:** **Proxy upload through manager** (not direct from pod to MinIO).

**Rationale:**
- Unified audit/usage tracking (all bytes flow through manager)
- No MinIO credentials in pods (security)
- Rate limiting at manager level
- Consistent with mbos-backend-v1 design decisions

**Flow:** Pod → Manager (tar stream) → MinIO

### 3.5 Offline Detection

**Decision:** **Client disconnection tracking + offline timeout**.

**Implementation:**
- Manager tracks `client_connected` state per session
- On WebSocket close, mark `offline = true` and record `offline_at`
- Cleaner scans for: `offline == true && (now - offline_at) > idle_timeout`
- Note: Pod continues running after client disconnect (for reconnection)

### 3.6 Status Updates During Wait

**Decision:** **Server-Sent Events (SSE)** for wait endpoint.

**Rationale:**
- Unidirectional streaming from server to client
- Native browser support
- Easy to parse (text/event-stream)
- Allows detailed status updates:
  - `creating` - Pod is being created
  - `pulling` - Image is being pulled
  - `starting_stub` - Stub is starting
  - `restoring` - Downloading/extracting snapshot (with progress %)
  - `ready` - Ready for connection

---

## 4. API Specification

### 4.1 Create Session

```
POST /v1/sessions
Authorization: Bearer <service-key>
Content-Type: application/json

Request:
{
  "image": "ghcr.io/myorg/my-agent:latest",
  "command": ["/app/agent-runner"],
  "args": ["--log-level", "info"],
  "env": {
    "MY_VAR": "value"
  },
  "config": {
    "allow_network_access": false,
    "workspace_size": "1Gi",
    "idle_timeout": "30m",
    "max_lifetime": "24h",
    "resource_requests": {
      "cpu": "500m",
      "memory": "512Mi"
    }
  },
  // Optional: for restore
  "agent_thread_id": "at_123",
  "restore_from_checkpoint": true
}

Response 201:
{
  "session_id": "sess_abc123",
  "state": "creating",
  "created_at": "2026-02-03T10:00:00Z",
  "expires_at": "2026-02-03T11:00:00Z"
}
```

### 4.2 Wait for Ready

```
GET /v1/sessions/{session_id}/wait
Authorization: Bearer <service-key>

Response (text/event-stream):
data: {"state":"creating","message":"Creating pod...","created_at":"..."}

data: {"state":"pulling","message":"Pulling image...","progress":0.5}

data: {"state":"restoring","message":"Restoring workspace...","progress":0.3}

data: {"state":"ready","message":"Ready for connection","created_at":"..."}

[Connection closes or client disconnects]
```

### 4.3 Shell Connection (WebSocket Upgrade)

```
GET /v1/sessions/{session_id}/shell
Authorization: Bearer <service-key>
Upgrade: websocket
Connection: Upgrade

[WebSocket established]

WebSocket frames (binary or text):
- Client → Server: stdin data
- Server → Client: stdout data (frame type 1)
- Server → Client: stderr data (frame type 2)
- Server → Client: exit code (frame type 3, final)

Frame format (JSON):
{
  "type": "stdout" | "stderr" | "exit",
  "data": "<base64 or text>",
  "seq": 123
}
```

### 4.4 Touch (Extend TTL)

```
POST /v1/sessions/{session_id}/touch
Authorization: Bearer <service-key>

Response 200:
{
  "expires_at": "2026-02-03T11:30:00Z"
}
```

### 4.5 Delete Session

```
DELETE /v1/sessions/{session_id}
Authorization: Bearer <service-Key>

Response 200:
{
  "message": "Session deleted",
  "snapshot_ref": "s3://mbos-sandbox-snapshots/..."
}
```

---

## 5. Implementation Phases

The implementation is divided into **4 phases** to allow incremental delivery and testing.

### Phase 1: Foundation (Core Infrastructure)
- Stub service implementation (gRPC server)
- Manager stub client (gRPC client)
- Pod spec builder with configurable command/args
- Session state management

### Phase 2: Storage (Snapshot/Restore)
- MinIO client integration
- Snapshot orchestration (pod → manager → MinIO)
- Restore orchestration (MinIO → manager → pod)
- Snapshot metadata storage

### Phase 3: API Layer (Client Communication)
- Session create API
- Wait/SSE endpoint with status streaming
- WebSocket shell endpoint
- Session touch/delete

### Phase 4: Lifecycle (Cleaner & TTL)
- Offline detection
- Cleaner CronJob implementation
- TTL enforcement
- Graceful shutdown with snapshot

---

# Part 2: Implementation Plan

## Task Organization

The plan is organized into **4 phases** with **27 total tasks**. Each task is designed to be completed in 2-5 minutes.

---

## Phase 1: Foundation (Core Infrastructure)

### Task 1: Add gRPC dependencies to go.mod

**Files:**
- Modify: `manager-service/go.mod`

**Step 1: Add gRPC dependencies**

```bash
cd manager-service
go get google.golang.org/grpc@v1.60.0
go get google.golang.org/protobuf@v1.32.0
```

**Step 2: Run go mod tidy**

```bash
go mod tidy
```

**Step 3: Verify dependencies**

```bash
grep -E "(grpc|protobuf)" go.mod
```

Expected: Lines showing grpc and protobuf versions

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add gRPC and protobuf dependencies"
```

---

### Task 2: Create gRPC protocol definition

**Files:**
- Create: `manager-service/api/stub/v1/stub.proto`
- Create: `manager-service/api/stub/v1/README.md`

**Step 1: Write the proto file**

Create `manager-service/api/stub/v1/stub.proto`:

```protobuf
syntax = "proto3";

package stub.v1;

option go_package = "github.com/vibe-kanban/mbos-sandbox-v1/manager-service/api/stub/v1;stubv1";

// Stub service runs inside the pod and communicates with manager
service StubService {
  // Called by manager to get stub status
  rpc GetStatus(StatusRequest) returns (StatusResponse);

  // Bidirectional stream for shell I/O
  rpc Shell(stream ShellRequest) returns (stream ShellResponse);

  // Trigger snapshot (upload workspace)
  rpc Snapshot(SnapshotRequest) returns (SnapshotResponse);

  // Trigger restore (download and extract workspace)
  rpc Restore(RestoreRequest) returns (RestoreResponse);

  // Health check
  rpc Health(HealthRequest) returns (HealthResponse);
}

message StatusRequest {
  string session_id = 1;
}

message StatusResponse {
  string status = 1;  // "starting", "ready", "snapshotting", "restoring", "error"
  string message = 2;
  int64 pid = 3;      // Main process PID if running
}

message ShellRequest {
  oneof input {
    string stdin = 1;
    bool resize = 2;   // Terminal resize
    WindowSize window_size = 3;
  }
  string session_id = 4;
}

message ShellResponse {
  oneof output {
    bytes stdout = 1;
    bytes stderr = 2;
    int32 exit_code = 3;
    StatusUpdate status = 4;
  }
}

message StatusUpdate {
  string state = 1;
  string message = 2;
}

message WindowSize {
  uint32 rows = 1;
  uint32 cols = 2;
}

message SnapshotRequest {
  string session_id = 1;
  string workspace_path = 2;  // Default: /workspace
}

message SnapshotResponse {
  bool success = 1;
  string error = 2;
  int64 size_bytes = 3;
  string checksum = 4;
}

message RestoreRequest {
  string session_id = 1;
  bytes tar_data = 2;  // Streamed in chunks
  string workspace_path = 3;
  int64 total_size = 4;
}

message RestoreResponse {
  bool success = 1;
  string error = 2;
  int64 extracted_bytes = 3;
}

message HealthRequest {}

message HealthResponse {
  bool healthy = 1;
  string version = 2;
}
```

**Step 2: Create README**

Create `manager-service/api/stub/v1/README.md`:

```markdown
# Stub Protocol (gRPC)

This directory contains the gRPC protocol definition for the stub service that runs inside sandbox pods.

## Service: StubService

The stub service runs as a sidecar container in each pod and communicates with the sandbox manager.

### Methods

- **GetStatus**: Get current stub status (starting, ready, snapshotting, etc.)
- **Shell**: Bidirectional streaming for stdin/stdout/stderr
- **Snapshot**: Trigger workspace snapshot (tar.gz upload)
- **Restore**: Restore workspace from tar.gz download
- **Health**: Health check

## Code Generation

```bash
# Generate Go code
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       api/stub/v1/stub.proto
```

## Communication Flow

1. Pod starts, stub container starts
2. Stub connects to manager (via environment variable: `STUB_ENDPOINT`)
3. Stub registers session and gets status
4. Manager can trigger shell, snapshot, restore operations
```

**Step 3: Add buf.gen.yaml for code generation**

Create `manager-service/api/buf.gen.yaml`:

```yaml
version: v1
plugins:
  - plugin: go
    out: ../..
    opt:
      - paths=source_relative
  - plugin: go-grpc
    out: ../..
    opt:
      - paths=source_relative
```

**Step 4: Create Makefile target for proto generation**

Add to `manager-service/Makefile` (create if not exists):

```makefile
.PHONY: proto
proto: ## Generate protobuf code
	@echo "Generating protobuf code..."
	@which protoc > /dev/null || (echo "protoc not installed. Install from https://grpc.io/docs/protoc-installation/"; exit 1)
	@protoc --version | grep -q "libprotoc 3" || (echo "protoc version 3.x required"; exit 1)
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       api/stub/v1/stub.proto
	@echo "Protobuf code generated successfully"
```

**Step 5: Commit**

```bash
git add api/
git commit -m "feat: add gRPC stub protocol definition"
```

---

### Task 3: Generate Go code from protobuf

**Files:**
- Create: `manager-service/api/stub/v1/stub.pb.go`
- Create: `manager-service/api/stub/v1/stub_grpc.pb.go`

**Step 1: Install protoc and plugins**

```bash
# Check if protoc is installed
protoc --version || echo "Need to install protoc"

# Install Go plugins if not present
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

**Step 2: Generate code**

```bash
cd manager-service
make proto
```

Expected: Output showing successful generation, files created in `api/stub/v1/`

**Step 3: Verify generated files**

```bash
ls -la api/stub/v1/*.pb.go
```

Expected: `stub.pb.go` and `stub_grpc.pb.go` exist

**Step 4: Test compilation**

```bash
go build ./api/stub/v1/...
```

Expected: No errors

**Step 5: Commit**

```bash
git add api/stub/v1/*.pb.go
git commit -m "feat: generate gRPC Go code from protobuf"
```

---

### Task 4: Create stub service implementation (in new directory)

**Files:**
- Create: `stub-service/main.go`
- Create: `stub-service/go.mod`
- Create: `stub-service/Dockerfile`

**Step 1: Create stub-service directory and go.mod**

Create `stub-service/go.mod`:

```go
module github.com/vibe-kanban/mbos-sandbox-v1/stub-service

go 1.21

require (
	github.com/vibe-kanban/mbos-sandbox-v1/manager-service/api/stub/v1 v0.0.0
	google.golang.org/grpc v1.60.0
	google.golang.org/protobuf v1.32.0
)

replace github.com/vibe-kanban/mbos-sandbox-v1/manager-service => ../manager-service
```

**Step 2: Write stub service main**

Create `stub-service/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/api/stub/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type StubServer struct {
	stubv1.UnimplementedStubServiceServer

	sessionID    string
	managerAddr  string
	managerConn  *grpc.ClientConn
	managerCli   stubv1.StubServiceClient

	// Process management
	cmd           *exec.Cmd
	cmdMutex      sync.Mutex
	cmdStarted    bool

	// Shell streams
	activeStreams sync.Map // map[*context.CancelFunc]bool
}

func NewStubServer(sessionID, managerAddr string) *StubServer {
	return &StubServer{
		sessionID:   sessionID,
		managerAddr: managerAddr,
	}
}

func (s *StubServer) StartManagerConnection(ctx context.Context) error {
	var err error
	s.managerConn, err = grpc.DialContext(ctx, s.managerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to manager: %w", err)
	}
	s.managerCli = stubv1.NewStubServiceClient(s.managerConn)
	return nil
}

func (s *StubServer) GetStatus(ctx context.Context, req *stubv1.StatusRequest) (*stubv1.StatusResponse, error) {
	s.cmdMutex.Lock()
	defer s.cmdMutex.Unlock()

	status := "ready"
	if s.cmd != nil && s.cmd.Process != nil {
		status = "running"
	} else if s.cmdStarted {
		status = "exited"
	}

	var pid int64
	if s.cmd != nil && s.cmd.Process != nil {
		pid = int64(s.cmd.Process.Pid)
	}

	return &stubv1.StatusResponse{
		Status:  status,
		Message: "Stub is operational",
		Pid:     pid,
	}, nil
}

func (s *StubServer) Shell(stream stubv1.StubService_ShellServer) error {
	// Ensure process is started
	if err := s.ensureProcess(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	s.activeStreams.Store(&cancel, true)
	defer s.activeStreams.Delete(&cancel)

	// Start goroutine to forward stdout/stderr to stream
	doneCh := make(chan error, 2)

	// Stdout forwarding
	go func() {
		doneCh <- s.forwardOutputStream(ctx, stream, s.cmd.Stdout, "stdout")
	}()

	// Stderr forwarding
	go func() {
		doneCh <- s.forwardOutputStream(ctx, stream, s.cmd.Stderr, "stderr")
	}()

	// Handle incoming stdin from stream
	for {
		req, err := stream.Recv()
		if err != nil {
			cancel() // Signal output forwarders to stop
			break
		}

		switch input := req.Input.(type) {
		case *stubv1.ShellRequest_Stdin:
			if s.cmd.Stdin != nil {
				if _, err := fmt.Fprint(s.cmd.Stdin, input.Stdin); err != nil {
					return fmt.Errorf("failed to write stdin: %w", err)
				}
			}
		case *stubv1.ShellRequest_Resize:
			// Handle terminal resize if needed
			// This would require setting up a pseudo-terminal
		}
	}

	// Wait for output forwarders to finish
	for i := 0; i < 2; i++ {
		if err := <-doneCh; err != nil {
			log.Printf("Output forwarder error: %v", err)
		}
	}

	// Wait for command to finish and send exit code
	if s.cmd != nil {
		state, err := s.cmd.Process.Wait()
		if err == nil {
			exitCode := int32(state.ExitCode())
			stream.Send(&stubv1.ShellResponse{
				Output: &stubv1.ShellResponse_ExitCode{ExitCode: exitCode},
			})
		}
	}

	return nil
}

func (s *StubServer) ensureProcess() error {
	s.cmdMutex.Lock()
	defer s.cmdMutex.Unlock()

	if s.cmd != nil {
		return nil // Already started
	}

	// Get command from environment
	command := os.Getenv("SANDBOX_COMMAND")
	if command == "" {
		command = "/bin/sh"
	}

	args := os.Getenv("SANDBOX_ARGS")
	var argsList []string
	if args != "" {
		// Simple split; in production, parse properly
		argsList = []string{args}
	}

	// Create command with pipes
	s.cmd = exec.Command(command, argsList...)

	stdin, err := s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := s.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	s.cmd.Stdin = stdin
	s.cmd.Stdout = stdout
	s.cmd.Stderr = stderr
	s.cmd.Dir = "/workspace"

	// Start command
	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}
	s.cmdStarted = true

	return nil
}

func (s *StubServer) forwardOutputStream(ctx context.Context, stream stubv1.StubService_ShellServer, pipe interface{}, streamType string) error {
	reader, ok := pipe.(interface{ Read([]byte) (int, error) })
	if !ok {
		return fmt.Errorf("pipe is not a reader")
	}

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, err := reader.Read(buf)
		if n > 0 {
			var resp *stubv1.ShellResponse
			if streamType == "stdout" {
				resp = &stubv1.ShellResponse{
					Output: &stubv1.ShellResponse_Stdout{Stdout: buf[:n]},
				}
			} else {
				resp = &stubv1.ShellResponse{
					Output: &stubv1.ShellResponse_Stderr{Stderr: buf[:n]},
				}
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		if err != nil {
			return nil // EOF or closed pipe
		}
	}
}

func (s *StubServer) Snapshot(ctx context.Context, req *stubv1.SnapshotRequest) (*stubv1.SnapshotResponse, error) {
	workspacePath := req.WorkspacePath
	if workspacePath == "" {
		workspacePath = "/workspace"
	}

	// Create tar.gz of workspace
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("snapshot-%s.tar.gz", s.sessionID))
	defer os.Remove(tempFile)

	cmd := exec.Command("tar", "czf", tempFile, "-C", workspacePath, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		return &stubv1.SnapshotResponse{
			Success: false,
			Error:   fmt.Sprintf("tar failed: %v: %s", err, output),
		}, nil
	}

	// Get file size and checksum
	info, _ := os.Stat(tempFile)

	return &stubv1.SnapshotResponse{
		Success:   true,
		SizeBytes: info.Size(),
	}, nil
}

func (s *StubServer) Restore(ctx context.Context, req *stubv1.RestoreRequest) (*stubv1.RestoreResponse, error) {
	workspacePath := req.WorkspacePath
	if workspacePath == "" {
		workspacePath = "/workspace"
	}

	// Ensure workspace directory exists
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		return &stubv1.RestoreResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to create workspace: %v", err),
		}, nil
	}

	// Extract tar.gz (streamed)
	// For simplicity, write to temp file first then extract
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("restore-%s.tar.gz", s.sessionID))
	defer os.Remove(tempFile)

	if err := os.WriteFile(tempFile, req.TarData, 0644); err != nil {
		return &stubv1.RestoreResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to write tar file: %v", err),
		}, nil
	}

	cmd := exec.Command("tar", "xzf", tempFile, "-C", workspacePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return &stubv1.RestoreResponse{
			Success: false,
			Error:   fmt.Sprintf("extract failed: %v: %s", err, output),
		}, nil
	}

	return &stubv1.RestoreResponse{
		Success: true,
	}, nil
}

func (s *StubServer) Health(ctx context.Context, req *stubv1.HealthRequest) (*stubv1.HealthResponse, error) {
	return &stubv1.HealthResponse{
		Healthy: true,
		Version: "1.0.0",
	}, nil
}

func main() {
	// Get configuration from environment
	sessionID := os.Getenv("SESSION_ID")
	if sessionID == "" {
		log.Fatal("SESSION_ID environment variable is required")
	}

	managerAddr := os.Getenv("MANAGER_ENDPOINT")
	if managerAddr == "" {
		managerAddr = "sandbox-manager:8080"
	}

	stubPort := os.Getenv("STUB_PORT")
	if stubPort == "" {
		stubPort = "9000"
	}

	// Create stub server
	stub := NewStubServer(sessionID, managerAddr)

	// Connect to manager
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := stub.StartManagerConnection(ctx); err != nil {
		log.Fatalf("Failed to connect to manager: %v", err)
	}
	defer stub.managerConn.Close()

	log.Printf("Stub connected to manager at %s", managerAddr)

	// Start gRPC server
	lis, err := net.Listen("tcp", ":"+stubPort)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	stubv1.RegisterStubServiceServer(grpcServer, stub)

	log.Printf("Stub service listening on port %s", stubPort)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
```

**Step 3: Create Dockerfile**

Create `stub-service/Dockerfile`:

```dockerfile
# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum* ./
COPY ../manager-service ./manager-service

# Download dependencies
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o stub-service .

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache tar ca-certificates

WORKDIR /app

COPY --from=builder /build/stub-service .

# Create workspace directory
RUN mkdir -p /workspace

EXPOSE 9000

ENTRYPOINT ["/app/stub-service"]
```

**Step 4: Commit**

```bash
git add stub-service/
git commit -m "feat: add stub service implementation"
```

---

### Task 5: Create session types and state management

**Files:**
- Create: `manager-service/internal/session/types.go`
- Create: `manager-service/internal/session/manager.go`

**Step 1: Write session types**

Create `manager-service/internal/session/types.go`:

```go
package session

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

type State string

const (
	StateCreating    State = "creating"
	StateRestoring   State = "restoring"
	StateReady       State = "ready"
	StateAttached    State = "attached"
	StateOffline     State = "offline"
	StateTerminating State = "terminating"
)

type Session struct {
	// Identity
	ID            string `json:"session_id" db:"session_id"`
	WorkspaceID   string `json:"workspace_id" db:"workspace_id"`
	ProjectID     string `json:"project_id" db:"project_id"`
	AgentThreadID string `json:"agent_thread_id,omitempty" db:"agent_thread_id"`
	EndUserID     string `json:"end_user_id,omitempty" db:"end_user_id"`

	// Pod specification
	Image           string            `json:"image"`
	Command         []string          `json:"command,omitempty"`
	Args            []string          `json:"args,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Config          Config            `json:"config"`

	// Runtime state
	State           State             `json:"state"`
	PodName         string            `json:"pod_name,omitempty"`
	PodNamespace    string            `json:"pod_namespace"`
	StubEndpoint    string            `json:"stub_endpoint,omitempty"`

	// Client tracking
	ClientConnected     bool        `json:"client_connected"`
	ClientConnectedAt   *time.Time  `json:"client_connected_at,omitempty"`
	ClientDisconnectedAt *time.Time `json:"client_disconnected_at,omitempty"`

	// TTL
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	LastActivityAt time.Time  `json:"last_activity_at" db:"last_activity_at"`
	ExpiresAt      time.Time  `json:"expires_at" db:"expires_at"`

	// Snapshot
	SnapshotRef   *string `json:"snapshot_ref,omitempty" db:"snapshot_ref"`
	RestoredFrom  *string `json:"restored_from,omitempty" db:"restored_from"`
}

type Config struct {
	// Network
	AllowNetworkAccess bool           `json:"allow_network_access"`
	NetworkPolicy      NetworkPolicy  `json:"network_policy,omitempty"`

	// Resources
	ResourceRequests corev1.ResourceList `json:"resource_requests,omitempty"`
	ResourceLimits   corev1.ResourceList `json:"resource_limits,omitempty"`

	// Storage
	WorkspaceSize string `json:"workspace_size,omitempty"`

	// TTL
	IdleTimeout  time.Duration `json:"idle_timeout,omitempty"`
	MaxLifetime  time.Duration `json:"max_lifetime,omitempty"`
}

type NetworkPolicy struct {
	DenyAll       bool     `json:"deny_all,omitempty"`
	AllowedCIDRs  []string `json:"allowed_cidrs,omitempty"`
	AllowedDNS    []string `json:"allowed_dns,omitempty"`
}

func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

func (s *Session) IsIdle(idleTimeout time.Duration) bool {
	if s.ClientConnected {
		return false
	}
	if s.ClientDisconnectedAt == nil {
		return false
	}
	return time.Since(*s.ClientDisconnectedAt) > idleTimeout
}

func (s *Session) ShouldExpireNow() bool {
	if s.IsExpired() {
		return true
	}
	if s.Config.IdleTimeout > 0 && s.IsIdle(s.Config.IdleTimeout) {
		return true
	}
	return false
}
```

**Step 2: Write session manager**

Create `manager-service/internal/session/manager.go`:

```go
package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	// Configuration
	defaultConfig Config
}

func NewManager(defaultConfig Config) *Manager {
	return &Manager{
		sessions:      make(map[string]*Session),
		defaultConfig: defaultConfig,
	}
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Generate session ID
	sessionID := "sess_" + uuid.New().String()

	// Calculate expiration
	now := time.Now()
	expiresAt := now.Add(m.defaultConfig.MaxLifetime)
	if req.Config.MaxLifetime > 0 {
		expiresAt = now.Add(req.Config.MaxLifetime)
	}

	// Merge config
	config := m.defaultConfig
	if req.Config.WorkspaceSize != "" {
		config.WorkspaceSize = req.Config.WorkspaceSize
	}
	if req.Config.IdleTimeout > 0 {
		config.IdleTimeout = req.Config.IdleTimeout
	}
	if req.Config.MaxLifetime > 0 {
		config.MaxLifetime = req.Config.MaxLifetime
	}
	if req.Config.ResourceRequests != nil {
		config.ResourceRequests = req.Config.ResourceRequests
	}
	if req.Config.ResourceLimits != nil {
		config.ResourceLimits = req.Config.ResourceLimits
	}
	if req.Config.AllowNetworkAccess {
		config.AllowNetworkAccess = true
	}

	session := &Session{
		ID:              sessionID,
		WorkspaceID:     req.WorkspaceID,
		ProjectID:       req.ProjectID,
		AgentThreadID:   req.AgentThreadID,
		EndUserID:       req.EndUserID,
		Image:           req.Image,
		Command:         req.Command,
		Args:            req.Args,
		Env:             req.Env,
		Config:          config,
		State:           StateCreating,
		PodNamespace:    req.PodNamespace,
		CreatedAt:       now,
		LastActivityAt:  now,
		ExpiresAt:       expiresAt,
	}

	m.sessions[sessionID] = session
	return session, nil
}

func (m *Manager) Get(sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[sessionID]
	return s, ok
}

func (m *Manager) UpdateState(sessionID string, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	s.State = state
	s.LastActivityAt = time.Now()
	return nil
}

func (m *Manager) SetPodInfo(sessionID, podName, stubEndpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	s.PodName = podName
	s.StubEndpoint = stubEndpoint
	return nil
}

func (m *Manager) MarkClientConnected(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	now := time.Now()
	s.ClientConnected = true
	s.ClientConnectedAt = &now
	s.ClientDisconnectedAt = nil
	s.State = StateAttached
	s.LastActivityAt = now
	return nil
}

func (m *Manager) MarkClientDisconnected(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	now := time.Now()
	s.ClientConnected = false
	s.ClientDisconnectedAt = &now
	s.State = StateOffline
	return nil
}

func (m *Manager) Touch(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Extend expiration
	s.LastActivityAt = time.Now()
	if s.Config.IdleTimeout > 0 {
		s.ExpiresAt = time.Now().Add(s.Config.IdleTimeout)
	}
	return nil
}

func (m *Manager) Delete(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

func (m *Manager) ListIdleSessions(idleTimeout time.Duration) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var idle []*Session
	for _, s := range m.sessions {
		if s.IsIdle(idleTimeout) {
			idle = append(idle, s)
		}
	}
	return idle
}

func (m *Manager) ListExpiredSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var expired []*Session
	now := time.Now()
	for _, s := range m.sessions {
		if s.ExpiresAt.Before(now) {
			expired = append(expired, s)
		}
	}
	return expired
}

type CreateRequest struct {
	WorkspaceID   string            `json:"workspace_id"`
	ProjectID     string            `json:"project_id"`
	AgentThreadID string            `json:"agent_thread_id,omitempty"`
	EndUserID     string            `json:"end_user_id,omitempty"`

	Image         string            `json:"image"`
	Command       []string          `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`

	PodNamespace  string            `json:"pod_namespace"`
	Config        Config            `json:"config"`

	RestoreFromCheckpoint *string   `json:"restore_from_checkpoint,omitempty"`
}
```

**Step 3: Commit**

```bash
git add manager-service/internal/session/
git commit -m "feat: add session type and manager"
```

---

### Task 6: Create stub client in manager

**Files:**
- Create: `manager-service/internal/stub/client.go`
- Create: `manager-service/internal/stub/shell.go`

**Step 1: Write stub client**

Create `manager-service/internal/stub/client.go`:

```go
package stub

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/api/stub/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn   *grpc.ClientConn
	client stubv1.StubServiceClient
}

func NewClient(ctx context.Context, endpoint string, timeout time.Duration) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to stub: %w", err)
	}

	return &Client{
		conn:   conn,
		client: stubv1.NewStubServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) GetStatus(ctx context.Context, sessionID string) (*stubv1.StatusResponse, error) {
	return c.client.GetStatus(ctx, &stubv1.StatusRequest{
		SessionId: sessionID,
	})
}

func (c *Client) Health(ctx context.Context) (*stubv1.HealthResponse, error) {
	return c.client.Health(ctx, &stubv1.HealthRequest{})
}

func (c *Client) Snapshot(ctx context.Context, sessionID, workspacePath string) (*stubv1.SnapshotResponse, error) {
	return c.client.Snapshot(ctx, &stubv1.SnapshotRequest{
		SessionId:     sessionID,
		WorkspacePath: workspacePath,
	})
}

func (c *Client) Restore(ctx context.Context, sessionID string, tarData []byte, workspacePath string) (*stubv1.RestoreResponse, error) {
	return c.client.Restore(ctx, &stubv1.RestoreRequest{
		SessionId:     sessionID,
		TarData:       tarData,
		WorkspacePath: workspacePath,
	})
}
```

**Step 2: Write shell stream handler**

Create `manager-service/internal/stub/shell.go`:

```go
package stub

import (
	"context"
	"io"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/api/stub/v1"
)

// ShellStream handles bidirectional shell I/O with a stub
type ShellStream struct {
	client stubv1.StubService_ShellClient
	ctx    context.Context
	cancel context.CancelFunc
}

func NewShellStream(ctx context.Context, client stubv1.StubServiceClient, sessionID string) (*ShellStream, error) {
	ctx, cancel := context.WithCancel(ctx)

	stream, err := client.Shell(ctx)
	if err != nil {
		cancel()
		return nil, err
	}

	// Send initial request to establish stream
	if err := stream.Send(&stubv1.ShellRequest{
		Input: &stubv1.ShellRequest_Stdin{Stdin: ""},
		SessionId: sessionID,
	}); err != nil {
		cancel()
		return nil, err
	}

	return &ShellStream{
		client: stream,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (s *ShellStream) Close() error {
	s.cancel()
	return nil
}

// WriteStdin writes data to the process stdin
func (s *ShellStream) WriteStdin(data []byte) error {
	return s.client.Send(&stubv1.ShellRequest{
		Input:     &stubv1.ShellRequest_Stdin{Stdin: string(data)},
		SessionId: "", // Session ID sent on first request only
	})
}

// ReadOutput reads from stdout or stderr. Returns the data, stream type ("stdout" or "stderr"), and error.
func (s *ShellStream) ReadOutput() ([]byte, string, error) {
	resp, err := s.client.Recv()
	if err != nil {
		return nil, "", err
	}

	switch output := resp.Output.(type) {
	case *stubv1.ShellResponse_Stdout:
		return output.Stdout, "stdout", nil
	case *stubv1.ShellResponse_Stderr:
		return output.Stderr, "stderr", nil
	case *stubv1.ShellResponse_ExitCode:
		return nil, "exit", io.EOF
	default:
		return nil, "", nil
	}
}

// StreamOutput continuously reads output and sends to the provided channels
func (s *ShellStream) StreamOutput(stdoutCh, stderrCh chan<- []byte, exitCh chan<- int32) error {
	defer close(stdoutCh)
	defer close(stderrCh)
	defer close(exitCh)

	for {
		data, streamType, err := s.ReadOutput()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch streamType {
		case "stdout":
			select {
			case stdoutCh <- data:
			case <-s.ctx.Done():
				return s.ctx.Err()
			}
		case "stderr":
			select {
			case stderrCh <- data:
			case <-s.ctx.Done():
				return s.ctx.Err()
			}
		case "exit":
			// Exit code is in the next response
			resp, err := s.client.Recv()
			if err == nil {
				if ec, ok := resp.Output.(*stubv1.ShellResponse_ExitCode); ok {
					select {
					case exitCh <- ec.ExitCode:
					case <-s.ctx.Done():
					}
				}
			}
			return nil
		}
	}
}
```

**Step 3: Commit**

```bash
git add manager-service/internal/stub/
git commit -m "feat: add stub client for manager"
```

---

### Task 7: Update pod builder to support stub sidecar

**Files:**
- Modify: `manager-service/internal/k8s/pods.go`

**Step 1: Read current pods.go**

```bash
cat manager-service/internal/k8s/pods.go
```

**Step 2: Add stub sidecar container to pod spec**

After reading the file, modify it to add the stub sidecar. Add new function:

```go
// buildStubContainer creates the stub sidecar container
func (c *Client) buildStubContainer(sessionID, managerEndpoint string) corev1.Container {
	return corev1.Container{
		Name:            "stub",
		Image:           c.config.StubImage, // Add to config
		ImagePullPolicy: corev1.PullIfNotPresent,

		Env: []corev1.EnvVar{
			{
				Name: "SESSION_ID",
				Value: sessionID,
			},
			{
				Name: "MANAGER_ENDPOINT",
				Value: managerEndpoint,
			},
			{
				Name: "STUB_PORT",
				Value: "9000",
			},
		},

		Ports: []corev1.ContainerPort{
			{
				Name:          "grpc",
				ContainerPort: 9000,
				Protocol:      corev1.ProtocolTCP,
			},
		},

		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},

		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "workspace",
				MountPath: "/workspace",
			},
		},
	}
}
```

**Step 3: Update CreatePod to include stub sidecar**

Modify the existing CreatePod function to add the stub container and use command/args from request.

**Step 4: Update config to include stub image**

Modify `manager-service/internal/config/types.go`:

```go
type Config struct {
	// ... existing fields ...

	// Stub configuration
	StubImage string `yaml:"stub_image" env:"STUB_IMAGE" default:"ghcr.io/vibe-kanban/stub-service:latest"`

	// Manager endpoint for stub
	ManagerEndpoint string `yaml:"manager_endpoint" env:"MANAGER_ENDPOINT" default:"sandbox-manager.sandbox-system.svc:8080"`
}
```

**Step 5: Commit**

```bash
git add manager-service/internal/k8s/pods.go manager-service/internal/config/types.go
git commit -m "feat: add stub sidecar to pod spec"
```

---

### Task 8: Write unit tests for session manager

**Files:**
- Create: `manager-service/internal/session/manager_test.go`

**Step 1: Write the test file**

Create `manager-service/internal/session/manager_test.go`:

```go
package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_Create(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Config{
		MaxLifetime: 24 * time.Hour,
		IdleTimeout: 30 * time.Minute,
	})

	req := CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	}

	s, err := m.Create(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, s.ID)
	assert.Equal(t, "ws1", s.WorkspaceID)
	assert.Equal(t, "proj1", s.ProjectID)
	assert.Equal(t, StateCreating, s.State)
	assert.False(t, s.ClientConnected)
}

func TestManager_Get(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Config{})

	req := CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	}

	s, err := m.Create(ctx, req)
	require.NoError(t, err)

	got, ok := m.Get(s.ID)
	assert.True(t, ok)
	assert.Equal(t, s.ID, got.ID)

	_, ok = m.Get("nonexistent")
	assert.False(t, ok)
}

func TestManager_UpdateState(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Config{})

	s, _ := m.Create(ctx, CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	})

	err := m.UpdateState(s.ID, StateReady)
	require.NoError(t, err)

	got, _ := m.Get(s.ID)
	assert.Equal(t, StateReady, got.State)
}

func TestManager_ClientConnection(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Config{})

	s, _ := m.Create(ctx, CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	})

	// Mark connected
	err := m.MarkClientConnected(s.ID)
	require.NoError(t, err)

	got, _ := m.Get(s.ID)
	assert.True(t, got.ClientConnected)
	assert.NotNil(t, got.ClientConnectedAt)
	assert.Nil(t, got.ClientDisconnectedAt)
	assert.Equal(t, StateAttached, got.State)

	// Mark disconnected
	err = m.MarkClientDisconnected(s.ID)
	require.NoError(t, err)

	got, _ = m.Get(s.ID)
	assert.False(t, got.ClientConnected)
	assert.NotNil(t, got.ClientDisconnectedAt)
	assert.Equal(t, StateOffline, got.State)
}

func TestManager_Touch(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Config{
		IdleTimeout: 30 * time.Minute,
	})

	s, _ := m.Create(ctx, CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	})

	oldExpiresAt := s.ExpiresAt

	err := m.Touch(s.ID)
	require.NoError(t, err)

	got, _ := m.Get(s.ID)
	assert.True(t, got.ExpiresAt.After(oldExpiresAt))
}

func TestManager_Delete(t *testing.T) {
	ctx := context.Background()
	m := NewManager(Config{})

	s, _ := m.Create(ctx, CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	})

	m.Delete(s.ID)

	_, ok := m.Get(s.ID)
	assert.False(t, ok)
}

func TestSession_IsExpired(t *testing.T) {
	s := &Session{
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
	assert.True(t, s.IsExpired())

	s.ExpiresAt = time.Now().Add(1 * time.Hour)
	assert.False(t, s.IsExpired())
}

func TestSession_IsIdle(t *testing.T) {
	now := time.Now()

	s := &Session{
		ClientConnected: true,
	}
	assert.False(t, s.IsIdle(time.Hour))

	s.ClientConnected = false
	s.ClientDisconnectedAt = &now
	assert.False(t, s.IsIdle(time.Hour))

	past := now.Add(-2 * time.Hour)
	s.ClientDisconnectedAt = &past
	assert.True(t, s.IsIdle(time.Hour))
}
```

**Step 2: Run tests**

```bash
cd manager-service
go test ./internal/session/... -v
```

Expected: All tests pass

**Step 3: Commit**

```bash
git add manager-service/internal/session/manager_test.go
git commit -m "test: add session manager unit tests"
```

---

## Phase 2: Storage (Snapshot/Restore)

### Task 9: Add MinIO/S3 dependencies

**Files:**
- Modify: `manager-service/go.mod`

**Step 1: Add MinIO SDK**

```bash
cd manager-service
go get github.com/minio/minio-go/v7@v7.0.66
go mod tidy
```

**Step 2: Verify**

```bash
grep minio go.mod
```

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add MinIO SDK"
```

---

### Task 10: Create storage manager

**Files:**
- Create: `manager-service/internal/storage/types.go`
- Create: `manager-service/internal/storage/manager.go`

**Step 1: Write storage types**

Create `manager-service/internal/storage/types.go`:

```go
package storage

import "time"

type SnapshotMetadata struct {
	WorkspaceID   string    `json:"workspace_id"`
	ProjectID     string    `json:"project_id"`
	AgentThreadID string    `json:"agent_thread_id"`
	SessionID     string    `json:"session_id"`
	CreatedAt     time.Time `json:"created_at"`
	SizeBytes     int64     `json:"size_bytes"`
	Checksum      string    `json:"checksum"`
	Version       string    `json:"version"`
}

type SnapshotLocation struct {
	Bucket string
	Key    string
}

func (l SnapshotLocation) String() string {
	return l.Bucket + "/" + l.Key
}
```

**Step 2: Write storage manager**

Create `manager-service/internal/storage/manager.go`:

```go
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Manager struct {
	client *minio.Client
	bucket string
}

func NewManager(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Manager, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Ensure bucket exists
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return &Manager{
		client: client,
		bucket: bucket,
	}, nil
}

func (m *Manager) GenerateSnapshotKey(workspaceID, projectID, sessionID string) string {
	// Format: {workspace}/{project}/{session}/workspace.tar.gz
	// Use session ID to avoid conflicts when creating new session for same agent thread
	return fmt.Sprintf("%s/%s/%s/workspace.tar.gz",
		strings.TrimPrefix(workspaceID, "ws_"),
		strings.TrimPrefix(projectID, "proj_"),
		strings.TrimPrefix(sessionID, "sess_"),
	)
}

func (m *Manager) UploadSnapshot(ctx context.Context, key string, data io.Reader, size int64) (*SnapshotMetadata, error) {
	// Calculate checksum while uploading
	hash := sha256.New()
	tee := io.TeeReader(data, hash)

	// Upload to MinIO
	_, err := m.client.PutObject(ctx, m.bucket, key, tee, size, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload snapshot: %w", err)
	}

	checksum := hex.EncodeToString(hash.Sum(nil))

	return &SnapshotMetadata{
		SizeBytes: size,
		Checksum:  checksum,
		Version:   "1.0",
	}, nil
}

func (m *Manager) DownloadSnapshot(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get snapshot: %w", err)
	}

	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, fmt.Errorf("failed to stat snapshot: %w", err)
	}

	return obj, info.Size, nil
}

func (m *Manager) DeleteSnapshot(ctx context.Context, key string) error {
	return m.client.RemoveObject(ctx, m.bucket, key, minio.RemoveObjectOptions{})
}

func (m *Manager) SnapshotExists(ctx context.Context, key string) (bool, error) {
	_, err := m.client.StatObject(ctx, m.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) GetSnapshotRef(workspaceID, projectID, sessionID string) SnapshotLocation {
	return SnapshotLocation{
		Bucket: m.bucket,
		Key:    m.GenerateSnapshotKey(workspaceID, projectID, sessionID),
	}
}
```

**Step 3: Commit**

```bash
git add manager-service/internal/storage/
git commit -m "feat: add storage manager for MinIO/S3"
```

---

### Task 11: Add storage configuration

**Files:**
- Modify: `manager-service/internal/config/types.go`

**Step 1: Add storage config section**

```go
type Config struct {
	// ... existing fields ...

	// Storage configuration
	Storage StorageConfig `yaml:"storage"`
}

type StorageConfig struct {
	// MinIO/S3 configuration
	Endpoint  string `yaml:"endpoint" env:"STORAGE_ENDPOINT"`
	AccessKey string `yaml:"access_key" env:"STORAGE_ACCESS_KEY"`
	SecretKey string `yaml:"secret_key" env:"STORAGE_SECRET_KEY"`
	Bucket    string `yaml:"bucket" env:"STORAGE_BUCKET"`
	UseSSL    bool   `yaml:"use_ssl" env:"STORAGE_USE_SSL"`

	// Snapshot settings
	SnapshotFormatVersion string `yaml:"snapshot_format_version" default:"1.0"`
}
```

**Step 2: Commit**

```bash
git add manager-service/internal/config/types.go
git commit -m "feat: add storage configuration"
```

---

### Task 12: Implement snapshot orchestrator

**Files:**
- Create: `manager-service/internal/snapshot/orchestrator.go`

**Step 1: Write snapshot orchestrator**

Create `manager-service/internal/snapshot/orchestrator.go`:

```go
package snapshot

import (
	"context"
	"fmt"
	"io"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/storage"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/stub"
)

type Orchestrator struct {
	stubClientFactory StubClientFactory
	storage           *storage.Manager
}

type StubClientFactory func(ctx context.Context, endpoint string) (*stub.Client, error)

func NewOrchestrator(storage *storage.Manager, stubFactory StubClientFactory) *Orchestrator {
	return &Orchestrator{
		stubClientFactory: stubFactory,
		storage:           storage,
	}
}

// Snapshot creates a snapshot of the session's workspace
func (o *Orchestrator) Snapshot(ctx context.Context, sess *session.Session, progressChan chan<- SnapshotProgress) (*storage.SnapshotLocation, error) {
	if sess.StubEndpoint == "" {
		return nil, fmt.Errorf("session has no stub endpoint")
	}

	// Connect to stub
	stubClient, err := o.stubClientFactory(ctx, sess.StubEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to stub: %w", err)
	}
	defer stubClient.Close()

	if progressChan != nil {
		progressChan <- SnapshotProgress{Stage: "connecting", Message: "Connected to stub"}
	}

	// Trigger snapshot on stub
	resp, err := stubClient.Snapshot(ctx, sess.ID, "/workspace")
	if err != nil {
		return nil, fmt.Errorf("stub snapshot failed: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("stub snapshot error: %s", resp.Error)
	}

	if progressChan != nil {
		progressChan <- SnapshotProgress{Stage: "snapshot_created", Message: "Snapshot created in pod"}
	}

	// Download tar from pod via k8s exec or via stub streaming
	// For simplicity, assume stub streams the tar data
	// This will need to be implemented with streaming

	// Generate location
	loc := o.storage.GetSnapshotRef(sess.WorkspaceID, sess.ProjectID, sess.ID)

	if progressChan != nil {
		progressChan <- SnapshotProgress{Stage: "uploading", Message: "Uploading to storage"}
	}

	// Upload to storage (implement streaming)
	// This will require getting the tar stream from the stub

	if progressChan != nil {
		progressChan <- SnapshotProgress{Stage: "complete", Message: "Snapshot complete"}
	}

	return &loc, nil
}

// Restore restores a snapshot to the session's workspace
func (o *Orchestrator) Restore(ctx context.Context, sess *session.Session, loc *storage.SnapshotLocation, progressChan chan<- RestoreProgress) error {
	if sess.StubEndpoint == "" {
		return fmt.Errorf("session has no stub endpoint")
	}

	// Connect to stub
	stubClient, err := o.stubClientFactory(ctx, sess.StubEndpoint)
	if err != nil {
		return fmt.Errorf("failed to connect to stub: %w", err)
	}
	defer stubClient.Close()

	if progressChan != nil {
		progressChan <- RestoreProgress{Stage: "connecting", Message: "Connected to stub"}
	}

	// Download from storage
	if progressChan != nil {
		progressChan <- RestoreProgress{Stage: "downloading", Message: "Downloading from storage"}
	}

	data, size, err := o.storage.DownloadSnapshot(ctx, loc.Key)
	if err != nil {
		return fmt.Errorf("failed to download snapshot: %w", err)
	}
	defer data.Close()

	if progressChan != nil {
		progressChan <- RestoreProgress{Stage: "downloading", Progress: 0.5, Message: fmt.Sprintf("Downloaded %d bytes", size)}
	}

	// Read all data (for streaming, implement chunked transfer)
	tarData, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("failed to read snapshot data: %w", err)
	}

	if progressChan != nil {
		progressChan <- RestoreProgress{Stage: "restoring", Message: "Restoring workspace"}
	}

	// Restore via stub
	resp, err := stubClient.Restore(ctx, sess.ID, tarData, "/workspace")
	if err != nil {
		return fmt.Errorf("stub restore failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("stub restore error: %s", resp.Error)
	}

	if progressChan != nil {
		progressChan <- RestoreProgress{Stage: "complete", Message: "Restore complete"}
	}

	return nil
}

type SnapshotProgress struct {
	Stage   string  `json:"stage"`
	Message string  `json:"message"`
	Progress float64 `json:"progress,omitempty"`
}

type RestoreProgress struct {
	Stage    string  `json:"stage"`
	Message  string  `json:"message"`
	Progress float64 `json:"progress,omitempty"`
}
```

**Step 2: Commit**

```bash
git add manager-service/internal/snapshot/
git commit -m "feat: add snapshot orchestrator"
```

---

### Task 13: Write storage tests

**Files:**
- Create: `manager-service/internal/storage/manager_test.go`

**Step 1: Write storage tests**

Create `manager-service/internal/storage/manager_test.go`:

```go
package storage

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_GenerateSnapshotKey(t *testing.T) {
	m := &Manager{bucket: "test-bucket"}

	tests := []struct {
		name       string
		workspace  string
		project    string
		session    string
		expected   string
	}{
		{
			name:      "normal case",
			workspace: "ws_abc123",
			project:   "proj_def456",
			session:   "sess_ghi789",
			expected:  "abc123/def456/ghi789/workspace.tar.gz",
		},
		{
			name:      "without prefixes",
			workspace: "abc123",
			project:   "def456",
			session:   "ghi789",
			expected:  "abc123/def456/ghi789/workspace.tar.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.GenerateSnapshotKey(tt.workspace, tt.project, tt.session)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSnapshotLocation_String(t *testing.T) {
	loc := SnapshotLocation{
		Bucket: "my-bucket",
		Key:    "path/to/file.tar.gz",
	}

	assert.Equal(t, "my-bucket/path/to/file.tar.gz", loc.String())
}
```

**Step 2: Run tests**

```bash
cd manager-service
go test ./internal/storage/... -v
```

**Step 3: Commit**

```bash
git add manager-service/internal/storage/manager_test.go
git commit -m "test: add storage manager unit tests"
```

---

## Phase 3: API Layer (Client Communication)

### Task 14: Create WebSocket handler for shell I/O

**Files:**
- Create: `manager-service/internal/httpapi/websocket.go`

**Step 1: Write WebSocket handler**

Create `manager-service/internal/httpapi/websocket.go`:

```go
package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/stub"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Configure appropriately for production
	},
}

type ShellFrame struct {
	Type string `json:"type"` // "stdin", "stdout", "stderr", "exit", "resize"
	Data string `json:"data"` // base64 encoded for binary data
	Seq  int    `json:"seq"`
}

type ShellHandler struct {
	sessionManager *session.Manager
	stubDialer     StubDialer
}

type StubDialer func(ctx context.Context, endpoint string) (*stub.Client, error)

func NewShellHandler(sessionManager *session.Manager, stubDialer StubDialer) *ShellHandler {
	return &ShellHandler{
		sessionManager: sessionManager,
		stubDialer:     stubDialer,
	}
}

func (h *ShellHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	// Get session
	sess, ok := h.sessionManager.Get(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if sess.State != session.StateReady && sess.State != session.StateAttached {
		http.Error(w, fmt.Sprintf("session not ready, current state: %s", sess.State), http.StatusServiceUnavailable)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("websocket upgrade failed: %v", err), http.StatusBadRequest)
		return
	}
	defer conn.Close()

	// Mark client as connected
	if err := h.sessionManager.MarkClientConnected(sessionID); err != nil {
		conn.WriteJSON(ShellFrame{Type: "error", Data: fmt.Sprintf("Failed to mark connected: %v", err)})
		return
	}
	defer h.sessionManager.MarkClientDisconnected(sessionID)

	// Connect to stub
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stubClient, err := h.stubDialer(ctx, sess.StubEndpoint)
	if err != nil {
		conn.WriteJSON(ShellFrame{Type: "error", Data: fmt.Sprintf("Failed to connect to stub: %v", err)})
		return
	}
	defer stubClient.Close()

	// Create shell stream
	stream, err := stub.NewShellStream(ctx, stubClient, sessionID)
	if err != nil {
		conn.WriteJSON(ShellFrame{Type: "error", Data: fmt.Sprintf("Failed to create shell stream: %v", err)})
		return
	}
	defer stream.Close()

	// Start bidirectional forwarding
	var wg sync.WaitGroup
	wg.Add(2)

	// WebSocket → Stub (stdin)
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var frame ShellFrame
			if err := json.Unmarshal(message, &frame); err != nil {
				continue
			}

			switch frame.Type {
			case "stdin":
				data, err := base64.StdEncoding.DecodeString(frame.Data)
				if err != nil {
					continue
				}
				if err := stream.WriteStdin(data); err != nil {
					return
				}
			case "resize":
				// Handle terminal resize
			}
		}
	}()

	// Stub → WebSocket (stdout/stderr)
	go func() {
		defer wg.Done()
		defer cancel()
		defer conn.Close()

		stdoutCh := make(chan []byte, 10)
		stderrCh := make(chan []byte, 10)
		exitCh := make(chan int32, 1)

		go func() {
			stream.StreamOutput(stdoutCh, stderrCh, exitCh)
		}()

		seq := 0
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case stdout, ok := <-stdoutCh:
				if !ok {
					return
				}
				frame := ShellFrame{
					Type: "stdout",
					Data: base64.StdEncoding.EncodeToString(stdout),
					Seq:  seq,
				}
				if err := conn.WriteJSON(frame); err != nil {
					return
				}
				seq++

			case stderr, ok := <-stderrCh:
				if !ok {
					return
				}
				frame := ShellFrame{
					Type: "stderr",
					Data: base64.StdEncoding.EncodeToString(stderr),
					Seq:  seq,
				}
				if err := conn.WriteJSON(frame); err != nil {
					return
				}
				seq++

			case exitCode, ok := <-exitCh:
				if !ok {
					return
				}
				frame := ShellFrame{
					Type: "exit",
					Data: fmt.Sprintf("%d", exitCode),
					Seq:  seq,
				}
				conn.WriteJSON(frame)
				return

			case <-ticker.C:
				// Send keepalive
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}
```

**Step 2: Add gorilla/websocket dependency**

```bash
cd manager-service
go get github.com/gorilla/websocket@v1.5.1
go mod tidy
```

**Step 3: Commit**

```bash
git add manager-service/internal/httpapi/websocket.go go.mod go.sum
git commit -m "feat: add WebSocket handler for shell I/O"
```

---

### Task 15: Create SSE wait handler

**Files:**
- Create: `manager-service/internal/httpapi/sse.go`

**Step 1: Write SSE handler**

Create `manager-service/internal/httpapi/sse.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
)

type WaitHandler struct {
	sessionManager *session.Manager
	pollInterval   time.Duration
}

type StatusEvent struct {
	State    string `json:"state"`
	Message  string `json:"message"`
	Progress float64 `json:"progress,omitempty"`
}

func NewWaitHandler(sessionManager *session.Manager) *WaitHandler {
	return &WaitHandler{
		sessionManager: sessionManager,
		pollInterval:   500 * time.Millisecond,
	}
}

func (h *WaitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	// Get session
	sess, ok := h.sessionManager.Get(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()

	// Send initial state
	h.sendEvent(w, flusher, sess.State, "Session created", 0)

	// Poll for state changes
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sess, ok = h.sessionManager.Get(sessionID)
			if !ok {
				h.sendEvent(w, flusher, "error", "Session not found", 0)
				return
			}

			switch sess.State {
			case session.StateCreating:
				h.sendEvent(w, flusher, "creating", "Creating pod...", 0.1)

			case session.StateRestoring:
				h.sendEvent(w, flusher, "restoring", "Restoring workspace from snapshot...", 0.5)

			case session.StateReady, session.StateAttached:
				h.sendEvent(w, flusher, "ready", "Session ready for connection", 1.0)
				return // Done

			case session.StateTerminating:
				h.sendEvent(w, flusher, "error", "Session is being terminated", 0)
				return

			default:
				// Continue polling
			}

			// Check timeout
			if sess.IsExpired() {
				h.sendEvent(w, flusher, "error", "Session expired while waiting", 0)
				return
			}
		}
	}
}

func (h *WaitHandler) sendEvent(w http.ResponseWriter, flusher http.Flusher, state, message string, progress float64) {
	event := StatusEvent{
		State:    state,
		Message:  message,
		Progress: progress,
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}
```

**Step 2: Commit**

```bash
git add manager-service/internal/httpapi/sse.go
git commit -m "feat: add SSE wait handler for session status"
```

---

### Task 16: Update HTTP handlers

**Files:**
- Modify: `manager-service/internal/httpapi/handlers.go`

**Step 1: Read current handlers**

```bash
cat manager-service/internal/httpapi/handlers.go
```

**Step 2: Add new handlers for session API**

Add new handler functions for:
- POST /v1/sessions
- GET /v1/sessions/{id}/wait
- GET /v1/sessions/{id}/shell (WebSocket)
- POST /v1/sessions/{id}/touch
- DELETE /v1/sessions/{id}

**Step 3: Commit**

```bash
git add manager-service/internal/httpapi/handlers.go
git commit -m "feat: add new session API handlers"
```

---

### Task 17: Update routing in app.go

**Files:**
- Modify: `manager-service/internal/app/app.go`

**Step 1: Read current app.go**

```bash
cat manager-service/internal/app/app.go
```

**Step 2: Add new routes**

Add routes for:
- /v1/sessions (POST)
- /v1/sessions/{id}/wait (GET)
- /v1/sessions/{id}/shell (GET, WebSocket)
- /v1/sessions/{id}/touch (POST)
- /v1/sessions/{id} (DELETE)

**Step 3: Commit**

```bash
git add manager-service/internal/app/app.go
git commit -m "feat: add session API routes"
```

---

### Task 18: Write API handler tests

**Files:**
- Create: `manager-service/internal/httpapi/handlers_test.go`

**Step 1: Write handler tests**

Create `manager-service/internal/httpapi/handlers_test.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
)

func TestCreateSessionHandler(t *testing.T) {
	sm := session.NewManager(session.Config{
		MaxLifetime: 24 * time.Hour,
	})

	h := &CreateSessionHandler{
		SessionManager: sm,
	}

	body := `{
		"image": "test:latest",
		"workspace_id": "ws1",
		"project_id": "proj1"
	}`

	req := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["session_id"])
}

func TestWaitHandler_SSE(t *testing.T) {
	sm := session.NewManager(session.Config{})

	sess, err := sm.Create(context.Background(), session.CreateRequest{
		WorkspaceID:  "ws1",
		ProjectID:    "proj1",
		Image:        "test:latest",
		PodNamespace: "sandbox",
	})
	require.NoError(t, err)

	h := NewWaitHandler(sm)

	req := httptest.NewRequest("GET", "/v1/sessions/"+sess.ID+"/wait", nil)
	w := httptest.NewRecorder()

	done := make(chan bool)
	go func() {
		h.ServeHTTP(w, req)
		done <- true
	}()

	// Simulate state change
	time.Sleep(100 * time.Millisecond)
	sm.UpdateState(sess.ID, session.StateReady)

	<-done
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
}
```

**Step 2: Run tests**

```bash
cd manager-service
go test ./internal/httpapi/... -v
```

**Step 3: Commit**

```bash
git add manager-service/internal/httpapi/handlers_test.go
git commit -m "test: add API handler tests"
```

---

## Phase 4: Lifecycle (Cleaner & TTL)

### Task 19: Create cleaner CronJob

**Files:**
- Create: `k8s/base/cleaner-cronjob.yaml`
- Create: `manager-service/cmd/cleaner/main.go`

**Step 1: Write cleaner CronJob manifest**

Create `k8s/base/cleaner-cronjob.yaml`:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: sandbox-cleaner
  namespace: sandbox-system
spec:
  schedule: "*/5 * * * *"  # Every 5 minutes
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 3
      template:
        spec:
          serviceAccountName: sandbox-manager
          restartPolicy: OnFailure
          containers:
          - name: cleaner
            image: sandbox-manager:latest
            command: ["/cleaner"]
            args:
            - --idle-timeout=30m
            - --scan-timeout=10m
            env:
            - name: POD_NAMESPACE
              value: "sandbox"
            - name: DB_PATH
              value: "/data/sessions.db"
            volumeMounts:
            - name: config
              mountPath: /etc/config
              readOnly: true
            resources:
              requests:
                cpu: 100m
                memory: 128Mi
              limits:
                cpu: 500m
                memory: 256Mi
          volumes:
          - name: config
            configMap:
              name: sandbox-manager-config
```

**Step 2: Write cleaner binary**

Create `manager-service/cmd/cleaner/main.go`:

```go
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/k8s"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
)

type Config struct {
	IdleTimeout   time.Duration
	ScanTimeout   time.Duration
	PodNamespace  string
	DryRun        bool
}

func main() {
	cfg := Config{
		IdleTimeout:  30 * time.Minute,
		ScanTimeout:  10 * time.Minute,
		PodNamespace: "sandbox",
		DryRun:       false,
	}

	idleTimeout := flag.Duration("idle-timeout", cfg.IdleTimeout, "Idle timeout before cleaning up sessions")
	scanTimeout := flag.Duration("scan-timeout", cfg.ScanTimeout, "Maximum time to spend scanning for idle sessions")
	dryRun := flag.Bool("dry-run", false, "Don't actually delete anything")
	flag.Parse()

	cfg.IdleTimeout = *idleTimeout
	cfg.ScanTimeout = *scanTimeout
	cfg.DryRun = *dryRun

	log.Printf("Starting cleaner: idle_timeout=%v, dry_run=%v", cfg.IdleTimeout, cfg.DryRun)

	if err := run(context.Background(), cfg); err != nil {
		log.Fatalf("Cleaner failed: %v", err)
	}

	log.Println("Cleaner completed successfully")
}

func run(ctx context.Context, cfg Config) error {
	// Create Kubernetes client
	k8sClient, err := k8s.NewClient()
	if err != nil {
		return err
	}

	// Scan for idle sessions
	ctx, cancel := context.WithTimeout(ctx, cfg.ScanTimeout)
	defer cancel()

	sessions, err := scanIdleSessions(ctx, k8sClient, cfg)
	if err != nil {
		return err
	}

	log.Printf("Found %d idle sessions", len(sessions))

	for _, sess := range sessions {
		log.Printf("Session %s: offline for %v, exceeds idle timeout of %v",
			sess.ID,
			time.Since(*sess.ClientDisconnectedAt),
			cfg.IdleTimeout)

		if cfg.DryRun {
			log.Printf("[DRY RUN] Would delete session %s (pod: %s)", sess.ID, sess.PodName)
			continue
		}

		// Trigger snapshot before deletion (if applicable)
		if err := snapshotSession(ctx, sess); err != nil {
			log.Printf("Failed to snapshot session %s: %v", sess.ID, err)
			// Continue with deletion even if snapshot fails
		}

		// Delete pod
		if err := k8sClient.DeletePod(ctx, cfg.PodNamespace, sess.PodName); err != nil {
			log.Printf("Failed to delete pod %s: %v", sess.PodName, err)
			continue
		}

		log.Printf("Deleted session %s (pod: %s)", sess.ID, sess.PodName)
	}

	return nil
}

func scanIdleSessions(ctx context.Context, client *k8s.Client, cfg Config) ([]*session.Session, error) {
	// List pods with session labels
	pods, err := client.ListPods(ctx, cfg.PodNamespace)
	if err != nil {
		return nil, err
	}

	var idleSessions []*session.Session
	now := time.Now()

	for _, pod := range pods {
		// Check for session annotations
		sessionID := pod.Annotations["session.mbos.io/id"]
		if sessionID == "" {
			continue
		}

		// Check offline status
		offlineAtStr := pod.Annotations["session.mbos.io/offline-at"]
		if offlineAtStr == "" {
			continue
		}

		offlineAt, err := time.Parse(time.RFC3339, offlineAtStr)
		if err != nil {
			log.Printf("Failed to parse offline-at for session %s: %v", sessionID, err)
			continue
		}

		// Check if idle timeout exceeded
		if now.Sub(offlineAt) > cfg.IdleTimeout {
			sess := &session.Session{
				ID:                 sessionID,
				PodName:            pod.Name,
				ClientDisconnected: true,
				ClientDisconnectedAt: &offlineAt,
			}
			idleSessions = append(idleSessions, sess)
		}
	}

	return idleSessions, nil
}

func snapshotSession(ctx context.Context, sess *session.Session) error {
	// Trigger snapshot via manager API or directly
	// This would call the snapshot orchestrator
	log.Printf("Snapshotting session %s before deletion", sess.ID)
	return nil
}
```

**Step 3: Commit**

```bash
git add k8s/base/cleaner-cronjob.yaml manager-service/cmd/cleaner/
git commit -m "feat: add cleaner CronJob for session回收"
```

---

### Task 20: Implement snapshot on pod termination

**Files:**
- Modify: `manager-service/internal/k8s/pods.go`
- Create: `manager-service/internal/lifecycle/hook.go`

**Step 1: Create pre-delete hook handler**

Create `manager-service/internal/lifecycle/hook.go`:

```go
package lifecycle

import (
	"context"
	"fmt"
	"log"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/snapshot"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/storage"
)

type PreDeleteHook struct {
	orchestrator *snapshot.Orchestrator
}

func NewPreDeleteHook(orchestrator *snapshot.Orchestrator) *PreDeleteHook {
	return &PreDeleteHook{
		orchestrator: orchestrator,
	}
}

// Execute runs snapshot before pod deletion
func (h *PreDeleteHook) Execute(ctx context.Context, sess *session.Session) (*storage.SnapshotLocation, error) {
	log.Printf("Executing pre-delete hook for session %s", sess.ID)

	progress := make(chan snapshot.SnapshotProgress)
	defer close(progress)

	// Run snapshot in background and log progress
	go func() {
		for p := range progress {
			log.Printf("Snapshot progress for %s: %s - %s", sess.ID, p.Stage, p.Message)
		}
	}()

	loc, err := h.orchestrator.Snapshot(ctx, sess, progress)
	if err != nil {
		return nil, fmt.Errorf("pre-delete snapshot failed: %w", err)
	}

	log.Printf("Pre-delete snapshot completed for session %s: %s", sess.ID, loc.String())
	return loc, nil
}
```

**Step 2: Update DeletePod to trigger snapshot**

Modify the k8s client's DeletePod method to call the pre-delete hook.

**Step 3: Commit**

```bash
git add manager-service/internal/lifecycle/hook.go
git commit -m "feat: add pre-delete snapshot hook"
```

---

### Task 21: Add session TTL management

**Files:**
- Create: `manager-service/internal/session/ttl.go`

**Step 1: Write TTL manager**

Create `manager-service/internal/session/ttl.go`:

```go
package session

import (
	"context"
	"log"
	"time"
)

type TTLManager struct {
	sessionManager *Manager
	checkInterval  time.Duration
}

func NewTTLManager(sm *Manager, checkInterval time.Duration) *TTLManager {
	return &TTLManager{
		sessionManager: sm,
		checkInterval:  checkInterval,
	}
}

func (m *TTLManager) Start(ctx context.Context) {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkExpiredSessions(ctx)
		}
	}
}

func (m *TTLManager) checkExpiredSessions(ctx context.Context) {
	expired := m.sessionManager.ListExpiredSessions()

	for _, sess := range expired {
		log.Printf("Session %s expired at %s", sess.ID, sess.ExpiresAt)

		// Trigger termination with snapshot
		if err := m.terminateSession(ctx, sess); err != nil {
			log.Printf("Failed to terminate expired session %s: %v", sess.ID, err)
		}
	}
}

func (m *TTLManager) terminateSession(ctx context.Context, sess *Session) error {
	// Update state
	sess.State = StateTerminating

	// This would trigger the pre-delete hook and pod deletion
	// Implementation depends on integration with k8s client

	return nil
}
```

**Step 2: Commit**

```bash
git add manager-service/internal/session/ttl.go
git commit -m "feat: add TTL manager for session expiration"
```

---

### Task 22: Integrate all components in app.go

**Files:**
- Modify: `manager-service/internal/app/app.go`

**Step 1: Update app initialization**

Add initialization for:
- Storage manager
- Snapshot orchestrator
- TTL manager
- WebSocket/SSE handlers
- Pre-delete hooks

**Step 2: Commit**

```bash
git add manager-service/internal/app/app.go
git commit -m "feat: integrate all components in app"
```

---

### Task 23: Update Dockerfile to include cleaner binary

**Files:**
- Modify: `manager-service/Dockerfile`

**Step 1: Update Dockerfile to build both binaries**

```dockerfile
# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Install dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

# Build manager and cleaner
RUN CGO_ENABLED=0 GOOS=linux go build -o manager ./cmd/manager
RUN CGO_ENABLED=0 GOOS=linux go build -o cleaner ./cmd/cleaner

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tar

WORKDIR /app

COPY --from=builder /build/manager .
COPY --from=builder /build/cleaner .

# Create symlink for cleaner
RUN ln -s /app/cleaner /cleaner

EXPOSE 8080

ENTRYPOINT ["/app/manager"]
```

**Step 2: Commit**

```bash
git add manager-service/Dockerfile
git commit -m "feat: update Dockerfile for multi-binary build"
```

---

### Task 24: Write integration tests

**Files:**
- Create: `manager-service/integration/session_test.go`

**Step 1: Write integration test**

Create `manager-service/integration/session_test.go`:

```go
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
)

func TestSessionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// This test requires a running Kubernetes cluster
	ctx := context.Background()

	sm := session.NewManager(session.Config{
		MaxLifetime: 1 * time.Hour,
		IdleTimeout: 30 * time.Minute,
	})

	// Create session
	sess, err := sm.Create(ctx, session.CreateRequest{
		WorkspaceID:  "test-ws",
		ProjectID:    "test-proj",
		Image:        "nginx:alpine",
		PodNamespace: "default",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, sess.ID)

	// Update state
	err = sm.UpdateState(sess.ID, session.StateReady)
	require.NoError(t, err)

	// Verify state
	updated, ok := sm.Get(sess.ID)
	require.True(t, ok)
	assert.Equal(t, session.StateReady, updated.State)

	// Mark connected
	err = sm.MarkClientConnected(sess.ID)
	require.NoError(t, err)

	connected, ok := sm.Get(sess.ID)
	require.True(t, ok)
	assert.True(t, connected.ClientConnected)

	// Mark disconnected
	err = sm.MarkClientDisconnected(sess.ID)
	require.NoError(t, err)

	disconnected, ok := sm.Get(sess.ID)
	require.True(t, ok)
	assert.False(t, disconnected.ClientConnected)
	assert.NotNil(t, disconnected.ClientDisconnectedAt)

	// Cleanup
	sm.Delete(sess.ID)
	_, ok = sm.Get(sess.ID)
	assert.False(t, ok)
}
```

**Step 2: Commit**

```bash
git add manager-service/integration/session_test.go
git commit -m "test: add integration tests for session lifecycle"
```

---

### Task 25: Update configuration files

**Files:**
- Modify: `manager-service/manager-config.example.yaml`

**Step 1: Add new configuration sections**

Add sections for:
- Storage (MinIO/S3)
- Stub configuration
- Cleaner settings
- Session TTL settings

**Step 2: Commit**

```bash
git add manager-service/manager-config.example.yaml
git commit -m "docs: update configuration example with new settings"
```

---

### Task 26: Write documentation

**Files:**
- Create: `docs/sandbox-refactor-v1.md`
- Create: `docs/api-reference-v1.md`

**Step 1: Write architecture documentation**

Create `docs/sandbox-refactor-v1.md` with:
- Overview of new architecture
- Component descriptions
- Data flow diagrams
- Configuration reference

**Step 2: Write API reference**

Create `docs/api-reference-v1.md` with:
- All API endpoints documented
- Request/response examples
- WebSocket message format
- Error codes

**Step 3: Commit**

```bash
git add docs/
git commit -m "docs: add architecture and API documentation"
```

---

### Task 27: Create migration guide

**Files:**
- Create: `docs/migration-guide-v1.md`

**Step 1: Write migration guide**

Create `docs/migration-guide-v1.md`:
- Changes from v0 to v1
- Breaking changes
- Migration steps
- Rollback procedure

**Step 2: Commit**

```bash
git add docs/migration-guide-v1.md
git commit -m "docs: add migration guide from v0 to v1"
```

---

# Part 3: Testing Strategy

## Unit Tests

Each package should have unit tests covering:
- Business logic (session manager, storage manager)
- Error handling paths
- Edge cases (empty inputs, nil values)

## Integration Tests

Integration tests cover:
- Full session lifecycle (create → ready → attach → disconnect → cleanup)
- Snapshot/restore flow
- API endpoints with real Kubernetes cluster

## E2E Tests

E2E tests use a test client to verify:
- Client connects to manager
- Session created successfully
- Wait endpoint streams status
- WebSocket shell I/O works
- Reconnection after disconnect
- Snapshot/restore on session回收

---

# Part 4: Deployment Considerations

## Configuration Requirements

The following environment variables/config must be set:
- `STORAGE_ENDPOINT`, `STORAGE_ACCESS_KEY`, `STORAGE_SECRET_KEY`, `STORAGE_BUCKET`
- `STUB_IMAGE`, `MANAGER_ENDPOINT`
- `DEFAULT_IDLE_TIMEOUT`, `DEFAULT_MAX_LIFETIME`

## Kubernetes Manifests

Update/create manifests for:
- Deployment with new containers (manager + stub init)
- Service for stub communication
- CronJob for cleaner
- ConfigMap with new config
- RBAC for pod access (needed for cleaner)

## Monitoring

Add metrics for:
- Session count by state
- Snapshot/restore success rate
- Client connections
- Pod lifecycle events

---

# Summary

This refactoring plan transforms mbos-sandbox-v1 from a simple "exec sandbox" to a full-featured session runtime manager with:

1. **Specifiable container images** with configurable command/args
2. **Bidirectional stub communication** via gRPC between manager and pod
3. **Client forwarding service** via WebSocket for shell I/O
4. **MinIO workspace persistence** with snapshot/restore
5. **Cleaner-based pod lifecycle** with offline detection and TTL

The implementation is divided into 4 phases with 27 bite-sized tasks, each taking 2-5 minutes to complete.
