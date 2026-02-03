# Offline Package

Offline packages are directories created by:

```bash
./sbx images export offline --out dist/sandbox-offline-YYYYMMDD-HHMMSS
```

## Contents (stable contract)

- `manifest.json`: images, digests, tool versions
- `sha256sums.txt`: file-level integrity for everything inside the package
- `images/*.tar`: docker `save` archives for the 3 images (linux/amd64)
- `bin/`: `skopeo`, `kubectl`, `kustomize`, `jq`, `yq`
- `k8s/`: kustomize base/overlays + scripts
- `docs/`: minimal docs

## Verify

```bash
./sbx images verify offline --path dist/sandbox-offline-*
```

This validates:
1) file checksums (`sha256sums.txt`)
2) image digests (`skopeo inspect docker-archive:`) match `manifest.json`

## Import

Import into local docker:

```bash
./sbx images import offline --from dist/sandbox-offline-* --to docker --verify
```

Import into Harbor (username/password only):

```bash
./sbx images import offline --from dist/sandbox-offline-* --to harbor \
  --registry harbor.local:28443 --project agentsmith --username admin --password '***' --verify
```

