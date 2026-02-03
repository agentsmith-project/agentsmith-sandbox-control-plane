# Task D: Reliable Exec Exit Codes

**Status:** ✅ Completed

## Summary

Implemented reliable exit code capture using a marker pattern with tail buffer preservation to ensure exit codes are never lost, even with truncated output.

## Implementation

### Files Created

- `internal/exec/wrapper.go` - Shell command wrapper
- `internal/exec/output.go` - Output handling with tail buffer
- `internal/exec/marker.go` - Exit code marker parsing
- `internal/k8s/exec.go` - K8s SPDY executor wrapper

### Key Features

1. **Exit Code Marker Pattern**
   - Marker: `__SBX_EXIT_CODE__=<n>`
   - Written to stderr (configurable)
   - Emitted after user command completes

2. **Command Wrapper**
   - `sh -lc` for shell execution
   - Sets environment variables first
   - Changes to workdir
   - Executes user command
   - Outputs exit code marker

3. **Tail Buffer Preservation**
   - Keeps last N bytes of output
   - Ensures marker is never truncated
   - `preserveTailBytes <= min(stdoutMaxBytes, stderrMaxBytes)`

4. **Special Handling for String Arguments**
   - Commands like `python -c "..."` need double quotes
   - Preserves variable expansion with `$VAR`
   - Properly escapes for shell safety

### Command Structure

```bash
sh -lc 'export ENV1=val1 && export ENV2=val2 && cd /workspace && <user-cmd>; echo "__SBX_EXIT_CODE__$?" >&2'
```

### Configuration

```yaml
exec:
  defaultTimeout: 30s
  maxTimeout: 300s
  stdoutMaxBytes: 1048576    # 1MB
  stderrMaxBytes: 1048576    # 1MB
  preserveTailBytes: 4096    # Must be <= maxBytes
  exitCodeMarker:
    key: "__SBX_EXIT_CODE__"
    stream: "stderr"
  shell:
    bin: sh
    args: ["-lc"]
  env:
    allowRegex: "^[A-Z_][A-Z0-9_]*$"
  workdir:
    allowedPrefixes: ["/workspace"]
```

### Output Handling

**TailBufferWriter:**
- Keeps only the last N bytes
- Ensures marker is never lost
- Tracks total bytes written
- Reports if output was truncated

**LimitWriter:**
- Limits total output size
- Discards excess bytes
- Preserves data up to limit

### Example Execution

**Request:**
```json
{
  "cmd": ["python", "-c", "import sys; print('hello'); sys.exit(7)"],
  "env": {"TEST_VAR": "value"},
  "workdir": "/workspace"
}
```

**Wrapped Command:**
```bash
sh -lc 'export TEST_VAR=value && cd /workspace && python -c "import sys; print('\''hello'\''); sys.exit(7)"; echo "__SBX_EXIT_CODE__$?" >&2'
```

**Response:**
```json
{
  "exitCode": 7,
  "stdout": "hello\n",
  "stderr": "",
  "durationMs": 123
}
```

## Validation

### Acceptance Criteria

- [x] `exit 7` returns `exitCode=7`
- [x] Large output still returns correct exit code
- [x] Marker does not appear in user output
- [x] Environment variables are properly set
- [x] Workdir changes work correctly

### Test Cases

1. **Simple Exit Code**
   - Command: `exit 42`
   - Result: `exitCode=42`

2. **Large Output with Exit Code**
   - Command: generate 10MB output then `exit 5`
   - Result: Output truncated, `exitCode=5`

3. **Environment Variables**
   - Command: `echo $MY_VAR`
   - Env: `{"MY_VAR": "test"}`
   - Result: `stdout: "test\n"`

4. **Special Characters**
   - Command: `echo "test's \"quoted\""`
   - Result: `stdout: "test's \"quoted\"\n"`

## Next Steps

See [05-files-targz.md](./05-files-targz.md) for file protocol unification.
