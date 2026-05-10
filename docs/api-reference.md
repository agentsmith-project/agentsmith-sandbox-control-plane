# API Reference

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

```text
/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

`bindingId` is the AFSCP workload mount binding id (`wmb_*`). AgentSmith creates the AFSCP binding first, then asks sandbox manager to materialize the runtime PV/PVC from the orchestrator-only mount plan.

### PUT - Create Or Ensure Workspace Binding

**Request**

```json
{
  "namespace_id": "ns_demo",
  "mount_binding_id": "wmb_demo"
}
```

`mount_binding_id` must match the path `bindingId`. The request body must not include storage backend settings or storage auth material.

**Behavior**

- sandbox manager calls AFSCP `GET /internal/v1/workload-mount-bindings/{mountBindingId}/orchestrator-plan`
- the AFSCP request uses the configured sandbox orchestrator service identity
- sandbox creates or reuses a PV/PVC using `payload_volume_subdir`, `secret_ref`, `mount_path`, `read_only`, and `security_policy` from that plan
- sandbox does not create storage auth Secrets and does not accept caller-supplied storage backend settings

**Response**

```json
{
  "binding_id": "wmb_demo",
  "workspace_id": "ws_001",
  "project_id": "proj_001",
  "status": "ready",
  "namespace": "sandbox-workloads",
  "pv_name": "juicefs-pv-ws-001-proj-001-wmb-demo",
  "pvc_name": "juicefs-pvc-ws-001-proj-001-wmb-demo",
  "volume_handle": "juicefs-ws-001-proj-001-wmb-demo",
  "namespace_id": "ns_demo",
  "mount_binding_id": "wmb_demo",
  "volume_id": "vol_demo",
  "mount_path": "/home/task-demo",
  "read_only": false
}
```

The response intentionally omits `secret_ref` and `payload_volume_subdir`.

### GET - Get Workspace Binding

Returns the sanitized binding status shown above.

### DELETE - Delete Workspace Binding

Deletes the PV/PVC managed by sandbox. AFSCP binding lifecycle is closed through workload release/status calls during workload deletion.

## Workload Endpoints

Base path:

```text
/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

### PUT - Create Or Ensure Workload Pod

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
  "workspace_binding_id": "wmb_demo"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `image` | yes | Container image |
| `command` | no | Optional container command |
| `env` | no | Additional env vars; `TASK_HOME`, `HOME`, and `WORKSPACE_PATH` are derived from the AFSCP plan |
| `cpu_request` | no | CPU request |
| `cpu_limit` | no | CPU limit |
| `memory_request` | no | Memory request |
| `memory_limit` | no | Memory limit |
| `idle_timeout_sec` | no | Idle timeout in seconds |
| `max_lifetime_sec` | no | Maximum pod lifetime |
| `workspace_binding_id` | yes | AFSCP-backed workspace binding id to mount |

`mount_path`, `sub_path`, `working_dir`, storage backend settings, and storage auth material are not accepted in this request.

The generated pod has:

- `VolumeMount.MountPath = mount_path` from AFSCP plan
- `VolumeMount.ReadOnly = read_only` from AFSCP plan
- writable plans prepare `workspace` and `.artifacts`; read-only plans skip writable init
- CSI PV `subdir = payload_volume_subdir` from AFSCP plan
- CSI `NodePublishSecretRef = secret_ref` from AFSCP plan
- `Container.WorkingDir = <mount_path>/workspace`
- env `TASK_HOME=<mount_path>`, `HOME=<mount_path>`, `WORKSPACE_PATH=<mount_path>/workspace`

Before creating a pod, sandbox re-checks the AFSCP orchestrator plan for the binding. If the plan is unavailable, create fails closed; if it differs from the PVC annotations, create returns `409 Conflict` and the binding must be re-ensured.

If `PUT` finds an existing pod but its mount path, readOnly bit, working dir, PVC, or runtime path env differs from the requested plan-derived spec, the manager returns `409 Conflict`.

### GET - Get Workload Status

Returns the workload pod status, or `{ "phase": "offline" }` when missing.

### DELETE - Delete Workload Pod

Calls AFSCP release with a stable idempotency key, deletes the pod, confirms it is gone, then reports `released`. If release or pod deletion fails, the pod is left in place so a later `DELETE` can retry; pod deletion failure does not write terminal AFSCP status.

### POST /keepalive - Extend Workload Expiry

Heartbeats the AFSCP workload mount binding and extends local `expires_at` using the workload idle timeout and max lifetime policy.

### POST /exec - Execute Command

Runs a command in the `main` container of the workload pod and returns stdout, stderr, exit code, and duration.
