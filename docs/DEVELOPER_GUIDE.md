# Developer Guide

This guide covers the ASBCP developer workflow. ASBCP means AgentSmith Sandbox Control Plane: the independently released sandbox workload lifecycle service consumed by AgentSmith.

## Local Checks

Use quick governance checks while editing docs, release workflows, or public project metadata:

```bash
bash scripts/verify-release.sh --quick
```

Use the full release gate only when evaluating an ASBCP release candidate:

```bash
bash scripts/verify-release.sh
```

The quick mode is not release readiness. It exists so PR/main can validate required governance files, workflow hardening, JSON evidence shape, and shell syntax without claiming that the image is releasable.

## Development Boundaries

- ASBCP manages sandbox workload lifecycle resources.
- AgentSmith manages product authorization, task selection, project context, UI, audit, and resource policy.
- AFSCP manages filesystem and storage truth. ASBCP consumes AFSCP mount plans; it does not own storage credentials or storage policy.

## Contract-First Flow

1. Update the relevant file under `docs/contracts/`.
2. Add or update focused guard evidence.
3. Update implementation in the owning source area.
4. Run quick checks during PR.
5. Run the full release gate before tag release.

## Build Notes

The public release image is `ghcr.io/agentsmith-project/agentsmith-sandbox-control-plane:<version>@sha256:<digest>`. AgentSmith must consume the digest form, not a mutable tag.

The current governance skeleton intentionally keeps code and Kubernetes migration risks visible in `docs/RISK_REGISTER.md` until the naming and runtime workers finish their slices.
