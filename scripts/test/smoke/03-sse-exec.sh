#!/bin/bash
# Smoke test: SSE command execution
# Tests the POST /v1/sandboxes/{id}/exec endpoint with SSE streaming

set -euo pipefail

MANAGER_URL="${SBX_MANAGER_HTTP_URL:-http://localhost:8080}"
SESSION_ID="${SBX_SESSION_ID:-smoke-test-sse-$(date +%s)}"

echo "=== SSE Exec Smoke Test ==="
echo "Manager URL: ${MANAGER_URL}"
echo "Session ID:  ${SESSION_ID}"
echo ""

# Step 1: Create sandbox (Manager waits for pod ready, allow up to 120s)
echo "--- Step 1: Create sandbox ---"
CREATE_RESPONSE=$(curl -s --max-time 120 -w "\n%{http_code}" -X PUT \
  "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}" \
  -H "Content-Type: application/json" \
  -d '{"ttlSeconds": 300}')

CREATE_STATUS=$(echo "$CREATE_RESPONSE" | tail -1)
CREATE_BODY=$(echo "$CREATE_RESPONSE" | head -n -1)

if [ "$CREATE_STATUS" -ne 200 ]; then
  echo "FAIL: Create sandbox returned ${CREATE_STATUS}"
  echo "Body: ${CREATE_BODY}"
  exit 1
fi
echo "PASS: Create sandbox returned 200"
echo "Body: ${CREATE_BODY}"
echo ""

# Step 2: Execute command via SSE
echo "--- Step 2: Execute 'echo hello' via SSE ---"
EXEC_RESPONSE=$(curl -s -N -X POST \
  "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/exec" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["echo", "hello"]}' \
  --max-time 30)

echo "SSE Response:"
echo "${EXEC_RESPONSE}"
echo ""

# Check for stdout event
if echo "$EXEC_RESPONSE" | grep -q "event: stdout"; then
  echo "PASS: Got stdout event"
else
  echo "FAIL: Missing stdout event"
  exit 1
fi

# Check for exit event
if echo "$EXEC_RESPONSE" | grep -q "event: exit"; then
  echo "PASS: Got exit event"
else
  echo "FAIL: Missing exit event"
  exit 1
fi

# Check exit code is 0
if echo "$EXEC_RESPONSE" | grep -q '"exit_code":0'; then
  echo "PASS: Exit code is 0"
else
  echo "FAIL: Exit code is not 0"
  exit 1
fi
echo ""

# Step 3: Execute failing command
echo "--- Step 3: Execute failing command ---"
FAIL_RESPONSE=$(curl -s -N -X POST \
  "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/exec" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["ls", "/nonexistent-path-12345"]}' \
  --max-time 30)

echo "SSE Response:"
echo "${FAIL_RESPONSE}"
echo ""

if echo "$FAIL_RESPONSE" | grep -q "event: exit"; then
  echo "PASS: Got exit event for failing command"
else
  echo "FAIL: Missing exit event for failing command"
  exit 1
fi
echo ""

# Step 4: Touch sandbox
echo "--- Step 4: Touch sandbox ---"
TOUCH_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/touch")

if [ "$TOUCH_STATUS" -eq 200 ]; then
  echo "PASS: Touch returned 200"
else
  echo "FAIL: Touch returned ${TOUCH_STATUS}"
  exit 1
fi
echo ""

# Step 5: Delete sandbox
echo "--- Step 5: Delete sandbox ---"
DELETE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
  "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}")

if [ "$DELETE_STATUS" -eq 204 ]; then
  echo "PASS: Delete returned 204"
else
  echo "FAIL: Delete returned ${DELETE_STATUS}"
  exit 1
fi
echo ""

echo "=== All SSE Exec Smoke Tests PASSED ==="
