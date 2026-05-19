SHELL := /bin/bash
.PHONY: help test test-unit test-race test-coverage test-integration test-e2e \
	lint build-asbcp build-docker build-image \
	kind-up kind-down kind-status port-forward smoke \
	release-gate clean-tools clean-test-deps

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
SBX := $(ROOT)/sbx
GO ?= go
ASBCP_DIR := $(ROOT)/manager-service
KUBECTL := $(ROOT)/tools/bin/linux-amd64/kubectl

KIND_CLUSTER ?= sandbox-cluster
KIND_PROXY ?= auto
HARBOR_CA ?= auto

ASBCP_URL ?= http://localhost:8080
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
	@echo "  make release-gate       # wrapper for scripts/verify-release.sh"
	@echo ""
	@echo "Build:"
	@echo "  make build-asbcp        # build ASBCP image"
	@echo "  make build-docker       # build ASBCP image"
	@echo "  make build-image IMG=.. # build one image (asbcp|runner fixture)"
	@echo ""
	@echo "Infrastructure:"
	@echo "  make kind-up            # create kind cluster + deploy"
	@echo "  make kind-down          # delete kind cluster"
	@echo "  make port-forward       # port-forward ASBCP to localhost:8080"
	@echo "  make smoke              # port-forward + smoke test"

test: test-unit

test-unit:
	@echo "==> Running unit tests with race detector..."
	@cd "$(ASBCP_DIR)" && "$(GO)" test -tags=short -race -count=1 ./...

test-race: test-unit

test-coverage:
	@echo "==> Running unit tests with coverage..."
	@cd "$(ASBCP_DIR)" && "$(GO)" test -tags=short -count=1 -coverprofile="$(COVERAGE_FILE)" -covermode=atomic ./...
	@echo "==> Coverage report:"
	@cd "$(ASBCP_DIR)" && "$(GO)" tool cover -func="$(COVERAGE_FILE)" | tail -1
	@echo "==> Coverage file: $(COVERAGE_FILE)"

test-integration:
	@echo "==> Running integration tests..."
	@cd "$(ASBCP_DIR)" && "$(GO)" test -tags=integration -count=1 -race -timeout=300s ./integration/...

test-e2e:
	@echo "==> Running E2E tests (requires K8s cluster and ASBCP)..."
	@cd "$(ASBCP_DIR)" && "$(GO)" test -tags=e2e -count=1 -timeout=900s ./e2e/...

lint:
	@echo "==> Running linters..."
	@cd "$(ASBCP_DIR)" && "$(GO)" vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && (cd "$(ASBCP_DIR)" && golangci-lint run ./...) || echo "    (golangci-lint not installed, go vet only)"

release-gate:
	@bash "$(ROOT)/scripts/verify-release.sh"

_check-coverage:
	@echo "==> Checking coverage threshold (>= $(COVERAGE_THRESHOLD)%)..."
	@cd "$(ASBCP_DIR)" && "$(GO)" test -tags=short -count=1 -coverprofile="$(COVERAGE_FILE)" -covermode=atomic ./... > /dev/null 2>&1
	@TOTAL=$$(cd "$(ASBCP_DIR)" && "$(GO)" tool cover -func="$(COVERAGE_FILE)" | grep "^total:" | awk '{print $$NF}' | tr -d '%'); \
	echo "    Total coverage: $${TOTAL}%"; \
	if [ $$(echo "$${TOTAL} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "    FAIL: coverage $${TOTAL}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	fi; \
	echo "    OK: coverage $${TOTAL}% >= $(COVERAGE_THRESHOLD)%"

_build-check:
	@echo "==> Verifying build..."
	@cd "$(ASBCP_DIR)" && "$(GO)" build ./cmd/asbcp
	@echo "    ASBCP binary builds OK"

build-asbcp:
	@$(SBX) images build --only asbcp

build-docker:
	@if [ "$(DRY_RUN)" = "1" ]; then \
	  echo "$(SBX) images build"; \
	else \
	  $(SBX) images build; \
	fi

build-image:
	@if [ -z "$(IMG)" ]; then \
	  echo "IMG is required (asbcp|runner)"; exit 2; \
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
	@"$(KUBECTL)" -n sandbox-system port-forward svc/agentsmith-sandbox-control-plane 8080:80

smoke:
	@bash -c 'set -euo pipefail; \
	  K="$(KUBECTL)"; \
	  if [ ! -x "$$K" ]; then echo "kubectl not found at $$K (run: make kind-up)"; exit 2; fi; \
	  "$$K" -n sandbox-system port-forward svc/agentsmith-sandbox-control-plane 8080:80 >/tmp/sbx-portforward.log 2>&1 & \
	  pf=$$!; \
	  trap "kill $$pf >/dev/null 2>&1 || true" EXIT; \
	  sleep 2; \
	  "$(ASBCP_DIR)"/scripts/test-asbcp-api.sh "$(ASBCP_URL)" "$(SERVICE_KEY)"; \
	'

clean-tools:
	@rm -rf "$(ROOT)/tools/bin"

clean-test-deps: kind-down clean-tools
