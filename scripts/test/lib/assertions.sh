#!/bin/bash
# scripts/test/lib/assertions.sh
# Assertion functions for smoke tests

# Source the runner library
source "$(dirname "$0")/runner.sh"

# Environment assertions
assert_cluster_accessible() {
    local msg=${1:-"Kubernetes cluster not accessible"}

    kubectl cluster-info &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        return 1
    fi
    return 0
}

assert_pod_running() {
    local pod_name_pattern=$1
    local msg=${2:-"Pod ${pod_name_pattern} not running"}

    local count=$(kubectl get pods --all-namespaces -l app="${pod_name_pattern}" -o json | jq '.items | length')

    if [ "${count}" -eq 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  No pods found with label app=${pod_name_pattern}${COLOR_NC}"
        return 1
    fi

    local ready=$(kubectl get pods --all-namespaces -l app="${pod_name_pattern}" -o json | jq '[.items[] | select(.status.phase=="Running")] | length')

    if [ "${ready}" -eq 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  ${count} pod(s) found, but none are running${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_service_ready() {
    local service_name=$1
    local namespace=${2:-"sandbox-system"}
    local msg=${3:-"Service ${service_name} not ready"}

    kubectl get service "${service_name}" -n "${namespace}" &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Service ${service_name} not found in namespace ${namespace}${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_endpoint_responds() {
    local url=$1
    local expected_status=${2:-200}
    local msg=${3:-"Endpoint ${url} not responding"}

    local actual_status=$(curl -s -o /dev/null -w "%{http_code}" "${url}")

    if [ "${actual_status}" != "${expected_status}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Expected status ${expected_status}, got ${actual_status}${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_json_field_equals() {
    local json=$1
    local field=$2
    local expected=$3
    local msg=${4:-"JSON field ${field} mismatch"}

    local actual=$(echo "${json}" | jq -r "${field}")

    if [ "${actual}" != "${expected}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Expected ${field}=${expected}, got ${actual}${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_json_field_exists() {
    local json=$1
    local field=$2
    local msg=${3:-"JSON field ${field} does not exist"}

    local actual=$(echo "${json}" | jq -r "${field} // empty")

    if [ -z "${actual}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_namespace_exists() {
    local namespace=$1
    local msg=${2:-"Namespace ${namespace} does not exist"}

    kubectl get namespace "${namespace}" &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_configmap_exists() {
    local configmap=$1
    local namespace=${2:-"sandbox-system"}
    local msg=${3:-"ConfigMap ${configmap} does not exist"}

    kubectl get configmap "${configmap}" -n "${namespace}" &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        return 1
    fi

    return 0
}

assert_secret_exists() {
    local secret=$1
    local namespace=${2:-"sandbox-system"}
    local msg=${3:-"Secret ${secret} does not exist"}

    kubectl get secret "${secret}" -n "${namespace}" &> /dev/null
    if [ $? -ne 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        return 1
    fi

    return 0
}

# Export functions
export -f assert_cluster_accessible
export -f assert_pod_running
export -f assert_service_ready
export -f assert_endpoint_responds
export -f assert_json_field_equals
export -f assert_json_field_exists
export -f assert_namespace_exists
export -f assert_configmap_exists
export -f assert_secret_exists
