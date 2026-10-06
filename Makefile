SHELL := /bin/sh
.DEFAULT_GOAL := help

# Use the pinned module dependency even when a parent go.work is present.
# Cross-repository development can explicitly pass make GOWORK=/path/to/go.work.
export GOWORK := off

GO ?= go
GOLANGCI_LINT ?= golangci-lint
NPM ?= npm
DOCKER ?= docker
BINARY ?= bin/xops-mcp
CONFIG ?= .local/server.yaml
IMAGE ?= xops-mcp:local
TEST_FLAGS ?= -count=1 -timeout=120s

.PHONY: help build build-check run test test-race test-sqlite test-postgres
.PHONY: lint fmt mod-check check web-install web-check test-browser docker-build clean

help: ## Show available targets
	@awk 'BEGIN { FS = ":.*## "; print "Usage: make [target] [VARIABLE=value]...\n" } /^[a-zA-Z0-9_-]+:.*## / { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Build the executable (BINARY=bin/xops-mcp)
	mkdir -p "$(dir $(BINARY))"
	$(GO) build -trimpath -o "$(BINARY)" ./cmd/xops-mcp

build-check: ## Check that all Go packages build
	$(GO) build ./...

run: build ## Build and serve an initialized deployment (CONFIG=.local/server.yaml)
	"$(abspath $(BINARY))" serve --config "$(CONFIG)"

test: ## Run Go tests (TEST_FLAGS controls count, timeout and test selection)
	$(GO) test $(TEST_FLAGS) ./...

test-race: ## Run Go tests with the race detector
	$(GO) test -race $(TEST_FLAGS) ./...

test-sqlite: ## Run the shared business tests with SQLite and the race detector
	XOPS_TEST_BACKEND=sqlite $(GO) test -race $(TEST_FLAGS) ./...

test-postgres: ## Run PostgreSQL race tests (requires XOPS_TEST_POSTGRES_DSN)
	@if [ -z "$$XOPS_TEST_POSTGRES_DSN" ]; then \
		printf '%s\n' 'Set XOPS_TEST_POSTGRES_DSN to a disposable PostgreSQL server with CREATEDB permission' >&2; \
		exit 1; \
	fi
	XOPS_TEST_BACKEND=postgres $(GO) test -race $(TEST_FLAGS) ./...

lint: ## Run golangci-lint v2
	$(GOLANGCI_LINT) run ./...

fmt: ## Format Go source files
	$(GO) fmt ./...

mod-check: ## Check module tidiness and downloaded dependency integrity
	$(GO) mod tidy -diff
	$(GO) mod verify

check: build-check test lint mod-check ## Run Go build, test, lint and module checks

web-install: ## Install pinned Web test dependencies
	$(NPM) --prefix web ci --ignore-scripts

web-check: ## Check JavaScript syntax and the embedded crypto dependency
	$(NPM) --prefix web run check

test-browser: web-check ## Run browser acceptance (requires Playwright Chromium or XOPS_TEST_CHROME)
	$(NPM) --prefix web run test:browser

docker-build: ## Build the container image (IMAGE=xops-mcp:local)
	$(DOCKER) build -t "$(IMAGE)" .

clean: ## Remove the configured executable and coverage.out
	rm -f -- "$(BINARY)" coverage.out
