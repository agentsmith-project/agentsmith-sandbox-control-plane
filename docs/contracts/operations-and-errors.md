# Operations and Errors Contract

ASBCP operations must be safe to retry unless explicitly documented otherwise.

## Operation Semantics

- Workspace binding ensure should create or reuse Kubernetes binding resources.
- Workload ensure should create or reuse the workload Pod for the same workload identifier.
- Keepalive should extend ASBCP and AFSCP lifecycle state.
- Exec should run against an existing workload Pod and return command output or stream status according to implementation capability.
- Delete should release AFSCP lifecycle state before reporting the workload released.

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
| `404` | Binding or workload not found |
| `409` | Lifecycle conflict or incompatible existing resource |
| `422` | Valid request shape but invalid lifecycle state |
| `429` | Rate limited |
| `500` | Internal ASBCP failure |
| `502` | AFSCP or Kubernetes dependency failure |
| `503` | ASBCP not ready |

## Operational Logging

Logs should include ASBCP request identifiers and workload identifiers, but must not include secrets, tokens, raw storage credentials, or full command output unless explicitly safe.
