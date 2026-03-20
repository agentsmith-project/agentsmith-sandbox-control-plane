# JuiceFS CSI Workspace Model

This document defines the only supported persistence model for `mbos-sandbox-v1`.

## Product Truth

- A workspace file library is the long-lived runtime environment
- Sandbox binds that environment through JuiceFS CSI
- Workload pods mount the bound environment at `/workspace`
- Pod lifetime is ephemeral
- Workspace lifetime is independent from pod lifetime

## Platform Boundary

`mbos-sandbox-v1` owns:

- workspace binding lifecycle
- JuiceFS Secret / PV / PVC resources
- workload pod lifecycle
- `/workspace` mount delivery

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
- `mount_path=/workspace`
- optional `subdir`

The caller should treat CSI details as implementation detail. The stable contract is the binding identifier.

## Recommended CSI Model

Based on the JuiceFS CSI documentation, the current model uses:

- JuiceFS CSI Driver
- static provisioning
- stable PV/PVC per workspace binding
- shared mount reuse through CSI mount pods

This matches the AgentSmith requirement that one file library can be reused by multiple tasks while remaining a single persistent environment.

## Storage Shape

Inside the mounted file library root, AgentSmith is expected to organize task runtime state using task namespaces such as:

- `.codex/tasks/<taskId>/`
- `.mbos/tasks/<taskId>/`
- `.artifacts/tasks/<taskId>/`

The mounted root remains the complete environment; task isolation happens inside the file library, not by allocating a separate volume per task.

## Release Checks

Before release, verify:

1. `PUT /workspace-bindings/{bindingId}` creates or reuses a stable binding
2. `PUT /workloads/{wlId}` with that binding mounts `/workspace`
3. deleting a workload leaves workspace contents intact
4. the same binding can be reused by later workloads
5. docs, tests, and operational runbooks all describe this same model
