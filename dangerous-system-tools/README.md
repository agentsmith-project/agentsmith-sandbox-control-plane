# Dangerous System Tools

This directory contains **intentionally dangerous** helpers that modify host-level system settings.

- They are **not** used by `./sbx` and are **not** required for sandbox itself.
- Use only if you understand Docker internals and accept the risk of data loss/outages.

## Switch Docker data-root

`switch-docker-dataroot.sh` stops Docker, updates only the `"data-root"` field in `/etc/docker/daemon.json`, and restarts Docker.

It does **not** change other daemon.json settings (proxy, mirrors, runtimes, etc).

See:
- `./dangerous-system-tools/switch-docker-dataroot.sh --help`

