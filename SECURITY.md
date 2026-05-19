# Security Policy

## Supported Surface

Security reports are accepted for the ASBCP service, container image, release workflow, API contracts, service authentication, workload lifecycle behavior, and Kubernetes resource handling.

ASBCP does not own AgentSmith user authorization or AFSCP filesystem truth. Reports in those areas should be routed to the owning project, but ASBCP maintainers will help triage cross-project impact.

## Reporting a Vulnerability

Please report security issues privately through the repository security advisory flow when available. If advisory reporting is unavailable, contact the maintainers through the private channel documented by the AgentSmith project.

Include:

- Affected ASBCP version or commit.
- Reproduction steps.
- Expected impact.
- Whether the issue requires AgentSmith or AFSCP coordination.
- Any logs or request examples with secrets removed.

## Secret Handling

Do not put service keys, AFSCP tokens, Kubernetes credentials, or raw storage credentials into issues, PRs, release evidence, or logs. ASBCP should receive only the AFSCP mount plan data needed to manage workload lifecycle resources.

## Release Security Gate

The release workflow must call `scripts/verify-release.sh` before publishing a GHCR image. Release evidence must include the immutable image digest and commit SHA.
