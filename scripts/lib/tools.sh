#!/usr/bin/env bash
set -euo pipefail

TOOLS_DIR_REL="tools/bin/linux-amd64"
TOOLS_LOCK_REL="tools/vendor/tools.lock.json"

tools_bin_dir() {
  local root="$1"
  echo "${root}/${TOOLS_DIR_REL}"
}

tools_lock_path() {
  local root="$1"
  echo "${root}/${TOOLS_LOCK_REL}"
}

tools_where() {
  local root="$1"
  local bin
  bin="$(tools_bin_dir "$root")"
  echo "tools bin: $bin"
  echo "skopeo: $(tools_resolve "$root" skopeo || echo missing)"
  echo "kubectl: $(tools_resolve "$root" kubectl || echo missing)"
  echo "kustomize: $(tools_resolve "$root" kustomize || echo missing)"
  echo "jq: $(tools_resolve "$root" jq || echo missing)"
  echo "yq: $(tools_resolve "$root" yq || echo missing)"
}

tools_resolve() {
  local root="$1"
  local name="$2"

  local p
  p="$(tools_bin_dir "$root")/${name}"
  if [ -x "$p" ]; then
    echo "$p"
    return 0
  fi
  if command -v "$name" >/dev/null 2>&1; then
    command -v "$name"
    return 0
  fi
  return 1
}

tools_fetch() {
  local root="$1"
  shift || true

  local proxy_mode="auto"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --proxy)
        proxy_mode="${2:-auto}"
        shift 2
        ;;
      -h|--help)
        echo "Usage: ./sbx tools fetch [--proxy auto|on|off]"
        return 0
        ;;
      *)
        echo "Unknown option: $1" >&2
        return 1
        ;;
    esac
  done

  local enabled
  enabled="$(proxy_effective_enabled "$proxy_mode")"
  local penv
  penv="$(proxy_env_host "$enabled")"

  local bindir
  bindir="$(tools_bin_dir "$root")"
  mkdir -p "$bindir"
  mkdir -p "$(dirname "$(tools_lock_path "$root")")"

  if ! command -v curl >/dev/null 2>&1; then
    die "curl is required for tools fetch"
  fi
  if ! command -v sha256sum >/dev/null 2>&1; then
    die "sha256sum is required for tools fetch"
  fi

  # Versions are pinned; sha256 is computed after download and recorded.
  # Users may adjust versions in tools/vendor/tools.sources.json.
  local sources="${root}/tools/vendor/tools.sources.json"
  if [ ! -f "$sources" ]; then
    die "Missing ${sources}"
  fi

  "$root"/scripts/lib/tools_fetch_impl.sh "$sources" "$bindir" "$(tools_lock_path "$root")" "$penv"
}

tools_verify() {
  local root="$1"
  local lock
  lock="$(tools_lock_path "$root")"
  if [ ! -f "$lock" ]; then
    die "Missing lock file: $lock (run: ./sbx tools fetch)"
  fi
  "$root"/scripts/lib/tools_verify_impl.sh "$lock" "$(tools_bin_dir "$root")"
}

