# Changelog

All notable ASBCP release changes will be recorded here.

This project follows pre-GA evidence-first release notes. The authoritative published image identity evidence is the GitHub Release asset `asbcp-final-manifest.json`; changelog entries must not require the post-push digest before the tag workflow creates that asset.

## Unreleased

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
