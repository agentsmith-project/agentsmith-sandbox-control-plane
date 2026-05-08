# Code Review — Production Readiness

**Scope:** manager-service current production path (workspace bindings, workload API, K8s client, cleaner).

## Checklist

| Area | Status |
|------|--------|
| **Route parsing** | `/v1/workspaces/{ws}/projects/{p}/workspace-bindings/{binding}` and `/workloads/{wl}` are both validated and dispatched cleanly. |
| **Binding ensure** | Creates or reuses Secret/PV/PVC through JuiceFS CSI. |
| **Workload create** | Requires `workspace_binding_id`; returns 200 on idempotent reuse. |
| **Workspace mount** | Pod mounts the task HOME subtree from the binding PVC at `/home/<task_home_segment>`; container cwd is `/home/<task_home_segment>/workspace`. |
| **Keepalive** | `expires_at` is extended and capped correctly. |
| **Exec** | Pod existence checked; timeout capped; container `main` only. |
| **Delete** | Deletes compute only; workspace binding remains independent. |
| **Cleaner** | Deletes expired workload pods only; does not touch bindings. |
| **Auth** | Service-key middleware and validator protect all `/v1/` routes. |
| **K8s client** | In-cluster / kubeconfig, QPS/burst, retry, namespace trimming. |
| **Storage model** | Current release truth is JuiceFS CSI workspace bindings plus task HOME subPath mounts. Historical `/workspace` wording is legacy-only and no longer the Agent task current path truth. |
| **Shutdown** | Signal handling and HTTP server shutdown with timeout. |
| **CreateRequest** | Resource quantities parsed and invalid inputs rejected. |
| **Rate limit** | YAML-configured limiter is active. |
