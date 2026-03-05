# Configuration Reference

The sandbox manager is configured via a YAML file and environment variables. The config file path is set by `CONFIG_PATH` (default: `/etc/sandbox-manager/manager-config.yaml`).

## YAML config

Schema version: `version: 1`.

### Top-level

| Field | Type | Description |
|-------|------|-------------|
| `version` | int | Must be `1` |
| `server` | object | HTTP server and timeouts |
| `auth` | object | Service-key auth |
| `kubernetes` | object | K8s client (QPS, burst) |
| `rateLimit` | object | Global rate limit |

### server

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `server.httpPort` | int | `8080` | Listen port |
| `server.requestIdHeader` | string | `X-Request-Id` | Header for request ID |
| `server.timeouts.readHeader` | duration | `5s` | Read header timeout |
| `server.timeouts.read` | duration | `30s` | Read body timeout |
| `server.timeouts.write` | duration | `60s` | Write timeout |
| `server.timeouts.idle` | duration | `120s` | Idle timeout |
| `server.maxHeaderBytes` | int | `1048576` | Max request header size |
| `server.metrics.enabled` | bool | `true` | Expose `/metrics` |
| `server.metrics.path` | string | `/metrics` | Metrics path |
| `server.debug.configPath` | string | `/debug/config` | Debug config dump path |
| `server.debug.enablePprof` | bool | `false` | Enable pprof |

### auth

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `auth.headerName` | string | `X-Service-Key` | Header name for service key |

### kubernetes

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `kubernetes.qps` | int | `50` | K8s API QPS |
| `kubernetes.burst` | int | `100` | K8s API burst |
| `kubernetes.requestTimeout` | duration | `15s` | K8s request timeout |

### rateLimit

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rateLimit.requestsPerMinute` | int | `60` | Global requests per minute |

---

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | Path to YAML config |
| `SERVICE_KEYS` | *(required for auth)* | Comma-separated list of valid service keys for `X-Service-Key` |
| `K8S_NAMESPACE` | `sandbox-workloads` | Kubernetes namespace for workload pods (overrides config if set) |
| `KUBECONFIG` | *(in-cluster or `~/.kube/config`)* | Kubeconfig path when not in-cluster |
| `JUICEFS_BASE_PATH` | `/mnt/juicefs/workloads` | Base path for workspace directories |
| `JUICEFS_PVC_NAME` | `juicefs-workloads-pvc` | PVC name for workload volumes |
| `DEBUG` | `false` | Set to `true` for verbose logs |
| `LOG_LEVEL` | *(unset)* | When set to `debug`, enables verbose structured logging |

---

## Cleaner (CronJob)

The cleaner binary is configured by **command-line flags** and env, not the manager YAML.

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | `default` | Namespace to scan for pods with `app=sandbox` or `app=managed-workload` and `expires_at` |
| `--dry-run` | `true` | If `true`, only log what would be deleted; if `false`, delete expired pods |
| `--log-level` | `info` | Log verbosity: `debug` (klog v=4), `info` (v=2), `warn`/`error` (v=0) |

The CronJob in base uses `--namespace=sandbox-workloads --log-level=info --dry-run=false`.

---

## Example YAML

```yaml
version: 1
server:
  httpPort: 8080
  metrics:
    enabled: true
    path: /metrics
auth:
  headerName: X-Service-Key
kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s
rateLimit:
  requestsPerMinute: 120
```
