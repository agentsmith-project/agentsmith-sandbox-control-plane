# Sandbox Pre-Launch Checklist

This checklist is for the current production model only:

- JuiceFS CSI workspace bindings
- workload pods mounted from AFSCP workload mount plans
- keepalive-driven AFSCP heartbeat and reclaim of compute pods

## Must-pass checks

### Binding lifecycle

- [ ] `PUT /workspace-bindings/{bindingId}` creates or reuses a stable binding
- [ ] binding ensure fetches AFSCP orchestrator plan with sandbox orchestrator identity
- [ ] `GET /workspace-bindings/{bindingId}` returns the current binding state
- [ ] `DELETE /workspace-bindings/{bindingId}` removes the binding resources cleanly
- [ ] ensuring the same binding twice returns the same stable PVC identity
- [ ] caller-provided storage backend settings and storage auth material are rejected

### Workload lifecycle

- [ ] `PUT /workloads/{wlId}` requires `workspace_binding_id`
- [ ] workload pod mount path and read-only mode come from AFSCP plan
- [ ] workload container `workingDir` is `<mount_path>/workspace`
- [ ] workload env contains `TASK_HOME`, `HOME`, and `WORKSPACE_PATH`
- [ ] pod reaches `Running`
- [ ] `POST /exec` works against the running pod
- [ ] `POST /keepalive` heartbeats AFSCP and extends `expires_at`
- [ ] `DELETE /workloads/{wlId}` removes compute and closes AFSCP mount lifecycle

### Persistence

- [ ] files written under the plan-provided `WORKSPACE_PATH` survive workload deletion and recreation when the AFSCP binding remains available; deletion runs the mounted payload flush barrier before terminal release
- [ ] expired compute reclaim goes through the manager workload lifecycle, not direct pod deletion
- [ ] the same binding can be reused across multiple workloads and tasks

### Configuration

- [ ] manager is configured with AFSCP internal base URL and orchestrator token
- [ ] manager uses only CSI driver, capacity, and storage class as local storage knobs
- [ ] documentation and scripts use the same AFSCP plan consumer model

### Release evidence

- [ ] API reference matches the live request/response shape
- [ ] integration contract matches AgentSmith’s current request body
- [ ] real workload runbook is up to date
- [ ] no current docs or smoke scripts mention removed persistence concepts
