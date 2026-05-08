# API Reference v2

## Authentication

All `/v1/` routes require:

```http
X-Service-Key: <service-key>
```

Health (`/healthz`), readiness (`/readyz`), and metrics (`/metrics`) do not require authentication.

All error responses use:

```json
{"error":"<message>"}
```

## Workspace Binding Endpoints

Base path:

```
/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

### PUT — Create or Ensure Workspace Binding

Creates or reuses a stable JuiceFS CSI binding for a file library.

**Request**

```json
{
  "file_library_id": "flib_demo",
  "filesystem_name": "agentsmith-workspace",
  "metadata_url": "postgres://postgres:postgres@db:5432/juicefs?sslmode=disable",
  "storage_endpoint": "http://minio:9000",
  "storage_capacity": "1Pi",
  "storage_class_name": "juicefs-csi-sc",
  "mount_options": ["cache-dir=/var/lib/juicefs/cache", "writeback_cache"],
  "subdir": "workspaces/flib_demo"
}
```

**Response**

```json
{
  "binding_id": "flib_demo",
  "workspace_id": "ws_001",
  "project_id": "proj_001",
  "file_library_id": "flib_demo",
  "status": "ready",
  "namespace": "sandbox-workloads",
  "secret_name": "workspace-binding-ws-001-proj-001-flib-demo",
  "pv_name": "workspace-binding-ws-001-proj-001-flib-demo",
  "pvc_name": "workspace-binding-ws-001-proj-001-flib-demo",
  "volume_handle": "ws_001/proj_001/flib_demo",
  "filesystem_name": "agentsmith-workspace",
  "mount_path": "/workspace"
}
```

### GET — Get Workspace Binding

Returns the current binding status.

### DELETE — Delete Workspace Binding

Deletes the binding Secret/PV/PVC managed by sandbox.

## Workload Endpoints

Base path:

```
/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

### PUT — Create or Ensure Workload Pod

Creates a workload pod or returns the existing one.

**Request**

```json
{
  "image": "registry.example.com/runner:latest",
  "command": ["tail", "-f", "/dev/null"],
  "env": {
    "AGENT_ID": "agent-001"
  },
  "cpu_request": "250m",
  "cpu_limit": "2",
  "memory_request": "256Mi",
  "memory_limit": "2Gi",
  "idle_timeout_sec": 1800,
  "max_lifetime_sec": 86400,
  "workspace_binding_id": "flib_demo",
  "mount_path": "/home/task-abc",
  "sub_path": "agent-tasks/task-abc",
  "working_dir": "/home/task-abc/workspace"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `image` | yes | Container image |
| `command` | no | Optional container command; defaults to keep-alive |
| `env` | no | Additional env vars; `TASK_HOME`, `HOME`, and `WORKSPACE_PATH` are injected from workload paths |
| `cpu_request` | no | CPU request |
| `cpu_limit` | no | CPU limit |
| `memory_request` | no | Memory request |
| `memory_limit` | no | Memory limit |
| `idle_timeout_sec` | no | Idle timeout in seconds |
| `max_lifetime_sec` | no | Maximum pod lifetime |
| `workspace_binding_id` | yes | Workspace binding PVC to mount |
| `mount_path` | no | Container PVC mount path. New Agent task workloads should pass `/home/<task_home_segment>`. Defaults to `/workspace` for legacy callers. Must be absolute and under an allowed prefix. |
| `sub_path` | no | PVC subPath to mount. New Agent task workloads should pass `agent-tasks/<task_home_segment>`. Must be relative and cannot contain `..`. |
| `working_dir` | no | Container working directory. New Agent task workloads should pass `/home/<task_home_segment>/workspace`. Must be absolute and inside `mount_path`. |

When a workload is created with the Agent task path fields above, the generated pod has:

- `VolumeMount.MountPath = mount_path`
- `VolumeMount.SubPath = sub_path`
- `Container.WorkingDir = working_dir`
- env `TASK_HOME=mount_path`, `HOME=mount_path`, `WORKSPACE_PATH=working_dir`

If `PUT` finds an existing pod but its mount path, subPath, workingDir, PVC, or runtime path env differs from the requested spec, the manager returns `409 Conflict` instead of silently treating the request as idempotent.

**Response**

```json
{
  "pod_name": "workload-wl_001",
  "phase": "Running",
  "ip": "10.244.0.15",
  "started_at": "2026-03-20T10:00:00Z",
  "expires_at": "2026-03-20T10:30:00Z"
}
```

### GET — Get Workload Status

Returns the workload pod status, or `{ "phase": "offline" }` when missing.

### DELETE — Delete Workload Pod

Deletes the workload pod only. It does not delete the workspace binding.

### POST /keepalive — Extend Workload Expiry

Extends `expires_at` using the workload idle timeout and max lifetime policy.

### POST /exec — Execute Command

Runs a command in the `main` container of the workload pod and returns stdout, stderr, exit code, and duration.
