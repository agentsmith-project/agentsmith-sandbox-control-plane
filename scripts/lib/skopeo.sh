#!/usr/bin/env bash
set -euo pipefail

skopeo_path() {
  local root="$1"
  tools_resolve "$root" skopeo
}

skopeo_inspect() {
  local root="$1"
  local transport_ref="$2"
  shift 2 || true
  local sk
  sk="$(skopeo_path "$root")" || die "skopeo not found (run: ./sbx tools fetch)"
  if [ "${SBX_SKOPEO_NO_PROXY:-}" = "true" ]; then
    env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy -u ALL_PROXY -u all_proxy -u NO_PROXY -u no_proxy \
      "$sk" inspect "$@" "$transport_ref"
  else
    "$sk" inspect "$@" "$transport_ref"
  fi
}

skopeo_copy() {
  local root="$1"
  local src="$2"
  local dest="$3"
  shift 3 || true
  local sk
  sk="$(skopeo_path "$root")" || die "skopeo not found (run: ./sbx tools fetch)"
  if [ "${SBX_SKOPEO_NO_PROXY:-}" = "true" ]; then
    env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy -u ALL_PROXY -u all_proxy -u NO_PROXY -u no_proxy \
      "$sk" copy "$@" "$src" "$dest"
  else
    "$sk" copy "$@" "$src" "$dest"
  fi
}
