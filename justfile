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
#
# Aggregate recipes (fmt / lint / test / check / fmt-check) use a single
# `docker run` each to avoid container startup tax.

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

plugin_name := "localsend"
koplugin_dev_version := "v2026.03_4"
image := "ghcr.io/kaikozlov/koplugin-dev:" + koplugin_dev_version

# Verbosity: quiet by default (failures + summary only). Use V=1 for full output.
#   just test
#   V=1 just test
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

# Run all tests in one container (quiet; V=1 verbose)
[group('test')]
test:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ run }} sh -c '
        set -euo pipefail
        V="{{ v }}"
        busted_opts="{{ busted_opts }}"
        go_test_opts="{{ go_test_opts }}"

        run_busted_quiet() {
            local label="$1"
            shift
            echo "$label"
            if [ "$V" = "1" ]; then
                "$@"
            else
                local out
                out="$(mktemp)"
                if "$@" >"$out" 2>&1; then
                    grep -E "^[0-9]+ success" "$out" || tail -n 3 "$out"
                    rm -f "$out"
                else
                    echo "$label failed — full output:" >&2
                    cat "$out" >&2
                    rm -f "$out"
                    exit 1
                fi
            fi
        }

        # shellcheck disable=SC2086
        run_busted_quiet "Running Lua tests" busted-koreader $busted_opts \
            --helper=/opt/koplugin-dev/commonrequire.lua \
            /opt/plugin/lua/spec/

        echo "Running Go tests (-race)..."
        # shellcheck disable=SC2086
        (cd /opt/plugin && go test ./... -race $go_test_opts -count=1)

        echo "Running Go integration tests..."
        # shellcheck disable=SC2086
        (cd /opt/plugin && go test ./internal/localsend/... -tags=integration -race $go_test_opts -count=1)
    '

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

# Run all linters in one container
[group('lint')]
lint:
    {{ run }} sh -c 'cd /opt/plugin/lua && luacheck . && cd /opt/plugin && golangci-lint run'

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

# Format all code in one container
[group('lint')]
fmt:
    {{ run }} sh -c 'stylua /opt/plugin/lua && cd /opt/plugin && go fmt ./...'

# Format Lua with stylua
[group('lint')]
fmt-lua:
    {{ run }} stylua /opt/plugin/lua

# Format Go code
[group('lint')]
fmt-go:
    {{ run }} sh -c 'cd /opt/plugin && go fmt ./...'

# Check formatting without modifying (one container)
[group('lint')]
fmt-check:
    {{ run }} sh -c 'stylua --check /opt/plugin/lua && cd /opt/plugin && test -z "$(gofmt -l .)"'

# Format, lint, and test in one container (used by pre-commit)
[group('lint')]
check:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ run }} sh -c '
        set -euo pipefail
        V="{{ v }}"
        busted_opts="{{ busted_opts }}"
        go_test_opts="{{ go_test_opts }}"

        echo "Formatting..."
        stylua /opt/plugin/lua
        (cd /opt/plugin && go fmt ./...)

        echo "Linting..."
        (cd /opt/plugin/lua && luacheck .)
        (cd /opt/plugin && golangci-lint run)

        run_busted_quiet() {
            local label="$1"
            shift
            echo "$label"
            if [ "$V" = "1" ]; then
                "$@"
            else
                local out
                out="$(mktemp)"
                if "$@" >"$out" 2>&1; then
                    grep -E "^[0-9]+ success" "$out" || tail -n 3 "$out"
                    rm -f "$out"
                else
                    echo "$label failed — full output:" >&2
                    cat "$out" >&2
                    rm -f "$out"
                    exit 1
                fi
            fi
        }

        # shellcheck disable=SC2086
        run_busted_quiet "Running Lua tests" busted-koreader $busted_opts \
            --helper=/opt/koplugin-dev/commonrequire.lua \
            /opt/plugin/lua/spec/

        echo "Running Go tests (-race)..."
        # shellcheck disable=SC2086
        (cd /opt/plugin && go test ./... -race $go_test_opts -count=1)

        echo "Running Go integration tests..."
        # shellcheck disable=SC2086
        (cd /opt/plugin && go test ./internal/localsend/... -tags=integration -race $go_test_opts -count=1)
    '

# =============================================================================
# Building
# =============================================================================

# Build Go binary (native)
[group('build')]
build-go:
    {{ run }} sh -c 'cd /opt/plugin && go build -o {{ plugin_name }} ./cmd/...'

# Cross-compile Go for ARM (Kindle/Kobo) — quick dev build, no arch tag or packaging
[group('build')]
build-go-arm:
    {{ run }} sh -c 'cd /opt/plugin && \
        GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 \
        go build -ldflags="-s -w" -o {{ plugin_name }}-armv7 ./cmd/... && \
        GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
        go build -ldflags="-s -w" -o {{ plugin_name }}-arm64 ./cmd/...'

# Cross-compiles three ARM targets (arm-legacy/armv7/arm64) with buildArchTag
# injected via ldflags, then packages each binary with the Lua source into a
# release zip. Mirrors .github/workflows/koplugin.yaml → build/*.zip.
# Requires host Go (CGO_ENABLED=0 cross-compile) and host `zip`.
# Usage: just release [-p|--package]   (-p = package only, reuse build/bin)
# Build & package release zips for all ARM targets (arm-legacy, armv7, arm64).
[group('build')]
release *args='':
    #!/usr/bin/env bash
    set -euo pipefail

    package_only=false
    for a in {{ args }}; do
        case "$a" in
            -p|--package) package_only=true ;;
            -h|--help)
                echo "Usage: just release [-p|--package]"
                echo "  -p, --package  package only (skip compilation, reuse build/bin)"
                exit 0
                ;;
            *) echo "Unknown option: $a" >&2; exit 2 ;;
        esac
    done

    build_dir="build"
    bin_dir="$build_dir/bin"
    pkg="localsend.koplugin"
    stage="$build_dir/$pkg"

    if $package_only; then
        for arch in arm-legacy armv7 arm64; do
            if [ ! -f "$bin_dir/localsend-$arch" ]; then
                echo "Error: $bin_dir/localsend-$arch not found — run 'just release' first." >&2
                exit 1
            fi
        done
        echo "Package-only mode: reusing existing binaries"
        rm -rf "$stage"
        rm -f "$build_dir"/*.zip
    else
        rm -rf "$build_dir"
        mkdir -p "$bin_dir"
    fi
    mkdir -p "$stage"

    # Stage Lua plugin source (shared by every zip)
    cp lua/*.lua "$stage/"

    if ! $package_only; then
        echo "Cross-compiling ARM binaries..."
        CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 \
            go build -ldflags="-s -w -X localsend-cli/cmd.buildArchTag=arm-legacy" \
            -o build/bin/localsend-arm-legacy .
        CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
            go build -ldflags="-s -w -X localsend-cli/cmd.buildArchTag=armv7" \
            -o build/bin/localsend-armv7 .
        CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
            go build -ldflags="-s -w -X localsend-cli/cmd.buildArchTag=arm64" \
            -o build/bin/localsend-arm64 .
    fi

    for arch in arm-legacy armv7 arm64; do
        echo "Packaging $arch zip..."
        cp "$bin_dir/localsend-$arch" "$stage/localsend"
        ( cd "$build_dir" && rm -f "localsend-koplugin-$arch.zip" \
            && zip -rq "localsend-koplugin-$arch.zip" "$pkg" )
    done

    rm -rf "$stage"

    echo
    echo "Done! Release artifacts:"
    ls -lh "$build_dir"/*.zip
    echo
    echo "Binaries:"
    ls -lh "$bin_dir"/*

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
