# Risk Register

| ID | Risk | Owner | Evidence | Status |
| --- | --- | --- | --- | --- |
| R-001 | ASBCP and AFSCP responsibilities become mixed because the names are similar. | Governance owner | README, contracts, ADRs | Open, mitigated by boundary docs |
| R-002 | Retired manager-era identifiers return to public release files. | Governance owner | `.github/tests/asbcp-governance-guard.sh` | Open, guard added |
| R-003 | Release workflow publishes an image without running the authoritative gate. | Release owner | Workflow hardening guard | Open, guard added |
| R-004 | AgentSmith consumes a mutable image tag instead of an immutable digest. | Cross-repo integration owner | Release notes and AgentSmith lock evidence | Open |
| R-005 | ASBCP accepts raw storage credentials instead of consuming AFSCP mount plans. | Contract owner | AFSCP mount-plan contract and smoke evidence | Open |
| R-006 | Current service code, module metadata, binary name, and Kubernetes identity are not fully migrated to ASBCP canonical identifiers. | Naming/runtime workers | Source and render guards | Open |
| R-007 | Full service smoke evidence is missing for workspace binding and workload lifecycle operations. | Contract/smoke worker | Smoke output in release evidence | Open |

No risk in this register can be closed by documentation alone. Closure requires the listed evidence and a passing release gate.
