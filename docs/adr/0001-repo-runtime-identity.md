# ADR 0001: Repository and Runtime Identity

## Status

Accepted.

## Context

ASBCP is published as an independent project and consumed by AgentSmith as an immutable image dependency.

## Decision

Use AgentSmith Sandbox Control Plane (ASBCP) as the canonical name. Runtime identity uses:

- Repository: `agentsmith-project/agentsmith-sandbox-control-plane`
- Image: `ghcr.io/agentsmith-project/agentsmith-sandbox-control-plane`
- Binary: `asbcp`
- Kubernetes app name: `agentsmith-sandbox-control-plane`
- Component label: `asbcp`

## Consequences

Public release docs and workflows use ASBCP identifiers. Retired manager-era names are blocked in the governed release surface by `.github/tests/asbcp-governance-guard.sh`.
