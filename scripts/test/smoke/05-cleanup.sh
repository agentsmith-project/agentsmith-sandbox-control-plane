#!/bin/bash
# scripts/test/smoke/05-cleanup.sh
# Smoke test scenario: Cleanup resources

set -e

# Source scenarios
source "$(dirname "$0")/../lib/scenarios.sh"
source "$(dirname "$0")/../lib/assertions.sh"

# Read WORKLOAD_ID and pod name from previous test
if [ -f /tmp/asbcp-smoke-workload-id.txt ]; then
    WORKLOAD_ID=$(cat /tmp/asbcp-smoke-workload-id.txt)
    rm /tmp/asbcp-smoke-workload-id.txt
else
    echo -e "${COLOR_YELLOW}⊘ SKIPPED: No workload ID found, nothing to clean${COLOR_NC}"
    exit 0
fi
if [ -f /tmp/asbcp-smoke-pod-name.txt ]; then
    WORKLOAD_POD_NAME=$(cat /tmp/asbcp-smoke-pod-name.txt)
    export WORKLOAD_POD_NAME
    rm /tmp/asbcp-smoke-pod-name.txt
fi
pod_name="${WORKLOAD_POD_NAME:-workload-${WORKLOAD_ID}}"

echo "Cleaning up workload ${WORKLOAD_ID} (pod: ${pod_name})..."

# 1. Delete workload via API
echo "  Deleting workload via API..."
if ! delete_workload "${ASBCP_URL}" "${WORKLOAD_ID}"; then
    echo -e "${COLOR_RED}✗ API delete failed${COLOR_NC}"
    exit 1
fi

echo -e "  ${COLOR_GREEN}Workload deleted${COLOR_NC}"

# 2. Wait for pod to be deleted
echo "  Waiting for pod to be deleted..."

for i in {1..30}; do
    kubectl get pod "${pod_name}" -n "${WORKLOAD_NAMESPACE}" &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "  ${COLOR_GREEN}Pod deleted${COLOR_NC}"
        break
    fi

    sleep 1
done

# 3. Verify cleanup
echo "  Verifying cleanup..."
kubectl get pod "${pod_name}" -n "${WORKLOAD_NAMESPACE}" &> /dev/null
if [ $? -eq 0 ]; then
    echo -e "${COLOR_RED}✗ Pod still exists after ASBCP cleanup${COLOR_NC}"
    exit 1
else
    echo -e "  ${COLOR_GREEN}Cleanup verified${COLOR_NC}"
fi

echo ""
echo -e "${COLOR_GREEN}✓ Cleanup test passed${COLOR_NC}"

exit 0
