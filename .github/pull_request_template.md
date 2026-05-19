## Summary

Describe the ASBCP change and the affected release surface.

## Contract Impact

- API:
- Auth:
- AFSCP mount-plan dependency:
- Operation/error behavior:

## Security Impact

- Service keys or tokens:
- Kubernetes permissions:
- Secret/logging exposure:

## Operational Impact

- Image/release:
- Runbooks:
- Rollback or rollforward:

## Evidence

- [ ] `bash scripts/verify-release.sh --quick`
- [ ] Focused source or smoke test, if applicable:
- [ ] Full `bash scripts/verify-release.sh`, if this is a release candidate:

## Documentation

- [ ] README/docs updated or not needed.
- [ ] Readiness evidence updated or not needed.
- [ ] Risk register updated or not needed.

## Boundary Check

- [ ] ASBCP remains scoped to sandbox workload lifecycle.
- [ ] AgentSmith product governance remains outside ASBCP.
- [ ] AFSCP filesystem/storage truth remains outside ASBCP.
- [ ] No mutable image tag is introduced as an AgentSmith release dependency.
