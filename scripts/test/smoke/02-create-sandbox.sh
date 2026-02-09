#!/bin/bash
# scripts/test/smoke/02-create-sandbox.sh
# Smoke test scenario: Create sandbox

set -e

# Source scenarios
source "$(dirname "$0")/../lib/scenarios.sh"
source "$(dirname "$0")/../lib/assertions.sh"

SANDBOX_ID="test-smoke-$(date +%s)"

echo "Creating sandbox ${SANDBOX_ID}..."

# 1. Create sandbox via API
echo "  Sending create request..."
response=$(create_sandbox "${MANAGER_URL}" "${SERVICE_KEY}" "${SANDBOX_ID}")
create_status=$?

if [ ${create_status} -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Failed to create sandbox${COLOR_NC}"
    echo "Response: ${response}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Create request accepted${COLOR_NC}"

# 2. Verify pod was created
echo "  Waiting for pod to be created..."
sleep 2

pod_name="sandbox-${SANDBOX_ID}"
kubectl get pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" &> /dev/null
if [ $? -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Pod ${pod_name} not found${COLOR_NC}"
    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Pod ${pod_name} created${COLOR_NC}"

# 3. Wait for pod to be ready
echo "  Waiting for pod to be ready (max 120s)..."
if ! wait_for_pod_ready "${SANDBOX_ID}" "${SANDBOX_NAMESPACE}" 120 2; then
    echo -e "${COLOR_RED}✗ Pod did not become ready in time${COLOR_NC}"

    # Get pod status for debugging
    echo "Pod status:"
    kubectl get pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" -o json | jq '.status'

    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Pod is ready${COLOR_NC}"

# 4. Verify session via API
echo "  Verifying session..."
session=$(get_session "${MANAGER_URL}" "${SERVICE_KEY}" "${SANDBOX_ID}")
get_status=$?

if [ ${get_status} -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Failed to get session${COLOR_NC}"
    echo "Response: ${session}"
    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

# Verify session state
session_state=$(echo "${session}" | jq -r '.state // empty')
if [ "${session_state}" != "ready" ]; then
    echo -e "${COLOR_RED}✗ Session state is '${session_state}', expected 'ready'${COLOR_NC}"
    cleanup_sandbox "${SANDBOX_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Session verified (state: ${session_state})${COLOR_NC}"

# 5. Save SANDBOX_ID for subsequent tests
echo "${SANDBOX_ID}" > /tmp/smoke-test-sandbox-id.txt

echo ""
echo -e "${COLOR_GREEN}✓ Sandbox creation test passed${COLOR_NC}"
echo "  Sandbox ID: ${SANDBOX_ID}"

exit 0
