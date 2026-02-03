# Sandbox

Kubernetes-based sandbox system for running isolated “session pods” with a Manager HTTP API, a Runner image, and a TTL-based GC CronJob.

## Repo Layout

- `manager-service/`: Manager HTTP API (Go)
- `images/runner/`: Runner image + build scripts
- `images/gc/`: GC image + scripts
- `k8s/`: Kustomize base + overlays + deployment utilities
- `sbx`: Single workflow entrypoint (build/push/offline/dev/k8s/tools)
- `scripts/`: Internal bash libraries used by `./sbx`
- `docs/`: Developer & operations docs
- `tools/`: Vendored binaries for offline (`skopeo`, `kubectl`, `kustomize`, `jq`, `yq`)

## Quick Start (kind)

```bash
# 0) Fetch vendored tools (recommended; avoids needing kubectl/kustomize/skopeo installed)
./sbx tools fetch --proxy auto

# 1) Create kind cluster + build + deploy (end-to-end)
./sbx dev up --force --proxy auto --harbor-ca auto

# 2) Access manager
tools/bin/linux-amd64/kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80

# 3) Smoke test API (requires a valid SERVICE_KEYS in the deployment)
cd manager-service && ./scripts/test-manager.sh http://127.0.0.1:8080 <service-key>
```

## Docs

- `docs/README.md`
- `docs/WORKFLOWS.md`
- `docs/OFFLINE.md`
- `docs/API.md`
