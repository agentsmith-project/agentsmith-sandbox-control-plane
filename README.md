# AgentSmith Sandbox Control Plane (ASBCP)

AgentSmith Sandbox Control Plane (ASBCP) is the sandbox workload lifecycle service for AgentSmith internal agent tasks. It is an independently released service with its own API contract, release gate, runbooks, risk ledger, and image publication path.

ASBCP is not the AgentSmith product management surface and is not an AFSCP submodule. AgentSmith chooses projects, tasks, authorization, and runner images. AFSCP owns filesystem and storage truth. ASBCP consumes an AFSCP workload mount plan and manages Kubernetes workload lifecycle resources.

## Scope

ASBCP owns:

- Workspace binding materialization from an AFSCP workload mount plan.
- Kubernetes PV/PVC and workload Pod lifecycle.
- Workload create, keepalive, exec, release, delete, health, readiness, and metrics APIs.
- ASBCP API contracts, release evidence, operational runbooks, and release workflows.

ASBCP does not own:

- AgentSmith product governance, UI, audit, AI resource policy, or task authorization.
- AFSCP filesystem truth, storage credentials, snapshot/version semantics, or recovery policy.
- Mutable image consumption by AgentSmith. AgentSmith must consume an immutable ASBCP image digest.

## Canonical Identifiers

| Layer | Canonical value |
| --- | --- |
| Full name | AgentSmith Sandbox Control Plane |
| Short name | ASBCP |
| Repository | `agentsmith-project/agentsmith-sandbox-control-plane` |
| Image | `ghcr.io/agentsmith-project/agentsmith-sandbox-control-plane:<version>@sha256:<digest>` |
| Binary | `asbcp` |
| Kubernetes app name | `agentsmith-sandbox-control-plane` |
| Kubernetes component label | `asbcp` |
| AFSCP caller service | `agentsmith-sandbox-control-plane` |
| AgentSmith runtime env | `ASBCP_IMAGE`, `ASBCP_INTERNAL_BASE_URL`, `ASBCP_SERVICE_KEY` |
| ASBCP service env | `ASBCP_CONFIG_PATH`, `ASBCP_SERVICE_KEYS`, `ASBCP_WORKLOAD_NAMESPACE`, `ASBCP_AFSCP_INTERNAL_BASE_URL`, `ASBCP_AFSCP_ORCHESTRATOR_TOKEN`, `ASBCP_AFSCP_CALLER_SERVICE`, `ASBCP_AFSCP_ACTOR_ID` |

## API Surface

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe |
| `GET` | `/metrics` | Prometheus metrics |
| `PUT` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}` | Ensure a workspace binding from an AFSCP plan |
| `GET` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}` | Read sanitized binding status |
| `DELETE` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workspace-bindings/{binding_id}` | Delete ASBCP-managed binding resources |
| `PUT` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}` | Ensure workload Pod |
| `GET` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}` | Read workload status |
| `POST` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}/keepalive` | Extend workload lifetime and AFSCP lifecycle |
| `POST` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}/exec` | Execute a command in the workload Pod |
| `DELETE` | `/v1/workspaces/{workspace_id}/projects/{project_id}/workloads/{workload_id}` | Release AFSCP lifecycle and delete workload Pod |

## Quick Verification

PR and main CI use quick governance checks. They prove the public governance surface and workflow hardening are intact, but they do not declare release readiness.

```bash
bash scripts/verify-release.sh --quick
```

The only authoritative ASBCP release gate is:

```bash
bash scripts/verify-release.sh
```

Tag releases must run that script before building or publishing a GHCR image.

## AgentSmith Consumption

AgentSmith consumes ASBCP as an external immutable image dependency. The consumer flow is:

```text
ASBCP release image digest
  -> AgentSmith ASBCP image lock
  -> generated site env
  -> Kubernetes render
  -> rollout and focused consumer smoke
```

AgentSmith must not build ASBCP source as part of its release lane and must not use mutable tags as release dependencies.

## Documentation

- [Developer Guide](docs/DEVELOPER_GUIDE.md)
- [Development Governance](docs/DEVELOPMENT_GOVERNANCE.md)
- [Release Gates](docs/RELEASE_GATES.md)
- [Readiness Evidence](docs/READINESS_EVIDENCE.md)
- [Risk Register](docs/RISK_REGISTER.md)
- [API Contract](docs/contracts/api-contract.md)
- [AFSCP Mount Plan Contract](docs/contracts/afscp-mount-plan-contract.md)
- [Release Runbook](docs/runbooks/release.md)
