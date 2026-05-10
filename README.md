# AgentSmith Sandbox Manager

Kubernetes workload manager for AgentSmith internal agents.

## Overview

Sandbox Manager owns the sandbox-side workload lifecycle:

- workspace binding materialization from an AFSCP workload mount plan
- workload pod lifecycle
- K8s PV/PVC and pod mount resources
- exec, keepalive, release, and reclaim APIs

AgentSmith owns workspace/project selection and task orchestration. For storage access, AgentSmith submits only `namespace_id` and `mount_binding_id`; sandbox calls AFSCP as the sandbox orchestrator, reads the current mount plan, and applies that plan to Kubernetes resources.

## Product Truth

- AFSCP is the source of truth for payload location, mount path, read-only mode, CSI secret reference, and security policy.
- Sandbox does not accept caller-supplied storage backend settings or caller-supplied pod mount paths.
- Workload pods mount the binding PVC at the AFSCP plan `mount_path`.
- The container working directory is `<mount_path>/workspace`.
- PV CSI `subdir` carries the AFSCP `payload_volume_subdir`; workload `VolumeMount.SubPath` is not part of the caller contract.

## Architecture

```text
AgentSmith
  | namespace_id + mount_binding_id
  v
Sandbox Manager
  | GET AFSCP orchestrator mount plan
  v
Kubernetes
  | PV/PVC from plan, workload Pod from binding id
  v
Workload container
  | TASK_HOME=<mount_path>
  | WORKSPACE_PATH=<mount_path>/workspace
```

See also:

- [docs/AFSCP_WORKLOAD_MOUNT_MODEL.md](docs/AFSCP_WORKLOAD_MOUNT_MODEL.md)
- [docs/contracts/agentsmith-integration-contract.md](docs/contracts/agentsmith-integration-contract.md)
- [docs/api-reference.md](docs/api-reference.md)
- [docs/runbook.md](docs/runbook.md)

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}` | Create or ensure a workspace binding from an AFSCP plan |
| `GET` | `/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}` | Get sanitized workspace binding status |
| `DELETE` | `/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}` | Delete sandbox-managed PV/PVC resources |
| `PUT` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Create or ensure workload pod |
| `GET` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Get workload pod status |
| `DELETE` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Delete workload pod and close AFSCP mount lifecycle |
| `POST` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive` | Heartbeat AFSCP and extend workload expiry |
| `POST` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec` | Execute command in workload pod |
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe |
| `GET` | `/metrics` | Prometheus metrics |

## Authentication

All `/v1/` routes require a valid `X-Service-Key` header. Health, readiness, and metrics endpoints are unauthenticated.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVICE_KEYS` | *(required)* | Comma-separated valid service keys |
| `K8S_NAMESPACE` | `sandbox-workloads` | Namespace for workspace bindings and workload pods |
| `AFSCP_INTERNAL_BASE_URL` | *(required)* | AFSCP internal API base URL |
| `AFSCP_ORCHESTRATOR_TOKEN` | *(required)* | Sandbox orchestrator token for AFSCP |
| `AFSCP_CALLER_SERVICE` | `sandbox-orchestrator` | Caller service header sent to AFSCP |
| `AFSCP_ACTOR_TYPE` | `system` | Actor type for AFSCP lifecycle calls |
| `AFSCP_ACTOR_ID` | `sandbox-manager` | Actor id for AFSCP lifecycle calls |
| `JUICEFS_CSI_DRIVER` | `csi.juicefs.com` | CSI driver name used for plan materialization |
| `JUICEFS_STORAGE_CAPACITY` | `1Pi` | Requested PVC capacity for each binding |
| `JUICEFS_STORAGE_CLASS_NAME` | *(unset)* | Optional storage class for binding PV/PVC |
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | YAML config path |

## Quick Start

```bash
cd manager-service
go build -o bin/manager ./cmd/manager/

export SERVICE_KEYS="my-secret-key"
export K8S_NAMESPACE="sandbox-workloads"
export AFSCP_INTERNAL_BASE_URL="http://localhost:20000"
export AFSCP_ORCHESTRATOR_TOKEN="sandbox-orchestrator-token"

./bin/manager
```

## Release Readiness

Before calling the service release-ready, verify:

1. Workspace binding ensure fetches the AFSCP plan and creates/reuses PV/PVC.
2. Workload create accepts `workspace_binding_id` only for mount selection.
3. Pod mount path, read-only mode, working directory, and runtime env come from the plan.
4. Keepalive and delete call the AFSCP workload mount lifecycle endpoints.
5. Docs, tests, and runbooks describe this same AFSCP plan consumer model.
