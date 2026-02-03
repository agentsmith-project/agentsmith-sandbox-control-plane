#!/usr/bin/env bash
set -euo pipefail

images_build() {
  local root="$1"
  shift || true

  local pull_proxy_mode="auto"
  local build_proxy_mode="auto"
  local platform="linux/amd64"
  local builder=""
  local only=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --proxy)
        # Back-compat within this refactor: --proxy affects pull-proxy only.
        pull_proxy_mode="${2:-auto}"
        shift 2
        ;;
      --pull-proxy) pull_proxy_mode="${2:-auto}"; shift 2 ;;
      --build-proxy) build_proxy_mode="${2:-auto}"; shift 2 ;;
      --platform) platform="${2:-linux/amd64}"; shift 2 ;;
      --builder) builder="${2:-}"; shift 2 ;;
      --only) only="${2:-}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx images build [--pull-proxy auto|on|off] [--build-proxy auto|on|off] [--platform linux/amd64] [--only manager|runner|gc]"
        return 0
        ;;
      *)
        die "Unknown option: $1"
        ;;
    esac
  done

  docker_buildx_require
  local pull_enabled
  pull_enabled="$(proxy_effective_enabled "$pull_proxy_mode")"
  if [ -z "$builder" ]; then
    if [ "$pull_enabled" = "true" ]; then
      builder="sbx-builder-proxy"
    else
      builder="sbx-builder"
    fi
  fi
  docker_ensure_builder "$builder"

  local build_proxy_enabled
  build_proxy_enabled="$(proxy_effective_enabled "$build_proxy_mode")"
  local build_args=""
  if [ "$build_proxy_enabled" = "true" ]; then
    build_args="$(proxy_env_build_args)"
  else
    # Explicitly override Docker's auto proxy build-args injection.
    build_args="--build-arg HTTP_PROXY= --build-arg HTTPS_PROXY= --build-arg http_proxy= --build-arg https_proxy= --build-arg NO_PROXY= --build-arg no_proxy="
  fi

  local manager_ver runner_ver gc_ver
  manager_ver="$(cat "${root}/manager-service/VERSION" 2>/dev/null || echo dev)"
  runner_ver="$(cat "${root}/images/runner/VERSION" 2>/dev/null || echo dev)"
  gc_ver="$(cat "${root}/images/gc/VERSION" 2>/dev/null || echo dev)"

  local tag_manager="sandbox-manager:${manager_ver}"
  local tag_runner="sandbox-runner:${runner_ver}"
  local tag_gc="sandbox-gc:${gc_ver}"

  # shellcheck disable=SC2206
  local build_arg_array=($build_args)

  log_info "Pull proxy: $pull_enabled (mode=$pull_proxy_mode)"
  log_info "Build args proxy: $build_proxy_enabled (mode=$build_proxy_mode)"

  case "$only" in
    "" )
      docker_build_image "${root}/images/runner" "${root}/images/runner/Dockerfile" "$tag_runner" "$platform" "${build_arg_array[@]}"
      docker_build_image "${root}/manager-service" "${root}/manager-service/Dockerfile" "$tag_manager" "$platform" "${build_arg_array[@]}"
      docker_build_image "${root}/images/gc" "${root}/images/gc/Dockerfile" "$tag_gc" "$platform" "${build_arg_array[@]}"
      ;;
    runner)
      docker_build_image "${root}/images/runner" "${root}/images/runner/Dockerfile" "$tag_runner" "$platform" "${build_arg_array[@]}"
      ;;
    manager)
      docker_build_image "${root}/manager-service" "${root}/manager-service/Dockerfile" "$tag_manager" "$platform" "${build_arg_array[@]}"
      ;;
    gc)
      docker_build_image "${root}/images/gc" "${root}/images/gc/Dockerfile" "$tag_gc" "$platform" "${build_arg_array[@]}"
      ;;
    *)
      die "Unknown --only value: $only"
      ;;
  esac

  log_info "Built images:"
  echo "  $tag_manager"
  echo "  $tag_runner"
  echo "  $tag_gc"
}

images_push_harbor() {
  local root="$1"
  shift || true

  local registry=""
  local project=""
  local username=""
  local password=""
  local proxy_mode="auto"
  local source_mode="docker"
  local platform="linux/amd64"
  local build_proxy_mode="auto"
  local only=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --registry) registry="${2:-}"; shift 2 ;;
      --project) project="${2:-}"; shift 2 ;;
      --username) username="${2:-}"; shift 2 ;;
      --password) password="${2:-}"; shift 2 ;;
      --proxy) proxy_mode="${2:-auto}"; shift 2 ;;
      --source) source_mode="${2:-docker}"; shift 2 ;;
      --platform) platform="${2:-linux/amd64}"; shift 2 ;;
      --build-proxy) build_proxy_mode="${2:-auto}"; shift 2 ;;
      --only) only="${2:-}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx images push harbor --registry HOST:PORT --project NAME --username USER --password PASS [--proxy auto|on|off] [--source docker|archive] [--build-proxy auto|on|off] [--only manager|runner|gc]"
        return 0
        ;;
      *)
        die "Unknown option: $1"
        ;;
    esac
  done

  [ -n "$registry" ] || die "--registry is required"
  [ -n "$project" ] || die "--project is required"
  [ -n "$username" ] || die "--username is required"
  [ -n "$password" ] || die "--password is required"

  local manager_ver runner_ver gc_ver
  manager_ver="$(cat "${root}/manager-service/VERSION" 2>/dev/null || echo dev)"
  runner_ver="$(cat "${root}/images/runner/VERSION" 2>/dev/null || echo dev)"
  gc_ver="$(cat "${root}/images/gc/VERSION" 2>/dev/null || echo dev)"

  local src_manager="docker-daemon:sandbox-manager:${manager_ver}"
  local src_runner="docker-daemon:sandbox-runner:${runner_ver}"
  local src_gc="docker-daemon:sandbox-gc:${gc_ver}"

  case "$only" in
    ""|manager|runner|gc) : ;;
    *) die "--only must be one of: manager|runner|gc" ;;
  esac

  if [ "$source_mode" != "archive" ]; then
    if [ -z "$only" ] || [ "$only" = "manager" ]; then
      docker_image_exists "sandbox-manager:${manager_ver}" || die "Missing local image sandbox-manager:${manager_ver} (run: ./sbx images build --only manager)"
    fi
    if [ -z "$only" ] || [ "$only" = "runner" ]; then
      docker_image_exists "sandbox-runner:${runner_ver}" || die "Missing local image sandbox-runner:${runner_ver} (run: ./sbx images build --only runner)"
    fi
    if [ -z "$only" ] || [ "$only" = "gc" ]; then
      docker_image_exists "sandbox-gc:${gc_ver}" || die "Missing local image sandbox-gc:${gc_ver} (run: ./sbx images build --only gc)"
    fi
  fi

  local dest_manager="docker://${registry}/${project}/sandbox-manager:${manager_ver}"
  local dest_runner="docker://${registry}/${project}/sandbox-runner:${runner_ver}"
  local dest_gc="docker://${registry}/${project}/sandbox-gc:${gc_ver}"

  log_info "Pushing images to harbor: ${registry}/${project}"
  if [ "$source_mode" = "archive" ]; then
    local tmpdir
    tmpdir="$(mktemp -d /tmp/sbx-push-archive.XXXXXX)"
    trap "rm -rf '$tmpdir'" EXIT

    # Build docker archives via buildx to avoid reading from docker-daemon content store.
    local build_proxy_enabled
    build_proxy_enabled="$(proxy_effective_enabled "$build_proxy_mode")"
    local args_build=()
    if [ "$build_proxy_enabled" = "true" ]; then
      # If build-time proxy args are configured, pass them.
      # shellcheck disable=SC2206
      args_build=($(proxy_env_build_args))
    else
      # Explicitly override Docker's auto proxy build-args injection.
      args_build=(--build-arg HTTP_PROXY= --build-arg HTTPS_PROXY= --build-arg http_proxy= --build-arg https_proxy= --build-arg NO_PROXY= --build-arg no_proxy=)
    fi

    local no_proxy_skopeo="false"
    if [ "$(proxy_effective_enabled "$proxy_mode")" = "false" ]; then
      no_proxy_skopeo="true"
    fi

    if [ -z "$only" ] || [ "$only" = "manager" ]; then
      docker_build_archive "${root}/manager-service" "${root}/manager-service/Dockerfile" "sandbox-manager:${manager_ver}" "$platform" "${tmpdir}/sandbox-manager.tar" "${args_build[@]}"
      if [ "$no_proxy_skopeo" = "true" ]; then
        SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "docker-archive:${tmpdir}/sandbox-manager.tar" "$dest_manager" --dest-creds "${username}:${password}"
      else
        skopeo_copy "$root" "docker-archive:${tmpdir}/sandbox-manager.tar" "$dest_manager" --dest-creds "${username}:${password}"
      fi
    fi

    if [ -z "$only" ] || [ "$only" = "runner" ]; then
      docker_build_archive "${root}/images/runner" "${root}/images/runner/Dockerfile" "sandbox-runner:${runner_ver}" "$platform" "${tmpdir}/sandbox-runner.tar" "${args_build[@]}"
      if [ "$no_proxy_skopeo" = "true" ]; then
        SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "docker-archive:${tmpdir}/sandbox-runner.tar" "$dest_runner" --dest-creds "${username}:${password}"
      else
        skopeo_copy "$root" "docker-archive:${tmpdir}/sandbox-runner.tar" "$dest_runner" --dest-creds "${username}:${password}"
      fi
    fi

    if [ -z "$only" ] || [ "$only" = "gc" ]; then
      docker_build_archive "${root}/images/gc" "${root}/images/gc/Dockerfile" "sandbox-gc:${gc_ver}" "$platform" "${tmpdir}/sandbox-gc.tar" "${args_build[@]}"
      if [ "$no_proxy_skopeo" = "true" ]; then
        SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "docker-archive:${tmpdir}/sandbox-gc.tar" "$dest_gc" --dest-creds "${username}:${password}"
      else
        skopeo_copy "$root" "docker-archive:${tmpdir}/sandbox-gc.tar" "$dest_gc" --dest-creds "${username}:${password}"
      fi
    fi
  else
    if [ "$(proxy_effective_enabled "$proxy_mode")" = "false" ]; then
      if [ -z "$only" ] || [ "$only" = "manager" ]; then SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "$src_manager" "$dest_manager" --dest-creds "${username}:${password}"; fi
      if [ -z "$only" ] || [ "$only" = "runner" ]; then SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "$src_runner" "$dest_runner" --dest-creds "${username}:${password}"; fi
      if [ -z "$only" ] || [ "$only" = "gc" ]; then SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "$src_gc" "$dest_gc" --dest-creds "${username}:${password}"; fi
    else
      if [ -z "$only" ] || [ "$only" = "manager" ]; then skopeo_copy "$root" "$src_manager" "$dest_manager" --dest-creds "${username}:${password}"; fi
      if [ -z "$only" ] || [ "$only" = "runner" ]; then skopeo_copy "$root" "$src_runner" "$dest_runner" --dest-creds "${username}:${password}"; fi
      if [ -z "$only" ] || [ "$only" = "gc" ]; then skopeo_copy "$root" "$src_gc" "$dest_gc" --dest-creds "${username}:${password}"; fi
    fi
  fi

  log_info "Verifying remote content (layer digests)..."
  local jqbin
  jqbin="$(tools_resolve "$root" jq)" || die "jq not found (run: ./sbx tools fetch or install jq)"
  for pair in \
    "sandbox-manager:${manager_ver} ${dest_manager}" \
    "sandbox-runner:${runner_ver} ${dest_runner}" \
    "sandbox-gc:${gc_ver} ${dest_gc}"; do
    if [ "$only" = "manager" ] && [[ "$pair" != sandbox-manager:* ]]; then continue; fi
    if [ "$only" = "runner" ] && [[ "$pair" != sandbox-runner:* ]]; then continue; fi
    if [ "$only" = "gc" ] && [[ "$pair" != sandbox-gc:* ]]; then continue; fi
    local ref="${pair%% *}"
    local dest="${pair#* }"
    local local_layers remote_layers
    if [ "$source_mode" = "archive" ]; then
      # In archive mode, verify remote layers are present; tar-to-layers check is done in offline verify.
      local_layers=""
    else
      local_layers="$(skopeo_inspect "$root" "docker-daemon:${ref}" | "$jqbin" -c '.Layers // []')"
    fi
    remote_layers="$(skopeo_inspect "$root" "$dest" --creds "${username}:${password}" | "$jqbin" -c '.Layers // []')"
    if [ -n "$local_layers" ] && [ "$local_layers" != "$remote_layers" ]; then
      die "Layer digest mismatch: $ref"
    fi
  done

  log_info "Push complete"
}
