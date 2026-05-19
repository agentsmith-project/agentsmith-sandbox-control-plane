# ADR 0004: AFSCP Mount-Plan Dependency

## Status

Accepted.

## Context

ASBCP must mount workspace data into workload Pods without owning filesystem truth.

## Decision

ASBCP consumes AFSCP workload mount plans. The plan provides payload location, mount path, read-only state, CSI secret reference, and lifecycle information. ASBCP turns the plan into Kubernetes resources.

## Consequences

ASBCP release evidence must prove it can consume an AFSCP plan. Raw storage settings are not ASBCP caller contract fields.
