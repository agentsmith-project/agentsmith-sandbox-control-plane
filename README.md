# mbos-sandbox-v1

Simplified Kubernetes Sandbox Manager

## Overview

A minimal Kubernetes pod lifecycle manager with JuiceFS-backed persistent workspaces. Provides a REST API for creating, managing, and executing commands in workload pods. Application-agnostic — the caller (e.g. AgentSmith) controls what runs inside pods.

Workload pods are ephemeral compute units. Persistence is JVS-only: the manager assigns a workspace (JuiceFS + JVS) to each pod on creation and restores the latest JVS state when a pod is created. Clients send keepalive to the manager; if no keepalive is received within the idle threshold, the cleaner deletes the pod (no snapshot or GC on cleanup—lifecycle is manager-controlled).

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  manager-service                     │
│                                                      │
│  ┌──────────────┐   ┌──────────────────────────┐    │
│  │ Service Key   │──▶│ Workload API Handler      │    │
│  │ Auth          │   │ (PUT/GET/DELETE/keepalive/exec)│    │
│  └──────────────┘   └──────┬───────────┬────────┘    │
│                             │           │             │
│                    ┌────────▼──┐  ┌─────▼──────────┐ │
│                    │ K8s Client │  │ K8s Executor   │ │
│                    │ (pods,     │  │ (SPDY exec     │ │
│                    │  patch)    │  │  streaming)    │ │
│                    └────────┬──┘  └─────┬──────────┘ │
│                             │           │             │
│                    ┌────────▼───────────▼──────────┐ │
│                    │     JVS Workspace Storage      │ │
│                    │   (snapshot / restore on       │ │
│                    │    JuiceFS mount)              │ │
│                    └───────────────────────────────┘ │
└─────────────────────────────────────────────────────┘

┌───────────────────────┐
│  cleaner (CronJob)    │   Separate binary.
│  Scans for expired    │   Deletes pods whose
│  pods (no keepalive   │   expires_at is past;
│  within threshold)    │   no snapshot/GC.
└───────────────────────┘
```

**Core components:**

| Component | Package | Role |
|---|---|---|
| Service Key Auth | `internal/auth` | Validates `X-Service-Key` header using constant-time comparison |
| Workload API Handler | `internal/workload` | REST endpoint routing for pod lifecycle and exec |
| K8s Client | `internal/k8s` | Pod CRUD, activity patching, readiness polling |
| K8s Executor | `internal/k8s` | SPDY-based command execution inside pods |
| JVS Workspace Storage | `internal/workspace` | JVS workspace prepare/restore on JuiceFS (manager-assigned) |

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Create or ensure workload pod |
| `GET` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Get pod status |
| `DELETE` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}` | Delete pod |
| `POST` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive` | Client keepalive (extend expires_at) |
| `POST` | `/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec` | Execute command in pod |
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe (checks K8s connectivity + config) |
| `GET` | `/metrics` | Prometheus-format metrics |

See [docs/api-reference-v2.md](docs/api-reference-v2.md) for full request/response schemas and examples.

## Authentication

All `/v1/` routes require a valid `X-Service-Key` header. Keys are loaded from the `SERVICE_KEYS` environment variable. Health, readiness, and metrics endpoints are unauthenticated.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVICE_KEYS` | *(required)* | Comma-separated list of valid service keys |
| `K8S_NAMESPACE` | `sandbox` | Kubernetes namespace for workload pods |
| `JUICEFS_BASE_PATH` | `/mnt/juicefs/workloads` | JuiceFS mount path for workspace storage |
| `JUICEFS_PVC_NAME` | `juicefs-workloads-pvc` | PVC name mounted into workload pods |
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | Path to YAML configuration file |

### YAML Configuration

The YAML config controls server behavior, K8s client tuning, and rate limiting. It supports hot-reload via filesystem watching. See [docs/CONFIGURATION_GUIDE.md](docs/CONFIGURATION_GUIDE.md) for the full schema.

Key defaults:

- HTTP port: `8080`
- Auth header: `X-Service-Key`
- K8s QPS/Burst: `50/100`
- K8s retry: 3 attempts, 200ms–2s exponential backoff
- Rate limit: 100 RPS global, 10 RPS per-IP, 5 RPS per-session

## Quick Start

```bash
# Build
cd manager-service
go build -o bin/manager ./cmd/manager/
go build -o bin/cleaner ./cmd/cleaner/

# Configure
export SERVICE_KEYS="my-secret-key"
export K8S_NAMESPACE="sandbox"
export JUICEFS_BASE_PATH="/mnt/juicefs/workloads"
export JUICEFS_PVC_NAME="juicefs-workloads-pvc"

# Run (requires kubeconfig or in-cluster config)
./bin/manager
```

Or use the Makefile:

```bash
make test           # Run unit tests with race detector
make build-manager  # Build manager container image
make kind-up        # Create a local kind cluster + deploy
make smoke          # Port-forward + smoke test
```

## Project Structure

```
manager-service/
├── cmd/
│   ├── manager/          # Main service binary
│   └── cleaner/          # TTL cleanup CronJob binary
├── internal/
│   ├── app/              # Application lifecycle, HTTP server setup
│   ├── auth/             # Service Key validator + middleware
│   ├── config/           # YAML config loading, validation, hot-reload
│   ├── errors/           # Retry utilities
│   ├── k8s/              # K8s client, pod operations, SPDY exec
│   ├── observability/    # Structured logging, Prometheus metrics, health checks
│   ├── ratelimit/        # Three-tier rate limiting (global/per-IP/per-session)
│   ├── workload/         # Workload REST handler (types, routing, pod builder)
│   └── workspace/        # JVS workspace storage (snapshot, restore, GC)
└── go.mod
k8s/                      # Kubernetes manifests (base, overlays, scripts)
docs/                     # Documentation and contracts
Makefile                  # Build, test, and infrastructure targets
```

## How It Works

1. **Pod Creation (PUT):** The handler prepares the JVS workspace (restoring the latest snapshot if one exists), builds a pod spec with the JuiceFS PVC mounted at `/workspace`, and creates it in Kubernetes. Waits up to 120s for the pod to become ready.

2. **Command Execution (POST /exec):** Opens a SPDY stream to the pod's `main` container and executes the given command. Returns stdout, stderr, exit code, and duration.

3. **Keepalive (POST /keepalive):** Client sends keepalive periodically. Manager updates `expires_at` and `last_activity_at`. If no keepalive is received for `idle_timeout_sec`, the pod is considered expired (capped by `max_lifetime_sec`).

4. **Pod Deletion (DELETE):** Deletes the pod. No snapshot or resource handling—manager controls lifecycle.

5. **Cleaner (CronJob):** Scans for pods whose `expires_at` is in the past (no keepalive within threshold), deletes them only. No snapshot or GC.
