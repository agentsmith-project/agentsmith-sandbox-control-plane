# Connection Management

This document describes the connection management improvements for the MBOS-Sandbox service, including signal handling and cascade disconnection.

## Overview

The connection management system provides:
1. **On-demand shell bridge connections** - Connections are created only when needed
2. **Signal handling** - Clients can send signals (SIGINT, SIGTERM, etc.) to sandbox processes
3. **EOF-based cascade disconnection** - When the shell process exits, connections are properly cleaned up

## Architecture

### Components

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────────┐
│   Client    │────▶│ WebSocket Handler│────▶│Connection Manager│
└─────────────┘     └──────────────────┘     └─────────────────┘
                           │                          │
                           ▼                          ▼
                    ┌──────────┐              ┌─────────────┐
                    │  Buffer  │              │Shell Bridge │
                    │ Manager  │              │   Client    │
                    └──────────┘              └─────────────┘
                                                        │
                                                        ▼
                                               ┌─────────────────┐
                                               │Shell Bridge Pod │
                                               │(stdout/stderr)  │
                                               └─────────────────┘
```

### Key Packages

- **`internal/websocket`** - WebSocket message handling and routing
- **`internal/connection`** - Connection lifecycle management
- **`internal/shellbridge`** - Shell bridge client protocol
- **`internal/sandbox`** - Sandbox state management
- **`internal/buffer`** - Message buffering for reconnection

## Signal Handling

### Signal Message Format

Clients can send signals to sandbox processes via WebSocket:

```json
{
  "type": "signal",
  "data": {
    "sandbox_id": "sandbox-123",
    "signal": "SIGTERM"
  }
}
```

### Supported Signals

- `SIGINT` (2) - Interrupt from keyboard
- `SIGTERM` (15) - Termination signal
- `SIGKILL` (9) - Kill signal (cannot be caught)
- `SIGHUP` (1) - Hangup detected on controlling terminal
- `SIGUSR1` (10) - User-defined signal 1
- `SIGUSR2` (12) - User-defined signal 2

### Signal Flow

1. Client sends signal message via WebSocket
2. `handleSignal()` validates the payload
3. Connection manager ensures shell bridge connection exists
4. Signal is forwarded to shell bridge via `SendSignal()`
5. Shell bridge delivers signal to the shell process

### Example Usage

```javascript
// Send SIGTERM to sandbox process
const signalMessage = {
  type: "signal",
  data: {
    sandbox_id: "my-sandbox",
    signal: "SIGTERM"
  }
};
websocket.send(JSON.stringify(signalMessage));
```

## EOF-based Cascade Disconnection

### Overview

When the shell process exits (normally or via signal), the shell bridge sends an EOF frame (DataTypeClose: 0x04). This triggers a cascade disconnection:

1. Shell bridge sends EOF frame
2. WebSocket handler receives EOF
3. Exit message is sent to client
4. WebSocket connection closes
5. Connection manager cleans up shell bridge connection

### Binary Frame Types

```
0x01 - DataTypeStdout  - Standard output
0x02 - DataTypeStderr  - Standard error
0x03 - DataTypeResize  - Terminal resize
0x04 - DataTypeClose   - EOF/Close (triggers cascade)
```

### Cascade Disconnection Flow

```
Shell Process Exits
        │
        ▼
Shell Bridge sends EOF (0x04)
        │
        ▼
WebSocket Handler: ReceiveOutput()
        │
        ▼
Send exit message to client
        │
        ▼
Close WebSocket connection
        │
        ▼
Connection Manager: HandleBridgeClose()
        │
        ▼
Clean up shell bridge connection
```

## Connection Manager

### EnsureConnection

The `EnsureConnection()` method manages shell bridge connections:

```go
client, err := connMgr.EnsureConnection(ctx, sandboxID)
```

**Behavior:**
- Returns existing connection if available
- Creates new connection if needed
- Returns error if sandbox not found
- Returns error if PodIP not set

### HandleBridgeClose

The `HandleBridgeClose()` method cleans up closed connections:

```go
connMgr.HandleBridgeClose(sandboxID)
```

**Behavior:**
- Closes the shell bridge connection if exists
- Clears the connection reference
- Logs warnings on errors but doesn't fail

### OnClose Callback

Each shell bridge client has an OnClose callback:

```go
client.OnClose(func() {
    connMgr.HandleBridgeClose(sandboxID)
})
```

This ensures automatic cleanup when the connection closes.

## WebSocket Message Types

| Type | Direction | Description |
|------|-----------|-------------|
| `create` | Client→Server | Create or attach to sandbox |
| `stdin` | Client→Server | Send input to shell |
| `signal` | Client→Server | Send signal to shell process |
| `stdout` | Server→Client | Output from shell |
| `stderr` | Server→Client | Error output from shell |
| `status` | Server→Client | Status updates |
| `exit` | Server→Client | Shell process exited |
| `error` | Server→Client | Error message |

## Error Handling

### Connection Errors

- **Sandbox not found** - Returned when trying to connect to non-existent sandbox
- **No PodIP** - Returned when sandbox pod doesn't have an IP yet
- **Connection failed** - Returned when shell bridge is unreachable

### Signal Errors

- **Invalid payload** - Returned when sandbox_id or signal is missing
- **Connection not ready** - Returned when shell bridge is not connected
- **Send failed** - Returned when signal delivery fails

## Testing

### Unit Tests

```bash
# Test connection manager
go test ./internal/connection -v

# Test shell bridge client
go test ./internal/shellbridge -v

# Test websocket handler
go test ./internal/websocket -v
```

### Integration Tests

```bash
# Run integration tests (requires cluster)
go test ./integration -v
```

### Test Coverage

- Connection lifecycle (create, reuse, close)
- Signal payload parsing
- Signal forwarding to shell bridge
- EOF-based cascade disconnection
- Concurrent connection handling
- Error scenarios

## Configuration

No additional configuration is required for connection management. The system uses existing configuration:

- `podNamespace` - Where sandbox pods are created
- `shellbridge.DefaultPort` (8080) - Shell bridge WebSocket port
- WebSocket timeouts - From `config.WebSocket`

## Migration from Old System

### Before

Connections were created directly in the WebSocket handler and not reused:

```go
client := shellbridge.NewClient(sess.PodIP, shellbridge.DefaultPort)
client.Connect(ctx)
// Use client
defer client.Close() // Always closed, even if reconnection
```

### After

Connections are managed by the connection manager:

```go
client, err := h.connectionManager.EnsureConnection(ctx, sess.SandboxID)
// Use client
// Don't close - manager handles lifecycle
```

### Benefits

1. **Connection reuse** - Multiple WebSocket connections to the same sandbox share the shell bridge connection
2. **Automatic cleanup** - OnClose callback ensures cleanup
3. **Better error handling** - Centralized error handling in manager
4. **Testability** - Mockable interface for testing

## Performance Considerations

### Connection Pooling

- Connections are reused across WebSocket connections
- Reduces overhead for reconnection scenarios
- Limits concurrent connections to shell bridge pods

### Buffer Management

- Messages are buffered while client is disconnected
- Buffered messages are sent on reconnection
- Prevents data loss during network issues

### Timeout Handling

- Shell bridge client has configurable read timeout
- WebSocket has configurable ping interval
- Proper timeout prevents resource leaks

## Security Considerations

### Signal Validation

- Only whitelisted signals are supported
- Payload validation prevents injection
- Sandbox must exist and be ready

### Connection Limits

- Each sandbox has at most one shell bridge connection
- Prevents resource exhaustion
- Ensures predictable behavior

## Future Enhancements

1. **Connection pooling** - Pool of connections per sandbox
2. **Metrics** - Track connection lifecycle metrics
3. **Health checks** - Periodic connection health verification
4. **Signal history** - Track signals sent to each sandbox
5. **Reconnection backoff** - Exponential backoff for failed connections

## References

- [Shell Bridge Protocol](../shellbridge/README.md)
- [WebSocket API](./websocket.md)
- [Sandbox Lifecycle](./sandbox-lifecycle.md)
