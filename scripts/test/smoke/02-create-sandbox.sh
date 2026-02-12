#!/bin/bash
# scripts/test/smoke/02-create-sandbox.sh
# Smoke test scenario: Create sandbox

set -e

# Source scenarios
source "$(dirname "$0")/../lib/scenarios.sh"
source "$(dirname "$0")/../lib/assertions.sh"

SANDBOX_ID="test-smoke-$(date +%s)"

echo "Creating sandbox ${SANDBOX_ID}..."

# 1. Create sandbox via API (allow long timeout: Manager waits for pod ready)
MANAGER_TIMEOUT=120
export MANAGER_TIMEOUT
echo "  Sending create request (timeout ${MANAGER_TIMEOUT}s)..."
set +e
response=$(create_sandbox "${MANAGER_URL}" "${SANDBOX_ID}")
create_status=$?
set -e

if [ ${create_status} -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Failed to create sandbox${COLOR_NC}"
    echo "Response: ${response}"
    exit 1
fi

# Parse pod name from create response (Manager returns sbx-<hash>, not sandbox-<id>)
pod_name=$(echo "${response}" | jq -r '.podName')
if [ -z "${pod_name}" ] || [ "${pod_name}" = "null" ]; then
    echo -e "${COLOR_RED}✗ Create response missing podName${COLOR_NC}"
    echo "Response: ${response}"
    exit 1
fi
export SANDBOX_POD_NAME="${pod_name}"
echo "${pod_name}" > /tmp/smoke-test-pod-name.txt

echo -e "  ${COLOR_GREEN}Create request accepted (pod: ${pod_name})${COLOR_NC}"

# 2. Verify pod was created
echo "  Waiting for pod to be created..."
sleep 2

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

# 4. Session is ready (pod ready is sufficient; API has no GET session endpoint)

# 5. Save SANDBOX_ID for subsequent tests
echo "${SANDBOX_ID}" > /tmp/smoke-test-sandbox-id.txt

echo ""
echo -e "${COLOR_GREEN}✓ Sandbox creation test passed${COLOR_NC}"
echo "  Sandbox ID: ${SANDBOX_ID}"

exit 0
