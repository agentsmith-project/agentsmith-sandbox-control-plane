# AgentSmith ↔ Sandbox Manager Integration Contract v2

This document defines the current integration contract between AgentSmith and `mbos-sandbox-v1`.

## Product Boundary

AgentSmith decides:

- which workspace / project / file library should be used
- when a workspace binding should exist
- what commands should run inside the task workspace directory

Sandbox Manager decides:

- how the workspace binding maps to JuiceFS CSI resources
- how the requested PVC `sub_path` is mounted into a workload pod
- workload pod lifecycle and exec

## Authentication

All `/v1/` routes require:

```
X-Service-Key: <service-key>
```

## Path Model

Binding endpoints:

```
/v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

Workload endpoints:

```
/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

AgentSmith source mapping:

| Path Parameter | AgentSmith Source |
|----------------|-------------------|
| `wsId` | workspace id |
| `projId` | project id |
| `bindingId` | stable file-library-backed binding id |
| `wlId` | workload id |

## 1. Ensure Workspace Binding

AgentSmith ensures a stable binding before creating an internal workload.

**Request**

```http
PUT /v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

```json
{
  "file_library_id": "flib_demo",
  "filesystem_name": "agentsmith-workspace",
  "metadata_url": "postgres://postgres:postgres@db:5432/juicefs?sslmode=disable",
  "storage_endpoint": "http://minio:9000",
  "storage_capacity": "1Pi",
  "storage_class_name": "juicefs-csi-sc",
  "mount_options": ["cache-dir=/var/lib/juicefs/cache","writeback_cache"]
}
```

**Expected behavior**

- idempotent ensure
- stable Secret / PV / PVC reuse
- binding status returned to caller

## 2. Create or Ensure Workload

AgentSmith creates a workload only after it has a valid `workspace_binding_id`.

**Request**

```http
PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

```json
{
  "image": "registry.example.com/agent-runner:latest",
  "env": {
    "AGENT_ID": "agent-001",
    "THREAD_ID": "thread-abc"
  },
  "cpu_request": "500m",
  "cpu_limit": "2",
  "memory_request": "512Mi",
  "memory_limit": "4Gi",
  "idle_timeout_sec": 1800,
  "max_lifetime_sec": 86400,
  "workspace_binding_id": "flib_demo",
  "mount_path": "/home/task-abc",
  "sub_path": "agent-tasks/task-abc",
  "working_dir": "/home/task-abc/workspace"
}
```

**Expected behavior**

- pod is created or reused idempotently
- workload pod mounts `sub_path` from the binding PVC at `mount_path`
- container working directory is `working_dir`
- runtime env includes `TASK_HOME=mount_path`, `HOME=mount_path`, and `WORKSPACE_PATH=working_dir`
- if an existing pod has a different mount path, subPath, workingDir, PVC, or runtime path env, create/ensure returns `409 Conflict`
- if the pod is recreated later with the same binding and subPath, task workspace contents remain available

## 3. Start Agent Process

After the pod is running, AgentSmith starts the actual process using `/exec`.

```http
POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec
```

AgentSmith is responsible for the command it runs. Sandbox Manager only provides the execution channel.

## 4. Health and Keepalive

AgentSmith uses:

- `GET /workloads/{wlId}` for pod status
- `POST /workloads/{wlId}/keepalive` to extend `expires_at`
- `POST /workloads/{wlId}/exec` for process-level checks

## 5. Deletion Semantics

Deleting a workload removes compute only:

```http
DELETE /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}
```

Deleting a workspace binding removes the CSI binding resources:

```http
DELETE /v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId}
```

These are separate lifecycle operations on purpose.

## 6. Release Expectations

Before release:

1. AgentSmith ensures a binding before workload creation
2. Sandbox Manager accepts `workspace_binding_id` plus explicit `mount_path`, `sub_path`, and `working_dir`
3. task HOME and workspace paths remain stable across workload recreation
4. external and internal task directory semantics stay aligned under the same file library model
