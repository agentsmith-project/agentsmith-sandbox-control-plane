#!/usr/bin/env bash
set -euo pipefail

sources_json="$1"
bindir="$2"
lock_json="$3"
proxy_env="$4"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for tools fetch impl" >&2
  exit 1
fi

eval "$proxy_env"

python3 - "$sources_json" "$bindir" "$lock_json" <<'PY'
import json, os, subprocess, sys, urllib.parse
import tempfile, tarfile

sources_path, bindir, lock_path = sys.argv[1], sys.argv[2], sys.argv[3]

with open(sources_path, "r", encoding="utf-8") as f:
    sources = json.load(f)

tools = sources.get("tools", [])
os.makedirs(bindir, exist_ok=True)

lock = {"schemaVersion": 1, "tools": []}

def sh(*args):
    subprocess.check_call(list(args))

def out(*args):
    return subprocess.check_output(list(args), text=True).strip()

for t in tools:
    name = t["name"]
    url = t["url"]
    target = os.path.join(bindir, name)
    print(f"[tools] downloading {name} from {url}")
    extract = t.get("extract")
    if extract and extract.get("type") == "tar.gz":
        with tempfile.TemporaryDirectory() as td:
            archive = os.path.join(td, "tool.tar.gz")
            sh("curl", "-fL", url, "-o", archive)
            with tarfile.open(archive, "r:gz") as tf:
                bin_name = extract.get("binary", name)
                member = tf.getmember(bin_name) if bin_name in tf.getnames() else None
                if member is None:
                    # fallback: take first file named like bin_name
                    for n in tf.getnames():
                        if n.endswith("/" + bin_name) or n == bin_name:
                            member = tf.getmember(n)
                            break
                if member is None:
                    raise RuntimeError(f"cannot find {bin_name} in archive")
                tf.extract(member, path=td)
                extracted = os.path.join(td, member.name)
                sh("cp", extracted, target)
        sh("chmod", "+x", target)
    else:
        sh("curl", "-fL", url, "-o", target)
        sh("chmod", "+x", target)
    sha = out("sha256sum", target).split()[0]
    lock["tools"].append({
        "name": name,
        "version": t.get("version", ""),
        "url": url,
        "sha256": sha,
        "path": os.path.relpath(target, start=os.getcwd()),
    })

os.makedirs(os.path.dirname(lock_path), exist_ok=True)
with open(lock_path, "w", encoding="utf-8") as f:
    json.dump(lock, f, indent=2, ensure_ascii=False)
print(f"[tools] wrote lock: {lock_path}")
PY
