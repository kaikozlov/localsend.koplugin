#!/usr/bin/env bash
# Deterministic legacy-Kindle compatibility audit for both packaged 32-bit ARM binaries.
# Runs entirely on Docker's isolated loopback network; no Internet is required.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
legacy_archive="${1:-$repo_dir/build/localsend-koplugin-arm-legacy.zip}"
armv7_archive="${2:-$repo_dir/build/localsend-koplugin-armv7.zip}"

for release_archive in "$legacy_archive" "$armv7_archive"; do
    if [[ ! -f "$release_archive" ]]; then
        echo "armcompat audit: release archive not found: $release_archive" >&2
        echo "run 'just release' first" >&2
        exit 1
    fi
done

for command in cc cmp curl go qemu-arm rg unzip; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "armcompat audit: required command not found: $command" >&2
        exit 1
    fi
done

work_dir="$(mktemp -d)"
server_pid=""
cleanup() {
    if [[ -n "$server_pid" ]]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    rm -rf "$work_dir"
}
trap cleanup EXIT

cd "$repo_dir"

# Prove that the overlay itself fails closed if it is ever passed to a non-ARM
# build. This catches accidental future reuse on the arm64 release command.
overlay="$(go run -buildvcs=false ./tools/armcompat -output-dir "$work_dir/overlay")"
if CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -buildvcs=false -overlay="$overlay" -o "$work_dir/invalid-arm64" . \
    >"$work_dir/guard.out" 2>&1; then
    echo "armcompat audit: ARM overlay unexpectedly compiled for GOARCH=arm64" >&2
    exit 1
fi
if ! rg -q "armcompatOverlayRequiresGOARCHArm" "$work_dir/guard.out"; then
    echo "armcompat audit: non-ARM build failed for an unexpected reason" >&2
    cat "$work_dir/guard.out" >&2
    exit 1
fi

host_binary="$work_dir/localsend-host"
fault_launcher="$work_dir/kindle-enosys"
trace_checker="$work_dir/tracecheck"
go build -buildvcs=false -o "$host_binary" .
go build -buildvcs=false -o "$trace_checker" ./tools/armcompat/tracecheck
cc -O2 -Wall -Wextra -o "$fault_launcher" \
    tools/armcompat/testdata/kindle_enosys.c

audit_archive() {
    local release_archive="$1"
    local arch="$2"
    local case_dir="$work_dir/$arch"
    local release_binary="$case_dir/localsend-$arch"
    local trace="$case_dir/receiver.trace"
    local version_output
    local trace_error
    local ready

    mkdir -p "$case_dir/received" "$case_dir/config"
    if ! unzip -p "$release_archive" localsend.koplugin/localsend >"$release_binary"; then
        echo "armcompat audit ($arch): could not extract the packaged binary" >&2
        exit 1
    fi
    chmod +x "$release_binary"

    version_output="$(qemu-arm -r 2.6.22.19-lab126 "$release_binary" --version)"
    if [[ "$version_output" != *"linux/$arch"* ]]; then
        echo "armcompat audit ($arch): unexpected release identity: $version_output" >&2
        exit 1
    fi

    printf 'legacy kernel transfer audit (%s)\n' "$arch" >"$case_dir/input.txt"

    "$fault_launcher" \
        qemu-arm -r 2.6.22.19-lab126 -strace \
        "$release_binary" recv \
        --https=false --webrtc=false \
        --devname "AuditKindle-$arch" \
        --dir "$case_dir/received" \
        --config-dir "$case_dir/config" \
        --on-transfer "printf callback-ok > '$case_dir/callback.txt'" \
        >"$case_dir/receiver.out" 2>"$trace" &
    server_pid=$!

    ready=false
    for _ in $(seq 1 100); do
        if curl -fsS --max-time 1 \
            http://127.0.0.1:53317/api/localsend/v2/info \
            >"$case_dir/info.json" 2>/dev/null; then
            ready=true
            break
        fi
        sleep 0.05
    done
    if [[ "$ready" != true ]]; then
        echo "armcompat audit ($arch): receiver did not become ready" >&2
        tail -80 "$case_dir/receiver.out" >&2 || true
        tail -80 "$trace" >&2 || true
        exit 1
    fi

    # Exercise repeated incoming accepts before the complete transfer.
    for _ in 1 2 3; do
        curl -fsS --max-time 2 \
            http://127.0.0.1:53317/api/localsend/v2/info >/dev/null
    done

    "$host_binary" send \
        --https=false --ip 127.0.0.1 --devname AuditSender \
        "$case_dir/input.txt" >/dev/null

    if ! cmp -s "$case_dir/input.txt" "$case_dir/received/input.txt"; then
        echo "armcompat audit ($arch): transferred file differs from input" >&2
        exit 1
    fi

    for _ in $(seq 1 100); do
        if [[ -f "$case_dir/callback.txt" ]]; then
            break
        fi
        sleep 0.05
    done
    if [[ "$(cat "$case_dir/callback.txt" 2>/dev/null || true)" != "callback-ok" ]]; then
        echo "armcompat audit ($arch): on-transfer callback did not run" >&2
        tail -80 "$case_dir/receiver.out" >&2 || true
        tail -80 "$trace" >&2 || true
        exit 1
    fi

    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
    server_pid=""

    # QEMU writes syscall entries and results separately, so concurrent guest
    # threads can split an ENOSYS result away from its syscall line. Validate
    # each modern-probe/legacy-fallback pair in stream order instead of
    # assuming that an individual trace line is atomic.
    if ! trace_error="$("$trace_checker" "$trace" 2>&1)"; then
        echo "armcompat audit ($arch): ${trace_error#tracecheck: }" >&2
        exit 1
    fi

    printf 'ARM compatibility audit passed (%s): %s\n' "$arch" "$version_output"
}

audit_archive "$legacy_archive" arm-legacy
audit_archive "$armv7_archive" armv7
