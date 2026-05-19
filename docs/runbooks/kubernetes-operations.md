# Kubernetes Operations Runbook

ASBCP manages Kubernetes resources needed for workspace bindings and workload Pods.

## Canonical Runtime Identity

- Application name: `agentsmith-sandbox-control-plane`
- Component label: `asbcp`
- Service account: `agentsmith-sandbox-control-plane`
- AFSCP caller service: `agentsmith-sandbox-control-plane`

## Readiness Checks

1. Deployment is available.
2. `/readyz` returns success.
3. Workload namespace is reachable.
4. ASBCP can call AFSCP with the configured caller identity.
5. Workspace binding and workload lifecycle smoke passes.

## Incident Checklist

- Capture ASBCP Pod logs with secrets redacted.
- Capture Kubernetes events for ASBCP-managed workloads.
- Check AFSCP dependency status and caller allowlist.
- Confirm AgentSmith is using a digest image, not a mutable tag.
- Update `docs/RISK_REGISTER.md` if the incident reveals a release-governance gap.
