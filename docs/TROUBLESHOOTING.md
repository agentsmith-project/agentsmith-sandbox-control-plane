# Troubleshooting

## Tools missing

Offline packaging requires vendored tools:

```bash
./sbx tools fetch --proxy auto
./sbx tools verify
```

## kind pulls fail: `x509: certificate signed by unknown authority`

Your kind node does not trust the Harbor CA.

- Use `./sbx dev up --harbor-ca auto` (or `./sbx dev reset --harbor-ca auto`) so the CA is installed into the kind node.

## Proxy behavior

- Host-side proxy uses: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`
- Build-time proxy args use: `DOCKER_IMAGE_HTTP_PROXY`, `DOCKER_IMAGE_HTTPS_PROXY`, `DOCKER_IMAGE_NO_PROXY`
- Final images never keep proxy env vars.

## Push/import “mismatch”

Harbor may rewrite manifest digests even when layer contents match.

- Re-run: `./sbx images verify offline --path <dir>`
- If pushing to Harbor from a docker archive (`--source archive`), expect digest differences; verify layer digests instead.

## Sandbox pod cannot pull runner image

Symptoms:
- `ImagePullBackOff` in `sandbox` namespace pods.
- Pull attempts go to `docker.io/library/...` or `localhost:5001/...`.

Fix:
- Ensure `k8s/overlays/<env>/patches/configmap-runner-image-full.yaml` sets `runnerImage` to a full Harbor reference.
- Ensure Harbor pull secret exists in both namespaces: `sandbox-system` and `sandbox` (run `./sbx k8s harbor-secret ...`).
