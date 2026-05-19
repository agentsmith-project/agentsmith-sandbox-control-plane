# ASBCP Pre-Launch Checklist

This checklist is for the ASBCP public release model.

## Must-Pass Checks

### Binding Lifecycle

- [ ] `PUT /workspace-bindings/{binding_id}` creates or reuses a stable binding.
- [ ] Binding ensure fetches an AFSCP orchestrator plan with ASBCP service identity.
- [ ] `GET /workspace-bindings/{binding_id}` returns current binding state.
- [ ] `DELETE /workspace-bindings/{binding_id}` removes ASBCP-managed binding resources cleanly.
- [ ] Caller-provided storage backend settings and storage auth material are rejected.

### Workload Lifecycle

- [ ] `PUT /workloads/{workload_id}` requires `workspace_binding_id`.
- [ ] Workload Pod mount path and read-only mode come from the AFSCP plan.
- [ ] Workload container working directory is `<mount_path>/workspace`.
- [ ] Workload env contains `TASK_HOME`, `HOME`, and `WORKSPACE_PATH`.
- [ ] Pod reaches running state.
- [ ] `POST /exec` works against the running Pod.
- [ ] `POST /keepalive` heartbeats AFSCP and extends expiry.
- [ ] `DELETE /workloads/{workload_id}` removes compute and closes AFSCP mount lifecycle.

### Release Evidence

- [ ] `bash scripts/verify-release.sh` passes.
- [ ] API reference matches the live request/response shape.
- [ ] AgentSmith integration contract matches current request bodies.
- [ ] GHCR digest pull is verified.
- [ ] Risk register has no release-blocking open risks.
