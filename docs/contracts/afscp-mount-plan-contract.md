# AFSCP Mount Plan Dependency Contract

ASBCP depends on AFSCP for filesystem and storage truth. ASBCP consumes the workload mount plan and turns that plan into Kubernetes lifecycle resources.

## Boundary

AFSCP owns:

- Payload location.
- Mount path.
- Read-only mode.
- CSI secret reference.
- Storage policy and recovery semantics.
- Filesystem version and restore behavior.

ASBCP owns:

- Fetching the current plan with its service identity.
- Creating or reusing Kubernetes binding resources from the plan.
- Starting workload Pods that mount the binding.
- Calling AFSCP lifecycle operations during keepalive and release.

## Required Plan Fields

The mount plan must provide:

- Binding identifier.
- Payload volume subdirectory.
- Mount path.
- Read-only flag.
- CSI driver and secret reference.
- Lifecycle operation endpoints or identifiers needed for keepalive and release.

## Prohibited Inputs

AgentSmith and ASBCP callers must not provide raw storage backend settings, raw credentials, caller-defined mount paths, or filesystem recovery policy as ASBCP workload inputs.

## Release Evidence

Release evidence must include a fixture or smoke proof that ASBCP can consume an AFSCP plan and complete workspace binding plus workload lifecycle operations.
