#!/usr/bin/env bash
set -euo pipefail

args_take_value() {
  local flag="$1"
  local value="${2:-}"
  if [ -z "$value" ]; then
    return 1
  fi
  printf "%s" "$value"
  return 0
}

args_bool_from_env() {
  local val="${1:-}"
  case "$val" in
    1|true|TRUE|yes|YES|y|Y) echo "true" ;;
    0|false|FALSE|no|NO|n|N|"") echo "false" ;;
    *) echo "false" ;;
  esac
}

args_require() {
  local name="$1"
  local value="${2:-}"
  if [ -z "$value" ]; then
    echo "missing"
    return 1
  fi
}

