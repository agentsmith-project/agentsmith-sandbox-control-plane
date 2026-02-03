# Task F: HTTP Error Models + Request ID + Graceful Shutdown

**Status:** ✅ Completed

## Summary

Implemented standard error response format, request ID tracking, and graceful HTTP server shutdown.

## Implementation

### Files Created

- `internal/httpapi/errors.go` - Error codes and response formats
- `internal/httpapi/types.go` - Request/response types
- `internal/observability/requestid.go` - Request ID middleware

### Key Features

1. **Standard Error Response Format**
   - All errors return consistent JSON structure
   - Includes error code, message, and request ID
   - Optional details field for additional context

2. **Request ID Tracking**
   - Extracts from `X-Request-Id` header (or variants)
   - Generates UUID if not present
   - Added to response headers
   - Included in error responses

3. **Graceful Shutdown**
   - Listens for SIGINT/SIGTERM
   - 30-second shutdown timeout
   - Completes in-flight requests
   - Rejects new requests during shutdown

### Error Response Format

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable error message",
    "requestId": "uuid-here",
    "details": {
      "optional": "additional context"
    }
  }
}
```

### Error Codes

| Code | Status | Description |
|------|--------|-------------|
| `SERVICE_KEY_MISSING` | 401 | No service key provided |
| `SERVICE_KEY_INVALID` | 401 | Invalid service key |
| `CONFIG_NOT_LOADED` | 503 | Configuration not loaded |
| `NOT_READY` | 503 | Service not ready |
| `BAD_REQUEST` | 400 | Invalid request format |
| `INVALID_ENV_KEY` | 422 | Invalid environment variable name |
| `INVALID_WORKDIR` | 422 | Invalid working directory |
| `INVALID_PATH` | 422 | Invalid file path |
| `UPLOAD_TOO_LARGE` | 413 | Upload exceeds size limit |
| `POD_NOT_FOUND` | 404 | Sandbox pod not found |
| `POD_NOT_READY` | 503 | Sandbox pod not ready |
| `POD_READY_TIMEOUT` | 504 | Pod ready timeout |
| `EXEC_TIMEOUT` | 504 | Command execution timeout |
| `EXEC_EXITCODE_UNAVAILABLE` | 500 | Exit code unavailable |

### Request ID Flow

```
Request without X-Request-Id:
  Client → Server → Generate UUID → Add to context → Process → Return in response header

Request with X-Request-Id:
  Client → Server → Extract from header → Add to context → Process → Return in response header
```

### Graceful Shutdown

```go
srv := &http.Server{
    Addr:         ":8080",
    ReadTimeout:  30 * time.Second,
    WriteTimeout: 60 * time.Second,
    IdleTimeout:  120 * time.Second,
}

go func() {
    sigint := make(chan os.Signal, 1)
    signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
    <-sigint

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    srv.Shutdown(ctx)
}()
```

### Middleware Stack

```
Request → RequestIDMiddleware → AuthMiddleware → ObservabilityMiddleware → Handler
         ↓                       ↓                  ↓                      ↓
      Add ID                Check Key          Log/Metric           Process
         ↓                       ↓                  ↓                      ↓
      Return               Return 401         Record Metrics        Return
```

## Configuration

```yaml
server:
  httpPort: 8080
  requestIdHeader: X-Request-Id
  timeouts:
    read: 30s
    write: 60s
    idle: 120s
```

### Boot Parameters

```bash
# Request ID header name
REQUEST_ID_HEADER=${REQUEST_ID_HEADER:-X-Request-Id}

# Shutdown timeout (seconds)
SHUTDOWN_TIMEOUT=${SHUTDOWN_TIMEOUT:-30}
```

## Validation

### Acceptance Criteria

- [x] All errors use consistent JSON format
- [x] Response headers include request ID
- [x] SIGTERM triggers graceful shutdown
- [x] In-flight requests complete during shutdown

### Example Error Responses

**Missing Service Key:**
```json
{
  "error": {
    "code": "SERVICE_KEY_MISSING",
    "message": "Service key is required",
    "requestId": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

**Invalid Environment Variable:**
```json
{
  "error": {
    "code": "INVALID_ENV_KEY",
    "message": "Environment variable key '9INVALID' does not match pattern '^[A-Z_][A-Z0-9_]*$'",
    "requestId": "550e8400-e29b-41d4-a716-446655440000",
    "details": {
      "key": "9INVALID",
      "pattern": "^[A-Z_][A-Z0-9_]*$"
    }
  }
}
```

## Next Steps

See [07-observability.md](./07-observability.md) for metrics and readiness endpoints.
