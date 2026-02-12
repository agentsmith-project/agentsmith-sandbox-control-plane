# Sandbox Manager Service

## Overview

The Manager Service is the core component of the Sandbox system. It provides an HTTP API for:
- Creating and managing sandbox pods (via Kubernetes)
- Executing commands in pods via `kubectl exec` with SSE streaming output
- File upload and download (tar.gz format)
- Session management (TTL, activity tracking)
- Automatic snapshot/restore of workspace state

## Architecture

```
Client --HTTP/SSE--> Manager --kubectl exec--> Pod (sleep infinity)
                       |
                       +---> MinIO/S3 (snapshots)
```

- **No authentication**: The manager runs in a trusted intranet; the calling client handles auth.
- **No shell-bridge**: Commands are executed directly via `kubectl exec`.
- **SSE streaming**: Command output (stdout/stderr) is streamed as Server-Sent Events.
- **Snapshot/restore**: On pod deletion, workspace is archived to object storage and restored on next creation.

## Quick Start

### Local Development

```bash
# Build the manager binary
cd manager-service && go build -o /tmp/sandbox-manager ./cmd/manager

# Run (requires accessible K8s cluster via KUBECONFIG or in-cluster config)
export CONFIG_PATH="$(pwd)/manager-config.example.yaml"
./manager
```

### Build Image

```bash
./scripts/build-image.sh        # Build Docker image
./scripts/build-image.sh -l     # Build and load into kind
./scripts/build-image.sh -p     # Build and push
```

### Testing

```bash
# From the project root:
make test          # Run unit tests
make test-unit     # Run unit tests (same as above)
make vet           # Run go vet
```

## Configuration

The manager is configured via a YAML file. See `manager-config.example.yaml` for the full schema.

**Environment Variables:**
- `CONFIG_PATH`: Path to YAML config file (default: `/etc/sandbox-manager/manager-config.yaml`)
- `CONFIG_RELOAD_DEBOUNCE` / `CONFIG_RELOAD_MIN_INTERVAL` / `CONFIG_RELOAD_BACKOFF_MAX`: Hot-reload tuning (optional)
- `STRICT_CONFIG_RELOAD`: Fail on invalid config reload (optional)
- `STORAGE_ENDPOINT` / `STORAGE_ACCESS_KEY` / `STORAGE_SECRET_KEY` / `STORAGE_BUCKET`: Object storage credentials

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| PUT | `/v1/sandboxes/{id}` | Create or ensure sandbox pod |
| POST | `/v1/sandboxes/{id}/exec` | Execute command (SSE stream) |
| POST | `/v1/sandboxes/{id}/touch` | Extend TTL |
| POST | `/v1/sandboxes/{id}/files/upload` | Upload tar.gz to pod |
| GET | `/v1/sandboxes/{id}/files/download` | Download tar.gz from pod |
| DELETE | `/v1/sandboxes/{id}` | Delete sandbox pod |
| GET | `/healthz` | Liveness check |
| GET | `/readyz` | Readiness check |
| GET | `/metrics` | Prometheus metrics |

## Scripts

- `build.sh` - Build Go binary
- `build-image.sh` - Build Docker image
- `test.sh` - Run tests
- `lint.sh` - Code quality checks

## Troubleshooting

1. **K8s connection failed**: Check `KUBECONFIG` or in-cluster config
2. **Pod creation failed**: Check RBAC permissions and namespace
3. **Image pull failed**: Verify image exists and pull secrets are configured
4. **Go version**: Requires Go 1.24+
