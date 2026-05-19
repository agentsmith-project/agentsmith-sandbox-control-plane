#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

require_file() {
  local path="$1"
  [ -f "${ROOT}/${path}" ] || fail "required file is missing: ${path}"
}

require_contains() {
  local path="$1"
  local pattern="$2"
  local description="$3"
  grep -Eq "${pattern}" "${ROOT}/${path}" || fail "${path} does not contain ${description}"
}

required_files=(
  README.md
  LICENSE
  NOTICE
  CONTRIBUTING.md
  SECURITY.md
  CHANGELOG.md
  docs/DEVELOPER_GUIDE.md
  docs/DEVELOPMENT_GOVERNANCE.md
  docs/RELEASE_GATES.md
  docs/READINESS_EVIDENCE.md
  docs/RISK_REGISTER.md
  docs/contracts/api-contract.md
  docs/contracts/auth-contract.md
  docs/contracts/afscp-mount-plan-contract.md
  docs/contracts/operations-and-errors.md
  docs/runbooks/local-development.md
  docs/runbooks/release.md
  docs/runbooks/rollback-rollforward.md
  docs/runbooks/kubernetes-operations.md
  docs/runbooks/diagnostics.md
  docs/adr/0001-repo-runtime-identity.md
  docs/adr/0002-service-auth.md
  docs/adr/0003-workload-lifecycle.md
  docs/adr/0004-afscp-mount-plan-dependency.md
  docs/adr/0005-image-release-contract.md
  docs/adr/0006-runner-artifact-classification.md
  docs/release-evidence/release-manifest.json
  scripts/verify-release.sh
  Makefile
  .github/pull_request_template.md
  .github/workflows/ci.yml
  .github/workflows/release.yml
)

for path in "${required_files[@]}"; do
  require_file "${path}"
done

require_contains README.md "AgentSmith Sandbox Control Plane \\(ASBCP\\)" "the canonical ASBCP name"
require_contains README.md "AFSCP" "the AFSCP boundary"
require_contains docs/RELEASE_GATES.md "scripts/verify-release\\.sh" "the authoritative release gate"
require_contains Makefile "release-gate:" "the release-gate wrapper"
require_contains Makefile "scripts/verify-release\\.sh" "the authoritative release gate wrapper"
require_contains .github/workflows/release.yml "scripts/verify-release\\.sh" "the authoritative release gate call"
require_contains .github/workflows/release.yml "packages:[[:space:]]*write" "GHCR package write permission"
require_contains .github/workflows/release.yml "docker/login-action" "GHCR login"
require_contains .github/workflows/release.yml "docker/build-push-action" "image build and push"
require_contains .github/workflows/release.yml "docker pull" "digest pull verification"

guard_paths=(
  README.md
  CONTRIBUTING.md
  SECURITY.md
  CHANGELOG.md
  NOTICE
  docs/DEVELOPER_GUIDE.md
  docs/DEVELOPMENT_GOVERNANCE.md
  docs/RELEASE_GATES.md
  docs/READINESS_EVIDENCE.md
  docs/RISK_REGISTER.md
  docs/README.md
  docs/AFSCP_WORKLOAD_MOUNT_MODEL.md
  docs/PRE_LAUNCH_CHECKLIST.md
  docs/api-reference.md
  docs/configuration.md
  docs/runbook.md
  docs/contracts
  docs/runbooks
  docs/adr
  docs/release-evidence
  .github
  scripts/verify-release.sh
)

if rg -n -g '!.github/tests/asbcp-governance-guard.sh' "SANDBOX_MANAGER|SANDBOX_SERVICE_KEY|github\\.com/sandbox/manager|mbos-sandbox-v1|agentsmith-sandbox-manager|/v1/sandboxes|sandbox-manager|Sandbox Manager" "${guard_paths[@]}" >/tmp/asbcp-old-name-guard.txt; then
  cat /tmp/asbcp-old-name-guard.txt >&2
  fail "old ASBCP naming or retired API path found in governed release surface"
fi

if grep -Eq 'Release gate PASSED|release-gate:[[:space:]].*(lint|test-race|_check-coverage|_build-check)' "${ROOT}/Makefile"; then
  fail "Makefile release-looking target must only wrap scripts/verify-release.sh"
fi

python3 -m json.tool "${ROOT}/docs/release-evidence/release-manifest.json" >/dev/null
python3 - "${ROOT}/docs/release-evidence/release-manifest.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

pending = [
    item.get("id", "<unknown>")
    for item in manifest.get("required_evidence", [])
    if item.get("status") == "pending"
]
if pending:
    print("pending release evidence: " + ", ".join(pending), file=sys.stderr)
    sys.exit(1)
PY

echo "ASBCP governance guard passed"
