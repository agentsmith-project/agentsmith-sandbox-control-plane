#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="release"
CHANGELOG_EVIDENCE_TAG=""
ASBCP_CONFIG_PATH="/etc/asbcp/asbcp-config.yaml"

case "${1:-}" in
  "")
    ;;
  "--quick")
    MODE="quick"
    shift
    ;;
  "--changelog-evidence-json")
    MODE="changelog-evidence-json"
    shift
    CHANGELOG_EVIDENCE_TAG="${1:-}"
    [ -n "${CHANGELOG_EVIDENCE_TAG}" ] || {
      echo "usage: bash scripts/verify-release.sh [--quick|--changelog-evidence-json <tag>|--risk-status-json|--api-contract-version]" >&2
      exit 2
    }
    shift
    ;;
  "--risk-status-json")
    MODE="risk-status-json"
    shift
    ;;
  "--api-contract-version")
    MODE="api-contract-version"
    shift
    ;;
  *)
    echo "usage: bash scripts/verify-release.sh [--quick|--changelog-evidence-json <tag>|--risk-status-json|--api-contract-version]" >&2
    exit 2
    ;;
esac

if [ "$#" -ne 0 ]; then
  echo "usage: bash scripts/verify-release.sh [--quick|--changelog-evidence-json <tag>|--risk-status-json|--api-contract-version]" >&2
  exit 2
fi

run() {
  echo "==> $*"
  "$@"
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required for release mode"
}

shell_function_stanza() {
  local function_name="$1"
  local path="$2"
  awk -v name="${function_name}() {" '
    $0 == name { in_function = 1 }
    in_function { print }
    in_function && $0 == "}" { exit }
  ' "$path"
}

read_version() {
  tr -d '[:space:]' < "${ROOT}/VERSION"
}

read_api_contract_version() {
  require_cmd python3
  python3 - "${ROOT}/docs/contracts/api-contract.md" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")
match = re.search(r"^Contract version:\s*`([^`]+)`\s*$", text, re.MULTILINE)
if not match:
    raise SystemExit("docs/contracts/api-contract.md must declare Contract version: `<version>`")
version = match.group(1).strip()
if not re.fullmatch(r"v[0-9]+", version):
    raise SystemExit(f"API contract version must look like vN, got {version!r}")
print(version)
PY
}

check_version_tag_contract() {
  local version="$1"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+)*$ ]] || \
    fail "VERSION must be semver without leading v, got: ${version}"

  local expected_tag="v${version}"
  if [ "${MODE}" = "release" ] && { [ "${GITHUB_REF_TYPE:-}" = "tag" ] || [[ "${GITHUB_REF:-}" == refs/tags/* ]]; }; then
    local actual_tag="${GITHUB_REF_NAME:-}"
    if [ -z "$actual_tag" ] && [[ "${GITHUB_REF:-}" == refs/tags/* ]]; then
      actual_tag="${GITHUB_REF#refs/tags/}"
    fi
    [ "$actual_tag" = "$expected_tag" ] || \
      fail "release tag must be ${expected_tag}, got ${actual_tag:-<unset>}"
  fi
}

emit_changelog_release_evidence() {
  local tag="$1"
  require_cmd python3
  python3 - "${ROOT}/CHANGELOG.md" "$tag" <<'PY'
import json
import sys
from pathlib import Path

changelog_path = Path(sys.argv[1])
tag = sys.argv[2]
if not tag:
    raise SystemExit("CHANGELOG release evidence requires a non-empty tag")


def read_changelog_release_section(path: Path, release_tag: str) -> list[str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    accepted_headings = {release_tag, f"[{release_tag}]"}
    section_start = None
    for index, line in enumerate(lines):
        if not line.startswith("## "):
            continue
        heading = line.removeprefix("## ").strip()
        heading_name = heading.split(" - ", 1)[0].strip()
        if heading_name in accepted_headings:
            section_start = index + 1
            break
    if section_start is None:
        raise SystemExit(f"Missing CHANGELOG.md release section for {release_tag}")

    section_end = len(lines)
    for index in range(section_start, len(lines)):
        if lines[index].startswith("## "):
            section_end = index
            break
    return lines[section_start:section_end]


def parse_known_breaking_changes(section: list[str], release_tag: str) -> list[str]:
    breaking_start = None
    for index, line in enumerate(section):
        if line.strip() == "### Breaking Changes":
            breaking_start = index + 1
            break
    if breaking_start is None:
        return []

    breaking_changes = []
    for line in section[breaking_start:]:
        stripped = line.strip()
        if stripped.startswith("### ") or stripped.startswith("## "):
            break
        if not stripped:
            continue
        if not stripped.startswith("- "):
            raise SystemExit(
                f"CHANGELOG.md release section for {release_tag} has a non-bullet Breaking Changes entry"
            )
        entry = stripped[2:].strip()
        if entry:
            breaking_changes.append(entry)
    if not breaking_changes:
        raise SystemExit(f"CHANGELOG.md release section for {release_tag} has an empty Breaking Changes subsection")
    return breaking_changes


def parse_changelog_summary(section: list[str]) -> str:
    entries = []
    heading = ""
    for line in section:
        stripped = line.strip()
        if stripped.startswith("### "):
            heading = stripped.removeprefix("### ").strip()
            continue
        if heading == "Breaking Changes" or not stripped.startswith("- "):
            continue
        entry = stripped[2:].strip()
        if entry:
            entries.append(entry)
    if not entries:
        return "No non-breaking changelog entries recorded."
    return "; ".join(entries)


section = read_changelog_release_section(changelog_path, tag)
payload = {
    "tag": tag,
    "known_breaking_changes": parse_known_breaking_changes(section, tag),
    "changelog_summary": parse_changelog_summary(section),
}
print(json.dumps(payload, ensure_ascii=False, sort_keys=True))
PY
}

check_changelog_release_evidence() {
  local tag="$1"
  emit_changelog_release_evidence "$tag" >/dev/null
}

emit_risk_status_evidence() {
  require_cmd python3
  python3 - "${ROOT}/docs/RISK_REGISTER.md" <<'PY'
import json
import re
import sys
from pathlib import Path

risk_register_path = Path(sys.argv[1])
source = "docs/RISK_REGISTER.md release_blocking column"
text = risk_register_path.read_text(encoding="utf-8")
lines = [line.strip() for line in text.splitlines() if line.strip().startswith("|")]

if len(lines) < 3:
    raise SystemExit("docs/RISK_REGISTER.md must contain a markdown risk table")


def cells(line: str) -> list[str]:
    return [cell.strip() for cell in line.strip().strip("|").split("|")]


def normalize_header(value: str) -> str:
    normalized = value.lower().replace("-", "_").replace(" ", "_")
    return re.sub(r"[^a-z0-9_]", "", normalized)


header = cells(lines[0])
normalized_header = [normalize_header(cell) for cell in header]
required_columns = {"id", "risk", "owner", "evidence", "release_blocking", "status"}
missing = sorted(required_columns - set(normalized_header))
if missing:
    raise SystemExit("docs/RISK_REGISTER.md missing columns: " + ", ".join(missing))

blocking_risks: list[str] = []
open_risks: list[str] = []
non_release_blocking_open_risks: list[str] = []

for line in lines[2:]:
    row_cells = cells(line)
    if len(row_cells) != len(normalized_header):
        raise SystemExit("docs/RISK_REGISTER.md malformed row: " + line)
    row = dict(zip(normalized_header, row_cells))
    risk_id = row["id"]
    if not risk_id.startswith("R-"):
        continue
    evidence = row["evidence"].strip()
    status = row["status"].strip()
    blocking_value = row["release_blocking"].strip().lower()
    is_open = status.lower().startswith("open")
    if not evidence:
        raise SystemExit(f"{risk_id}: evidence must not be empty")
    if blocking_value in {"yes", "true", "release-blocking", "release_blocking"}:
        blocking_risks.append(risk_id)
    elif blocking_value in {"no", "false", "non-release-blocking", "non_release_blocking"}:
        if is_open:
            if "non-release-blocking" not in status.lower():
                raise SystemExit(f"{risk_id}: open non-blocking risks must say non-release-blocking in status")
            open_risks.append(risk_id)
            non_release_blocking_open_risks.append(risk_id)
    else:
        raise SystemExit(f"{risk_id}: release_blocking must be Yes or No, got {row['release_blocking']!r}")

if blocking_risks:
    known_risk_status = "Release-blocking risks in docs/RISK_REGISTER.md: " + ", ".join(blocking_risks)
else:
    open_suffix = ", ".join(open_risks) if open_risks else "none"
    known_risk_status = (
        "No release-blocking risks in docs/RISK_REGISTER.md; "
        f"open non-release-blocking risks remain: {open_suffix}"
    )

payload = {
    "known_risk_status": known_risk_status,
    "source": source,
    "release_blocking_field": "release_blocking",
    "blocking_risks": blocking_risks,
    "open_risks": open_risks,
    "non_release_blocking_open_risks": non_release_blocking_open_risks,
}
print(json.dumps(payload, ensure_ascii=False, sort_keys=True))
PY
}

check_risk_status_evidence() {
  require_cmd python3
  local payload
  payload="$(emit_risk_status_evidence)"
  python3 - "${payload}" <<'PY'
import json
import sys

payload = json.loads(sys.argv[1])
failures = []
if payload.get("source") != "docs/RISK_REGISTER.md release_blocking column":
    failures.append("risk status source must be docs/RISK_REGISTER.md release_blocking column")
if payload.get("blocking_risks"):
    failures.append("release-blocking risks remain: " + ", ".join(payload["blocking_risks"]))
if not payload.get("open_risks"):
    failures.append("risk status must preserve open non-release-blocking risks")
if "no risks" in payload.get("known_risk_status", "").lower():
    failures.append("known_risk_status must not claim there are no risks")
if failures:
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
PY
}

check_api_contract_version_evidence() {
  require_cmd python3
  local contract_version
  contract_version="$(read_api_contract_version)"
  [ -n "$contract_version" ] || fail "could not read API contract version"

  python3 - "${ROOT}/docs/release-evidence/release-manifest.json" "$contract_version" <<'PY'
import json
import sys

manifest_path = sys.argv[1]
contract_version = sys.argv[2]
with open(manifest_path, "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

if manifest.get("api_contract_version") != contract_version:
    raise SystemExit(
        "docs/release-evidence/release-manifest.json api_contract_version must match "
        f"docs/contracts/api-contract.md Contract version {contract_version}"
    )
PY

  local workflow="${ROOT}/.github/workflows/release.yml"
  grep -Fq -- "--api-contract-version" "$workflow" || \
    fail "release workflow must read API contract version through scripts/verify-release.sh --api-contract-version"
  grep -Fq -- "dist/asbcp-api-contract-version.txt" "$workflow" || \
    fail "release workflow must pass parsed API contract version through dist/asbcp-api-contract-version.txt"
  if grep -Eq "API_CONTRACT_VERSION:[[:space:]]*['\"]?v[0-9]+" "$workflow"; then
    fail "release workflow must not hardcode API_CONTRACT_VERSION; read docs/contracts/api-contract.md"
  fi
}

check_release_workflow_lock_output() {
  require_cmd python3
  local workflow="${ROOT}/.github/workflows/release.yml"
  local required=(
    "Validate release version"
    "Run authoritative release gate"
    'v${version}'
    "steps.version.outputs.version"
    "steps.version.outputs.image_tag"
    'ASBCP_VERSION: ${{ steps.version.outputs.image_tag }}'
    "ASBCP_GIT_TAG"
    "asbcp_version="
    "asbcp_source_image="
    "asbcp_release_url="
    "asbcp_commit_sha="
    "steps.build.outputs.digest"
    "docker pull"
    "Verify anonymous digest pull"
    "DOCKER_CONFIG="
    "Generate final manifest"
    "asbcp-final-manifest.json"
    "asbcp-release-notes.md"
    "body_path:"
    "fail_on_unmatched_files: true"
    "files:"
    '"schema_id"'
    '"asbcp_version"'
    '"git_tag"'
    '"commit_sha"'
    '"image_ref"'
    '"image_digest"'
    '"api_contract_version"'
    '"anonymous_pull"'
    '"same_digest_proof"'
    '"known_breaking_changes"'
    '"changelog_summary"'
    '"known_risk_status"'
    '"known_risk_status_source"'
    '"runbook_url"'
    '"release_notes"'
    '"body_source"'
    "TAG_RESOLVED_DIGEST"
    "ANONYMOUS_DIGEST"
    "SAME_DIGEST_MATCH"
    "fresh-empty"
    "--changelog-evidence-json"
    "dist/asbcp-changelog-evidence.json"
    "--risk-status-json"
    "dist/asbcp-risk-status.json"
    "--api-contract-version"
    "dist/asbcp-api-contract-version.txt"
    'api_contract_version = Path("dist/asbcp-api-contract-version.txt").read_text'
    '"schema_id": "https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json"'
    '"tag_ref"'
    '"tag_resolved_digest"'
    '"build_push_digest"'
    '"anonymous_digest"'
    'changelog_evidence["known_breaking_changes"]'
    'Changelog summary: {changelog_evidence["changelog_summary"]}'
    'risk_status_evidence["known_risk_status"]'
    'risk_status_evidence["source"]'
    'API contract version: {api_contract_version}'
    'asbcp_version={os.environ["ASBCP_VERSION"]}'
    'Path("dist/asbcp-release-notes.md").write_text(manifest["release_notes"]["body_source"]'
  )
  for token in "${required[@]}"; do
    grep -Fq -- "$token" "$workflow" || fail "release workflow missing: $token"
  done
  if grep -Eq "packages/container/.*/visibility|visibility=public|Set GHCR package public" "$workflow"; then
    fail "release workflow must verify anonymous digest pull instead of patching GHCR package visibility"
  fi
  if grep -Eq "secrets\\.[A-Za-z0-9_]*PAT|PERSONAL_ACCESS_TOKEN|GHCR_PAT" "$workflow"; then
    fail "release workflow must not require a PAT for package visibility or release evidence"
  fi
  if grep -Fxq "      - name: Verify digest pull" "$workflow"; then
    fail "release workflow must use the fresh anonymous docker pull as the digest proof"
  fi
  if grep -Fq "docker manifest inspect" "$workflow"; then
    fail "release workflow must not use docker manifest inspect as release evidence fallback"
  fi
  if grep -Eq '"public_inspect_result"|"version":[[:space:]]*os\\.environ\\["ASBCP_VERSION"\\]|"tag":[[:space:]]*os\\.environ\\["ASBCP_TAG"\\]|"commit":[[:space:]]*os\\.environ\\["GITHUB_SHA"\\]' "$workflow"; then
    fail "release workflow final manifest uses stale field names"
  fi
  if grep -Eq 'pulled_digest|anonymous_pull_digest|ANONYMOUS_PULL_DIGEST|"body_source":[[:space:]]*"dist/asbcp-release-notes\\.md"' "$workflow"; then
    fail "release workflow must record tag_resolved_digest/build_push_digest/anonymous_digest and store release_notes.body_source as full body text"
  fi
  if grep -Fq '"asset": "asbcp-release-notes.md"' "$workflow"; then
    fail "release workflow must not describe release notes body as an uploaded asset"
  fi
  if grep -Fq "runtime_evidence_field" "${ROOT}/docs/release-evidence/release-manifest.json"; then
    fail "final manifest schema must call same_digest_proof image identity evidence, not runtime evidence"
  fi
  if grep -Fq 'ASBCP_VERSION: ${{ steps.version.outputs.version }}' "$workflow"; then
    fail "release workflow asbcp_version must use the v-prefixed git tag"
  fi
  if grep -Fq "BREAKING_CHANGES" "$workflow"; then
    fail "release workflow must parse known_breaking_changes from CHANGELOG.md instead of static BREAKING_CHANGES env"
  fi
  if grep -Eq "def parse_known_breaking_changes|def parse_changelog_summary" "$workflow"; then
    fail "release workflow must reuse scripts/verify-release.sh CHANGELOG parser instead of inline parser functions"
  fi
  if grep -Eq "AgentSmith consumer adoption|AgentSmith release gates?" "$workflow"; then
    fail "release workflow must not gate ASBCP release on AgentSmith consumer adoption"
  fi
  if grep -Eq 'KNOWN_RISK_STATUS:|os\.environ\["KNOWN_RISK_STATUS"\]' "$workflow"; then
    fail "release workflow must derive known_risk_status from docs/RISK_REGISTER.md instead of a static KNOWN_RISK_STATUS string"
  fi
  if grep -Eq "API_CONTRACT_VERSION:[[:space:]]*['\"]?v[0-9]+" "$workflow"; then
    fail "release workflow must derive api_contract_version from docs/contracts/api-contract.md"
  fi

  python3 - "$workflow" <<'PY'
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
}

check_final_manifest_schema() {
  require_cmd python3
  python3 - "${ROOT}/docs/release-evidence/release-manifest.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

schema = manifest.get("final_manifest_schema") or {}
required_fields = {
    "schema_id",
    "asbcp_version",
    "git_tag",
    "commit_sha",
    "image_ref",
    "image_digest",
    "api_contract_version",
    "anonymous_pull",
    "same_digest_proof",
    "known_breaking_changes",
    "changelog_summary",
    "known_risk_status",
    "known_risk_status_source",
    "runbook_url",
    "release_notes",
}
required_nested_fields = {
    "anonymous_pull": {
        "result",
        "tag_ref",
        "image_ref",
        "tag_resolved_digest",
        "build_push_digest",
        "anonymous_digest",
        "docker_config",
        "commands",
    },
    "same_digest_proof": {
        "tag_resolved_digest",
        "build_push_digest",
        "anonymous_digest",
        "matches",
        "source",
    },
    "release_notes": {
        "body_source",
        "github_release_url",
    },
}
failures = []
if schema.get("schema_id") != "https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json":
    failures.append("final_manifest_schema.schema_id must be https://agentsmith.dev/schemas/asbcp/final-manifest.v1.json")
if schema.get("asset") != "asbcp-final-manifest.json":
    failures.append("final_manifest_schema.asset must be asbcp-final-manifest.json")
if schema.get("image_identity_evidence_field") != "same_digest_proof":
    failures.append("final_manifest_schema.image_identity_evidence_field must be same_digest_proof")
if schema.get("risk_status_source") != "docs/RISK_REGISTER.md release_blocking column":
    failures.append("final_manifest_schema.risk_status_source must be docs/RISK_REGISTER.md release_blocking column")
if schema.get("runtime_evidence_field"):
    failures.append("final_manifest_schema must not use runtime_evidence_field for same_digest_proof")
missing = sorted(required_fields - set(schema.get("required_fields") or []))
if missing:
    failures.append("final_manifest_schema.required_fields missing: " + ", ".join(missing))
nested_fields = schema.get("nested_required_fields") or {}
for object_name, fields in required_nested_fields.items():
    missing_nested = sorted(fields - set(nested_fields.get(object_name) or []))
    if missing_nested:
        failures.append(
            "final_manifest_schema.nested_required_fields."
            + object_name
            + " missing: "
            + ", ".join(missing_nested)
        )
for object_name, forbidden_fields in {
    "anonymous_pull": {"pulled_digest", "command"},
    "same_digest_proof": {"anonymous_pull_digest"},
    "release_notes": {"asset", "body_path"},
}.items():
    present_forbidden = sorted(forbidden_fields & set(nested_fields.get(object_name) or []))
    if present_forbidden:
        failures.append(
            "final_manifest_schema.nested_required_fields."
            + object_name
            + " contains stale fields: "
            + ", ".join(present_forbidden)
        )

if failures:
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
PY
}

check_readiness_evidence() {
  require_cmd python3
  python3 - "${ROOT}/docs/release-evidence/release-manifest.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

failures = []
if manifest.get("release_gate") != "scripts/verify-release.sh":
    failures.append("release_gate must be scripts/verify-release.sh")

required_ids = {
    "governance_guard",
    "workflow_hardening",
    "health_ready_smoke",
    "workspace_binding_fixture",
    "workload_lifecycle_smoke",
    "dockerfile_contract",
    "docker_image_build",
    "kubernetes_render",
    "changelog_release_evidence",
    "anonymous_pull",
    "same_digest_proof",
    "risk_register_release_status",
    "final_manifest",
    "raw_storage_exclusion",
    "runner_artifact_classification",
}
seen = set()
for item in manifest.get("required_evidence", []):
    evidence_id = item.get("id", "")
    seen.add(evidence_id)
    status = item.get("status", "")
    if status in {"pending", "deferred"}:
        failures.append(f"{evidence_id}: {status} evidence must not pass full release readiness")
    elif status not in {"added", "workflow-gated"}:
        failures.append(f"{evidence_id}: unsupported evidence status {status!r}")

missing = sorted(required_ids - seen)
if missing:
    failures.append("missing required evidence ids: " + ", ".join(missing))

if failures:
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
PY
}

check_dockerfile_contract() {
  local dockerfile="${ROOT}/manager-service/Dockerfile"
  local legacy_cmd="cmd/""manager"
  local legacy_component="sandbox-""manager"
  local required=(
    "ARG VERSION=dev"
    "ARG VCS_REF=unknown"
    "ARG BUILD_DATE=unknown"
    'ARG ASBCP_BUILD_HTTP_PROXY=""'
    'ARG ASBCP_BUILD_HTTPS_PROXY=""'
    'ARG ASBCP_BUILD_NO_PROXY=""'
    "org.opencontainers.image.title"
    "org.opencontainers.image.version"
    "org.opencontainers.image.revision"
    "org.opencontainers.image.created"
    "-o asbcp ./cmd/asbcp"
    "USER 10001"
    'CMD ["./asbcp"]'
  )
  for token in "${required[@]}"; do
    grep -Fq -- "$token" "$dockerfile" || fail "Dockerfile missing contract token: $token"
  done
  local forbidden=(
    "${legacy_cmd}"
    "${legacy_component}"
    "USER root"
    "ARG HTTP_PROXY"
    "ARG HTTPS_PROXY"
    "ARG http_proxy"
    "ARG https_proxy"
    "ARG NO_PROXY"
    "ARG no_proxy"
    'HTTP_PROXY="${HTTP_PROXY}"'
    'HTTPS_PROXY="${HTTPS_PROXY}"'
    'http_proxy="${http_proxy}"'
    'https_proxy="${https_proxy}"'
    'NO_PROXY="${NO_PROXY}"'
    'no_proxy="${no_proxy}"'
    "192.168."
    "pullot"
    "mirrors.tuna.tsinghua.edu.cn"
    "mirrors.aliyun.com"
    "mirrors.cloud.tencent.com"
    "/etc/apt/sources.list"
  )
  for token in "${forbidden[@]}"; do
    if grep -Fq -- "$token" "$dockerfile"; then
      fail "Dockerfile contains forbidden release token: $token"
    fi
  done

  local release_build_tokens=(
    'asbcp_build_http_proxy="${ASBCP_BUILD_HTTP_PROXY:-}"'
    'asbcp_build_https_proxy="${ASBCP_BUILD_HTTPS_PROXY:-}"'
    'asbcp_build_no_proxy="${ASBCP_BUILD_NO_PROXY:-}"'
    '--build-arg "ASBCP_BUILD_HTTP_PROXY=${asbcp_build_http_proxy}"'
    '--build-arg "ASBCP_BUILD_HTTPS_PROXY=${asbcp_build_https_proxy}"'
    '--build-arg "ASBCP_BUILD_NO_PROXY=${asbcp_build_no_proxy}"'
    '--build-arg "HTTP_PROXY="'
    '--build-arg "HTTPS_PROXY="'
    '--build-arg "http_proxy="'
    '--build-arg "https_proxy="'
    '--build-arg "NO_PROXY="'
    '--build-arg "no_proxy="'
  )
  local release_build_forbidden=(
    "env -u HTTP_PROXY"
    "env -u HTTPS_PROXY"
    "env -u http_proxy"
    "env -u https_proxy"
    "env -u ALL_PROXY"
    "env -u all_proxy"
  )
  local release_build_function
  release_build_function="$(shell_function_stanza build_release_image "${ROOT}/scripts/verify-release.sh")"
  [ -n "$release_build_function" ] || fail "release verifier missing build_release_image function"
  for token in "${release_build_tokens[@]}"; do
    grep -Fq -- "$token" <<<"$release_build_function" || \
      fail "release image build must clear Dockerfile proxy build args: $token"
  done
  for token in "${release_build_forbidden[@]}"; do
    if grep -Fq -- "$token" <<<"$release_build_function"; then
      fail "release image build must allow Docker/BuildKit host proxy env and clear only Dockerfile build args: $token"
    fi
  done
}

check_raw_storage_sdk_dependency_absent() {
  local raw_storage_sdk_prefix="github.com/""mi""nio/"
  if grep -Fq "$raw_storage_sdk_prefix" "${ROOT}/manager-service/go.mod" "${ROOT}/manager-service/go.sum"; then
    fail "unused raw-storage SDK dependency ${raw_storage_sdk_prefix}* must stay out of ASBCP Go module"
  fi
}

render_kustomize_overlays() {
  local version="$1"
  require_cmd kubectl
  local tmpdir="$2"
  local legacy_env_prefix="SANDBOX_""MANAGER"
  local legacy_component="sandbox-""manager"
  local legacy_configmap="manager-""configmap"
  local overlay out
  for overlay in dev staging production; do
    out="${tmpdir}/kustomize-${overlay}.yaml"
    echo "==> kubectl kustomize k8s/overlays/${overlay}"
    kubectl kustomize "${ROOT}/k8s/overlays/${overlay}" > "$out"
    grep -Fq "$ASBCP_CONFIG_PATH" "$out" || fail "${overlay} overlay does not render ${ASBCP_CONFIG_PATH}"
    grep -Fq "app.kubernetes.io/component: asbcp" "$out" || fail "${overlay} overlay missing ASBCP component label"
    case "$overlay" in
      dev) grep -Fq "image: agentsmith-sandbox-control-plane:${version}" "$out" || fail "dev overlay image tag mismatch" ;;
      staging|production) grep -Fq "image: localhost:5001/agentsmith-sandbox-control-plane:${version}" "$out" || fail "${overlay} overlay image tag mismatch" ;;
    esac
    if grep -Eq "/etc/asbcp/config\.yaml|${legacy_component}|${legacy_env_prefix}|${legacy_configmap}" "$out"; then
      fail "${overlay} overlay rendered old ASBCP naming or config path"
    fi
    if [ "$overlay" = "production" ] && grep -Eq '^kind: Ingress$|^[[:space:]]*type: (NodePort|LoadBalancer)$' "$out"; then
      fail "production overlay must be internal-only by default and must not render Ingress, NodePort, or LoadBalancer"
    fi
  done
}

build_release_image() {
  local version="$1"
  local tmpdir="$2"
  require_cmd docker
  local git_sha build_date image_tag label_version image_user image_cmd image_env env_entry
  local asbcp_build_http_proxy asbcp_build_https_proxy asbcp_build_no_proxy
  local http_proxy_state https_proxy_state no_proxy_state
  local docker_build_cmd
  git_sha="$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
  build_date="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  image_tag="agentsmith-sandbox-control-plane:${version}-release-gate"
  asbcp_build_http_proxy="${ASBCP_BUILD_HTTP_PROXY:-}"
  asbcp_build_https_proxy="${ASBCP_BUILD_HTTPS_PROXY:-}"
  asbcp_build_no_proxy="${ASBCP_BUILD_NO_PROXY:-}"
  http_proxy_state="<empty>"
  https_proxy_state="<empty>"
  no_proxy_state="<empty>"
  [ -n "$asbcp_build_http_proxy" ] && http_proxy_state="<set>"
  [ -n "$asbcp_build_https_proxy" ] && https_proxy_state="<set>"
  [ -n "$asbcp_build_no_proxy" ] && no_proxy_state="<set>"

  docker_build_cmd=(
    docker build
    --quiet
    --build-arg "VERSION=${version}"
    --build-arg "VCS_REF=${git_sha}"
    --build-arg "BUILD_DATE=${build_date}"
    --build-arg "ASBCP_BUILD_HTTP_PROXY=${asbcp_build_http_proxy}"
    --build-arg "ASBCP_BUILD_HTTPS_PROXY=${asbcp_build_https_proxy}"
    --build-arg "ASBCP_BUILD_NO_PROXY=${asbcp_build_no_proxy}"
    --build-arg "HTTP_PROXY="
    --build-arg "HTTPS_PROXY="
    --build-arg "http_proxy="
    --build-arg "https_proxy="
    --build-arg "NO_PROXY="
    --build-arg "no_proxy="
    -t "$image_tag"
    -f "${ROOT}/manager-service/Dockerfile"
    "${ROOT}/manager-service"
  )
  echo "==> docker build --quiet --build-arg VERSION=${version} --build-arg VCS_REF=${git_sha} --build-arg BUILD_DATE=${build_date} --build-arg ASBCP_BUILD_HTTP_PROXY=${http_proxy_state} --build-arg ASBCP_BUILD_HTTPS_PROXY=${https_proxy_state} --build-arg ASBCP_BUILD_NO_PROXY=${no_proxy_state} --build-arg HTTP_PROXY= --build-arg HTTPS_PROXY= --build-arg http_proxy= --build-arg https_proxy= --build-arg NO_PROXY= --build-arg no_proxy= -t ${image_tag} -f ${ROOT}/manager-service/Dockerfile ${ROOT}/manager-service"
  "${docker_build_cmd[@]}"

  label_version="$(docker image inspect "$image_tag" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}')"
  [ "$label_version" = "$version" ] || fail "image OCI version label mismatch: ${label_version}"
  image_user="$(docker image inspect "$image_tag" --format '{{ .Config.User }}')"
  [ "$image_user" = "10001" ] || fail "image must run as UID 10001, got ${image_user}"
  image_cmd="$(docker image inspect "$image_tag" --format '{{ json .Config.Cmd }}')"
  [[ "$image_cmd" == *"./asbcp"* ]] || fail "image command must start ./asbcp, got ${image_cmd}"
  image_env="$(docker image inspect "$image_tag" --format '{{ range .Config.Env }}{{ println . }}{{ end }}')"
  while IFS= read -r env_entry; do
    case "$env_entry" in
      ASBCP_BUILD_HTTP_PROXY=*|ASBCP_BUILD_HTTPS_PROXY=*|ASBCP_BUILD_NO_PROXY=*|HTTP_PROXY=*|HTTPS_PROXY=*|http_proxy=*|https_proxy=*|NO_PROXY=*|no_proxy=*)
        fail "image must not persist proxy env: ${env_entry%%=*}"
        ;;
    esac
  done <<<"$image_env"
  echo "$image_tag" > "${tmpdir}/release-image-tag"
}

run_release_fixture_smoke() {
  run bash -c "cd '${ROOT}/manager-service' && go test -count=1 ./internal/observability -run 'TestHealthChecker_HandleHealthz_Returns200|TestHealthChecker_HandleReadyz_AllChecksPassing_Returns200'"
  run bash -c "cd '${ROOT}/manager-service' && go test -count=1 ./internal/k8s -run 'TestBuildExecURL'"
  run bash -c "cd '${ROOT}/manager-service' && go test -count=1 ./internal/workspacebinding -run 'TestEnsureAndGetBindingUsesAFSCPPlan|TestDeleteBinding'"
  run bash -c "cd '${ROOT}/manager-service' && go test -count=1 ./internal/workload -run 'TestBuildPod_UsesAFSCPPlanPaths|TestHandleDeletePodReleasesAFSCPMountAndMarksReleased|TestHandleDeletePodFlushesAFSCPMountBeforeDeletingPod|TestHandleKeepaliveHeartbeatsAFSCPMount|TestHandleExec_PodExistsReturnsError_Returns500|TestHandleExec_PodExistsButExecFails'"
  run bash -c "cd '${ROOT}/manager-service' && go test -tags=integration -count=1 ./integration -run 'TestHTTPStack_HealthAndV1WithAuth|TestHTTPStack_Exec_InvalidBody_Returns400|TestIntegration_FullLifecycle_CreateGetKeepaliveDeleteGet|TestIntegration_FullLifecycle_GetKeepaliveDeleteGet'"
}

if [ "${MODE}" = "changelog-evidence-json" ]; then
  emit_changelog_release_evidence "${CHANGELOG_EVIDENCE_TAG}"
  exit 0
fi

if [ "${MODE}" = "risk-status-json" ]; then
  emit_risk_status_evidence
  exit 0
fi

if [ "${MODE}" = "api-contract-version" ]; then
  read_api_contract_version
  exit 0
fi

run bash "${ROOT}/.github/tests/asbcp-governance-guard.sh"
run bash -n "${ROOT}/scripts/verify-release.sh"
run bash -n "${ROOT}/.github/tests/asbcp-governance-guard.sh"

VERSION="$(read_version)"
[ -n "$VERSION" ] || fail "could not read root VERSION"
check_version_tag_contract "$VERSION"
check_changelog_release_evidence "v${VERSION}"
check_risk_status_evidence
check_api_contract_version_evidence
check_release_workflow_lock_output
check_final_manifest_schema
check_readiness_evidence
check_raw_storage_sdk_dependency_absent

EXPECTED_GO_VERSION="$(awk '$1 == "go" { print $2; exit }' "${ROOT}/manager-service/go.mod")"
[ -n "${EXPECTED_GO_VERSION}" ] || fail "could not read Go version from manager-service/go.mod"

for workflow in "${ROOT}/.github/workflows/ci.yml" "${ROOT}/.github/workflows/release.yml"; do
  grep -Eq "GO_VERSION:[[:space:]]*['\"]?${EXPECTED_GO_VERSION}['\"]?" "${workflow}" || \
    fail "$(basename "${workflow}") must use GO_VERSION ${EXPECTED_GO_VERSION}"
done

if [ "${MODE}" = "quick" ]; then
  echo "==> Quick governance checks passed (not release readiness)"
  exit 0
fi

require_cmd go

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

run bash -c "cd '${ROOT}/manager-service' && go test -count=1 ./internal/app -run 'TestActiveSurfaceUsesCanonicalASBCPIdentifiers|TestRunnerImageIsNotASBCPActiveReleaseSurface|TestRunnerFixtureAllowlistIsDocumented|TestActiveK8sFilenamesUseCanonicalASBCPNames|TestReleaseGateIsSingleAuthority|TestReleaseVerifierCoversBlockingReleaseEvidence|TestCanonicalASBCPConfigPathIsConsistent|TestDockerfileReleaseContract|TestReleaseWorkflowEmitsAgentSmithLockFieldsAndValidatesTag|TestReleaseWorkflowPublishesFinalManifestAfterAnonymousInspect|TestGovernanceGuardCoversTrackedOldNamePaths|TestKustomizeRendersASBCPReleaseControls'"
run_release_fixture_smoke
run bash -c "cd '${ROOT}/manager-service' && go test -tags=short -count=1 ./..."
run bash -c "cd '${ROOT}/manager-service' && CGO_ENABLED=0 go build -o '${TMP_DIR}/asbcp' ./cmd/asbcp"
check_dockerfile_contract
render_kustomize_overlays "$VERSION" "$TMP_DIR"
build_release_image "$VERSION" "$TMP_DIR"

echo "==> ASBCP release gate passed"
