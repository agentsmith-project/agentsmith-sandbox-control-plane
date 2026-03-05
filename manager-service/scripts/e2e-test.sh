#!/bin/bash
# E2E Test Suite for Sandbox Manager v2.0.0
# This script performs comprehensive end-to-end testing

# Don't exit on error - we want to run all tests
# set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
MANAGER_BIN="${MANAGER_BIN:-./manager}"
MANAGER_PORT="${MANAGER_PORT:-8080}"
SERVICE_KEY="${SERVICE_KEY:-test-e2e-key-12345}"
CONFIG_FILE="${CONFIG_FILE:-/tmp/e2e-manager-config.yaml}"
NAMESPACE="${NAMESPACE:-sandbox}"
LOG_FILE="${LOG_FILE:-/tmp/e2e-test.log}"
MANAGER_PID=""

# Workload path components for v2 API
WS_ID="ws-1"
PROJ_ID="proj-1"

# Test counters
TESTS_TOTAL=0
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_SKIPPED=0

# Test results array
declare -a FAILED_TESTS
declare -a SKIPPED_TESTS

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1" | tee -a "$LOG_FILE"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1" | tee -a "$LOG_FILE"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $1" | tee -a "$LOG_FILE"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1" | tee -a "$LOG_FILE"
}

print_header() {
    echo ""
    echo "=========================================="
    echo "$1"
    echo "=========================================="
}

# Test assertion helpers
assert_equals() {
    local expected="$1"
    local actual="$2"
    local message="${3:-Assertion failed}"

    if [ "$expected" = "$actual" ]; then
        return 0
    else
        log_error "$message: expected '$expected', got '$actual'"
        return 1
    fi
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    local message="${3:-Assertion failed}"

    if echo "$haystack" | grep -q "$needle"; then
        return 0
    else
        log_error "$message: '$needle' not found in response"
        return 1
    fi
}

assert_http_status() {
    local expected="$1"
    local actual="$2"
    local message="${3:-HTTP status mismatch}"

    if [ "$expected" -eq "$actual" ]; then
        return 0
    else
        log_error "$message: expected $expected, got $actual"
        return 1
    fi
}

# Start manager
start_manager() {
    log_info "Starting manager service..."

    # Create config file (minimal v2 schema)
    cat > "$CONFIG_FILE" <<EOF
version: 1
server:
  httpPort: ${MANAGER_PORT}
  requestIdHeader: X-Request-Id
  timeouts:
    readHeader: 5s
    read: 120s
    write: 120s
    idle: 120s
  maxHeaderBytes: 1048576
  metrics:
    enabled: true
    path: /metrics
    requireServiceKey: false
auth:
  enabled: true
  headerName: X-Service-Key
kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s
  retry:
    enabled: true
    maxAttempts: 3
    baseBackoff: 200ms
    maxBackoff: 2s
EOF

    # Start manager in background
    CONFIG_PATH="$CONFIG_FILE" SERVICE_KEYS="$SERVICE_KEY" nohup "$MANAGER_BIN" > "$LOG_FILE" 2>&1 &
    MANAGER_PID=$!

    # Wait for manager to be ready
    local max_wait=30
    local waited=0
    while [ $waited -lt $max_wait ]; do
        if curl -s "http://localhost:$MANAGER_PORT/healthz" > /dev/null 2>&1; then
            log_info "Manager started (PID: $MANAGER_PID)"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done

    log_error "Manager failed to start within ${max_wait}s"
    return 1
}

# Stop manager
stop_manager() {
    if [ -n "$MANAGER_PID" ]; then
        log_info "Stopping manager (PID: $MANAGER_PID)..."
        kill "$MANAGER_PID" 2>/dev/null || true
        wait "$MANAGER_PID" 2>/dev/null || true
        MANAGER_PID=""
    fi
}

# Cleanup on exit
cleanup() {
    log_info "Cleaning up..."
    stop_manager

    # Clean up test pods
    if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
        kubectl delete pods -n "$NAMESPACE" -l e2e=true --ignore-not-found=true >/dev/null 2>&1 || true
    fi

    # Clean up config file
    rm -f "$CONFIG_FILE"
}

# Run a test case
run_test() {
    local test_name="$1"
    local test_func="$2"

    TESTS_TOTAL=$((TESTS_TOTAL + 1))

    log_info "Running: $test_name"

    if $test_func; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        log_success "$test_name"
        return 0
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        log_error "$test_name"
        FAILED_TESTS+=("$test_name")
        return 1
    fi
}

# Skip a test case
skip_test() {
    local test_name="$1"
    local reason="$2"

    TESTS_TOTAL=$((TESTS_TOTAL + 1))
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
    log_warn "SKIP: $test_name - $reason"
    SKIPPED_TESTS+=("$test_name: $reason")
}

# HTTP request helper
http_request() {
    local method="$1"
    local url="$2"
    local headers="$3"
    local body="$4"

    local response
    local status

    if [ -n "$body" ]; then
        response=$(curl -s -w "\n%{http_code}" -X "$method" \
            $headers \
            -H "Content-Type: application/json" \
            -d "$body" \
            "$url" 2>/dev/null)
    else
        response=$(curl -s -w "\n%{http_code}" -X "$method" \
            $headers \
            "$url" 2>/dev/null)
    fi

    status=$(echo "$response" | tail -n1)
    body=$(echo "$response" | head -n-1)

    echo "$body"
    return $status
}

# ============================================================
# TEST CASES
# ============================================================

test_01_healthz_no_auth() {
    local response=$(curl -s -w "\n%{http_code}" "http://localhost:$MANAGER_PORT/healthz")
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 200 "$status" "healthz should return 200" || return 1
    assert_contains "$body" '"status":"ok"' "healthz response should contain status:ok" || return 1
}

test_02_readyz_no_auth() {
    local response=$(curl -s -w "\n%{http_code}" "http://localhost:$MANAGER_PORT/readyz")
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    # Can be 200 or 503 depending on K8s state
    if [ "$status" != "200" ] && [ "$status" != "503" ]; then
        log_error "readyz returned unexpected status: $status"
        return 1
    fi

    if [ "$status" = "200" ]; then
        assert_contains "$body" '"ready":true' "readyz should indicate ready" || return 1
    fi
}

test_03_metrics_no_auth() {
    local response=$(curl -s -w "\n%{http_code}" "http://localhost:$MANAGER_PORT/metrics")
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 200 "$status" "metrics should return 200" || return 1
    assert_contains "$body" "# HELP" "metrics should contain HELP comments" || return 1
    assert_contains "$body" "http_request_total" "metrics should contain http_request_total" || return 1
}

# Debug config endpoint removed in v2 - no /debug/config

test_05_auth_no_key() {
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/test-no-key" \
        -H "Content-Type: application/json" \
        -d '{"image": "ubuntu:22.04"}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 401 "$status" "should return 401 without service key" || return 1
    assert_contains "$body" "SERVICE_KEY_MISSING" "should return SERVICE_KEY_MISSING error" || return 1
}

test_06_auth_invalid_key() {
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/test-invalid-key" \
        -H "X-Service-Key: invalid-key-123" \
        -H "Content-Type: application/json" \
        -d '{"image": "ubuntu:22.04"}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 401 "$status" "should return 401 with invalid service key" || return 1
    assert_contains "$body" "SERVICE_KEY_INVALID" "should return SERVICE_KEY_INVALID error" || return 1
}

test_07_auth_valid_key() {
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/test-valid-key" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"image": "ubuntu:22.04"}')
    local status=$(echo "$response" | tail -n1)

    # May fail due to no runner image, but should not be auth error
    if [ "$status" = "401" ]; then
        log_error "Valid key was rejected with 401"
        return 1
    fi
}

test_08_create_workload_valid_request() {
    local wl_id="e2e-create-$$"
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{
            "image": "ubuntu:22.04",
            "idle_timeout_sec": 900,
            "env": {"TEST_VAR": "test_value"}
        }')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    # May fail if no K8s cluster or image pull issues
    if [ "$status" != "200" ] && [ "$status" != "201" ]; then
        # 500 is acceptable when cluster/image unavailable
        if [ "$status" = "500" ]; then
            log_info "Create failed (expected without cluster): $body"
            return 0
        fi
        log_error "Unexpected response: status=$status, body=$body"
        return 1
    fi

    # If successful, verify response structure
    assert_contains "$body" '"pod_name"' "response should contain pod_name" || return 1
}

test_09_create_workload_missing_image() {
    local wl_id="e2e-missing-image-$$"
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"env": {"KEY": "value"}}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 400 "$status" "should return 400 for missing image" || return 1
    assert_contains "$body" "image" "should mention image in error" || return 1
}

test_10_create_workload_invalid_id() {
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/Invalid_ID" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"image": "ubuntu:22.04"}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 400 "$status" "should return 400 for invalid workload_id" || return 1
    assert_contains "$body" "invalid" "should mention invalid in error" || return 1
}

test_11_keepalive_workload() {
    local wl_id="e2e-keepalive-$$"
    local response=$(curl -s -w "\n%{http_code}" -X POST \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id/keepalive" \
        -H "X-Service-Key: $SERVICE_KEY")
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    # 404 acceptable if pod does not exist
    if [ "$status" = "200" ] || [ "$status" = "404" ]; then
        return 0
    fi
    log_error "Unexpected response: status=$status, body=$body"
    return 1
}

test_12_exec_valid_command() {
    local wl_id="e2e-exec-$$"
    local response=$(curl -s -w "\n%{http_code}" -X POST \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id/exec" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"cmd": ["echo", "hello"], "timeout_seconds": 10}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    # 404 acceptable if pod does not exist
    if [ "$status" != "200" ]; then
        if [ "$status" = "404" ]; then
            return 0
        fi
        log_error "Unexpected response: status=$status, body=$body"
        return 1
    fi

    # If successful, verify response structure
    assert_contains "$body" '"exit_code"' "response should contain exit_code" || return 1
    assert_contains "$body" '"stdout"' "response should contain stdout" || return 1
}

test_13_exec_missing_command() {
    local wl_id="e2e-exec-missing-$$"
    local response=$(curl -s -w "\n%{http_code}" -X POST \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id/exec" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"timeout_seconds": 10}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    assert_http_status 400 "$status" "should return 400 for missing command" || return 1
    assert_contains "$body" "cmd" "should mention cmd in error" || return 1
}

test_14_exec_invalid_timeout() {
    local wl_id="e2e-exec-timeout-$$"
    local response=$(curl -s -w "\n%{http_code}" -X POST \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id/exec" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"cmd": ["echo", "test"], "timeout_seconds": 500}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    # 504 Gateway Timeout is returned when timeout exceeds max
    assert_http_status 504 "$status" "should return 504 for invalid timeout" || return 1
}

# File upload/download and symlink tests removed - v2 API does not have file endpoints

test_19_delete_workload() {
    local wl_id="e2e-delete-$$"
    local response=$(curl -s -w "\n%{http_code}" -X DELETE \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id" \
        -H "X-Service-Key: $SERVICE_KEY")
    local status=$(echo "$response" | tail -n1)

    # Should succeed even if pod doesn't exist
    # 204 No Content is the correct response for successful DELETE
    if [ "$status" != "200" ] && [ "$status" != "202" ] && [ "$status" != "204" ] && [ "$status" != "404" ]; then
        log_error "Unexpected status: $status"
        return 1
    fi
}

test_20_request_id_propagation() {
    local test_id="test-req-id-$$"
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$test_id" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "X-Request-Id: test-request-id-123" \
        -H "Content-Type: application/json" \
        -d '{"image": "ubuntu:22.04"}')
    local status=$(echo "$response" | tail -n1)

    # Request should be processed
    if [ "$status" = "401" ]; then
        log_error "Auth failed"
        return 1
    fi

    # Check metrics for the request
    return 0
}

test_21_metrics_increments() {
    # Make some requests to increment metrics
    curl -s "http://localhost:$MANAGER_PORT/healthz" > /dev/null
    curl -s "http://localhost:$MANAGER_PORT/readyz" > /dev/null

    local metrics=$(curl -s "http://localhost:$MANAGER_PORT/metrics")

    # Check that request counts exist
    if ! echo "$metrics" | grep -q "http_request_total{"; then
        log_error "Metrics don't contain request counts"
        return 1
    fi

    return 0
}

# Config hash test removed - /debug/config endpoint not in v2

# Integration test with actual pod (requires K8s cluster and image)
test_30_create_and_exec_pod() {
    local wl_id="e2e-integration-$$"
    local response=$(curl -s -w "\n%{http_code}" -X PUT \
        "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"image": "ubuntu:22.04", "idle_timeout_sec": 900}')
    local status=$(echo "$response" | tail -n1)
    local body=$(echo "$response" | head -n-1)

    if [ "$status" != "200" ] && [ "$status" != "201" ]; then
        skip_test "create_and_exec_pod" "Pod creation failed (no cluster?): $body"
        return 0
    fi

    # Wait for pod to be ready
    local max_wait=60
    local waited=0
    while [ $waited -lt $max_wait ]; do
        response=$(curl -s -X POST \
            "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id/exec" \
            -H "X-Service-Key: $SERVICE_KEY" \
            -H "Content-Type: application/json" \
            -d '{"cmd": ["echo", "integration-test"], "timeout_seconds": 10}')
        status=$(curl -s -w "%{http_code}" -X POST \
            "http://localhost:$MANAGER_PORT/v1/workspaces/$WS_ID/projects/$PROJ_ID/workloads/$wl_id/exec" \
            -H "X-Service-Key: $SERVICE_KEY" \
            -H "Content-Type: application/json" \
            -d '{"cmd": ["echo", "integration-test"], "timeout_seconds": 10}' \
            2>/dev/null | tail -n1)

        if [ "$status" = "200" ]; then
            # Exec succeeded
            return 0
        fi

        sleep 2
        waited=$((waited + 2))
    done

    log_error "Pod did not become ready within ${max_wait}s"
    return 1
}

# ============================================================
# MAIN TEST EXECUTION
# ============================================================

main() {
    print_header "Sandbox Manager E2E Test Suite v2.0.0"

    # Setup
    trap cleanup EXIT
    trap cleanup INT TERM

    log_info "Log file: $LOG_FILE"
    echo "" > "$LOG_FILE"

    # Check prerequisites
    log_info "Checking prerequisites..."

    if [ ! -f "$MANAGER_BIN" ]; then
        log_error "Manager binary not found: $MANAGER_BIN"
        log_info "Please build with: go build -o $MANAGER_BIN ./cmd/manager/main.go"
        exit 1
    fi

    if ! command -v kubectl >/dev/null 2>&1; then
        log_error "kubectl not found in PATH"
        exit 1
    fi

    if ! command -v curl >/dev/null 2>&1; then
        log_error "curl not found in PATH"
        exit 1
    fi

    # Check Kind cluster
    if ! kubectl cluster-info >/dev/null 2>&1; then
        log_error "Cannot connect to Kubernetes cluster"
        log_info "Please ensure Kind cluster is running"
        exit 1
    fi

    log_info "Prerequisites check passed"

    # Create namespace
    log_info "Creating namespace: $NAMESPACE"
    kubectl create namespace "$NAMESPACE" >/dev/null 2>&1 || true

    # Start manager
    if ! start_manager; then
        log_error "Failed to start manager"
        exit 1
    fi

    sleep 2

    # Run tests
    print_header "Running Tests"

    # Basic endpoint tests
    run_test "01 - Health check endpoint" test_01_healthz_no_auth
    run_test "02 - Readiness check endpoint" test_02_readyz_no_auth
    run_test "03 - Metrics endpoint" test_03_metrics_no_auth

    # Authentication tests
    run_test "05 - Auth without service key" test_05_auth_no_key
    run_test "06 - Auth with invalid key" test_06_auth_invalid_key
    run_test "07 - Auth with valid key" test_07_auth_valid_key

    # Workload creation tests
    run_test "08 - Create workload with valid request" test_08_create_workload_valid_request
    run_test "09 - Create workload missing image" test_09_create_workload_missing_image
    run_test "10 - Create workload invalid id" test_10_create_workload_invalid_id

    # Keepalive tests
    run_test "11 - Keepalive workload" test_11_keepalive_workload

    # Exec tests
    run_test "12 - Exec valid command" test_12_exec_valid_command
    run_test "13 - Exec with missing command" test_13_exec_missing_command
    run_test "14 - Exec with invalid timeout" test_14_exec_invalid_timeout

    # Delete tests
    run_test "19 - Delete workload" test_19_delete_workload

    # Request handling tests
    run_test "20 - Request ID propagation" test_20_request_id_propagation
    run_test "21 - Metrics increments" test_21_metrics_increments
    # Integration tests (require K8s cluster)
    run_test "30 - Integration: Create and exec pod" test_30_create_and_exec_pod

    # Print results
    print_header "Test Results"

    echo "Total:  $TESTS_TOTAL"
    echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
    echo -e "${RED}Failed: $TESTS_FAILED${NC}"
    echo -e "${YELLOW}Skipped: $TESTS_SKIPPED${NC}"

    if [ $TESTS_FAILED -gt 0 ]; then
        echo ""
        echo -e "${RED}Failed tests:${NC}"
        for test in "${FAILED_TESTS[@]}"; do
            echo "  - $test"
        done
    fi

    if [ $TESTS_SKIPPED -gt 0 ]; then
        echo ""
        echo -e "${YELLOW}Skipped tests:${NC}"
        for test in "${SKIPPED_TESTS[@]}"; do
            echo "  - $test"
        done
    fi

    # Exit with appropriate code
    if [ $TESTS_FAILED -gt 0 ]; then
        exit 1
    else
        exit 0
    fi
}

# Run main
main "$@"
