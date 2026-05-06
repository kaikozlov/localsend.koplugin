# Code Review Worklog (2026-03-07)

This file started as a checkpoint document for the in-progress comprehensive code review and now also includes the finalized review report.

## Scope

- Repository: `localsend.koplugin`
- Components reviewed so far:
  - Go LocalSend CLI / protocol implementation
  - Lua KOReader plugin
  - Protocol compatibility against `OFFICIAL_LOCALSEND/`
  - KOReader API/lifecycle compatibility against `OFFICIAL_KOREADER/`

## Review Workflow Completed So Far

1. Read context:
   - `CLAUDE.md`
   - `docs/localsend_protocol_v3.md`
   - `docs/code_review_findings.md`
2. Ran parallel discovery passes for:
   - Go resource lifecycle
   - Go memory / bounded growth
   - Go security / crypto / path handling
   - Lua lifecycle / shell execution / state handling
   - Protocol interop
3. Verified candidate findings by reading full files and related tests.
4. Ran the main Go and Lua test suites.

## Commands Run

Context / discovery:

```bash
rg --files docs
rg -n "go func|make\\(chan|chan " internal cmd
rg -n "map\\[|make\\(map|\\[\\]|make\\(\\[\\]" internal lua
rg -n "os\\.Execute|os\\.execute|io\\.open|shell_escape|init\\(" lua
rg -n "nonce|token|register|prepare-upload|infoV3|DeviceType|base64" internal docs OFFICIAL_LOCALSEND
```

Tests:

```bash
go test ./... -race -count=1
cd lua && busted spec/
go test ./... -list '^(Test|Example)' 2>/dev/null | wc -l
find lua/spec -type f -name '*_spec.lua' | wc -l
```

## Test Results

- `go test ./... -race -count=1`: passed
- `cd lua && busted spec/`: passed
  - Result observed: `508 successes / 0 failures / 0 errors / 0 pending`
- Go test inventory:
  - `437` tests/examples listed
- Lua spec inventory:
  - `25` spec files

## Verified Findings

### 1. HTTP v3 responses are initialized with legacy identity fields

Status: verified

Evidence:

- `internal/localsend/recv/recv.go:72-84`
  - `NewFileReceiver()` initializes `identity` with `models.NewDeviceInfo(...)`.
- `internal/models/discovery.go:35-46`
  - `NewDeviceInfo()` returns `Version: "2.1"` and populates `Fingerprint`, not `Token`.
- `internal/localsend/recv/handlers.go:284-291`
  - `registerV3Handler()` returns `fr.identity.Version` and `fr.identity.Token`.
- `internal/localsend/recv/handlers.go:364-370`
  - `infoV3Handler()` does the same.

Impact:

- `/api/localsend/v3/register` and `/api/localsend/v3/info` can return a V2-style version and an empty token.
- This is a protocol interop bug for v3 HTTP peers.

Why tests did not catch it:

- `internal/localsend/recv/handlers_test.go:21-36`
  - `newTestReceiver()` bypasses `NewFileReceiver()` and hardcodes `Version: "2.3"` and `Token: "test-token"`.

Expected review classification:

- Severity: Medium
- Confidence: 100
- Category: Protocol / Interop

### 2. HTTP v3 authentication is reduced to nonce exchange by IP, without sender token or client-certificate verification

Status: verified

Evidence in this repository:

- `internal/localsend/recv/handlers.go:243-257`
  - v3 nonce exchange stores state under `clientID := c.IP()`.
- `internal/localsend/recv/handlers.go:316-332`
  - `preUploadV3Handler()` only checks whether both nonces exist; it does not verify `metaReq.Info.Token`.
- `internal/localsend/utils/utils.go:233-239`
  - TLS listener uses `server.ListenTLSWithCertificate(...)`; there is no client-auth / mTLS configuration.

Cross-check against official implementation:

- `OFFICIAL_LOCALSEND/core/src/http/client/v3.rs:25-158`
  - official HTTP client attaches a client identity certificate.
- `OFFICIAL_LOCALSEND/core/src/http/server/mod.rs:195-200`
  - official HTTP server configures `with_client_cert_verifier(...)`.
- `OFFICIAL_LOCALSEND/core/src/http/server/mod.rs:213-230`
  - official request identity is derived from the client certificate public key when available.

Impact:

- The current v3 HTTP implementation effectively authenticates "same IP completed nonce exchange" rather than "authenticated peer identity".
- This weakens the security model and risks interop divergence from official LocalSend v3 HTTP behavior.

Expected review classification:

- Severity: High
- Confidence: 100
- Category: Security / Protocol

### 3. WebRTC receiver logs file write failures but can still acknowledge success

Status: verified

Evidence:

- `internal/webrtc/transfer/receiver.go:975-989`
  - `handleBinaryData()` logs `Failed to write data` but does not mark the file/session failed.
- `internal/webrtc/transfer/receiver.go:998-1056`
  - `finishCurrentFile()` starts with `success := true` and only flips to failure for checksum mismatch.
  - If the write failed and no checksum is provided, the receiver can still send `Success: true`.

Impact:

- On storage-constrained e-readers, disk-full or I/O failures can produce truncated files that are reported as successful transfers.
- This is a real data-loss / integrity bug.

Test gap:

- `internal/webrtc/transfer/receiver_security_test.go:1263-1300`
  - the checksum mismatch test simulates deletion directly and does not execute the actual receiver write-failure path.

Expected review classification:

- Severity: High
- Confidence: 100
- Category: Data Integrity / Error Handling

### 4. WebRTC sender never handles `RTCSendFileResponse`, so per-file receiver failures are invisible

Status: verified

Evidence:

- `internal/webrtc/transfer/rtc_protocol.go:145-148`
  - protocol parser already recognizes `file_response`.
- `internal/webrtc/transfer/sender.go:323-330`
  - `handleMessage()` has no branch for sender state while files are being sent, so `file_response` is ignored.
- `internal/webrtc/transfer/sender.go:651-725`
  - `SendFiles()` streams all files, sends the final delimiter, waits only for buffer drain, then returns success.

Cross-check against spec / official implementation:

- `docs/localsend_protocol_v3.md:1476-1496`
  - both documented flows wait for `RTCSendFileResponse`.
- `OFFICIAL_LOCALSEND/web/app/services/webrtc.ts:246-286`
  - official pipelined sender still waits for each file status.

Impact:

- Even if the receiver correctly reports a failed file, the sender will not surface it.
- Combined with the receiver-side bug above, this makes transfer failure reporting particularly unreliable.

Expected review classification:

- Severity: High
- Confidence: 100
- Category: Protocol / Error Handling

## Additional Verified Finding

### 5. WebRTC blocked-peer tracking has no global cleanup path

Status: verified

Evidence:

- `internal/webrtc/transfer/receiver.go:43-49`
  - package-level `blockedPeers` map persists across receiver instances.
- `internal/webrtc/transfer/receiver.go:107-142`
  - expired entries are only deleted when `isPeerBlocked(peerID)` is called for that exact same key.
- `internal/webrtc/transfer/receiver.go:392-397`
  - block checks occur only on reconnect for the offered signaling ID.
- `internal/webrtc/transfer/receiver.go:689-694`
  - entries are inserted after max PIN failures.
- `internal/localsend/constants/security.go:11-16`
  - HTTP path has an explicit cleanup interval constant, but WebRTC blocked peers do not use any periodic cleanup.
- `internal/localsend/recv/recv.go:375-376`
  - HTTP receiver starts a cleanup goroutine for PIN attempts.
- `internal/utils/ratelimit.go:98-126`
  - shared rate-limiter implementation already has an explicit `CleanupExpired()` path.

Impact:

- Repeated one-off signaling IDs can leave stale entries in memory indefinitely.
- On a desktop this would be minor; on 256-512 MB e-readers, it is avoidable state growth in a hot security path.

Expected review classification:

- Severity: Medium
- Confidence: 75
- Category: Resource Management / Security

## Strengths Already Confirmed

- Bounded nonce cache implementation:
  - `internal/localsend/nonce_cache.go`
- Bounded signaling-side request tracking:
  - `internal/webrtc/signaling/client.go`
- 32-bit ARM atomic alignment handled explicitly:
  - `internal/localsend/session/recv.go`
- Path sanitization / file creation paths look deliberate:
  - `internal/webrtc/transfer/receiver.go`
  - `internal/localsend/session/recv.go`
- Lua `init()` lifecycle guards appear careful and aligned with `CLAUDE.md` guidance:
  - `lua/main.lua`

## Completion Status

The review is complete. The finalized report provided to the user is preserved below.

## Final Test Coverage Notes To Carry Into The Review

- `internal/localsend/recv/handlers_test.go`
  - v3 handler tests use `newTestReceiver()` with a hand-built identity, so they do not verify the real `NewFileReceiver()` initialization path.
- `internal/localsend/recv/handlers_test.go`
  - no test currently asserts that `prepare-upload` rejects a missing or invalid v3 sender token.
- `internal/webrtc/transfer/receiver_security_test.go`
  - checksum tests do not exercise actual write-failure handling in `handleBinaryData()` / `finishCurrentFile()`.
- `internal/webrtc/transfer/*_test.go`
  - no test currently verifies that `RTCSender.SendFiles()` waits for and propagates `RTCSendFileResponse`.
- `cmd/`, `cmd/recv/`, `cmd/scan/`, `cmd/send/`
  - no direct package tests; behavior is covered indirectly by lower-level and integration tests, but CLI flag wiring remains lightly tested.

## Final Review Report

### High Priority Issues

#### HTTP v3 authentication is not actually verifying peer identity

**Location:** `internal/localsend/recv/handlers.go:243`, `internal/localsend/recv/handlers.go:316`, `internal/localsend/utils/utils.go:233`, `OFFICIAL_LOCALSEND/core/src/http/server/mod.rs:195`, `OFFICIAL_LOCALSEND/core/src/http/client/v3.rs:25`  
**Severity:** High  
**Confidence:** 100  
**Category:** Security / Protocol  

**Problem:** v3 nonce state is keyed only by `c.IP()`, `prepare-upload` never verifies `metaReq.Info.Token`, and the HTTPS server does not request client certificates.  

**Impact:** Any peer on the same IP path that completes nonce exchange can be treated as authenticated, which is weaker than the documented and official v3 HTTP model.  

**Recommended Fix:** derive the client ID from the client cert public key when HTTPS is enabled, require client-auth TLS for v3 HTTP, and verify the sender token against the exchanged nonces before accepting `prepare-upload`.  

**Test Requirement:** Required. Add handler tests that reject missing/invalid v3 tokens and exercise certificate-backed identity.  

**Verification:** current code only checks nonce presence; official LocalSend uses a client identity cert and server-side client-cert verifier.

#### WebRTC receiver can report success after a file write failure

**Location:** `internal/webrtc/transfer/receiver.go:975`, `internal/webrtc/transfer/receiver.go:998`  
**Severity:** High  
**Confidence:** 100  
**Category:** Data Integrity / Error Handling  

**Problem:** `handleBinaryData()` logs `Write` errors but does not mark the file failed; `finishCurrentFile()` defaults to `success := true` and only flips on checksum mismatch.  

**Impact:** On nearly-full e-readers, a truncated file can still be acknowledged as a successful transfer.  

**Recommended Fix:** track per-file write/sync/close errors, delete the partial file, and send `RTCSendFileResponse{Success:false}` on any I/O failure.  

**Test Requirement:** Required. Add a failing test that injects a write error and asserts the receiver reports failure and removes the partial file.  

**Verification:** the only current checksum test is a simulation, not an execution of the real write-failure path: `internal/webrtc/transfer/receiver_security_test.go:1263`.

#### WebRTC sender ignores `RTCSendFileResponse`

**Location:** `internal/webrtc/transfer/rtc_protocol.go:145`, `internal/webrtc/transfer/sender.go:323`, `internal/webrtc/transfer/sender.go:651`, `docs/localsend_protocol_v3.md:1476`, `OFFICIAL_LOCALSEND/web/app/services/webrtc.ts:246`  
**Severity:** High  
**Confidence:** 100  
**Category:** Protocol / Error Handling  

**Problem:** the parser recognizes `file_response`, but `handleMessage()` has no sending-state branch for it, and `SendFiles()` returns after flush/delimiter alone.  

**Impact:** sender-side UX and automation can report success even when the receiver rejected or failed a file.  

**Recommended Fix:** add a sending state that waits for one `RTCSendFileResponse` per file and propagates failures back to the caller.  

**Test Requirement:** Required. Add a sender test that receives a negative `file_response` and asserts `SendFiles()` returns an error.  

**Verification:** spec and official sender both wait for file status; this implementation does not.

### Medium Priority Issues

#### v3 register/info responses are built from V2 identity defaults

**Location:** `internal/localsend/recv/recv.go:72`, `internal/models/discovery.go:35`, `internal/localsend/recv/handlers.go:284`, `internal/localsend/recv/handlers.go:364`, `internal/localsend/recv/handlers_test.go:21`  
**Severity:** Medium  
**Confidence:** 100  
**Category:** Protocol / Interop  

**Problem:** `NewFileReceiver()` initializes `identity` via `NewDeviceInfo()`, which sets `Version: "2.1"` and `Fingerprint`, not `Token`; v3 handlers then return those fields directly.  

**Impact:** `/api/localsend/v3/register` and `/api/localsend/v3/info` can advertise the wrong version and an empty token to official peers.  

**Recommended Fix:** initialize a dedicated v3 HTTP identity for these endpoints, or populate v3-only fields before handler registration.  

**Test Requirement:** Required. Build v3 handler tests from `NewFileReceiver()` rather than the hand-crafted test identity.  

**Verification:** existing tests mask the bug by hardcoding `Version: "2.3"` and `Token: "test-token"` in `newTestReceiver()`.

#### WebRTC blocked-peer tracking has no global cleanup

**Location:** `internal/webrtc/transfer/receiver.go:43`, `internal/webrtc/transfer/receiver.go:107`, `internal/webrtc/transfer/receiver.go:392`, `internal/webrtc/transfer/receiver.go:689`, `internal/localsend/recv/recv.go:375`, `internal/utils/ratelimit.go:98`  
**Severity:** Medium  
**Confidence:** 75  
**Category:** Resource Management / Security  

**Problem:** expired `blockedPeers` entries are only removed if the exact same signaling ID reconnects.  

**Impact:** repeated one-off IDs can leave stale entries in memory indefinitely, which is avoidable state growth on constrained devices.  

**Recommended Fix:** replace this map with the shared `RateLimiter`, or add periodic/global eviction similar to the HTTP PIN cleanup path.  

**Test Requirement:** Recommended. Add a cleanup test that proves expired entries are removed without requiring the same peer ID to reconnect.  

**Verification:** there is no background cleanup path for WebRTC, while the HTTP PIN limiter has one.

### Strengths

- `init()` side effects in the KOReader plugin are guarded with session-level flags, which matches the lifecycle constraints in `CLAUDE.md`: `lua/main.lua:169`, `lua/main.lua:293`.
- The nonce cache is explicitly bounded and uses defensive copies: `internal/localsend/nonce_cache.go:23`.
- 32-bit ARM atomic alignment is handled correctly in `RecvSession`: `internal/localsend/session/recv.go:50`.
- Path sanitization and atomic file creation are applied in both HTTP and WebRTC receive paths: `internal/webrtc/transfer/receiver.go:283`, `internal/localsend/session/recv.go:183`, `internal/utils/files.go:32`.

### Verified Non-Issues

- The KOReader networking lifecycle call is valid; `willRerunWhenConnected` exists in upstream KOReader: `OFFICIAL_KOREADER/koreader/frontend/ui/network/manager.lua:690`.
- Signaling callback tracking is bounded and cleaned up periodically: `internal/webrtc/signaling/client.go:35`, `internal/webrtc/signaling/client.go:308`.
- The HTTP PIN-attempt path already has explicit stale-entry cleanup, so the memory-growth concern there is handled: `internal/localsend/recv/recv.go:461`, `internal/utils/ratelimit.go:98`.

### Test Coverage Analysis

- `go test ./... -race -count=1` passed. `cd lua && busted spec/` passed with `508 successes / 0 failures / 0 errors / 0 pending`.
- Coverage is generally strong: `437` Go tests/examples and `25` Lua spec files.
- The most important gaps are around the findings above: v3 handler tests bypass `NewFileReceiver()`, there is no rejection test for invalid/missing v3 sender tokens, no receiver test injects real write failures, and no sender test asserts `RTCSendFileResponse` handling.
- The `cmd/` packages have no direct tests, so CLI flag wiring and end-user error reporting are still mostly covered indirectly.

### Recommendations

1. Fix the v3 HTTP identity/auth path first. It is both a protocol-interop bug and the clearest security gap.
2. Fix WebRTC transfer-result propagation next, on both receiver and sender sides, with failing tests first.
3. Replace or clean up the WebRTC `blockedPeers` map so its lifetime matches the HTTP rate-limit path.
4. Add constructor-based v3 handler tests and negative-path transfer tests before shipping to low-storage devices.

## Reference Refresh Notes (2026-03-07)

The local reference copies were refreshed after the review was completed:

- `OFFICIAL_KOREADER/koreader`
  - Updated from `7353a2e5a` (`2026-01-01`) to `5cd27cff1` (`2026-03-07`) via `git pull --ff-only`.
- `OFFICIAL_LOCALSEND/web`
  - Updated from `24c6999` (`2025-07-10`) to `ea5d55d` (`2026-01-30`) via `git pull --ff-only`.
- `OFFICIAL_LOCALSEND/core`
  - Re-synced from upstream `localsend/core/` at commit `3ec2d77` (`2026-03-07`).

Material observations from the refresh:

- KOReader:
  - High churn overall, but the APIs this plugin depends on remain compatible.
  - `frontend/ui/network/manager.lua` still provides `runWhenConnected()` and `willRerunWhenConnected()`.
  - `frontend/ui/uimanager.lua` still provides the same `scheduleIn()` / `unschedule()` behavior used by the plugin.
  - The `InputDialog` and `ButtonDialog` changes observed upstream are internal improvements and do not require changes in this plugin's current usage.
- LocalSend web:
  - Source tree moved under `app/`, so the current paths are `OFFICIAL_LOCALSEND/web/app/services/*`.
  - `app/services/webrtc.ts` is materially unchanged for the file-response flow used in the review.
  - `app/services/signaling.ts` now declares `PeerDeviceType` values as `MOBILE`, `DESKTOP`, `WEB`, `HEADLESS`, `SERVER`.
- LocalSend core:
  - The current upstream HTTP v3 client/server still use nonce caches, certificate-backed identity, and client-certificate verification.
  - This remains useful protocol-reference code, but it is not the complete shipping LocalSend product behavior by itself.

Potentially project-impacting upstream drift:

- The refreshed LocalSend core and web signaling types now model WebRTC `deviceType` in `SCREAMING_SNAKE_CASE`.
- This conflicts with current assumptions in:
  - `CLAUDE.md`
  - `docs/localsend_protocol_v3.md`
  - `internal/webrtc/signaling/messages.go`
  - `internal/localsend/localsend.go`
  - related signaling tests that currently expect lowercase values
- Practical effect:
  - Incoming uppercase signaling values in discovery paths may normalize to `"desktop"` in this project today.
  - Outgoing lowercase signaling values from this project may now be out of step with refreshed official clients.

Recommended follow-up from the refresh:

1. Re-verify WebRTC signaling `deviceType` casing against the live signaling service and current official clients.
2. If upstream uppercase signaling is confirmed, update this project to accept both cases on input and emit the upstream-compatible case on output.
3. Update `CLAUDE.md` and `docs/localsend_protocol_v3.md` after that verification so the local protocol guidance matches current upstream behavior.

## Compatibility Baseline Reset (2026-03-08)

After cloning the full upstream `localsend/localsend` repository, the support target is now defined as the union of the official clients we need to interoperate with, not `core/` in isolation.

Current upstream sources used for that baseline:

- Native app / shared app code:
  - `OFFICIAL_LOCALSEND_FULL/app`
  - `OFFICIAL_LOCALSEND_FULL/common`
- Official web client:
  - `OFFICIAL_LOCALSEND/web`
- Protocol/reference implementation:
  - `OFFICIAL_LOCALSEND/core`
- KOReader API compatibility:
  - `OFFICIAL_KOREADER/koreader`

### Support Matrix

1. Native app / common: officially shipped and required
   - Source of truth for LocalSend's current shipped local HTTP receive and upload behavior.
   - Proven by:
     - `OFFICIAL_LOCALSEND_FULL/app/lib/provider/network/server/controller/receive_controller.dart`
     - `OFFICIAL_LOCALSEND_FULL/common/lib/api_route_builder.dart`
     - `OFFICIAL_LOCALSEND_FULL/common/lib/src/task/upload/http_upload.dart`
   - Current implication:
     - local HTTP transfer routes remain `v1` / `v2`
     - these paths are mandatory for compatibility

2. Web client: officially shipped and required
   - Source of truth for current signaling and WebRTC transfer behavior.
   - Proven by:
     - `OFFICIAL_LOCALSEND/web/app/services/signaling.ts`
     - `OFFICIAL_LOCALSEND/web/app/services/webrtc.ts`
     - `OFFICIAL_LOCALSEND/web/app/services/crypto.ts`
   - Current implication:
     - WebRTC signaling and data-channel behavior are mandatory for compatibility
     - Phase 2 `deviceType` casing work remains justified by current shipped web behavior

3. Core: official reference, but not sufficient by itself to define mandatory support
   - Useful for protocol mechanics, token generation, nonce handling, and emerging implementation direction.
   - Proven by:
     - `OFFICIAL_LOCALSEND/core/src/http/client/v3.rs`
     - `OFFICIAL_LOCALSEND/core/src/http/server/mod.rs`
     - `OFFICIAL_LOCALSEND/core/src/webrtc/*`
   - Current implication:
     - `core` behavior is relevant when it matches shipped native-app or web behavior
     - `core` behavior alone does not automatically make a server path or auth rule mandatory for this project

### Immediate Planning Consequence

Phase 3 must now be justified against this matrix:

- Required now:
  - issues that break compatibility with the shipped native app
  - issues that break compatibility with the shipped web client
  - issues that cause correctness, integrity, or resource failures in our own implementation regardless of upstream coverage
- Reference-only until proven otherwise:
  - behaviors present only in `OFFICIAL_LOCALSEND/core` that are not exercised by the shipped native app or shipped web client

### Specific Reset For HTTP v3

- `OFFICIAL_LOCALSEND/core` still shows HTTPS v3 mutual TLS, nonce exchange, and certificate-backed identity.
- The full upstream shipped native app currently exposes local HTTP receive/upload routes through `v1` / `v2`, not `v3`.
- Therefore:
  - the earlier HTTP v3 finding cannot be prioritized solely because `core` contains a partial v3 server/client model
  - any further HTTP v3 work must be justified by actual interoperability targets, not by `core` alone
  - Phase 3 priority should shift first to issues that affect shipped web or native-app compatibility, plus our verified local correctness bugs

## Order Of Operations

This is the current working plan. It starts with documentation alignment so the repo's local guidance matches the refreshed upstream references before implementation work begins.

### Phase 1: Documentation Alignment

Goal: make `CLAUDE.md`, `docs/localsend_protocol_v3.md`, and this worklog describe the same upstream baseline.

Status as of 2026-03-07:

- Completed in this session:
  - `CLAUDE.md` protocol interop guidance now matches the refreshed upstream V3 casing baseline
  - `docs/localsend_protocol_v3.md` now reflects current V3 signaling `deviceType` casing, current HTTPS v3 mutual-TLS behavior, current observed web token-verification behavior, refreshed upstream paths, and a refreshed appendix analysis date
  - stale `packages/*` and `web/services/*` references were removed from the protocol document
- Remaining documentation rule:
  - future protocol claims should only be added after they are proven by current source, current tests, or direct runtime reproduction

1. Record the refreshed upstream baseline in the worklog
   - Keep the refreshed reference commit IDs and repository layout current.
   - Record only observations that are backed by current source, tests, or direct reproduction.
2. Update `CLAUDE.md`
   - Correct protocol interop notes that are now stale after the reference refresh.
   - Explicitly document any known upstream drift that affects current implementation choices.
   - Keep only high-signal guidance that contributors should treat as operationally true.
3. Update `docs/localsend_protocol_v3.md`
   - Fix stale signaling examples, casing assumptions, and upstream file path references.
   - Remove claims that are not backed by current reference code or a direct verified behavior trace.
   - Ensure references to official files point at the refreshed tree layout, especially `OFFICIAL_LOCALSEND/web/app/services/*`.
4. Reconcile worklog assumptions with the updated docs
   - Remove statements that were true for the older reference snapshots but not the refreshed ones.
   - Keep the worklog usable as the current source of record for decisions and follow-up tasks.

Exit criteria for Phase 1:

- `CLAUDE.md`, `docs/localsend_protocol_v3.md`, and the worklog no longer disagree on WebRTC signaling `deviceType` assumptions, upstream file paths, or HTTP v3 certificate expectations.
- Every protocol claim we keep is backed by a current source reference or a direct verified behavior check.

### Verification Standard For Documentation

We do not document guesses, stale beliefs, or softened "probably" language.

For each protocol claim we keep, we should be able to answer one of these:

1. Which exact current source file proves this?
2. Which exact current test proves this?
3. Which exact runtime reproduction proves this?

If we cannot answer one of those, the claim does not belong in the documentation yet.

### Current Audit Findings For `docs/localsend_protocol_v3.md`

These were the protocol-document audit targets for the Phase 1 pass. The items below have now been corrected in the current docs set.

1. Verify and correct WebRTC signaling `deviceType` casing
   - Refreshed upstream references now model signaling `deviceType` as `SCREAMING_SNAKE_CASE`:
     - `OFFICIAL_LOCALSEND/core/src/model/discovery.rs:3-10`
     - `OFFICIAL_LOCALSEND/core/src/webrtc/signaling.rs:77-139`
     - `OFFICIAL_LOCALSEND/web/app/services/signaling.ts:126-131`
   - Completed correction:
     - the docs now reflect the current official source baseline that V3 HTTP and current WebRTC signaling sources serialize `SCREAMING_SNAKE_CASE`
     - stale lowercase signaling examples were removed from the protocol document

2. Verify and correct HTTP v3 certificate expectations
   - Refreshed upstream core also requires client certificate authentication on the server side and derives peer identity from the presented client certificate:
     - `OFFICIAL_LOCALSEND/core/src/http/server/mod.rs:140-148`
     - `OFFICIAL_LOCALSEND/core/src/http/server/mod.rs:194-229`
     - `OFFICIAL_LOCALSEND/core/src/http/server/client_cert_verifier.rs:11-60`
   - Completed correction:
     - summary tables and TLS section now describe current HTTPS v3 mutual TLS, client-auth server behavior, and public-key-derived peer identity

3. Verify and correct the web token-verification note
   - In the refreshed web app:
     - `verifyToken()` exists in `OFFICIAL_LOCALSEND/web/app/services/crypto.ts:159-209`
     - but there are no call sites for `verifyToken()`, `publicKeyFromPem()`, or `publicKeyFromDer()` in the current app tree
     - the WebRTC handshake still receives remote tokens without verifying them in `OFFICIAL_LOCALSEND/web/app/services/webrtc.ts:105-141` and `OFFICIAL_LOCALSEND/web/app/services/webrtc.ts:376-439`
   - Completed correction:
     - the protocol document now distinguishes between verification helpers existing in `app/services/crypto.ts` and the current `app/services/webrtc.ts` flow not calling them

4. Correct stale reference implementation paths
   - Refreshed upstream layout is now:
     - HTTP client split across `OFFICIAL_LOCALSEND/core/src/http/client/mod.rs`, `v2.rs`, and `v3.rs`
     - web app sources moved under `OFFICIAL_LOCALSEND/web/app/services/*`
   - Completed correction:
     - appendix paths now point at the refreshed `core/src/http/client/{mod,v3}.rs` split and `web/app/services/*`

5. Refresh the appendix analysis date and re-verify every summary-table row
   - The appendix now records the current verification pass date and the currently proven implementation-difference rows.
   - Completed correction:
     - appendix analysis date refreshed to `2026-03-07`
     - the WebRTC token-verification row was rewritten to describe current observed behavior precisely

### Phase 2: Code Alignment To Upstream

Goal: update the implementation where refreshed upstream behavior materially affects interoperability.

Status as of 2026-03-08:

- Completed in this session:
  - WebRTC signaling `deviceType` is now normalized at the signaling boundary
  - outbound signaling payloads now serialize known device types as `SCREAMING_SNAKE_CASE`
  - inbound legacy lowercase signaling payloads are still accepted and normalized
  - `ClientInfo.ToAnnouncement()` now lowers known signaling values for internal display/model reuse
  - signaling compatibility tests were updated to the refreshed upstream casing baseline and extended with explicit legacy-lowercase coverage
- Validation completed:
  - `go test ./internal/webrtc/signaling/...`
  - `go test ./...`

1. Verify the highest-risk upstream drift in code-facing terms
   - Confirm current WebRTC signaling `deviceType` expectations against refreshed official references and, if needed, the live signaling flow.
   - Identify any input/output normalization required for compatibility with both older and refreshed peers.
2. Update protocol-facing code paths
   - Adjust signaling serialization and parsing if the refreshed upstream baseline requires different casing or field handling.
   - Update discovery/display normalization so refreshed upstream peer metadata is preserved correctly.
3. Update tests for upstream compatibility
   - Add or revise tests to cover the refreshed upstream behavior.
   - Prefer compatibility tests that exercise both the old and new representations when the ecosystem is mixed.

Exit criteria for Phase 2:

- The code emits upstream-compatible protocol data for the refreshed baseline.
- Incoming upstream messages that should interoperate are accepted and normalized correctly.
- Tests capture the compatibility contract we are now targeting.

### Phase 3: Code Review Issues

Goal: address the verified defects after the upstream-alignment baseline is stable.

Status as of 2026-03-08:

- Completed in this session:
  - split receiver V2 and V3 identity handling so V3 `register` / `info` no longer reuse V2 `Version` / `Fingerprint` defaults
  - `prepare-upload` v3 now rejects missing sender tokens and uses the exchanged nonce plus presented client certificate to verify sender tokens when a TLS client cert is available
  - HTTPS listener now requires a client certificate and validates that the presented cert is structurally valid, time-valid, and self-signed before request handling
  - nonce cache keys now prefer the presented client certificate identity when available instead of always using only the request IP
  - constructor-backed tests replaced the previous masked V3 handler assumptions, and TLS client-cert verification now has direct unit coverage
- Validation completed:
  - `go test ./internal/localsend/recv ./internal/localsend/utils`
  - `go test ./...`

Status reset after full-upstream verification:

- The compatibility baseline for Phase 3 is now:
  - shipped native app / common behavior
  - shipped web behavior
  - project-local correctness and resource safety bugs
- `OFFICIAL_LOCALSEND/core` remains reference material, but core-only behavior is no longer sufficient by itself to define Phase 3 priority.
- That means the HTTP v3 work already completed can remain in place, but additional HTTP v3 hardening is not the next default priority unless a shipped client depends on it.

1. Fix WebRTC transfer-result propagation
   - Make receiver write failures visible and fatal at the protocol layer.
   - Make the sender wait for and honor `RTCSendFileResponse`.
   - This is mandatory because it affects the shipped web interoperability path and local transfer correctness.
2. Fix bounded-growth / cleanup issues
   - Replace or clean up the WebRTC blocked-peer tracking so it matches the resource-discipline used elsewhere.
   - This remains important for constrained e-reader devices independent of upstream client coverage.
3. Re-baseline any remaining HTTP v3 work against actual supported peers
   - Before doing more HTTP v3 tightening, confirm whether the behavior is required by shipped web or native-app interoperability.
   - Treat `OFFICIAL_LOCALSEND/core` as reference-only unless another official client depends on the same path.
4. Close the review-driven test gaps
   - Add the missing negative-path and regression tests that would have caught the verified bugs.
   - Prioritize tests around shipped compatibility paths first.

Exit criteria for Phase 3:

- The verified review findings are either fixed with tests or explicitly deferred with documented rationale.
- The remaining backlog is smaller, clearer, and based on the shipped compatibility baseline rather than stale assumptions about `core/` alone.
