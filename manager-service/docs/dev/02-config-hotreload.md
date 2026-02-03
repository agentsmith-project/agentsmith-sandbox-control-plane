# Task B: ConfigMap Hot Reload

**Status:** ✅ Completed

## Summary

Implemented ConfigMap-based configuration hot reload using fsnotify with debouncing, throttling, hash-based deduplication, and failure backoff.

## Implementation

### Files Created

- `internal/config/watch.go` - Configuration file watching and hot reload

### Key Features

1. **File System Watching**
   - Uses `github.com/fsnotify/fsnotify` for file system events
   - Handles ConfigMap symlink/rename updates correctly
   - Watches directory (not just file) to catch symlink changes

2. **Debouncing**
   - Default: 300ms debounce delay
   - Configurable via `CONFIG_RELOAD_DEBOUNCE` env var
   - Multiple file events within debounce window are merged

3. **Throttling**
   - Default: 1s minimum interval between reloads
   - Configurable via `CONFIG_RELOAD_MIN_INTERVAL` env var
   - Prevents rapid successive reload attempts

4. **Hash Gate**
   - SHA256 hash of config content
   - Skips reload if content hasn't changed
   - Prevents unnecessary reloads

5. **Failure Backoff**
   - Exponential backoff: 1s, 2s, 4s, 8s, ... max 30s
   - Configurable max backoff via `CONFIG_RELOAD_BACKOFF_MAX` env var
   - Resets on successful reload
   - Continues using last known good config on failure

6. **Strict Mode (Optional)**
   - Controlled by `STRICT_CONFIG_RELOAD` env var
   - When enabled, service returns 503 on reload failure
   - Default: false (continue with old config)

### Boot Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `CONFIG_PATH` | `/etc/sandbox-manager/manager-config.yaml` | Path to config file |
| `CONFIG_RELOAD_DEBOUNCE` | `300ms` | Debounce delay |
| `CONFIG_RELOAD_MIN_INTERVAL` | `1s` | Minimum interval between reloads |
| `CONFIG_RELOAD_BACKOFF_MAX` | `30s` | Maximum backoff time |
| `STRICT_CONFIG_RELOAD` | `false` | Enable strict mode |

### Reload Statistics

The watcher tracks:
- `totalAttempts` - Total reload attempts
- `successCount` - Successful reloads
- `failureCount` - Failed reloads
- `lastSuccess` - Last successful reload event
- `lastFailure` - Last failed reload event
- `lastReloadAt` - Timestamp of last reload

## Validation

### Acceptance Criteria

- [x] Modifying ConfigMap takes effect without restart
- [x] `/debug/config` shows hash changes after reload
- [x] Invalid config does not overwrite good config
- [x] Backoff mechanism works correctly
- [x] Multiple events are debounced properly

### Test Scenarios

1. **Valid Config Update**
   - Edit ConfigMap → pod detects change → reload succeeds → new hash shown

2. **Invalid Config Update**
   - Edit ConfigMap with invalid YAML → reload fails → old config continues
   - `lastError` field populated in `/debug/config`

3. **Rapid Updates**
   - Multiple ConfigMap edits → debounced to single reload
   - Hash check prevents reload if content identical

4. **Backoff Test**
   - Repeated invalid updates → backoff increases (1s → 2s → 4s → ...)
   - Fixed update → backoff resets

## Next Steps

See [03-auth-servicekey.md](./03-auth-servicekey.md) for Service Key authentication implementation.
