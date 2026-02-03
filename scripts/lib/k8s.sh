#!/usr/bin/env bash
set -euo pipefail

k8s_scripts_dir() {
  local root="$1"
  echo "${root}/k8s/scripts"
}

k8s_update_images() {
  local root="$1"
  shift || true

  local registry=""
  local project=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --registry) registry="${2:-}"; shift 2 ;;
      --project) project="${2:-}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx k8s update-images --registry HOST:PORT --project NAME"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  [ -n "$registry" ] || die "--registry is required"
  [ -n "$project" ] || die "--project is required"

  (cd "${root}/k8s" && REGISTRY="$registry" HARBOR_PROJECT="$project" ./scripts/update-images.sh)
}

k8s_deploy() {
  local root="$1"
  local env="${2:-}"
  [ -n "$env" ] || die "Usage: ./sbx k8s deploy dev|staging|production"
  local tools_bin
  tools_bin="$(tools_bin_dir "$root")"
  (cd "${root}/k8s" && PATH="${tools_bin}:$PATH" ./scripts/deploy.sh "$env")
}

k8s_undeploy() {
  local root="$1"
  local env="${2:-}"
  [ -n "$env" ] || die "Usage: ./sbx k8s undeploy dev|staging|production"
  local tools_bin
  tools_bin="$(tools_bin_dir "$root")"
  (cd "${root}/k8s" && PATH="${tools_bin}:$PATH" ./scripts/undeploy.sh "$env")
}

k8s_verify() {
  local root="$1"
  local env="${2:-}"
  [ -n "$env" ] || die "Usage: ./sbx k8s verify dev|staging|production"
  local tools_bin
  tools_bin="$(tools_bin_dir "$root")"
  (cd "${root}/k8s" && PATH="${tools_bin}:$PATH" ./scripts/verify.sh "$env")
}

k8s_health() {
  local root="$1"
  local tools_bin
  tools_bin="$(tools_bin_dir "$root")"
  (cd "${root}/k8s" && PATH="${tools_bin}:$PATH" ./scripts/health-check.sh)
}

k8s_backup() {
  local root="$1"
  shift || true

  local out=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --out) out="${2:-}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx k8s backup --out DIR"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  [ -n "$out" ] || die "--out is required"
  local tools_bin
  tools_bin="$(tools_bin_dir "$root")"
  (cd "${root}/k8s" && PATH="${tools_bin}:$PATH" BACKUP_DIR="$out" ./scripts/backup.sh)
}

k8s_setup_harbor_secret() {
  local root="$1"
  shift || true

  local registry=""
  local project=""
  local username=""
  local password=""
  local namespace="sandbox-system"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --registry) registry="${2:-}"; shift 2 ;;
      --project) project="${2:-}"; shift 2 ;;
      --username) username="${2:-}"; shift 2 ;;
      --password) password="${2:-}"; shift 2 ;;
      --namespace) namespace="${2:-sandbox-system}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx k8s harbor-secret --registry HOST:PORT --project NAME --username USER --password PASS [--namespace sandbox-system]"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done

  [ -n "$registry" ] || die "--registry is required"
  [ -n "$project" ] || die "--project is required"
  [ -n "$username" ] || die "--username is required"
  [ -n "$password" ] || die "--password is required"

  (cd "${root}/k8s" && REGISTRY="$registry" HARBOR_PROJECT="$project" REGISTRY_USERNAME="$username" REGISTRY_PASSWORD="$password" NAMESPACE="$namespace" ./scripts/setup-harbor-secret.sh)
}
