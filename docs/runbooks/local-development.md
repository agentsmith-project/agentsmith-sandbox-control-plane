# Local Development Runbook

Use this runbook for ASBCP development and governance validation.

## Quick Governance Check

```bash
bash scripts/verify-release.sh --quick
```

This validates public governance files, release workflow hardening, release evidence JSON, and shell syntax. It is safe for docs and workflow PRs, but it is not release readiness.

## Full Release Gate

```bash
bash scripts/verify-release.sh
```

Run the full gate before tag release or when validating a release candidate. If it fails on an open migration risk, update `docs/RISK_REGISTER.md` and hand off to the owning worker.

## Local Runtime Inputs

Canonical ASBCP runtime names are:

- `ASBCP_CONFIG_PATH`
- `ASBCP_SERVICE_KEYS`
- `ASBCP_WORKLOAD_NAMESPACE`
- `ASBCP_AFSCP_INTERNAL_BASE_URL`
- `ASBCP_AFSCP_ORCHESTRATOR_TOKEN`
- `ASBCP_AFSCP_CALLER_SERVICE`
- `ASBCP_AFSCP_ACTOR_ID`

Do not add raw storage credentials to ASBCP local runtime config.
