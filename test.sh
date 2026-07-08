#!/bin/bash
# Docker-only test entry point.
#
# Local host test runs are intentionally not supported; the Lua plugin must run
# against the real KOReader runtime provided by the koplugin-dev container, and
# Go tests use the same containerized toolchain for consistency with CI.

set -euo pipefail

for arg in "$@"; do
    case "$arg" in
        --verbose|-v)
            # make/docker already stream full output.
            ;;
        *)
            echo "Unsupported argument: $arg" >&2
            echo "Usage: ./test.sh [--verbose]" >&2
            echo "For focused runs use Makefile targets, e.g. make test-lua-filter FILTER='pattern'." >&2
            exit 2
            ;;
    esac
done

exec make test
