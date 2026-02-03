# Task A: Configuration Loading and Validation

**Status:** ✅ Completed

## Summary

Implemented a structured configuration system with YAML loading, validation, and default value application.

## Implementation

### Files Created

- `internal/config/types.go` - Configuration structure definitions
- `internal/config/load.go` - Configuration loading with defaults
- `internal/config/validate.go` - Configuration validation rules

### Key Features

1. **Structured Configuration Types**
   - Server configuration (timeouts, ports, metrics)
   - Authentication configuration (service key settings)
   - Kubernetes client configuration (QPS, burst, retry)
   - Sandbox defaults (resources, volumes, labels)
   - Exec configuration (timeouts, output limits, exit code marker)
   - Files configuration (upload/download limits, tar settings)

2. **Configuration Loading**
   - Load from YAML file with `config.Load()`
   - Apply defaults for missing values
   - Calculate SHA256 hash for change detection
   - Track metadata (source path, loaded time, reload count)

3. **Configuration Validation**
   - Version validation (only version 1 supported)
   - Range validation (timeouts, sizes)
   - Format validation (paths, regex patterns)
   - Kubernetes quantity validation
   - Relationship validation (preserveTailBytes <= maxBytes)

### Configuration Schema

```yaml
version: 1

server:
  httpPort: 8080
  requestIdHeader: X-Request-Id
  timeouts:
    readHeader: 5s
    read: 30s
    write: 60s
    idle: 120s

auth:
  enabled: true
  headerName: X-Service-Key

kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s

sandbox:
  defaults:
    namespace: sandbox
    runnerImage: sandbox-runner:1.0.0
    ttlSeconds: 900

exec:
  defaultTimeout: 30s
  maxTimeout: 300s
  stdoutMaxBytes: 1048576
  stderrMaxBytes: 1048576
  preserveTailBytes: 4096
  exitCodeMarker:
    key: "__SBX_EXIT_CODE__"
    stream: "stderr"

files:
  rootPrefix: /workspace
  upload:
    maxBytes: 52428800
    format: tar.gz
  download:
    format: tar.gz
```

## Validation

### Acceptance Criteria

- [x] Startup correctly loads configuration from file
- [x] Configuration hash is correctly calculated
- [x] Invalid configuration returns detailed error messages
- [x] Defaults are applied for missing values

### Test Results

Configuration validation includes:
- Field-level error messages with `fieldPath`, `ruleId`, `rule`, and `message`
- Support for all Kubernetes quantity formats
- Path validation with absolute path requirements
- Regex validation for environment variable keys

## Next Steps

See [02-config-hotreload.md](./02-config-hotreload.md) for ConfigMap hot reload implementation.
