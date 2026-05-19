#!/bin/bash
# Test script for agentsmith-sandbox-control-plane workspace/project/workload API
# Usage: ./scripts/test-asbcp-api.sh [asbcp-url] [service-key]
# Example: ./scripts/test-asbcp-api.sh http://localhost:8080 test-key-123

set -e

ASBCP_URL="${1:-http://localhost:8080}"
SERVICE_KEY="${2:-test-key-123}"
WS_ID="ws-1"
PROJ_ID="proj-1"
WL_ID="wl-test-$(date +%s)"
BINDING_ID="${BINDING_ID:-wmb_demo}"
AFSCP_NAMESPACE_ID="${AFSCP_NAMESPACE_ID:-ns_demo}"

redact_secret() {
  local value="${1:-}"
  if [ -z "$value" ]; then
    echo "<empty>"
    return
  fi
  if [ "${#value}" -le 8 ]; then
    echo "<redacted>"
    return
  fi
  printf '%s...%s\n' "${value:0:4}" "${value: -4}"
}

echo "=== ASBCP API Test ==="
echo "ASBCP URL: $ASBCP_URL"
echo "Service Key: $(redact_secret "$SERVICE_KEY")"
echo "Workload: $WS_ID / $PROJ_ID / $WL_ID"
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

test_result() {
  if [ $1 -eq 0 ]; then
    echo -e "${GREEN}✓ $2${NC}"
  else
    echo -e "${RED}✗ $2${NC}"
    exit 1
  fi
}

# 1. Health check endpoint (no auth required)
echo "1. Testing /healthz (no auth)..."
curl -s -o /dev/null -w "%{http_code}" "${ASBCP_URL}/healthz" | grep -q "200"
test_result $? "/healthz returned 200"

# 2. Readiness check endpoint (no auth required)
echo "2. Testing /readyz (no auth)..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${ASBCP_URL}/readyz")
if [ "$STATUS" == "200" ] || [ "$STATUS" == "503" ]; then
  test_result 0 "/readyz returned 200 or 503 (service may not be ready)"
else
  test_result 1 "/readyz returned unexpected status: $STATUS"
fi

# 3. Metrics endpoint (no auth required, unless configured)
echo "3. Testing /metrics (no auth)..."
curl -s -o /dev/null -w "%{http_code}" "${ASBCP_URL}/metrics" | grep -q "200"
test_result $? "/metrics returned 200"

# 4. Ensure workspace binding (requires auth)
echo "4. Testing PUT /v1/workspaces/{wsId}/projects/{projId}/workspace-bindings/{bindingId} (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X PUT "${ASBCP_URL}/v1/workspaces/${WS_ID}/projects/${PROJ_ID}/workspace-bindings/${BINDING_ID}" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d "{
    \"namespace_id\": \"${AFSCP_NAMESPACE_ID}\",
    \"mount_binding_id\": \"${BINDING_ID}\"
  }")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "200" ]; then
  test_result 0 "Ensure workspace binding returned $HTTP_CODE"
elif [ "$HTTP_CODE" == "401" ]; then
  test_result 1 "Ensure workspace binding returned 401 (invalid service key)"
else
  test_result 1 "Ensure workspace binding returned unexpected status: $HTTP_CODE - $BODY"
fi

# 5. Create workload (requires auth)
echo "5. Testing PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId} (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X PUT "${ASBCP_URL}/v1/workspaces/${WS_ID}/projects/${PROJ_ID}/workloads/${WL_ID}" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d "{
    \"image\": \"ubuntu:22.04\",
    \"workspace_binding_id\": \"${BINDING_ID}\",
    \"idle_timeout_sec\": 900,
    \"max_lifetime_sec\": 3600,
    \"env\": {}
  }")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "201" ]; then
  test_result 0 "Create workload returned $HTTP_CODE"
  POD_NAME=$(echo "$BODY" | grep -o '"pod_name":"[^"]*"' | cut -d'"' -f4)
  echo "   Pod name: $POD_NAME"
elif [ "$HTTP_CODE" == "401" ]; then
  test_result 1 "Create workload returned 401 (invalid service key)"
else
  test_result 1 "Create workload returned unexpected status: $HTTP_CODE - $BODY"
fi

# 6. Keepalive workload (requires auth)
echo "6. Testing POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${ASBCP_URL}/v1/workspaces/${WS_ID}/projects/${PROJ_ID}/workloads/${WL_ID}/keepalive" \
  -H "X-Service-Key: ${SERVICE_KEY}")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Keepalive workload returned $HTTP_CODE"
else
  test_result 1 "Keepalive workload returned unexpected status: $HTTP_CODE"
fi

# 7. Exec command (requires auth)
echo "7. Testing POST /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${ASBCP_URL}/v1/workspaces/${WS_ID}/projects/${PROJ_ID}/workloads/${WL_ID}/exec" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "cmd": ["echo", "Hello from workload"],
    "timeout_seconds": 10
  }')
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "200" ]; then
  test_result 0 "Exec returned 200"
  EXIT_CODE=$(echo "$BODY" | grep -Eo '"exit_code"[[:space:]]*:[[:space:]]*[0-9-]+' | sed -E 's/.*:[[:space:]]*//' | tr -d ' ')
  echo "   Exit code: $EXIT_CODE"
elif [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Exec returned 404 (pod not found - expected if no cluster)"
else
  test_result 1 "Exec returned unexpected status: $HTTP_CODE - $BODY"
fi

# 8. Delete workload (requires auth)
echo "8. Testing DELETE /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId} (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X DELETE "${ASBCP_URL}/v1/workspaces/${WS_ID}/projects/${PROJ_ID}/workloads/${WL_ID}" \
  -H "X-Service-Key: ${SERVICE_KEY}")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "200" ]; then
  test_result 0 "Delete returned 200 (release confirmed)"
else
  test_result 1 "Delete expected confirmed release success (200), got: $HTTP_CODE - $BODY"
fi

# 9. Test auth failure
echo "9. Testing auth failure (invalid key)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X PUT "${ASBCP_URL}/v1/workspaces/${WS_ID}/projects/${PROJ_ID}/workloads/auth-test" \
  -H "X-Service-Key: invalid-key-123" \
  -H "Content-Type: application/json" \
  -d '{"image": "ubuntu:22.04"}')
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "401" ]; then
  test_result 0 "Auth failed with 401"
else
  test_result 1 "Auth test should return 401, got: $HTTP_CODE"
fi

echo ""
echo -e "${GREEN}=== All tests passed! ===${NC}"
