# Readiness Evidence

This ledger records ASBCP release-readiness evidence. It is intentionally lightweight and focused on sandbox workload lifecycle.

| Evidence | Required for release | Current status |
| --- | --- | --- |
| Governance guard | Yes | Added in `.github/tests/asbcp-governance-guard.sh` |
| Release workflow calls authoritative gate | Yes | Enforced by governance guard |
| API contract skeleton | Yes | Added under `docs/contracts/` |
| Service auth contract skeleton | Yes | Added under `docs/contracts/` |
| AFSCP mount-plan dependency contract | Yes | Added under `docs/contracts/` |
| Health and readiness smoke | Yes | Fake-fixture smoke covers `healthz`, `readyz`, and authenticated v1 health path |
| Workspace binding fixture | Yes | Fake AFSCP/Kubernetes fixture proves mount-plan consumption |
| Workload create, keepalive, exec, release, delete smoke | Yes | Fake-fixture smoke covers create, keepalive, exec route/error contract, AFSCP release, and delete paths |
| Kubernetes render check | Yes | Enforced by `scripts/verify-release.sh` |
| Dockerfile contract and image build | Yes | Enforced by `scripts/verify-release.sh` |
| Digest pull | Workflow-gated | Tag release workflow pulls the published tag@digest before release notes |
| Retired naming guard | Yes | Added for governed release surface |
| Raw storage credential exclusion | Yes | Contract and active guard added |
| Runner artifact classification | Yes | ADR and active release-surface guard added |

The release gate fails on `pending` or `deferred` required evidence. Current runtime smoke evidence is fake-fixture based; live cluster smoke remains release-candidate evidence, not a reason for this gate to report false readiness.
