# justfile for localsend.koplugin
#
# Uses the koplugin-dev Docker image from GHCR for a unified build/test/lint
# environment. No local toolchain required — just Docker (and `just`).
#
# Quick start:
#   just setup     # install git hooks and pull the image (one-time)
#   just test      # run all tests (quiet; V=1 for verbose)
#   just lint      # lint everything
#   just shell     # drop into the container

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

plugin_name := "localsend"
koplugin_dev_version := "v2026.03_4"
image := "ghcr.io/kaikozlov/koplugin-dev:" + koplugin_dev_version

# Verbosity: quiet by default (failures + summary only). Use V=1 for full output.
#   just test
#   V=1 just test
#   ./test.sh --verbose
v := env("V", "0")

# SDL dummy driver for headless KOReader
sdl_env := "-e SDL_VIDEODRIVER=dummy"

# Mount the repo as /opt/plugin. Lua plugin source lives in /opt/plugin/lua;
# expose that as PLUGIN_PATH so native PluginLoader tests exercise the real plugin.
mount := "-v " + justfile_directory() + ":/opt/plugin -e PLUGIN_NAME=" + plugin_name + " -e PLUGIN_PATH=/opt/plugin/lua"

# Persist Go module/build caches across ephemeral docker run --rm containers.
go_cache := "-v " + plugin_name + "-go-mod:/root/go/pkg/mod -v " + plugin_name + "-go-build:/root/.cache/go-build"

# Standard run (no network)
run := "docker run --rm " + sdl_env + " " + mount + " " + go_cache + " " + image
# Interactive run
run_it := "docker run --rm -it " + sdl_env + " " + mount + " " + go_cache + " " + image

busted_opts := if v == "1" { "--verbose" } else { "" }
go_test_opts := if v == "1" { "-v" } else { "" }

# =============================================================================
# Default
# =============================================================================

[group('default')]
[private]
default:
    @just --list

# =============================================================================
# Setup
# =============================================================================

# Pull the koplugin-dev image and install git hooks
[group('setup')]
setup: install-hooks
    docker pull {{ image }}

# Configure Git to use the checked-in hooks
[group('setup')]
install-hooks:
    git config core.hooksPath .githooks
    chmod +x .githooks/pre-commit
    @echo "Installed git hooks from .githooks/"

# =============================================================================
# Testing
# =============================================================================

# Run all tests (quiet; V=1 verbose)
[group('test')]
test: test-lua test-go-race test-go-integration

# Run Lua tests via busted-koreader (quiet; V=1 verbose)
[group('test')]
test-lua:
    #!/usr/bin/env bash
    set -euo pipefail
    label="Running Lua tests"
    cmd='{{ run }} busted-koreader {{ busted_opts }} --helper=/opt/koplugin-dev/commonrequire.lua /opt/plugin/lua/spec/'
    echo "$label"
    if [ "{{ v }}" = "1" ]; then
        eval "$cmd"
    else
        out="$(mktemp)"
        if eval "$cmd" >"$out" 2>&1; then
            grep -E '^[0-9]+ success' "$out" || tail -n 3 "$out"
            rm -f "$out"
        else
            echo "$label failed — full output:" >&2
            cat "$out" >&2
            rm -f "$out"
            exit 1
        fi
    fi

# Run Lua tests matching a pattern, e.g. `just test-lua-filter caching`
[group('test')]
test-lua-filter filter:
    #!/usr/bin/env bash
    set -euo pipefail
    label="Running Lua tests (filter={{ filter }})"
    cmd='{{ run }} busted-koreader {{ busted_opts }} --helper=/opt/koplugin-dev/commonrequire.lua --filter="{{ filter }}" /opt/plugin/lua/spec/'
    echo "$label"
    if [ "{{ v }}" = "1" ]; then
        eval "$cmd"
    else
        out="$(mktemp)"
        if eval "$cmd" >"$out" 2>&1; then
            grep -E '^[0-9]+ success' "$out" || tail -n 3 "$out"
            rm -f "$out"
        else
            echo "$label failed — full output:" >&2
            cat "$out" >&2
            rm -f "$out"
            exit 1
        fi
    fi

# Run Go tests (quiet; V=1 verbose)
[group('test')]
test-go:
    @echo "Running Go tests..."
    {{ run }} sh -c 'cd /opt/plugin && go test ./... {{ go_test_opts }} -count=1'

# Run Go tests with race detector (quiet; V=1 verbose)
[group('test')]
test-go-race:
    @echo "Running Go tests (-race)..."
    {{ run }} sh -c 'cd /opt/plugin && go test ./... -race {{ go_test_opts }} -count=1'

# Run Go integration tests with race detector (quiet; V=1 verbose)
[group('test')]
test-go-integration:
    @echo "Running Go integration tests..."
    {{ run }} sh -c 'cd /opt/plugin && go test ./internal/localsend/... -tags=integration -race {{ go_test_opts }} -count=1'

# =============================================================================
# Linting
# =============================================================================

# Run all linters
[group('lint')]
lint: lint-lua lint-go

# Run luacheck
[group('lint')]
lint-lua:
    {{ run }} sh -c 'cd /opt/plugin/lua && luacheck .'

# Run golangci-lint
[group('lint')]
lint-go:
    {{ run }} sh -c 'cd /opt/plugin && golangci-lint run'

# =============================================================================
# Formatting
# =============================================================================

# Format all code
[group('lint')]
fmt: fmt-lua fmt-go

# Format Lua with stylua
[group('lint')]
fmt-lua:
    {{ run }} stylua /opt/plugin/lua

# Format Go code
[group('lint')]
fmt-go:
    {{ run }} sh -c 'cd /opt/plugin && go fmt ./...'

# Check formatting without modifying
[group('lint')]
fmt-check:
    {{ run }} stylua --check /opt/plugin/lua
    {{ run }} sh -c 'cd /opt/plugin && test -z "$(gofmt -l .)"'

# =============================================================================
# Building
# =============================================================================

# Build Go binary (native)
[group('build')]
build-go:
    {{ run }} sh -c 'cd /opt/plugin && go build -o {{ plugin_name }} ./cmd/...'

# Cross-compile Go for ARM (Kindle/Kobo)
[group('build')]
build-go-arm:
    {{ run }} sh -c 'cd /opt/plugin && \
        GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
        go build -ldflags="-s -w" -o {{ plugin_name }}-armv7 ./cmd/... && \
        GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
        go build -ldflags="-s -w" -o {{ plugin_name }}-arm64 ./cmd/...'

# =============================================================================
# Interactive
# =============================================================================

# Drop into a shell in the dev container
[group('interactive')]
shell:
    {{ run_it }} /bin/bash

# Start KOReader's LuaJIT REPL
[group('interactive')]
lua:
    {{ run_it }} /opt/lib/koreader/luajit

# =============================================================================
# Cleanup
# =============================================================================

# Remove build artifacts
[group('default')]
clean:
    rm -rf build/ *.zip {{ plugin_name }} {{ plugin_name }}-arm*

# Remove Docker volumes used for Go module/build caches
[group('default')]
clean-go-cache:
    docker volume rm {{ plugin_name }}-go-mod {{ plugin_name }}-go-build 2>/dev/null || true
