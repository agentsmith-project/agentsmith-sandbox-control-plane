# AgentSmith Sandbox Manager Integration Contract

This document defines the current integration contract between AgentSmith and `mbos-sandbox-v1`.

## Product Boundary

AgentSmith decides:

- which workspace / project / repo should be used
- when an AFSCP workload mount binding should be created
- what commands should run inside the task workspace directory

Sandbox Manager decides:

- how the AFSCP orchestrator mount plan maps to Kubernetes PV/PVC and workload pod mounts
- workload pod lifecycle, keepalive, release, and exec

AFSCP is the authority for payload subdir, mount path, read-only mode, secret reference, and security policy. Sandbox Manager does not accept caller-provided storage backend settings or storage auth material.

## Authentication

All `/v1/` routes require:

```http
X-Service-Key: <service-key>
```

Sandbox Manager calls AFSCP with its configured orchestrator service identity:

- `Authorization: Bearer <AFSCP_ORCHESTRATOR_TOKEN>`
- `X-AFSCP-Caller-Service: <AFSCP_CALLER_SERVICE>`
- `X-AFSCP-Namespace-Id: <namespace_id>`

Mutating AFSCP lifecycle calls also include `Idempotency-Key`, `X-AFSCP-Actor-Type`, and `X-AFSCP-Actor-Id`.

## Path Model

Binding endpoints:

```text
/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

Workload endpoints:

```text
/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

| Path Parameter | AgentSmith Source |
|----------------|-------------------|
| `wsId` | workspace id |
| `projId` | project id |
| `bindingId` | AFSCP workload mount binding id (`wmb_*`) |
| `wlId` | workload id |

## 1. Ensure Workspace Binding

AgentSmith creates an AFSCP workload mount binding before creating an internal workload. Sandbox Manager consumes that binding through the AFSCP orchestrator plan endpoint.

```http
PUT /v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

```json
{
  "namespace_id": "ns_demo",
  "mount_binding_id": "wmb_demo"
}
```

Expected behavior:

- `mount_binding_id` matches `bindingId`
- sandbox fetches AFSCP `OrchestratorMountPlan` as the sandbox orchestrator identity
- sandbox creates or reuses PV/PVC with the AFSCP plan's `payload_volume_subdir`, `secret_ref`, `mount_path`, `read_only`, and `security_policy`
- response returns only sanitized binding status; it does not return `secret_ref` or `payload_volume_subdir`
- request bodies containing storage backend settings or storage auth material are rejected

## 2. Create Or Ensure Workload

AgentSmith creates a workload only after the workspace binding is ready.

```http
PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

```json
{
  "image": "registry.example.com/agent-runner:latest",
  "env": {
    "AGENT_ID": "agent-001",
    "THREAD_ID": "thread-abc"
  },
  "cpu_request": "500m",
  "cpu_limit": "2",
  "memory_request": "512Mi",
  "memory_limit": "4Gi",
  "idle_timeout_sec": 1800,
  "max_lifetime_sec": 86400,
  "workspace_binding_id": "wmb_demo"
}
```

Expected behavior:

- pod is created or reused idempotently
- pod mounts the binding PVC at AFSCP plan `mount_path`
- pod read-only mode follows AFSCP plan `read_only`
- writable plans prepare `workspace` and `.artifacts`; read-only plans do not run writable init
- container working directory is `<mount_path>/workspace`
- runtime env includes `TASK_HOME=<mount_path>`, `HOME=<mount_path>`, and `WORKSPACE_PATH=<mount_path>/workspace`
- caller-provided `mount_path`, `sub_path`, and `working_dir` are rejected
- create re-checks the AFSCP plan before pod creation; unavailable or changed plans fail closed
- if an existing pod differs from the plan-derived spec, create/ensure returns `409 Conflict`

## 3. Start Agent Process

After the pod is running, AgentSmith starts the actual process using `/exec`.

```http
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec
```

AgentSmith is responsible for the command it runs. Sandbox Manager only provides the execution channel.

## 4. Health And Keepalive

AgentSmith uses:

- `GET /workloads/{wlId}` for pod status
- `POST /workloads/{wlId}/keepalive` to heartbeat AFSCP and extend local `expires_at`
- `POST /workloads/{wlId}/exec` for process-level checks

## 5. Deletion Semantics

Deleting a workload removes compute and closes the AFSCP mount lifecycle:

```http
DELETE /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

Sandbox Manager calls AFSCP release with a stable idempotency key before deleting the pod. It reports `released` only after the pod is confirmed gone. If release or pod deletion fails, the pod remains so the same workload delete can be retried; pod deletion failure does not write terminal AFSCP status.

Deleting a workspace binding removes the sandbox-managed PV/PVC:

```http
DELETE /v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

AgentSmith should delete workload pods before deleting their workspace binding.
