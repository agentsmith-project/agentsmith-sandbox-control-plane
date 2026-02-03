# Sandbox WebSocket API Reference v1

**Version:** 1.0
**Protocol:** WebSocket
**Base Path:** `/ws`
**Last Updated:** 2026-02-04

---

## Table of Contents

1. [Overview](#overview)
2. [Connection Endpoint](#connection-endpoint)
3. [WebSocket Protocol](#websocket-protocol)
4. [Message Types](#message-types)
5. [Create Request](#create-request)
6. [Status Updates](#status-updates)
7. [Data Messages](#data-messages)
8. [Error Handling](#error-handling)
9. [Security Considerations](#security-considerations)
10. [Example Client Flows](#example-client-flows)
11. [State Machine](#state-machine)
12. [Rate Limits & Quotas](#rate-limits--quotas)

---

## Overview

The Sandbox API provides a WebSocket-based interface for creating and managing isolated container environments with persistent shell sessions. Each sandbox session is identified by an `agent_thread_id` and maintains a 1:1 relationship with a Kubernetes Pod.

### Key Features

- **Persistent Shell Sessions**: Using tmux for session persistence
- **Bidirectional Communication**: Full-duplex WebSocket with message buffering
- **Automatic Snapshot/Restore**: Workspace state preserved across sessions
- **TTL-based Lifecycle**: Automatic cleanup based on idle time and max lifetime
- **Security Controls**: Network, filesystem, and resource isolation

### Architecture

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   Client    │◄───────►│   Manager   │◄───────►│     Pod     │
│ (WebSocket) │         │ (Go Service)│         │  (Container)│
└─────────────┘         └─────────────┘         └─────────────┘
                              │
                              ▼
                       ┌─────────────┐
                       │    MinIO    │
                       │ (Snapshots) │
                       └─────────────┘
```

---

## Connection Endpoint

### WebSocket URL

```
ws://sandbox-manager.example.com/ws
```

### Secure WebSocket (Recommended for Production)

```
wss://sandbox-manager.example.com/ws
```

### Connection Parameters

No query parameters are required. All configuration is sent via the initial `create` message.

### Connection Headers

```http
Connection: Upgrade
Upgrade: websocket
Sec-WebSocket-Key: <client-generated-key>
Sec-WebSocket-Version: 13
```

---

## WebSocket Protocol

### Message Format

All messages are JSON-encoded with the following envelope:

```json
{
  "type": "message_type",
  "data": { /* type-specific payload */ }
}
```

### Binary Encoding for Data

Binary data (stdin/stdout/stderr) is Base64-encoded when transmitted as JSON to ensure safe transmission:

```json
{
  "type": "stdout",
  "data": "SGVsbG8gV29ybGQhCg=="
}
```

### Message Flow

```
Client                    Manager
  │                          │
  ├───── WebSocket Connect ──►│
  │                          │
  ├───── create ─────────────►│
  │                          ├───── Create Pod ───► Kubernetes
  │                          │
  │◄──── status (creating) ───┤
  │                          │
  │◄──── status (restoring) ──┤
  │                          ├───── Restore from MinIO ──►
  │                          │
  │◄──── status (ready) ─────┤
  │                          │
  ├───── stdin ─────────────►│
  │                          ├───── kubectl exec ───► Pod
  │◄──── stdout ─────────────┤◄──── tmux output ────┘
```

---

## Message Types

### Direction Legend

- **C → M**: Client to Manager
- **M → C**: Manager to Client

### Message Type Reference

| Type | Direction | Description | Required Fields |
|------|-----------|-------------|-----------------|
| `create` | C → M | Create/connect to sandbox | `agent_thread_id`, `image` |
| `stdin` | C → M | Send input to sandbox | `data` |
| `status` | M → C | Session status update | `state`, `progress` |
| `stdout` | M → C | Standard output | `data` |
| `stderr` | M → C | Standard error | `data` |
| `exit` | M → C | Process exit notification | `code` |
| `error` | M → C | Error message | `message` |
| `ping` | C → M | Keep-alive ping | (empty) |
| `pong` | M → C | Keep-alive response | (empty) |

---

## Create Request

### Purpose

Initiates a new sandbox session or connects to an existing one for the given `agent_thread_id`.

### Message Schema

```json
{
  "type": "create",
  "agent_thread_id": "at_abc123def456",
  "image": "python:3.11-slim",
  "command": ["/bin/bash"],
  "env": {
    "MY_VAR": "value",
    "PATH": "/usr/local/bin:/usr/bin:/bin"
  },
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

### Field Descriptions

#### Core Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `agent_thread_id` | string | Yes | Unique identifier for the session. Reusing this ID connects to the existing session. |
| `image` | string | Yes | Container image reference (e.g., `python:3.11`, `ubuntu:22.04`) |
| `command` | string[] | No | Command to run in tmux session. Default: `["/bin/bash"]` |
| `env` | object | No | Environment variables to set in the container |
| `config` | object | No | Security and resource configuration |

#### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allow_network_access` | boolean | `false` | Allow outbound network access from the container |
| `readonly_filesystem` | boolean | `false` | Mount root filesystem as read-only (except /workspace) |
| `cpu_limit` | string | `"2"` | CPU limit in cores (e.g., `"1"`, `"2"`, `"500m"`) |
| `memory_limit` | string | `"1Gi"` | Memory limit (e.g., `"512Mi"`, `"1Gi"`, `"2Gi"`) |
| `idle_timeout` | duration | `"30m"` | Time before cleanup after client disconnect |
| `max_lifetime` | duration | `"24h"` | Maximum lifetime regardless of activity |
| `drop_all_capabilities` | boolean | `false` | Drop all Linux capabilities |
| `allow_privileged` | boolean | `false` | Allow privileged container mode |

### Duration Format

Durations are specified as strings with the format:

```
<number><unit>
```

Valid units:
- `s` - seconds
- `m` - minutes
- `h` - hours

Examples: `"30s"`, `"5m"`, `"1h"`, `"24h"`

### Response

The Manager responds with a series of `status` messages as the session progresses through states.

---

## Status Updates

### Purpose

Provides asynchronous updates about the session state and progress.

### Message Schema

```json
{
  "type": "status",
  "state": "creating",
  "message": "Provisioning pod...",
  "progress": 0.1,
  "timestamp": "2026-02-04T12:00:00Z"
}
```

### Field Descriptions

| Field | Type | Description |
|-------|------|-------------|
| `state` | string | Current session state (see State Machine) |
| `message` | string | Human-readable status message |
| `progress` | number | Progress from 0.0 to 1.0 |
| `timestamp` | string | ISO 8601 timestamp |

### State Values

| State | Description | Progress Range |
|-------|-------------|----------------|
| `creating` | Pod is being created | 0.0 - 0.4 |
| `restoring` | Restoring workspace from snapshot | 0.4 - 0.9 |
| `ready` | Session ready for I/O | 1.0 |
| `offline` | Session disconnected but Pod running | N/A |
| `error` | Session failed | N/A |

---

## Data Messages

### Stdin (Client to Manager)

Send input to the sandbox session.

**Schema:**
```json
{
  "type": "stdin",
  "data": "bHMgLWxhCg=="  // Base64-encoded "ls -la\n"
}
```

**Example (unencoded):** `ls -la\n`

### Stdout (Manager to Client)

Receive standard output from the sandbox.

**Schema:**
```json
{
  "type": "stdout",
  "data": "dG90YWwgMjQK"  // Base64-encoded output
}
```

### Stderr (Manager to Client)

Receive standard error output from the sandbox.

**Schema:**
```json
{
  "type": "stderr",
  "data": "ZXJyb3I6IGNvbW1hbmQgbm90IGZvdW5kCg=="
}
```

### Exit (Manager to Client)

Notification that the sandbox process has exited.

**Schema:**
```json
{
  "type": "exit",
  "code": 0
}
```

**Exit Codes:**

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1-127 | Process exited with error |
| 128+N | Terminated by signal N |

---

## Error Handling

### Error Message Format

```json
{
  "type": "error",
  "code": "IMAGE_PULL_FAILED",
  "message": "Failed to pull image python:3.11: connection timeout",
  "details": {
    "image": "python:3.11",
    "timeout": "5m"
  }
}
```

### Error Codes

| Code | Description | Retryable |
|------|-------------|-----------|
| `INVALID_REQUEST` | Malformed message or missing required fields | No |
| `IMAGE_PULL_FAILED` | Container image could not be pulled | Yes |
| `IMAGE_NOT_FOUND` | Container image does not exist | No |
| `RESOURCE_LIMIT_EXCEEDED` | Cluster resource quota exceeded | Yes |
| `SNAPSHOT_FAILED` | Failed to create workspace snapshot | No |
| `RESTORE_FAILED` | Failed to restore workspace snapshot | No |
| `TMUX_NOT_FOUND` | Container image does not contain tmux | No |
| `NETWORK_ACCESS_DENIED` | Attempted blocked network access | No |
| `SESSION_EXPIRED` | Session exceeded TTL | No |
| `INTERNAL_ERROR` | Unexpected server error | Yes |

### Recommended Client Behavior

1. **Do not retry** for non-retryable errors
2. **Exponential backoff** for retryable errors (max 3 attempts)
3. **Recreate session** for `SESSION_EXPIRED`
4. **Verify image** for `IMAGE_NOT_FOUND` or `TMUX_NOT_FOUND`

---

## Security Considerations

### Authentication

**Current Status:** No authentication (internal trusted network)

**Future Enhancement:** JWT-based authentication planned for Phase 2

### Authorization

Access is controlled by `agent_thread_id`. Clients can only access sessions they create.

### Network Isolation

When `allow_network_access: false`:
- NetworkPolicy denies all egress traffic
- DNS resolution is blocked
- External API calls will fail

### Filesystem Isolation

When `readonly_filesystem: true`:
- Root filesystem mounted read-only
- `/workspace` remains writable (emptyDir volume)
- Useful for preventing permanent modifications

### Resource Limits

| Resource | Default | Maximum | Description |
|----------|---------|---------|-------------|
| CPU | 2 cores | 8 cores | vCPU limit |
| Memory | 1Gi | 8Gi | Memory limit |
| Max Lifetime | 24h | 168h (7d) | Hard session timeout |
| Idle Timeout | 30m | 4h | Disconnect timeout |

### Capability Control

When `drop_all_capabilities: true`:
- All Linux capabilities dropped
- Processes run as non-root
- Restricts system-level operations

---

## Example Client Flows

### Example 1: Simple Python Session

```javascript
const ws = new WebSocket('ws://sandbox-manager.example.com/ws');

ws.onopen = () => {
  // Create a Python session
  ws.send(JSON.stringify({
    type: 'create',
    agent_thread_id: 'at_session_001',
    image: 'python:3.11-slim',
    command: ['/usr/bin/python3'],
    config: {
      allow_network_access: false,
      idle_timeout: '10m'
    }
  }));
};

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  
  switch (msg.type) {
    case 'status':
      console.log(`Status: ${msg.state} (${msg.progress * 100}%)`);
      if (msg.state === 'ready') {
        // Send Python code
        ws.send(JSON.stringify({
          type: 'stdin',
          data: btoa('print("Hello, World!")\n')
        }));
        ws.send(JSON.stringify({
          type: 'stdin',
          data: btoa('exit()\n')
        }));
      }
      break;
      
    case 'stdout':
      console.log('Output:', atob(msg.data));
      break;
      
    case 'exit':
      console.log(`Exited with code: ${msg.code}`);
      ws.close();
      break;
  }
};
```

### Example 2: Reconnect with Backlog

```javascript
let agentThreadId = 'at_session_002';

function connect() {
  const ws = new WebSocket('ws://sandbox-manager.example.com/ws');
  
  ws.onopen = () => {
    // Reconnect to existing session
    ws.send(JSON.stringify({
      type: 'create',
      agent_thread_id: agentThreadId,
      image: 'ubuntu:22.04'
    }));
    
    // Session state is preserved
    // Any missed output will be sent automatically
  };
  
  ws.onclose = () => {
    // Reconnect after 5 seconds
    setTimeout(connect, 5000);
  };
  
  return ws;
}
```

### Example 3: Error Handling

```javascript
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  
  if (msg.type === 'error') {
    console.error(`Error [${msg.code}]: ${msg.message}`);
    
    switch (msg.code) {
      case 'IMAGE_NOT_FOUND':
        console.error('Please verify the image name');
        break;
        
      case 'TMUX_NOT_FOUND':
        console.error('Image must contain tmux. Use a different image.');
        break;
        
      case 'SESSION_EXPIRED':
        console.error('Session expired. Creating new session...');
        createNewSession();
        break;
        
      case 'RESOURCE_LIMIT_EXCEEDED':
        console.error('Server overloaded. Retrying in 30s...');
        setTimeout(() => ws.reconnect(), 30000);
        break;
    }
  }
};
```

---

## State Machine

### Session States

```
┌──────────┐
│ creating │ ◄─────┐
└────┬─────┘      │
     │            │ Reconnect
     ▼            │
┌──────────┐      │
│restoring │ ─────┘
└────┬─────┘
     │
     ▼
┌──────────┐     WebSocket Disconnect     ┌──────────┐
│  ready   │ ──────────────────────────► │ offline  │
└──────────┘                              └────┬─────┘
     │                                         │
     │ Process Exit                           │ Reconnect
     ▼                                         │
┌──────────┐                                  │
│  exited  │ ─────────────────────────────────┘
└──────────┘
```

### State Transitions

| From | To | Trigger |
|------|-----|---------|
| (none) | `creating` | `create` message received |
| `creating` | `restoring` | Pod ready, snapshot exists |
| `creating` | `ready` | Pod ready, no snapshot |
| `restoring` | `ready` | Snapshot restored |
| `ready` | `offline` | WebSocket disconnect |
| `offline` | `ready` | WebSocket reconnect |
| `ready` | `exited` | Process exits |
| `offline` | `exited` | Process exits while disconnected |

### TTL Behavior

| State | TTL Clock | Description |
|-------|-----------|-------------|
| `creating` | Max Lifetime | Counts toward max lifetime |
| `restoring` | Max Lifetime | Counts toward max lifetime |
| `ready` | Max Lifetime + Idle | Both timers active |
| `offline` | Max Lifetime + Idle | Both timers active |

---

## Rate Limits & Quotas

### Per-Client Limits

| Resource | Limit | Description |
|----------|-------|-------------|
| Concurrent Sessions | 10 | Maximum simultaneous sessions per client |
| Messages/Second | 100 | Maximum stdin messages per second |
| Connection Rate | 5/second | Maximum WebSocket connections per second |

### Per-Session Limits

| Resource | Limit | Description |
|----------|-------|-------------|
| Buffer Size | 10,000 messages | Ring buffer capacity |
| Message Size | 1 MB | Maximum message payload |
| Session Duration | 24 hours (default) | Max lifetime |

### Response to Limit Exceeded

```json
{
  "type": "error",
  "code": "RATE_LIMIT_EXCEEDED",
  "message": "Too many concurrent sessions. Maximum: 10",
  "retry_after": "30s"
}
```

---

## Best Practices

### 1. Agent Thread ID Generation

Use a consistent, unique identifier per logical session:

```javascript
// Good: Stable ID for user session
agent_thread_id = `user_${userId}_session_${sessionId}`;

// Bad: Random ID each connection
agent_thread_id = uuid.v4();  // Creates new session each time!
```

### 2. Image Selection

Ensure images include:
- `tmux` (required)
- Basic utilities (`bash`, `curl`, `tar`)
- Sufficient tools for your use case

**Recommended base images:**
- `ubuntu:22.04` - Full-featured
- `python:3.11-slim` - Python development
- `node:20-slim` - Node.js development
- `alpine:latest` - Minimal (requires tmux installation)

### 3. Timeout Configuration

Set appropriate timeouts based on expected usage:

```json
{
  "config": {
    "idle_timeout": "30m",    // Interactive session
    "max_lifetime": "4h"      // Work session
  }
}
```

### 4. Error Recovery

Implement robust error handling:

```javascript
let reconnectAttempts = 0;
const MAX_RECONNECT_ATTEMPTS = 5;

function handleDisconnect() {
  if (reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
    reconnectAttempts++;
    const delay = Math.min(1000 * Math.pow(2, reconnectAttempts), 30000);
    setTimeout(connect, delay);
  } else {
    console.error('Max reconnection attempts reached');
  }
}
```

### 5. Graceful Shutdown

Send `exit` command before closing:

```javascript
function closeSession() {
  ws.send(JSON.stringify({
    type: 'stdin',
    data: btoa('exit\n')
  }));
  
  // Wait for exit message
  ws.addEventListener('message', function onExit(msg) {
    if (JSON.parse(msg.data).type === 'exit') {
      ws.close();
    }
  }, { once: true });
}
```

---

## Changelog

### v1.0 (2026-02-04)
- Initial API definition
- WebSocket protocol
- Snapshot/restore functionality
- TTL-based lifecycle
- Security controls

---

## Support

For issues, questions, or feature requests:
- GitHub Issues: [repository link]
- Documentation: [docs link]
- Status Page: [status link]

---

**Document Version:** 1.0  
**API Version:** 1.0  
**Last Modified:** 2026-02-04
