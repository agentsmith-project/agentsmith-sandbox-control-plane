# Runbook

This runbook covers the current production path only:

- workspace bindings backed by JuiceFS CSI
- workload pods mounted at `/workspace`
- keepalive-driven workload reclaim

## Health and readiness

- `GET /healthz` — process liveness
- `GET /readyz` — manager can talk to Kubernetes and has valid config
- `GET /metrics` — Prometheus metrics

## Core operational checks

### 1. Verify workspace binding

```bash
curl -s -X PUT \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workspace-bindings/flib_demo \
  -H "X-Service-Key: $SERVICE_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "file_library_id": "flib_demo",
    "filesystem_name": "agentsmith-workspace",
    "metadata_url": "postgres://postgres:postgres@db:5432/juicefs?sslmode=disable"
  }' | jq .
```

Check:

- binding returns `status=ready`
- `pvc_name` is present
- binding can be fetched again with `GET`

### 2. Verify workload mount

Create a workload with `workspace_binding_id`, then check:

- pod reaches `Running`
- pod mounts `/workspace`
- pod can write and read under `/workspace`

### 3. Verify reclaim behavior

- let `expires_at` pass or delete the workload pod
- confirm the pod is removed
- recreate the workload with the same `workspace_binding_id`
- confirm files under `/workspace` are still present

## Cleaner

The cleaner only deletes expired workload pods. It must not delete workspace bindings.

Useful checks:

```bash
kubectl -n sandbox-system get cronjob sandbox-cleaner
kubectl -n sandbox-system logs -l app=sandbox-cleaner --tail=100
```

If pods are not reclaimed:

1. Check `expires_at`
2. Check keepalive traffic
3. Check cleaner namespace and `--dry-run`
4. Check RBAC and namespace wiring

## Release checks

Before release:

1. `git status` clean
2. manager and cleaner tests pass
3. workspace binding ensure/get/delete works
4. workload create/get/delete/keepalive/exec works
5. deleting a workload does not delete workspace contents
6. docs and API reference match the current binding + JuiceFS CSI model
