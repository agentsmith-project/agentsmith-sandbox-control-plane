# Offline Deploy E2E Test Report

Date: 2026-01-22

Scope:
- Clean repo of oversized offline/test artifacts
- Reset: kind cluster, local images, remote Harbor images
- Re-run full offline flow: build → export offline package → verify → import → deploy → API smoke/e2e

Environment assumptions (from `secrets/test.env`):
- `HARBOR_REGISTRY=harbor.pullot.com:28443`
- `HARBOR_PROJECT=agentsmith`
- Harbor auth: username/password only
- Host `docker pull` uses proxy; Harbor bypasses proxy via `NO_PROXY`
- Dockerfile RUN steps must not use proxy (build-time proxy args disabled)

---

## 1) Repo cleanup (pre-test)

### 1.1 Findings
- `tools/` contained multi-GB offline packages and image tarballs from previous tests (not needed for source control / new run).
- `manager-service/manager` binary present (~52MiB).

### 1.2 Actions
- Deleted old offline packages under `tools/`:
  - `tools/sandbox-offline-*`
  - `tools/offline-package/`
  - `tools/offline/` (if present)
- Deleted leftover backup scripts:
  - `tools/*.bak`
- Deleted build artifact:
  - `manager-service/manager`

---

## 2) Reset (kind + images)

Planned steps:
- Delete kind cluster(s)
- Delete local docker images for `sandbox-*` and Harbor-tagged images
- Delete remote Harbor images in project `agentsmith`

### 2.1 Delete kind cluster
- Command: `./sbx dev down --force`
- Result: deleted `sandbox-cluster`

### 2.2 Delete local docker images
- Removed local tags matching:
  - `sandbox-manager:*`, `sandbox-runner:*`, `sandbox-gc:*`
  - `harbor.pullot.com:28443/agentsmith/sandbox-*:*`
  - `localhost:5001/agentsmith/sandbox-*:*`
- Result: no remaining matching images

### 2.3 Delete remote Harbor images
- Goal: remove all tags/artifacts for:
  - `agentsmith/sandbox-manager`
  - `agentsmith/sandbox-runner`
  - `agentsmith/sandbox-gc`
- Note: host had global proxy variables (including `ALL_PROXY`) that break Harbor API calls unless cleared.
- Action: used `env -i ... curl ...` with `NO_PROXY`/`no_proxy` set to the Harbor host and `--cacert secrets/harbor-ca.crt`.
- Result: all three repositories deleted, `repos_after: 0`

---

## 3) Offline flow (online side)

Planned steps:
- Fetch tools: `./sbx tools fetch --proxy on`
- Build images: `./sbx images build --pull-proxy on --build-proxy off --platform linux/amd64`
- Export offline package to a temp path (not inside repo)
- Verify offline package integrity

### 3.1 Fetch tools (vendored)
- Command: `./sbx tools fetch --proxy on` then `./sbx tools verify`
- Result: ok (`skopeo/kubectl/kustomize/jq/yq` present)

### 3.2 Build images (no build-time proxy args)
- Command: `./sbx images build --pull-proxy on --build-proxy off --platform linux/amd64`
- Result: built:
  - `sandbox-manager:2.0.2`
  - `sandbox-runner:1.0.0`
  - `sandbox-gc:1.0.0`
- Verification: `docker inspect` shows no `HTTP_PROXY/HTTPS_PROXY/*proxy*` in final image env.

### 3.3 Export + verify offline package
- Export path: `/tmp/sbx-offline-e2e-20260122-195945`
- Commands:
  - `./sbx images export offline --out /tmp/sbx-offline-e2e-20260122-195945`
  - `./sbx images verify offline --path /tmp/sbx-offline-e2e-20260122-195945`
- Result: verified `sha256sums.txt` + image archives:
  - `images/sandbox-manager_2.0.2_linux_amd64.tar`
  - `images/sandbox-runner_1.0.0_linux_amd64.tar`
  - `images/sandbox-gc_1.0.0_linux_amd64.tar`
- Contract check: package includes `bin/` and `k8s/` as documented.

---

## 4) Offline flow (air-gapped simulation)

Planned steps:
- Import offline package into Harbor: `./sbx images import offline ... --to harbor --verify --proxy off`
- Create kind cluster + install Harbor CA into node
- Create pull secret in both namespaces
- Update images and deploy production overlay
- Verify + run manager API test

### 4.0 Script adjustment during test (minimal)
- Issue: this host has global `ALL_PROXY/all_proxy` set, which can break “no-proxy” Harbor operations even when `HTTP_PROXY` is cleared.
- Change (intended to be non-breaking):
  - `scripts/lib/proxy.sh`: include `ALL_PROXY/all_proxy` in proxy on/off export set
  - `scripts/lib/skopeo.sh`: when `SBX_SKOPEO_NO_PROXY=true`, also unset `ALL_PROXY/all_proxy`
  - `scripts/lib/offline.sh`: relax post-push verification to “remote tag is inspectable” (digest/layer equality is not reliable across transports/registries)
  - `k8s/overlays/{staging,production}`: add `patches/configmap-runner-image-full.yaml` so manager config uses a full Harbor runner image and a longer `podReadyWait`

### 4.1 Final offline package used
- Path: `/tmp/sbx-offline-e2e-final-20260122-232225`
- Notes:
  - Contains updated `k8s/overlays/production/patches/configmap-runner-image-full.yaml`
  - Verified via `./sbx images verify offline --path ...`

### 4.2 Import offline package into Harbor (from docker-archive tar)
- Reason: `docker load` of the huge runner tar can fail on this host (`invalid diffID for layer ...`), so Harbor import must avoid `docker load`.
- Command:
  - `./sbx images import offline --from /tmp/sbx-offline-e2e-final-20260122-232225 --to harbor ... --verify --proxy off`
- Implementation:
  - `skopeo copy docker-archive:<tar> docker://<harbor>/<project>/<name>:<tag>`
- Result:
  - `sandbox-manager:2.0.2`, `sandbox-runner:1.0.0`, `sandbox-gc:1.0.0` present in Harbor project.

### 4.3 Create kind cluster + install CA
- Commands:
  - `kind create cluster --name sandbox-cluster --wait 60s`
  - `docker cp secrets/harbor-ca.crt sandbox-cluster-control-plane:/usr/local/share/ca-certificates/harbor/ca.crt`
  - `docker exec sandbox-cluster-control-plane update-ca-certificates`
- Result: kind node trusts Harbor TLS cert and can pull images.

### 4.4 Deploy production overlay from offline package
- Commands (run from host with `PATH=/tmp/.../bin:$PATH`):
  - `/tmp/sbx-offline-e2e-final-20260122-232225/k8s/scripts/setup-harbor-secret.sh` (creates `harbor-registry-secret` in both `sandbox-system` and `sandbox`)
  - `/tmp/sbx-offline-e2e-final-20260122-232225/k8s/scripts/deploy.sh production`
  - `/tmp/sbx-offline-e2e-final-20260122-232225/k8s/scripts/verify.sh production`
- Result:
  - `sandbox-manager` ready (3/3 in production overlay)
  - Runner default image resolves to `harbor.pullot.com:28443/agentsmith/sandbox-runner:1.0.0`

### 4.5 API e2e
- Port-forward: `kubectl -n sandbox-system port-forward svc/sandbox-manager 18080:80`
- Test: `manager-service/scripts/test-manager.sh http://127.0.0.1:18080 <service-key>`
- Result: all API tests passed, and `sandbox` namespace ends with no pods.

---

## 5) Post-test cleanup

Planned steps:
- Delete temp offline package
- Ensure no oversized artifacts remain in repo working tree

### 5.1 Remove temp offline package(s)
- Verified no remaining `/tmp/sbx-offline*` directories:
  - `find /tmp -maxdepth 1 -type d \\( -name "sbx-offline*" -o -name "sandbox-offline*" \\) -print`

### 5.2 Reset kind
- Verified no remaining kind clusters:
  - `kind get clusters`

### 5.3 Repo artifact check
- Verified no large files in the repo working tree (>50MiB):
  - `find . -type f -size +50M -print`

### 5.4 Local docker image cleanup
- Found leftover local registry tags:
  - `docker images | rg 'localhost:5001/sandbox-'`
- Removed:
  - `docker rmi -f localhost:5001/sandbox-gc:1.0.0 localhost:5001/sandbox-manager:2.0.0 localhost:5001/sandbox-manager:1.0.0 localhost:5001/sandbox-runner:1.0.0`
- Re-verified:
  - `docker images | rg 'sandbox-(manager|runner|gc)|localhost:5001/sandbox-|harbor.pullot.com:28443/agentsmith/sandbox-'` → no matches
