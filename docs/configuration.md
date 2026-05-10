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
| `AFSCP_INTERNAL_BASE_URL` | *(required)* | AFSCP internal API base URL |
| `AFSCP_ORCHESTRATOR_TOKEN` | *(required)* | Sandbox orchestrator service token for AFSCP |
| `AFSCP_CALLER_SERVICE` | `sandbox-orchestrator` | Caller service header sent to AFSCP |
| `AFSCP_ACTOR_TYPE` | `system` | Actor type for mutating AFSCP lifecycle calls |
| `AFSCP_ACTOR_ID` | `sandbox-manager` | Actor id for mutating AFSCP lifecycle calls |
| `JUICEFS_CSI_DRIVER` | `csi.juicefs.com` | JuiceFS CSI driver name |
| `JUICEFS_STORAGE_CAPACITY` | `1Pi` | Default binding PVC capacity |
| `JUICEFS_STORAGE_CLASS_NAME` | *(unset)* | Optional binding storage class |
| `DEBUG` | `false` | Verbose logs |
| `LOG_LEVEL` | *(unset)* | When set to `debug`, enables verbose structured logs |

Kubernetes overlays set `AFSCP_INTERNAL_BASE_URL` to the AFSCP internal API
service, for example `http://afscp-api.agentsmith-system.svc.cluster.local:8080`.
Do not point sandbox at the AgentSmith API service for AFSCP lifecycle calls.

## Reclaim

Expired workload reclaim must enter through the manager workload delete API. That path owns AFSCP release before pod deletion and writes `released` only after the pod is confirmed gone.

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
