# Release Gates

The only authoritative ASBCP release-readiness command is:

```bash
bash scripts/verify-release.sh
```

Release workflow, manual release rehearsal, and release owner signoff must use that entrypoint. Workflow steps may build and publish only after it passes.

## Quick Mode

```bash
bash scripts/verify-release.sh --quick
```

Quick mode is allowed for PR/main because it validates governance, workflow hardening, release evidence shape, and shell syntax without performing a release-readiness claim. Quick mode must never be described as a release gate.

## Release Mode

Release mode must cover:

- ASBCP governance guard.
- Shell syntax for release scripts.
- Go version alignment between workflows and module metadata.
- Root `VERSION` to release tag contract: tag releases must use `v$(cat VERSION)`.
- Current tag `CHANGELOG.md` release section must parse before image publication; `known_breaking_changes` are stable `{id, summary}` objects and `changelog_summary` comes from `scripts/verify-release.sh --changelog-evidence-json <tag>`.
- `known_risk_status` must come from `scripts/verify-release.sh --risk-status-json`, which reads the `Release-blocking` column in `docs/RISK_REGISTER.md`.
- The ASBCP service prerequisite contract in `docs/contracts/asbcp-provider-prerequisites.v1.json` must cover Kubernetes RBAC, secret/env projections, AFSCP caller identity, allowed caller, `orchestrator_mount`, and no-public-ingress.
- Readiness evidence manifest must not contain `pending` or `deferred` required release evidence.
- Fake-fixture smoke for health/ready handlers, workspace binding, workload create/keepalive/release/delete paths, and exec route/error contract.
- Source tests and binary build checks for the ASBCP service.
- Dockerfile contract and Docker image build with root `VERSION` metadata.
- `kubectl kustomize` render for dev, staging, and production overlays.
- Release workflow proof that GHCR image publication follows the gate.

The release workflow must call `scripts/verify-release.sh` before `docker/build-push-action`. Final manifest generation must call `scripts/generate-final-manifest`, which reuses the same `--changelog-evidence-json`, `--risk-status-json`, and `--api-contract-version` parsers instead of carrying separate CHANGELOG, risk-status, or API contract parsing logic.

## Non-Gates

- AgentSmith consumer adoption tests are not ASBCP release gates.
- Manual approval alone is not release readiness.
- A successful PR/main workflow is not release readiness.
- A locally built mutable image tag is not release readiness.

## Release Evidence

Release evidence is tracked in `docs/release-evidence/release-manifest.json` and summarized in `docs/READINESS_EVIDENCE.md`. Each tag release must publish image digest, commit SHA, API contract version, structured `known_breaking_changes`, CHANGELOG-derived `changelog_summary`, risk-register-derived `known_risk_status` plus `known_risk_status_source`, and an attached `asbcp-final-manifest.json` conforming to `docs/schemas/asbcp-final-manifest.v1.schema.json`, generated after fresh anonymous `docker pull` of `image:tag` and `image:tag@build_push_digest`. `same_digest_proof` only proves `tag_resolved_digest`, `build_push_digest`, and `anonymous_digest` are identical; it must not be described as post-push container behavior evidence. `release_notes.body_source` in that manifest is the complete GitHub Release body text, and the `body_path` file is written from that field rather than published as a second asset.
