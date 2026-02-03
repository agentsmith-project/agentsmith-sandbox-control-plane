# mbos-sandbox-v1

A Kubernetes-based sandbox system for running isolated, persistent shell sessions with a WebSocket API, automatic workspace snapshot/restore, and TTL-based lifecycle management.

## Features

- **Persistent Shell Sessions**: Long-lived tmux sessions that survive WebSocket reconnections
- **WebSocket API**: Bidirectional communication with automatic message buffering
- **Snapshot & Restore**: Automatic workspace persistence via MinIO/S3
- **TTL Management**: Configurable idle timeout and max lifetime with automatic cleanup
- **Security Controls**: Network policies, resource limits, readonly filesystem, and capabilities
- **Flexible Runtime**: Support for custom container images with configurable commands
- **Developer-Friendly**: Single entrypoint (`./sbx`) for all operations

## Architecture

```
┌─────────────────┐         ┌─────────────────┐         ┌─────────────────┐
│     Client      │◄────────►│     Manager     │◄────────►│      Pod        │
│  (WebSocket)    │         │  (Go Service)   │         │ (User Container)│
└─────────────────┘         │                 │         │                 │
                            │  - Session Mgmt  │         │  /workspace     │
                            │  - Ring Buffer   │         │                 │
                            │  - kubectl exec  │         │  tmux session   │
                            └────────┬─────────┘         └─────────────────┘
                                     │
                                     ▼
                            ┌─────────────────┐
                            │      MinIO      │
                            │  (Snapshots)    │
                            └─────────────────┘

Cleaner (CronJob):
  - Scans every 5 minutes
  - Reads Pod annotations for TTL
  - Directly deletes expired Pods
  - Manager Finalizer handles snapshot
```

## Quick Start

### Prerequisites

- Go 1.21+ (for local development)
- kubectl or use vendored tools
- Kubernetes cluster (kind for local, or any K8s cluster)

### Using Kind (Local Development)

```bash
# 1. Fetch vendored tools (recommended; avoids needing kubectl/kustomize installed)
./sbx tools fetch --proxy auto

# 2. Create kind cluster + build + deploy (end-to-end)
./sbx dev up --force --proxy auto --harbor-ca auto

# 3. Access manager
tools/bin/linux-amd64/kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80

# 4. Test the WebSocket API
# Connect to ws://localhost:8080/ws and send:
{"type":"create","agent_thread_id":"test-123","image":"python:3.11","command":["/bin/bash"]}
```

### Make Targets (Convenience)

```bash
make test
make test-integration
make build-manager
make build-runner
make build-cleaner
make kind-up
make kind-status
make smoke
```

### WS Client (Interactive)

Build and run the CLI client:

```bash
cd manager-service
go build -o ../bin/ws-client ./cmd/ws-client
../bin/ws-client --url ws://localhost:8080/ws --agent-thread-id at_demo --image python:3.11 --command /bin/bash
```

Notes:
- Raw TTY mode enabled by default; exit with `Ctrl-]` (or `--exit-key ctrl-c`).
- Auto reconnect with backoff on disconnect.
- Terminal resize is detected and sent automatically when running in a TTY.

## Project Structure

```
mbos-sandbox-v1/
├── manager-service/          # Go HTTP/WebSocket API service
│   ├── cmd/                  # Main entrypoints (manager, cleaner)
│   ├── internal/             # Internal packages
│   │   ├── api/              # HTTP/WebSocket handlers
│   │   ├── buffer/           # Ring buffer for messages
│   │   ├── cleaner/          # TTL-based pod cleanup
│   │   ├── config/           # Configuration loading
│   │   ├── k8s/              # Kubernetes client utilities
│   │   ├── sandbox/          # Pod creation and management
│   │   ├── session/          # Session state management
│   │   ├── snapshot/         # Workspace snapshot/restore
│   │   └── websocket/        # WebSocket connection handling
│   ├── integration/          # Integration tests
│   ├── scripts/              # Build and test scripts
│   └── manager-config.example.yaml
├── images/
│   ├── runner/               # Sandbox runner image
│   └── gc/                   # Garbage collection image
├── k8s/                      # Kubernetes manifests
│   ├── base/                 # Kustomize base
│   ├── overlays/
│   │   ├── dev/              # Development (kind) overlay
│   │   └── production/       # Production overlay
│   └── scripts/              # Deployment utilities
├── scripts/                  # Internal bash libraries
├── tools/                    # Vendored binaries for offline
├── sbx                       # Single workflow entrypoint
└── docs/                     # Documentation
    ├── plans/                # Design and implementation docs
    ├── api-reference-v1.md   # Complete API reference
    ├── API.md                # Quick API guide
    ├── WORKFLOWS.md          # Common workflows
    ├── CONFIGURATION_GUIDE.md
    └── TROUBLESHOOTING.md
```

## WebSocket API

### Connection

```
GET /ws
```

### Message Types

#### Client → Manager

**Create Session**
```json
{
  "type": "create",
  "agent_thread_id": "at_abc123",
  "image": "python:3.11",
  "command": ["/bin/bash"],
  "env": {"MY_VAR": "value"},
  "config": {
    "allow_network_access": false,
    "readonly_filesystem": false,
    "cpu_limit": "2",
    "memory_limit": "1Gi",
    "idle_timeout": "30m",
    "max_lifetime": "24h"
  }
}
```

**Send Input**
```json
{
  "type": "stdin",
  "data": "base64_encoded_string"
}
```

#### Manager → Client

**Status Update**
```json
{
  "type": "status",
  "state": "creating|restoring|ready|error",
  "message": "string",
  "progress": 0.0-1.0
}
```

**Output**
```json
{
  "type": "stdout",
  "data": "base64_encoded_string"
}
```

See [api-reference-v1.md](docs/api-reference-v1.md) for complete API documentation.

## Configuration

### Manager Configuration

The manager is configured via YAML (see `manager-service/manager-config.example.yaml`):

```yaml
version: 1

server:
  httpPort: 8080
  metrics:
    enabled: true
    path: /metrics

kubernetes:
  namespace: sandbox
  qps: 50
  burst: 100

sandbox:
  defaults:
    namespace: sandbox
    runnerImage: sandbox-runner:1.0.0
    ttlSeconds: 900
    resources:
      limits:
        cpu: "1"
        memory: 1Gi

storage:
  endpoint: minio:9000
  bucket: mbos-sandbox-snapshots
  useSSL: false

buffer:
  capacity: 10000
```

### Environment Variables

- `CONFIG_PATH`: Path to manager config YAML
- `SERVICE_KEYS`: Comma-separated list of valid service keys
- `MINIO_ACCESS_KEY`: MinIO/S3 access key
- `MINIO_SECRET_KEY`: MinIO/S3 secret key

## Security Controls

| Control | Type | Description |
|---------|------|-------------|
| `allow_network_access` | bool | Allow external network access |
| `readonly_filesystem` | bool | Mount root as read-only (except /workspace) |
| `cpu_limit` | string | CPU limit (e.g., "2") |
| `memory_limit` | string | Memory limit (e.g., "1Gi") |
| `idle_timeout` | duration | Idle timeout before cleanup (e.g., "30m") |
| `max_lifetime` | duration | Maximum lifetime (e.g., "24h") |
| `drop_all_capabilities` | bool | Drop all Linux capabilities |
| `allow_privileged` | bool | Allow privileged mode |

## Development

### Building Manager

```bash
cd manager-service
./scripts/build.sh
```

### Running Tests

```bash
cd manager-service
./scripts/test.sh
```

### Code Quality

```bash
cd manager-service
./scripts/lint.sh
```

### Making Changes

1. Edit code in `manager-service/internal/`
2. Run `./scripts/build.sh` to build
3. Run `./scripts/test.sh` to verify
4. Update documentation if needed

## Deployment

### Development (Kind)

```bash
./sbx dev up --force --proxy auto --harbor-ca auto
```

### Production (Harbor)

```bash
# 1. Push images to Harbor
./sbx images push harbor \
  --registry "$HARBOR_REGISTRY" \
  --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" \
  --password "$HARBOR_PASSWORD"

# 2. Deploy
./sbx k8s deploy production

# 3. Verify
./sbx k8s verify production
```

See [WORKFLOWS.md](docs/WORKFLOWS.md) for more deployment workflows.

## Documentation

- [API Reference](docs/api-reference-v1.md) - Complete WebSocket API documentation
- [Workflows](docs/WORKFLOWS.md) - Common development and deployment workflows
- [Configuration Guide](docs/CONFIGURATION_GUIDE.md) - Detailed configuration options
- [Troubleshooting](docs/TROUBLESHOOTING.md) - Common issues and solutions
- [Offline Mode](docs/OFFLINE.md) - Air-gapped deployment guide
- [Design Docs](docs/plans/) - Architecture and implementation plans

## License

[Add your license here]

## Contributing

[Add contributing guidelines here]
