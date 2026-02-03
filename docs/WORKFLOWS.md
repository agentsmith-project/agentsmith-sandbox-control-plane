# Workflows

This repo has a single entrypoint: `./sbx`.

## Tools (recommended)

Vendored tools avoid relying on host-installed `kubectl/kustomize/skopeo`.

```bash
set -euo pipefail
./sbx tools fetch --proxy auto
./sbx tools verify
```

## Dev (kind)

```bash
set -euo pipefail

# Optional: provide Harbor/proxy config (recommended for reproducibility).
# This file is gitignored by default.
if [ -f secrets/test.env ]; then
  set -a
  source secrets/test.env
  set +a
fi

require_env() { [ -n "${!1:-}" ] || { echo "Missing env: $1" >&2; exit 2; }; }

# If using a remote registry (Harbor), we expect these vars.
# Example values are in secrets/test.env.
require_env HARBOR_REGISTRY
require_env HARBOR_PROJECT
require_env HARBOR_USERNAME
require_env HARBOR_PASSWORD

# Proxy behavior:
# - Host-side pull/buildkit uses HTTP_PROXY/HTTPS_PROXY when dev up uses --proxy on|auto.
# - Harbor must be bypassed via NO_PROXY (e.g. include harbor.pullot.com).
: "${NO_PROXY:=}"
case ",$NO_PROXY," in
  *",${HARBOR_REGISTRY%%:*},"*) : ;;
  *) echo "WARN: NO_PROXY does not include ${HARBOR_REGISTRY%%:*} (Harbor should bypass proxy)" >&2 ;;
esac

# Ensure vendored kubectl exists (fetch tools if needed).
./sbx tools fetch --proxy auto >/dev/null

# Creates kind cluster, installs Harbor CA into kind node, pushes images (archive), deploys dev overlay.
export REGISTRY="$HARBOR_REGISTRY"
export HARBOR_PROJECT
export HARBOR_USERNAME
export HARBOR_PASSWORD

./sbx dev up --force --proxy auto --harbor-ca auto

# Port-forward and run API e2e.
K=tools/bin/linux-amd64/kubectl
$K -n sandbox-system port-forward svc/sandbox-manager 8080:80 >/tmp/sbx-portforward.log 2>&1 &
PF_PID=$!
trap 'kill "$PF_PID" >/dev/null 2>&1 || true' EXIT

for _ in $(seq 1 80); do
  curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break
  sleep 0.2
done

SERVICE_KEY=$($K -n sandbox-system get secret sandbox-manager-keys -o jsonpath='{.data.SERVICE_KEYS}' | base64 -d)
SERVICE_KEY=${SERVICE_KEY%%,*}
(cd manager-service && ./scripts/test-manager.sh http://127.0.0.1:8080 "$SERVICE_KEY")
```

## Production (Harbor)

```bash
set -euo pipefail

require_env() { [ -n "${!1:-}" ] || { echo "Missing env: $1" >&2; exit 2; }; }

# Required Harbor auth (username/password only).
require_env HARBOR_REGISTRY
require_env HARBOR_PROJECT
require_env HARBOR_USERNAME
require_env HARBOR_PASSWORD

# Recommended: ensure Harbor is in NO_PROXY.
: "${NO_PROXY:=}"
case ",$NO_PROXY," in
  *",${HARBOR_REGISTRY%%:*},"*) : ;;
  *) echo "WARN: NO_PROXY does not include ${HARBOR_REGISTRY%%:*}" >&2 ;;
esac

# 1) Push 3 images to Harbor (from buildx docker-archive to avoid local docker store issues).
#    --proxy off: skopeo to Harbor bypasses proxy.
#    --build-proxy off: Dockerfile steps do not use proxy; buildkit can still pull base images via builder env.
./sbx images push harbor \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD" \
  --source archive --proxy off --build-proxy off

# 2) Create/update imagePullSecret (writes to both sandbox-system and sandbox namespaces).
./sbx k8s harbor-secret \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD"

# 3) Update kustomize overlays to point to Harbor and correct tags.
./sbx k8s update-images --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT"

# 4) Deploy + verify.
./sbx k8s deploy production
./sbx k8s verify production
```

## Offline (air-gapped)

Online machine (build + package):

```bash
set -euo pipefail

# 1) Fetch and lock vendored tools (included in the offline package).
./sbx tools fetch --proxy on
./sbx tools verify

# 2) Build images (linux/amd64 only).
#    - pull-proxy on: base image pulls go via host proxy
#    - build-proxy off: Dockerfile steps do not use proxy build-args and won't persist proxy
./sbx images build --pull-proxy on --build-proxy off --platform linux/amd64

# 3) Export + verify offline package.
OUT="dist/sandbox-offline-$(date +%Y%m%d-%H%M%S)"
./sbx images export offline --out "$OUT"
./sbx images verify offline --path "$OUT"
```

Air-gapped machine (import + deploy):

```bash
set -euo pipefail

require_env() { [ -n "${!1:-}" ] || { echo "Missing env: $1" >&2; exit 2; }; }

# Path to the extracted offline directory.
OFFLINE_DIR="${OFFLINE_DIR:-dist/sandbox-offline-*}"
./sbx images verify offline --path "$OFFLINE_DIR"

# If importing to Harbor (still air-gapped), provide username/password.
require_env HARBOR_REGISTRY
require_env HARBOR_PROJECT
require_env HARBOR_USERNAME
require_env HARBOR_PASSWORD

# Import images from the offline package into Harbor, verifying integrity.
./sbx images import offline \
  --from "$OFFLINE_DIR" \
  --to harbor \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD" \
  --verify

# Create pull secret in the cluster (both namespaces).
./sbx k8s harbor-secret \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD"

# Update manifests and deploy.
./sbx k8s update-images --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT"
./sbx k8s deploy production
./sbx k8s verify production
```
