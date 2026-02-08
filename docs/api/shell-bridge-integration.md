# Shell-Bridge Integration API Reference

This document describes the shell-bridge integration for the MBOS sandbox system.

## Overview

Shell-bridge is a WebSocket-based PTY shell server that runs inside sandbox pods, providing persistent shell sessions without requiring long-lived connections from the manager service.

## Architecture

```
Client → Manager Service → Shell-Bridge (in Pod) → Shell
         (WebSocket)        (WebSocket)          (PTY)
```

### Benefits

- **No long-lived connections**: Manager doesn't need to maintain K8s exec connections
- **Pod survivability**: Pods can be reclaimed even if manager crashes
- **Persistent sessions**: Shell state (environment, working directory) persists across reconnects
- **Streaming I/O**: Real-time bidirectional streaming of stdin/stdout/stderr

## WebSocket Protocol

### Connection URL

```
ws://<pod-ip>:8080/ws
```

### Message Format

Shell-bridge uses a hybrid protocol:

**Text Messages (JSON):**
- `{"type": "exec", "shell": "bash", "command": "ls -la", "env": []}` - Execute command
- `{"type": "exit", "code": 0}` - Command exited
- `{"type": "error", "message": "..."}` - Error occurred

**Binary Messages:**
- Format: `[Type:1][Length:4][Data:N]` (Big Endian)
- Types:
  - `0x01` = stdout
  - `0x02` = stderr
  - `0x03` = resize
  - `0x04` = close

## Configuration

### Pod Spec

When creating a pod, set the `ShellType` field to enable shell-bridge:

```go
podSpec := &k8s.PodSpec{
    ShellType: "bash",  // or "zsh", "sh", "fish", "nu"
    Workdir:   "/workspace",
    // ... other fields
}
```

### Shell Types

| Shell | Value | Description |
|-------|-------|-------------|
| Bash | `bash` | Default, full-featured |
| Zsh | `zsh` | Modern, powerful |
| POSIX sh | `sh` | Minimal, compatible |
| Fish | `fish` | User-friendly |
| Nushell | `nu` | Structured data |

## Client Library

### Go Client

```go
import "github.com/sandbox/manager/internal/shellbridge"

// Create client
client := shellbridge.NewClient(podIP, 8080)

// Connect
if err := client.Connect(ctx); err != nil {
    log.Fatal(err)
}
defer client.Close()

// Execute command
if err := client.ExecCommand(ctx, "bash", "ls -la", nil); err != nil {
    log.Fatal(err)
}

// Receive output
for {
    output, err := client.ReceiveOutput(ctx)
    if err == io.EOF {
        break
    }
    if err != nil {
        log.Fatal(err)
    }

    if output.Type == byte(shellbridge.DataTypeStdout) {
        fmt.Print(string(output.Data))
    }
}
```

## Docker Image

The runner image includes shell-bridge:

```dockerfile
# Build stage for shell-bridge
FROM golang:1.21-alpine AS shellbridge
WORKDIR /src
COPY shell-bridge/ .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /shellb ./cmd/shellb

# Runner image
FROM ubuntu:22.04 AS final
COPY --from=shellbridge /shellb /usr/local/bin/shellb

CMD ["/usr/local/bin/shellb", "--shell=bash", "--port=8080", "--workdir=/workspace"]
```

## Migration from Tmux

The old tmux-based approach is still supported for backward compatibility. To migrate:

1. Set `ShellType` on pod spec instead of `Command`
2. Use WebSocket client instead of K8s exec
3. Commands execute in shell-bridge PTY instead of tmux session

### Before (Tmux)

```go
podSpec.Command = "some command"  // Runs via tmux wrapper
```

### After (Shell-Bridge)

```go
podSpec.ShellType = "bash"  // Runs shell-bridge
// Commands sent via WebSocket
```

## Troubleshooting

### Connection Refused

Ensure shell-bridge is running in the pod:
```bash
kubectl exec -it <pod-name> -- ps aux | grep shellb
```

### Pod Has No IP

Wait for pod to be running:
```bash
kubectl wait --for=condition=Ready pod/<pod-name>
```

### Binary Frame Parse Errors

Check frame format matches `[Type:1][Length:4][Data:N]`.
