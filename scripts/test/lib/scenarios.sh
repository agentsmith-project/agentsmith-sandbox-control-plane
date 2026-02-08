#!/bin/bash
# scripts/test/lib/scenarios.sh
# Test scenario definitions and helper functions

# Default configuration
MANAGER_URL="${MANAGER_URL:-http://localhost:8080}"
SERVICE_KEY="${SERVICE_KEY:-test-key}"
SANDBOX_ID="${SANDBOX_ID:-test-smoke-$(date +%s)}"
SANDBOX_NAMESPACE="${SANDBOX_NAMESPACE:-sandbox}"

# Test scenario helpers
create_sandbox() {
    local url=${1:-${MANAGER_URL}}
    local key=${2:-${SERVICE_KEY}}
    local sandbox_id=${3:-${SANDBOX_ID}}

    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "${url}/v1/sandboxes/${sandbox_id}/sandbox" \
        -H "X-Service-Key: ${key}" \
        -H "Content-Type: application/json" \
        -d '{
            "image": "sandbox-runner:1.0.0",
            "command": ["sh", "-lc", "echo hello && sleep 300"]
        }')

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
    return $( [ "${status}" -eq 201 ] )
}

delete_sandbox() {
    local url=${1:-${MANAGER_URL}}
    local key=${2:-${SERVICE_KEY}}
    local sandbox_id=${3:-${SANDBOX_ID}}

    local response=$(curl -s -w "\n%{http_code}" -X DELETE \
        "${url}/v1/sandboxes/${sandbox_id}/sandbox" \
        -H "X-Service-Key: ${key}")

    local status=$(echo "${response}" | tail -n1)
    return $( [ "${status}" -eq 200 ] || [ "${status}" -eq 204 ] || [ "${status}" -eq 404 ] )
}

touch_session() {
    local url=${1:-${MANAGER_URL}}
    local key=${2:-${SERVICE_KEY}}
    local sandbox_id=${3:-${SANDBOX_ID}}

    local response=$(curl -s -w "\n%{http_code}" -X POST \
        "${url}/v1/sandboxes/${sandbox_id}/touch" \
        -H "X-Service-Key: ${key}" \
        -H "Content-Type: application/json")

    local status=$(echo "${response}" | tail -n1)
    return $( [ "${status}" -eq 200 ] )
}

get_session() {
    local url=${1:-${MANAGER_URL}}
    local key=${2:-${SERVICE_KEY}}
    local sandbox_id=${3:-${SANDBOX_ID}}

    local response=$(curl -s -w "\n%{http_code}" \
        "${url}/v1/sandboxes/${sandbox_id}" \
        -H "X-Service-Key: ${key}")

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
    return $( [ "${status}" -eq 200 ] )
}

check_pod_ready() {
    local sandbox_id=${1:-${SANDBOX_ID}}
    local namespace=${2:-${SANDBOX_NAMESPACE}}

    local pod_name="sandbox-${sandbox_id}"

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

    delete_sandbox "${MANAGER_URL}" "${SERVICE_KEY}" "${sandbox_id}" || true

    local pod_name="sandbox-${sandbox_id}"
    kubectl delete pod "${pod_name}" -n "${SANDBOX_NAMESPACE}" --ignore-not-found=true &> /dev/null || true

    echo "Cleanup complete"
}

# Export functions
export -f create_sandbox
export -f delete_sandbox
export -f touch_session
export -f get_session
export -f check_pod_ready
export -f wait_for_pod_ready
export -f cleanup_sandbox
