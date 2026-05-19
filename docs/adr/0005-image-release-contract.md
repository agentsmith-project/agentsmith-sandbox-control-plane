# ADR 0005: Image Release Contract

## Status

Accepted.

## Context

AgentSmith needs a stable dependency on ASBCP without building ASBCP source in its own release lane.

## Decision

ASBCP publishes `ghcr.io/agentsmith-project/agentsmith-sandbox-control-plane:<version>@sha256:<digest>`. AgentSmith consumes the digest through its own image lock and consumer adoption gates.

## Consequences

ASBCP release workflow must run `scripts/verify-release.sh`, build and push the image, record the digest, verify digest pull, and publish release notes.
