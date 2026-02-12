#!/usr/bin/env bash
set -euo pipefail

kind_require() {
  if ! command -v kind >/dev/null 2>&1; then
    die "kind is required"
  fi
  if ! tools_resolve "$ROOT_DIR" kubectl >/dev/null 2>&1; then
    die "kubectl is required (run: ./sbx tools fetch)"
  fi
}

dev_up() {
  local root="$1"
  shift || true

  local cluster="sandbox-cluster"
  local harbor_ca=""
  local proxy_mode="auto"
  local force="false"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --cluster) cluster="${2:-sandbox-cluster}"; shift 2 ;;
      --harbor-ca) harbor_ca="${2:-}"; shift 2 ;;
      --proxy) proxy_mode="${2:-auto}"; shift 2 ;;
      --force) force="true"; shift ;;
      -h|--help)
        echo "Usage: ./sbx dev up [--cluster NAME] [--harbor-ca PATH|auto] [--proxy auto|on|off] [--force]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  kind_require

  if kind get clusters | grep -q "^${cluster}$"; then
    if [ "$force" = "true" ]; then
      log_warn "Kind cluster exists, deleting: $cluster"
      kind delete cluster --name "$cluster"
    else
      die "Kind cluster already exists: $cluster (use --force to recreate)"
    fi
  fi

  local enabled
  enabled="$(proxy_effective_enabled "$proxy_mode")"
  local penv
  penv="$(proxy_env_host "$enabled")"
  eval "$penv"

  log_info "Creating kind cluster: $cluster (proxy=$enabled)"
  kind create cluster --name "$cluster" --wait 60s

  if [ -n "$harbor_ca" ]; then
    if [ "$harbor_ca" = "auto" ]; then
      local reg="${REGISTRY:-${HARBOR_REGISTRY:-}}"
      [ -n "$reg" ] || die "--harbor-ca auto requires REGISTRY or HARBOR_REGISTRY"
      harbor_ca="$root/secrets/harbor-ca.crt"
      mkdir -p "$(dirname "$harbor_ca")"
      if [ -s "$harbor_ca" ]; then
        log_info "Using existing Harbor CA: $harbor_ca"
      else
        dev_harbor_fetch_ca "$reg" "$harbor_ca"
      fi
    fi
    [ -f "$harbor_ca" ] || die "CA file not found: $harbor_ca"
    dev_kind_install_ca "$cluster" "$harbor_ca"
  fi

  # Default dev registry config: allow user override via env or flags in k8s update-images
  local reg="${REGISTRY:-${HARBOR_REGISTRY:-localhost:5001}}"
  local proj="${HARBOR_PROJECT:-agentsmith}"

  if [[ "$reg" == "localhost:"* ]] || [[ "$reg" == "127.0.0.1:"* ]]; then
    log_info "Building images into local docker (pull uses proxy; Dockerfile build does not)..."
    DOCKER_IMAGE_HTTP_PROXY="" DOCKER_IMAGE_HTTPS_PROXY="" DOCKER_IMAGE_NO_PROXY="" \
      "${root}/sbx" images build --pull-proxy "$proxy_mode" --build-proxy off
    local manager_ver runner_ver
    manager_ver="$(cat "${root}/manager-service/VERSION" 2>/dev/null || echo dev)"
    runner_ver="$(cat "${root}/images/runner/VERSION" 2>/dev/null || echo dev)"
    log_info "Loading images into Kind cluster..."
    kind load docker-image "sandbox-manager:${manager_ver}" "sandbox-runner:${runner_ver}" --name "$cluster"
  else
    if [ -z "${HARBOR_USERNAME:-}" ] || [ -z "${HARBOR_PASSWORD:-}" ]; then
      die "Remote registry requires HARBOR_USERNAME and HARBOR_PASSWORD"
    fi
    log_info "Building and pushing images to registry (source=archive; build-proxy=off)..."
    DOCKER_IMAGE_HTTP_PROXY="" DOCKER_IMAGE_HTTPS_PROXY="" DOCKER_IMAGE_NO_PROXY="" \
      "${root}/sbx" images push harbor --registry "$reg" --project "$proj" --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD" --proxy off --source archive --build-proxy off
  fi

  log_info "Updating k8s images for dev overlay..."
  "${root}/sbx" k8s update-images --registry "$reg" --project "$proj"

  if [ -n "${HARBOR_USERNAME:-}" ] && [ -n "${HARBOR_PASSWORD:-}" ]; then
    log_info "Creating imagePullSecret for registry..."
    "${root}/sbx" k8s harbor-secret --registry "$reg" --project "$proj" --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD"
  fi

  log_info "Deploying to dev..."
  "${root}/sbx" k8s deploy dev

  log_info "Dev environment ready"
}

dev_down() {
  local _root="$1"
  shift || true

  local cluster="sandbox-cluster"
  local force="false"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --cluster) cluster="${2:-sandbox-cluster}"; shift 2 ;;
      --force) force="true"; shift ;;
      -h|--help)
        echo "Usage: ./sbx dev down [--cluster NAME] [--force]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  kind_require
  if ! kind get clusters | grep -q "^${cluster}$"; then
    log_warn "Kind cluster not found: $cluster"
    return 0
  fi

  if [ "$force" != "true" ]; then
    die "Refusing to delete cluster without --force"
  fi

  kind delete cluster --name "$cluster"
  log_info "Deleted kind cluster: $cluster"
}

dev_reset() {
  local root="$1"
  shift || true

  local cluster="sandbox-cluster"
  local pass_through=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --cluster)
        cluster="${2:-sandbox-cluster}"
        pass_through+=("$1" "$2")
        shift 2
        ;;
      *)
        pass_through+=("$1")
        shift
        ;;
    esac
  done

  "${root}/sbx" dev down --force --cluster "$cluster" || true
  "${root}/sbx" dev up --force "${pass_through[@]}"
}

dev_status() {
  local _root="$1"
  shift || true

  local cluster="sandbox-cluster"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --cluster) cluster="${2:-sandbox-cluster}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx dev status [--cluster NAME]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  kind_require
  if ! kind get clusters | grep -q "^${cluster}$"; then
    die "Kind cluster not found: $cluster"
  fi
  local k
  k="$(tools_resolve "$ROOT_DIR" kubectl)"
  "$k" cluster-info --context "kind-${cluster}" || true
  "$k" get pods -n sandbox-system --context "kind-${cluster}" || true
  "$k" get pods -n sandbox --context "kind-${cluster}" || true
}

dev_kind_install_ca() {
  local cluster="$1"
  local ca_file="$2"
  local node="${cluster}-control-plane"

  log_info "Installing CA into kind node: $node"
  local ca_content
  ca_content="$(cat "$ca_file")"

  docker exec "$node" sh -c "mkdir -p /usr/local/share/ca-certificates/harbor"
  docker exec "$node" sh -c "cat > /usr/local/share/ca-certificates/harbor/ca.crt <<'EOF'
${ca_content}
EOF"
  docker exec "$node" sh -c "update-ca-certificates"

  log_info "CA installed"
}

dev_harbor_fetch_ca() {
  local registry="$1"
  local out="$2"

  if ! command -v curl >/dev/null 2>&1; then
    die "curl is required to fetch Harbor CA"
  fi
  if ! command -v openssl >/dev/null 2>&1; then
    die "openssl is required to fetch Harbor CA"
  fi

  local host="$registry"
  host="${host#https://}"
  host="${host#http://}"
  host="${host%/}"

  local url="https://${host}/api/v2.0/systeminfo/getcert"
  log_info "Fetching Harbor CA (preferred): $url"
  # Some Harbor deployments disable this endpoint; fallback to fetching the presented server cert.
  if curl -fsSLk "$url" -o "$out" 2>/dev/null; then
    return 0
  fi

  local h="$host"
  local port="443"
  if [[ "$h" == *:* ]]; then
    port="${h##*:}"
    h="${h%:*}"
  fi

  log_warn "Harbor getcert endpoint unavailable; falling back to server certificate via openssl (${h}:${port})"
  openssl s_client -showcerts -connect "${h}:${port}" -servername "$h" </dev/null 2>/dev/null \
    | awk '/-----BEGIN CERTIFICATE-----/{flag=1} flag{print} /-----END CERTIFICATE-----/{exit}' \
    >"$out"
}
