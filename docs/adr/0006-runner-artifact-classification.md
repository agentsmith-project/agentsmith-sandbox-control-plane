# ADR 0006: Runner Artifact Classification

## Status

Accepted.

## Context

The repository may contain runner-related assets, but the active AgentSmith integration needs the ASBCP service image as the release artifact.

## Decision

The ASBCP public release gate and GHCR release publish only the ASBCP service image unless a future ADR creates a separate runner artifact release plan.

## Consequences

Runner assets are not release blockers for the ASBCP service image unless they are part of active lifecycle smoke evidence.
