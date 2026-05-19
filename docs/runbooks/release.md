# Release Runbook

This runbook describes ASBCP image release. AgentSmith consumer adoption happens after ASBCP publishes an immutable image digest.

## Preconditions

- `docs/RISK_REGISTER.md` has no release-blocking open risks.
- `docs/READINESS_EVIDENCE.md` is updated.
- API contract version and breaking changes are known.
- `CHANGELOG.md` contains the current `v<version>` release section; `scripts/verify-release.sh` must parse `known_breaking_changes` and `changelog_summary` before any image push.
- `scripts/verify-release.sh --risk-status-json` reports no release-blocking risks while preserving open non-release-blocking risks from `docs/RISK_REGISTER.md`.
- The tag is `v<version>`.

## Steps

1. Run the authoritative gate:

```bash
bash scripts/verify-release.sh
```

2. Push a `v*` tag.
3. Confirm the release workflow logs show the gate ran before image build and push.
4. Confirm the GitHub Release includes `asbcp-final-manifest.json`.
5. Confirm the final manifest records `known_breaking_changes` and `changelog_summary` from `scripts/verify-release.sh --changelog-evidence-json <tag>`.
6. Confirm the final manifest records `known_risk_status` and `known_risk_status_source` from `scripts/verify-release.sh --risk-status-json`.
7. Confirm the final manifest records `anonymous_pull` from a fresh empty Docker config, including `tag_resolved_digest` from `image:tag` and `anonymous_digest` from `image:tag@build_push_digest`.
8. Confirm `same_digest_proof.matches: true` because `tag_resolved_digest`, `build_push_digest`, and `anonymous_digest` are identical; this is published image identity evidence, not post-push container behavior evidence.
9. Confirm `release_notes.body_source` contains the complete GitHub Release body text and `body_path` was written from that field.
10. After ASBCP release completes, share the digest with AgentSmith for image-lock update and downstream consumer adoption tests.

## Release Notes Must Include

- Version.
- Commit SHA.
- Image digest.
- API contract version.
- Breaking changes.
- CHANGELOG-derived `changelog_summary`.
- Risk-register-derived `known_risk_status`, `known_risk_status_source`, and runbook links.
- `same_digest_proof` with the matching tag-resolved, build-push, and anonymous `tag@digest` digests, without presenting it as post-push container behavior evidence.

## Non-Goals

Do not run AgentSmith release gates as ASBCP release criteria. AgentSmith consumer adoption is separate downstream evidence.
