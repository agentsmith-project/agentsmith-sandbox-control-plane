#!/bin/bash
# scripts/test/smoke/05-cleanup.sh
# Smoke test scenario: Cleanup resources

set -e

# Source scenarios
source "$(dirname "$0")/../lib/scenarios.sh"
source "$(dirname "$0")/../lib/assertions.sh"

# Read SANDBOX_ID and pod name from previous test
if [ -f /tmp/smoke-test-sandbox-id.txt ]; then
    SANDBOX_ID=$(cat /tmp/smoke-test-sandbox-id.txt)
    rm /tmp/smoke-test-sandbox-id.txt
else
    echo -e "${COLOR_YELLOW}⊘ SKIPPED: No sandbox ID found, nothing to clean${COLOR_NC}"
    exit 0
fi
if [ -f /tmp/smoke-test-pod-name.txt ]; then
    SANDBOX_POD_NAME=$(cat /tmp/smoke-test-pod-name.txt)
    export SANDBOX_POD_NAME
    rm /tmp/smoke-test-pod-name.txt
fi
pod_name="${SANDBOX_POD_NAME:-workload-${SANDBOX_ID}}"

echo "Cleaning up sandbox ${SANDBOX_ID} (pod: ${pod_name})..."

# 1. Delete sandbox via API
echo "  Deleting sandbox via API..."
if ! delete_sandbox "${MANAGER_URL}" "${SANDBOX_ID}"; then
    echo -e "${COLOR_RED}✗ API delete failed${COLOR_NC}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Sandbox deleted${COLOR_NC}"

# 2. Wait for pod to be deleted
echo "  Waiting for pod to be deleted..."

for i in {1..30}; do
    kubectl get pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "  ${COLOR_GREEN}Pod deleted${COLOR_NC}"
        break
    fi

    sleep 1
done

# 3. Verify cleanup
echo "  Verifying cleanup..."
kubectl get pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" &> /dev/null
if [ $? -eq 0 ]; then
    echo -e "${COLOR_RED}✗ Pod still exists after ASBCP cleanup${COLOR_NC}"
    exit 1
else
    echo -e "  ${COLOR_GREEN}Cleanup verified${COLOR_NC}"
fi

echo ""
echo -e "${COLOR_GREEN}✓ Cleanup test passed${COLOR_NC}"

exit 0
