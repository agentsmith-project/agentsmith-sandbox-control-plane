#!/bin/bash
# scripts/test/lib/scenarios.sh
# Test scenario definitions and helper functions

# Default configuration
ASBCP_URL="${ASBCP_URL:-${ASBCP_HTTP_URL:-http://localhost:8080}}"
SERVICE_KEY="${SERVICE_KEY:-test-key-123}"
WORKLOAD_ID="${WORKLOAD_ID:-test-smoke-$(date +%s)}"
WORKLOAD_NAMESPACE="${WORKLOAD_NAMESPACE:-sandbox-workloads}"
WORKSPACE_ID="${WORKSPACE_ID:-ws-1}"
PROJECT_ID="${PROJECT_ID:-proj-1}"
WORKSPACE_BINDING_ID="${WORKSPACE_BINDING_ID:-wmb_demo}"
AFSCP_NAMESPACE_ID="${AFSCP_NAMESPACE_ID:-ns_demo}"
# Pod name from create response; load from file for cross-script use.
if [ -z "${WORKLOAD_POD_NAME:-}" ] && [ -f /tmp/asbcp-smoke-pod-name.txt ]; then
    WORKLOAD_POD_NAME=$(cat /tmp/asbcp-smoke-pod-name.txt)
    export WORKLOAD_POD_NAME
fi
# Timeout for HTTP calls (avoid hang when ASBCP is not reachable)
ASBCP_TIMEOUT="${ASBCP_TIMEOUT:-10}"

# Test scenario helpers
ensure_workspace_binding() {
    local url=${1:-${ASBCP_URL}}
    local binding_id=${2:-${WORKSPACE_BINDING_ID}}

    local response=$(curl -s --max-time "${ASBCP_TIMEOUT}" -w "\n%{http_code}" -X PUT \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workspace-bindings/${binding_id}" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"namespace_id\":\"${AFSCP_NAMESPACE_ID}\",\"mount_binding_id\":\"${binding_id}\"}")

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
	[ "${status}" -eq 200 ]
}

create_workload() {
    local url=${1:-${ASBCP_URL}}
    local workload_id=${2:-${WORKLOAD_ID}}

    local response=$(curl -s --max-time "${ASBCP_TIMEOUT}" -w "\n%{http_code}" -X PUT \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${workload_id}" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"image\":\"${WORKLOAD_IMAGE:-ubuntu:22.04}\",\"workspace_binding_id\":\"${WORKSPACE_BINDING_ID}\",\"idle_timeout_sec\":300,\"max_lifetime_sec\":3600}")

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)

    echo "${body}"
	[ "${status}" -eq 200 ] || [ "${status}" -eq 201 ]
}

delete_workload() {
    local url=${1:-${ASBCP_URL}}
    local workload_id=${2:-${WORKLOAD_ID}}

    local response=$(curl -s --max-time "${ASBCP_TIMEOUT}" -w "\n%{http_code}" -X DELETE \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${workload_id}" \
        -H "X-Service-Key: ${SERVICE_KEY}")

    local status=$(echo "${response}" | tail -n1)
    local body=$(echo "${response}" | head -n-1)
    echo "${body}"

    if [ "${status}" -eq 200 ]; then
        return 0
    fi
    if [ "${status}" -eq 409 ]; then
        echo "DELETE retryable conflict: terminal fact is not proven yet; retry after reconcile. Response: ${body}" >&2
        return 1
    fi

    echo "DELETE failed with status ${status}: ${body}" >&2
    return 1
}

keepalive_workload() {
    local url=${1:-${ASBCP_URL}}
    local workload_id=${2:-${WORKLOAD_ID}}

    local response=$(curl -s --max-time "${ASBCP_TIMEOUT}" -w "\n%{http_code}" -X POST \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${workload_id}/keepalive" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json")

    local status=$(echo "${response}" | tail -n1)
    [ "${status}" -eq 200 ]
}

exec_workload() {
    local url=${1:-${ASBCP_URL}}
    local workload_id=${2:-${WORKLOAD_ID}}
    local cmd=${3:-'["echo", "hello"]'}

    curl -s -N -X POST \
        "${url}/v1/workspaces/${WORKSPACE_ID}/projects/${PROJECT_ID}/workloads/${workload_id}/exec" \
        -H "X-Service-Key: ${SERVICE_KEY}" \
        -H "Content-Type: application/json" \
        -d "{\"cmd\": ${cmd}}" \
        --max-time 30
}

check_pod_ready() {
    local workload_id=${1:-${WORKLOAD_ID}}
    local namespace=${2:-${WORKLOAD_NAMESPACE}}

    local pod_name="${WORKLOAD_POD_NAME:-workload-${workload_id}}"

    kubectl get pod "${pod_name}" -n "${namespace}" &> /dev/null
    if [ $? -ne 0 ]; then
        return 1
    fi

    local phase=$(kubectl get pod "${pod_name}" -n "${namespace}" -o jsonpath='{.status.phase}')
    [ "${phase}" == "Running" ]
}

wait_for_pod_ready() {
    local workload_id=${1:-${WORKLOAD_ID}}
    local namespace=${2:-${WORKLOAD_NAMESPACE}}
    local timeout=${3:-120}
    local interval=${4:-2}

    local elapsed=0
    while [ ${elapsed} -lt ${timeout} ]; do
        if check_pod_ready "${workload_id}" "${namespace}"; then
            return 0
        fi
        sleep ${interval}
        elapsed=$((elapsed + interval))
    done

    return 1
}

cleanup_workload() {
    local workload_id=${1:-${WORKLOAD_ID}}

    echo "Cleaning up workload ${workload_id}..."

    # Test cleanup is best-effort only. It may tolerate an already reconciled
    # resource, but delete_workload itself preserves the product DELETE contract.
    delete_workload "${ASBCP_URL}" "${workload_id}" || true

    echo "Cleanup complete"
}

# Export functions
export -f create_workload
export -f ensure_workspace_binding
export -f delete_workload
export -f keepalive_workload
export -f exec_workload
export -f check_pod_ready
export -f wait_for_pod_ready
export -f cleanup_workload
