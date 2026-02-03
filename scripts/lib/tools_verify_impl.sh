#!/usr/bin/env bash
set -euo pipefail

lock_json="$1"
bindir="$2"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for tools verify impl" >&2
  exit 1
fi
if ! command -v sha256sum >/dev/null 2>&1; then
  echo "sha256sum is required for tools verify impl" >&2
  exit 1
fi

python3 - "$lock_json" "$bindir" <<'PY'
import json, os, subprocess, sys

lock_path, bindir = sys.argv[1], sys.argv[2]
with open(lock_path, "r", encoding="utf-8") as f:
    lock = json.load(f)

def out(*args):
    return subprocess.check_output(list(args), text=True).strip()

errors = 0
for t in lock.get("tools", []):
    name = t["name"]
    expected = t["sha256"]
    p = os.path.join(bindir, name)
    if not os.path.exists(p):
        print(f"[tools] missing: {p}")
        errors += 1
        continue
    actual = out("sha256sum", p).split()[0]
    if actual != expected:
        print(f"[tools] sha mismatch: {name}: expected {expected}, got {actual}")
        errors += 1
    else:
        print(f"[tools] ok: {name}")

if errors:
    raise SystemExit(1)
PY

