#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="release"
ASBCP_CONFIG_PATH="/etc/asbcp/asbcp-config.yaml"

if [ "${1:-}" = "--quick" ]; then
  MODE="quick"
  shift
fi

if [ "$#" -ne 0 ]; then
  echo "usage: bash scripts/verify-release.sh [--quick]" >&2
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

check_release_workflow_lock_output() {
  local workflow="${ROOT}/.github/workflows/release.yml"
  local required=(
    "Validate release version"
    'v${version}'
    "steps.version.outputs.version"
    "steps.version.outputs.image_tag"
    "asbcp_version="
    "asbcp_source_image="
    "asbcp_release_url="
    "asbcp_commit_sha="
    "steps.build.outputs.digest"
    "docker pull"
    "Verify anonymous digest pull"
    "DOCKER_CONFIG="
    "docker manifest inspect"
    "Generate final manifest"
    "asbcp-final-manifest.json"
    "asbcp-release-notes.md"
    "body_path:"
    "fail_on_unmatched_files: true"
    "files:"
    '"version"'
    '"tag"'
    '"commit"'
    '"image_digest"'
    '"api_contract_version"'
    '"public_inspect_result"'
  )
  for token in "${required[@]}"; do
    grep -Fq "$token" "$workflow" || fail "release workflow missing: $token"
  done
  if grep -Eq "packages/container/.*/visibility|visibility=public|Set GHCR package public" "$workflow"; then
    fail "release workflow must verify anonymous digest pull instead of patching GHCR package visibility"
  fi
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
    "digest_pull",
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

run bash "${ROOT}/.github/tests/asbcp-governance-guard.sh"
run bash -n "${ROOT}/scripts/verify-release.sh"
run bash -n "${ROOT}/.github/tests/asbcp-governance-guard.sh"

VERSION="$(read_version)"
[ -n "$VERSION" ] || fail "could not read root VERSION"
check_version_tag_contract "$VERSION"
check_release_workflow_lock_output
check_readiness_evidence

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

run bash -c "cd '${ROOT}/manager-service' && go test -count=1 ./internal/app -run 'TestActiveSurfaceUsesCanonicalASBCPIdentifiers|TestRunnerImageIsNotASBCPActiveReleaseSurface|TestRunnerFixtureAllowlistIsDocumented|TestLegacyManagerFilenamesAreCompatibilityOnly|TestReleaseGateIsSingleAuthority|TestReleaseVerifierCoversBlockingReleaseEvidence|TestCanonicalASBCPConfigPathIsConsistent|TestDockerfileReleaseContract|TestReleaseWorkflowEmitsAgentSmithLockFieldsAndValidatesTag|TestKustomizeRendersASBCPReleaseControls'"
run_release_fixture_smoke
run bash -c "cd '${ROOT}/manager-service' && go test -tags=short -count=1 ./..."
run bash -c "cd '${ROOT}/manager-service' && CGO_ENABLED=0 go build -o '${TMP_DIR}/asbcp' ./cmd/asbcp"
check_dockerfile_contract
render_kustomize_overlays "$VERSION" "$TMP_DIR"
build_release_image "$VERSION" "$TMP_DIR"

echo "==> ASBCP release gate passed"
