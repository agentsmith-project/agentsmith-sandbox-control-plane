#!/bin/bash
# scripts/test/lib/scenarios.sh
# Test scenario definitions and helper functions

# Default configuration
MANAGER_URL="${MANAGER_URL:-http://localhost:8080}"
SANDBOX_ID="${SANDBOX_ID:-test-smoke-$(date +%s)}"
SANDBOX_NAMESPACE="${SANDBOX_NAMESPACE:-sandbox}"
# Pod name from create response (Manager uses sbx-<hash>; load from file for cross-script use)
if [ -z "${SANDBOX_POD_NAME:-}" ] && [ -f /tmp/smoke-test-pod-name.txt ]; then
    SANDBOX_POD_NAME=$(cat /tmp/smoke-test-pod-name.txt)
    export SANDBOX_POD_NAME
fi
# Timeout for HTTP calls (avoid hang when Manager not reachable)
MANAGER_TIMEOUT="${MANAGER_TIMEOUT:-10}"

# Test scenario helpers
create_sandbox() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X PUT \
        "${url}/v1/sandboxes/${sandbox_id}" \
        -H "Content-Type: application/json" \
        -d '{"ttlSeconds": 300}')

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
    return $( [ "${status}" -eq 200 ] )
}

delete_sandbox() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X DELETE \
        "${url}/v1/sandboxes/${sandbox_id}")

    local status=$(echo "${response}" | tail -n1)
    return $( [ "${status}" -eq 204 ] || [ "${status}" -eq 404 ] )
}

touch_session() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}

    local response=$(curl -s --max-time "${MANAGER_TIMEOUT}" -w "\n%{http_code}" -X POST \
        "${url}/v1/sandboxes/${sandbox_id}/touch" \
        -H "Content-Type: application/json")

    local status=$(echo "${response}" | tail -n1)
    return $( [ "${status}" -eq 200 ] )
}

exec_command() {
    local url=${1:-${MANAGER_URL}}
    local sandbox_id=${2:-${SANDBOX_ID}}
    local cmd=${3:-'["echo", "hello"]'}

    curl -s -N -X POST \
        "${url}/v1/sandboxes/${sandbox_id}/exec" \
        -H "Content-Type: application/json" \
        -d "{\"cmd\": ${cmd}}" \
        --max-time 30
}

check_pod_ready() {
    local sandbox_id=${1:-${SANDBOX_ID}}
    local namespace=${2:-${SANDBOX_NAMESPACE}}

    local pod_name="${SANDBOX_POD_NAME:-sandbox-${sandbox_id}}"

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
export -f delete_sandbox
export -f touch_session
export -f exec_command
export -f check_pod_ready
export -f wait_for_pod_ready
export -f cleanup_sandbox
