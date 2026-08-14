#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
official_ref="${OFFICIAL_LOCALSEND_REF:-af3aad33c965defc39ecff8d9a4396a851ce3cc1}"
rust_image="${OFFICIAL_RUST_IMAGE:-rust:1.97.0-bookworm}"
dev_image="${KOPLUGIN_DEV_IMAGE:-ghcr.io/kaikozlov/koplugin-dev:v2026.07.1_1}"
temporary_checkout=""
binary_volume=""

cache_source() {
    local override="$1"
    local fallback="$2"
    if [[ -z "$override" ]]; then
        printf '%s\n' "$fallback"
        return
    fi
    mkdir -p "$override"
    (cd "$override" && pwd)
}

cargo_registry_source="$(cache_source "${OFFICIAL_INTEROP_CARGO_REGISTRY_DIR:-}" localsend-official-cargo-registry)"
cargo_git_source="$(cache_source "${OFFICIAL_INTEROP_CARGO_GIT_DIR:-}" localsend-official-cargo-git)"
cargo_target_source="$(cache_source "${OFFICIAL_INTEROP_CARGO_TARGET_DIR:-}" localsend-official-cargo-target)"

cleanup() {
    if [[ -n "$binary_volume" ]]; then
        docker volume rm -f "$binary_volume" >/dev/null 2>&1 || true
    fi
    if [[ -n "$temporary_checkout" ]]; then
        rm -rf "$temporary_checkout"
    fi
}
trap cleanup EXIT

if [[ -n "${OFFICIAL_LOCALSEND_DIR:-}" ]]; then
    official_dir="$OFFICIAL_LOCALSEND_DIR"
else
    temporary_checkout="$(mktemp -d)"
    official_dir="$temporary_checkout/localsend"
    echo "Fetching LocalSend official core at $official_ref"
    git init -q "$official_dir"
    git -C "$official_dir" remote add origin https://github.com/localsend/localsend.git
    git -C "$official_dir" config core.sparseCheckout true
    mkdir -p "$official_dir/.git/info"
    printf '/packages/core/\n' >"$official_dir/.git/info/sparse-checkout"
    git -C "$official_dir" fetch -q --depth=1 origin "$official_ref"
    git -C "$official_dir" checkout -q --detach FETCH_HEAD
fi

official_dir="$(cd "$official_dir" && pwd)"
if [[ ! -f "$official_dir/packages/core/Cargo.toml" ]]; then
    echo "Official LocalSend core not found under $official_dir/packages/core" >&2
    exit 1
fi
if git -C "$official_dir" rev-parse HEAD >/dev/null 2>&1; then
    actual_ref="$(git -C "$official_dir" rev-parse HEAD)"
    echo "Official LocalSend: $actual_ref"
fi

binary_volume="localsend-official-interop-bin-$$"
docker volume create "$binary_volume" >/dev/null

echo "Building Go implementation in $dev_image"
docker run --rm \
    -v "$root:/opt/plugin:ro" \
    -v "$binary_volume:/opt/interop" \
    -v localsend-go-mod:/root/go/pkg/mod \
    -v localsend-go-build:/root/.cache/go-build \
    "$dev_image" \
    sh -c 'cd /opt/plugin && GOFLAGS=-buildvcs=false CGO_ENABLED=0 go build -o /opt/interop/localsend .'

echo "Running official-core interoperability tests in $rust_image"
docker run --rm \
    -e LOCALSEND_BIN=/opt/interop/localsend \
    -v "$root:/opt/plugin:ro" \
    -v "$official_dir/packages/core:/opt/official-core:ro" \
    -v "$binary_volume:/opt/interop:ro" \
    -v "$cargo_registry_source:/usr/local/cargo/registry" \
    -v "$cargo_git_source:/usr/local/cargo/git" \
    -v "$cargo_target_source:/opt/cargo-target" \
    -w /opt/plugin/interop/official \
    "$rust_image" \
    sh -c '
        rustup component add rustfmt >/dev/null 2>&1 &&
        cargo fmt --check &&
        exec cargo test --locked --target-dir /opt/cargo-target "$@" -- --test-threads=1
    ' interop-test "$@"
