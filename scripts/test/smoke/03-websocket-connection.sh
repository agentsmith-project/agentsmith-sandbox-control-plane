#!/bin/bash
# scripts/test/smoke/03-websocket-connection.sh
# Smoke test scenario: WebSocket connection

set -e

# Source scenarios
source "$(dirname "$0")/../lib/scenarios.sh"
source "$(dirname "$0")/../lib/assertions.sh"

# Read SANDBOX_ID from previous test
if [ -f /tmp/smoke-test-sandbox-id.txt ]; then
    SANDBOX_ID=$(cat /tmp/smoke-test-sandbox-id.txt)
else
    echo -e "${COLOR_YELLOW}⊘ SKIPPED: No sandbox ID found, run create-sandbox first${COLOR_NC}"
    exit 0
fi

echo "Testing WebSocket connection for sandbox ${SANDBOX_ID}..."

# Note: Full WebSocket testing requires a WebSocket client
# This is a basic HTTP-level check to verify the endpoint is accessible

# 1. Check WebSocket endpoint is accessible (will fail with WebSocket upgrade expected)
echo "  Checking WebSocket endpoint..."
response=$(curl -s -w "\n%{http_code}" \
    "http://localhost:8080/ws?id=${SANDBOX_ID}" \
    -H "Connection: Upgrade" \
    -H "Upgrade: websocket" \
    --max-time 5 2>/dev/null || echo "000\n000")

status=$(echo "${response}" | tail -n1)

# We expect either 426 (Upgrade Required) or a WebSocket-related response
# The important thing is the server responds
if [ "${status}" == "000" ]; then
    echo -e "${COLOR_RED}✗ WebSocket endpoint not accessible${COLOR_NC}"
    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}WebSocket endpoint accessible (status: ${status})${COLOR_NC}"

# 2. Test session touch via API
echo "  Testing session touch..."
if ! touch_session "${MANAGER_URL}" "${SERVICE_KEY}" "${SANDBOX_ID}"; then
    echo -e "${COLOR_RED}✗ Session touch failed${COLOR_NC}"
    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Session touch successful${COLOR_NC}"

# 3. Verify pod is still running
echo "  Verifying pod is still running..."
if ! check_pod_ready "${SANDBOX_ID}" "${SANDBOX_NAMESPACE}"; then
    echo -e "${COLOR_RED}✗ Pod is not running${COLOR_NC}"
    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Pod is still running${COLOR_NC}"

echo ""
echo -e "${COLOR_GREEN}✓ WebSocket connection test passed${COLOR_NC}"

exit 0
