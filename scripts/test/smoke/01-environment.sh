#!/bin/bash
# scripts/test/smoke/01-environment.sh
# Smoke test scenario: Environment checks

set -e

# Source assertions
source "$(dirname "$0")/../lib/assertions.sh"

echo "Checking environment..."

# 1. Check required commands
echo -n "  Checking kubectl..."
assert_command_exists "kubectl" "kubectl is required"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

echo -n "  Checking curl..."
assert_command_exists "curl" "curl is required"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

echo -n "  Checking jq..."
assert_command_exists "jq" "jq is required"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 2. Check Kubernetes cluster
echo -n "  Checking cluster access..."
assert_cluster_accessible "Kubernetes cluster not accessible"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 3. Check required namespaces
echo -n "  Checking sandbox-system namespace..."
assert_namespace_exists "sandbox-system" "sandbox-system namespace does not exist"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

echo -n "  Checking sandbox namespace..."
assert_namespace_exists "sandbox" "sandbox namespace does not exist"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 4. Check required ConfigMaps
echo -n "  Checking sandbox-manager-config ConfigMap..."
assert_configmap_exists "sandbox-manager-config" "sandbox-system" "sandbox-manager-config not found"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 5. Check Manager service
echo -n "  Checking Manager service..."
assert_service_ready "manager" "sandbox-system" "Manager service not ready"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 6. Check Manager pod is running
echo -n "  Checking Manager pod..."
assert_pod_running "manager" "Manager pod not running"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 7. Check Manager endpoint
echo -n "  Checking Manager health endpoint..."
assert_endpoint_responds "http://localhost:8080/healthz" "200" "Manager health check failed"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

echo -n "  Checking Manager readiness endpoint..."
assert_endpoint_responds "http://localhost:8080/readyz" "200" "Manager readiness check failed"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

# 8. Check metrics endpoint
echo -n "  Checking metrics endpoint..."
assert_endpoint_responds "http://localhost:8080/metrics" "200" "Metrics endpoint not responding"
echo -e " ${COLOR_GREEN}OK${COLOR_NC}"

echo ""
echo -e "${COLOR_GREEN}✓ Environment check passed${COLOR_NC}"

exit 0
