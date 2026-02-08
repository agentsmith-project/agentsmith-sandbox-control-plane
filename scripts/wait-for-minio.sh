#!/usr/bin/env bash
set -euo pipefail

MINIO_ENDPOINT="${MINIO_ENDPOINT:-http://localhost:9000}"
TIMEOUT="${TIMEOUT:-60}"
ELAPSED=0

echo "Waiting for MinIO at $MINIO_ENDPOINT (timeout: ${TIMEOUT}s)..."

while [ $ELAPSED -lt $TIMEOUT ]; do
  if curl -sf "$MINIO_ENDPOINT/minio/health/live" >/dev/null 2>&1; then
    echo "MinIO is ready!"
    exit 0
  fi
  sleep 2
  ELAPSED=$((ELAPSED + 2))
  echo "Still waiting... (${ELAPSED}s/${TIMEOUT}s)"
done

echo "ERROR: MinIO did not become ready within ${TIMEOUT}s" >&2
exit 1
