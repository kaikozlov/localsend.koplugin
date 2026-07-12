#!/bin/bash
# Docker-only test entry point.
#
# Local host test runs are intentionally not supported; the Lua plugin must run
# against the real KOReader runtime provided by the koplugin-dev container, and
# Go tests use the same containerized toolchain for consistency with CI.
#
# Quiet by default (failures + summaries). Pass --verbose / -v for full output.

set -euo pipefail

V=0
for arg in "$@"; do
    case "$arg" in
        --verbose|-v)
            V=1
            ;;
        -h|--help)
            echo "Usage: ./test.sh [--verbose|-v]"
            echo "  (default) quiet — failures and summaries only"
            echo "  -v        verbose — full busted/go test output"
            echo "Focused runs: just test-lua-filter 'pattern'  (V=1 for verbose)"
            exit 0
            ;;
        *)
            echo "Unsupported argument: $arg" >&2
            echo "Usage: ./test.sh [--verbose|-v]" >&2
            echo "Focused runs: just test-lua-filter 'pattern'  (V=1 for verbose)" >&2
            exit 2
            ;;
    esac
done

exec env V="$V" just test
