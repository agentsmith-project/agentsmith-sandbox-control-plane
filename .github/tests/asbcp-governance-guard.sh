#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

cd "${ROOT}" || fail "could not enter repo root: ${ROOT}"

require_file() {
  local path="$1"
  [ -f "${ROOT}/${path}" ] || fail "required file is missing: ${path}"
}

require_contains() {
  local path="$1"
  local pattern="$2"
  local description="$3"
  grep -Eq -- "${pattern}" "${ROOT}/${path}" || fail "${path} does not contain ${description}"
}

required_files=(
  README.md
  .gitignore
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
  manager-service/scripts/test-asbcp-api.sh
  k8s/overlays/production/access/README.md
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
require_contains Makefile "scripts/test-asbcp-api\\.sh" "the canonical ASBCP API smoke script"
require_contains manager-service/README.md "scripts/test-asbcp-api\\.sh" "the canonical ASBCP API smoke script"
require_contains .github/workflows/release.yml "scripts/verify-release\\.sh" "the authoritative release gate call"
require_contains .github/workflows/release.yml "Run authoritative release gate" "the named authoritative release gate step"
require_contains .github/workflows/release.yml "packages:[[:space:]]*write" "GHCR package write permission"
require_contains .github/workflows/release.yml "docker/login-action" "GHCR login"
require_contains .github/workflows/release.yml "docker/build-push-action" "image build and push"
require_contains .github/workflows/release.yml "docker pull" "digest pull verification"
require_contains .github/workflows/release.yml "Generate final manifest" "final manifest generation"
require_contains .github/workflows/release.yml "asbcp-final-manifest\\.json" "final manifest release asset"
require_contains .github/workflows/release.yml "asbcp-release-notes\\.md" "release notes derived from the final manifest"
require_contains .github/workflows/release.yml "body_path:" "release notes file upload"
require_contains .github/workflows/release.yml "fail_on_unmatched_files:[[:space:]]*true" "hard failure when final manifest asset is missing"
require_contains .github/workflows/release.yml "files:" "GitHub Release asset upload"
require_contains .github/workflows/release.yml "\"schema_id\"" "final manifest schema id field"
require_contains .github/workflows/release.yml "https://agentsmith\\.dev/schemas/asbcp/final-manifest\\.v1\\.json" "planned final manifest schema id"
require_contains .github/workflows/release.yml "\"asbcp_version\"" "final manifest ASBCP version field"
require_contains .github/workflows/release.yml "\"git_tag\"" "final manifest git tag field"
require_contains .github/workflows/release.yml "\"commit_sha\"" "final manifest commit SHA field"
require_contains .github/workflows/release.yml "\"image_ref\"" "final manifest image ref field"
require_contains .github/workflows/release.yml "\"image_digest\"" "final manifest image digest field"
require_contains .github/workflows/release.yml "\"api_contract_version\"" "final manifest API contract version field"
require_contains .github/workflows/release.yml "\"anonymous_pull\"" "final manifest anonymous pull field"
require_contains .github/workflows/release.yml "\"same_digest_proof\"" "final manifest same digest proof field"
require_contains .github/workflows/release.yml "\"known_breaking_changes\"" "final manifest breaking changes field"
require_contains .github/workflows/release.yml "\"changelog_summary\"" "final manifest changelog summary field"
require_contains .github/workflows/release.yml "\"known_risk_status\"" "final manifest risk status field"
require_contains .github/workflows/release.yml "\"runbook_url\"" "final manifest runbook link field"
require_contains .github/workflows/release.yml "\"release_notes\"" "final manifest release notes evidence field"
require_contains .github/workflows/release.yml "\"body_source\"" "GitHub Release body source field"
require_contains .github/workflows/release.yml "\"tag_resolved_digest\"" "same digest proof tag-resolved digest field"
require_contains .github/workflows/release.yml "\"build_push_digest\"" "same digest proof build-push digest field"
require_contains .github/workflows/release.yml "\"anonymous_digest\"" "same digest proof anonymous digest field"
require_contains .github/workflows/release.yml "TAG_RESOLVED_DIGEST" "tag-resolved digest workflow output"
require_contains .github/workflows/release.yml "ANONYMOUS_DIGEST" "anonymous tag@digest workflow output"
require_contains .github/workflows/release.yml "fresh-empty" "fresh anonymous Docker config evidence"
require_contains .github/workflows/release.yml "manifest\\[\"release_notes\"\\]\\[\"body_source\"\\]" "release notes file derived from manifest body_source"
require_contains scripts/verify-release.sh "check_changelog_release_evidence" "pre-push CHANGELOG release evidence gate"
require_contains scripts/verify-release.sh "emit_changelog_release_evidence" "shared CHANGELOG release evidence parser"
require_contains scripts/verify-release.sh "--changelog-evidence-json" "shared CHANGELOG evidence JSON command"
require_contains .github/workflows/release.yml "--changelog-evidence-json" "final manifest reuse of the authoritative CHANGELOG evidence parser"
require_contains .github/workflows/release.yml "dist/asbcp-changelog-evidence\\.json" "parsed CHANGELOG evidence handoff"
require_contains .github/workflows/release.yml "--risk-status-json" "risk register release status parser"
require_contains .github/workflows/release.yml "dist/asbcp-risk-status\\.json" "risk status evidence handoff"
require_contains .github/workflows/release.yml "--api-contract-version" "API contract version parser"
require_contains .github/workflows/release.yml "dist/asbcp-api-contract-version\\.txt" "parsed API contract version handoff"
require_contains .github/workflows/release.yml "\"known_risk_status_source\"" "final manifest risk status source field"
require_contains scripts/verify-release.sh "read_api_contract_version" "API contract version reader"
require_contains scripts/verify-release.sh "check_api_contract_version_evidence" "API contract version evidence gate"
require_contains scripts/verify-release.sh "### Breaking Changes" "CHANGELOG breaking changes subsection parser"
require_contains scripts/verify-release.sh "emit_risk_status_evidence" "risk register release status evidence parser"
require_contains scripts/verify-release.sh "docs/RISK_REGISTER\\.md" "risk register status source"
require_contains docs/RISK_REGISTER.md "Release-blocking" "release-blocking classification column"
require_contains docs/RISK_REGISTER.md "non-release-blocking" "non-blocking open risk classification"
require_contains k8s/overlays/production/README.md "internal-only by default" "production internal-only default"
require_contains k8s/overlays/production/README.md "private operator opt-in" "explicit private access opt-in"
require_contains k8s/overlays/production/access/README.md "private operator opt-in" "explicit private access example documentation"

if grep -Fq "BREAKING_CHANGES" "${ROOT}/.github/workflows/release.yml"; then
  fail "release workflow must parse known_breaking_changes from CHANGELOG.md instead of static BREAKING_CHANGES env"
fi

if grep -Fq '"asset": "asbcp-release-notes.md"' "${ROOT}/.github/workflows/release.yml"; then
  fail "release_notes must identify the GitHub Release body source, not an unuploaded release-notes asset"
fi

if grep -Fq "docker manifest inspect" "${ROOT}/.github/workflows/release.yml"; then
  fail "release workflow must not use docker manifest inspect as release evidence fallback"
fi

if grep -Eq 'pulled_digest|anonymous_pull_digest|ANONYMOUS_PULL_DIGEST|"body_source":[[:space:]]*"dist/asbcp-release-notes\.md"' "${ROOT}/.github/workflows/release.yml"; then
  fail "release workflow must record tag_resolved_digest/build_push_digest/anonymous_digest and store release_notes.body_source as full body text"
fi

if grep -Eq "test-manager\\.sh" "${ROOT}/Makefile" "${ROOT}/manager-service/README.md"; then
  fail "Makefile and manager-service README must use test-asbcp-api.sh, not test-manager.sh"
fi

if grep -Eq '(^|[[:space:]])build-manager([[:space:]:]|$)' "${ROOT}/Makefile"; then
  fail "Makefile must not keep the build-manager old alias"
fi

if grep -Eq 'KNOWN_RISK_STATUS:|os\.environ\["KNOWN_RISK_STATUS"\]' "${ROOT}/.github/workflows/release.yml"; then
  fail "release workflow must derive known_risk_status from docs/RISK_REGISTER.md instead of a static KNOWN_RISK_STATUS string"
fi

if grep -Eq "API_CONTRACT_VERSION:[[:space:]]*['\"]?v[0-9]+" "${ROOT}/.github/workflows/release.yml"; then
  fail "release workflow must derive api_contract_version from docs/contracts/api-contract.md"
fi

active_operator_surface_paths=(
  "sbx"
  "scripts/lib/sandbox.sh"
)
active_operator_alias_patterns=(
  '(^|[[:space:]])\./sbx[[:space:]]+sandbox([[:space:]]|$)'
  '(^|[[:space:]])sandbox\)'
  'sbx_sandbox'
  'sandbox_(list|cleanup)'
  'app=llm-sandbox'
  '\["sandbox/'
  'kubectl[[:space:]][^[:cntrl:]]*delete[[:space:]]+pods?'
)
active_operator_alias_regex="$(IFS='|'; printf '%s' "${active_operator_alias_patterns[*]}")"
active_operator_alias_output="$(mktemp -t asbcp-active-operator-alias.XXXXXX)"
trap 'rm -f "${old_name_guard_output:-}" "${active_operator_alias_output:-}"' EXIT

set +e
git -C "${ROOT}" grep -n -I -E -- "${active_operator_alias_regex}" -- "${active_operator_surface_paths[@]}" >"${active_operator_alias_output}"
active_operator_alias_status=$?
set -e
if [ "${active_operator_alias_status}" -eq 0 ]; then
  cat "${active_operator_alias_output}" >&2
  fail "active sbx operator surface must use ASBCP workload wording and must not keep the retired sandbox alias/defaults/direct pod deletion"
elif [ "${active_operator_alias_status}" -ne 1 ]; then
  cat "${active_operator_alias_output}" >&2
  fail "active sbx operator alias scan failed"
fi

if grep -Eq 'access/(ingress|nodeport|loadbalancer)\.yaml' "${ROOT}/k8s/overlays/production/kustomization.yaml"; then
  fail "production overlay must be internal-only by default and must not include ASBCP access resources"
fi

if grep -Fq "runtime_evidence_field" "${ROOT}/docs/release-evidence/release-manifest.json"; then
  fail "final manifest schema must call same_digest_proof image identity evidence, not runtime evidence"
fi

python3 - "${ROOT}/.github/workflows/release.yml" <<'PY'
import sys

workflow = open(sys.argv[1], "r", encoding="utf-8").read()
sequence = [
    "Run authoritative release gate",
    "Build and push ASBCP image",
    "Verify anonymous digest pull",
    "Generate final manifest",
    "Create GitHub Release",
]
previous = -1
failures = []
for token in sequence:
    index = workflow.find(token)
    if index == -1:
        failures.append(f"release workflow missing ordered step {token}")
        continue
    if index <= previous:
        failures.append("release workflow must order " + " -> ".join(sequence))
        break
    previous = index
if failures:
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
PY

# Content scanning intentionally uses every tracked text file instead of a
# directory allowlist. This keeps manager-service source/config/tests, go.mod,
# Dockerfile, and k8s/base under the same governance guard as docs/workflows.
# Exact guard/evidence self-tests are excluded because they intentionally keep
# retired tokens as fixtures; active service and smoke test roots stay scanned.
tracked_active_text_exclude_pathspecs=(
  ':!.github/tests/asbcp-governance-guard.sh'
  ':!manager-service/internal/app/active_contract_guard_test.go'
)

canonical_forbidden_content_patterns=(
  'SANDBOX_MANAGER(_[A-Z0-9_]+)?'
  'SANDBOX_CONTROL_PLANE(_[A-Z0-9_]+)?'
  'SANDBOX_SERVICE_KEY'
  'SANDBOX_SOURCE_DIR'
  '--sandbox-source-dir'
  'github\.com/sandbox/manager'
  'mbos-sandbox-v1'
  'agentsmith-sandbox-manager'
  '/v1/sandboxes'
  'sandbox-manager'
  'Sandbox Manager'
  'sandbox manager'
  'sandboxManager'
  'SandboxManager'
  'sandbox_manager'
  'manager delete API'
  'sandboxControlPlane'
  'SandboxControlPlane'
  'sandbox_control_plane'
  'start-manager'
  'stop-manager'
  'restart-manager'
  'build-manager'
  '/etc/asbcp/config\.yaml'
  '/etc/sandbox-manager/manager-config\.yaml'
  'sandbox-manager-image\.lock'
  'sandbox-manager-pv-rbac\.yaml\.tpl'
  'go[[:space:]]+run[[:space:]]+\./cmd/manager'
  '\./cmd/manager'
  'cmd/manager'
  'cmd/cleaner'
  'E2E_MANAGER_'
  'ManagerURL'
  'ManagerBin'
  'MANAGER_URL'
  'MANAGER_VERSION'
  'MANAGER_[A-Z0-9_]*'
  'SANDBOX_ID'
  'create_sandbox'
  'delete_sandbox'
  'create sandbox'
  'Create sandbox'
  'Creating sandbox'
  'Failed to create sandbox'
  'Sandbox ID'
  'sandbox ID'
  'GC_VERSION'
  'Manager 副本'
  'Manager 可能'
  'Manager 测试'
  'Manager 代码'
)

canonical_forbidden_content_regex="$(IFS='|'; printf '%s' "${canonical_forbidden_content_patterns[*]}")"
old_name_guard_output="$(mktemp -t asbcp-old-name-guard.XXXXXX)"

set +e
git -C "${ROOT}" grep -n -I -E -- "${canonical_forbidden_content_regex}" -- . "${tracked_active_text_exclude_pathspecs[@]}" >"${old_name_guard_output}"
old_name_guard_status=$?
set -e
if [ "${old_name_guard_status}" -eq 0 ]; then
  cat "${old_name_guard_output}" >&2
  fail "old ASBCP naming, retired command invocation, or smoke/operator alias found in tracked active text"
elif [ "${old_name_guard_status}" -ne 1 ]; then
  cat "${old_name_guard_output}" >&2
  fail "tracked active text old-name scan failed"
fi

canonical_forbidden_path_tokens=(
  "manager"
  "manager-config"
  "sandbox-manager"
  "sandbox manager"
  "agentsmith-sandbox-manager"
  "cmd/manager"
  "cmd/cleaner"
  "bin/cleaner"
  "api-reference-v2"
  "agentsmith-integration-contract-v2"
  "wait-for-minio.sh"
  "mbos-sandbox-v1"
  "SANDBOX_CONTROL_PLANE"
  "SANDBOX_SOURCE_DIR"
  "--sandbox-source-dir"
  "start-manager"
  "stop-manager"
  "restart-manager"
  "sandbox-manager-image.lock"
  "sandbox-manager-pv-rbac.yaml.tpl"
)

# Content guard also covers retired config paths: /etc/asbcp/config.yaml and
# /etc/sandbox-manager/manager-config.yaml. Keep these literals visible so the
# guard's own coverage tests can detect accidental removal.

active_path_exception_root="manager-service"
active_path_exception_prefix="${active_path_exception_root}/"
active_path_exception_reason="Go module root exception only; this release-governance slice does not rename manager-service"

scan_old_name_path() {
  local candidate_path="$1"
  local path_to_scan
  local token

  [ -n "${candidate_path}" ] || return 0
  [ -e "${ROOT}/${candidate_path}" ] || return 0

  path_to_scan="${candidate_path}"
  if [[ "${candidate_path}" == "${active_path_exception_root}" ]]; then
    [ -n "${active_path_exception_reason}" ] || fail "manager-service active path exception must include a reason"
    path_to_scan=""
  elif [[ "${candidate_path}" == "${active_path_exception_prefix}"* ]]; then
    [ -n "${active_path_exception_reason}" ] || fail "manager-service active path exception must include a reason"
    path_to_scan="${candidate_path#${active_path_exception_prefix}}"
  fi

  for token in "${canonical_forbidden_path_tokens[@]}"; do
    if [[ "${path_to_scan}" == *"${token}"* ]]; then
      printf '%s: forbidden path token %s\n' "${candidate_path}" "${token}" >&2
      fail "old ASBCP naming found in path name"
    fi
  done
}

while IFS= read -r tracked_path; do
  scan_old_name_path "${tracked_path}"
done < <(git -C "${ROOT}" ls-files --cached --others --exclude-standard)

while IFS= read -r ignored_path; do
  scan_old_name_path "${ignored_path}"
done < <(git -C "${ROOT}" ls-files --others --ignored --exclude-standard)

while IFS= read -r fs_path; do
  scan_old_name_path "${fs_path#"${ROOT}/"}"
done < <(find "${ROOT}" -mindepth 1 -path "${ROOT}/.git" -prune -o -type d -print)

if grep -Eq 'Release gate PASSED|release-gate:[[:space:]].*(lint|test-race|_check-coverage|_build-check)' "${ROOT}/Makefile"; then
  fail "Makefile release-looking target must only wrap scripts/verify-release.sh"
fi

python3 -m json.tool "${ROOT}/docs/release-evidence/release-manifest.json" >/dev/null
python3 - "${ROOT}/docs/release-evidence/release-manifest.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

schema = manifest.get("final_manifest_schema") or {}
failures = []
if schema.get("schema_id") != "https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json":
    failures.append("final_manifest_schema.schema_id must be https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json")
if schema.get("image_identity_evidence_field") != "same_digest_proof":
    failures.append("final_manifest_schema.image_identity_evidence_field must be same_digest_proof")
if schema.get("risk_status_source") != "docs/RISK_REGISTER.md release_blocking column":
    failures.append("final_manifest_schema.risk_status_source must be docs/RISK_REGISTER.md release_blocking column")
if schema.get("runtime_evidence_field"):
    failures.append("final_manifest_schema must not use runtime_evidence_field for same_digest_proof")
required_fields = set(schema.get("required_fields") or [])
if "known_risk_status_source" not in required_fields:
    failures.append("final_manifest_schema.required_fields must include known_risk_status_source")
release_notes_fields = set((schema.get("nested_required_fields") or {}).get("release_notes") or [])
if "body_source" not in release_notes_fields:
    failures.append("final_manifest_schema.nested_required_fields.release_notes must include body_source")
if "asset" in release_notes_fields:
    failures.append("release_notes nested schema must not require asset for the generated release body")
anonymous_pull_fields = set((schema.get("nested_required_fields") or {}).get("anonymous_pull") or [])
same_digest_fields = set((schema.get("nested_required_fields") or {}).get("same_digest_proof") or [])
required_anonymous_pull = {"tag_ref", "tag_resolved_digest", "build_push_digest", "anonymous_digest", "commands"}
missing_anonymous_pull = sorted(required_anonymous_pull - anonymous_pull_fields)
if missing_anonymous_pull:
    failures.append("final_manifest_schema.nested_required_fields.anonymous_pull missing: " + ", ".join(missing_anonymous_pull))
required_same_digest = {"tag_resolved_digest", "build_push_digest", "anonymous_digest", "matches"}
missing_same_digest = sorted(required_same_digest - same_digest_fields)
if missing_same_digest:
    failures.append("final_manifest_schema.nested_required_fields.same_digest_proof missing: " + ", ".join(missing_same_digest))
if {"pulled_digest", "command"} & anonymous_pull_fields:
    failures.append("anonymous_pull nested schema must not keep pulled_digest or single command")
if "anonymous_pull_digest" in same_digest_fields:
    failures.append("same_digest_proof nested schema must not keep anonymous_pull_digest")
if "body_path" in release_notes_fields:
    failures.append("release_notes nested schema must not require body_path")

pending = [
    item.get("id", "<unknown>")
    for item in manifest.get("required_evidence", [])
    if item.get("status") == "pending"
]
if pending:
    failures.append("pending release evidence: " + ", ".join(pending))
if failures:
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
PY

echo "ASBCP governance guard passed"
