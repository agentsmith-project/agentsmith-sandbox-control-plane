#!/usr/bin/env bash
set -euo pipefail

workload_list() {
  local _root="$1"
  shift || true

  local ns="${WORKLOAD_NAMESPACE:-sandbox-workloads}"
  local selector="${WORKLOAD_SELECTOR:-app=managed-workload}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --namespace) ns="${2:-$ns}"; shift 2 ;;
      --selector) selector="${2:-$selector}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx workloads list [--namespace sandbox-workloads] [--selector app=managed-workload]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  if command -v jq >/dev/null 2>&1; then
    kubectl -n "$ns" get pods -l "$selector" -o json | jq -r '
      .items[]
      | [
          .metadata.name,
          (.metadata.labels["workload_id"] // ""),
          (.metadata.labels["workspace_id"] // ""),
          (.metadata.labels["project_id"] // ""),
          (.status.phase // ""),
          (.metadata.annotations["expires_at"] // ""),
          (.metadata.annotations["last_activity_at"] // ""),
          (.metadata.annotations["workload/maxExpiresAt"] // "")
        ] | @tsv' | column -t || true
  else
    kubectl -n "$ns" get pods -l "$selector" -o wide || true
  fi
}

workload_expired() {
  local _root="$1"
  shift || true

  local ns="${WORKLOAD_NAMESPACE:-sandbox-workloads}"
  local selector="${WORKLOAD_SELECTOR:-app=managed-workload}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --namespace) ns="${2:-$ns}"; shift 2 ;;
      --selector) selector="${2:-$selector}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx workloads expired [--namespace sandbox-workloads] [--selector app=managed-workload]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  log_info "Listing expired ASBCP workload candidates only."
  log_info "Use the ASBCP workload delete API for cleanup so AFSCP release and storage flush run."

  local now
  now="$(date -u +%s)"
  local skew="${NOW_SKEW_SECONDS:-5}"

  local jqbin
  jqbin="$(tools_resolve "$_root" jq || true)"
  if [ -z "$jqbin" ]; then
    jqbin="$(command -v jq 2>/dev/null || true)"
  fi
  [ -n "$jqbin" ] || die "jq is required (run: ./sbx tools fetch or install jq)"

  kubectl -n "$ns" get pods -l "$selector" -o json | \
    "$jqbin" -r '
      .items[]
      | [
          .metadata.name,
          (.metadata.labels["workload_id"] // ""),
          (.metadata.labels["workspace_id"] // ""),
          (.metadata.labels["project_id"] // ""),
          (.metadata.annotations["expires_at"] // ""),
          (.metadata.annotations["last_activity_at"] // ""),
          (.metadata.annotations["workload/idleTimeoutSec"] // "1800")
        ] | @tsv' | while IFS=$'\t' read -r name workload_id workspace_id project_id expiresAt lastActiveAt ttl; do
        local expire_epoch=""
        if [ -n "$expiresAt" ]; then
          expire_epoch="$(date -u -d "$expiresAt" +%s 2>/dev/null || true)"
        fi
        if [ -z "$expire_epoch" ] && [ -n "$lastActiveAt" ]; then
          local last_epoch=""
          last_epoch="$(date -u -d "$lastActiveAt" +%s 2>/dev/null || true)"
          if [ -n "$last_epoch" ]; then
            expire_epoch=$(( last_epoch + ttl ))
          fi
        fi

        if [ -z "$expire_epoch" ]; then
          printf '%s\t%s\t%s\t%s\t%s\n' "$name" "$workload_id" "$workspace_id" "$project_id" "missing valid expiry"
          continue
        fi

        if [ $(( now + skew )) -ge "$expire_epoch" ]; then
          printf '%s\t%s\t%s\t%s\t%s\n' "$name" "$workload_id" "$workspace_id" "$project_id" "expired"
        fi
      done
}
