# AgentSmith ASBCP Integration Contract

This document defines the integration contract between AgentSmith and AgentSmith Sandbox Control Plane (ASBCP).

## Product Boundary

AgentSmith decides:

- Which workspace, project, and repository should be used.
- When an AFSCP workload mount binding should be created.
- Which runner image and command should run inside the task workspace directory.
- Product authorization, audit, and policy.

ASBCP decides:

- How the AFSCP orchestrator mount plan maps to Kubernetes binding resources and workload Pod mounts.
- Workload Pod lifecycle, keepalive, release, and exec.

AFSCP is the authority for payload location, mount path, read-only mode, secret reference, and security policy. ASBCP does not accept caller-provided storage backend settings or storage auth material.

## Authentication

All `/v1/` ASBCP routes require:

```http
X-Service-Key: <service-key>
```

AgentSmith configures:

- `ASBCP_INTERNAL_BASE_URL`
- `ASBCP_SERVICE_KEY`

ASBCP calls AFSCP with its configured service identity:

- `Authorization: Bearer <ASBCP_AFSCP_ORCHESTRATOR_TOKEN>`
- `X-AFSCP-Caller-Service: agentsmith-sandbox-control-plane`
- `X-AFSCP-Namespace-Id: <namespace_id>`

Mutating AFSCP lifecycle calls also include idempotency and actor headers.

## Path Model

Binding endpoints:

```text
/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}
```

Workload endpoints:

```text
/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}
```

## 1. Ensure Workspace Binding

AgentSmith creates an AFSCP workload mount binding before creating an internal workload. ASBCP consumes that binding through the AFSCP orchestrator plan endpoint.

```http
PUT /v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}
```

```json
{
  "namespace_id": "ns_demo",
  "mount_binding_id": "wmb_demo"
}
```

Expected behavior:

- `mount_binding_id` matches `binding_id`.
- ASBCP fetches the AFSCP plan with canonical service identity.
- ASBCP creates or reuses Kubernetes binding resources from the plan.
- Binding readiness requires the per-binding PVC to be `Bound` with a `volumeName`; static PV/PVC binding may complete asynchronously.
- If the PVC is not yet `Bound`, ASBCP may return `503` code `not_ready` with `Retry-After`. This is a per-binding readiness gap and does not necessarily mean the ASBCP service is unavailable.
- Response returns only sanitized binding status.
- Storage backend settings and storage auth material in request bodies are rejected.

## 2. Create Or Ensure Workload

AgentSmith creates a workload only after the workspace binding is ready.

```http
PUT /v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}
```

```json
{
  "image": "registry.example.com/agent-runner:v1",
  "env": {
    "AGENT_ID": "agent-001",
    "THREAD_ID": "thread-abc"
  },
  "workspace_binding_id": "wmb_demo"
}
```

Expected behavior:

- Pod is created or reused idempotently.
- Pod mounts the binding at AFSCP plan `mount_path`.
- Pod read-only mode follows AFSCP plan `read_only`.
- Container working directory is `<mount_path>/workspace`.
- Runtime env includes `TASK_HOME`, `HOME`, and `WORKSPACE_PATH` derived from the plan.
- Caller-provided mount path, sub path, working directory, storage backend settings, and storage auth material are rejected.
- Unavailable or changed plans fail closed.
- If the workspace binding PVC is not yet `Bound`, workload create fails with retryable `503` code `not_ready`; AgentSmith should retry binding ensure before retrying workload create.
- Pod identity is scope-qualified by `{workspace_id, project_id, workload_id}`. All status, keepalive, exec, release, and delete paths validate Pod annotations and labels against the URL scope before operating on the Pod.
- Workload status returns `image`/`image_ref` from the main container spec image and returns `image_id` only from the main container Kubernetes `ImageID` when status exposes it; `containerStatuses[].image` is not live image identity.

## 3. Start Agent Process

After the Pod is running, AgentSmith starts the actual process using `/exec`.

```http
POST /v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}/exec
```

AgentSmith is responsible for the command it runs. ASBCP provides the execution channel.

## 4. Health And Keepalive

AgentSmith uses:

- `GET /workloads/{workload_id}` for Pod status.
- `POST /workloads/{workload_id}/keepalive` to heartbeat AFSCP and extend local expiry.
- `POST /workloads/{workload_id}/exec` for process-level checks.

## 4a. Runtime Readiness Diagnostics

When AgentSmith records `AGENT_SANDBOX_UNAVAILABLE`, it should preserve ASBCP
create/status call summaries with the AgentSmith API request, pod-manager
diagnostic summary, and Kubernetes pod/status/event evidence. The ASBCP side of
that handoff is sanitized API/log/status data: operation, request id, workload
id or binding id, phase/status when known, HTTP status, stable ASBCP error
code, and `Retry-After` for retryable readiness gaps.

This section only defines consumer diagnostics correlation. It does not make
AgentSmith backend-real gates, runtime flake classification, or Product
Readiness an ASBCP release gate.

## 5. Deletion Semantics

Deleting a workload removes compute and closes the AFSCP mount lifecycle:

```http
DELETE /v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}
```

ASBCP calls AFSCP release with a stable idempotency key before deleting the Pod. If release or deletion fails, the same workload delete should be retryable.
If a Pod is still Pending and its main container never started, ASBCP may skip the storage flush barrier because no workload writer existed; the delete still closes AFSCP release and terminal status through the same retryable lifecycle.
ASBCP serializes concurrent DELETE requests for the same workload and sends terminal released status with a stable `observed_at` and workload/mount scoped idempotency key, so repeated request IDs observe the same release/status convergence instead of creating AFSCP idempotency conflicts.

Deleting a workspace binding removes ASBCP-managed binding resources:

```http
DELETE /v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}
```

AgentSmith should delete workload Pods before deleting their workspace binding.
