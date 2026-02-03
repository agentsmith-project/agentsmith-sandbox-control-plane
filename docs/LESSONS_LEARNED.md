# Lessons Learned (Refactor + End-to-End Test)

This document captures operational lessons from running a clean end-to-end flow:

- kind reset → CA trust → build/push images → deploy → API e2e.

## 1) Harbor CA trust in kind is mandatory

Symptom:
- Pods stuck in `ImagePullBackOff` with `x509: certificate signed by unknown authority`.

Root cause:
- kind node container does not trust the Harbor CA by default.

Fix:
- Use `./sbx dev up --harbor-ca auto` (or `./sbx dev reset --harbor-ca auto`) so the CA is installed into the kind node.

Implementation note:
- Some Harbor deployments return 404 for `/api/v2.0/systeminfo/getcert`; fallback must fetch the presented server cert via `openssl s_client`.

## 2) Proxy must be split by responsibility

Requirements:
- Host-side `docker pull` / `curl` / `kind` may need proxy.
- Harbor traffic must bypass proxy (`NO_PROXY` includes Harbor hostname).
- Image build must not persist proxy into final images.

Best practice in this repo:
- Host-side: `HTTP_PROXY/HTTPS_PROXY/NO_PROXY`
- Build-time only: `DOCKER_IMAGE_HTTP_PROXY/DOCKER_IMAGE_HTTPS_PROXY/DOCKER_IMAGE_NO_PROXY`
- Images must not contain proxy env after build.

Additional gotcha:
- Some environments also set `ALL_PROXY/all_proxy`; treat it as part of the host proxy surface area, and ensure “no-proxy” paths clear it.

## 3) buildx builder networking matters

Symptom:
- Build fails resolving Docker Hub metadata (`auth.docker.io` / `registry-1.docker.io`) with timeouts.

Root cause:
- Buildkit container cannot reach the proxy (proxy reachable only from host network in some environments).

Fix:
- Create docker-container buildx builders with host networking and proxy injected into buildkit container.

## 4) Prefer `--source archive` when pushing to Harbor

Symptom:
- `docker save` / `skopeo copy docker-daemon:` can fail if the local docker content store is corrupted.

Fix:
- Use `./sbx images push harbor --source archive ...` to build per-image docker archives via buildx and push via `skopeo`.

Related:
- If `docker load` fails for large archives (e.g. `invalid diffID for layer ...`), import/push to Harbor directly from `docker-archive:` via `skopeo` instead of loading into the daemon first.

## 5) Sandbox pod image pull requires both registry auth and correct runner image

Two common failure modes:
1) Runner image points to `docker.io/library/...` or `localhost:5001/...` (wrong default).
2) Runner image is in Harbor but the `sandbox` namespace has no pull secret.

Fixes:
- Ensure `k8s/overlays/<env>/patches/configmap-runner-image-full.yaml` sets `sandbox.defaults.runnerImage` to a full Harbor reference.
- Ensure Harbor secret exists in both namespaces: `sandbox-system` and `sandbox`.
- Manager can be configured to attach `imagePullSecrets` when creating sandbox pods.

## 6) E2E API test expectations

- Upload endpoint expects `tar.gz` bytes (per config); sending raw text returns 4xx.
- Delete may return `204` (No Content); tests must accept this.
- Download returns binary; do not capture the body into shell variables.

## 7) Minimal cleanup expectations

- After e2e, `sandbox` namespace should have no leftover pods.
- `sandbox-system` should only have manager + recent GC jobs.
