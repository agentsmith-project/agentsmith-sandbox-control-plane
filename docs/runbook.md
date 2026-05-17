# Runbook

This runbook covers the current production path only:

- workspace bindings backed by JuiceFS CSI
- workload pods mounted from AFSCP workload mount plans
- keepalive-driven AFSCP heartbeat and workload reclaim

## Health and readiness

- `GET /healthz` — process liveness
- `GET /readyz` — manager can talk to Kubernetes and has valid config
- `GET /metrics` — Prometheus metrics

## Core operational checks

### 1. Verify workspace binding

```bash
curl -s -X PUT \
  http://localhost:8080/v1/workspaces/ws_001/projects/proj_001/workspace-bindings/wmb_demo \
  -H "X-Service-Key: $SERVICE_KEY" \
  -H "X-Correlation-Id: corr-runbook" \
  -H "Content-Type: application/json" \
  -d '{
    "namespace_id": "ns_demo",
    "mount_binding_id": "wmb_demo"
  }' | jq .
```

Check:

- binding returns `status=ready`
- `pvc_name` is present
- response omits `secret_ref` and `payload_volume_subdir`
- binding can be fetched again with `GET`

### 2. Verify workload mount

Create a workload with `workspace_binding_id`, then check:

- pod reaches `Running`
- pod mounts the binding PVC at AFSCP plan `mount_path`
- container `workingDir` is `<mount_path>/workspace`
- env includes `TASK_HOME=<mount_path>`, `HOME=<mount_path>`, `WORKSPACE_PATH=<mount_path>/workspace`
- keepalive produces an AFSCP heartbeat
- pod can write and read under `$WORKSPACE_PATH`

### 3. Verify reclaim behavior

- let `expires_at` pass or delete the workload pod
- confirm the pod is removed
- recreate the workload with the same `workspace_binding_id`
- confirm files under the same task `WORKSPACE_PATH` are still present

## Reclaim

Active Kubernetes deployment has no separate pod-deleting reclaim path. Reclaim
must go through the manager workload delete path so AFSCP release runs before
pod deletion, the mounted payload path is flushed before deletion, and terminal
`released` status is written only after the pod is confirmed gone.

If pods are not reclaimed:

1. Check `expires_at`
2. Check keepalive traffic
3. Call `DELETE /workloads/{wlId}` through the manager
4. Check RBAC and namespace wiring

## Release checks

Before release:

1. `git status` clean
2. manager tests pass
3. workspace binding ensure/get/delete works
4. workload create/get/delete/keepalive/exec works
5. deleting a workload calls and confirms AFSCP release, flushes the mounted payload path, removes the pod, then reports `released`
6. docs and API reference match the current AFSCP plan consumer model
