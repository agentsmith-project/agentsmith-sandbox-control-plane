# AFSCP Workload Mount Plan Model

This document defines the supported persistence model for `mbos-sandbox-v1`.

## Product Truth

- AgentSmith creates an AFSCP workload mount binding for a workspace/project task.
- Sandbox Manager consumes the AFSCP orchestrator mount plan for that binding.
- Workload pods mount the sandbox-managed PVC at the AFSCP plan `mount_path`.
- AFSCP is the authority for payload subdir, read-only mode, Secret reference, and mount security policy.
- Pod lifetime is ephemeral; AFSCP binding lifetime is closed through heartbeat/release/status.

## Platform Boundary

`mbos-sandbox-v1` owns:

- fetching AFSCP orchestrator mount plans with the sandbox orchestrator identity
- PV/PVC materialization from plan fields
- workload pod lifecycle
- AFSCP heartbeat on workload keepalive
- AFSCP release before workload pod deletion, then `released` status only after the pod is confirmed gone

AgentSmith owns:

- product authorization
- creating the AFSCP workload mount binding
- passing `namespace_id` and `mount_binding_id` to sandbox
- starting the process inside the workload pod through `/exec`

Sandbox Manager does not own storage backend endpoints, metadata connections, or storage auth material.

## Binding Shape

A workspace binding request carries:

- `namespace_id`
- `mount_binding_id`

Sandbox fetches AFSCP `OrchestratorMountPlan` and stores only the runtime facts needed by Kubernetes:

- PV CSI `subdir = payload_volume_subdir`
- PV CSI `NodePublishSecretRef = secret_ref`
- PVC annotations for `mount_path`, `read_only`, `namespace_id`, `mount_binding_id`, `volume_id`, and security policy

The sandbox API response is sanitized and does not expose `secret_ref` or `payload_volume_subdir`.

## Workload Shape

Workload creation carries:

- image, command, env, resource requests/limits
- `workspace_binding_id`
- timeout settings

The caller does not pass `mount_path`, `sub_path`, or `working_dir`. Sandbox derives:

- `TASK_HOME=<mount_path>`
- `HOME=<mount_path>`
- `WORKSPACE_PATH=<mount_path>/workspace`
- container `workingDir=<mount_path>/workspace`

Writable plans run a workload init container that prepares `workspace` and
`.artifacts`. Read-only plans mount the PVC read-only and skip that init so the
pod does not block on writes to an intentionally read-only mount.

## Release Checks

Before release, verify:

1. `PUT /workspace-bindings/{bindingId}` fetches AFSCP plan and creates or reuses PV/PVC.
2. PV/PVC use only AFSCP plan payload subdir, Secret ref, mount path, read-only flag, and security policy.
3. `PUT /workloads/{wlId}` mounts the binding PVC at the plan mount path.
4. `POST /workloads/{wlId}/keepalive` calls AFSCP heartbeat.
5. `DELETE /workloads/{wlId}` calls AFSCP release with a stable idempotency key, deletes the pod, confirms it is gone, then reports `released`; failed pod deletion leaves the pod present for retry and does not write terminal status.
6. docs, tests, and operational runbooks all describe this same model.
