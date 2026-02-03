# Configuration

Primary interface is `./sbx`. The repo avoids committing environment-specific secrets or `.env` files.

## Registry / Harbor

Used by:
- `./sbx images push harbor`
- `./sbx k8s update-images`

Required flags (no implicit prompts):
- `--registry HOST:PORT`
- `--project NAME`
- `--username USER`
- `--password PASS`

## Proxy

Host-side (pull/push/skopeo/kind):
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`

Notes:
- Put Harbor host (e.g. `harbor.pullot.com`) in `NO_PROXY` so registry auth/pull does not go through proxy.

Build-time (only used during `docker build`, never kept in final images):
- `DOCKER_IMAGE_HTTP_PROXY`, `DOCKER_IMAGE_HTTPS_PROXY`, `DOCKER_IMAGE_NO_PROXY`

## Tools (vendored for offline)

Vendored binaries live under `tools/bin/linux-amd64`.

```bash
./sbx tools fetch --proxy auto
./sbx tools verify
```
