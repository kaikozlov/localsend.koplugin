# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LocalSend CLI: Go implementation of LocalSend protocol (AirDrop alternative).
- **KOReader Plugin** (`lua/`): File receiver for e-readers (Kindle, Kobo, reMarkable)
- **Standalone CLI**: Command-line tool for sending/receiving files

## Build Commands

```bash
go build -o localsend                    # Local build
./arm_build.sh                           # Cross-compile ARM + package
./arm_build.sh --package                 # Package only (reuse binaries)

# Manual cross-compilation
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags="-s -w" -o localsend  # armv7
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o localsend        # arm64
```

## Test Commands

### Docker-based (recommended, uses real KOReader runtime)

```bash
make setup              # One-time: pull koplugin-dev image
make test               # All Lua + Go tests
make test-lua           # Lua tests via busted-koreader (real KOReader)
make test-go            # Go tests
make test-go-race       # Go tests with race detector
make test-go-integration # Go integration tests
make lint               # luacheck + golangci-lint
make fmt                # stylua + go fmt
make shell              # Interactive container shell
make help               # List all targets
```

Image: `ghcr.io/kaikozlov/koplugin-dev:v2026.03_1` — contains real KOReader Linux runtime.
Bump `KOPLUGIN_DEV_VERSION` in Makefile when the image updates.

### Local (no Docker required)

```bash
./test.sh                    # All tests (Go + Lua) with race detector
./test.sh --verbose          # Show all output

go test ./... -race -count=1                                    # Go unit tests
go test ./internal/localsend/... -tags=integration -count=1    # Go integration tests
go test ./internal/localsend/recv -run TestName -count=1       # Single test

cd lua && busted spec/                  # Lua tests (requires local Lua/busted)
cd lua && busted spec/some_spec.lua     # Single Lua test
```

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
├── localsend_firewall.lua  # Kindle iptables (no-op on other devices)
├── localsend_update.lua    # OTA updates
└── spec/                   # Tests (busted)
```

## Protocol

- **V2**: HTTP(S) + UDP multicast discovery (224.0.0.167:53317)
- **V3**: WebRTC via signaling server for NAT traversal
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

- **Firewall**: `localsend_firewall.lua` manages iptables (no-op elsewhere)
- **Telemetry**: `fm-out-*` files fill 64MB tmpfs; cleared via `clearTmpTelemetryFiles()`

## Testing

Lua tests run inside the koplugin-dev Docker container against a real KOReader runtime.
Tests currently use `lua/spec/test_helper.lua` which mocks KOReader deps — this will be
gradually migrated to use real modules as the test infrastructure matures.

```lua
helper.setup_complete()      -- Mock all KOReader deps
helper.create_instance()     -- Create plugin instance
```

Available helpers from `commonrequire.lua` (container environment):
```lua
load_plugin("localsend")     -- Load plugin via real PluginLoader
fastforward_ui_events()      -- Run scheduled UI tasks immediately
disable_plugins()            -- Clear all plugins for isolated testing
get_test_data_dir()          -- Isolated temp directory
get_plugin_path()            -- Path to plugin under test
```

Go integration tests: `//go:build integration` tag, run with `-tags=integration`

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
