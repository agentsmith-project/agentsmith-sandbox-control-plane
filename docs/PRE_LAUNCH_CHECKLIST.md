# Sandbox Pre-Launch Checklist

This checklist is for the current production model only:

- JuiceFS CSI workspace bindings
- workload pods mounted with task HOME subPath semantics
- keepalive-driven reclaim of compute pods

## Must-pass checks

### Binding lifecycle

- [ ] `PUT /workspace-bindings/{bindingId}` creates or reuses a stable binding
- [ ] `GET /workspace-bindings/{bindingId}` returns the current binding state
- [ ] `DELETE /workspace-bindings/{bindingId}` removes the binding resources cleanly
- [ ] ensuring the same binding twice returns the same stable PVC identity

### Workload lifecycle

- [ ] `PUT /workloads/{wlId}` requires `workspace_binding_id`
- [ ] workload pod mounts `sub_path=agent-tasks/<task_home_segment>` at `mount_path=/home/<task_home_segment>`
- [ ] workload container `workingDir` is `/home/<task_home_segment>/workspace`
- [ ] workload env contains `TASK_HOME`, `HOME`, and `WORKSPACE_PATH`
- [ ] pod reaches `Running`
- [ ] `POST /exec` works against the running pod
- [ ] `POST /keepalive` extends `expires_at`
- [ ] `DELETE /workloads/{wlId}` removes only compute

### Persistence

- [ ] files written under the task `WORKSPACE_PATH` survive workload deletion and recreation
- [ ] cleaner deletes expired pods without deleting workspace bindings
- [ ] the same binding can be reused across multiple workloads and tasks

### Configuration

- [ ] manager is configured through `JUICEFS_CSI_DRIVER`, storage capacity, storage class, mount options, and related CSI envs
- [ ] all deployment paths use the current JuiceFS CSI binding env/config model
- [ ] documentation and scripts use the same binding + CSI model

### Release evidence

- [ ] API reference matches the live request/response shape
- [ ] integration contract matches AgentSmith’s current request body
- [ ] real workload runbook is up to date
- [ ] no current docs or smoke scripts mention removed persistence concepts
