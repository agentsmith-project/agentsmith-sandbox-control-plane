# ADR 0002: Service Authentication

## Status

Accepted.

## Context

AgentSmith calls ASBCP as an internal service. ASBCP calls AFSCP to fetch mount plans and update lifecycle state.

## Decision

AgentSmith to ASBCP uses `X-Service-Key` backed by `ASBCP_SERVICE_KEYS`. ASBCP to AFSCP uses an orchestrator token and the canonical caller and actor identity `agentsmith-sandbox-control-plane`.

## Consequences

ASBCP does not perform end-user authorization. AgentSmith remains responsible for user and task authorization before making ASBCP calls.
