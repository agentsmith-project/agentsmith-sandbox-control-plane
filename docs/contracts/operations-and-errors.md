# Operations and Errors Contract

ASBCP operations must be safe to retry unless explicitly documented otherwise.

## Operation Semantics

- Workspace binding ensure should create or reuse Kubernetes binding resources.
- Workload ensure should create or reuse the workload Pod for the same workload identifier.
- Keepalive should extend ASBCP and AFSCP lifecycle state.
- Exec should run against an existing workload Pod and return command output or stream status according to implementation capability.
- Delete should release AFSCP lifecycle state before reporting the workload released. If both the durable workload terminal fact and Pod are missing, Delete must fail closed with retryable `409` code `workload_release_incomplete`; Pod absence alone is not terminal truth.
- Workload Pod operations must validate Pod annotations and labels against the URL `{workspace_id, project_id, workload_id}` before acting. Scope metadata drift is a non-retryable `409` `conflict` until the incompatible Pod is reconciled.

## Error Shape

API errors use a stable JSON envelope:

```json
{
  "error": {
    "code": "dependency_failure",
    "message": "AFSCP orchestrator mount plan is unavailable",
    "request_id": "req-..."
  }
}
```

The envelope must include a stable machine-readable `code`, a human-readable `message`, and `request_id` when available. API responses must not expose Kubernetes raw errors, AFSCP raw errors, service keys, AFSCP tokens, raw storage credentials, or full command output unless explicitly safe.

## Status Codes

| Status | Meaning |
| --- | --- |
| `400` | Invalid request or unsupported contract field |
| `401` | Missing or invalid service key |
| `404` | Binding or non-delete workload operation target not found |
| `409` | Retryable lifecycle conflict, incompatible existing resource, or missing durable workload terminal truth |
| `422` | Valid request shape but invalid lifecycle state |
| `429` | Rate limited |
| `500` | Internal ASBCP failure |
| `502` | AFSCP or Kubernetes dependency failure |
| `503` | ASBCP not ready, or a workspace binding exists but its per-binding PVC is not yet `Bound` |

## Stable 409 Codes

| Code | Meaning |
| --- | --- |
| `workload_release_incomplete` | Workload DELETE cannot yet prove durable release terminal truth. Retry the workload release/delete path. |
| `workspace_binding_release_incomplete` | Workspace binding DELETE cannot prove every workload for the binding is released, or the fact source needed for that proof is unavailable. Retry after workload release state is reconciled. |
| `conflict` | Generic non-release conflict, such as incompatible existing resources. Do not treat every `409` as release retryable. |

## Stable 503 Codes

| Code | Meaning |
| --- | --- |
| `not_ready` | Retryable readiness gap. This can mean ASBCP service readiness, or for workspace binding ensure/get and workload create, that the binding PVC is still Pending/unbound after ASBCP created or reused PV/PVC objects. `Retry-After` may be present. |

## Operational Logging

Logs should include ASBCP request identifiers and workload identifiers, but must not include secrets, tokens, raw storage credentials, or full command output unless explicitly safe.
