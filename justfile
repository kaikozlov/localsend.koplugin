# justfile for localsend.koplugin
#
# Shared recipes are vendored from koplugin-dev (just/shared.just).
# No local toolchain required — just Docker (and `just`).
#
# Quick start:
#   just setup     # one-time: install hooks and pull the development image
#   just           # verify everything, build release packages, and audit them
#   just --list    # discover optional focused or maintenance recipes
#
# When shared recipes change upstream:
#   just sync-shared   # refresh just/shared.just (then commit)

plugin_name := "localsend"
koplugin_dev_version := "v2026.07_1"
# Git ref used by `just sync-shared` (recipe source). Independent of the image pin.
koplugin_dev_ref := env("KOPLUGIN_DEV_REF", "main")
# Go CLI owns the repo root; the installable .koplugin lives under lua/.
plugin_path := "/opt/plugin/lua"
spec_dir := "lua/spec"
lua_paths := "lua"
has_go := "1"
# Build-tagged tests follow these prefixes so the second Go pass runs only the
# integration/stability suite instead of repeating every ordinary package test.
go_integration_packages := "./internal/... -run ^\\(TestIntegration_\\|TestStability_\\)"
exclude_tags := "e2e"

import "./just/shared.just"

# Canonical end-to-end workflow.
# Verify everything, build all release packages, and audit them.
[group('build')]
default:
    {{ _run }} {{ _reenter }} _all

[private]
_all: _verify _package

# =============================================================================
# Canonical verification
# =============================================================================

# Read-only static checks suitable for pre-commit.
[group('lint')]
verify-static:
    {{ _run }} {{ _reenter }} _verify_static

[private]
_verify_static: fmt-check lint i18n-check pot-check

# Definitive local/CI verification, including race and tagged integration tests.
[group('test')]
verify:
    {{ _run }} {{ _reenter }} _verify

[private]
_verify: _verify_static test

# Uses REFERENCE/OFFICIAL_LOCALSEND/localsend when available; otherwise the
# runner sparse-fetches the pinned upstream commit.
# Cross-test the Go executable with LocalSend's official Rust V2 client/server.
[group('test')]
test-official *args='':
    KOPLUGIN_DEV_IMAGE="{{ _image }}" \
    tools/officialinterop/run.sh {{ args }}

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

# Run after changing user-facing strings wrapped in _() / deps._() / N_() /
# deps.N_(). Uses gettext from the pinned development image. Commit the result;
# its standard POT header intentionally matches KOReader's workflow.
# Regenerate the template and merge it into every translation catalogue.
[group('build')]
pot:
    {{ _run }} just --justfile /opt/plugin/justfile _pot

[private]
_pot:
    #!/usr/bin/env bash
    set -euo pipefail
    source_owner="$(stat -c '%u:%g' .)"
    trap 'chown -R "$source_owner" lua/locale' EXIT
    version="$(grep -oP 'version = "\K[^"]+' lua/_meta.lua)"
    mkdir -p lua/locale
    xgettext --language=Lua --from-code=UTF-8 \
      --keyword=_ --keyword=deps._ --keyword=N_:1,2 --keyword=deps.N_:1,2 \
      --add-comments=@translators \
      --package-name=localsend --package-version="$version" \
      --msgid-bugs-address=https://github.com/kaikozlov/localsend.koplugin/issues \
      -o lua/locale/localsend.pot lua/*.lua
    echo "Wrote lua/locale/localsend.pot"

    shopt -s nullglob
    catalogues=(lua/locale/*.po)
    for catalogue in "${catalogues[@]}"; do
        echo "Merging lua/locale/localsend.pot into $catalogue"
        msgmerge --quiet --update --backup=none "$catalogue" lua/locale/localsend.pot
    done

# Validate syntax and compare each catalogue with a temporary deterministic
# msgmerge result. This checks msgid coverage, obsolete entries, ordering, and
# source references without enforcing translation completeness. Ignore volatile
# gettext date headers so a version-only POT regeneration remains reproducible.
# Check every .po is synchronized with lua/locale/localsend.pot.
[group('lint')]
i18n-check:
    {{ _run }} just --justfile /opt/plugin/justfile _i18n-check

[private]
_i18n-check:
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob

    template="lua/locale/localsend.pot"
    catalogues=(lua/locale/*.po)
    if (( ${#catalogues[@]} == 0 )); then
        echo "No translation catalogues to validate"
        exit 0
    fi

    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    failed=0

    strip_volatile() {
        grep -v -E '^"(POT-Creation-Date|PO-Revision-Date):' "$1"
    }

    for catalogue in "${catalogues[@]}"; do
        echo "Checking $catalogue"
        msgfmt -o /dev/null "$catalogue"

        merged="$tmp/$(basename "$catalogue")"
        msgmerge --quiet -o "$merged" "$catalogue" "$template"
        if ! diff -u --label "$catalogue" --label "$catalogue (merged with localsend.pot)" \
                <(strip_volatile "$catalogue") <(strip_volatile "$merged"); then
            failed=1
        fi
    done

    if (( failed )); then
        echo >&2
        echo "Translation catalogues are out of sync with $template." >&2
        echo "Run: just pot" >&2
        exit 1
    fi

# Fail if lua/locale/localsend.pot is out of sync with the translatable strings
# (_() / deps._() / N_() / deps.N_()) in lua/*.lua. Regenerates the template to a
# temp file, strips the volatile POT-Creation-Date / PO-Revision-Date headers
# (which xgettext re-stamps on every run), and diffs against the committed file.
# Deterministic: only real string changes cause a diff. Run `just pot` to fix.
[private]
pot-check:
    {{ _run }} just --justfile /opt/plugin/justfile _pot-check

[private]
_pot-check:
    #!/usr/bin/env bash
    set -euo pipefail

    version="$(grep -oP 'version = "\K[^"]+' lua/_meta.lua)"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT

    xgettext --language=Lua --from-code=UTF-8 \
      --keyword=_ --keyword=deps._ --keyword=N_:1,2 --keyword=deps.N_:1,2 \
      --add-comments=@translators \
      --package-name=localsend --package-version="$version" \
      --msgid-bugs-address=https://github.com/kaikozlov/localsend.koplugin/issues \
      -o "$tmp/localsend.pot" lua/*.lua

    # xgettext rewrites POT-Creation-Date (and the template's PO-Revision-Date)
    # on every invocation, so the committed file would otherwise always appear
    # changed. Drop both headers before diffing so only real string changes are
    # reported.
    strip_volatile() {
        grep -v -E '^"(POT-Creation-Date|PO-Revision-Date):' "$1"
    }

    if ! diff -u --label lua/locale/localsend.pot --label "regenerated (uncommitted)" \
            <(strip_volatile lua/locale/localsend.pot) \
            <(strip_volatile "$tmp/localsend.pot"); then
        echo >&2
        echo "lua/locale/localsend.pot is out of sync with the translatable" >&2
        echo "strings in lua/*.lua (_() / deps._() / N_() / deps.N_())." >&2
        echo >&2
        echo "Run: just pot" >&2
        exit 1
    fi
    echo "localsend.pot is in sync with source"

# Run the deterministic QEMU/seccomp audit against both packaged 32-bit ARM binaries.
# Requires a completed release build so the exact shipped binaries are exercised.
[group('test')]
test-armcompat:
    {{ _run }} env GOFLAGS=-buildvcs=false bash /opt/plugin/tools/armcompat/audit.sh

# Build the exact release packages and audit both legacy 32-bit ARM binaries.
[group('build')]
package:
    {{ _run }} {{ _reenter }} _package

[private]
_package: release test-armcompat

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
    # Translation catalogues: localsend_i18n.lua loads locale/<lang>.po at runtime.
    cp -r lua/locale "$stage/"
    cp LICENSE "$stage/"

    if ! $package_only; then
        echo "Cross-compiling ARM binaries..."
        # Amazon's legacy Kindle kernels omit modern ARM epoll, eventfd,
        # accept, pipe, and dup variants. Build 32-bit packages with a scoped
        # GOROOT overlay that restores the corresponding legacy syscall paths.
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
