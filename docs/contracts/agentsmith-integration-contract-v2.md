# AgentSmith ↔ Sandbox Manager Integration Contract v2

This document defines how AgentSmith interacts with the Sandbox Manager API to provision and operate agent workload pods.

## Terminology

| Term | Definition |
|------|-----------|
| **Sandbox Manager** | The `manager-service` REST API documented in [api-reference-v2.md](../api-reference-v2.md) |
| **Workload pod** | A Kubernetes pod created by the Sandbox Manager, running a container image chosen by AgentSmith |
| **Workspace** | A persistent directory at `/workspace` inside the pod, backed by a JuiceFS PVC mount |
| **Agent process** | The process AgentSmith starts inside the workload pod via `/exec` |

## Authentication

AgentSmith authenticates to the Sandbox Manager using the `X-Service-Key` header. The key is provisioned out-of-band and stored in AgentSmith's configuration (e.g. `SANDBOX_SERVICE_KEY` environment variable).

```
X-Service-Key: <AgentSmith's service key>
```

## Endpoint Base

All workload operations use the path pattern:

```
/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

AgentSmith maps its own domain identifiers to these path parameters:

| Path Parameter | AgentSmith Source |
|----------------|-------------------|
| `wsId` | Workspace ID from the user's workspace |
| `projId` | Project ID from the user's project |
| `wlId` | Agent thread ID or workload ID |

---

## 1. Creating a Sandbox Pod

AgentSmith creates a workload pod when an agent session begins.

**Request:**

```
PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

```json
{
  "image": "registry.example.com/agent-runner:latest",
  "env": {
    "AGENT_ID": "agent-001",
    "THREAD_ID": "thread-abc",
    "LOG_LEVEL": "info"
  },
  "cpu_request": "500m",
  "cpu_limit": "2",
  "memory_request": "512Mi",
  "memory_limit": "4Gi",
  "idle_timeout_sec": 1800,
  "max_lifetime_sec": 86400
}
```

**Key behaviors:**

- **No `command` field** — the pod starts with the default keep-alive (`tail -f /dev/null`). AgentSmith controls what runs via `/exec`.
- **Idempotent** — if the pod already exists, the manager returns `200 OK` with the existing pod's status. AgentSmith can safely retry on transient failures.
- **Persistent workspace reuse** — the pod remounts the same workspace path for the same `{wsId}/{wlId}` lifecycle shape. The agent process sees the existing `/workspace` state because persistence comes from the mounted JuiceFS/PVC directory, not from snapshot restore.

**AgentSmith handling:**

```
response = PUT /v1/.../workloads/{wlId}
if response.status == 201:
    # New pod created — proceed to start agent
elif response.status == 200:
    # Pod already exists — check if agent process is running (see §3)
else:
    # Error — retry with backoff or surface to user
```

---

## 2. Starting an Agent Process

After the pod is created and running, AgentSmith starts the agent process via `/exec`.

**Request:**

```
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec
```

```json
{
  "cmd": [
    "bash", "-c",
    "nohup /usr/local/bin/agent-process --config /workspace/.agent/config.yaml > /workspace/.agent/agent.log 2>&1 & echo $!"
  ],
  "timeout_seconds": 10
}
```

**Key pattern:** The agent process is launched in the background with `nohup ... &`. The command returns the PID via `echo $!`. AgentSmith stores this PID for health monitoring.

**AgentSmith handling:**

```
response = POST /v1/.../workloads/{wlId}/exec
if response.exit_code == 0:
    agent_pid = response.stdout.strip()
    # Store PID for health checks
else:
    # Agent failed to start — check stderr, retry or escalate
```

---

## 3. Monitoring Agent Health

AgentSmith periodically checks whether the agent process is still running inside the pod.

**Health check via pgrep:**

```
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec
```

```json
{
  "cmd": ["pgrep", "-f", "agent-process"],
  "timeout_seconds": 5
}
```

**Interpreting the result:**

| `exit_code` | Meaning |
|-------------|---------|
| `0` | Agent process is running. `stdout` contains PID(s). |
| `1` | No matching process found — agent has exited. |
| `-1` | Exec infrastructure error — pod may be unreachable. |

**Pod-level health check:**

AgentSmith also checks the pod's phase to detect pod-level failures:

```
GET /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

| `phase` | AgentSmith Action |
|---------|-------------------|
| `Running` | Pod is healthy. Check agent process separately. |
| `Pending` | Pod is starting. Wait and retry. |
| `Failed` | Pod crashed. Delete and recreate. |
| `offline` | Pod does not exist. Create a new one. |

**Recommended polling interval:** Every 30–60 seconds for agent process health, combined with a `/keepalive` call to keep the pod alive.

---

## 4. Extending Pod TTL

AgentSmith calls `/keepalive` to keep the pod alive while the agent is active. This should be called on every meaningful interaction (user message, tool execution, etc.) or on a periodic heartbeat.

```
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive
```

Response:

```json
{
  "expires_at": "2026-02-28T11:00:00Z"
}
```

**Best practice:** Combine `/keepalive` with the health check loop. Every health check cycle that confirms the agent is alive should also send keepalive.

---

## 5. Handling Agent Crash Recovery

When AgentSmith detects that the agent process has exited (via pgrep returning exit code 1), it follows this recovery sequence:

```
1. Check exit reason
   POST /exec {"cmd": ["cat", "/workspace/.agent/agent.log"]}
   → Read last N lines to determine crash cause

2. Decide whether to restart
   - Transient error → restart agent process (go to §2)
   - Permanent error → surface error to user, optionally delete pod

3. Restart agent
   POST /exec {"cmd": ["bash", "-c", "nohup /usr/local/bin/agent-process ... &"]}

4. Keepalive to extend TTL
   POST /keepalive
```

**Crash recovery is AgentSmith's responsibility.** The Sandbox Manager does not monitor or restart processes inside pods. It only manages the workload pod lifecycle and mounted workspace availability.

---

## 6. Tearing Down a Sandbox

When the agent session ends (user closes session, agent completes task, or explicit cleanup):

```
DELETE /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

Response:

```json
{
  "message": "pod deleted"
}
```

The next time a pod is created for the same `{wsId}/{wlId}`, the manager restores the latest JVS state for that workspace.

---

## Lifecycle Sequence Diagram

```
AgentSmith                      Sandbox Manager                 Kubernetes
    │                                │                               │
    │  PUT /workloads/{wlId}         │                               │
    │  {image, env, timeouts}        │                               │
    │───────────────────────────────▶│                               │
    │                                │  Restore JVS snapshot         │
    │                                │  (if exists)                  │
    │                                │                               │
    │                                │  Create Pod                   │
    │                                │──────────────────────────────▶│
    │                                │                               │
    │                                │  Wait for Ready               │
    │                                │◀ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ │
    │                                │                               │
    │  201 {pod_name, phase, ip}     │                               │
    │◀───────────────────────────────│                               │
    │                                │                               │
    │  POST /exec                    │                               │
    │  {cmd: [nohup agent ... &]}    │                               │
    │───────────────────────────────▶│  SPDY exec                   │
    │                                │──────────────────────────────▶│
    │                                │◀──────────────────────────────│
    │  200 {exit_code: 0, stdout: PID}                               │
    │◀───────────────────────────────│                               │
    │                                │                               │
    │         ┌──── Health check + keepalive loop ────┐              │
    │         │                                    │                  │
    │  POST /exec {cmd: [pgrep ...]} │             │                  │
    │───────────────────────────────▶│             │                  │
    │  200 {exit_code: 0}            │             │                  │
    │◀───────────────────────────────│             │                  │
    │                                │             │                  │
    │  POST /keepalive               │             │                  │
    │───────────────────────────────▶│  Patch pod annotations        │
    │  200 {expires_at}              │──────────────────────────────▶│
    │◀───────────────────────────────│             │                  │
    │         │                                    │                  │
    │         └────────────── (repeat) ────────────┘                  │
    │                                │                               │
    │  ── Agent crashes ──           │                               │
    │                                │                               │
    │  POST /exec {cmd: [pgrep ...]} │                               │
    │───────────────────────────────▶│                               │
    │  200 {exit_code: 1}            │                               │
    │◀───────────────────────────────│                               │
    │                                │                               │
    │  POST /exec {cmd: [cat log]}   │                               │
    │───────────────────────────────▶│                               │
    │  200 {stdout: <log contents>}  │                               │
    │◀───────────────────────────────│                               │
    │                                │                               │
    │  POST /exec                    │                               │
    │  {cmd: [nohup agent ... &]}    │    (restart agent)            │
    │───────────────────────────────▶│──────────────────────────────▶│
    │  200 {exit_code: 0}            │                               │
    │◀───────────────────────────────│                               │
    │                                │                               │
    │  ── Session ends ──            │                               │
    │                                │                               │
    │  DELETE /workloads/{wlId}      │                               │
    │───────────────────────────────▶│  Delete Pod                   │
    │                                │──────────────────────────────▶│
    │                                │                               │
    │                                │  Wait for termination         │
    │                                │◀ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ │
    │                                │                               │
    │                                │  Cleaner deletes pod only      │
    │                                │                               │
    │  200 {message}                 │                               │
    │◀───────────────────────────────│                               │
    │                                │                               │
```

---

## Summary of AgentSmith Responsibilities

| Concern | Owner |
|---------|-------|
| Pod lifecycle (create, delete, TTL) | Sandbox Manager |
| Workspace persistence (JVS prepare/restore) | Sandbox Manager |
| TTL enforcement for idle pods | Sandbox Manager (cleaner CronJob) |
| What runs inside the pod | AgentSmith |
| Starting/stopping agent processes | AgentSmith (via `/exec`) |
| Agent health monitoring | AgentSmith (via `/exec` + `pgrep`) |
| Agent crash recovery | AgentSmith |
| Keeping pods alive (`/keepalive`) | AgentSmith |
