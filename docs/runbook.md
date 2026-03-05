# Runbook

## Health and readiness

- **Liveness:** `GET /healthz` — returns 200 when the process is up. No auth.
- **Readiness:** `GET /readyz` — returns 200 when the manager can talk to the Kubernetes API and the configured namespace exists. No auth.

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/healthz   # 200
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/readyz    # 200
```

## Metrics

- **Metrics:** `GET /metrics` — Prometheus-format metrics. Path is configurable (`server.metrics.path`).

## Cleaner (CronJob)

- **Schedule:** Runs every 5 minutes (`*/5 * * * *`).
- **Behavior:** Lists pods in the configured namespace(s) with labels `app=sandbox` or `app=managed-workload`, checks the `expires_at` annotation (RFC3339). If `now > expires_at`, the pod is deleted (or only logged when `--dry-run=true`).
- **Pod-only cleanup:** The cleaner only deletes expired pods.

### Checking the cleaner

```bash
# List CronJob and recent jobs
kubectl -n sandbox-system get cronjob sandbox-cleaner
kubectl -n sandbox-system get jobs -l app=sandbox-cleaner

# Logs of the last run
kubectl -n sandbox-system logs -l app=sandbox-cleaner --tail=100
```

### If pods are not reclaimed

1. **Confirm `expires_at`:**  
   `kubectl -n <namespace> get pod <pod-name> -o jsonpath='{.metadata.annotations.expires_at}'`  
   If missing or in the future, the cleaner will not delete the pod.

2. **Keepalive:**  
   Expiry is extended when the client sends `POST …/workloads/{id}/keepalive`. If the client stops sending keepalives, `expires_at` is not updated and the pod will be reclaimed after the idle timeout.

3. **Cleaner logs:**  
   Check for `[DRY-RUN]` (dry-run still on) or errors (e.g. RBAC, namespace not found). In production, ensure the overlay sets `--dry-run=false` and the correct `--namespace`.

4. **Namespace:**  
   The cleaner scans the namespace passed via `--namespace`. Production patch uses `--namespace=sandbox-workloads`.

## Secrets (SERVICE_KEYS)

Service keys are provided via the `SERVICE_KEYS` environment variable (comma-separated). In Kubernetes, this is typically set from a Secret (e.g. `sandbox-manager-keys` → key `SERVICE_KEYS`). Rotate by updating the Secret and restarting the manager (and optionally the cleaner if it ever uses the same Secret). There is no in-memory hot-rotate of keys; restart is required.

## Troubleshooting

| Symptom | Checks |
|--------|--------|
| 401 on `/v1/` | `X-Service-Key` header present and value in `SERVICE_KEYS` |
| 503 / readyz failing | K8s API reachable; `K8S_NAMESPACE` (or in-cluster namespace) exists and is listable |
| Pod not created | Check manager logs, K8s RBAC, resource quota, and namespace |
| Pod not deleted by cleaner | See “If pods are not reclaimed” above |
