SHELL := /bin/bash
.PHONY: help test test-integration build-manager build-runner build-cleaner build-image \
	kind-up kind-down kind-status port-forward smoke clean-tools clean-test-deps

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
RUNNER_IMAGE ?=

help:
	@cat <<'EOF'
Targets:
  make test                # unit tests (skip integration)
  make test-integration     # full test suite
  make build-manager        # build manager image
  make build-runner         # build runner image
  make build-cleaner        # build gc/cleaner image
  make build-image IMG=...  # build any image (manager|runner|gc)
  make kind-up              # fetch tools + create kind cluster + build/deploy
  make kind-down            # delete kind cluster
  make kind-status          # cluster status
  make port-forward         # port-forward manager service to localhost:8080
  make smoke                # port-forward + run manager API smoke test
  make clean-test-deps      # remove kind cluster + vendored tools
EOF

test:
	@cd "$(MANAGER_DIR)" && "$(GO)" test -tags=short ./...

test-integration:
	@cd "$(MANAGER_DIR)" && "$(GO)" test ./...

build-manager:
	@$(SBX) images build --only manager

build-runner:
	@$(SBX) images build --only runner

build-cleaner:
	@$(SBX) images build --only gc

build-image:
	@if [ -z "$(IMG)" ]; then \
	  echo "IMG is required (manager|runner|gc)"; exit 2; \
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
	  args=("$${MANAGER_URL}" "$${SERVICE_KEY}"); \
	  if [ -n "$${RUNNER_IMAGE}" ]; then args+=("$${RUNNER_IMAGE}"); fi; \
	  "$(MANAGER_DIR)"/scripts/test-manager.sh "$${args[@]}"; \
	'

clean-tools:
	@rm -rf "$(ROOT)/tools/bin"

clean-test-deps: kind-down clean-tools
