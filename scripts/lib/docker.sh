#!/usr/bin/env bash
set -euo pipefail

docker_require() {
  if ! command -v docker >/dev/null 2>&1; then
    die "docker is required"
  fi
}

docker_buildx_require() {
  docker_require
  if ! docker buildx version >/dev/null 2>&1; then
    die "docker buildx is required"
  fi
}

docker_ensure_builder() {
  local builder_name="$1"
  if docker buildx inspect "$builder_name" >/dev/null 2>&1; then
    docker buildx use "$builder_name" >/dev/null
    return 0
  fi
  log_info "Creating buildx builder: $builder_name"
  local driver_opts=()
  # Host networking is required in some environments where the proxy is only
  # reachable from the host network.
  driver_opts+=("--driver-opt" "network=host")
  # Allow buildkit to use host proxy for pulling base images and build-time downloads,
  # without persisting proxy variables into the final image layers.
  if [ -n "${HTTP_PROXY:-}" ]; then driver_opts+=("--driver-opt" "env.HTTP_PROXY=${HTTP_PROXY}"); fi
  if [ -n "${HTTPS_PROXY:-}" ]; then driver_opts+=("--driver-opt" "env.HTTPS_PROXY=${HTTPS_PROXY}"); fi
  if [ -n "${http_proxy:-}" ]; then driver_opts+=("--driver-opt" "env.http_proxy=${http_proxy}"); fi
  if [ -n "${https_proxy:-}" ]; then driver_opts+=("--driver-opt" "env.https_proxy=${https_proxy}"); fi

  docker buildx create --name "$builder_name" --driver docker-container "${driver_opts[@]}" --use >/dev/null
  docker buildx inspect --bootstrap >/dev/null
}

docker_build_image() {
  local context_dir="$1"
  local dockerfile="$2"
  local tag="$3"
  local platform="$4"
  shift 4 || true
  local extra_args=("$@")

  docker_buildx_require

  log_info "Building: $tag (platform=$platform)"
  docker buildx build \
    --platform "$platform" \
    -f "$dockerfile" \
    -t "$tag" \
    --load \
    "${extra_args[@]}" \
    "$context_dir"
}

docker_image_exists() {
  local ref="$1"
  docker image inspect "$ref" >/dev/null 2>&1
}

docker_save_image() {
  local ref="$1"
  local out_tar="$2"
  docker_require
  mkdir -p "$(dirname "$out_tar")"
  docker save -o "$out_tar" "$ref"
}

docker_build_archive() {
  local context_dir="$1"
  local dockerfile="$2"
  local tag="$3"
  local platform="$4"
  local out_tar="$5"
  shift 5 || true
  local extra_args=("$@")

  docker_buildx_require
  # Ensure we are using a docker-container buildx builder so the "type=docker"
  # exporter is supported. Prefer the proxy-enabled builder when proxy env is set.
  if [ -n "${HTTP_PROXY:-}" ] || [ -n "${HTTPS_PROXY:-}" ] || [ -n "${http_proxy:-}" ] || [ -n "${https_proxy:-}" ]; then
    docker_ensure_builder "sbx-builder-proxy"
  else
    docker_ensure_builder "sbx-builder"
  fi
  mkdir -p "$(dirname "$out_tar")"

  log_info "Building archive: $tag -> $out_tar (platform=$platform)"
  docker buildx build \
    --platform "$platform" \
    -f "$dockerfile" \
    -t "$tag" \
    --output "type=docker,dest=${out_tar}" \
    "${extra_args[@]}" \
    "$context_dir"
}

docker_load_image() {
  local tar="$1"
  docker_require
  docker load -i "$tar" >/dev/null
}
