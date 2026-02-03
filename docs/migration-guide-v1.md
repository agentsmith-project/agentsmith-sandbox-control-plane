# Migration Guide: v0 to v1

**Version:** 1.0  
**Last Updated:** 2026-02-04  
**Status:** Ready for Review

---

## Table of Contents

1. [Overview](#overview)
2. [Breaking Changes](#breaking-changes)
3. [New Features](#new-features)
4. [Migration Steps](#migration-steps)
5. [Configuration Changes](#configuration-changes)
6. [API Changes](#api-changes)
7. [Rollback Procedure](#rollback-procedure)
8. [Testing Recommendations](#testing-recommendations)
9. [Troubleshooting](#troubleshooting)
10. [Support](#support)

---

## Overview

The mbos-sandbox v1 is a significant architectural refactoring that introduces WebSocket-based communication, persistent shell sessions, automatic workspace snapshot/restore, and enhanced security controls.

### What Changed

| Area | v0 | v1 |
|------|----|----|
| **Communication** | REST API (`PUT`, `POST`, `DELETE`) | WebSocket (bidirectional streaming) |
| **Session Model** | Stateless exec per request | Persistent tmux sessions |
| **Workspace** | Ephemeral | Automatic snapshot/restore |
| **Lifecycle** | TTL-based manual touch | TTL + idle timeout with automatic cleanup |
| **Protocol** | HTTP/JSON | WebSocket with JSON messages |
| **File Transfer** | Separate upload/download endpoints | Integrated into WebSocket flow |
| **Image Selection** | Server-configured default | Client-specified per session |
| **Security Controls** | Basic resource limits | Comprehensive (network, FS, capabilities) |

### Why Migrate?

- **Persistent Sessions**: Maintain shell state across connections
- **Better UX**: Real-time bidirectional communication vs. request/response
- **Workspace Continuity**: Automatic save/restore of work files
- **Enhanced Security**: Network policies, read-only filesystems, capability controls
- **Operational Simplicity**: No manual TTL management, automatic cleanup

---

## Breaking Changes

### 1. Protocol Change: REST to WebSocket

**v0 (REST API):**
```http
PUT /v1/sandboxes/session-123
Content-Type: application/json

{
  "ttlSeconds": 900,
  "image": "sandbox-runner:1.0.0",
  "cpuLimit": "1",
  "memoryLimit": "1Gi"
}
```

**v1 (WebSocket):**
```javascript
const ws = new WebSocket('ws://sandbox-manager.example.com/ws');

ws.onopen = () => {
  ws.send(JSON.stringify({
    type: 'create',
    agent_thread_id: 'session-123',
    image: 'python:3.11-slim',
    config: {
      cpu_limit: '1',
      memory_limit: '1Gi',
      idle_timeout: '15m'
    }
  }));
};
```

**Migration Required:**
- Replace all REST API calls with WebSocket connections
- Implement WebSocket message handling
- Update error handling for WebSocket-specific errors

### 2. Session Identifier

**v0:** `sessionId` (URL parameter)

**v1:** `agent_thread_id` (message field)

**Impact:** Rename session identifiers throughout your codebase.

### 3. File Operations

**v0:** Dedicated endpoints
```http
POST /v1/sandboxes/{sessionId}/files/upload?dest=/workspace/file.txt
GET /v1/sandboxes/{sessionId}/files/download?src=/workspace/file.txt
```

**v1:** No separate file transfer endpoints. Use shell commands:
```javascript
// Upload: Write file via stdin/shell
ws.send(JSON.stringify({
  type: 'stdin',
  data: btoa('cat > /workspace/file.txt << \'EOF\'\ncontent here\nEOF\n')
}));

// Download: Read file via shell
ws.send(JSON.stringify({
  type: 'stdin',
  data: btoa('cat /workspace/file.txt\n')
}));
// Capture stdout response
```

**Migration Required:**
- Remove file upload/download client code
- Implement file operations via shell commands
- Handle Base64 encoding/decoding in client

### 4. Exec Model

**v0:** Fire-and-forget exec
```http
POST /v1/sandboxes/{sessionId}/exec
{
  "cmd": ["python", "script.py"],
  "timeoutSeconds": 30
}
```
Returns full result when complete.

**v1:** Interactive streaming
```javascript
ws.send(JSON.stringify({
  type: 'stdin',
  data: btoa('python script.py\n')
}));
// Response streams via stdout messages
```

**Migration Required:**
- Change from request/response to streaming model
- Implement response accumulation for command results
- Handle streaming output in real-time

### 5. TTL Management

**v0:** Manual refresh
```http
POST /v1/sandboxes/{sessionId}/touch
```

**v1:** Automatic + activity-based
- TTL extends automatically on WebSocket activity
- No manual touch required
- Separate `idle_timeout` and `max_lifetime` controls

**Migration Required:**
- Remove manual TTL refresh calls
- Configure appropriate `idle_timeout` and `max_lifetime` values
- Update monitoring for session lifecycle changes

---

## New Features

### 1. Persistent Shell Sessions

Sessions maintain state across connections:

```javascript
// First connection
ws.send(JSON.stringify({
  type: 'create',
  agent_thread_id: 'my-session',
  image: 'python:3.11'
}));
ws.send(JSON.stringify({
  type: 'stdin',
  data: btoa('x = 42\n')
}));

// Disconnect...

// Later reconnect - 'x' is still defined!
ws2.send(JSON.stringify({
  type: 'create',
  agent_thread_id: 'my-session',  // Same ID
  image: 'python:3.11'
}));
ws2.send(JSON.stringify({
  type: 'stdin',
  data: btoa('print(x)\n')  // Outputs: 42
}));
```

### 2. Automatic Workspace Snapshot/Restore

Workspace automatically saved to MinIO on pod deletion:

- **Snapshot Trigger:** Pod deletion (via Finalizer)
- **Restore Trigger:** Pod ready after creation
- **Storage:** MinIO/S3 compatible
- **Format:** tar.gz compression
- **Key Pattern:** `snapshots/{workspace_id}/{project_id}/{agent_thread_id}/workspace.tar.gz`

### 3. Enhanced Security Controls

New security configuration options:

| Control | Type | Default | Description |
|---------|------|---------|-------------|
| `allow_network_access` | bool | `false` | Allow outbound network access |
| `readonly_filesystem` | bool | `false` | Mount root FS read-only |
| `drop_all_capabilities` | bool | `false` | Drop all Linux capabilities |
| `allow_privileged` | bool | `false` | Allow privileged mode |

Example:
```javascript
ws.send(JSON.stringify({
  type: 'create',
  agent_thread_id: 'secure-session',
  image: 'python:3.11',
  config: {
    allow_network_access: false,
    readonly_filesystem: true,
    drop_all_capabilities: true
  }
}));
```

### 4. Message Buffering

Automatic message buffering for reconnection scenarios:

- **Capacity:** 10,000 messages per session (configurable)
- **Behavior:** Circular buffer, overwrites oldest when full
- **Benefit:** Reconnecting clients receive missed output

### 5. Session Status Notifications

Real-time status updates:

```javascript
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  
  if (msg.type === 'status') {
    console.log(`State: ${msg.state}, Progress: ${msg.progress * 100}%`);
    // States: creating, restoring, ready, offline, error
  }
};
```

### 6. Exit Code Handling

Process exit notifications:

```javascript
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  
  if (msg.type === 'exit') {
    console.log(`Process exited with code: ${msg.code}`);
    // 0 = success, 1-127 = error, 128+N = signal
  }
};
```

---

## Migration Steps

### Phase 1: Preparation

**Step 1: Assess Current Usage**

Audit your v0 usage:

```bash
# Find all sandbox API calls
grep -r "v1/sandboxes" your-codebase/

# Identify session creation patterns
grep -r "PUT /v1/sandboxes" your-codebase/

# Check file operations
grep -r "files/upload\|files/download" your-codebase/
```

**Step 2: Plan Client Updates**

Document required changes:

| Component | Current (v0) | Target (v1) | Effort |
|-----------|--------------|-------------|--------|
| Session creation | REST PUT | WebSocket create | Medium |
| Command execution | REST POST | WebSocket stdin | Medium |
| File operations | REST endpoints | Shell commands | High |
| Error handling | HTTP status codes | WebSocket error messages | Low |
| Reconnection logic | N/A | Implement reconnect | Medium |

**Step 3: Set Up Test Environment**

Deploy v1 in a non-production environment:

```bash
# Deploy v1 manager
kubectl apply -k k8s/overlays/dev/

# Verify deployment
kubectl get pods -n sandbox-system

# Port-forward for testing
kubectl port-forward -n sandbox-system svc/sandbox-manager 8080:8080
```

### Phase 2: Client Implementation

**Step 4: Implement WebSocket Client**

Create a WebSocket client wrapper:

```javascript
class SandboxClient {
  constructor(url) {
    this.url = url;
    this.ws = null;
    this.messageHandlers = new Map();
    this.pendingCommands = new Map();
  }

  connect() {
    this.ws = new WebSocket(this.url);
    
    this.ws.onopen = () => {
      console.log('WebSocket connected');
    };
    
    this.ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      this.handleMessage(msg);
    };
    
    this.ws.onclose = () => {
      console.log('WebSocket disconnected');
      // Implement reconnect logic
      setTimeout(() => this.connect(), 5000);
    };
    
    this.ws.onerror = (error) => {
      console.error('WebSocket error:', error);
    };
  }

  createSession(agentThreadId, image, config) {
    this.send({
      type: 'create',
      agent_thread_id: agentThreadId,
      image: image,
      config: config
    });
  }

  sendCommand(command) {
    this.send({
      type: 'stdin',
      data: btoa(command + '\n')
    });
  }

  send(message) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    }
  }

  handleMessage(msg) {
    switch (msg.type) {
      case 'status':
        this.messageHandlers.get('status')?.(msg);
        break;
      case 'stdout':
      case 'stderr':
        this.messageHandlers.get('output')?.(msg);
        break;
      case 'exit':
        this.messageHandlers.get('exit')?.(msg);
        break;
      case 'error':
        this.messageHandlers.get('error')?.(msg);
        break;
    }
  }

  on(event, handler) {
    this.messageHandlers.set(event, handler);
  }

  disconnect() {
    if (this.ws) {
      this.ws.close();
    }
  }
}
```

**Step 5: Migrate Session Creation**

Replace v0 session creation:

**Before (v0):**
```javascript
async function createSession(sessionId) {
  const response = await fetch(`http://manager/v1/sandboxes/${sessionId}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      ttlSeconds: 900,
      cpuLimit: '1',
      memoryLimit: '1Gi'
    })
  });
  return response.json();
}
```

**After (v1):**
```javascript
function createSession(client, agentThreadId) {
  return new Promise((resolve, reject) => {
    client.on('status', (msg) => {
      if (msg.state === 'ready') {
        resolve();
      } else if (msg.state === 'error') {
        reject(new Error(msg.message));
      }
    });
    
    client.createSession(agentThreadId, 'python:3.11-slim', {
      cpu_limit: '1',
      memory_limit: '1Gi',
      idle_timeout: '15m'
    });
  });
}
```

**Step 6: Migrate Command Execution**

Replace v0 exec calls:

**Before (v0):**
```javascript
async function executeCommand(sessionId, command) {
  const response = await fetch(`http://manager/v1/sandboxes/${sessionId}/exec`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ cmd: command.split(' ') })
  });
  const result = await response.json();
  return result.stdout;
}
```

**After (v1):**
```javascript
function executeCommand(client, command) {
  return new Promise((resolve, reject) => {
    let output = '';
    
    const outputHandler = (msg) => {
      output += atob(msg.data);
    };
    
    const exitHandler = (msg) => {
      client.on('output', null);  // Remove handler
      client.on('exit', null);
      resolve(output);
    };
    
    client.on('output', outputHandler);
    client.on('exit', exitHandler);
    
    client.sendCommand(command);
  });
}
```

**Step 7: Migrate File Operations**

Replace v0 file upload/download:

**Before (v0):**
```javascript
async function uploadFile(sessionId, destPath, content) {
  await fetch(`http://manager/v1/sandboxes/${sessionId}/files/upload?dest=${destPath}`, {
    method: 'POST',
    body: content
  });
}

async function downloadFile(sessionId, srcPath) {
  const response = await fetch(`http://manager/v1/sandboxes/${sessionId}/files/download?src=${srcPath}`);
  return response.blob();
}
```

**After (v1):**
```javascript
async function uploadFile(client, destPath, content) {
  const base64Content = btoa(content);
  const escapedContent = content.replace(/'/g, "'\\''");
  
  await executeCommand(client, `cat > ${destPath} << 'EOF'\n${content}\nEOF`);
}

async function downloadFile(client, srcPath) {
  return await executeCommand(client, `cat ${srcPath}`);
}
```

### Phase 3: Testing & Validation

**Step 8: Unit Testing**

Update unit tests:

```javascript
describe('SandboxClient v1', () => {
  let client;
  let mockWs;

  beforeEach(() => {
    mockWs = createMockWebSocket();
    client = new SandboxClient('ws://test');
    client.ws = mockWs;
  });

  test('creates session with correct message', () => {
    client.createSession('test-id', 'python:3.11', {});
    
    expect(mockWs.send).toHaveBeenCalledWith(JSON.stringify({
      type: 'create',
      agent_thread_id: 'test-id',
      image: 'python:3.11',
      config: {}
    }));
  });

  test('handles status messages', (done) => {
    client.on('status', (msg) => {
      expect(msg.state).toBe('ready');
      done();
    });
    
    mockWs.simulateMessage({ type: 'status', state: 'ready' });
  });
});
```

**Step 9: Integration Testing**

Test against v1 environment:

```javascript
const testConfig = {
  url: 'ws://localhost:8080/ws',
  image: 'python:3.11-slim'
};

describe('Sandbox v1 Integration', () => {
  let client;

  beforeEach(async () => {
    client = new SandboxClient(testConfig.url);
    client.connect();
    await waitForOpen(client);
  });

  afterEach(() => {
    client.disconnect();
  });

  test('creates and runs session', async () => {
    await createSession(client, 'test-session-1');
    
    const result = await executeCommand(client, 'echo "Hello, v1!"');
    expect(result.trim()).toBe('Hello, v1!');
  });

  test('persists state across reconnections', async () => {
    await createSession(client, 'test-session-2');
    await executeCommand(client, 'x = 42');
    
    // Disconnect
    client.disconnect();
    
    // Reconnect to same session
    client.connect();
    await waitForOpen(client);
    await createSession(client, 'test-session-2');
    
    const result = await executeCommand(client, 'print(x)');
    expect(result.trim()).toBe('42');
  });
});
```

### Phase 4: Production Migration

**Step 10: Deploy v1 to Production**

```bash
# Backup current configuration
kubectl get configmap -n sandbox-system sandbox-config -o yaml > config-backup.yaml

# Deploy v1
kubectl apply -k k8s/overlays/prod/

# Monitor rollout
kubectl rollout status deployment/sandbox-manager -n sandbox-system

# Verify pods
kubectl get pods -n sandbox-system -l app=sandbox-manager
```

**Step 11: Traffic Migration**

Choose migration strategy:

**Option A: Blue-Green Deployment**
1. Deploy v1 alongside v0
2. Update DNS/load balancer to v1
3. Monitor for issues
4. Keep v0 running for rollback

**Option B: Canary Deployment**
1. Direct 10% of traffic to v1
2. Monitor metrics and errors
3. Gradually increase to 100%
4. Decommission v0

**Step 12: Monitor and Validate**

Key metrics to monitor:

```bash
# Session creation rate
kubectl logs -n sandbox-system deployment/sandbox-manager --tail=100 | grep "Creating session"

# Error rate
kubectl logs -n sandbox-system deployment/sandbox-manager --tail=100 | grep "ERROR"

# Snapshot/restore operations
kubectl logs -n sandbox-system deployment/sandbox-manager --tail=100 | grep -E "snapshot|restore"

# Pod lifecycle
kubectl get pods -n sandbox --sort-by=.metadata.creationTimestamp
```

---

## Configuration Changes

### Server-Side Configuration

**v0 Configuration:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: sandbox-config
  namespace: sandbox-system
data:
  runner-image-default: "sandbox-runner:1.0.0"
  ttl-default-seconds: "900"
  cpu-limit-default: "2"
  memory-limit-default: "1Gi"
```

**v1 Configuration:**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: sandbox-config
  namespace: sandbox-system
data:
  # WebSocket server
  server-port: "8080"
  
  # Kubernetes
  k8s-namespace: "sandbox"
  k8s-pod-namespace: "sandbox"
  
  # Storage (MinIO)
  storage-endpoint: "minio:9000"
  storage-bucket: "mbos-sandbox-snapshots"
  storage-use-ssl: "false"
  
  # Buffer
  buffer-capacity: "10000"
  
  # Defaults (can be overridden by clients)
  defaults-idle-timeout: "30m"
  defaults-max-lifetime: "24h"
  defaults-cpu-limit: "2"
  defaults-memory-limit: "1Gi"
  
  # tmux
  tmux-check-on-startup: "true"
```

**Migration Steps:**

1. Update ConfigMap with new fields
2. Configure MinIO/S3 credentials via Secret:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: sandbox-storage
  namespace: sandbox-system
type: Opaque
data:
  access-key: <BASE64_ACCESS_KEY>
  secret-key: <BASE64_SECRET_KEY>
```

3. Create sandbox namespace:
```bash
kubectl create namespace sandbox
```

4. Deploy Cleaner CronJob:
```bash
kubectl apply -f k8s/base/cleaner-cronjob.yaml
```

### Client-Side Configuration

**New Configuration Required:**

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `websocket_url` | string | Required | WebSocket server URL |
| `reconnect_interval` | duration | 5s | Reconnect delay |
| `max_reconnect_attempts` | int | 10 | Max reconnection tries |
| `message_timeout` | duration | 30s | Command response timeout |

**Example Configuration:**

```javascript
const sandboxConfig = {
  websocketUrl: 'wss://sandbox-manager.example.com/ws',
  reconnectInterval: 5000,
  maxReconnectAttempts: 10,
  messageTimeout: 30000,
  
  // Session defaults
  defaultImage: 'python:3.11-slim',
  defaultConfig: {
    cpu_limit: '1',
    memory_limit: '512Mi',
    idle_timeout: '15m',
    max_lifetime: '4h',
    allow_network_access: false
  }
};
```

---

## API Changes

### API Comparison

| Feature | v0 (REST) | v1 (WebSocket) |
|---------|-----------|----------------|
| **Protocol** | HTTP/1.1 | WebSocket (RFC 6455) |
| **Base URL** | `http://manager/v1/sandboxes` | `ws://manager/ws` |
| **Auth** | `X-Service-Key` header | Planned for Phase 2 |
| **Session ID** | URL parameter | Message field (`agent_thread_id`) |
| **Request** | JSON body | JSON message |
| **Response** | JSON response | JSON message (async) |
| **Streaming** | No | Yes (stdout/stderr) |

### Endpoint Mapping

| v0 Operation | v1 Equivalent |
|--------------|---------------|
| `PUT /v1/sandboxes/{id}` | `{type: "create", agent_thread_id: "..."}` |
| `DELETE /v1/sandboxes/{id}` | No direct equivalent (TTL-based) |
| `POST /v1/sandboxes/{id}/touch` | Automatic (activity-based) |
| `POST /v1/sandboxes/{id}/exec` | `{type: "stdin", data: "..."}` |
| `POST /v1/sandboxes/{id}/files/upload` | Shell commands (cat, tee, etc.) |
| `GET /v1/sandboxes/{id}/files/download` | Shell commands (cat, etc.) |
| `GET /healthz` | Separate HTTP endpoint |
| `GET /readyz` | Separate HTTP endpoint |

### Message Schema Changes

**v0 Request:**
```json
{
  "ttlSeconds": 900,
  "image": "sandbox-runner:1.0.0",
  "cpuLimit": "1",
  "memoryLimit": "1Gi",
  "env": {"KEY": "VALUE"}
}
```

**v1 Create Message:**
```json
{
  "type": "create",
  "agent_thread_id": "at_abc123",
  "image": "python:3.11-slim",
  "command": ["/bin/bash"],
  "env": {"KEY": "VALUE"},
  "config": {
    "cpu_limit": "1",
    "memory_limit": "1Gi",
    "idle_timeout": "15m",
    "max_lifetime": "24h",
    "allow_network_access": false,
    "readonly_filesystem": false,
    "drop_all_capabilities": false,
    "allow_privileged": false
  }
}
```

### Error Handling Changes

**v0 Errors:**
```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "Invalid session ID",
    "requestId": "req-123"
  }
}
```

**v1 Errors:**
```json
{
  "type": "error",
  "code": "INVALID_REQUEST",
  "message": "Malformed message or missing required fields",
  "details": {}
}
```

---

## Rollback Procedure

### Immediate Rollback (< 1 hour)

**Step 1: Stop v1 Traffic**

```bash
# If using service selector labels
kubectl patch svc sandbox-manager -n sandbox-system -p '{"spec":{"selector":{"version":"v0"}}}'

# Or update DNS/load balancer to point to v0
```

**Step 2: Scale Down v1**

```bash
kubectl scale deployment/sandbox-manager -n sandbox-system --replicas=0
```

**Step 3: Verify v0 is Running**

```bash
kubectl get pods -n sandbox-system -l app=sandbox-manager,version=v0
kubectl logs -n sandbox-system deployment/sandbox-manager-v0 --tail=50
```

### Extended Rollback (Restore v0)

**Step 1: Restore v0 Configuration**

```bash
kubectl apply -f k8s/overlays/prod-v0/
```

**Step 2: Restore v0 Deployment**

```bash
kubectl rollout undo deployment/sandbox-manager -n sandbox-system
```

**Step 3: Remove v1 Resources**

```bash
# Remove Cleaner CronJob
kubectl delete cronjob/sandbox-cleaner -n sandbox-system

# Remove sandbox namespace (optional - keep for snapshot recovery)
kubectl delete namespace sandbox
```

**Step 4: Restore DNS/Load Balancer**

Update your DNS or load balancer to point back to v0.

### Data Preservation

**Snapshots in MinIO are preserved** during rollback. When you migrate to v1 again:

1. Existing snapshots remain available
2. Sessions restore from previous snapshots
3. No data loss occurs

**To completely remove v1 snapshots:**

```bash
# Using MinIO client
mc rm --recursive --force minio/mbos-sandbox-snapshots/
```

---

## Testing Recommendations

### Pre-Migration Testing

**1. Smoke Tests**

```javascript
describe('Sandbox v1 Smoke Tests', () => {
  test('WebSocket connection', async () => {
    const client = new SandboxClient('ws://test/ws');
    client.connect();
    await waitForOpen(client);
    expect(client.ws.readyState).toBe(WebSocket.OPEN);
  });

  test('Session creation', async () => {
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'smoke-test-1');
    
    const result = await executeCommand(client, 'echo test');
    expect(result).toContain('test');
  });

  test('Session persistence', async () => {
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'smoke-test-2');
    await executeCommand(client, 'echo persistent > /tmp/test.txt');
    
    client.disconnect();
    client.connect();
    await createSession(client, 'smoke-test-2');
    
    const result = await executeCommand(client, 'cat /tmp/test.txt');
    expect(result).toContain('persistent');
  });
});
```

**2. Functional Tests**

```javascript
describe('Sandbox v1 Functional Tests', () => {
  test('Python execution', async () => {
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'func-test-python', 'python:3.11');
    
    const result = await executeCommand(client, 'python -c "print(2+2)"');
    expect(result.trim()).toBe('4');
  });

  test('File operations', async () => {
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'func-test-files');
    
    await executeCommand(client, 'echo "content" > /workspace/test.txt');
    const result = await executeCommand(client, 'cat /workspace/test.txt');
    expect(result.trim()).toBe('content');
  });

  test('Network isolation', async () => {
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'func-test-network', 'python:3.11', {
      allow_network_access: false
    });
    
    // Should fail when network is disabled
    await expectAsync(executeCommand(client, 'curl https://example.com'))
      .toBeRejected();
  });
});
```

**3. Performance Tests**

```javascript
describe('Sandbox v1 Performance Tests', () => {
  test('Session creation latency', async () => {
    const start = Date.now();
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'perf-test-1');
    const duration = Date.now() - start;
    
    expect(duration).toBeLessThan(5000); // 5 seconds max
  });

  test('Message throughput', async () => {
    const client = new SandboxClient('ws://test/ws');
    await createSession(client, 'perf-test-2');
    
    const start = Date.now();
    for (let i = 0; i < 100; i++) {
      await executeCommand(client, `echo ${i}`);
    }
    const duration = Date.now() - start;
    
    expect(duration).toBeLessThan(10000); // 100 commands in 10 seconds
  });

  test('Concurrent sessions', async () => {
    const clients = [];
    for (let i = 0; i < 10; i++) {
      const client = new SandboxClient('ws://test/ws');
      await createSession(client, `perf-test-concurrent-${i}`);
      clients.push(client);
    }
    
    // All sessions should be ready
    for (const client of clients) {
      const result = await executeCommand(client, 'echo ready');
      expect(result).toContain('ready');
    }
  });
});
```

### Post-Migration Testing

**1. Health Checks**

```bash
# Manager health
curl http://sandbox-manager.sandbox-system.svc/healthz
curl http://sandbox-manager.sandbox-manager.svc/readyz

# Check pods
kubectl get pods -n sandbox-system
kubectl get pods -n sandbox
```

**2. Integration Tests**

Run your application's integration tests against v1.

**3. Monitoring Validation**

Verify metrics are being collected:

```bash
# Prometheus metrics
curl http://sandbox-manager.sandbox-system.svc/metrics

# Check for errors
kubectl logs -n sandbox-system deployment/sandbox-manager --tail=100 | grep -i error
```

**4. Snapshot/Restore Verification**

```bash
# Create a session with data
# (via client)

# Force pod deletion
kubectl delete pod sbx-test-session -n sandbox

# Verify snapshot exists
mc ls minio/mbos-sandbox-snapshots/snapshots/default/default/test-session/

# Reconnect and verify data restored
# (via client)
```

### Load Testing

**Test Plan:**

1. **Baseline:** Measure v0 performance metrics
2. **Migration:** Deploy v1 to canary
3. **Comparison:** Run same load test against v1
4. **Validation:** Compare metrics

**Key Metrics:**

| Metric | v0 Target | v1 Target | Notes |
|--------|-----------|-----------|-------|
| Session creation latency | < 3s | < 5s | v1 includes snapshot restore |
| Command execution latency | < 500ms | < 500ms | Should be similar |
| Concurrent sessions | 100+ | 100+ | No regression |
| Memory per session | < 100Mi | < 150Mi | v1 has buffer overhead |
| Error rate | < 0.1% | < 0.1% | No regression |

---

## Troubleshooting

### Common Issues

**Issue 1: WebSocket Connection Fails**

```
Error: WebSocket connection failed
```

**Solutions:**
1. Verify WebSocket endpoint is accessible:
```bash
curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" http://sandbox-manager:8080/ws
```

2. Check firewall/proxy allows WebSocket connections
3. Verify TLS certificate if using `wss://`

**Issue 2: Session Stuck in "Creating" State**

```
Status: creating (progress: 0.1) - never reaches ready
```

**Solutions:**
1. Check Pod is being created:
```bash
kubectl get pods -n sandbox -l agent_thread_id=your-session-id
```

2. Check Pod logs:
```bash
kubectl logs -n sandbox sbx-your-session-id
```

3. Verify image exists and is accessible
4. Check for resource quota issues

**Issue 3: Snapshot Restore Fails**

```
Error: Failed to restore workspace snapshot
```

**Solutions:**
1. Verify MinIO/S3 connectivity:
```bash
kubectl exec -n sandbox-system deployment/sandbox-manager -- nc -zv minio 9000
```

2. Check snapshot exists:
```bash
mc ls minio/mbos-sandbox-snapshots/snapshots/.../your-session-id/
```

3. Check manager logs for detailed errors
4. Session will start with empty workspace if restore fails

**Issue 4: Session Expires Too Quickly**

```
Session terminated after 5 minutes despite activity
```

**Solutions:**
1. Check `max_lifetime` configuration:
```javascript
config: {
  max_lifetime: '24h',  // Increase if needed
  idle_timeout: '30m'   // Increase if needed
}
```

2. Verify WebSocket is staying connected
3. Check Cleaner CronJob is running:
```bash
kubectl get cronjob -n sandbox-system
```

**Issue 5: tmux Session Not Found**

```
Error: tmux session not found
```

**Solutions:**
1. Verify container image includes `tmux`:
```bash
docker run --rm python:3.11-slim which tmux
# If not found, use different image or install tmux
```

2. Check startup script is running
3. Verify Pod hasn't restarted (tmux session is lost on restart)

### Getting Help

If you encounter issues not covered here:

1. **Check Logs:**
```bash
# Manager logs
kubectl logs -n sandbox-system deployment/sandbox-manager --tail=200 -f

# Cleaner logs
kubectl logs -n sandbox-system job/sandbox-cleaner-<timestamp> --tail=100

# Pod logs
kubectl logs -n sandbox sbx-<session-id> --tail=100
```

2. **Describe Resources:**
```bash
kubectl describe pod -n sandbox sbx-<session-id>
kubectl describe deployment -n sandbox-system sandbox-manager
```

3. **Check Events:**
```bash
kubectl get events -n sandbox --sort-by=.lastTimestamp
kubectl get events -n sandbox-system --sort-by=.lastTimestamp
```

4. **Review Documentation:**
- API Reference: `docs/api-reference-v1.md`
- Design Doc: `docs/plans/2026-02-03-sandbox-refactor-design.md`
- Troubleshooting: `docs/TROUBLESHOOTING.md`

---

## Support

### Resources

- **Documentation:** `/docs` directory in repository
- **API Reference:** `docs/api-reference-v1.md`
- **Design Documents:** `docs/plans/`
- **Source Code:** `/manager-service` directory

### Getting Help

For questions, issues, or feature requests:

1. **Internal Teams:** Contact the Sandbox team via your organization's communication channels
2. **Bug Reports:** Create an issue in the repository with:
   - Environment details (Kubernetes version, platform)
   - Reproduction steps
   - Logs and error messages
3. **Feature Requests:** Create an issue with the `enhancement` label

### Version Compatibility

| Component | v0 | v1 |
|-----------|----|----|
| **Kubernetes** | 1.24+ | 1.24+ |
| **Go Runtime** | 1.21+ | 1.21+ |
| **Client Protocol** | HTTP/1.1 | WebSocket (RFC 6455) |
| **Storage** | None required | MinIO/S3 required |
| **Browser Support** | All modern browsers | All modern browsers (WebSocket) |

---

## Checklist

Use this checklist to track your migration progress:

### Preparation
- [ ] Audit current v0 usage
- [ ] Document required client changes
- [ ] Set up v1 test environment
- [ ] Review and plan client implementation
- [ ] Identify and schedule migration windows

### Client Implementation
- [ ] Implement WebSocket client wrapper
- [ ] Migrate session creation
- [ ] Migrate command execution
- [ ] Migrate file operations
- [ ] Update error handling
- [ ] Implement reconnection logic
- [ ] Remove v0 REST API calls

### Testing
- [ ] Write unit tests for WebSocket client
- [ ] Write integration tests for v1
- [ ] Run smoke tests against v1
- [ ] Run functional tests against v1
- [ ] Run performance tests against v1
- [ ] Validate snapshot/restore functionality
- [ ] Test security controls

### Deployment
- [ ] Deploy v1 to staging
- [ ] Configure MinIO/S3 storage
- [ ] Deploy Cleaner CronJob
- [ ] Run load tests on staging
- [ ] Plan traffic migration strategy
- [ ] Schedule production deployment

### Production Migration
- [ ] Deploy v1 to production
- [ ] Migrate traffic (blue-green or canary)
- [ ] Monitor health metrics
- [ ] Monitor error rates
- [ ] Validate session functionality
- [ ] Complete traffic migration
- [ ] Decommission v0 (after validation period)

### Post-Migration
- [ ] Document any custom configurations
- [ ] Update runbooks and SOPs
- [ ] Train operations team
- [ ] Update monitoring dashboards
- [ ] Schedule post-migration review

---

**Document Version:** 1.0  
**Last Modified:** 2026-02-04  
**Next Review:** After production migration
