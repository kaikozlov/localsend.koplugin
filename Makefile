# LocalSend KOReader Plugin — Docker-based dev environment
#
# Uses koplugin-dev image from GHCR for a unified build/test/lint environment.
# No local toolchain required — just Docker.
#
# Quick start:
#   make setup     # pull the image (one-time)
#   make test      # run all tests
#   make lint      # lint everything
#   make shell     # drop into the container

PLUGIN_NAME := localsend
KOPLUGIN_DEV_VERSION := v2026.03_1
IMAGE := ghcr.io/kaikozlov/koplugin-dev:$(KOPLUGIN_DEV_VERSION)

# Mount current repo as /opt/plugin
MOUNT := -v "$(PWD)":/opt/plugin -e PLUGIN_NAME=$(PLUGIN_NAME)
RUN := docker run --rm $(MOUNT) $(IMAGE)
RUN_IT := docker run --rm -it $(MOUNT) $(IMAGE)

# =============================================================================
# Setup
# =============================================================================

.PHONY: setup
setup: ## Pull the koplugin-dev image
	docker pull $(IMAGE)

# =============================================================================
# Testing
# =============================================================================

.PHONY: test
test: test-lua test-go ## Run all tests

.PHONY: test-lua
test-lua: ## Run Lua tests (excludes e2e)
	$(RUN) busted-koreader --verbose \
		--helper=/opt/koplugin-dev/commonrequire.lua \
		--exclude-tags=e2e \
		/opt/plugin/lua/spec/

.PHONY: test-lua-all
test-lua-all: ## Run all Lua tests including e2e
	$(RUN) busted-koreader --verbose \
		--helper=/opt/koplugin-dev/commonrequire.lua \
		/opt/plugin/lua/spec/

.PHONY: test-e2e
test-e2e: ## Run only e2e tests
	$(RUN) busted-koreader --verbose \
		--helper=/opt/koplugin-dev/commonrequire.lua \
		--filter=e2e \
		/opt/plugin/lua/spec/

.PHONY: test-go
test-go: ## Run Go tests
	$(RUN) sh -c 'cd /opt/plugin && go test ./... -v -count=1'

.PHONY: test-go-race
test-go-race: ## Run Go tests with race detector
	$(RUN) sh -c 'cd /opt/plugin && go test ./... -race -v -count=1'

.PHONY: test-go-integration
test-go-integration: ## Run Go integration tests
	$(RUN) sh -c 'cd /opt/plugin && go test ./internal/localsend/... -tags=integration -v -count=1'

# =============================================================================
# Linting
# =============================================================================

.PHONY: lint
lint: lint-lua lint-go ## Run all linters

.PHONY: lint-lua
lint-lua: ## Run luacheck
	$(RUN) luacheck /opt/plugin/lua

.PHONY: lint-go
lint-go: ## Run golangci-lint
	$(RUN) sh -c 'cd /opt/plugin && golangci-lint run'

# =============================================================================
# Formatting
# =============================================================================

.PHONY: fmt
fmt: fmt-lua fmt-go ## Format all code

.PHONY: fmt-lua
fmt-lua: ## Format Lua with stylua
	$(RUN) stylua /opt/plugin/lua

.PHONY: fmt-go
fmt-go: ## Format Go code
	$(RUN) sh -c 'cd /opt/plugin && go fmt ./...'

.PHONY: fmt-check
fmt-check: ## Check formatting without modifying
	$(RUN) stylua --check /opt/plugin/lua
	$(RUN) sh -c 'cd /opt/plugin && test -z "$$(gofmt -l .)"'

# =============================================================================
# Building
# =============================================================================

.PHONY: build-go
build-go: ## Build Go binary (native)
	$(RUN) sh -c 'cd /opt/plugin && go build -o $(PLUGIN_NAME) ./cmd/...'

.PHONY: build-go-arm
build-go-arm: ## Cross-compile Go for ARM (Kindle/Kobo)
	$(RUN) sh -c 'cd /opt/plugin && \
		GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
		go build -ldflags="-s -w" -o $(PLUGIN_NAME)-armv7 ./cmd/... && \
		GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -ldflags="-s -w" -o $(PLUGIN_NAME)-arm64 ./cmd/...'

# =============================================================================
# Interactive
# =============================================================================

.PHONY: shell
shell: ## Drop into a shell in the dev container
	$(RUN_IT) /bin/bash

.PHONY: lua
lua: ## Start KOReader's LuaJIT REPL
	$(RUN_IT) /opt/lib/koreader/luajit

# =============================================================================
# Cleanup
# =============================================================================

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf build/ *.zip $(PLUGIN_NAME) $(PLUGIN_NAME)-arm*

.PHONY: help
help: ## Show this help
	@echo "$(PLUGIN_NAME).koplugin targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
