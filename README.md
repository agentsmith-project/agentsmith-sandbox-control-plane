# mbos-sandbox-v1

Simplified Kubernetes workload manager for AgentSmith internal agents.

## Overview

`mbos-sandbox-v1` is the platform-side execution service behind AgentSmith internal agents.

It owns:

- workload pod lifecycle
- JuiceFS CSI workspace binding lifecycle
- `/workspace` mount delivery inside workload pods
- exec and keepalive APIs
- reclaim of expired compute pods

It does **not** own notebook business logic, file-library selection, or task orchestration. Those remain in `agentsmith`.

## Product Truth

- A workspace file library is the persistent runtime environment
- Sandbox mounts that environment at `/workspace`
- Compute pods are ephemeral
- Workspace data persists through JuiceFS CSI
- Keepalive and TTL only govern pod lifetime, not workspace lifetime

## Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                        manager-service                        │
│                                                               │
│  ┌──────────────┐   ┌──────────────────────────────────────┐  │
│  │ Service Key  │──▶│ HTTP API                             │  │
│  │ Auth         │   │ - workspace bindings                 │  │
│  └──────────────┘   │ - workloads                          │  │
│                     │ - keepalive / exec / delete          │  │
│                     └──────────────┬───────────────────────┘  │
│                                    │                          │
│                    ┌───────────────▼───────────────┐          │
│                    │ K8s Client / Executor         │          │
│                    │ - Secrets / PV / PVC          │          │
│                    │ - Pods / exec / status        │          │
│                    └───────────────┬───────────────┘          │
│                                    │                          │
│                    ┌───────────────▼───────────────┐          │
│                    │ JuiceFS CSI                   │          │
│                    │ - static PV / PVC             │          │
│                    │ - shared mount via /workspace │          │
│                    └───────────────────────────────┘          │
└───────────────────────────────────────────────────────────────┘
```

See also:

- [docs/JUICEFS_CSI_WORKSPACE_MODEL.md](docs/JUICEFS_CSI_WORKSPACE_MODEL.md)
- [docs/contracts/agentsmith-integration-contract-v2.md](docs/contracts/agentsmith-integration-contract-v2.md)
- [docs/api-reference-v2.md](docs/api-reference-v2.md)
- [docs/runbook.md](docs/runbook.md)

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}` | Create or ensure a JuiceFS CSI workspace binding |
| `GET` | `/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}` | Get workspace binding status |
| `DELETE` | `/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}` | Delete workspace binding resources |
| `PUT` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Create or ensure workload pod |
| `GET` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Get workload pod status |
| `DELETE` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Delete workload pod |
| `POST` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive` | Extend workload expiry |
| `POST` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec` | Execute command in workload pod |
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe |
| `GET` | `/metrics` | Prometheus metrics |

## Authentication

All `/v1/` routes require a valid `X-Service-Key` header. Health, readiness, and metrics endpoints are unauthenticated.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVICE_KEYS` | *(required)* | Comma-separated valid service keys |
| `K8S_NAMESPACE` | `sandbox-workloads` | Namespace for workspace bindings and workload pods |
| `JUICEFS_CSI_DRIVER` | `csi.juicefs.com` | CSI driver name |
| `JUICEFS_STORAGE_CAPACITY` | `1Pi` | Requested PVC capacity for each binding |
| `JUICEFS_STORAGE_CLASS_NAME` | *(unset)* | Optional storage class for binding PV/PVC |
| `JUICEFS_MOUNT_OPTIONS` | *(unset)* | Comma-separated JuiceFS mount options |
| `JUICEFS_SUBDIR` | *(unset)* | Optional volume subdir prefix |
| `JUICEFS_MOUNT_SERVICE_ACCOUNT` | *(unset)* | Optional mount pod service account |
| `JUICEFS_MOUNT_IMAGE` | *(unset)* | Optional mount pod image override |
| `JUICEFS_STORAGE_ENDPOINT` | `http://localhost:19000` | Object storage endpoint written into the JuiceFS secret |
| `JUICEFS_STORAGE_CREDENTIAL_SEED` | `sandbox-juicefs-credential-seed` | Deterministic seed used when generating binding secrets |
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | YAML config path |

### YAML Configuration

The YAML config controls server behavior, Kubernetes client tuning, and rate limiting. See [manager-service/manager-config.example.yaml](manager-service/manager-config.example.yaml).

## Quick Start

```bash
cd manager-service
go build -o bin/manager ./cmd/manager/
go build -o bin/cleaner ./cmd/cleaner/

export SERVICE_KEYS="my-secret-key"
export K8S_NAMESPACE="sandbox-workloads"
export JUICEFS_CSI_DRIVER="csi.juicefs.com"
export JUICEFS_STORAGE_CAPACITY="1Pi"

./bin/manager
```

## How It Works

1. **Ensure workspace binding** — a caller ensures `workspace_binding_id`, which produces the Secret, PV, and PVC needed for a stable JuiceFS CSI mount.
2. **Create workload** — the caller creates a workload with `workspace_binding_id`; the manager mounts the bound PVC at `/workspace`.
3. **Exec / keepalive** — AgentSmith runs commands inside the pod and periodically extends `expires_at`.
4. **Delete workload** — deleting a workload only removes compute. The workspace binding and its JuiceFS-backed data remain until the binding is deleted.
5. **Cleaner** — removes expired workload pods only. It does not delete workspace bindings.

## Release Readiness

Before calling the service release-ready, verify:

1. A workspace binding can be ensured and reused for the same file library.
2. A workload pod mounts `/workspace` from that binding.
3. Deleting or reclaiming the pod does not remove workspace contents.
4. AgentSmith internal agents and notebook tasks can reuse the same workspace binding across restarts.
5. All operational docs describe JuiceFS CSI bindings as the only persistence truth.
