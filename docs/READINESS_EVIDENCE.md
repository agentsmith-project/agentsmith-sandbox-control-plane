# Readiness Evidence

This ledger records ASBCP release-readiness evidence. It is intentionally lightweight and focused on sandbox workload lifecycle.

Current release-prep target: `v2.0.7`. The already-published `v2.0.6` image remains the BC0001 durable terminal truth release; BC0002 scoped workload identity behavior is prepared for `v2.0.7` and must not be treated as published `v2.0.6` behavior.

| Evidence | Required for release | Current status |
| --- | --- | --- |
| Governance guard | Yes | Added in `.github/tests/asbcp-governance-guard.sh` |
| Release workflow calls authoritative gate | Yes | Enforced by governance guard |
| API contract skeleton | Yes | Added under `docs/contracts/` |
| Service auth contract skeleton | Yes | Added under `docs/contracts/` |
| AFSCP mount-plan dependency contract | Yes | Added under `docs/contracts/` |
| Health and readiness smoke | Yes | Fake-fixture smoke covers `healthz`, `readyz`, and authenticated v1 health path |
| Workspace binding fixture | Yes | Fake AFSCP/Kubernetes fixture proves mount-plan consumption and label-independent PVC reference safety |
| Workload lifecycle and exec route/error smoke | Yes | Fake-fixture smoke covers create, scope-qualified Pod identity, keepalive, exec route/error contract, AFSCP release, terminal DELETE scope-drift fail-closed behavior, and delete paths |
| Kubernetes render check | Yes | Enforced by `scripts/verify-release.sh` |
| Dockerfile contract and image build | Yes | Enforced by `scripts/verify-release.sh` |
| CHANGELOG release evidence | Yes | `scripts/verify-release.sh` parses the current tag section into structured `known_breaking_changes` objects and `changelog_summary` before image publication; the workflow reuses that JSON for the final manifest |
| Risk register release status | Yes | `scripts/verify-release.sh --risk-status-json` reads `docs/RISK_REGISTER.md`, fails on release-blocking rows, and preserves open non-release-blocking risks in `known_risk_status` |
| Anonymous pull | Workflow-gated | Tag release workflow uses a fresh Docker config to pull the published `image:tag`, record `tag_resolved_digest`, pull `image:tag@build_push_digest`, and record `anonymous_digest` before release notes |
| Same digest proof | Workflow-gated | Final manifest records `same_digest_proof` comparing `tag_resolved_digest`, `build_push_digest`, and `anonymous_digest`; it is published image identity evidence, not post-push container behavior evidence |
| Final manifest | Workflow-gated | Tag release workflow calls `scripts/generate-final-manifest` to generate `asbcp-final-manifest.json` conforming to `docs/schemas/asbcp-final-manifest.v1.schema.json`, including CHANGELOG-derived `changelog_summary` and the complete GitHub Release body in `release_notes.body_source`, and uploads only the manifest as a GitHub Release asset |
| Provider prerequisites contract | Yes | `docs/contracts/asbcp-provider-prerequisites.v1.json` covers ASBCP service RBAC verbs, secret/env projections, `ASBCP_AFSCP_*`, AFSCP caller identity, allowed caller, `orchestrator_mount`, and no-public-ingress |
| Public error redaction | Yes | `httperror.Write` redacts credential-like dependency messages before returning public JSON error envelopes |
| Retired naming guard | Yes | Added for governed release surface |
| Raw storage credential exclusion | Yes | Contract and active guard added |
| Runner artifact classification | Yes | ADR and active release-surface guard added |

The release gate fails on `pending` or `deferred` required evidence and on any risk register row marked release-blocking. Current health/readiness and workload lifecycle evidence is fake-fixture based. This slice does not claim real post-push container behavior evidence; the GitHub Release asset uses `same_digest_proof` only for published image identity evidence across the fresh anonymous `image:tag`, build-push, and fresh anonymous `image:tag@digest` digests.
