# Task C: Service Key Authentication

**Status:** ✅ Completed

## Summary

Implemented service key authentication middleware that protects `/v1/*` endpoints while leaving `/healthz`, `/readyz`, `/metrics`, and `/debug/config` publicly accessible.

## Implementation

### Files Created

- `internal/auth/servicekey.go` - Service key validation logic
- `internal/auth/middleware.go` - HTTP authentication middleware

### Key Features

1. **Constant-Time Key Comparison**
   - Uses `crypto/subtle.ConstantTimeCompare`
   - Prevents timing attacks for key guessing
   - Safe against statistical analysis

2. **Multiple Key Sources**
   - Primary: `X-Service-Key` header (configurable name)
   - Secondary: `Authorization: ServiceKey <key>` header (optional)
   - Supports key rotation via multiple valid keys

3. **Multiple Keys Support**
   - Service keys specified as comma-separated list
   - From `SERVICE_KEYS` environment variable
   - Stored in K8s Secret (not ConfigMap)

4. **Error Responses**
   - `401` for missing key (`SERVICE_KEY_MISSING`)
   - `401` for invalid key (`SERVICE_KEY_INVALID`)
   - Standard JSON error format with request ID

### Configuration

#### Environment Variables

```bash
# Comma-separated list of valid service keys
SERVICE_KEYS=key1,key2,key3
```

#### Runtime Configuration

```yaml
auth:
  enabled: true
  headerName: X-Service-Key
  acceptAuthorization: true
  authorizationScheme: ServiceKey
  failStatusCode: 401
```

### Route Protection

| Route | Auth Required | Notes |
|-------|---------------|-------|
| `/healthz` | No | Liveness probe |
| `/readyz` | No | Readiness probe |
| `/metrics` | Optional | Controlled by `server.metrics.requireServiceKey` |
| `/debug/config` | No | Debug endpoint |
| `/v1/*` | Yes | All business API endpoints |

### Example Requests

**With X-Service-Key header:**
```bash
curl -H "X-Service-Key: dev-key-12345" \
  http://localhost:8080/v1/sandboxes/my-session
```

**With Authorization header:**
```bash
curl -H "Authorization: ServiceKey dev-key-12345" \
  http://localhost:8080/v1/sandboxes/my-session
```

**Missing key returns 401:**
```json
{
  "error": {
    "code": "SERVICE_KEY_MISSING",
    "message": "Service key is required",
    "requestId": "abc123"
  }
}
```

## Validation

### Acceptance Criteria

- [x] No key returns 401 `SERVICE_KEY_MISSING`
- [x] Wrong key returns 401 `SERVICE_KEY_INVALID`
- [x] Valid key allows access to `/v1/*` endpoints
- [x] Operations endpoints (`/healthz`, `/readyz`, etc.) work without auth
- [x] Multiple keys can be configured for rotation

### Security Considerations

1. **Keys in Secret, Not ConfigMap**
   - Service keys stored in K8s Secret
   - Prevents accidental exposure in ConfigMap
   - Allows separate access controls

2. **Constant-Time Comparison**
   - Prevents timing-based key discovery
   - Safe against statistical analysis

3. **No Key = Open (Development Mode)**
   - If `SERVICE_KEYS` is empty, all requests allowed
   - Useful for local development
   - Should NOT be used in production

## Next Steps

See [04-exec-exitcode.md](./04-exec-exitcode.md) for exec exit code implementation.
