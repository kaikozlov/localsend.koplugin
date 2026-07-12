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
KOPLUGIN_DEV_VERSION := v2026.03_4
IMAGE := ghcr.io/kaikozlov/koplugin-dev:$(KOPLUGIN_DEV_VERSION)

# SDL dummy driver for headless KOReader (real device/UIManager support)
SDL_ENV := -e SDL_VIDEODRIVER=dummy

# Mount current repo as /opt/plugin. Lua plugin source lives in /opt/plugin/lua;
# expose that as PLUGIN_PATH so native PluginLoader tests exercise the real plugin.
MOUNT := -v "$(PWD)":/opt/plugin -e PLUGIN_NAME=$(PLUGIN_NAME) -e PLUGIN_PATH=/opt/plugin/lua

# Persist Go module/build caches across ephemeral docker run --rm containers.
# Without these volumes, every test run redownloads modules into /root/go/pkg/mod
# and rebuilds packages into /root/.cache/go-build from scratch.
GO_CACHE := \
	-v $(PLUGIN_NAME)-go-mod:/root/go/pkg/mod \
	-v $(PLUGIN_NAME)-go-build:/root/.cache/go-build

RUN := docker run --rm $(SDL_ENV) $(MOUNT) $(GO_CACHE) $(IMAGE)
RUN_IT := docker run --rm -it $(SDL_ENV) $(MOUNT) $(GO_CACHE) $(IMAGE)

# Verbosity: quiet by default (failures + summary only). Use V=1 for full output.
#   make test
#   make test V=1
#   ./test.sh --verbose
V ?= 0
ifeq ($(V),1)
BUSTED_OPTS := --verbose
GO_TEST_OPTS := -v
else
BUSTED_OPTS :=
GO_TEST_OPTS :=
endif

# Run busted quietly: KOReader bootstrap/log spam is captured. On success print
# the summary line; on failure dump the full log. V=1 streams live output.
define run_busted_quiet
	@echo "$(1)"
	@if [ "$(V)" = "1" ]; then \
		$(2); \
	else \
		out=$$(mktemp); \
		if $(2) >$$out 2>&1; then \
			grep -E '^[0-9]+ success' $$out || tail -n 3 $$out; \
			rm -f $$out; \
		else \
			echo "$(1) failed — full output:" >&2; \
			cat $$out >&2; \
			rm -f $$out; \
			exit 1; \
		fi; \
	fi
endef

# =============================================================================
# Setup
# =============================================================================

.PHONY: setup
setup: ## Pull the koplugin-dev image and install git hooks
	docker pull $(IMAGE)
	cp scripts/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit
	@echo "✓ Git pre-commit hook installed"

# =============================================================================
# Testing
# =============================================================================

.PHONY: test
test: test-lua test-go-race test-go-integration ## Run all tests (quiet; V=1 verbose)

.PHONY: test-lua
test-lua: ## Run Lua tests (quiet; V=1 verbose)
	$(call run_busted_quiet,Running Lua tests,$(RUN) busted-koreader $(BUSTED_OPTS) \
		--helper=/opt/koplugin-dev/commonrequire.lua \
		/opt/plugin/lua/spec/)

.PHONY: test-lua-filter
test-lua-filter: ## Run Lua tests matching FILTER="pattern" (quiet; V=1 verbose)
	@test -n "$(FILTER)" || (echo 'Usage: make test-lua-filter FILTER="pattern" [V=1]' >&2; exit 2)
	$(call run_busted_quiet,Running Lua tests (filter=$(FILTER)),$(RUN) busted-koreader $(BUSTED_OPTS) \
		--helper=/opt/koplugin-dev/commonrequire.lua \
		--filter="$(FILTER)" \
		/opt/plugin/lua/spec/)

.PHONY: test-go
test-go: ## Run Go tests (quiet; V=1 verbose)
	@echo "Running Go tests..."
	@$(RUN) sh -c 'cd /opt/plugin && go test ./... $(GO_TEST_OPTS) -count=1'

.PHONY: test-go-race
test-go-race: ## Run Go tests with race detector (quiet; V=1 verbose)
	@echo "Running Go tests (-race)..."
	@$(RUN) sh -c 'cd /opt/plugin && go test ./... -race $(GO_TEST_OPTS) -count=1'

.PHONY: test-go-integration
test-go-integration: ## Run Go integration tests with race detector (quiet; V=1 verbose)
	@echo "Running Go integration tests..."
	@$(RUN) sh -c 'cd /opt/plugin && go test ./internal/localsend/... -tags=integration -race $(GO_TEST_OPTS) -count=1'

# =============================================================================
# Linting
# =============================================================================

.PHONY: lint
lint: lint-lua lint-go ## Run all linters

.PHONY: lint-lua
lint-lua: ## Run luacheck
	$(RUN) sh -c 'cd /opt/plugin/lua && luacheck .'

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

.PHONY: clean-go-cache
clean-go-cache: ## Remove Docker volumes used for Go module/build caches
	docker volume rm $(PLUGIN_NAME)-go-mod $(PLUGIN_NAME)-go-build 2>/dev/null || true

.PHONY: help
help: ## Show this help
	@echo "$(PLUGIN_NAME).koplugin targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
