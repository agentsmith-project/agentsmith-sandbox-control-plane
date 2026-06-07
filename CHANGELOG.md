# Changelog

All notable ASBCP release changes will be recorded here.

This project follows pre-GA evidence-first release notes. The authoritative published image identity evidence is the GitHub Release asset `asbcp-final-manifest.json`; changelog entries must not require the post-push digest before the tag workflow creates that asset.

## Unreleased

## [v2.0.18] - 2026-06-06

### Fixed

- Workload DELETE now serializes concurrent release/delete convergence for the same workload and uses a stable terminal status `observed_at` plus workload/mount scoped idempotency key, preventing repeated request IDs from producing AFSCP `IDEMPOTENCY_CONFLICT` during release/status retries.

## [v2.0.17] - 2026-06-06

### Fixed

- Workload DELETE now skips the storage flush barrier for Pending Pods whose main container never started, allowing retryable release/delete convergence instead of retaining no-writer Pods until they consume local-kind capacity.

## [v2.0.16] - 2026-06-02

### Fixed

- Workload create now treats temporarily invisible workspace PVCs (`NotFound`) as retryable `503 not_ready` readiness with `Retry-After`, while RBAC/generic PVC get errors still fail fast.

## [v2.0.15] - 2026-06-02

### Fixed

- Workspace binding PVC briefly `NotFound`/unobservable now remains a retryable `503 not_ready` readiness gap, while non-readiness PVC get errors still fail fast.

## [v2.0.14] - 2026-06-02

### Fixed

- Workspace binding/workload create now waits for each per-binding PVC to be `Bound`; `Pending`/unbound PVCs return retryable `503 not_ready`, avoiding sandbox Pod creation before its PVC is bound.

## [v2.0.13] - 2026-06-01

### Changed

- Removed the provider-specific Jira Python dependency from the runner fixture and offline bundle inputs so ASBCP runner assets remain provider-neutral.

## [v2.0.12] - 2026-05-29

### Fixed

- Workspace init containers are restored to restricted-compatible non-root `1000:1000` execution with only fail-fast `mkdir -p` and `test -w` checks for `TASK_HOME`, `WORKSPACE_PATH`, and `ARTIFACTS_PATH`; writable directories are provided by the substrate/CSI fsGroup-capable volume contract.
- `v2.0.11` is superseded by `v2.0.12` and is not the recommended adoption version because it used a root workspace-init workaround.

## [v2.0.11] - 2026-05-29

### Release Status

- Superseded by `v2.0.12`; not recommended for adoption.

### Fixed

- Workspace init containers now run as `root:1000` only for task directory preparation while managed workload containers remain non-root `1000:1000`, fixing root-owned CSI/JuiceFS payload mounts where non-root init could not prepare writable task HOME, workspace, and workspace `.artifacts` directories.

## [v2.0.10] - 2026-05-28

### Changed

- Workload status responses now expose the main container desired image reference (`image`/`image_ref`) and live Kubernetes imageID (`image_id`) for AgentSmith managed runner image identity checks.

## [v2.0.9] - 2026-05-27

### Changed

- Workspace binding JuiceFS CSI PVs now add `attr-cache=0s`, `entry-cache=0s`, `dir-entry-cache=0s`, and `negative-entry-cache=0s` mount options while preserving the AFSCP payload `subdir`, prioritizing pre-GA cross-client delete visibility correctness.

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
