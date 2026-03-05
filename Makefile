SHELL := /bin/bash
.PHONY: help test test-unit test-race test-coverage test-integration test-e2e \
	lint build-manager build-cleaner build-image \
	kind-up kind-down kind-status port-forward smoke \
	release-gate clean-tools clean-test-deps

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
SBX := $(ROOT)/sbx
GO ?= go
MANAGER_DIR := $(ROOT)/manager-service
KUBECTL := $(ROOT)/tools/bin/linux-amd64/kubectl

KIND_CLUSTER ?= sandbox-cluster
KIND_PROXY ?= auto
HARBOR_CA ?= auto

MANAGER_URL ?= http://localhost:8080
SERVICE_KEY ?= test-key-123

COVERAGE_THRESHOLD ?= 55
COVERAGE_FILE := $(ROOT)/coverage.out

help:
	@echo "Testing:"
	@echo "  make test               # alias for test-unit"
	@echo "  make test-unit          # unit tests with -race"
	@echo "  make test-race          # alias for test-unit"
	@echo "  make test-coverage      # unit tests + coverage report"
	@echo "  make test-integration   # integration tests"
	@echo "  make test-e2e           # E2E tests (needs kind cluster)"
	@echo "  make lint               # golangci-lint"
	@echo "  make release-gate       # ALL: lint + test-race + coverage + build"
	@echo ""
	@echo "Build:"
	@echo "  make build-manager      # build manager image"
	@echo "  make build-cleaner      # build gc/cleaner image"
	@echo "  make build-image IMG=.. # build any image (manager|gc)"
	@echo ""
	@echo "Infrastructure:"
	@echo "  make kind-up            # create kind cluster + deploy"
	@echo "  make kind-down          # delete kind cluster"
	@echo "  make port-forward       # port-forward manager to localhost:8080"
	@echo "  make smoke              # port-forward + smoke test"

test: test-unit

test-unit:
	@echo "==> Running unit tests with race detector..."
	@cd "$(MANAGER_DIR)" && "$(GO)" test -tags=short -race -count=1 ./...

test-race: test-unit

test-coverage:
	@echo "==> Running unit tests with coverage..."
	@cd "$(MANAGER_DIR)" && "$(GO)" test -tags=short -count=1 -coverprofile="$(COVERAGE_FILE)" -covermode=atomic ./...
	@echo "==> Coverage report:"
	@cd "$(MANAGER_DIR)" && "$(GO)" tool cover -func="$(COVERAGE_FILE)" | tail -1
	@echo "==> Coverage file: $(COVERAGE_FILE)"

test-integration:
	@echo "==> Running integration tests..."
	@cd "$(MANAGER_DIR)" && "$(GO)" test -count=1 -race -timeout=300s ./...

test-e2e:
	@echo "==> Running E2E tests..."
	@cd "$(MANAGER_DIR)" && "$(GO)" test -tags=E2E -count=1 -timeout=600s ./e2e/...

lint:
	@echo "==> Running golangci-lint..."
	@cd "$(MANAGER_DIR)" && golangci-lint run ./...

release-gate: lint test-race _check-coverage _build-check
	@echo ""
	@echo "==> Release gate PASSED"

_check-coverage:
	@echo "==> Checking coverage threshold (>= $(COVERAGE_THRESHOLD)%)..."
	@cd "$(MANAGER_DIR)" && "$(GO)" test -tags=short -count=1 -coverprofile="$(COVERAGE_FILE)" -covermode=atomic ./... > /dev/null 2>&1
	@TOTAL=$$(cd "$(MANAGER_DIR)" && "$(GO)" tool cover -func="$(COVERAGE_FILE)" | grep "^total:" | awk '{print $$NF}' | tr -d '%'); \
	echo "    Total coverage: $${TOTAL}%"; \
	if [ $$(echo "$${TOTAL} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "    FAIL: coverage $${TOTAL}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	fi; \
	echo "    OK: coverage $${TOTAL}% >= $(COVERAGE_THRESHOLD)%"

_build-check:
	@echo "==> Verifying build..."
	@cd "$(MANAGER_DIR)" && "$(GO)" build ./cmd/manager/
	@cd "$(MANAGER_DIR)" && "$(GO)" build ./cmd/cleaner/
	@echo "    All binaries build OK"

build-manager:
	@$(SBX) images build --only manager

build-cleaner:
	@$(SBX) images build --only gc

build-image:
	@if [ -z "$(IMG)" ]; then \
	  echo "IMG is required (manager|gc)"; exit 2; \
	fi
	@$(SBX) images build --only "$(IMG)"

kind-up:
	@$(SBX) tools fetch --proxy "$(KIND_PROXY)"
	@$(SBX) dev up --force --proxy "$(KIND_PROXY)" --harbor-ca "$(HARBOR_CA)" --cluster "$(KIND_CLUSTER)"

kind-down:
	@$(SBX) dev down --force --cluster "$(KIND_CLUSTER)"

kind-status:
	@$(SBX) dev status --cluster "$(KIND_CLUSTER)"

port-forward:
	@"$(KUBECTL)" -n sandbox-system port-forward svc/sandbox-manager 8080:80

smoke:
	@bash -c 'set -euo pipefail; \
	  K="$(KUBECTL)"; \
	  if [ ! -x "$$K" ]; then echo "kubectl not found at $$K (run: make kind-up)"; exit 2; fi; \
	  "$$K" -n sandbox-system port-forward svc/sandbox-manager 8080:80 >/tmp/sbx-portforward.log 2>&1 & \
	  pf=$$!; \
	  trap "kill $$pf >/dev/null 2>&1 || true" EXIT; \
	  sleep 2; \
	  "$(MANAGER_DIR)"/scripts/test-manager.sh "$(MANAGER_URL)" "$(SERVICE_KEY)"; \
	'

clean-tools:
	@rm -rf "$(ROOT)/tools/bin"

clean-test-deps: kind-down clean-tools
