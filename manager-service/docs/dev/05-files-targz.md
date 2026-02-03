# Task E: Unified tar.gz File Protocol

**Status:** ✅ Completed

## Summary

Implemented unified tar.gz protocol for both file upload and download, replacing the previous inconsistent gzip/no-gzip approach.

## Implementation

### Files Created

- `internal/files/tar.go` - File upload/download handlers

### Key Features

1. **Upload: tar.gz from stdin**
   - Client sends tar.gz stream
   - Pod executes: `tar -xzf - -C <dest>`
   - Validates destination path
   - Enforces size limit

2. **Download: tar.gz to stdout**
   - Pod executes: `tar -czf - -C <src> .`
   - Streams tar.gz to client
   - Validates source path
   - Client extracts with `tar -xzf -`

3. **Path Validation**
   - Must be absolute path
   - Must be under `rootPrefix` (/workspace)
   - Prevents path traversal attacks

4. **Size Limits**
   - Configurable max upload size
   - Default: 50MB
   - Returns 413 if exceeded

### Configuration

```yaml
files:
  rootPrefix: /workspace
  upload:
    defaultDest: /workspace
    maxBytes: 52428800    # 50MB
    format: tar.gz
  download:
    defaultSrc: /workspace
    format: tar.gz
  tar:
    bin: tar
    rejectSymlinks: true
```

### Upload Flow

```
Client → Manager → Pod (tar -xzf - -C /workspace)
       ←          ←
```

1. Client creates tar.gz of files
2. POST to `/v1/sandboxes/{id}/files/upload?dest=/workspace/subdir`
3. Manager streams tar.gz to pod
4. Pod extracts with `tar -xzf - -C /workspace/subdir`
5. Manager returns 200 on success

### Download Flow

```
Client ← Manager ← Pod (tar -czf - -C /workspace .)
       →          →
```

1. GET from `/v1/sandboxes/{id}/files/download?src=/workspace/subdir`
2. Manager streams to pod's exec
3. Pod creates tar.gz with `tar -czf - -C /workspace/subdir .`
4. Manager streams response to client
5. Client extracts with `tar -xzf -`

### Example Commands

**Upload files:**
```bash
# Create tar.gz
tar -czf - file1.txt file2.txt | \
  curl -X POST \
    -H "X-Service-Key: dev-key-12345" \
    -H "Content-Type: application/x-gzip" \
    --data-binary @- \
    "http://localhost:8080/v1/sandboxes/session1/files/upload?dest=/workspace"
```

**Download files:**
```bash
# Extract tar.gz from response
curl -X GET \
  -H "X-Service-Key: dev-key-12345" \
  "http://localhost:8080/v1/sandboxes/session1/files/download?src=/workspace" \
  | tar -xzf -
```

### Path Validation

All paths must:
1. Be absolute (start with `/`)
2. Be under `rootPrefix` (default: `/workspace`)
3. Not contain `..` components that escape rootPrefix

Examples:
- ✅ `/workspace` - valid
- ✅ `/workspace/subdir` - valid
- ❌ `/tmp` - invalid (not under rootPrefix)
- ❌ `/workspace/../etc` - invalid (escapes rootPrefix)

## Validation

### Acceptance Criteria

- [x] Upload tar.gz correctly extracts in pod
- [x] Download tar.gz correctly packages files
- [x] Invalid paths are rejected
- [x] Uploads exceeding limit return 413
- [x] Symlinks are handled correctly

### Protocol Change Notice

**⚠️ Breaking Change:** This version uses `tar.gz` for both upload and download. The previous version used:
- Upload: tar.gz (gzip)
- Download: tar (no gzip)

**Migration:** Clients must be updated to:
1. **Upload:** Continue sending tar.gz (no change)
2. **Download:** Now handle gzip-encoded response

## Next Steps

See [06-http-errors.md](./06-http-errors.md) for error model implementation.
