#!/bin/bash
# scripts/test/smoke/02-create-workload.sh
# Smoke test scenario: create workload.

set -e

# Source scenarios
source "$(dirname "$0")/../lib/scenarios.sh"
source "$(dirname "$0")/../lib/assertions.sh"

WORKLOAD_ID="test-smoke-$(date +%s)"

echo "Creating workload ${WORKLOAD_ID}..."

# 1. Ensure workspace binding via API.
ASBCP_TIMEOUT=120
export ASBCP_TIMEOUT
echo "  Ensuring workspace binding ${WORKSPACE_BINDING_ID}..."
set +e
binding_response=$(ensure_workspace_binding "${ASBCP_URL}" "${WORKSPACE_BINDING_ID}")
binding_status=$?
set -e

if [ ${binding_status} -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Failed to ensure workspace binding${COLOR_NC}"
    echo "Response: ${binding_response}"
    exit 1
fi

# 2. Create workload via API (allow long timeout: ASBCP waits for pod ready)
echo "  Sending create request (timeout ${ASBCP_TIMEOUT}s)..."
set +e
response=$(create_workload "${ASBCP_URL}" "${WORKLOAD_ID}")
create_status=$?
set -e

if [ ${create_status} -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Failed to create workload${COLOR_NC}"
    echo "Response: ${response}"
    exit 1
fi

# Parse pod name from create response.
pod_name=$(echo "${response}" | jq -r '.pod_name')
if [ -z "${pod_name}" ] || [ "${pod_name}" = "null" ]; then
    echo -e "${COLOR_RED}✗ Create response missing podName${COLOR_NC}"
    echo "Response: ${response}"
    exit 1
fi
export WORKLOAD_POD_NAME="${pod_name}"
echo "${pod_name}" > /tmp/asbcp-smoke-pod-name.txt

echo -e "  ${COLOR_GREEN}Create request accepted (pod: ${pod_name})${COLOR_NC}"

# 3. Verify pod was created
echo "  Waiting for pod to be created..."
sleep 2

kubectl get pod "${pod_name}" -n "${WORKLOAD_NAMESPACE}" &> /dev/null
if [ $? -ne 0 ]; then
    echo -e "${COLOR_RED}✗ Pod ${pod_name} not found${COLOR_NC}"
    cleanup_workload "${WORKLOAD_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Pod ${pod_name} created${COLOR_NC}"

# 4. Wait for pod to be ready
echo "  Waiting for pod to be ready (max 120s)..."
if ! wait_for_pod_ready "${WORKLOAD_ID}" "${WORKLOAD_NAMESPACE}" 120 2; then
    echo -e "${COLOR_RED}✗ Pod did not become ready in time${COLOR_NC}"

    # Get pod status for debugging
    echo "Pod status:"
    kubectl get pod "${pod_name}" -n "${WORKLOAD_NAMESPACE}" -o json | jq '.status'

    cleanup_workload "${WORKLOAD_ID}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Pod is ready${COLOR_NC}"

# 5. Workload is ready (pod ready is sufficient for this smoke stage)

# 6. Save WORKLOAD_ID for subsequent tests
echo "${WORKLOAD_ID}" > /tmp/asbcp-smoke-workload-id.txt

echo ""
echo -e "${COLOR_GREEN}✓ Workload creation test passed${COLOR_NC}"
echo "  Workload ID: ${WORKLOAD_ID}"

exit 0
