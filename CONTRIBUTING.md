# Contributing

Thank you for helping improve AgentSmith Sandbox Control Plane (ASBCP). This project uses contract-first, evidence-driven delivery because ASBCP is a releaseable infrastructure component consumed by AgentSmith through an immutable image digest.

## Contribution Checklist

- Keep ASBCP scoped to sandbox workload lifecycle.
- Do not move AgentSmith product governance, audit, UI, or AI resource policy into ASBCP.
- Do not copy AFSCP storage-control-plane business content into ASBCP.
- Update API, auth, operation, or AFSCP mount-plan contracts before changing behavior.
- Add or update focused guard tests before changing release workflow or governance files.
- Use `bash scripts/verify-release.sh --quick` for governance-only PR checks.
- Use `bash scripts/verify-release.sh` only when you are ready to evaluate ASBCP release readiness.

## Pull Requests

Every PR should include:

- Contract impact.
- Security impact.
- Operational impact.
- Test and evidence output.
- Documentation impact.

The PR template in `.github/pull_request_template.md` is the required review shape.

## Naming

Use the canonical ASBCP identifiers from `README.md`. Retired manager-era identifiers must not appear in governed release files, workflows, or active public docs. The governance guard enforces this.

## Release Readiness

PR/main CI is intentionally lighter and must not be described as release readiness. The only authoritative release gate is `scripts/verify-release.sh`; the tag release workflow must call it before image publication.
