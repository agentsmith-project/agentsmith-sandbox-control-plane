#!/usr/bin/env bash
set -euo pipefail

# Offline bundles may still carry the runner as a non-active fixture.
# ASBCP active release/dev paths do not build or push this image by default.
offline_export() {
  local root="$1"
  shift || true

  local out_dir=""
  local source="docker"
  local registry=""
  local project=""
  local username=""
  local password=""
  local proxy_mode="auto"
  local asbcp_tag=""
  local runner_tag=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --out) out_dir="${2:-}"; shift 2 ;;
      --source) source="${2:-docker}"; shift 2 ;;
      --asbcp-tag) asbcp_tag="${2:-}"; shift 2 ;;
      --runner-tag) runner_tag="${2:-}"; shift 2 ;;
      --registry) registry="${2:-}"; shift 2 ;;
      --project) project="${2:-}"; shift 2 ;;
      --username) username="${2:-}"; shift 2 ;;
      --password) password="${2:-}"; shift 2 ;;
      --proxy) proxy_mode="${2:-auto}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx images export offline --out DIR [--source docker|harbor|registry] [--registry H] [--project P] [--username U] [--password PW] [--proxy auto|on|off]"
        return 0
        ;;
      *)
        die "Unknown option: $1"
        ;;
    esac
  done

  [ -n "$out_dir" ] || die "--out is required"

  local enabled
  enabled="$(proxy_effective_enabled "$proxy_mode")"
  local penv
  penv="$(proxy_env_host "$enabled")"

  docker_require

  tools_verify "$root" || die "Bundled tools missing/invalid (run: ./sbx tools fetch)"

  local asbcp_ver runner_ver
  asbcp_ver="${asbcp_tag:-$(cat "${root}/VERSION" 2>/dev/null || echo dev)}"
  runner_ver="${runner_tag:-$(cat "${root}/images/runner/VERSION" 2>/dev/null || echo dev)}"

  local local_asbcp="agentsmith-sandbox-control-plane:${asbcp_ver}"
  local local_runner="sandbox-runner:${runner_ver}"

  mkdir -p "$out_dir"
  mkdir -p "${out_dir}/images" "${out_dir}/bin" "${out_dir}/k8s" "${out_dir}/docs"

  # Copy tools (air-gapped)
  cp -a "$(tools_bin_dir "$root")/." "${out_dir}/bin/"

  # Copy k8s tree (deterministic subset)
  if command -v rsync >/dev/null 2>&1; then
    rsync -a --delete \
      --exclude '.git' \
      --exclude 'config/*.env' \
      --exclude 'config/*.env.*' \
      "${root}/k8s/" "${out_dir}/k8s/"
  else
    (cd "${root}" && tar -cf - k8s | (cd "${out_dir}" && tar -xf -))
    rm -f "${out_dir}/k8s/config/deploy.env" 2>/dev/null || true
    rm -f "${out_dir}/k8s/cluster/kind/config.env" 2>/dev/null || true
  fi

  # Minimal docs
  for f in README.md WORKFLOWS.md OFFLINE.md TROUBLESHOOTING.md API.md CONFIGURATION_GUIDE.md; do
    if [ -f "${root}/docs/${f}" ]; then
      cp -a "${root}/docs/${f}" "${out_dir}/docs/"
    fi
  done

  # Ensure images available locally
  if [ "$source" != "docker" ]; then
    [ -n "$registry" ] || die "--registry is required when --source != docker"
    [ -n "$project" ] || die "--project is required when --source != docker"
    [ -n "$username" ] || die "--username is required when --source != docker"
    [ -n "$password" ] || die "--password is required when --source != docker"
    eval "$penv"
    skopeo_copy "$root" "docker://${registry}/${project}/${local_asbcp}" "docker-daemon:${local_asbcp}" --src-creds "${username}:${password}"
    skopeo_copy "$root" "docker://${registry}/${project}/${local_runner}" "docker-daemon:${local_runner}" --src-creds "${username}:${password}"
  fi

  docker_image_exists "$local_asbcp" || die "Missing local image: $local_asbcp (run: ./sbx images build)"
  docker_image_exists "$local_runner" || die "Missing local image: $local_runner (run: ./sbx images build)"

  local tar_asbcp="${out_dir}/images/agentsmith-sandbox-control-plane_${asbcp_ver}_linux_amd64.tar"
  local tar_runner="${out_dir}/images/sandbox-runner_${runner_ver}_linux_amd64.tar"

  docker_save_image "$local_asbcp" "$tar_asbcp"
  docker_save_image "$local_runner" "$tar_runner"

  offline_write_manifest "$root" "$out_dir" "$asbcp_ver" "$runner_ver" "$tar_asbcp" "$tar_runner"
  offline_write_sha256sums "$out_dir"

  log_info "Offline package created: $out_dir"
}

offline_import() {
  local root="$1"
  shift || true

  local from_dir=""
  local to="docker"
  local registry=""
  local project=""
  local username=""
  local password=""
  local do_verify="false"
  local proxy_mode="auto"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --from) from_dir="${2:-}"; shift 2 ;;
      --to) to="${2:-docker}"; shift 2 ;;
      --registry) registry="${2:-}"; shift 2 ;;
      --project) project="${2:-}"; shift 2 ;;
      --username) username="${2:-}"; shift 2 ;;
      --password) password="${2:-}"; shift 2 ;;
      --verify) do_verify="true"; shift ;;
      --proxy) proxy_mode="${2:-auto}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx images import offline --from DIR [--to docker|harbor|registry] [--registry H] [--project P] [--username U] [--password PW] [--verify]"
        return 0
        ;;
      *)
        die "Unknown option: $1"
        ;;
    esac
  done

  [ -n "$from_dir" ] || die "--from is required"
  [ -f "${from_dir}/manifest.json" ] || die "Missing manifest.json in $from_dir"

  if [ "$do_verify" = "true" ]; then
    offline_verify "$root" --path "$from_dir"
  fi

  local enabled
  enabled="$(proxy_effective_enabled "$proxy_mode")"
  local penv
  penv="$(proxy_env_host "$enabled")"

  if [ "$to" = "docker" ]; then
    docker_require
    local tar_files
    tar_files="$(ls -1 "${from_dir}/images/"*.tar 2>/dev/null || true)"
    [ -n "$tar_files" ] || die "No image tar files found in ${from_dir}/images"
    for t in $tar_files; do
      log_info "Loading image tar: $t"
      docker_load_image "$t"
    done
    log_info "Imported into local docker daemon"
    return 0
  fi

  [ -n "$registry" ] || die "--registry is required when --to != docker"
  [ -n "$project" ] || die "--project is required when --to != docker"
  [ -n "$username" ] || die "--username is required when --to != docker"
  [ -n "$password" ] || die "--password is required when --to != docker"

  eval "$penv"
  offline_push_archives "$root" "$from_dir" "$to" "$registry" "$project" "$username" "$password" "$enabled"
}

offline_verify() {
  local root="$1"
  shift || true

  local path=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --path) path="${2:-}"; shift 2 ;;
      -h|--help)
        echo "Usage: ./sbx images verify offline --path DIR"
        return 0
        ;;
      *) die "Unknown option: $1" ;;
    esac
  done
  [ -n "$path" ] || die "--path is required"

  [ -f "${path}/sha256sums.txt" ] || die "Missing sha256sums.txt"
  if ! command -v sha256sum >/dev/null 2>&1; then
    die "sha256sum is required"
  fi

  (cd "$path" && sha256sum -c sha256sums.txt) >/dev/null

  # Image-layer verify from docker-archive digests in manifest.json
  local sk
  sk="$(skopeo_path "$root")" || die "skopeo not found (run: ./sbx tools fetch)"
  local jqbin
  jqbin="$(tools_resolve "$root" jq)" || die "jq not found (run: ./sbx tools fetch or install jq)"

  local count
  count="$("$jqbin" -r '.images | length' "${path}/manifest.json")"
  if [ -z "$count" ] || [ "$count" = "null" ]; then
    die "Invalid manifest.json"
  fi

  local i
  for ((i=0; i<count; i++)); do
    local tar_rel expected tar_path actual
    tar_rel="$("$jqbin" -r ".images[$i].tarPath" "${path}/manifest.json")"
    expected="$("$jqbin" -r ".images[$i].digest" "${path}/manifest.json")"
    tar_path="${path}/${tar_rel}"
    [ -f "$tar_path" ] || die "Missing tar: $tar_rel"
    actual="$("$sk" inspect "docker-archive:${tar_path}" | "$jqbin" -r '.Digest // ""')"
    if [ -n "$expected" ] && [ "$expected" != "null" ] && [ "$actual" != "$expected" ]; then
      die "Digest mismatch for ${tar_rel}: expected ${expected}, got ${actual}"
    fi
    log_info "offline ok: ${tar_rel}"
  done
  log_info "Offline package verified: $path"
}

offline_write_manifest() {
  local root="$1"
  local out_dir="$2"
  local asbcp_ver="$3"
  local runner_ver="$4"
  local tar_asbcp="$5"
  local tar_runner="$6"

  if ! command -v sha256sum >/dev/null 2>&1; then
    die "sha256sum is required for offline manifest"
  fi

  local sk
  sk="$(skopeo_path "$root")" || die "skopeo not found (run: ./sbx tools fetch)"
  local jqbin
  jqbin="$(tools_resolve "$root" jq)" || die "jq not found (run: ./sbx tools fetch or install jq)"

  local gen_at git_commit
  gen_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  git_commit="$(git -C "$root" rev-parse HEAD)"

  local rel_asbcp rel_runner
  rel_asbcp="${tar_asbcp#${out_dir}/}"
  rel_runner="${tar_runner#${out_dir}/}"

  local sha_asbcp sha_runner
  sha_asbcp="$(sha256sum "$tar_asbcp" | awk '{print $1}')"
  sha_runner="$(sha256sum "$tar_runner" | awk '{print $1}')"

  local dig_asbcp dig_runner
  dig_asbcp="$("$sk" inspect "docker-archive:${tar_asbcp}" | "$jqbin" -r '.Digest // ""')"
  dig_runner="$("$sk" inspect "docker-archive:${tar_runner}" | "$jqbin" -r '.Digest // ""')"

  "$jqbin" -n \
    --arg genAt "$gen_at" \
    --arg gitCommit "$git_commit" \
    --arg asbcpVer "$asbcp_ver" \
    --arg runnerVer "$runner_ver" \
    --arg asbcpTar "$rel_asbcp" \
    --arg runnerTar "$rel_runner" \
    --arg asbcpSha "$sha_asbcp" \
    --arg runnerSha "$sha_runner" \
    --arg asbcpDig "$dig_asbcp" \
    --arg runnerDig "$dig_runner" \
    '{
      schemaVersion: 1,
      generatedAt: $genAt,
      platform: "linux/amd64",
      gitCommit: $gitCommit,
      images: [
        {name:"agentsmith-sandbox-control-plane", tag:$asbcpVer, tarPath:$asbcpTar, tarSha256:$asbcpSha, digest:$asbcpDig},
        {name:"sandbox-runner", tag:$runnerVer, tarPath:$runnerTar, tarSha256:$runnerSha, digest:$runnerDig}
      ]
    }' > "${out_dir}/manifest.json"
  log_info "offline wrote manifest.json"
}

offline_write_sha256sums() {
  local out_dir="$1"
  if ! command -v sha256sum >/dev/null 2>&1; then
    die "sha256sum is required"
  fi

  (
    cd "$out_dir"
    find . -type f ! -name sha256sums.txt -print0 | sort -z | xargs -0 sha256sum > sha256sums.txt
  )
}

offline_push_loaded_images() {
  local root="$1"
  local from_dir="$2"
  local to="$3"
  local registry="$4"
  local project="$5"
  local username="$6"
  local password="$7"

  local jqbin
  jqbin="$(tools_resolve "$root" jq)" || die "jq not found (run: ./sbx tools fetch or install jq)"

  local refs
  refs="$("$jqbin" -r '.images[] | "\(.name):\(.tag)"' "${from_dir}/manifest.json")"

  for ref in $refs; do
    local dest="docker://${registry}/${project}/${ref}"
    log_info "Pushing: $ref -> $dest"
    skopeo_copy "$root" "docker-daemon:${ref}" "$dest" --dest-creds "${username}:${password}"
    # Registries may rewrite manifest/config and/or recompress layers; strict digest/layer equality
    # is not reliable across different transports. At this stage we only require that the tag exists
    # and is inspectable in the destination registry.
    local remote_d
    remote_d="$(skopeo_inspect "$root" "$dest" --creds "${username}:${password}" | "$jqbin" -r '.Digest // ""')"
    [ -n "$remote_d" ] || die "Remote inspect failed after push for $ref"
  done
}

offline_push_archives() {
  local root="$1"
  local from_dir="$2"
  local to="$3"
  local registry="$4"
  local project="$5"
  local username="$6"
  local password="$7"
  local proxy_enabled="${8:-true}"

  local jqbin
  jqbin="$(tools_resolve "$root" jq)" || die "jq not found (run: ./sbx tools fetch or install jq)"

  local count
  count="$("$jqbin" -r '.images | length' "${from_dir}/manifest.json")"
  [ -n "$count" ] && [ "$count" != "null" ] || die "Invalid manifest.json"

  local i
  for ((i=0; i<count; i++)); do
    local name tag tar_rel tar_path dest
    name="$("$jqbin" -r ".images[$i].name" "${from_dir}/manifest.json")"
    tag="$("$jqbin" -r ".images[$i].tag" "${from_dir}/manifest.json")"
    tar_rel="$("$jqbin" -r ".images[$i].tarPath" "${from_dir}/manifest.json")"
    tar_path="${from_dir}/${tar_rel}"
    [ -f "$tar_path" ] || die "Missing tar: $tar_rel"

    dest="docker://${registry}/${project}/${name}:${tag}"
    log_info "Pushing: ${name}:${tag} (${tar_rel}) -> ${dest}"
    if [ "$proxy_enabled" = "false" ]; then
      SBX_SKOPEO_NO_PROXY=true skopeo_copy "$root" "docker-archive:${tar_path}" "$dest" --dest-creds "${username}:${password}"
      SBX_SKOPEO_NO_PROXY=true skopeo_inspect "$root" "$dest" --creds "${username}:${password}" >/dev/null
    else
      skopeo_copy "$root" "docker-archive:${tar_path}" "$dest" --dest-creds "${username}:${password}"
      skopeo_inspect "$root" "$dest" --creds "${username}:${password}" >/dev/null
    fi
  done
}
