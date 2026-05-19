# Diagnostics Runbook

Use this runbook to triage ASBCP release or runtime failures.

## Release Gate Failure

1. Read the failing step from `scripts/verify-release.sh`.
2. If the governance guard fails, fix required files, workflow hardening, release evidence JSON, or retired naming.
3. If source tests fail, route to the owning code worker.
4. If image publication fails, inspect GHCR permissions and release workflow logs.

## Runtime Failure

1. Check `/healthz` and `/readyz`.
2. Check ASBCP service-key configuration.
3. Check AFSCP base URL, token, caller service, and actor identity.
4. Check workspace binding status.
5. Check workload Pod events and logs.
6. Check AFSCP lifecycle release status before deleting resources manually.

## Evidence Capture

Record command, timestamp, commit SHA, image digest, API contract version, and sanitized logs. Do not record service keys, AFSCP tokens, raw storage credentials, or user secrets.
