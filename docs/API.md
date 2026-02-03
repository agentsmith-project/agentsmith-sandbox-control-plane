# Sandbox Manager API

Base URL examples:

- Port-forward: `http://127.0.0.1:8080`
- In-cluster: `http://sandbox-manager.sandbox-system.svc`

## Auth

When enabled, requests to `/v1/*` require a service key.

- Header: `X-Service-Key: <key>` (default; configurable)

## Health & Debug

- `GET /healthz` → 200
- `GET /readyz` → 200 (ready) / 503 (not ready)
- `GET /metrics` → Prometheus metrics (may require auth based on config)
- `GET /debug/config` → current config + metadata

## Sandboxes (v1)

Session id is a caller-provided identifier. The system derives pod name deterministically.

- `PUT /v1/sandboxes/{sessionId}` → create/ensure sandbox
- `DELETE /v1/sandboxes/{sessionId}` → delete sandbox
- `POST /v1/sandboxes/{sessionId}/touch` → extend activity/TTL
- `POST /v1/sandboxes/{sessionId}/exec` → exec a command
- `POST /v1/sandboxes/{sessionId}/files/upload?dest=/workspace/...` → upload raw bytes (tar.gz per config)
- `GET /v1/sandboxes/{sessionId}/files/download?src=/workspace/...` → download (tar.gz per config)

### Create sandbox

`PUT /v1/sandboxes/{sessionId}`

Request body (`application/json`):

Notes:
- If `image` is omitted, the manager uses its configured default runner image.

```json
{
  "ttlSeconds": 900,
  "image": "sandbox-runner:1.0.0",
  "cpuLimit": "1",
  "memoryLimit": "1Gi",
  "ephemeralStorageLimit": "2Gi",
  "containerName": "runner",
  "workdir": "/workspace",
  "env": {"KEY": "VALUE"}
}
```

Response:

```json
{
  "podName": "sbx-...",
  "expiresAt": "2026-01-01T00:00:00Z"
}
```

### Exec

`POST /v1/sandboxes/{sessionId}/exec`

```json
{
  "cmd": ["echo", "hello"],
  "workdir": "/workspace",
  "env": {"KEY": "VALUE"},
  "timeoutSeconds": 10
}
```

Response:

```json
{
  "exitCode": 0,
  "stdout": "hello\n",
  "stderr": "",
  "durationMs": 12
}
```

## Errors

Errors are returned as JSON:

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "…",
    "requestId": "…"
  }
}
```
