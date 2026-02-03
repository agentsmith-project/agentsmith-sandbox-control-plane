# Task H: /debug/config Endpoint

**Status:** ✅ Completed

## Summary

Implemented `/debug/config` endpoint that exposes current configuration with sanitization, metadata, and reload statistics.

## Implementation

### Files Modified

- `cmd/manager/main.go` - Added `handleDebugConfig` method
- `internal/httpapi/types.go` - Debug config response types

### /debug/config Endpoint

**Path:** `GET /debug/config`

**Purpose:** Expose current configuration for debugging and monitoring.

**Authentication:** Not required (public endpoint for operations)

### Response Structure

```json
{
  "meta": {
    "schemaVersion": 1,
    "sourcePath": "/etc/sandbox-manager/manager-config.yaml",
    "currentHash": "abc123...",
    "loadedAt": "2026-01-21T10:00:00Z",
    "reloadCount": 5,
    "lastError": null
  },
  "config": {
    "version": 1,
    "server": {
      "httpPort": 8080,
      "requestIdHeader": "X-Request-Id",
      "timeouts": {
        "readHeader": "5s",
        "read": "30s",
        "write": "60s",
        "idle": "120s"
      },
      "metrics": {
        "enabled": true,
        "path": "/metrics",
        "requireServiceKey": false
      },
      "debug": {
        "configPath": "/debug/config",
        "enablePprof": false
      }
    },
    "auth": {
      "enabled": true,
      "headerName": "X-Service-Key",
      "acceptAuthorization": true,
      "authorizationScheme": "ServiceKey",
      "failStatusCode": 401
    },
    "kubernetes": {
      "qps": 50,
      "burst": 100,
      "requestTimeout": "15s",
      "retry": {
        "enabled": true,
        "maxAttempts": 3,
        "baseBackoff": "200ms",
        "maxBackoff": "2s"
      }
    },
    "exec": {
      "defaultTimeout": "30s",
      "maxTimeout": "300s",
      "stdoutMaxBytes": 1048576,
      "stderrMaxBytes": 1048576,
      "preserveTailBytes": 4096,
      "exitCodeMarker": {
        "key": "__SBX_EXIT_CODE__",
        "stream": "stderr"
      },
      "shell": {
        "bin": "sh",
        "args": ["-lc"]
      },
      "env": {
        "allowRegex": "^[A-Z_][A-Z0-9_]*$"
      },
      "workdir": {
        "allowedPrefixes": ["/workspace"]
      }
    },
    "files": {
      "rootPrefix": "/workspace",
      "upload": {
        "defaultDest": "/workspace",
        "maxBytes": 52428800,
        "format": "tar.gz"
      },
      "download": {
        "defaultSrc": "/workspace",
        "format": "tar.gz"
      },
      "tar": {
        "bin": "tar",
        "rejectSymlinks": true
      }
    }
  },
  "boot": {
    "configPath": "/etc/sandbox-manager/manager-config.yaml",
    "debounceDuration": "300ms",
    "minInterval": "1s",
    "maxBackoff": "30s",
    "strictMode": false
  }
}
```

### Fields

#### meta
| Field | Description |
|-------|-------------|
| `schemaVersion` | Config schema version (always 1) |
| `sourcePath` | Path to config file |
| `currentHash` | SHA256 hash of current config |
| `loadedAt` | Timestamp when config was loaded |
| `reloadCount` | Number of successful reloads |
| `lastError` | Last reload error (if any) |

#### config
The full current configuration (sanitized - no service keys).

#### boot
Boot configuration parameters (from environment variables).

### Error Handling

**Last Error Structure:**
```json
{
  "meta": {
    "lastError": {
      "code": "CONFIG_VALIDATION_FAILED",
      "message": "Configuration validation failed with 2 errors",
      "fieldPath": "exec.maxTimeout",
      "ruleId": "RANGE",
      "rule": "maxTimeout must be between 0 and 1 hour",
      "timestamp": "2026-01-21T10:05:00Z"
    }
  }
}
```

### Sanitization

**What's Excluded:**
- Service keys (from `SERVICE_KEYS` env var)
- Any sensitive data

**What's Included:**
- All non-sensitive configuration
- Configuration metadata
- Boot parameters

### Use Cases

1. **Verify Config Loading**
   ```bash
   curl http://localhost:8080/debug/config | jq '.meta.loadedAt'
   ```

2. **Check Hash Changes**
   ```bash
   # Before update
   HASH1=$(curl -s http://localhost:8080/debug/config | jq -r '.meta.currentHash')

   # Update ConfigMap...

   # After update
   HASH2=$(curl -s http://localhost:8080/debug/config | jq -r '.meta.currentHash')

   if [ "$HASH1" != "$HASH2" ]; then
     echo "Config reloaded!"
   fi
   ```

3. **Monitor Reload Stats**
   ```bash
   curl http://localhost:8080/debug/config | jq '.meta.reloadCount'
   ```

4. **Debug Load Failures**
   ```bash
   curl http://localhost:8080/debug/config | jq '.meta.lastError'
   ```

## Validation

### Acceptance Criteria

- [x] Returns current hash
- [x] Shows loadedAt timestamp
- [x] Shows lastError if reload failed
- [x] Config is sanitized (no keys exposed)

### Example Queries

**Get current hash:**
```bash
curl -s http://localhost:8080/debug/config | jq -r '.meta.currentHash'
```

**Check if config loaded:**
```bash
curl -s http://localhost:8080/debug/config | jq -r '.meta.loadedAt'
```

**Get reload count:**
```bash
curl -s http://localhost:8080/debug/config | jq '.meta.reloadCount'
```

**View boot parameters:**
```bash
curl -s http://localhost:8080/debug/config | jq '.boot'
```

## Next Steps

See [09-integration-test.md](./09-integration-test.md) for integration testing guide.
