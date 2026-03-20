# Configuration Reference

The sandbox manager is configured through a YAML file plus environment variables.

## YAML Config

Schema version: `version: 1`

### Top-level fields

| Field | Type | Description |
|-------|------|-------------|
| `version` | int | Must be `1` |
| `server` | object | HTTP server and timeout settings |
| `auth` | object | Service-key auth settings |
| `kubernetes` | object | Kubernetes client tuning |
| `rateLimit` | object | Global rate limiting |

### Key sections

| Field | Default | Description |
|-------|---------|-------------|
| `server.httpPort` | `8080` | HTTP listen port |
| `server.requestIdHeader` | `X-Request-Id` | Request ID header |
| `server.metrics.enabled` | `true` | Expose `/metrics` |
| `auth.headerName` | `X-Service-Key` | Service-key header |
| `kubernetes.qps` | `50` | K8s API QPS |
| `kubernetes.burst` | `100` | K8s API burst |
| `kubernetes.requestTimeout` | `15s` | K8s request timeout |
| `rateLimit.requestsPerMinute` | `60` | Global RPM |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | YAML config path |
| `SERVICE_KEYS` | *(required)* | Comma-separated valid `X-Service-Key` values |
| `K8S_NAMESPACE` | `sandbox-workloads` | Namespace for bindings and workloads |
| `KUBECONFIG` | *(auto)* | Kubeconfig path when running out of cluster |
| `JUICEFS_CSI_DRIVER` | `csi.juicefs.com` | JuiceFS CSI driver name |
| `JUICEFS_STORAGE_CAPACITY` | `1Pi` | Default binding PVC capacity |
| `JUICEFS_STORAGE_CLASS_NAME` | *(unset)* | Optional binding storage class |
| `JUICEFS_MOUNT_OPTIONS` | *(unset)* | Comma-separated mount options |
| `JUICEFS_SUBDIR` | *(unset)* | Optional CSI subdir prefix |
| `JUICEFS_MOUNT_SERVICE_ACCOUNT` | *(unset)* | Optional mount pod service account |
| `JUICEFS_MOUNT_IMAGE` | *(unset)* | Optional mount pod image override |
| `JUICEFS_STORAGE_ENDPOINT` | `http://localhost:19000` | Object storage endpoint written into JuiceFS secrets |
| `JUICEFS_STORAGE_CREDENTIAL_SEED` | `sandbox-juicefs-credential-seed` | Deterministic secret seed |
| `DEBUG` | `false` | Verbose logs |
| `LOG_LEVEL` | *(unset)* | When set to `debug`, enables verbose structured logs |

## Cleaner

The cleaner binary is configured by command-line flags and environment variables, not by the manager YAML.

| Flag | Default | Description |
|------|---------|-------------|
| `--namespace` | `default` | Namespace to scan for expired workload pods |
| `--dry-run` | `true` | Log-only when true |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |

The cleaner only deletes expired workload pods. It does not delete workspace bindings or JuiceFS data.

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
