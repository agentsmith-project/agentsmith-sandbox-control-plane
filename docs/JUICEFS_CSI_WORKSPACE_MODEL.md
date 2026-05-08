# JuiceFS CSI Workspace Model

This document defines the only supported persistence model for `mbos-sandbox-v1`.

## Product Truth

- A workspace file library is the long-lived runtime environment
- Sandbox binds that environment through JuiceFS CSI
- Workload pods mount an explicit PVC `sub_path` at the requested container `mount_path`
- Pod lifetime is ephemeral
- Workspace lifetime is independent from pod lifetime

## Platform Boundary

`mbos-sandbox-v1` owns:

- workspace binding lifecycle
- JuiceFS Secret / PV / PVC resources
- workload pod lifecycle
- workload mount delivery from `workspace_binding_id` + `mount_path` + `sub_path` + `working_dir`

`agentsmith` owns:

- which file library should be used
- when to ensure a binding
- task orchestration inside the mounted workspace
- task namespace layout under the file library root

## Binding Shape

A `workspace binding` is the platform object that connects a business file library to a concrete JuiceFS CSI mount.

At minimum it carries:

- `binding_id`
- `workspace_id`
- `project_id`
- `file_library_id`
- `status`
- `pvc_name`
- `mount_path=/workspace` as the binding's legacy/recommended mount hint
- optional `subdir`

The caller should treat CSI details as implementation detail. The stable contract is the binding identifier.

Workload creation has its own path contract. Agent task workloads should pass:

- `mount_path=/home/<task_home_segment>`
- `sub_path=agent-tasks/<task_home_segment>`
- `working_dir=/home/<task_home_segment>/workspace`

## Recommended CSI Model

Based on the JuiceFS CSI documentation, the current model uses:

- JuiceFS CSI Driver
- static provisioning
- stable PV/PVC per workspace binding
- shared mount reuse through CSI mount pods

This matches the AgentSmith requirement that one file library can be reused by multiple tasks while remaining a single persistent environment.

## Storage Shape

Inside the file library PVC, AgentSmith is expected to organize task runtime state using task namespaces such as:

- `agent-tasks/<task_home_segment>/workspace/`
- `agent-tasks/<task_home_segment>/.codex/`
- `agent-tasks/<task_home_segment>/.mbos/`
- `agent-tasks/<task_home_segment>/.agents/`

The mounted task subtree is the container's task HOME. Task isolation happens with PVC subPath, not by allocating a separate volume per task.

## Release Checks

Before release, verify:

1. `PUT /workspace-bindings/{bindingId}` creates or reuses a stable binding
2. `PUT /workloads/{wlId}` with that binding mounts `agent-tasks/<task_home_segment>` at `/home/<task_home_segment>`
3. deleting a workload leaves workspace contents intact
4. the same binding can be reused by later workloads
5. docs, tests, and operational runbooks all describe this same model
