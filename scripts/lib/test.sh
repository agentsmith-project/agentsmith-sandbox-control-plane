#!/bin/bash
# scripts/lib/test.sh
# Test command library for sbx CLI

# shellcheck source=scripts/lib/runner.sh
source "${ROOT_DIR}/scripts/test/lib/runner.sh"
# shellcheck source=scripts/lib/assertions.sh
source "${ROOT_DIR}/scripts/test/lib/assertions.sh"
# shellcheck source=scripts/lib/scenarios.sh
source "${ROOT_DIR}/scripts/test/lib/scenarios.sh"

# sbx_test_usage displays usage information for test commands
sbx_test_usage() {
  cat <<'EOF'
Usage:
  ./sbx test smoke [options]       Run smoke tests
  ./sbx test unit [options]        Run unit tests
  ./sbx test cover [options]       Run tests with coverage

Options:
  --verbose, -v     Enable verbose output
  --help, -h         Show this help

Environment Variables:
  MANAGER_URL        Manager service URL (default: http://localhost:8080)
  SANDBOX_NAMESPACE  Sandbox namespace (default: sandbox)
EOF
}

# sbx_test_smoke runs smoke tests
sbx_test_smoke() {
  local verbose=false

  while [[ $# -gt 0 ]]; do
    case $1 in
      --verbose|-v)
        verbose=true
        shift
        ;;
      --help|-h)
        sbx_test_usage
        return 0
        ;;
      *)
        log_error "Unknown option: $1"
        sbx_test_usage
        return 1
        ;;
    esac
  done

  log_info "Running smoke tests..."

  # Source test libraries
  source "${ROOT_DIR}/scripts/test/lib/runner.sh"
  source "${ROOT_DIR}/scripts/test/lib/assertions.sh"
  source "${ROOT_DIR}/scripts/test/lib/scenarios.sh"

  # Run all scenarios even if one fails (so we get full report)
  set +e
  run_scenario "${ROOT_DIR}/scripts/test/smoke/01-environment.sh" "Environment Check"
  run_scenario "${ROOT_DIR}/scripts/test/smoke/02-create-sandbox.sh" "Create Sandbox"
  run_scenario "${ROOT_DIR}/scripts/test/smoke/03-sse-exec.sh" "SSE Exec"
  run_scenario "${ROOT_DIR}/scripts/test/smoke/04-snapshot-restore.sh" "Snapshot & Restore"
  run_scenario "${ROOT_DIR}/scripts/test/smoke/05-cleanup.sh" "Cleanup Resources"
  set -e

  # Print final report and exit with its status
  print_report
}

# sbx_test_unit runs unit tests
sbx_test_unit() {
  log_info "Running unit tests..."

  cd "${ROOT_DIR}/manager-service"

  if [ -f "go.mod" ]; then
    go test -v ./internal/...
  else
    log_error "No Go module found"
    return 1
  fi
}

# sbx_test_cover runs tests with coverage
sbx_test_cover() {
  log_info "Running tests with coverage..."

  cd "${ROOT_DIR}/manager-service"

  if [ -f "go.mod" ]; then
    log_info "Running tests with coverage profile..."
    go test -coverprofile=coverage.out ./internal/...

    log_info "Generating coverage report..."
    go tool cover -html=coverage.out -o coverage.html

    log_info "Coverage report: coverage.html"
    log_info "Function-level coverage:"
    go tool cover -func=coverage.out | grep -v "100.0%"
  else
    log_error "No Go module found"
    return 1
  fi
}

# sbx_test_all runs all tests
sbx_test_all() {
  log_info "Running all tests..."

  sbx_test_unit
  local unit_status=$?

  sbx_test_smoke
  local smoke_status=$?

  if [ ${unit_status} -ne 0 ] || [ ${smoke_status} -ne 0 ]; then
    log_error "Some tests failed"
    return 1
  fi

  log_info "All tests passed"
  return 0
}

# sbx_test entry point
sbx_test() {
  local cmd="${1:-}"
  shift || true

  case "$cmd" in
    -h|--help|help|"")
      sbx_test_usage
      ;;
    smoke)
      sbx_test_smoke "$@"
      ;;
    unit)
      sbx_test_unit "$@"
      ;;
    cover)
      sbx_test_cover "$@"
      ;;
    all)
      sbx_test_all "$@"
      ;;
    *)
      log_error "Unknown test command: $cmd"
      sbx_test_usage
      exit 1
      ;;
  esac
}
