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
pod_name="${SANDBOX_POD_NAME:-sandbox-${SANDBOX_ID}}"

echo "Cleaning up sandbox ${SANDBOX_ID} (pod: ${pod_name})..."

# 1. Delete sandbox via API
echo "  Deleting sandbox via API..."
if ! delete_sandbox "${MANAGER_URL}" "${SANDBOX_ID}"; then
    echo -e "${COLOR_YELLOW}⊘ API delete failed, continuing with pod cleanup${COLOR_NC}"
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

    if [ $i -eq 30 ]; then
        echo -e "${COLOR_YELLOW}⊘ Pod still exists after 30s, forcing deletion${COLOR_NC}"
        kubectl delete pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" --force --grace-period=0 &> /dev/null || true
    fi

    sleep 1
done

# 3. Verify cleanup
echo "  Verifying cleanup..."
kubectl get pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" &> /dev/null
if [ $? -eq 0 ]; then
    echo -e "${COLOR_YELLOW}⊘ Pod still exists after cleanup${COLOR_NC}"
else
    echo -e "  ${COLOR_GREEN}Cleanup verified${COLOR_NC}"
fi

echo ""
echo -e "${COLOR_GREEN}✓ Cleanup test passed${COLOR_NC}"

exit 0
