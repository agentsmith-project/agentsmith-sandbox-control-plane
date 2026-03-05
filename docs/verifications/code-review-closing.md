# Code Review — Production Readiness

**Scope:** manager-service core (workload API, K8s, workspace, cleaner).

## Checklist

| Area | Status |
|------|--------|
| **parseRoute** | `/v1/workspaces/{ws}/projects/{p}/workloads/{wl}[/action]`; `workloadID` validated for K8s name. |
| **Pod create** | 200 when pod already exists; no duplicate create. |
| **Keepalive** | `newExpires` capped by `workload/maxExpiresAt`; `PatchActivity` updates annotations. |
| **Exec** | Pod existence checked; 404 when missing; container `main`; timeout capped (max 300s). |
| **Delete** | Get pod → delete → wait. No snapshot or GC. |
| **Cleaner** | Selects by `expires_at` and `app=managed-workload` / `app=sandbox`; deletes only (no snapshot/GC). |
| **Auth** | Service-key middleware and validator. |
| **K8s client** | In-cluster / kubeconfig; QPS/burst; retry; namespace trimmed. |
| **Workspace** | JVS prepare/restore; `PayloadSubPath` for pod volume. Cleaner does not touch storage. |
| **Shutdown** | Signal handling and HTTP server shutdown with timeout. |
| **CreateRequest** | Resource quantities parsed with `resource.ParseQuantity`; 400 on invalid. |
| **Rate limit** | Config from YAML used; limiter cleanup started/stopped. |
