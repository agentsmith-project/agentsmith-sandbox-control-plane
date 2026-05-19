# Release Runbook

This runbook describes ASBCP image release. AgentSmith consumer adoption happens after ASBCP publishes an immutable image digest.

## Preconditions

- `docs/RISK_REGISTER.md` has no release-blocking open risks.
- `docs/READINESS_EVIDENCE.md` is updated.
- API contract version and breaking changes are known.
- The tag is `v<version>`.

## Steps

1. Run the authoritative gate:

```bash
bash scripts/verify-release.sh
```

2. Push a `v*` tag.
3. Confirm the release workflow logs show the gate ran before image build and push.
4. Confirm the GHCR image digest is present in the GitHub Release body.
5. Confirm the workflow pulled the digest form successfully.
6. Share the digest with AgentSmith for image-lock update and consumer adoption tests.

## Release Notes Must Include

- Version.
- Commit SHA.
- Image digest.
- API contract version.
- Breaking changes.
- Known risks and runbook links.

## Non-Goals

Do not run AgentSmith release gates as ASBCP release criteria. AgentSmith consumer adoption is separate downstream evidence.
