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
- Readiness evidence manifest must not contain `pending` or `deferred` required release evidence.
- Fake-fixture smoke for health/ready handlers, workspace binding, and workload create/keepalive/exec/release/delete paths.
- Source tests and binary build checks for the ASBCP service.
- Dockerfile contract and Docker image build with root `VERSION` metadata.
- `kubectl kustomize` render for dev, staging, and production overlays.
- Release workflow proof that GHCR image publication follows the gate.

The release workflow must call `scripts/verify-release.sh` before `docker/build-push-action`.

## Non-Gates

- AgentSmith consumer adoption tests are not ASBCP release gates.
- Manual approval alone is not release readiness.
- A successful PR/main workflow is not release readiness.
- A locally built mutable image tag is not release readiness.

## Release Evidence

Release evidence is tracked in `docs/release-evidence/release-manifest.json` and summarized in `docs/READINESS_EVIDENCE.md`. Each tag release must publish image digest, commit SHA, API contract version, breaking changes, known risk status, and an attached `asbcp-final-manifest.json` generated after anonymous public digest inspect.
