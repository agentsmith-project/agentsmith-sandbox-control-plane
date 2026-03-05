# API Reference v2

## Authentication

All `/v1/` routes require the `X-Service-Key` header. Requests without a valid key receive `401 Unauthorized`.

```
X-Service-Key: <your-service-key>
```

Health (`/healthz`), readiness (`/readyz`), and metrics (`/metrics`) endpoints do not require authentication.

### Error Response Format

All error responses use a consistent JSON envelope:

```json
{"error": "<message>"}
```

---

## Workload Endpoints

Base path: `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}`

Path parameters:

| Parameter | Description |
|-----------|-------------|
| `wsId` | Workspace identifier |
| `projId` | Project identifier |
| `wlId` | Workload identifier (used to derive the pod name: `workload-{wlId}`) |

---

### PUT — Create or Ensure Workload Pod

Creates a new workload pod, or returns the existing pod if it already exists. On creation, the handler restores the latest JVS workspace snapshot (if any) and mounts it at `/workspace` inside the pod.

**Request**

```
PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
Content-Type: application/json
X-Service-Key: <key>
```

```json
{
  "image": "registry.example.com/runner:latest",
  "command": ["bash", "-c", "sleep infinity"],
  "env": {
    "AGENT_ID": "agent-001",
    "LOG_LEVEL": "debug"
  },
  "cpu_request": "250m",
  "cpu_limit": "2",
  "memory_request": "256Mi",
  "memory_limit": "2Gi",
  "idle_timeout_sec": 1800,
  "max_lifetime_sec": 86400
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `image` | string | **yes** | — | Container image |
| `command` | string[] | no | `["tail", "-f", "/dev/null"]` | Container entrypoint. Omit to keep the pod alive indefinitely. |
| `env` | map[string]string | no | `{}` | Environment variables. `WORKSPACE_PATH=/workspace` is always injected. |
| `cpu_request` | string | no | *(none)* | CPU request (Kubernetes quantity, e.g. `"250m"`) |
| `cpu_limit` | string | no | *(none)* | CPU limit |
| `memory_request` | string | no | *(none)* | Memory request (e.g. `"256Mi"`) |
| `memory_limit` | string | no | *(none)* | Memory limit |
| `idle_timeout_sec` | int | no | `1800` (30 min) | Seconds of inactivity before the pod expires |
| `max_lifetime_sec` | int | no | `86400` (24 h) | Maximum pod lifetime in seconds |

**Response — 201 Created** (new pod)

```json
{
  "pod_name": "workload-abc123",
  "phase": "Running",
  "ip": "10.244.0.15",
  "started_at": "2026-02-28T10:00:00Z",
  "expires_at": "2026-02-28T10:30:00Z"
}
```

**Response — 200 OK** (pod already exists)

```json
{
  "pod_name": "workload-abc123",
  "phase": "Running",
  "ip": "10.244.0.15",
  "started_at": "2026-02-28T09:45:00Z",
  "message": "pod already exists"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `pod_name` | string | Kubernetes pod name |
| `phase` | string | Pod phase (`Pending`, `Running`, `Succeeded`, `Failed`) |
| `ip` | string | Pod IP address (empty if not yet assigned) |
| `started_at` | string | Pod creation timestamp (RFC 3339) |
| `expires_at` | string | When the pod will expire (RFC 3339). Only present on 201. |
| `message` | string | Human-readable message. Only present on 200. |

**Status Codes**

| Code | Meaning |
|------|---------|
| 201 | Pod created and (optionally) ready |
| 200 | Pod already existed |
| 400 | Invalid request body or missing `image` |
| 401 | Missing or invalid service key |
| 429 | Rate limit exceeded |
| 500 | Workspace preparation or pod creation failed |

**Example**

```bash
curl -X PUT \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workloads/wl_001 \
  -H "X-Service-Key: my-secret-key" \
  -H "Content-Type: application/json" \
  -d '{
    "image": "ubuntu:22.04",
    "idle_timeout_sec": 3600,
    "max_lifetime_sec": 86400
  }'
```

---

### GET — Get Workload Status

Returns the current status of a workload pod. If the pod does not exist, returns `{"phase": "offline"}` with status 200 (not 404) so callers can poll without error handling.

**Request**

```
GET /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
X-Service-Key: <key>
```

**Response — 200 OK** (pod exists)

```json
{
  "pod_name": "workload-wl_001",
  "phase": "Running",
  "ip": "10.244.0.15",
  "started_at": "2026-02-28T10:00:00Z",
  "last_activity_at": "2026-02-28T10:25:00Z",
  "expires_at": "2026-02-28T10:30:00Z"
}
```

**Response — 200 OK** (pod does not exist)

```json
{
  "phase": "offline"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `pod_name` | string | Kubernetes pod name |
| `phase` | string | Pod phase or `"offline"` if the pod doesn't exist |
| `ip` | string | Pod IP address |
| `started_at` | string | Pod creation timestamp (RFC 3339) |
| `last_activity_at` | string | Last activity timestamp from pod annotation |
| `expires_at` | string | Expiration timestamp from pod annotation |

**Status Codes**

| Code | Meaning |
|------|---------|
| 200 | Status returned (including `"offline"` for missing pods) |
| 401 | Missing or invalid service key |
| 429 | Rate limit exceeded |
| 500 | K8s API error |

**Example**

```bash
curl -s \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workloads/wl_001 \
  -H "X-Service-Key: my-secret-key" | jq .
```

---

### DELETE — Delete Workload Pod

Deletes the workload pod. No snapshot or resource handling; lifecycle is manager-controlled.

**Request**

```
DELETE /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
X-Service-Key: <key>
```

No request body.

**Response — 200 OK**

```json
{
  "message": "pod deleted"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `message` | string | Human-readable result message |

**Status Codes**

| Code | Meaning |
|------|---------|
| 200 | Pod deleted |
| 401 | Missing or invalid service key |
| 404 | Pod not found |
| 429 | Rate limit exceeded |
| 500 | Pod deletion failed |

**Example**

```bash
curl -X DELETE \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workloads/wl_001 \
  -H "X-Service-Key: my-secret-key"
```

---

### POST /keepalive — Client Keepalive

Clients must send keepalive periodically. The manager updates the pod's `last_activity_at` and `expires_at` (set to `now + idle_timeout_sec`, capped by `max_lifetime_sec`). If no keepalive is received within the idle threshold, the cleaner will delete the pod.

**Request**

```
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive
X-Service-Key: <key>
```

No request body.

**Response — 200 OK**

```json
{
  "expires_at": "2026-02-28T11:00:00Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `expires_at` | string | New expiration timestamp (RFC 3339) |

**Status Codes**

| Code | Meaning |
|------|---------|
| 200 | Keepalive accepted |
| 401 | Missing or invalid service key |
| 404 | Pod not found |
| 429 | Rate limit exceeded |
| 500 | Failed to patch pod annotations |

**Example**

```bash
curl -X POST \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workloads/wl_001/keepalive \
  -H "X-Service-Key: my-secret-key"
```

---

### POST /exec — Execute Command in Pod

Executes a command inside the workload pod's `main` container via Kubernetes SPDY exec. The command runs synchronously and returns stdout, stderr, exit code, and wall-clock duration.

**Request**

```
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec
Content-Type: application/json
X-Service-Key: <key>
```

```json
{
  "cmd": ["bash", "-c", "echo hello && ls /workspace"],
  "timeout_seconds": 60
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `cmd` | string[] | **yes** | — | Command and arguments to execute |
| `timeout_seconds` | int | no | `30` | Maximum execution time in seconds |

**Response — 200 OK**

```json
{
  "exit_code": 0,
  "stdout": "hello\nfile1.txt\nfile2.py\n",
  "stderr": "",
  "duration_ms": 42
}
```

| Field | Type | Description |
|-------|------|-------------|
| `exit_code` | int | Process exit code (0 = success, -1 = exec infrastructure error) |
| `stdout` | string | Standard output |
| `stderr` | string | Standard error |
| `duration_ms` | int | Wall-clock execution time in milliseconds |

**Status Codes**

| Code | Meaning |
|------|---------|
| 200 | Command executed (check `exit_code` for process result) |
| 400 | Invalid request body or empty `cmd` |
| 401 | Missing or invalid service key |
| 429 | Rate limit exceeded |
| 500 | SPDY connection or exec infrastructure failure |

**Example**

```bash
curl -X POST \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workloads/wl_001/exec \
  -H "X-Service-Key: my-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["python3", "-c", "print(42)"], "timeout_seconds": 10}'
```

---

## Operational Endpoints

These endpoints are unauthenticated and intended for Kubernetes probes and monitoring.

### GET /healthz — Liveness Probe

Returns 200 if the process is running. Always succeeds.

**Response — 200 OK**

```json
{
  "status": "ok",
  "time": "2026-02-28T10:00:00Z"
}
```

---

### GET /readyz — Readiness Probe

Returns 200 only if all readiness checks pass (K8s API connectivity + configuration loaded).

**Response — 200 OK**

```json
{
  "ready": true,
  "configLoaded": true,
  "k8sConnected": true,
  "message": "Service is ready"
}
```

**Response — 503 Service Unavailable**

```json
{
  "ready": false,
  "configLoaded": false,
  "k8sConnected": false,
  "message": "Service is not ready: check_A, check_B"
}
```

---

### GET /metrics — Prometheus Metrics

Returns metrics in Prometheus text exposition format. Includes:

- `http_request_total{method, path, status}` — request counter
- `http_request_duration_seconds{method, path}` — request duration histogram
- `workload_create_total`, `workload_keepalive_total`, `workload_exec_total`, `workload_delete_total` — workload counters
- `config_reload_success_total`, `config_reload_failure_total` — config reload counters
- `config_hash_info{hash}` — current config hash
- `k8s_api_fail_total{operation}` — K8s API failure counter

---

## Rate Limiting

All `/v1/` routes are subject to three-tier rate limiting:

| Tier | Default Rate | Default Burst |
|------|-------------|---------------|
| Global | 100 req/s | 200 |
| Per-IP | 10 req/s | 20 |
| Per-Session | 5 req/s | 10 |

When rate-limited, the server returns `429 Too Many Requests` with body `rate limit exceeded`.
