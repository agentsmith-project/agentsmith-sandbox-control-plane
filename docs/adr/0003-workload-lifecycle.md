# ADR 0003: Workload Lifecycle Ownership

## Status

Accepted.

## Context

Agent tasks need sandbox workload Pods with workspace files mounted from AFSCP-managed storage truth.

## Decision

ASBCP owns workspace binding materialization, workload Pod lifecycle, keepalive, exec, release, and delete operations. AgentSmith owns task selection and runner image selection. AFSCP owns storage truth and mount plan semantics.

## Consequences

ASBCP APIs accept workload lifecycle inputs, not AgentSmith product governance controls or storage backend credentials.
