# Configuration Reference

ASBCP is configured through a YAML file plus environment variables. This document describes the canonical public release contract; implementation migration gaps are tracked in `docs/RISK_REGISTER.md`.

## YAML Config

Schema version: `version: 1`

| Field | Type | Description |
| --- | --- | --- |
| `version` | int | Must be `1` |
| `server` | object | HTTP server and timeout settings |
| `auth` | object | Service-key auth settings |
| `kubernetes` | object | Kubernetes client tuning |
| `rateLimit` | object | Global rate limiting |

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `ASBCP_CONFIG_PATH` | `/etc/asbcp/asbcp-config.yaml` | YAML config path |
| `ASBCP_SERVICE_KEYS` | required | Comma-separated valid `X-Service-Key` values |
| `ASBCP_WORKLOAD_NAMESPACE` | `sandbox-workloads` | Namespace for workspace bindings and workloads |
| `KUBECONFIG` | auto | Kubeconfig path when running out of cluster |
| `ASBCP_AFSCP_INTERNAL_BASE_URL` | required | AFSCP internal API base URL |
| `ASBCP_AFSCP_ORCHESTRATOR_TOKEN` | required | ASBCP orchestrator service token for AFSCP |
| `ASBCP_AFSCP_CALLER_SERVICE` | `agentsmith-sandbox-control-plane` | Caller service header sent to AFSCP |
| `ASBCP_AFSCP_ACTOR_ID` | `agentsmith-sandbox-control-plane` | Actor id for mutating AFSCP lifecycle calls |
| `ASBCP_JUICEFS_CSI_DRIVER` | `csi.juicefs.com` | CSI driver name when the AFSCP plan requires JuiceFS |
| `ASBCP_STORAGE_CAPACITY` | `1Pi` | Default binding PVC capacity |
| `ASBCP_STORAGE_CLASS_NAME` | unset | Optional binding storage class |
| `ASBCP_LOG_LEVEL` | unset | When set to `debug`, enables verbose structured logs |

ASBCP configuration must not include raw storage credentials. Filesystem and storage truth are provided by AFSCP mount plans.

## Reclaim

Expired workload reclaim must enter through the ASBCP workload delete API. That path owns AFSCP release confirmation, mounted payload flush, pod deletion, and terminal status only after the pod is confirmed gone.
