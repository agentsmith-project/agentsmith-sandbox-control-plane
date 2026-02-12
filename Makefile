# mbos-sandbox-v1 Makefile
# Provides unified entry points for testing and development

.PHONY: help test test-unit test-coverage test-integration test-integration-k8s test-e2e
.PHONY: docker-compose-up docker-compose-down test-clean
.PHONY: build kind-status kind-up

# Variables
GO ?= go
GO_TEST_OPTS ?= -v -cover -race
COVERAGE_FILE ?= coverage.out
COVERAGE_HTML ?= coverage.html
DOCKER_COMPOSE_FILE ?= docker-compose.test.yaml

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

test: test-unit ## Run all tests

test-unit: ## Run unit tests
	@echo "Running unit tests..."
	cd manager-service && $(GO) test ./... $(GO_TEST_OPTS)

test-coverage: ## Generate coverage report
	@echo "Generating coverage report..."
	cd manager-service && $(GO) test ./... -coverprofile=../$(COVERAGE_FILE)
	$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report: $(COVERAGE_HTML)"

build: ## Build the manager binary
	@echo "Building manager..."
	cd manager-service && $(GO) build -o /tmp/sandbox-manager ./cmd/manager

docker-compose-up: ## Start Docker Compose test dependencies
	@echo "Starting test dependencies..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) up -d

docker-compose-down: ## Stop Docker Compose test dependencies
	@echo "Stopping test dependencies..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) down -v

test-clean: docker-compose-down ## Clean up test artifacts
	@echo "Cleaning up..."
	rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)

kind-status: ## Show kind cluster status
	./sbx dev status

kind-up: ## Create and setup kind cluster
	./sbx dev up

vet: ## Run go vet
	cd manager-service && $(GO) vet ./...

test-integration: docker-compose-up ## Run integration tests (requires Docker)
	@echo "Waiting for test dependencies..."
	@sleep 5
	@echo "Running storage integration tests..."
	cd manager-service && $(GO) test ./internal/storage/... -tags=integration $(GO_TEST_OPTS)

test-integration-k8s: ## Run K8s integration tests (requires kind cluster)
	@echo "Running K8s integration tests..."
	cd manager-service && $(GO) test ./internal/k8s/... -tags=integration $(GO_TEST_OPTS)

test-e2e: ## Run E2E tests (requires kind cluster + deployed manager)
	cd manager-service && $(GO) test ./e2e/... -tags=e2e $(GO_TEST_OPTS) -timeout 5m
