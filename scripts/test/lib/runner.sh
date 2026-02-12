#!/bin/bash
# scripts/test/lib/runner.sh
# Test framework for smoke tests

# Idempotent when sourced multiple times (e.g. from test.sh and assertions.sh)
[[ -n "${_RUNNER_SH_LOADED:-}" ]] && return 0
_RUNNER_SH_LOADED=1

set -e

# Color definitions
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_RED='\033[0;31m'
readonly COLOR_YELLOW='\033[0;33m'
readonly COLOR_BLUE='\033[0;34m'
readonly COLOR_NC='\033[0m'

# Test counters
TEST_PASSED=0
TEST_FAILED=0
TEST_SKIPPED=0

# Test results tracking
declare -a PASSED_TESTS=()
declare -a FAILED_TESTS=()

# run_scenario executes a test scenario
# Usage: run_scenario <script_path> <description>
run_scenario() {
    local scenario=$1
    local description=$2

    echo -e "${COLOR_BLUE}================================================================${COLOR_NC}"
    echo -e "${COLOR_BLUE}Running: ${description}${COLOR_NC}"
    echo -e "${COLOR_BLUE}Script: ${scenario}${COLOR_NC}"
    echo -e "${COLOR_BLUE}================================================================${COLOR_NC}"

    local start_time=$(date +%s)

    if bash "${scenario}"; then
        local end_time=$(date +%s)
        local duration=$((end_time - start_time))
        echo -e "${COLOR_GREEN}✓ PASSED: ${description} (${duration}s)${COLOR_NC}"
        ((TEST_PASSED++))
        PASSED_TESTS+=("${description}")
        return 0
    else
        local end_time=$(date +%s)
        local duration=$((end_time - start_time))
        echo -e "${COLOR_RED}✗ FAILED: ${description} (${duration}s)${COLOR_NC}"
        ((TEST_FAILED++))
        FAILED_TESTS+=("${description}")
        return 1
    fi
}

# print_report prints the final test report
print_report() {
    echo ""
    echo -e "${COLOR_BLUE}================================"
    echo "Smoke Test Report"
    echo "================================${COLOR_NC}"
    echo -e "Passed:  ${COLOR_GREEN}${TEST_PASSED}${COLOR_NC}"
    echo -e "Failed:  ${COLOR_RED}${TEST_FAILED}${COLOR_NC}"
    echo -e "Skipped: ${COLOR_YELLOW}${TEST_SKIPPED}${COLOR_NC}"

    local total=$((TEST_PASSED + TEST_FAILED + TEST_SKIPPED))
    echo "Total:   ${total}"
    echo -e "${COLOR_BLUE}================================${COLOR_NC}"

    # Print failed tests
    if [ ${TEST_FAILED} -gt 0 ]; then
        echo ""
        echo -e "${COLOR_RED}Failed Tests:${COLOR_NC}"
        for test in "${FAILED_TESTS[@]}"; do
            echo -e "  ${COLOR_RED}✗${COLOR_NC} ${test}"
        done
    fi

    # Print passed tests
    if [ ${TEST_PASSED} -gt 0 ]; then
        echo ""
        echo -e "${COLOR_GREEN}Passed Tests:${COLOR_NC}"
        for test in "${PASSED_TESTS[@]}"; do
            echo -e "  ${COLOR_GREEN}✓${COLOR_NC} ${test}"
        done
    fi

    echo ""

    if [ ${TEST_FAILED} -eq 0 ]; then
        echo -e "${COLOR_GREEN}✓ All tests passed!${COLOR_NC}"
        return 0
    else
        echo -e "${COLOR_RED}✗ Some tests failed!${COLOR_NC}"
        return 1
    fi
}

# skip_test marks the current test as skipped
skip_test() {
    local reason=$1
    echo -e "${COLOR_YELLOW}⊘ SKIPPED: ${reason}${COLOR_NC}"
    ((TEST_SKIPPED++))
}

# assert_command_exists checks if a command is available
# Usage: assert_command_exists <command> <error_message>
assert_command_exists() {
    local cmd=$1
    local msg=$2

    if ! command -v "${cmd}" &> /dev/null; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Command '${cmd}' not found${COLOR_NC}"
        return 1
    fi
    return 0
}

# assert_file_exists checks if a file exists
# Usage: assert_file_exists <file_path> <error_message>
assert_file_exists() {
    local file=$1
    local msg=$2

    if [ ! -f "${file}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  File not found: ${file}${COLOR_NC}"
        return 1
    fi
    return 0
}

# assert_http_status checks if an HTTP request returns the expected status
# Usage: assert_http_status <url> <expected_status> <error_message>
assert_http_status() {
    local url=$1
    local expected_status=$2
    local msg=$3

    local actual_status=$(curl -s -o /dev/null -w "%{http_code}" "${url}")

    if [ "${actual_status}" != "${expected_status}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Expected status ${expected_status}, got ${actual_status}${COLOR_NC}"
        return 1
    fi
    return 0
}

# assert_contains checks if a string contains a substring
# Usage: assert_contains <haystack> <needle> <error_message>
assert_contains() {
    local haystack=$1
    local needle=$2
    local msg=$3

    if [[ ! "${haystack}" == *"${needle}"* ]]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Expected '${haystack}' to contain '${needle}'${COLOR_NC}"
        return 1
    fi
    return 0
}

# assert_equals checks if two values are equal
# Usage: assert_equals <expected> <actual> <error_message>
assert_equals() {
    local expected=$1
    local actual=$2
    local msg=$3

    if [ "${expected}" != "${actual}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Expected '${expected}', got '${actual}'${COLOR_NC}"
        return 1
    fi
    return 0
}

# assert_not_empty checks if a value is not empty
# Usage: assert_not_empty <value> <error_message>
assert_not_empty() {
    local value=$1
    local msg=$2

    if [ -z "${value}" ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        echo -e "${COLOR_RED}  Value is empty${COLOR_NC}"
        return 1
    fi
    return 0
}

# assert_success checks if the last command succeeded
# Usage: assert_success <error_message>
assert_success() {
    local msg=$1

    if [ $? -ne 0 ]; then
        echo -e "${COLOR_RED}✗ ERROR: ${msg}${COLOR_NC}"
        return 1
    fi
    return 0
}

# wait_for waits for a condition to be true
# Usage: wait_for <timeout> <interval> <condition_command>
wait_for() {
    local timeout=$1
    local interval=$2
    shift 2
    local condition_cmd="$@"

    local elapsed=0
    while [ ${elapsed} -lt ${timeout} ]; do
        if eval "${condition_cmd}"; then
            return 0
        fi
        sleep ${interval}
        elapsed=$((elapsed + interval))
    done

    echo -e "${COLOR_RED}✗ ERROR: Timeout waiting for condition${COLOR_NC}"
    return 1
}

# Export functions for use in test scripts
export -f run_scenario
export -f print_report
export -f skip_test
export -f assert_command_exists
export -f assert_file_exists
export -f assert_http_status
export -f assert_contains
export -f assert_equals
export -f assert_not_empty
export -f assert_success
export -f wait_for
