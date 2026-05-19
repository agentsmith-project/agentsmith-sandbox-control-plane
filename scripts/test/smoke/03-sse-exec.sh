#!/bin/bash
# Smoke test: command execution over the active workload API.
# Tests create -> exec -> keepalive -> delete using /v1/workspaces/.../workloads.

set -euo pipefail

ASBCP_URL="${ASBCP_URL:-${ASBCP_HTTP_URL:-http://localhost:8080}}"
SERVICE_KEY="${SERVICE_KEY:-test-key-123}"
WORKSPACE_ID="${WORKSPACE_ID:-ws-1}"
PROJECT_ID="${PROJECT_ID:-proj-1}"
WORKSPACE_BINDING_ID="${WORKSPACE_BINDING_ID:-wmb_demo}"
AFSCP_NAMESPACE_ID="${AFSCP_NAMESPACE_ID:-ns_demo}"
SESSION_ID="${SBX_SESSION_ID:-smoke-test-sse-$(date +%s)}"
WORKLOAD_URL="${ASBCP_URL}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${SESSION_ID}"

echo "=== ASBCP Exec Smoke Test ==="
echo "ASBCP URL:   ${ASBCP_URL}"
echo "Workload ID: ${SESSION_ID}"
echo ""

# Step 1: Ensure workspace binding.
echo "--- Step 1: Ensure workspace binding ---"
BINDING_STATUS=$(curl -s --max-time 120 -o /tmp/asbcp-smoke-binding.json -w "%{http_code}" -X PUT \
  "${ASBCP_URL}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workspace-bindings/${WORKSPACE_BINDING_ID}" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"namespace_id\":\"${AFSCP_NAMESPACE_ID}\",\"mount_binding_id\":\"${WORKSPACE_BINDING_ID}\"}")

if [ "$BINDING_STATUS" -ne 200 ]; then
  echo "FAIL: Ensure workspace binding returned ${BINDING_STATUS}"
  cat /tmp/asbcp-smoke-binding.json
  exit 1
fi
echo "PASS: Workspace binding ready"
echo ""

# Step 2: Create workload (ASBCP waits for pod ready, allow up to 120s)
echo "--- Step 2: Create workload ---"
CREATE_RESPONSE=$(curl -s --max-time 120 -w "\n%{http_code}" -X PUT \
  "${WORKLOAD_URL}" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"image\":\"${WORKLOAD_IMAGE:-ubuntu:22.04}\",\"workspace_binding_id\":\"${WORKSPACE_BINDING_ID}\",\"idle_timeout_sec\":300,\"max_lifetime_sec\":3600}")

CREATE_STATUS=$(echo "$CREATE_RESPONSE" | tail -1)
CREATE_BODY=$(echo "$CREATE_RESPONSE" | head -n -1)

if [ "$CREATE_STATUS" -ne 200 ] && [ "$CREATE_STATUS" -ne 201 ]; then
  echo "FAIL: Create workload returned ${CREATE_STATUS}"
  echo "Body: ${CREATE_BODY}"
  exit 1
fi
echo "PASS: Create workload returned ${CREATE_STATUS}"
echo "Body: ${CREATE_BODY}"
echo ""

# Step 3: Execute command
echo "--- Step 3: Execute 'echo hello' ---"
EXEC_RESPONSE=$(curl -s --max-time 30 -w "\n%{http_code}" -X POST \
  "${WORKLOAD_URL}/exec" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["echo", "hello"], "timeout_seconds": 10}')
EXEC_STATUS=$(echo "$EXEC_RESPONSE" | tail -1)
EXEC_BODY=$(echo "$EXEC_RESPONSE" | head -n -1)

echo "Exec Response:"
echo "${EXEC_BODY}"
echo ""

if [ "$EXEC_STATUS" -ne 200 ]; then
  echo "FAIL: Exec returned ${EXEC_STATUS}"
  exit 1
fi

if echo "$EXEC_BODY" | grep -q '"stdout"'; then
  echo "PASS: Got stdout"
else
  echo "FAIL: Missing stdout"
  exit 1
fi

# Check exit code is 0.
if echo "$EXEC_BODY" | grep -Eq '"exit_code"[[:space:]]*:[[:space:]]*0'; then
  echo "PASS: Exit code is 0"
else
  echo "FAIL: Exit code is not 0"
  exit 1
fi
echo ""

# Step 4: Execute failing command
echo "--- Step 4: Execute failing command ---"
FAIL_RESPONSE=$(curl -s --max-time 30 -w "\n%{http_code}" -X POST \
  "${WORKLOAD_URL}/exec" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["ls", "/nonexistent-path-12345"], "timeout_seconds": 10}')
FAIL_STATUS=$(echo "$FAIL_RESPONSE" | tail -1)
FAIL_BODY=$(echo "$FAIL_RESPONSE" | head -n -1)

echo "Exec Response:"
echo "${FAIL_BODY}"
echo ""

if [ "$FAIL_STATUS" -eq 200 ] && echo "$FAIL_BODY" | grep -q '"exit_code"'; then
  echo "PASS: Got exit code for failing command"
else
  echo "FAIL: Missing exec result for failing command"
  exit 1
fi
echo ""

# Step 5: Keepalive workload
echo "--- Step 5: Keepalive workload ---"
TOUCH_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  "${WORKLOAD_URL}/keepalive" \
  -H "X-Service-Key: ${SERVICE_KEY}")

if [ "$TOUCH_STATUS" -eq 200 ]; then
  echo "PASS: Keepalive returned 200"
else
  echo "FAIL: Keepalive returned ${TOUCH_STATUS}"
  exit 1
fi
echo ""

# Step 6: Delete workload (release path)
echo "--- Step 6: Delete workload ---"
DELETE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE \
  "${WORKLOAD_URL}" \
  -H "X-Service-Key: ${SERVICE_KEY}")

if [ "$DELETE_STATUS" -eq 200 ]; then
  echo "PASS: Delete returned 200"
else
  echo "FAIL: Delete returned ${DELETE_STATUS}"
  exit 1
fi
echo ""

echo "=== ASBCP Exec Smoke Test PASSED ==="
