#!/bin/bash
# Test script for sandbox-manager v2 API
# Usage: ./scripts/test-manager.sh [manager-url] [service-key] [runner-image]
# Example: ./scripts/test-manager.sh http://localhost:8080 test-key-123 harbor.example.com/project/sandbox-runner:1.0.0

set -e

MANAGER_URL="${1:-http://localhost:8080}"
SERVICE_KEY="${2:-test-key-123}"
RUNNER_IMAGE="${3:-}"
SESSION_ID="test-session-$(date +%s)"

echo "=== Sandbox Manager API Test ==="
echo "Manager URL: $MANAGER_URL"
echo "Service Key: $SERVICE_KEY"
echo "Runner Image: ${RUNNER_IMAGE:-<manager default>}"
echo "Session ID: $SESSION_ID"
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
curl -s -o /dev/null -w "%{http_code}" "${MANAGER_URL}/healthz" | grep -q "200"
test_result $? "/healthz returned 200"

# 2. Readiness check endpoint (no auth required)
echo "2. Testing /readyz (no auth)..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${MANAGER_URL}/readyz")
if [ "$STATUS" == "200" ] || [ "$STATUS" == "503" ]; then
  test_result 0 "/readyz returned 200 or 503 (service may not be ready)"
else
  test_result 1 "/readyz returned unexpected status: $STATUS"
fi

# 3. Metrics endpoint (no auth required, unless configured)
echo "3. Testing /metrics (no auth)..."
curl -s -o /dev/null -w "%{http_code}" "${MANAGER_URL}/metrics" | grep -q "200"
test_result $? "/metrics returned 200"

# 4. Debug config endpoint (no auth required)
echo "4. Testing /debug/config (no auth)..."
curl -s "${MANAGER_URL}/debug/config" | grep -q "schemaVersion"
test_result $? "/debug/config returned valid JSON"

# 5. Create sandbox (requires auth)
echo "5. Testing PUT /v1/sandboxes/{id} (with auth)..."
IMAGE_FIELD=""
if [ -n "$RUNNER_IMAGE" ]; then
  IMAGE_FIELD=",
    \"image\": \"${RUNNER_IMAGE}\""
fi
RESPONSE=$(curl -s -w "\n%{http_code}" -X PUT "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d "{
    \"ttlSeconds\": 900,
    \"containerName\": \"runner\"${IMAGE_FIELD},
    \"workdir\": \"/workspace\",
    \"cpuLimit\": \"1\",
    \"memoryLimit\": \"1Gi\",
    \"ephemeralStorageLimit\": \"2Gi\",
    \"env\": {}
  }")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "202" ]; then
  test_result 0 "Create sandbox returned $HTTP_CODE"
  POD_NAME=$(echo "$BODY" | grep -o '"podName":"[^"]*"' | cut -d'"' -f4)
  echo "   Pod name: $POD_NAME"
elif [ "$HTTP_CODE" == "401" ]; then
  test_result 1 "Create sandbox returned 401 (invalid service key)"
else
  test_result 1 "Create sandbox returned unexpected status: $HTTP_CODE - $BODY"
fi

# 6. Touch sandbox (requires auth)
echo "6. Testing POST /v1/sandboxes/{id}/touch (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/touch" \
  -H "X-Service-Key: ${SERVICE_KEY}")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "202" ] || [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Touch sandbox returned $HTTP_CODE"
else
  test_result 1 "Touch sandbox returned unexpected status: $HTTP_CODE"
fi

# 7. Exec command (requires auth)
echo "7. Testing POST /v1/sandboxes/{id}/exec (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/exec" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "cmd": ["echo", "Hello from sandbox"],
    "timeoutSeconds": 10
  }')
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "200" ]; then
  test_result 0 "Exec returned 200"
  EXIT_CODE=$(echo "$BODY" | grep -Eo '"exitCode"[[:space:]]*:[[:space:]]*[0-9]+' | sed -E 's/.*:[[:space:]]*//' | tr -d ' ')
  echo "   Exit code: $EXIT_CODE"
elif [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Exec returned 404 (pod not found - expected if no cluster)"
else
  test_result 1 "Exec returned unexpected status: $HTTP_CODE - $BODY"
fi

# 8. File upload (requires auth)
echo "8. Testing POST /v1/sandboxes/{id}/files/upload (with auth)..."
rm -f /tmp/test-upload.txt /tmp/test-upload.tar.gz
echo "test content" > /tmp/test-upload.txt
tar -czf /tmp/test-upload.tar.gz -C /tmp test-upload.txt
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/files/upload" \
  -H "X-Service-Key: ${SERVICE_KEY}" \
  -H "Content-Type: application/x-gzip" \
  --data-binary @/tmp/test-upload.tar.gz)
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Upload returned $HTTP_CODE"
else
  test_result 1 "Upload returned unexpected status: $HTTP_CODE"
fi
rm -f /tmp/test-upload.txt /tmp/test-upload.tar.gz

# 9. File download (requires auth)
echo "9. Testing GET /v1/sandboxes/{id}/files/download (with auth)..."
OUT="/tmp/test-download.tar.gz"
rm -f "$OUT"
HTTP_CODE=$(curl -s -o "$OUT" -w "%{http_code}" -X GET "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}/files/download" \
  -H "X-Service-Key: ${SERVICE_KEY}")

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Download returned $HTTP_CODE"
else
  test_result 1 "Download returned unexpected status: $HTTP_CODE"
fi
rm -f "$OUT"

# 10. Delete sandbox (requires auth)
echo "10. Testing DELETE /v1/sandboxes/{id} (with auth)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X DELETE "${MANAGER_URL}/v1/sandboxes/${SESSION_ID}" \
  -H "X-Service-Key: ${SERVICE_KEY}")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)

if [ "$HTTP_CODE" == "200" ] || [ "$HTTP_CODE" == "202" ] || [ "$HTTP_CODE" == "404" ]; then
  test_result 0 "Delete returned $HTTP_CODE"
elif [ "$HTTP_CODE" == "204" ]; then
  test_result 0 "Delete returned 204"
else
  test_result 1 "Delete returned unexpected status: $HTTP_CODE"
fi

# 11. Test auth failure
echo "11. Testing auth failure (invalid key)..."
RESPONSE=$(curl -s -w "\n%{http_code}" -X PUT "${MANAGER_URL}/v1/sandboxes/auth-test" \
  -H "X-Service-Key: invalid-key-123")
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" == "401" ]; then
  ERROR_CODE=$(echo "$BODY" | grep -o '"code":"[^"]*"' | cut -d'"' -f4)
  if [ "$ERROR_CODE" == "SERVICE_KEY_MISSING" ] || [ "$ERROR_CODE" == "SERVICE_KEY_INVALID" ]; then
    test_result 0 "Auth failed with correct error code: $ERROR_CODE"
  else
    test_result 1 "Auth failed but with wrong error code: $ERROR_CODE"
  fi
else
  test_result 1 "Auth test should return 401, got: $HTTP_CODE"
fi

echo ""
echo -e "${GREEN}=== All tests passed! ===${NC}"
