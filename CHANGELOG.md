# Changelog

All notable ASBCP release changes will be recorded here.

This project follows pre-GA evidence-first release notes. The authoritative published image identity evidence is the GitHub Release asset `asbcp-final-manifest.json`; changelog entries must not require the post-push digest before the tag workflow creates that asset.

## Unreleased

No unreleased changes.

## [v2.0.8] - 2026-05-27

### Changed

- Workload DELETE now establishes the storage flush barrier before releasing the AFSCP mount or deleting the pod, keeping data written before terminal close visible after delete.

## [v2.0.7] - 2026-05-20

### Breaking Changes

- ASBCP-BC-0002: pre-GA workload and binding Kubernetes object names are now scope-qualified hashed identities; workload Pod status, keepalive, exec, release, and delete paths validate Pod annotations and labels against URL workspace/project/workload scope before operating.

### Changed

- Workspace binding delete scans all pods for PVC references instead of relying on the driftable `app=managed-workload` label selector.
- Workload DELETE retries with durable terminal facts now still prove scoped Pod absence; the terminal DELETE scope-drift fail-closed path rejects a same-name Pod with mismatched workspace/project/workload metadata.
- Workload create contract docs now list the flat `cpu_*`, `memory_*`, `idle_timeout_sec`, and `max_lifetime_sec` fields.
- Prepared BC0002 release evidence under `v2.0.7`; the already-published `v2.0.6` image identity remains the BC0001 release and is not retconned to include scoped workload identity behavior.

## [v2.0.6] - 2026-05-19

### Breaking Changes

- ASBCP-BC-0001: pre-GA clean cut makes durable terminal truth the workload DELETE authority; missing durable release fact plus pod absence now returns fail-closed retryable `409` code `workload_release_incomplete` instead of absence/404 completion, release facts require the ConfigMap fact store/RBAC path, and workspace-binding reconciliation is fail-closed when provider prerequisites or binding state cannot be proven.

### Changed

- Moved the BC0001 workload terminal truth evidence to the `v2.0.6` release track because the existing `v2.0.5` tag is already published at an older commit.
- Recorded BC0001 workload truth changes: durable terminal truth is required before DELETE success, the DELETE 404-to-409 fail-closed contract is explicit, ConfigMap fact store/RBAC is part of the provider prerequisite surface, and workspace-binding uncertainty is fail-closed.

## [v2.0.5] - 2026-05-19

### Breaking Changes

- pre-GA clean cut for ASBCP release evidence schema and active workload smoke naming; no compatibility aliases are kept for retired manager/sandbox release surfaces.

### Changed

- Fixed the release evidence schema after the already-published `v2.0.4` tag by generating the `v2.0.5` final manifest with `same_digest_proof`, `known_risk_status_source`, and full `release_notes.body_source`.
- Kept anonymous `docker pull` evidence fresh by pulling both `image:tag` and `image:tag@digest` with an empty Docker config before publishing the final manifest.
- Documented that the production overlay is internal-only by default; access examples are private operator opt-in only and are not part of the default production kustomization.
- Completed old active smoke cleanup by moving scripts and copy from manager/sandbox wording to ASBCP/workload wording.

## [v2.0.4] - 2026-05-19

### Breaking Changes

- Pre-GA ASBCP public release contract.

### Changed

- Published tag release evidence with the then-current legacy final manifest schema. This historical release is not retconned; `v2.0.5` is the clean-cut schema fix.
- Hardened workflow guards so release notes are file-based and the release workflow must publish final manifest fields for version, tag, commit, image digest, API contract version, and public inspect result.
- Cleaned ASBCP release image build defaults so Dockerfile proxy usage is opt-in via empty-by-default `ASBCP_BUILD_*` build args and release gate builds clear host proxy defaults.

## 2.0.3 - 2026-05-19

- Added ASBCP public governance skeleton.
- Added authoritative release gate entrypoint at `scripts/verify-release.sh`.
- Added GitHub PR, CI, and tag release workflow skeletons.
- Added ASBCP/AFSCP boundary contracts, runbooks, ADRs, risk register, and readiness evidence ledger.
- Added governed release-surface guard for retired naming and workflow hardening.
