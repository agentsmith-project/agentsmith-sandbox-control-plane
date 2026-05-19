#!/bin/bash
set -euo pipefail

# ASBCP 本地构建脚本（Go 二进制）

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVICE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "$SERVICE_DIR"

echo "[asbcp] Building Go binary..."

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

# 构建
VERSION="$(cat "${SERVICE_DIR}/../VERSION" 2>/dev/null || echo "dev")"
go build -ldflags "-X github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/app.version=${VERSION}" -o asbcp ./cmd/asbcp

echo "[asbcp] ✓ Build completed: ${SERVICE_DIR}/asbcp"
