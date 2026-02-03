#!/usr/bin/env bash
set -euo pipefail

warn() { echo "WARN: $*" >&2; }
die() { echo "ERROR: $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Switch Docker "data-root" (DANGEROUS).

This script:
  1) stops Docker (systemd)
  2) updates ONLY the "data-root" field in /etc/docker/daemon.json
  3) starts Docker

It does NOT modify other daemon.json fields (proxy, mirrors, runtimes, etc).

Usage:
  sudo ./dangerous-system-tools/switch-docker-dataroot.sh --to /data/docker

Options:
  --to PATH        Target Docker data-root (absolute path recommended)
  --force          Skip interactive confirmation
  --dry-run        Print what would change; do not stop/start Docker
  --docker-timeout SECONDS
                   Timeout for querying docker info (default: 5)
  -h, --help       Show help

Notes:
  - Only one Docker root dir is active at a time; switching makes the other one "disappear" to dockerd.
  - If PATH does not exist, Docker will create it (but you likely want it pre-created and on the right filesystem).
EOF
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root=""
if command -v git >/dev/null 2>&1; then
  repo_root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null || true)"
fi

resolve_jq() {
  if command -v jq >/dev/null 2>&1; then
    command -v jq
    return 0
  fi
  if [ -n "$repo_root" ] && [ -x "${repo_root}/tools/bin/linux-amd64/jq" ]; then
    echo "${repo_root}/tools/bin/linux-amd64/jq"
    return 0
  fi
  die "jq not found (install jq or run ./sbx tools fetch to get tools/bin/linux-amd64/jq)"
}

need_root() {
  if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    die "Run as root (use: sudo $0 ...)"
  fi
}

run_with_timeout() {
  local seconds="$1"
  shift || true

  if command -v timeout >/dev/null 2>&1; then
    timeout "${seconds}s" "$@"
    return $?
  fi

  if command -v python3 >/dev/null 2>&1; then
    local s="$seconds"
    python3 - "$s" "$@" <<'PY'
import subprocess, sys
timeout_s=float(sys.argv[1])
cmd=sys.argv[2:]
try:
    p=subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=timeout_s, check=False, text=True)
    sys.stdout.write(p.stdout)
    sys.exit(p.returncode)
except subprocess.TimeoutExpired:
    sys.exit(124)
PY
    return $?
  fi

  "$@"
}

systemctl_require() {
  command -v systemctl >/dev/null 2>&1 || die "systemctl not found (this tool expects systemd-managed Docker)"
}

docker_running_rootdir() {
  if command -v docker >/dev/null 2>&1; then
    local out
    out="$(run_with_timeout "${docker_timeout:-5}" docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)"
    if [ -z "$out" ]; then
      return 0
    fi
    echo "$out"
  fi
}

daemon_json="/etc/docker/daemon.json"

to=""
force="false"
dry_run="false"
docker_timeout="5"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --to) to="${2:-}"; shift 2 ;;
    --force) force="true"; shift ;;
    --dry-run) dry_run="true"; shift ;;
    --docker-timeout) docker_timeout="${2:-5}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "Unknown option: $1 (use --help)" ;;
  esac
done

[ -n "$to" ] || die "--to is required (use --help)"

need_root
systemctl_require

jqbin="$(resolve_jq)"

current_running="$(docker_running_rootdir)"
if [ -z "$current_running" ] && command -v docker >/dev/null 2>&1; then
  warn "Unable to query docker info (daemon down or command timed out)."
fi
configured_current=""
if [ -f "$daemon_json" ]; then
  if "$jqbin" -e . "$daemon_json" >/dev/null 2>&1; then
    configured_current="$("$jqbin" -r '."data-root" // ""' "$daemon_json")"
  else
    warn "$daemon_json exists but is not valid JSON; will be replaced with minimal JSON containing only data-root"
  fi
fi

echo "Current docker info rootdir : ${current_running:-<docker not running or not installed>}"
echo "Configured daemon.json root : ${configured_current:-<unset>}"
echo "Target rootdir              : $to"

if [ "$dry_run" = "true" ]; then
  echo "DRY RUN: no changes applied."
  exit 0
fi

if [ "$force" != "true" ]; then
  echo
  echo "DANGER: This will STOP Docker and switch the active data-root."
  echo "Type exactly: SWITCH"
  read -r confirm
  [ "$confirm" = "SWITCH" ] || die "Aborted"
fi

echo "Stopping Docker..."
systemctl stop docker || true
systemctl stop docker.socket 2>/dev/null || true

echo "Updating $daemon_json (only: data-root)..."
tmp="$(mktemp)"
trap 'rm -f "$tmp" >/dev/null 2>&1 || true' EXIT

if [ -f "$daemon_json" ] && "$jqbin" -e . "$daemon_json" >/dev/null 2>&1; then
  "$jqbin" --arg dr "$to" '. + {"data-root": $dr}' "$daemon_json" >"$tmp"
else
  "$jqbin" -n --arg dr "$to" '{"data-root": $dr}' >"$tmp"
fi

if [ -f "$daemon_json" ]; then
  backup="${daemon_json}.bak.$(date +%Y%m%d-%H%M%S)"
  cp -a "$daemon_json" "$backup"
  echo "Backup written: $backup"
fi

install -m 600 -o root -g root "$tmp" "$daemon_json"

echo "Starting Docker..."
systemctl start docker

echo "Verifying..."
new_running="$(docker_running_rootdir)"
echo "DockerRootDir now: ${new_running:-<unknown>}"
if [ -n "$new_running" ] && [ "$new_running" != "$to" ]; then
  warn "DockerRootDir does not match target. Check 'systemctl status docker' and '$daemon_json'."
fi

echo "Done."
