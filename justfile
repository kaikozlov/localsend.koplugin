# justfile for localsend.koplugin
#
# Shared recipes come from a sibling checkout of koplugin-dev.
# No local toolchain required — just Docker (and `just`).
#
# Quick start:
#   just setup     # install git hooks and pull the image (one-time)
#   just test      # run all tests (quiet; V=1 for verbose)
#   just lint      # lint everything
#   just shell     # drop into the container
#   just release   # cross-compile ARM + package release zips (host Go)

plugin_name := "localsend"
koplugin_dev_version := "v2026.03_4"
# Go CLI owns the repo root; the installable .koplugin lives under lua/.
plugin_path := "/opt/plugin/lua"
spec_dir := "lua/spec"
lua_paths := "lua"
has_go := "1"
go_integration_packages := "./internal/localsend/..."
exclude_tags := "e2e"

import "../koplugin-dev/shared.just"

# =============================================================================
# Build (product-specific)
# =============================================================================

# Cross-compiles three ARM targets (arm-legacy/armv7/arm64) with buildArchTag
# injected via ldflags, then packages each binary with the Lua source into a
# release zip. Mirrors .github/workflows/koplugin.yaml → build/*.zip.
# Requires host Go (CGO_ENABLED=0 cross-compile) and host `zip`.
# Usage: just release [-p|--package]   (-p = package only, reuse build/bin)
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

    # Stage Lua plugin source and license (shared by every zip)
    cp lua/*.lua "$stage/"
    cp LICENSE "$stage/"

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
