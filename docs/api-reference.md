# API Reference

This reference summarizes the active ASBCP API contract. The normative contract lives in `docs/contracts/api-contract.md`.

## Authentication

All `/v1/` routes require:

```http
X-Service-Key: <service-key>
```

Health (`/healthz`), readiness (`/readyz`), and metrics (`/metrics`) do not require authentication.

## Workspace Binding Endpoints

Base path:

```text
/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}
```

`binding_id` is the AFSCP workload mount binding id. AgentSmith creates the AFSCP binding first, then asks ASBCP to materialize runtime PV/PVC resources from the AFSCP orchestrator mount plan.

### PUT

```json
{
  "namespace_id": "ns_demo",
  "mount_binding_id": "wmb_demo"
}
```

Expected behavior:

- `mount_binding_id` matches the path binding id.
- ASBCP calls AFSCP for the orchestrator mount plan.
- ASBCP creates or reuses Kubernetes binding resources from plan fields.
- The response is sanitized and does not expose AFSCP secret reference or payload subdirectory.
- Request bodies containing storage backend settings or storage auth material are rejected.

### GET

Returns sanitized binding status.

### DELETE

Deletes ASBCP-managed binding resources. AFSCP workload lifecycle release is handled through workload deletion.

## Workload Endpoints

Base path:

```text
/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}
```

### PUT

```json
{
  "image": "registry.example.com/agent-runner:v1",
  "command": ["tail", "-f", "/dev/null"],
  "env": {
    "AGENT_ID": "agent-001"
  },
  "cpu_request": "250m",
  "cpu_limit": "1",
  "memory_request": "512Mi",
  "memory_limit": "1Gi",
  "workspace_binding_id": "wmb_demo",
  "idle_timeout_sec": 1800,
  "max_lifetime_sec": 86400
}
```

Expected behavior:

- ASBCP creates or reuses the workload Pod.
- Pod mount path and read-only mode come from the AFSCP plan.
- Container working directory is `<mount_path>/workspace`.
- `TASK_HOME`, `HOME`, and `WORKSPACE_PATH` are derived from the AFSCP plan.
- Caller-provided mount path, sub path, working directory, storage backend settings, and storage auth material are rejected.
- If an existing Pod differs from the plan-derived spec, ASBCP returns a lifecycle conflict.
- Pod identity is scoped by `{workspace_id, project_id, workload_id}`. ASBCP validates Pod annotations and labels against the URL scope before GET status, keepalive, exec, release, or delete operations.

### GET

Returns workload Pod status, or an offline/missing status when absent.
Status may include `image` and `image_ref` from the main container spec image. `image_id` is returned only when Kubernetes status exposes the main container `ImageID`; ASBCP does not derive it from `containerStatuses[].image` or spec image.

### POST `/keepalive`

Heartbeats the AFSCP workload mount binding and extends local expiry.

### POST `/exec`

Runs a command in the workload Pod and returns execution status according to implementation capability.

### DELETE

Calls AFSCP release with a stable idempotency key, confirms release, flushes the mounted payload path when applicable, deletes the Pod, and reports release only after deletion is confirmed. Failures should leave enough state for retry.
