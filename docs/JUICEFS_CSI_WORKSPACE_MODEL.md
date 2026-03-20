# JuiceFS CSI Workspace Model

This document describes the current persistent workspace model used by AgentSmith when integrating with `mbos-sandbox-v1`.

## Product Truth

- A workload pod mounts a persistent workspace at `/workspace`
- The mounted workspace is the long-lived runtime environment
- Keepalive/TTL only govern compute pod lifetime
- Workspace persistence comes from the PVC-backed JuiceFS directory, not from snapshot restore

This is the current release truth for notebook/internal agent execution.

## Current Boundary

`mbos-sandbox-v1` is responsible for:

- workload pod lifecycle
- mounting the configured PVC into the pod
- command execution in the running pod
- keepalive-driven cleanup

`agentsmith` is responsible for:

- deciding which file library maps to which persistent workspace
- preparing runtime layout inside the mounted workspace
- orchestrating notebook/agent task execution against that mounted workspace

## Storage Shape

The expected storage shape is:

- one shared JuiceFS PVC
- per-workspace/per-workload subdirectories under a stable base path
- workload pod mounts `/workspace` using PVC + `subPath`

This keeps:

- workspace contents persistent
- workload pods ephemeral
- lifecycle ownership clear

## Release Readiness Expectations

Before calling this integration release-ready, verify:

1. workload pod can mount the expected workspace directory at `/workspace`
2. workspace contents survive pod restart/deletion
3. keepalive expiry deletes the pod without deleting the workspace contents
4. AgentSmith can reuse the same persistent workspace across workload recreations
5. operational docs describe PVC/CSI as the persistence truth
