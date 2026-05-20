# ASBCP API Contract

Contract version: `v1`

ASBCP exposes lifecycle APIs for workspace bindings and workloads. API paths are scoped by AgentSmith workspace and project identifiers, but ASBCP does not authorize end users. AgentSmith performs product authorization before calling ASBCP.

## Health

| Method | Path | Auth | Response |
| --- | --- | --- | --- |
| `GET` | `/healthz` | No | Liveness status |
| `GET` | `/readyz` | No | Readiness status |
| `GET` | `/metrics` | No | Prometheus metrics |

## Workspace Binding

| Method | Path | Purpose |
| --- | --- | --- |
| `PUT` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}` | Ensure ASBCP resources from an AFSCP mount plan |
| `GET` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}` | Read sanitized binding status |
| `DELETE` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}` | Delete ASBCP-managed binding resources |

Create request fields:

- `namespace_id`: AgentSmith namespace or tenant context used by AFSCP.
- `mount_binding_id`: AFSCP workload mount binding identifier.

ASBCP must not accept caller-provided storage endpoints, raw credentials, or pod mount paths.

## Workload

| Method | Path | Purpose |
| --- | --- | --- |
| `PUT` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}` | Ensure workload Pod |
| `GET` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}` | Read workload status |
| `POST` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}/keepalive` | Extend workload lifetime and AFSCP lifecycle |
| `POST` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}/exec` | Execute command in the workload Pod |
| `DELETE` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}` | Release AFSCP lifecycle and delete workload Pod |

Workload create request fields:

- `workspace_binding_id`: ASBCP binding to mount.
- `image`: Workload container image selected by AgentSmith.
- `command`: Optional command selected by AgentSmith.
- `env`: Optional environment variables selected by AgentSmith.
- `cpu_request`: Optional CPU request quantity.
- `cpu_limit`: Optional CPU limit quantity.
- `memory_request`: Optional memory request quantity.
- `memory_limit`: Optional memory limit quantity.
- `idle_timeout_sec`: Optional idle timeout in seconds.
- `max_lifetime_sec`: Optional maximum lifetime in seconds.

ASBCP does not choose the runner image and does not own AgentSmith task policy.

Kubernetes object identity is scope-qualified. Workload Pod lookup and mutation are keyed by `{workspace_id, project_id, workload_id}` and must verify Pod annotations and labels match that URL scope before status, keepalive, exec, release, or delete behavior proceeds. Workspace binding PV/PVC and workload fact object names are also generated from structured identity parts with a bounded DNS-label slug plus hash suffix, not by ambiguous hyphen concatenation.

## Compatibility

Breaking changes require a new contract version and release notes. Tag releases must include the API contract version in the GitHub Release body.
