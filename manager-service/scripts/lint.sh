#!/bin/bash
set -euo pipefail

# Manager 代码检查脚本

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVICE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "$SERVICE_DIR"

echo "[manager] Running linters..."

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

# Go vet
echo "  [go vet]"
go vet ./...

# Go fmt check
echo "  [go fmt]"
if [ "$(gofmt -l . | wc -l)" -gt 0 ]; then
    echo "  ✗ Code is not formatted. Run: gofmt -w ."
    exit 1
fi

echo "[manager] ✓ Lint checks passed"
