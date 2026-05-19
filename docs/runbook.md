# Operations Runbook

This legacy top-level runbook now points to the ASBCP runbook set under `docs/runbooks/`.

Current production checks:

- Workspace bindings are backed by AFSCP workload mount plans.
- Workload Pods mount ASBCP-managed Kubernetes resources derived from those plans.
- Keepalive drives AFSCP heartbeat and ASBCP workload expiry.

## Health And Readiness

- `GET /healthz`: process liveness.
- `GET /readyz`: ASBCP can talk to Kubernetes and has valid config.
- `GET /metrics`: Prometheus metrics.

## Core Operational Checks

1. Ensure a workspace binding using `namespace_id` and `mount_binding_id`.
2. Confirm the binding response is sanitized.
3. Create a workload using `workspace_binding_id`.
4. Confirm the workload reaches running state and uses plan-derived runtime paths.
5. Run keepalive and exec.
6. Delete the workload and confirm AFSCP release plus Pod deletion.

## Release Checks

Use `bash scripts/verify-release.sh` as the only authoritative ASBCP release gate. See `docs/runbooks/release.md`.
