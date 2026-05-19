# Risk Register

| ID | Risk | Owner | Evidence | Release-blocking | Status |
| --- | --- | --- | --- | --- | --- |
| R-001 | ASBCP and AFSCP responsibilities become mixed because the names are similar. | Governance owner | README, contracts, ADRs | No | Open, non-release-blocking; mitigated by boundary docs and release guard coverage |
| R-002 | Retired manager-era identifiers return to public release files. | Governance owner | `.github/tests/asbcp-governance-guard.sh` | No | Open, non-release-blocking; guard blocks governed release surface regressions |
| R-003 | Release workflow publishes an image without running the authoritative gate. | Release owner | Workflow hardening guard | No | Open, non-release-blocking; workflow and quick release gate enforce the ordering |
| R-004 | AgentSmith consumes a mutable image tag instead of an immutable digest. | Cross-repo integration owner | Release notes and AgentSmith lock evidence | No | Open, non-release-blocking for ASBCP image publication; downstream must still lock digest before consuming |
| R-005 | ASBCP accepts raw storage credentials instead of consuming AFSCP mount plans. | Contract owner | AFSCP mount-plan contract and smoke evidence | No | Open, non-release-blocking; active contract guard and smoke fixtures cover the exclusion |
| R-006 | Current service code, module metadata, binary name, and Kubernetes identity are not fully migrated to ASBCP canonical identifiers. | Naming/runtime workers | Source and render guards | No | Open, non-release-blocking; remaining file-name compatibility is documented and allowlisted |
| R-007 | Full service smoke evidence is missing for workspace binding and workload lifecycle operations. | Contract/smoke worker | Smoke output in release evidence | No | Open, non-release-blocking; release gate includes focused fixture smoke, backend-real evidence remains a separate operator step |

No risk in this register can be closed by documentation alone. Closure requires the listed evidence and a passing release gate. The release gate derives `known_risk_status` from the `Release-blocking` column and must fail if any row is marked release-blocking.
