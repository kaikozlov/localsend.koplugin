# Vendored from https://github.com/kaikozlov/koplugin-dev
# Ref: main
# Refresh with: just sync-shared

# shared.just — Common just recipes for KOReader plugins
#
# Source of truth: https://github.com/kaikozlov/koplugin-dev
# Plugins vendor this file (e.g. vendor/shared.just) and import it:
#
#   plugin_name := "myplugin"
#   koplugin_dev_version := "v2026.03_4"
#   plugin_path := "/opt/plugin"            # nested Lua: "/opt/plugin/lua"
#   spec_dir := "spec"                      # nested Lua: "lua/spec"
#   lua_paths := "."                        # stylua/luacheck paths under /opt/plugin
#   has_go := "0"                           # "1" for Go+Lua plugins
#   go_integration_packages := ""           # e.g. "./internal/foo/..." or ""
#   exclude_tags := "e2e"
#   import "./just/shared.just"
#
# Refresh a vendored copy with: just sync-shared
# Use just/ (not vendor/) so Go does not treat the directory as a module vendor tree.
#
# Image: GHCR by default. Override with IMAGE_NAME=... for a local build.
# Verbosity: quiet by default; V=1 for full busted/go output.
# Aggregates (fmt/lint/test/check/fmt-check) use one docker run each.

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# Optional local Dockerfile tree for docker-build (sibling checkout or override).
[private]
_koplugin_dev_dir := env_var_or_default("KOPLUGIN_DEV_DIR", justfile_directory() + "/../koplugin-dev")

[private]
_image := env_var_or_default("IMAGE_NAME", "ghcr.io/kaikozlov/koplugin-dev:" + koplugin_dev_version)

[private]
_v := env("V", "0")

[private]
_sdl_env := "-e SDL_VIDEODRIVER=dummy"

[private]
_mount := "-v " + justfile_directory() + ":/opt/plugin -e PLUGIN_NAME=" + plugin_name + " -e PLUGIN_PATH=" + plugin_path

[private]
_go_cache := if has_go == "1" { "-v " + plugin_name + "-go-mod:/root/go/pkg/mod -v " + plugin_name + "-go-build:/root/.cache/go-build" } else { "" }

[private]
_run := "docker run --rm " + _sdl_env + " " + _mount + " " + _go_cache + " " + _image

[private]
_run_network := "docker run --rm --network=host " + _sdl_env + " " + _mount + " " + _go_cache + " " + _image

[private]
_run_it := "docker run --rm -it " + _sdl_env + " " + _mount + " " + _go_cache + " " + _image

[private]
_busted_opts := if _v == "1" { "--verbose" } else { "" }

[private]
_go_test_opts := if _v == "1" { "-v" } else { "" }

[private]
_commonrequire := "/opt/koplugin-dev/commonrequire.lua"

# =============================================================================
# Default
# =============================================================================

[group('default')]
[private]
_default:
    @just --list

# =============================================================================
# Setup
# =============================================================================

# Pull the koplugin-dev image and install git hooks
[group('setup')]
setup: install-hooks
    docker pull {{ _image }}

# Configure Git to use the checked-in hooks
[group('setup')]
install-hooks:
    git config core.hooksPath .githooks
    chmod +x .githooks/pre-commit
    @echo "Installed git hooks from .githooks/"

# Build a local koplugin-dev image (optional; GHCR is the default)
[group('setup')]
docker-build:
    #!/usr/bin/env bash
    set -euo pipefail
    version="$(awk -F= '/^ARG KOREADER_VERSION=/{print $2; exit}' '{{ _koplugin_dev_dir }}/Dockerfile')"
    image="${IMAGE_NAME:-koplugin-dev:$version}"
    if [ -z "$(docker images -q "$image" 2>/dev/null)" ]; then
        echo "Building $image..."
        docker build -t "$image" '{{ _koplugin_dev_dir }}'
    else
        echo "$image already exists"
    fi

# Force rebuild the local koplugin-dev image
[group('setup')]
docker-rebuild:
    #!/usr/bin/env bash
    set -euo pipefail
    version="$(awk -F= '/^ARG KOREADER_VERSION=/{print $2; exit}' '{{ _koplugin_dev_dir }}/Dockerfile')"
    image="${IMAGE_NAME:-koplugin-dev:$version}"
    docker build --no-cache -t "$image" '{{ _koplugin_dev_dir }}'

# =============================================================================
# Testing
# =============================================================================

# Run Lua tests (and Go tests when has_go=1). Excludes e2e by default.
[group('test')]
test:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _run }} sh -c '
        set -euo pipefail
        V="{{ _v }}"
        busted_opts="{{ _busted_opts }}"
        go_test_opts="{{ _go_test_opts }}"
        has_go="{{ has_go }}"

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
            --helper={{ _commonrequire }} \
            --exclude-tags={{ exclude_tags }} \
            /opt/plugin/{{ spec_dir }}/

        if [ "$has_go" = "1" ]; then
            echo "Running Go tests (-race)..."
            # shellcheck disable=SC2086
            (cd /opt/plugin && go test ./... -race $go_test_opts -count=1)
            if [ -n "{{ go_integration_packages }}" ]; then
                echo "Running Go integration tests..."
                # shellcheck disable=SC2086
                (cd /opt/plugin && go test {{ go_integration_packages }} -tags=integration -race $go_test_opts -count=1)
            fi
        fi
    '

# Run all Lua tests, including e2e, with host networking
[group('test')]
test-all:
    #!/usr/bin/env bash
    set -euo pipefail
    label="Running all Lua tests (including e2e)"
    cmd='{{ _run_network }} busted-koreader {{ _busted_opts }} --helper={{ _commonrequire }} /opt/plugin/{{ spec_dir }}/'
    echo "$label"
    if [ "{{ _v }}" = "1" ]; then
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

# Run only e2e tests, with host networking
[group('test')]
test-e2e:
    #!/usr/bin/env bash
    set -euo pipefail
    label="Running e2e tests"
    cmd='{{ _run_network }} busted-koreader {{ _busted_opts }} --helper={{ _commonrequire }} --filter=e2e /opt/plugin/{{ spec_dir }}/'
    echo "$label"
    if [ "{{ _v }}" = "1" ]; then
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

# Run Lua tests matching a busted filter pattern
[group('test')]
test-filter filter:
    #!/usr/bin/env bash
    set -euo pipefail
    label="Running Lua tests (filter={{ filter }})"
    cmd='{{ _run }} busted-koreader {{ _busted_opts }} --helper={{ _commonrequire }} --filter="{{ filter }}" /opt/plugin/{{ spec_dir }}/'
    echo "$label"
    if [ "{{ _v }}" = "1" ]; then
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

# Run Go tests
[group('test')]
test-go:
    @echo "Running Go tests..."
    {{ _run }} sh -c 'cd /opt/plugin && go test ./... {{ _go_test_opts }} -count=1'

# Run Go tests with the race detector
[group('test')]
test-go-race:
    @echo "Running Go tests (-race)..."
    {{ _run }} sh -c 'cd /opt/plugin && go test ./... -race {{ _go_test_opts }} -count=1'

# =============================================================================
# Linting / formatting
# =============================================================================

# Run all linters in one container
[group('lint')]
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _run }} sh -c '
        set -euo pipefail
        (cd /opt/plugin && luacheck {{ lua_paths }})
        if [ "{{ has_go }}" = "1" ]; then
            (cd /opt/plugin && golangci-lint run)
        fi
    '

# Run luacheck
[group('lint')]
lint-lua:
    {{ _run }} sh -c 'cd /opt/plugin && luacheck {{ lua_paths }}'

# Run golangci-lint
[group('lint')]
lint-go:
    {{ _run }} sh -c 'cd /opt/plugin && golangci-lint run'

# Format all code in one container
[group('lint')]
fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _run }} sh -c '
        set -euo pipefail
        (cd /opt/plugin && stylua {{ lua_paths }})
        if [ "{{ has_go }}" = "1" ]; then
            (cd /opt/plugin && go fmt ./...)
        fi
    '

# Format Lua with stylua
[group('lint')]
fmt-lua:
    {{ _run }} sh -c 'cd /opt/plugin && stylua {{ lua_paths }}'

# Format Go code
[group('lint')]
fmt-go:
    {{ _run }} sh -c 'cd /opt/plugin && go fmt ./...'

# Check formatting without modifying (one container)
[group('lint')]
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _run }} sh -c '
        set -euo pipefail
        (cd /opt/plugin && stylua --check {{ lua_paths }})
        if [ "{{ has_go }}" = "1" ]; then
            (cd /opt/plugin && test -z "$(gofmt -l .)")
        fi
    '

# Format, lint, and test in one container (used by pre-commit)
[group('lint')]
check:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _run }} sh -c '
        set -euo pipefail
        V="{{ _v }}"
        busted_opts="{{ _busted_opts }}"
        go_test_opts="{{ _go_test_opts }}"
        has_go="{{ has_go }}"

        echo "Formatting..."
        (cd /opt/plugin && stylua {{ lua_paths }})
        if [ "$has_go" = "1" ]; then
            (cd /opt/plugin && go fmt ./...)
        fi

        echo "Linting..."
        (cd /opt/plugin && luacheck {{ lua_paths }})
        if [ "$has_go" = "1" ]; then
            (cd /opt/plugin && golangci-lint run)
        fi

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
            --helper={{ _commonrequire }} \
            --exclude-tags={{ exclude_tags }} \
            /opt/plugin/{{ spec_dir }}/

        if [ "$has_go" = "1" ]; then
            echo "Running Go tests (-race)..."
            # shellcheck disable=SC2086
            (cd /opt/plugin && go test ./... -race $go_test_opts -count=1)
            if [ -n "{{ go_integration_packages }}" ]; then
                echo "Running Go integration tests..."
                # shellcheck disable=SC2086
                (cd /opt/plugin && go test {{ go_integration_packages }} -tags=integration -race $go_test_opts -count=1)
            fi
        fi
    '

# Run Go integration tests with race detector
[group('test')]
test-go-integration:
    @echo "Running Go integration tests..."
    {{ _run }} sh -c 'cd /opt/plugin && go test {{ go_integration_packages }} -tags=integration -race {{ _go_test_opts }} -count=1'

# =============================================================================
# Building (Go)
# =============================================================================

# Build Go binary (native)
[group('build')]
build-go:
    {{ _run }} sh -c 'cd /opt/plugin && go build -o {{ plugin_name }} ./cmd/...'

# Cross-compile Go for ARM (Kindle/Kobo) — quick dev build, no packaging
[group('build')]
build-go-arm:
    {{ _run }} sh -c 'cd /opt/plugin && \
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
    {{ _run_it }} /bin/bash

# Start KOReader's LuaJIT REPL
[group('interactive')]
lua:
    {{ _run_it }} /opt/lib/koreader/luajit

# =============================================================================
# Cleanup
# =============================================================================

# Remove build artifacts
[group('default')]
clean:
    rm -rf build/ *.zip {{ plugin_name }} {{ plugin_name }}-arm* test-results.xml

# Remove Docker volumes used for Go module/build caches
[group('default')]
clean-go-cache:
    docker volume rm {{ plugin_name }}-go-mod {{ plugin_name }}-go-build 2>/dev/null || true

# Remove local Docker image
[group('default')]
docker-clean:
    #!/usr/bin/env bash
    set -euo pipefail
    version="$(awk -F= '/^ARG KOREADER_VERSION=/{print $2; exit}' '{{ _koplugin_dev_dir }}/Dockerfile')"
    image="${IMAGE_NAME:-koplugin-dev:$version}"
    docker rmi "$image" 2>/dev/null || true
