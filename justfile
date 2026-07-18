# justfile for localsend.koplugin
#
# Shared recipes are vendored from koplugin-dev (just/shared.just).
# No local toolchain required — just Docker (and `just`).
#
# Quick start:
#   just setup     # install git hooks and pull the image (one-time)
#   just test      # run all tests (quiet; V=1 for verbose)
#   just lint      # lint everything
#   just shell     # drop into the container
#   just release   # cross-compile ARM + package release zips in Docker
#
# When shared recipes change upstream:
#   just sync-shared   # refresh just/shared.just (then commit)

plugin_name := "localsend"
koplugin_dev_version := "v2026.03_6"
# Git ref used by `just sync-shared` (recipe source). Independent of the image pin.
koplugin_dev_ref := env("KOPLUGIN_DEV_REF", "main")
# Go CLI owns the repo root; the installable .koplugin lives under lua/.
plugin_path := "/opt/plugin/lua"
spec_dir := "lua/spec"
lua_paths := "lua"
has_go := "1"
go_integration_packages := "./internal/localsend/..."
exclude_tags := "e2e"

import "./just/shared.just"

# =============================================================================
# Setup (plugin-local)
# =============================================================================

# Refresh just/shared.just from upstream koplugin-dev
# (Named just/ — not vendor/ — so Go does not treat it as a module vendor tree.)
[group('setup')]
sync-shared:
    #!/usr/bin/env bash
    set -euo pipefail
    ref="{{ koplugin_dev_ref }}"
    mkdir -p just
    tmp="$(mktemp)"
    url="https://raw.githubusercontent.com/kaikozlov/koplugin-dev/${ref}/shared.just"
    echo "Fetching ${url}"
    curl -fsSL "$url" -o "$tmp"
    {
        echo "# Vendored from https://github.com/kaikozlov/koplugin-dev"
        echo "# Ref: ${ref}"
        echo "# Refresh with: just sync-shared"
        echo
        cat "$tmp"
    } > just/shared.just
    rm -f "$tmp"
    echo "Updated just/shared.just from koplugin-dev@${ref}"

# =============================================================================
# Build (product-specific)
# =============================================================================

# Run the deterministic QEMU/seccomp audit against both packaged 32-bit ARM binaries.
# Requires a completed release build so the exact shipped binaries are exercised.
[group('test')]
test-armcompat:
    {{ _run }} env GOFLAGS=-buildvcs=false bash /opt/plugin/tools/armcompat/audit.sh

# Cross-compiles three ARM targets (arm-legacy/armv7/arm64) with buildArchTag
# injected via ldflags, then packages each binary with the Lua source into a
# release zip. Runs in the pinned koplugin-dev image used by tests and CI.
# Usage: just release [-p|--package]   (-p = package only, reuse build/bin)
[group('build')]
release *args='':
    {{ _run }} env GOFLAGS=-buildvcs=false just --justfile /opt/plugin/justfile _release {{ args }}

[private]
_release *args='':
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

    # Docker runs as root, but generated artifacts should remain manageable by
    # the checkout owner on the host, including after a failed release build.
    source_owner="$(stat -c '%u:%g' .)"
    trap 'if [ -e "$build_dir" ]; then chown -R "$source_owner" "$build_dir"; fi' EXIT

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

    # Stage Lua plugin source and license (shared by every zip)
    cp lua/*.lua "$stage/"
    cp LICENSE "$stage/"

    if ! $package_only; then
        echo "Cross-compiling ARM binaries..."
        # Amazon's Linux 2.6.31 Kindle kernels omit epoll_pwait and ARM
        # accept4 (ENOSYS). Build 32-bit packages with a scoped GOROOT overlay
        # that restores epoll_wait and Go's former accept fallback.
        compat_overlay="$(go run ./tools/armcompat -output-dir "$build_dir/arm-runtime-compat")"
        CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 \
            go build -overlay="$compat_overlay" \
            -ldflags="-s -w -X localsend-cli/cmd.buildArchTag=arm-legacy" \
            -o build/bin/localsend-arm-legacy .
        CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
            go build -overlay="$compat_overlay" \
            -ldflags="-s -w -X localsend-cli/cmd.buildArchTag=armv7" \
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
