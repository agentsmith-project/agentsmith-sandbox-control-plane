#!/bin/bash
set -euo pipefail

# ASBCP 测试脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVICE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "$SERVICE_DIR"

echo "[asbcp] Running tests..."

ensure_writable_gocache() {
  if [ -n "${GOCACHE:-}" ]; then
    return 0
  fi

  local cache testfile
  cache="$(go env GOCACHE 2>/dev/null || true)"
  if [ -z "$cache" ]; then
    export GOCACHE="/tmp/go-cache"
    mkdir -p "$GOCACHE" 2>/dev/null || true
    return 0
  fi

  testfile="${cache}/.gocache_write_test.$$"
  if ! (mkdir -p "$cache" 2>/dev/null && touch "$testfile" 2>/dev/null); then
    export GOCACHE="/tmp/go-cache"
    mkdir -p "$GOCACHE" 2>/dev/null || true
    return 0
  fi
  rm -f "$testfile" 2>/dev/null || true
}

ensure_writable_gocache

# 运行 Go 测试（如果有）
if [ -d "test" ] || find . -name "*_test.go" | grep -q .; then
    go test ./...
else
    echo "[asbcp] No tests found, skipping"
fi

echo "[asbcp] ✓ Tests completed"
