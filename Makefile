# mbos-sandbox-v1 Makefile
# Provides unified entry points for testing and development

.PHONY: help test test-unit test-integration test-e2e test-coverage
.PHONY: docker-compose-up docker-compose-down test-clean
.PHONY: build-sbx-client kind-status kind-up

# Variables
GO ?= go
GO_TEST_OPTS ?= -v -cover -race
COVERAGE_FILE ?= coverage.out
COVERAGE_HTML ?= coverage.html
DOCKER_COMPOSE_FILE ?= docker-compose.test.yaml
MINIO_ENDPOINT ?= http://localhost:9000

help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

test: test-unit test-integration ## Run all tests (unit + integration)

test-unit: ## Run unit tests only
	@echo "Running unit tests..."
	cd manager-service && $(GO) test ./internal/... $(GO_TEST_OPTS)

test-integration: docker-compose-up ## Run integration tests (starts dependencies)
	@echo "Waiting for test dependencies..."
	./scripts/wait-for-minio.sh
	@echo "Running integration tests..."
	cd manager-service && $(GO) test ./integration/... $(GO_TEST_OPTS) \
		-run Integration

test-e2e: build-sbx-client ## Run E2E tests (requires kind cluster)
	@echo "Checking kind cluster..."
	./sbx dev status || { echo "Kind cluster not found. Run: ./sbx dev up"; exit 1; }
	@echo "Running E2E tests..."
	cd manager-service && $(GO) test ./e2e/... $(GO_TEST_OPTS) -run E2E

test-coverage: test ## Generate coverage report
	@echo "Generating coverage report..."
	cd manager-service && $(GO) test ./... -coverprofile=../$(COVERAGE_FILE)
	$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report: $(COVERAGE_HTML)"

docker-compose-up: ## Start Docker Compose test dependencies
	@echo "Starting test dependencies..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) up -d

docker-compose-down: ## Stop Docker Compose test dependencies
	@echo "Stopping test dependencies..."
	docker-compose -f $(DOCKER_COMPOSE_FILE) down -v

test-clean: docker-compose-down ## Clean up test artifacts
	@echo "Cleaning up..."
	rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)

build-sbx-client: ## Build the sandbox client binary
	@echo "Building sbx-client..."
	cd manager-service && $(GO) build -o /tmp/sbx-client ./cmd/sbx-client

kind-status: ## Show kind cluster status
	./sbx dev status

kind-up: ## Create and setup kind cluster
	./sbx dev up
