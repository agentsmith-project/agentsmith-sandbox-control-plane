# MBOS Sandbox v1

Kubernetes-based sandbox execution service. Provides a simple HTTP API for creating sandbox pods, executing commands (with SSE streaming), and managing files.

## Architecture

```
Client (HTTP) → Manager Service → kubectl exec → Pod Container (sleep infinity)
```

- **Manager Service**: Go HTTP server that manages sandbox pod lifecycle and proxies command execution via Kubernetes API.
- **Runner Pods**: Ubuntu-based containers with development tools. Entrypoint is `sleep infinity`; commands are executed on-demand via `kubectl exec`.
- **No auth/rate-limiting**: The manager runs in a trusted internal network. Authentication and traffic control are the responsibility of the upstream client.

## API

All endpoints are under `/v1/sandboxes/{id}`:

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/v1/sandboxes/{id}` | Create sandbox (returns JSON) |
| `DELETE` | `/v1/sandboxes/{id}` | Delete sandbox (triggers snapshot) |
| `POST` | `/v1/sandboxes/{id}/exec` | Execute command (**SSE streaming**) |
| `POST` | `/v1/sandboxes/{id}/touch` | Update activity / extend TTL |
| `POST` | `/v1/sandboxes/{id}/files/upload` | Upload tar.gz archive |
| `GET` | `/v1/sandboxes/{id}/files/download` | Download tar.gz archive |

### SSE Exec Streaming

```bash
curl -N -X POST http://localhost:8080/v1/sandboxes/my-session/exec \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["echo", "hello world"]}'
```

Response (`text/event-stream`):
```
event: stdout
data: {"data":"aGVsbG8gd29ybGQK"}

event: exit
data: {"exit_code":0,"duration_ms":42}
```

Output data is base64-encoded.

## Health Checks

- `GET /healthz` — Liveness
- `GET /readyz` — Readiness (checks K8s connectivity + config)
- `GET /metrics` — Prometheus metrics

## Development

```bash
# Build
make build

# Run tests
make test

# Run go vet
make vet
```

## Manual run and client testing

To run the sandbox locally and drive it manually with a client (e.g. `curl`):

### Prerequisites

- **Docker** (for Kind and images)
- **kind** on `PATH`
- **kubectl** (or run `./sbx tools fetch` in this repo)
- **curl**, **jq** (for manual API calls)

### 1. Start the dev environment

```bash
./sbx dev up
```

This creates a Kind cluster `sandbox-cluster`, builds manager + runner images, and deploys the stack (namespaces `sandbox-system`, `sandbox`, Manager, MinIO, etc.).

### 2. Expose the Manager

In another terminal, port-forward so you can reach the Manager from your machine:

```bash
kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80
```

Leave it running. The client talks to `http://localhost:8080`.

### 3. Use curl as the client

**Create a sandbox**

```bash
export SID="my-session-$(date +%s)"
curl -s -X PUT "http://localhost:8080/v1/sandboxes/${SID}" \
  -H "Content-Type: application/json" \
  -d '{"ttlSeconds": 300}'
```

Expect `200` and a JSON body.

**Run a command (SSE)** — stdout is base64-encoded:

```bash
curl -N -X POST "http://localhost:8080/v1/sandboxes/${SID}/exec" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["echo", "hello world"]}'
```

Decode one line of output (e.g. the `data` field from a `stdout` event):

```bash
echo 'aGVsbG8gd29ybGQK' | base64 -d
```

**More one-shot commands**

```bash
# list root
curl -N -X POST "http://localhost:8080/v1/sandboxes/${SID}/exec" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["ls", "-la", "/"]}'

# whoami
curl -N -X POST "http://localhost:8080/v1/sandboxes/${SID}/exec" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["whoami"]}'
```

**Touch (refresh TTL)**

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X POST "http://localhost:8080/v1/sandboxes/${SID}/touch"
```

**Delete sandbox**

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X DELETE "http://localhost:8080/v1/sandboxes/${SID}"
```

`204` means success.

### 4. Smoke tests (optional)

With the port-forward running, run the full smoke suite (create → exec → snapshot/restore → cleanup):

```bash
./sbx test smoke
```

Environment variables: `MANAGER_URL` (default `http://localhost:8080`), `SANDBOX_NAMESPACE` (default `sandbox`).

## Configuration

Configuration is loaded from a YAML file (default: `/etc/sandbox-manager/manager-config.yaml`), with hot-reload support via file watching.

See [`manager-service/manager-config.example.yaml`](manager-service/manager-config.example.yaml) for a complete example.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | Config file path |
| `CONFIG_RELOAD_DEBOUNCE` | `300ms` | Hot-reload debounce |
| `STORAGE_ENDPOINT` | `localhost:9000` | MinIO/S3 endpoint |
| `STORAGE_ACCESS_KEY` | `minioadmin` | MinIO access key |
| `STORAGE_SECRET_KEY` | `minioadmin` | MinIO secret key |
| `STORAGE_BUCKET` | `sandboxes` | MinIO bucket |
| `STORAGE_USE_SSL` | `false` | Use SSL for MinIO |

## Deployment

Kubernetes manifests are in `k8s/` using Kustomize:

```bash
# Deploy to kind cluster
./sbx k8s deploy

# Check status
./sbx dev status
```

## Project Structure

```
mbos-sandbox-v1/
├── manager-service/          # Go manager service
│   ├── cmd/manager/          # Main entrypoint
│   ├── cmd/cleaner/          # CronJob for pod cleanup
│   └── internal/
│       ├── app/              # Application initialization
│       ├── config/           # Configuration (YAML, hot-reload)
│       ├── exec/             # Command wrapper utilities
│       ├── files/            # File upload/download (tar.gz)
│       ├── finalizer/        # K8s finalizer (snapshot on delete)
│       ├── httpapi/          # REST API handlers + SSE streaming
│       ├── k8s/              # Kubernetes client (pods, exec)
│       ├── observability/    # Logging, metrics, health
│       └── storage/          # MinIO/S3 snapshot storage
├── images/runner/            # Runner pod Docker image
├── k8s/                      # Kubernetes manifests (Kustomize)
└── sbx                       # CLI helper script
```
