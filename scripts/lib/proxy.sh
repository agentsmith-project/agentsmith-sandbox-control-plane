#!/usr/bin/env bash
set -euo pipefail

proxy_parse_mode() {
  local mode="${1:-auto}"
  case "$mode" in
    auto|on|off) echo "$mode" ;;
    *) echo "auto" ;;
  esac
}

proxy_effective_enabled() {
  local mode
  mode="$(proxy_parse_mode "${1:-auto}")"

  if [ "$mode" = "on" ]; then
    echo "true"
    return 0
  fi
  if [ "$mode" = "off" ]; then
    echo "false"
    return 0
  fi

  if [ -n "${HTTP_PROXY:-}" ] || [ -n "${HTTPS_PROXY:-}" ]; then
    echo "true"
    return 0
  fi
  echo "false"
}

proxy_env_host() {
  # Host-side proxy env (for curl/skopeo/kind). Uses HTTP_PROXY/HTTPS_PROXY/NO_PROXY.
  local enabled="$1"
  if [ "$enabled" = "true" ]; then
    echo "export HTTP_PROXY=${HTTP_PROXY:-}; export HTTPS_PROXY=${HTTPS_PROXY:-}; export http_proxy=${http_proxy:-${HTTP_PROXY:-}}; export https_proxy=${https_proxy:-${HTTPS_PROXY:-}}; export ALL_PROXY=${ALL_PROXY:-}; export all_proxy=${all_proxy:-${ALL_PROXY:-}}; export NO_PROXY=${NO_PROXY:-}; export no_proxy=${no_proxy:-${NO_PROXY:-}}"
  else
    echo "export HTTP_PROXY=; export HTTPS_PROXY=; export http_proxy=; export https_proxy=; export ALL_PROXY=; export all_proxy=; export NO_PROXY=${NO_PROXY:-}; export no_proxy=${no_proxy:-${NO_PROXY:-}}"
  fi
}

proxy_env_build_args() {
  # Build-time proxy args; only DOCKER_IMAGE_* is used (never fall back to host proxy vars).
  # This ensures "docker pull uses proxy but Dockerfile build does not" can be enforced.
  local img_http="${DOCKER_IMAGE_HTTP_PROXY:-}"
  local img_https="${DOCKER_IMAGE_HTTPS_PROXY:-}"
  local img_no="${DOCKER_IMAGE_NO_PROXY:-}"

  local args=()
  if [ -n "$img_http" ]; then args+=("--build-arg" "HTTP_PROXY=$img_http" "--build-arg" "http_proxy=$img_http"); fi
  if [ -n "$img_https" ]; then args+=("--build-arg" "HTTPS_PROXY=$img_https" "--build-arg" "https_proxy=$img_https"); fi
  if [ -n "$img_no" ]; then args+=("--build-arg" "NO_PROXY=$img_no" "--build-arg" "no_proxy=$img_no"); fi
  printf "%s " "${args[@]}"
}
