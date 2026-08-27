# AGENTS.md

## Project Overview

LocalSend CLI: Go implementation of LocalSend protocol (AirDrop alternative).
- **KOReader Plugin** (`lua/`): File receiver for e-readers (Kindle, Kobo, reMarkable)
- **Standalone CLI**: Command-line tool for sending/receiving files

## Build Commands

```bash
go build -o localsend                    # Local build
just release                            # Cross-compile ARM + package release zips
just release -p                         # Package only (reuse binaries)

# Manual cross-compilation
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags="-s -w" -o localsend  # armv7
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o localsend        # arm64
```

## Test Commands

### Docker-only (uses real KOReader runtime)

All project test entry points run inside the `koplugin-dev` Docker image. Do not
run host `go test` or host `busted`; that bypasses the KOReader runtime and lets
machine-specific toolchains hide bugs.

```bash
just setup              # One-time: install hooks + pull koplugin-dev image
just verify             # Definitive read-only fmt + lint + i18n + test pass (pre-push/CI)
just verify-static      # Read-only fmt + lint + i18n pass (pre-commit)
just test               # All Lua + Go tests (quiet; failures + summaries)
V=1 just test           # Same, with full busted/go -v output
just test-filter "pattern"  # Focused Lua run in Docker
just test-go            # Go tests in Docker
just test-go-race       # Go tests with race detector in Docker
just test-go-integration # Go integration tests in Docker
just test-official      # Cross-test Go with pinned official LocalSend Rust core
just bench-transfer     # Transfer/file-I/O microbenchmarks in Docker
just test-stress        # Opt-in 1000 x 1 MiB HTTP receive stress test
just test-web           # Verify pinned LocalSend Web source contract + WebRTC compatibility tests
just test-armcompat     # QEMU/seccomp audit of packaged legacy ARM binary
just lint               # luacheck + golangci-lint in Docker
just fmt                # stylua + go fmt in Docker
just check              # Mutating fmt + lint + test pass in one container
just package            # Build release zips + run the QEMU ARM audit
just shell              # Interactive container shell
just                    # List all recipes
```

Image: `ghcr.io/kaikozlov/koplugin-dev` — contains real KOReader Linux runtime and QEMU user-mode tooling.
Bump `koplugin_dev_version` in `justfile` when the image updates.

`just test-official` additionally uses `rust:1.97.0-bookworm` and sparse-fetches
the pinned `OFFICIAL_LOCALSEND_REF` into a temporary checkout; see
`interop/official/README.md`.

Shared recipes are vendored at `just/shared.just` (from koplugin-dev). Refresh with
`just sync-shared` when upstream recipes change, then commit the file.
Product packaging stays local: `just release`.

## Architecture

```
cmd/{recv,send,scan}/       # CLI commands (Cobra)

internal/
├── localsend/              # V2 protocol: recv/, send/, session/, constants/
├── webrtc/                 # V3 protocol: signaling/, transfer/
├── crypto/                 # token.go (Ed25519/RSA-PSS), nonce.go, cert.go
├── models/                 # DeviceInfo, FileMeta, Discovery
├── storage/                # TrustedDeviceStore (PAIR persistence)
└── utils/                  # Path sanitization, extension parsing

lua/
├── main.lua                # Entry, menu, lifecycle
├── localsend_state.lua     # ServerState (session-level state)
├── localsend_server.lua    # Process management
├── localsend_firewall.lua  # iptables rules when binary available, else no-op
├── localsend_update.lua    # OTA updates
└── spec/                   # Tests (busted)
```

## Protocol

- **V2 / native LocalSend 1.18.x**: protocol 2.2 over HTTP(S) + UDP multicast discovery (224.0.0.167:53317)
- **WebRTC / LocalSend Web**: protocol 2.3 signaling + WebRTC, pinned by the interop harness to `ea5d55d34db2f21b84bf0ffe39d6342013b4ecd8`; `REFERENCE/` is optional analysis material, never a test/runtime dependency
- Native 1.18.x currently disables its WebRTC path; that does **not** make WebRTC optional for this project because LocalSend Web requires it
- **PAIR**: Ed25519 key exchange, skips PIN for trusted devices
- **Security**: TLS, PIN with rate limiting, nonce replay protection, constant-time compare

## KOReader Plugin Lifecycle (CRITICAL)

**This bug has occurred 3 times. Be vigilant.**

`init()` runs on EVERY widget recreation:
- Opening different book
- Switching file manager ↔ reader view
- Some suspend/resume scenarios

**DON'T** put side effects in `init()` without guards:
```lua
-- BAD: WiFi prompt on every book open
NetworkMgr:runWhenConnected(function() ... end)
-- BAD: Dialog/notification on every book open
UIManager:show(InfoMessage:new{ ... })
```

**DO** use `ServerState` flags in `localsend_state.lua`:
```lua
if not ServerState.some_action_attempted then
    ServerState.some_action_attempted = true
    NetworkMgr:runWhenConnected(function() ... end)
end
```

State lifetimes:
- `self.*` → widget instance (destroyed on book change)
- `ServerState.*` → KOReader session (persists across widgets)
- `G_reader_settings` → persistent storage

## Security

- **Path traversal**: `utils.SanitizeRelativePath()` for untrusted filenames
- **PIN**: constant-time compare (`crypto/subtle`), rate limiting per IP
- **Tokens**: nonce-bound, 1hr expiry
- **Shell**: `shell_escape()` in Lua for user input

## Go Patterns

**sync.Once for channel close** (prevents double-close panic):
```go
closeOnce sync.Once
func (c *Client) Close() { c.closeOnce.Do(func() { close(c.done) }) }
```
Used in: SignalingClient, RTCSender, Discoverer

**64-bit alignment on 32-bit ARM**: int64 fields using atomic ops must be first in struct:
```go
type RecvSession struct {
    filesCount int64  // Must be first for ARM alignment
    // ...
}
```

**Mutex before callback**: Release lock before calling user callbacks to prevent deadlock.

## Protocol Interop

- **Nonce order**: `sender_nonce || receiver_nonce` (order matters!)
- **Base64**: URL-safe, no padding (`base64.RawURLEncoding`)
- **DeviceType**: V2 lowercase; current official V3 HTTP and WebRTC signaling sources serialize `SCREAMING_SNAKE_CASE`

## Kindle-Specific

- **Firewall**: `localsend_firewall.lua` manages iptables rules whenever the `iptables` binary is available (gated on `command -v iptables`, not device type) — otherwise it no-ops
- **Telemetry**: `fm-out-*` files fill 64MB tmpfs; cleared via `clearTmpTelemetryFiles()`

## Testing

Lua tests run inside the koplugin-dev Docker container against a **real, headless
KOReader**. No whole KOReader module is stubbed — specs `require()` the real
`UIManager`, `Device`, `util`, `NetworkMgr`, widgets, `G_reader_settings`, etc.
directly. Where a boundary can't be reproduced on the real filesystem (offline
network, a broken binary, a malformed transfer log), specs wrap the specific
function with save/restore rather than stubbing a whole module.

`lua/spec/spec_helper.lua` is the single shared helper. It provides:
- `setup_complete()` / `before_each()` — materialises the installed plugin layout
  (binary shim + isolated settings) so `require("main")` loads exactly as on-device,
  and installs transparent **spies** (call-through) on `UIManager.show/close/scheduleIn`,
  `os.execute`, `os.remove`, `util.removeFile`, and `ffi/util.purgeDir`.
- `create_instance()` — a real plugin instance against live modules.
- `load_via_filemanager()` — load through the real `PluginLoader` + `FileManager`.
- `state.*` capture tables and `find_notification` / `find_dialog` / `find_execute_call`.

`os.execute` is the only spy that does **not** call through (it is captured, not run,
so tests never launch the receiver or touch iptables).

Available globals from `commonrequire.lua` (container environment):
```lua
load_plugin("localsend.koplugin") -- Load plugin via real PluginLoader
fastforward_ui_events()           -- Run scheduled UI tasks immediately
disable_plugins()                 -- Clear all plugins for isolated testing
get_test_data_dir()               -- Isolated temp directory
get_plugin_path()                 -- Path to plugin source under test
```

Go integration tests: `//go:build integration` tag, run with `-tags=integration` via justfile recipes.

## Writing Tests

**Tests must verify behavior, not just exist.** Avoid test slop.

Good test:
```go
func TestPINRateLimit_BlocksAfterThreeAttempts(t *testing.T) {
    recv := NewReceiver()
    for i := 0; i < 3; i++ {
        recv.CheckPIN("wrong")
    }
    assert.True(t, recv.IsBlocked("127.0.0.1"))  // Specific assertion
}
```

Bad test:
```go
func TestReceiver(t *testing.T) {
    recv := NewReceiver()
    recv.CheckPIN("1234")  // No assertion - what is this testing?
}
```

Principles:
- **Test name = expected behavior**: `TestX_DoesY_WhenZ`
- **One behavior per test**: Split multiple assertions into separate tests if testing different behaviors
- **Assert outcomes, not implementation**: Test what it does, not how
- **Edge cases over happy paths**: Empty input, nil, boundaries, error conditions
- **No sleep-based synchronization**: Use channels, waitgroups, or polling with timeout

When fixing bugs: write failing test first, then fix. Prevents regression.
