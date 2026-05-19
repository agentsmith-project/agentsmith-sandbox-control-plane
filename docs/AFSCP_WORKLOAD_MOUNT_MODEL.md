# AFSCP Workload Mount Plan Model

This document defines how ASBCP consumes AFSCP workload mount plans.

## Product Truth

- AgentSmith creates an AFSCP workload mount binding for a workspace/project task.
- ASBCP consumes the AFSCP orchestrator mount plan for that binding.
- Workload Pods mount ASBCP-managed Kubernetes resources at the AFSCP plan `mount_path`.
- AFSCP is the authority for payload subdirectory, read-only mode, secret reference, and mount security policy.
- Pod lifetime is ephemeral; AFSCP binding lifetime is closed through heartbeat, release, and status operations.

## Platform Boundary

ASBCP owns:

- Fetching AFSCP orchestrator mount plans with the ASBCP service identity.
- PV/PVC materialization from plan fields.
- Workload Pod lifecycle.
- AFSCP heartbeat on workload keepalive.
- AFSCP release before workload Pod deletion and terminal release status after deletion is confirmed.

AgentSmith owns:

- Product authorization.
- Creating the AFSCP workload mount binding.
- Passing `namespace_id` and `mount_binding_id` to ASBCP.
- Starting the process inside the workload Pod through `/exec`.

ASBCP does not own storage backend endpoints, metadata connections, storage auth material, filesystem recovery, or AFSCP storage policy.

## Binding Shape

A workspace binding request carries:

- `namespace_id`
- `mount_binding_id`

ASBCP fetches the AFSCP plan and stores only runtime facts needed by Kubernetes. The ASBCP API response is sanitized and does not expose storage secret references or payload subdirectories.

## Workload Shape

Workload creation carries:

- Image, command, env, and resource requests/limits selected by AgentSmith.
- `workspace_binding_id`.
- Timeout settings.

The caller does not pass `mount_path`, `sub_path`, or `working_dir`. ASBCP derives task home, workspace path, and container working directory from the AFSCP plan.

## Release Checks

Before release, verify:

1. Workspace binding ensure fetches the AFSCP plan and creates or reuses Kubernetes binding resources.
2. Binding resources use only AFSCP plan fields for filesystem placement.
3. Workload creation mounts the binding at the plan mount path.
4. Keepalive calls AFSCP heartbeat.
5. Workload deletion calls AFSCP release, deletes the Pod, and reports terminal release only after deletion is confirmed.
6. Docs, tests, and operational runbooks describe this same model.
