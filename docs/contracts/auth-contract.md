# Service Auth Contract

ASBCP uses service-to-service authentication. AgentSmith calls ASBCP with a service key; ASBCP calls AFSCP with an orchestrator token and caller identity.

## AgentSmith to ASBCP

Required AgentSmith runtime values:

- `ASBCP_INTERNAL_BASE_URL`
- `ASBCP_SERVICE_KEY`

Required ASBCP service values:

- `ASBCP_SERVICE_KEYS`

All `/v1/` API routes require `X-Service-Key`. Health, readiness, and metrics endpoints do not require service-key auth.

## ASBCP to AFSCP

Required ASBCP values:

- `ASBCP_AFSCP_INTERNAL_BASE_URL`
- `ASBCP_AFSCP_ORCHESTRATOR_TOKEN`
- `ASBCP_AFSCP_CALLER_SERVICE=agentsmith-sandbox-control-plane`
- `ASBCP_AFSCP_ACTOR_TYPE=system`
- `ASBCP_AFSCP_ACTOR_ID=agentsmith-sandbox-control-plane`

ASBCP must use the canonical caller identity consistently across configuration, AFSCP allowlists, logs, and release evidence. The machine-readable ASBCP service prerequisite list is `docs/contracts/asbcp-provider-prerequisites.v1.json`; it records the required `orchestrator_mount` role, allowed caller, Kubernetes RBAC verbs, secret keys, environment projections, and no-public-ingress requirement.

## Secret Rules

- Do not write service keys or AFSCP tokens to docs, logs, release evidence, or reusable local config.
- Do not persist request-scoped credentials in the repository.
- Do not treat raw storage credentials as ASBCP runtime configuration.
