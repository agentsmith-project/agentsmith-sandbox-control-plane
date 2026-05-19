# Development Governance

ASBCP uses an ASBCP-lite governance model. It borrows the release discipline of AFSCP-style control-plane work without copying AFSCP storage business content.

## Principles

- Contract first: API, auth, AFSCP dependency, operation, and error behavior are documented before implementation changes.
- Evidence driven: release claims require guard output, release evidence, workflow proof, and image digest proof.
- One release gate: `scripts/verify-release.sh` is the authoritative release-readiness entrypoint.
- Boundary clarity: ASBCP is a sandbox workload lifecycle service, not an AgentSmith product governance surface and not an AFSCP storage module.
- No compatibility drift: retired manager-era names and retired API shapes must not return to governed release files.

## Required Evidence

- Quick governance guard result for PR/main.
- Full release gate result for tag release.
- API contract version in release notes.
- GHCR image digest and commit SHA in release notes.
- Readiness and risk register updates when behavior changes.

## ADR Rules

Use `docs/adr/` for decisions that affect identity, service auth, workload lifecycle, release artifacts, AFSCP dependency, or AgentSmith consumption. ADRs should be short, dated when possible, and focused on ASBCP scope.

## Boundaries

ASBCP must not become the place where AgentSmith product controls or AFSCP storage controls are reimplemented. If a change needs those responsibilities, coordinate with the owning project and keep ASBCP contracts limited to what it consumes or exposes.
