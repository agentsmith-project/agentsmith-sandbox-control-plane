#!/bin/bash
# scripts/test/lib/scenarios.sh
# Test scenario definitions and helper functions

# Default configuration
MANAGER_URL="${MANAGER_URL:-http://localhost:8080}"
SERVICE_KEY="${SERVICE_KEY:-test-key-123}"
SANDBOX_ID="${SANDBOX_ID:-test-smoke-$(date +%s)}"
SANDBOX_NAMESPACE="${SANDBOX_NAMESPACE:-sandbox-workloads}"
WORKSPACE_ID="${WORKSPACE_ID:-ws-1}"
PROJECT_ID="${PROJECT_ID:-proj-1}"
WORKSPACE_BINDING_ID="${WORKSPACE_BINDING_ID:-wmb_demo}"
AFSCP_NAMESPACE_ID="${AFSCP_NAMESPACE_ID:-ns_demo}"
# Pod name from create response; load from file for cross-script use.
if [ -z "${SANDBOX_POD_NAME:-}" ] && [ -f /tmp/smoke-test-pod-name.txt ]; then
    SANDBOX_POD_NAME=$(cat /tmp/smoke-test-pod-name.txt)
    export SANDBOX_POD_NAME
fi
# Timeout for HTTP calls (avoid hang when Manager not reachable)
MANAGER_TIMEOUT="${MANAGER_TIMEOUT:-10}"

# Test scenario helpers
ensure_workspace_binding() {
    local url=${1:-${MANAGER_URL}}
    local binding_id=${2:-${WORKSPACE_BINDING_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X PUT \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workspace-bindings/${binding_id}" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"namespace_id\":\"${AFSCP_NAMESPACE_ID}\",\"mount_binding_id\":\"${binding_id}\"}")

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
    [ "${status}" -eq 200 ]
}

create_sandbox() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X PUT \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${sandbox_id}" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"image\":\"${WORKLOAD_IMAGE:-ubuntu:22.04}\",\"workspace_binding_id\":\"${WORKSPACE_BINDING_ID}\",\"idle_timeout_sec\":300,\"max_lifetime_sec\":3600}")

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
    [ "${status}" -eq 200 ] || [ "${status}" -eq 201 ]
}

delete_sandbox() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X DELETE \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${sandbox_id}" \
        -H "X-Service-Key: ${SERVICE_KEY}")

    local status=$(echo "${response}" | tail -n1)
    [ "${status}" -eq 200 ] || [ "${status}" -eq 404 ]
}

touch_session() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X POST \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${sandbox_id}/keepalive" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json")

    local status=$(echo "${response}" | tail -n1)
    [ "${status}" -eq 200 ]
}

exec_command() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}
    local cmd=${3:-'["echo", "hello"]'}

    curl -s -N -X POST \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${sandbox_id}/exec" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"cmd\": ${cmd}}" \
        --max-time 30
}

check_pod_ready() {
    local sandbox_id=${1:-${SANDBOX_ID}}
    local namespace=${2:-${SANDBOX_NAMESPACE}}

    local pod_name="${SANDBOX_POD_NAME:-workload-${sandbox_id}}"

    kubectl get pod "${pod_name}" -n "${namespace}" &> /dev/null
    if [ $? -ne 0 ]; then
        return 1
    fi

    local phase=$(kubectl get pod "${pod_name}" -n "${namespace}" -o jsonpath='{.status.phase}')
    [ "${phase}" == "Running" ]
}

wait_for_pod_ready() {
    local sandbox_id=${1:-${SANDBOX_ID}}
    local namespace=${2:-${SANDBOX_NAMESPACE}}
    local timeout=${3:-120}
    local interval=${4:-2}

    local elapsed=0
    while [ ${elapsed} -lt ${timeout} ]; do
        if check_pod_ready "${sandbox_id}" "${namespace}"; then
            return 0
        fi
        sleep ${interval}
        elapsed=$((elapsed + interval))
    done

    return 1
}

cleanup_sandbox() {
    local sandbox_id=${1:-${SANDBOX_ID}}

    echo "Cleaning up sandbox ${sandbox_id}..."

    delete_sandbox "${MANAGER_URL}" "${sandbox_id}" || true

    echo "Cleanup complete"
}

# Export functions
export -f create_sandbox
export -f ensure_workspace_binding
export -f delete_sandbox
export -f touch_session
export -f exec_command
export -f check_pod_ready
export -f wait_for_pod_ready
export -f cleanup_sandbox
