# Rollback and Rollforward Runbook

ASBCP rollback and rollforward are image-digest operations.

## Rollback

1. Identify the last known good ASBCP image digest.
2. Confirm the digest belongs to a GitHub Release with release evidence.
3. Ask AgentSmith operators to update the ASBCP image lock to that digest.
4. Roll out the consumer environment.
5. Run focused AgentSmith consumer smoke for workspace binding and workload lifecycle.

## Rollforward

1. Fix the ASBCP issue in a new commit.
2. Run `bash scripts/verify-release.sh`.
3. Publish a new tag and GHCR digest.
4. Update AgentSmith image lock to the new digest.
5. Run consumer adoption evidence.

## Data and Lifecycle Notes

ASBCP should avoid destructive cleanup until AFSCP lifecycle release is confirmed. If release fails, preserve workload resources where possible so retry can complete safely.
