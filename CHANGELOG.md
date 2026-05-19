# Changelog

All notable ASBCP release changes will be recorded here.

This project follows pre-GA evidence-first release notes. Each release entry must include the image digest, commit SHA, API contract version, breaking changes, and known operational risks.

## Unreleased

## 2.0.4 - 2026-05-19

- Fixed tag release evidence by generating `asbcp-final-manifest.json` after fresh anonymous digest inspect and uploading it as a GitHub Release asset.
- Hardened workflow guards so release notes are file-based and the release workflow must publish final manifest fields for version, tag, commit, image digest, API contract version, and public inspect result.
- Cleaned ASBCP release image build defaults so Dockerfile proxy usage is opt-in via empty-by-default `ASBCP_BUILD_*` build args and release gate builds clear host proxy defaults.

## 2.0.3 - 2026-05-19

- Added ASBCP public governance skeleton.
- Added authoritative release gate entrypoint at `scripts/verify-release.sh`.
- Added GitHub PR, CI, and tag release workflow skeletons.
- Added ASBCP/AFSCP boundary contracts, runbooks, ADRs, risk register, and readiness evidence ledger.
- Added governed release-surface guard for retired naming and workflow hardening.
