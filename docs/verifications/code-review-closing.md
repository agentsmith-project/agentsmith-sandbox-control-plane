# Code Review — Production Readiness

**Scope:** manager-service current production path (workspace bindings, workload API, K8s client).

## Checklist

| Area | Status |
|------|--------|
| **Route parsing** | `/v1/workspaces/{ws}/projects/{p}/workspace-bindings/{binding}` and `/workloads/{wl}` are both validated and dispatched cleanly. |
| **Binding ensure** | Fetches AFSCP orchestrator mount plan and creates/reuses PV/PVC without caller-supplied storage auth material. |
| **Workload create** | Requires `workspace_binding_id`; returns 200 on idempotent reuse. |
| **Workspace mount** | Pod mount path, read-only mode, runtime env, and writable init behavior are derived from the AFSCP plan. |
| **Keepalive** | AFSCP heartbeat runs before `expires_at` is extended and capped locally. |
| **Exec** | Pod existence checked; timeout capped; container `main` only. |
| **Delete** | Calls AFSCP release before compute deletion and writes `released` only after the pod is confirmed gone; pod deletion failures remain retryable and do not write terminal status. |
| **Reclaim** | Active deploy surface avoids direct pod deletion; compute closure goes through manager delete so AFSCP release/status runs. |
| **Auth** | Service-key middleware and validator protect all `/v1/` routes. |
| **K8s client** | In-cluster / kubeconfig, QPS/burst, retry, namespace trimming. |
| **Storage model** | Current release truth is AFSCP workload mount plan consumption plus sandbox-managed PV/PVC. |
| **Shutdown** | Signal handling and HTTP server shutdown with timeout. |
| **CreateRequest** | Resource quantities parsed and invalid inputs rejected. |
| **Rate limit** | YAML-configured limiter is active. |
