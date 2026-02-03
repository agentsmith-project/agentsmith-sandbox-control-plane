#!/usr/bin/env bash
set -euo pipefail

sandbox_list() {
  local _root="$1"
  shift || true

  local ns="sandbox"
  local selector="app=llm-sandbox"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --namespace) ns="${2:-sandbox}"; shift 2 ;;
      --selector) selector="${2:-app=llm-sandbox}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx sandbox list [--namespace sandbox] [--selector app=llm-sandbox]"
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
          (.status.phase // ""),
          (.metadata.annotations["sandbox/ttlSeconds"] // ""),
          (.metadata.annotations["sandbox/lastActiveAt"] // ""),
          (.metadata.annotations["sandbox/expiresAt"] // "")
        ] | @tsv' | column -t || true
  else
    kubectl -n "$ns" get pods -l "$selector" -o wide || true
  fi
}

sandbox_cleanup() {
  local _root="$1"
  shift || true

  local ns="sandbox"
  local selector="app=llm-sandbox"
  local dry_run="true"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --namespace) ns="${2:-sandbox}"; shift 2 ;;
      --selector) selector="${2:-app=llm-sandbox}"; shift 2 ;;
      --dry-run) dry_run="true"; shift ;;
      --force) dry_run="false"; shift ;;
      -h|--help)
        echo "Usage: ./sbx sandbox cleanup [--namespace sandbox] [--selector app=llm-sandbox] [--dry-run|--force]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  if [ "$dry_run" = "true" ]; then
    log_info "Dry-run cleanup (no deletion). Use --force to delete."
  fi

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
          (.metadata.annotations["sandbox/expiresAt"] // ""),
          (.metadata.annotations["sandbox/lastActiveAt"] // ""),
          (.metadata.annotations["sandbox/ttlSeconds"] // "900")
        ] | @tsv' | while IFS=$'\t' read -r name expiresAt lastActiveAt ttl; do
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
          if [ "$dry_run" = "true" ]; then
            echo "[dry-run] delete $name (no valid timestamps)"
          else
            echo "delete $name (no valid timestamps)"
            kubectl -n "$ns" delete pod "$name" --grace-period=0 --force || true
          fi
          continue
        fi

        if [ $(( now + skew )) -ge "$expire_epoch" ]; then
          if [ "$dry_run" = "true" ]; then
            echo "[dry-run] delete $name (expired)"
          else
            echo "delete $name (expired)"
            kubectl -n "$ns" delete pod "$name" --grace-period=0 --force || true
          fi
        fi
      done
}
