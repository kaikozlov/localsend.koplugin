# LocalSend Protocol v3 (Unofficial)

Based on the official implementations:
- **Rust Core**: [localsend/localsend/packages/core](https://github.com/localsend/localsend/tree/main/packages/core) (primary reference)
- **Web Client**: [localsend/localsend/packages/web](https://github.com/localsend/localsend/tree/main/packages/web) (TypeScript/Nuxt)

> [!NOTE]
> This specification extends [Protocol v2.1](https://github.com/localsend/protocol) with WebRTC support and HTTP v3 endpoints. The v2.1 protocol remains valid for legacy/LAN-only communication.

> [!IMPORTANT]
> **Version Naming**: While HTTP endpoints use `/api/localsend/v3/*`, the `version` field in client info messages is `"2.3"`. This document refers to the protocol as "v3" for clarity regarding the endpoint version.

---

## Table of Contents

- [1. Overview](#1-overview)
- [2. Changes from v2.1](#2-changes-from-v21)
- [3. Infrastructure](#3-infrastructure)
  - [3.1 Default Ports and Addresses](#31-default-ports-and-addresses)
  - [3.2 Public Signaling Server](#32-public-signaling-server)
  - [3.3 STUN Servers](#33-stun-servers)
- [4. Cryptographic Primitives](#4-cryptographic-primitives)
  - [4.1 Base64 Encoding](#41-base64-encoding)
  - [4.2 Nonce Generation](#42-nonce-generation)
  - [4.3 Token Format](#43-token-format)
  - [4.4 Token Generation](#44-token-generation)
  - [4.5 Token Verification](#45-token-verification)
  - [4.6 Supported Signature Algorithms](#46-supported-signature-algorithms)
- [5. TLS Certificates](#5-tls-certificates)
  - [5.1 Certificate Structure](#51-certificate-structure)
  - [5.2 Certificate Verification](#52-certificate-verification)
- [6. HTTP API v3](#6-http-api-v3)
  - [6.1 Nonce Exchange](#61-nonce-exchange)
  - [6.2 Register](#62-register)
  - [6.3 Prepare Upload, Upload, Cancel](#63-prepare-upload-upload-cancel-v3--client-only-not-routed-by-any-server)
  - [6.4 Error Handling](#64-error-handling)
- [7. WebRTC Signaling Protocol](#7-webrtc-signaling-protocol)
  - [7.1 Connection](#71-connection)
  - [7.2 Server Messages](#72-server-messages)
  - [7.3 Client Messages](#73-client-messages)
  - [7.4 SDP Encoding](#74-sdp-encoding)
- [8. WebRTC Data Channel Protocol](#8-webrtc-data-channel-protocol)
  - [8.1 Data Channel Configuration](#81-data-channel-configuration)
  - [8.2 Message Framing](#82-message-framing)
  - [8.3 Complete Flow Diagram](#83-complete-flow-diagram)
  - [8.4 Sending Flow](#84-sending-flow)
  - [8.5 Receiving Flow](#85-receiving-flow)
  - [8.6 Message Types](#86-message-types)
- [9. Enums](#9-enums)
- [10. Implementation Notes](#10-implementation-notes)
- [Appendix C: Implementation Differences (Rust vs Web)](#appendix-c-implementation-differences-rust-vs-web)

---

## 1. Overview

LocalSend v3 introduces WebRTC support for faster, more reliable file transfers that work across different networks without requiring direct LAN connectivity. The protocol operates in two phases:

1. **Signaling** - WebSocket-based discovery and SDP exchange via a signaling server
2. **Data Transfer** - Peer-to-peer file transfer over WebRTC data channels

---

## 2. Changes from v2.1

| Feature | v2.1 | v3 |
|---------|------|-----|
| HTTP API | `/api/localsend/v2/*` | `/api/localsend/v3/*` (discovery only — nonce + register) |
| File Transfer (HTTP) | Full support via v2 endpoints | **Not implemented** — falls back to v2 HTTP for LAN |
| File Transfer (WebRTC) | N/A | Full support via data channels |
| Nonce Exchange | Not required | Required before WebRTC session setup |
| Register Response | - | Adds `hasWebInterface` field |
| Certificate Verification | Optional | Verify signature + public key match |
| Token in Discovery | `fingerprint` | `token` (used for peer merging) |

---

## 3. Infrastructure

### 3.1 Default Ports and Addresses

| Protocol | Port | Address |
|----------|------|--------|
| HTTP/HTTPS | 53317 | - |
| Multicast UDP | 53317 | 224.0.0.167 |

### 3.2 Public Signaling Server

LocalSend provides a public signaling server for WebRTC connection establishment:

```
wss://public.localsend.org/v1/ws
```

This server:
- Assigns UUIDs to connected clients
- Groups clients by IP address into "rooms"
- Relays SDP offers/answers between peers
- Does NOT relay file data (that goes directly peer-to-peer)

### 3.3 STUN Servers

STUN servers are required for WebRTC NAT traversal. Example configuration:

```
stun:stun.l.google.com:19302
stun:stun1.l.google.com:19302
```

Clients should support configurable STUN/TURN servers for enterprise environments.

## 4. Cryptographic Primitives

### 4.1 Base64 Encoding

All base64 encoding uses **URL-safe alphabet without padding** (`URL_SAFE_NO_PAD`).

```
Standard alphabet: A-Z a-z 0-9 + /
URL-safe alphabet: A-Z a-z 0-9 - _
Padding: NONE (no trailing '=' characters)
```

**Example:**
```
Raw bytes: [0xDE, 0xAD, 0xBE, 0xEF]
Standard:  "3q2+7w=="
URL-safe:  "3q2-7w"
```

### 4.2 Nonce Generation

Nonces are cryptographically random byte sequences.

```rust
// Generation
length: 32 bytes
source: cryptographically secure random number generator

// Validation
valid if: 16 <= length <= 128 bytes
```

**WebRTC Combined Nonce:**
```
final_nonce = sender_nonce || receiver_nonce
```

The sender transmits their nonce first, then the final nonce is the concatenation of sender's nonce followed by receiver's nonce. Both peers compute the same combined nonce.

> **Note on Salt Types:** Token salts come in two forms:
> - **Nonces** (WebRTC): 16-128 bytes, used for peer authentication
> - **Timestamps** (Discovery/HTTP): Exactly 8 bytes (little-endian u64 Unix seconds), valid for 1 hour

### 4.3 Token Format

Tokens are used for authentication and pairing. The format is:

```
{HASH_METHOD}.{HASH}.{SALT}.{SIGN_METHOD}.{SIGNATURE}
```

| Field | Description |
|-------|-------------|
| `HASH_METHOD` | Always `sha256` |
| `HASH` | Base64-encoded SHA-256 hash of `(public_key_spki_der \|\| salt)` |
| `SALT` | Base64-encoded salt (nonce bytes or timestamp) |
| `SIGN_METHOD` | `ed25519` or `rsa-pss` |
| `SIGNATURE` | Base64-encoded signature of the hash |

> **Important:** The public key MUST be encoded in **SPKI DER format** (SubjectPublicKeyInfo), not raw key bytes. This is the format returned by `to_public_key_der()` in most cryptographic libraries.

**Example token:**
```
sha256.VGhpcyBpcyBhIHRlc3Q.MTIzNDU2Nzg.ed25519.U2lnbmF0dXJlRGF0YUhlcmU
```

### 4.4 Token Generation

There are two token types with different salt sources:

#### Nonce-based Tokens (WebRTC)
Used for WebRTC authentication where both peers exchange nonces.

```python
def generate_token_nonce(signing_key, nonce):
    # 1. Get public key in SPKI DER format
    public_key_der = signing_key.to_public_key_der()  # SPKI format

    # 2. Compute hash
    hash_input = public_key_der + nonce
    digest = sha256(hash_input)

    # 3. Sign the hash
    signature = signing_key.sign(digest)

    # 4. Format token
    return f"sha256.{base64(digest)}.{base64(nonce)}.ed25519.{base64(signature)}"
```

#### Timestamp-based Tokens (Discovery/HTTP)
Used for HTTP discovery and registration where no prior nonce exchange exists.

```python
def generate_token_timestamp(signing_key):
    # 1. Get current Unix timestamp as 8-byte little-endian
    salt = unix_timestamp_u64().to_le_bytes()  # 8 bytes

    # 2. Get public key in SPKI DER format
    public_key_der = signing_key.to_public_key_der()

    # 3. Compute hash
    hash_input = public_key_der + salt
    digest = sha256(hash_input)

    # 4. Sign the hash
    signature = signing_key.sign(digest)

    # 5. Format token
    return f"sha256.{base64(digest)}.{base64(salt)}.ed25519.{base64(signature)}"
```

### 4.5 Token Verification

> [!WARNING]
> **Implementation Note (as of 2025-12-30):** The Rust core fully implements nonce-based token verification for WebRTC. The web client only verifies timestamp-based tokens for discovery; it does **not** verify the remote peer's nonce-based token during WebRTC handshake. See [Appendix C](#appendix-c-implementation-differences-rust-vs-web) for details.

#### Nonce-based Token Verification
```python
def verify_token_nonce(public_key, token, expected_nonce):
    # 1. Parse token
    hash_method, hash_b64, salt_b64, sign_method, sig_b64 = token.split('.')

    # 2. Validate methods
    assert hash_method == "sha256"
    assert sign_method == public_key.signature_method()  # "ed25519" or "rsa-pss"

    # 3. Decode and validate salt matches expected nonce
    salt = base64_decode(salt_b64)
    assert salt == expected_nonce

    # 4. Recompute hash using SPKI DER format
    public_key_der = public_key.to_der()  # SPKI format
    expected_digest = sha256(public_key_der + salt)

    # 5. Verify hash matches
    provided_digest = base64_decode(hash_b64)
    assert expected_digest == provided_digest

    # 6. Verify signature
    signature = base64_decode(sig_b64)
    public_key.verify(expected_digest, signature)
```

#### Timestamp-based Token Verification
```python
def verify_token_timestamp(public_key, token):
    # 1. Parse token
    hash_method, hash_b64, salt_b64, sign_method, sig_b64 = token.split('.')

    # 2. Validate methods
    assert hash_method == "sha256"
    assert sign_method == public_key.signature_method()

    # 3. Decode and validate timestamp
    salt = base64_decode(salt_b64)
    assert len(salt) == 8  # Must be exactly 8 bytes
    timestamp = u64_from_le_bytes(salt)
    now = unix_timestamp_u64()
    assert now - timestamp <= 3600  # Token valid for 1 hour

    # 4. Recompute hash using SPKI DER format
    public_key_der = public_key.to_der()
    expected_digest = sha256(public_key_der + salt)

    # 5. Verify hash matches
    provided_digest = base64_decode(hash_b64)
    assert expected_digest == provided_digest

    # 6. Verify signature
    signature = base64_decode(sig_b64)
    public_key.verify(expected_digest, signature)
```

### 4.6 Supported Signature Algorithms

| Algorithm | Identifier | Key Format | Generation | Verification |
|-----------|------------|------------|------------|--------------|
| Ed25519 | `ed25519` | PKCS#8 PEM | ✅ Yes | ✅ Yes |
| RSA-PSS with SHA-256 | `rsa-pss` | PKCS#8 PEM | ❌ No | ✅ Yes (legacy interop) |

> [!NOTE]
> Current implementations generate tokens exclusively with Ed25519 (`SigningTokenKey` wraps `ed25519_dalek::SigningKey`). RSA-PSS support exists for **verification only** — implementations must be able to verify RSA-PSS tokens received from older clients, but will never generate them. The `generate_token_nonce` and `generate_token_timestamp` functions hardcode `sign_method = "ed25519"`.

**Ed25519 Example Public Key:**
```
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAZmdXP230oqK92o65ra3XaF2F8r3+fK5DEBK4c40qVts=
-----END PUBLIC KEY-----
```

---

## 5. TLS Certificates

### 5.1 Certificate Structure

LocalSend uses self-signed X.509 certificates:

| Field | Value |
|-------|-------|
| Common Name (CN) | `LocalSend User` |
| Validity | 10 years |
| Key Algorithm | RSA 2048 or Ed25519 |
| Signature Algorithm | SHA-256 with RSA |

### 5.2 Certificate Verification

When connecting over HTTPS, verify:

1. **Signature validity** - Certificate is properly self-signed
2. **Time validity** - Current time is within certificate's validity period
3. **Public key match** - If expecting a specific peer, verify the certificate's public key matches

```python
def verify_certificate(cert_der, expected_public_key=None):
    cert = parse_x509(cert_der)
    
    # 1. Check time validity
    if not cert.is_valid_now():
        raise Error("Time validity error")
    
    # 2. Check signature (self-signed)
    cert.verify_signature()
    
    # 3. Optional: Check public key
    if expected_public_key:
        cert_key = cert.public_key.to_pem()
        if cert_key != expected_public_key:
            raise Error("Public key mismatch")
```

---

## 6. HTTP API v3

The v3 HTTP API is limited to **discovery and session setup**. File transfer over HTTP uses v2 endpoints exclusively (see §2). The v3 additions are:

- **Nonce exchange** (`/nonce`) — cryptographic handshake for WebRTC session setup
- **Register** (`/register`) — enhanced device discovery with `hasWebInterface` field

The v3 Rust client library includes `prepare_upload`, `upload`, and `cancel` methods targeting `/api/localsend/v3/*` paths, but **no server implementation routes these endpoints** — neither the Rust HTTP server nor the Flutter app's HTTP server. In practice, v3 file transfers use WebRTC data channels (§8), and LAN-only transfers fall back to v2 HTTP.

> [!NOTE]
> The v2 HTTP endpoints (`/api/localsend/v2/prepare-upload`, `/api/localsend/v2/upload`, `/api/localsend/v2/cancel`) remain the only HTTP-based file transfer mechanism. See the [v2.1 protocol spec](https://github.com/localsend/protocol) for details.

### 6.1 Nonce Exchange

Used for secure handshakes before sensitive operations.

`POST /api/localsend/v3/nonce`

**Request:**
```json5
{
  "nonce": "base64-url-safe-nonce-string"  // 32 bytes recommended
}
```

**Response:**
```json5
{
  "nonce": "base64-url-safe-nonce-string"  // New nonce for client to use
}
```

The server validates: `16 <= len(nonce) <= 128` bytes.

### 6.2 Register

Same as v2 register but with additional field.

`POST /api/localsend/v3/register`

**Request:**
```json5
{
  "alias": "Nice Orange",
  "version": "2.0",
  "deviceModel": "Samsung",       // nullable
  "deviceType": "MOBILE",         // nullable, SCREAMING_SNAKE_CASE
  "token": "unique-client-token",
  "port": 53317,
  "protocol": "HTTPS",            // "HTTP" | "HTTPS"
  "hasWebInterface": false        // optional, default false
}
```

**Response:**
```json5
{
  "alias": "Secret Banana",
  "version": "2.0",
  "deviceModel": "Windows",
  "deviceType": "DESKTOP",
  "token": "unique-client-token",
  "hasWebInterface": true
}
```

### 6.3 Prepare Upload, Upload, Cancel (v3 — client-only, not routed by any server)

The Rust core's v3 HTTP client (`core/src/http/client/v3.rs`) includes `prepare_upload()`, `upload()`, and `cancel()` methods that target `/api/localsend/v3/prepare-upload`, `/api/localsend/v3/upload`, and `/api/localsend/v3/cancel` respectively. However, **no server implementation handles these routes**:

- The Rust HTTP server (`core/src/http/server/mod.rs`) only routes `/api/localsend/v3/nonce` and `/api/localsend/v3/register`
- The Flutter app's HTTP server (`receive_controller.dart`) only registers v1 and v2 routes via `ApiRoute.*.v2` (the `ApiRoute` enum in `api_route_builder.dart` generates only v1 and v2 paths)

These v3 client methods appear to be scaffolding for future use or are used internally by the WebRTC sender during session setup. The actual request/response formats are identical to v2 (see [v2.1 spec §4](https://github.com/localsend/protocol#4-file-transfer-http-aka-upload-api)). In practice, v3 file transfers use WebRTC data channels (§8).

> [!WARNING]
> Do not implement v3 `/prepare-upload`, `/upload`, or `/cancel` server endpoints expecting interoperability with the official LocalSend app. Use v2 HTTP endpoints for LAN file transfers.

### 6.4 Error Handling

**Error Response Format:**
```json5
{
  "message": "Error description"
}
```

**Error Codes:**

| HTTP Code | Description |
|-----------|-------------|
| 204 | Finished (no file transfer needed) |
| 400 | Invalid body / Missing parameters |
| 401 | PIN required / Invalid PIN |
| 403 | Rejected / Invalid token |
| 409 | Blocked by another session |
| 429 | Too many requests |
| 500 | Unknown error |

## 7. WebRTC Signaling Protocol

The signaling server facilitates WebRTC connection establishment between peers.

### 7.1 Connection

Connect to the signaling server via WebSocket:

```
wss://<signaling-server>/ws?d=<base64-client-info>
```

The `d` query parameter contains base64-encoded JSON (URL_SAFE_NO_PAD):

```json5
{
  "alias": "Nice Orange",
  "version": "2.0",
  "deviceModel": "Samsung",    // optional
  "deviceType": "mobile",      // optional, lowercase
  "token": "unique-client-token"
}
```

### 7.2 Server Messages

Messages sent from server to client. All use `type` field with `SCREAMING_SNAKE_CASE`.

#### HELLO
Sent immediately after connection. Contains your assigned info and current peers.

```json5
{
  "type": "HELLO",
  "client": {
    "id": "uuid",              // Server-assigned UUID
    "alias": "Nice Orange",
    "version": "2.0",
    "deviceModel": "Samsung",  // optional
    "deviceType": "mobile",    // optional
    "token": "unique-client-token"
  },
  "peers": [
    // Array of other connected clients (same structure as client)
  ]
}
```

#### JOIN
A new peer joined the room.

```json5
{
  "type": "JOIN",
  "peer": { /* ClientInfo */ }
}
```

#### UPDATE
A peer updated its info.

```json5
{
  "type": "UPDATE",
  "peer": { /* ClientInfo */ }
}
```

#### LEFT
A peer disconnected.

```json5
{
  "type": "LEFT",
  "peerId": "uuid"
}
```

#### OFFER
SDP offer from another peer.

```json5
{
  "type": "OFFER",
  "peer": { /* ClientInfo of sender */ },
  "sessionId": "unique-session-id",
  "sdp": "zlib-compressed-base64-sdp"
}
```

#### ANSWER
SDP answer from another peer.

```json5
{
  "type": "ANSWER",
  "peer": { /* ClientInfo of sender */ },
  "sessionId": "unique-session-id",
  "sdp": "zlib-compressed-base64-sdp"
}
```

#### ERROR
```json5
{
  "type": "ERROR",
  "code": 500
}
```

### 7.3 Client Messages

Messages sent from client to server.

#### UPDATE
```json5
{
  "type": "UPDATE",
  "info": { /* ClientInfoWithoutId */ }
}
```

#### OFFER
```json5
{
  "type": "OFFER",
  "sessionId": "unique-session-id",  // Client-generated UUID
  "target": "target-peer-uuid",
  "sdp": "zlib-compressed-base64-sdp"
}
```

#### ANSWER
```json5
{
  "type": "ANSWER",
  "sessionId": "unique-session-id",  // Same as the offer's sessionId
  "target": "target-peer-uuid",
  "sdp": "zlib-compressed-base64-sdp"
}
```

### 7.4 Keep-Alive

Clients should send periodic keep-alive messages to prevent WebSocket timeout:

| Implementation | Method | Interval |
|----------------|--------|----------|
| Rust Core | WebSocket Ping frame (`Message::Ping`) | 120 seconds |
| Web Client | Empty text message (`""`) | 120 seconds |

Both methods are acceptable; the signaling server handles both.

**Token Refresh (Web-specific):**

The web client also refreshes its identity token periodically to handle long-running browser sessions:

```javascript
// Every 30 minutes, generate new token and send UPDATE
setInterval(async () => {
  const info = await generateNewInfo();  // New token with fresh timestamp
  socket.send(JSON.stringify({ type: "UPDATE", info }));
}, 30 * 60 * 1000);
```

### 7.5 SDP Encoding

SDP strings are compressed and encoded:

```python
def encode_sdp(sdp_string):
    compressed = zlib.compress(sdp_string.encode(), level=BEST_COMPRESSION)
    return base64_url_safe_no_pad_encode(compressed)

def decode_sdp(encoded):
    compressed = base64_url_safe_no_pad_decode(encoded)
    return zlib.decompress(compressed).decode()
```

---

## 8. WebRTC Data Channel Protocol

After SDP exchange, peers communicate over a WebRTC data channel.

### 8.1 Data Channel Configuration

```javascript
{
  label: "data",
  ordered: true,
  maxPacketLifeTime: null,
  maxRetransmits: null,
  protocol: null,
  negotiated: false
}
```

### 8.2 Message Framing

#### Chunk Size
All binary data is chunked at **16 KiB (16,384 bytes)**.

```
CHUNK_SIZE = 16 * 1024  // 16 KiB
```

#### Message Types
- **String messages**: JSON protocol messages (headers, responses)
- **Binary messages**: File data chunks, chunked JSON for large messages

#### Delimiter
A **text message containing `"0"`** signals the end of a chunked message or file transfer.

```
Delimiter = text message "0"
Detection = msg.is_string && msg.data.len() <= 1
```

#### Chunked String Messages
Large JSON messages (like file lists) are sent as:
1. Binary chunks (max 16 KiB each)
2. Text delimiter `"0"` to signal end
3. Receiver concatenates binary chunks until delimiter

```python
# Sending
def send_string_in_chunks(channel, json_string):
    data = json_string.encode()
    for i in range(0, len(data), CHUNK_SIZE):
        chunk = data[i:i+CHUNK_SIZE]
        channel.send_binary(chunk)
    channel.send_text("0")  # delimiter

# Receiving
def receive_string_from_chunks(channel):
    buffer = bytearray()
    while True:
        msg = channel.receive()
        if msg.is_string:  # hit delimiter
            break
        buffer.extend(msg.data)
    return buffer.decode()
```

### 8.3 Complete Flow Diagram

```mermaid
sequenceDiagram
    participant S as Sender
    participant R as Receiver
    
    Note over S,R: Data Channel Opens (label="data", ordered=true)
    
    rect rgb(240, 248, 255)
    Note over S,R: Phase 1: Nonce Exchange
    S->>R: RTCNonceMessage {nonce: base64(32 bytes)}
    R->>S: RTCNonceMessage {nonce: base64(32 bytes)}
    Note over S,R: combined_nonce = sender_nonce || receiver_nonce
    end
    
    rect rgb(255, 248, 240)
    Note over S,R: Phase 2: Token Exchange
    S->>R: RTCTokenRequest {token: "sha256.hash.nonce.ed25519.sig"}
    R->>S: RTCTokenResponse {status: "OK", token: "..."}
    Note over S,R: Both verify token signature using combined_nonce
    end
    
    rect rgb(240, 255, 240)
    Note over S,R: Phase 3: PIN Handling (Optional)
    alt Receiver requires PIN
        Note right of R: Receiver sends PIN_REQUIRED challenge
        loop Until correct PIN or max attempts
            S->>R: RTCPinMessage {pin: "123456"}
            alt Wrong PIN
                R->>S: RTCPinReceivingResponse {status: "PIN_REQUIRED"}
            else Max attempts exceeded
                R->>S: RTCPinReceivingResponse {status: "TOO_MANY_ATTEMPTS"}
                Note over S,R: Session terminated
            end
        end
        R->>S: RTCPinReceivingResponse {status: "OK"}
    end
    alt Sender requires PIN
        S->>R: RTCPinSendingResponse {status: "PIN_REQUIRED"}
        loop Until correct PIN or max attempts
            R->>S: RTCPinMessage {pin: "123456"}
            alt Wrong PIN
                S->>R: RTCPinSendingResponse {status: "PIN_REQUIRED"}
            else Max attempts exceeded
                S->>R: RTCPinSendingResponse {status: "TOO_MANY_ATTEMPTS"}
                Note over S,R: Session terminated
            end
        end
        S->>R: RTCPinSendingResponse {status: "OK", files: [...]}
    end
    end
    
    rect rgb(255, 240, 255)
    Note over S,R: Phase 4: File List Exchange
    S->>R: [binary chunks] RTCPinSendingResponse with files
    S->>R: delimiter "0"
    R->>S: [binary chunks] RTCFileListResponse with tokens
    R->>S: delimiter "0"
    end
    
    rect rgb(248, 248, 248)
    Note over S,R: Phase 5: File Transfer
    loop For each accepted file
        S->>R: RTCSendFileHeaderRequest {id, token} (text)
        S->>R: [binary chunks up to 16KB each]
    end
    S->>R: delimiter "0" (end of all files)
    end
```

### 8.4 Sending Flow (Sender Perspective)

1. **Create data channel** with label `"data"`, ordered=true
2. **Wait for channel open event**
3. **Wait for buffer to empty** before sending

#### Phase 1: Nonce Exchange
```python
# Generate and send local nonce
local_nonce = random_bytes(32)
send_text(json({nonce: base64(local_nonce)}))

# Receive remote nonce
remote_msg = receive()  # must be string
remote_nonce = base64_decode(json_parse(remote_msg).nonce)

# Combine: sender first, receiver second
combined_nonce = local_nonce + remote_nonce
```

#### Phase 2: Token Exchange
```python
# Generate token using combined nonce as salt
token = generate_token(signing_key, combined_nonce)
send_text(json({token: token}))

# Receive response
response = receive()  # RTCTokenResponse
if response.status == "INVALID_SIGNATURE":
    disconnect()
if expecting_public_key:
    verify_token(expecting_public_key, response.token, combined_nonce)
```

#### Phase 3: PIN Handling
```python
if response.status == "PIN_REQUIRED":
    while True:
        pin = prompt_user_for_pin()
        send_text(json({pin: pin}))
        result = receive()
        if result.status == "OK":
            break
        if result.status == "TOO_MANY_ATTEMPTS":
            disconnect()

if own_pin_config:
    # Verify remote's PIN attempts
    send_result = verify_remote_pin(receive_rx, own_pin_config)
```

#### Phase 4: File List
```python
# Send file list (chunked)
file_list = RTCPinSendingResponse(status="OK", files=[...])
send_string_in_chunks(json(file_list))
send_delimiter()

# Receive file tokens (chunked)
response = receive_string_from_chunks()  # RTCFileListResponse
if response.status == "DECLINED":
    return
file_tokens = response.files  # {file_id: token}
```

#### Phase 5: File Transfer
```python
for file in selected_files:
    token = file_tokens[file.id]
    
    # Send header as text
    header = {id: file.id, token: token}
    send_text(json(header))
    
    # Send file data in 16KB chunks
    for chunk in file.read_chunks(16384):
        send_binary(chunk)

# Final delimiter
wait_buffer_empty()
send_delimiter()
receive()  # Wait for ack before closing
```

### 8.5 Receiving Flow (Receiver Perspective)

1. **Wait for data channel** with label `"data"`
2. **Set up message handler**

The flow mirrors the sender, with nonce order reversed:

```python
# Nonce: receive first, then send
remote_nonce = receive_nonce()
local_nonce = random_bytes(32)
send_text(json({nonce: base64(local_nonce)}))
combined_nonce = remote_nonce + local_nonce  # sender's first!
```

#### Receiving Files
```python
file_state = None
while True:
    msg = receive()
    
    if msg.is_string:
        if is_delimiter(msg):
            # End of all files
            break
            
        # New file header or end of previous file
        if file_state:
            file_state.complete()
            send_text(json({id: file_state.id, success: true}))
        
        header = json_parse(msg)  # RTCSendFileHeaderRequest
        if file_tokens[header.id] != header.token:
            error("Invalid token")
            continue
        
        file_state = start_receiving_file(header.id)
    else:
        # Binary file data
        if file_state:
            file_state.write(msg.data)
```

### 8.6 Message Types

All messages use JSON with `camelCase` field names. Status fields use `SCREAMING_SNAKE_CASE`.

#### RTCNonceMessage
```json5
{
  "nonce": "base64-encoded-32-bytes"
}
```

#### RTCTokenRequest
```json5
{
  "token": "sha256.{hash}.{nonce}.ed25519.{signature}"
}
```

#### RTCTokenResponse
```json5
{
  "status": "OK",           // "OK" | "PIN_REQUIRED" | "INVALID_SIGNATURE"
  "token": "..."            // Present if status != "INVALID_SIGNATURE"
}
```

#### RTCPinMessage
```json5
{
  "pin": "123456"
}
```

#### RTCPinReceivingResponse
```json5
{
  "status": "OK"            // "OK" | "PIN_REQUIRED" | "TOO_MANY_ATTEMPTS"
}
```

#### RTCPinSendingResponse
```json5
{
  "status": "OK",           // "OK" | "PIN_REQUIRED" | "TOO_MANY_ATTEMPTS"
  "files": [                // Only if status == "OK"
    {
      "id": "uuid",
      "fileName": "photo.png",
      "size": 12345,
      "fileType": "image/png",
      "sha256": "...",              // optional
      "preview": "base64-data",     // optional
      "metadata": {                 // optional
        "modified": "2024-01-01T12:00:00Z",
        "accessed": "2024-01-01T12:00:00Z"
      }
    }
  ]
}
```

#### RTCFileListResponse
```json5
{
  "status": "OK",           // "OK" | "PAIR" | "DECLINED" | "INVALID_SIGNATURE"
  "files": {                // Present if status == "OK"
    "file-id-1": "token-uuid-1",
    "file-id-2": "token-uuid-2"
  },
  "publicKey": "PEM-key"    // Present if status == "PAIR"
}
```

#### RTCPairResponse
```json5
{
  "status": "OK",           // "OK" | "PAIR_DECLINED" | "INVALID_SIGNATURE"
  "publicKey": "PEM-key"    // Present if status == "OK"
}
```

#### RTCSendFileHeaderRequest
```json5
{
  "id": "file-uuid",
  "token": "file-token-uuid"
}
```

#### RTCSendFileResponse
```json5
{
  "id": "file-uuid",
  "success": true,
  "error": null             // String if success == false
}
```

---

## 9. Enums

### Device Type

| Value | Description |
|-------|-------------|
| `MOBILE` | Mobile device (Android, iOS, FireOS) |
| `DESKTOP` | Desktop (Windows, macOS, Linux) |
| `WEB` | Web browser (Firefox, Chrome) |
| `HEADLESS` | Program without GUI running on a terminal |
| `SERVER` | (Self-hosted) cloud service running 24/7 |

> **Note:** In signaling messages, device types use lowercase (`mobile`, `desktop`). In HTTP API, they use SCREAMING_SNAKE_CASE.

### Protocol Type

| Value | Description |
|-------|-------------|
| `HTTP` | Unencrypted HTTP |
| `HTTPS` | Encrypted HTTPS with TLS |

---

## 10. Implementation Notes

### 10.1 URL Construction

IPv6 addresses must be enclosed in brackets:

```python
def build_url(protocol, host, port, path):
    if ':' in host:  # IPv6
        host = f"[{host}]"
    return f"{protocol}://{host}:{port}/api/localsend/v3{path}"

# Examples:
# build_url("https", "192.168.1.1", 53317, "/register")
#   -> "https://192.168.1.1:53317/api/localsend/v3/register"
# build_url("https", "::1", 53317, "/register")
#   -> "https://[::1]:53317/api/localsend/v3/register"
```

### 10.2 Nonce Cache

Implementations should use an LRU cache for nonce management:

```
Cache size: 200 entries
Key: Client identifier (public key for HTTPS, IP for HTTP)
Value: Nonce bytes
```

Two caches are maintained:
- **received_nonce_map**: Nonces received from remote
- **generated_nonce_map**: Nonces we generated and sent

### 10.3 Buffer Management

Before sending over WebRTC data channel, implementations should handle back-pressure:

| Implementation | Buffer Check | Polling Interval |
|----------------|--------------|------------------|
| Rust Core | Wait until `buffered_amount() == 0` | 100ms |
| Web Client | Wait while `bufferedAmount > 1 MiB` | 50ms |

**Rust approach (wait for empty):**
```python
def wait_buffer_empty(data_channel):
    while data_channel.buffered_amount() != 0:
        sleep(100ms)
```

**Web approach (threshold-based):**
```javascript
const MAX_BUFFERED_AMOUNT = 1024 * 1024;  // 1 MiB
while (dataChannel.bufferedAmount > MAX_BUFFERED_AMOUNT) {
    await sleep(50);
}
```

Both approaches work; the web approach allows more pipelining while the Rust approach is more conservative.

### 10.4 File Read Chunk Size

When reading files for transfer, recommended buffer size:

```
Read buffer: 1 KiB (for file reading)
Send chunk:  16 KiB (for data channel transmission)
```

### 10.5 WebRTC ICE Configuration

For WebRTC implementations, consider these ICE-related settings:

**Ephemeral UDP Port Range:**
On restricted environments (embedded devices, firewalls), limit the ephemeral UDP port range used by ICE candidates:

```
Recommended range: 50000-50100 (101 ports)
Default range:     1-65535 (full ephemeral range)
```

Restricting the port range:
- Makes firewall configuration simpler and more secure
- Reduces the attack surface on embedded devices
- Ensures compatibility with NAT and firewall rules

**ICE Timeouts:**
For reliable connections across varying network conditions:

```
ICE disconnected timeout: 25 seconds (extended from default)
ICE candidate pool size:  2 (for faster connection setup)
```

**ICE Candidate Types:**
LocalSend primarily uses host and server-reflexive (STUN) candidates. TURN relay is optional for enterprise environments.

### 10.6 ICE Gathering Strategy

Both official implementations use **complete ICE gathering** (not trickle ICE):

```python
# Wait for all ICE candidates before sending SDP
offer = peer_connection.create_offer()
peer_connection.set_local_description(offer)
await gathering_complete_promise()  # Block until complete
send_sdp(peer_connection.local_description.sdp)
```

This simplifies the signaling protocol by including all ICE candidates in a single SDP message, at the cost of slightly longer connection setup time.

### 10.7 Session and File Token Generation

| Token Type | Rust Core | Web Client |
|------------|-----------|------------|
| Session ID | UUID v4 (`Uuid::new_v4()`) | Random alphanumeric (`Math.random().toString(36).substring(2,15)`) |
| File Token | UUID v4 | Random decimal (`Math.random().toString()`) |

For new implementations, UUID v4 is recommended for stronger uniqueness guarantees.

### 10.8 Data Channel JavaScript Configuration

For JavaScript/TypeScript implementations, set the binary type explicitly:

```javascript
const dataChannel = peerConnection.createDataChannel("data");
dataChannel.binaryType = "arraybuffer";  // Required for proper binary handling
```

---

## Appendix A: Test Vectors

These JSON examples are taken directly from the Rust unit tests and can be used to verify your implementation's serialization/deserialization.

### A.1 Signaling Messages

#### HELLO Message
```json
{
  "type": "HELLO",
  "client": {
    "id": "00000000-0000-0000-0000-000000000000",
    "alias": "Cute Apple",
    "version": "2.3",
    "deviceModel": "Dell",
    "deviceType": "DESKTOP",
    "token": "123"
  },
  "peers": []
}
```

#### OFFER Message
```json
{
  "type": "OFFER",
  "peer": {
    "id": "00000000-0000-0000-0000-000000000000",
    "alias": "Cute Apple",
    "version": "2.3",
    "deviceType": "DESKTOP",
    "token": "123"
  },
  "sessionId": "456",
  "sdp": "my-sdp"
}
```

Note: `deviceModel` is omitted when null (not present).

#### Client UPDATE Message
```json
{
  "type": "UPDATE",
  "info": {
    "alias": "Cute Apple",
    "version": "2.3",
    "deviceModel": "Dell",
    "deviceType": "DESKTOP",
    "token": "123"
  }
}
```

### A.2 WebRTC Data Channel Messages

#### RTCFileListResponse (PAIR)
```json
{
  "status": "PAIR",
  "publicKey": "123"
}
```

#### RTCFileListResponse (OK)
```json
{
  "status": "OK",
  "files": {
    "file-uuid-1": "token-uuid-1",
    "file-uuid-2": "token-uuid-2"
  }
}
```

### A.3 Token Verification Test Vector

```python
# Test token from Rust tests
public_key_pem = """-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAZmdXP230oqK92o65ra3XaF2F8r3+fK5DEBK4c40qVts=
-----END PUBLIC KEY-----"""

token = "sha256.RikOdJlAUTdMVFZjEk7Bft5G9cxnNBBLfgttPpyS2FY.hJCuZwAAAAA.ed25519.iNgHrRzX2Iel-Ozj47yn5o5v0cGY_BswK6JYqwY65j7Krpr43KanAaCrjUng7gHtc2pCcylUrKswR_rxyswhDA"

# Token format: sha256.{hash}.{salt}.ed25519.{signature}
# Salt is 8-byte little-endian timestamp: 0x67AE9084 = 1739456644

# CRITICAL: Hash is computed over SPKI DER format, not raw key bytes
# The PEM above decodes to SPKI DER (44 bytes for Ed25519):
#   30 2a 30 05 06 03 2b 65 70 03 21 00 [32-byte raw key]
# The hash input is: SPKI_DER || salt_bytes
```

### A.4 Chunk Processing Test

Data chunking behavior with CHUNK_SIZE = 16384 (16 KiB):

```python
# Input: 32773 bytes (CHUNK_SIZE * 2 + 5)
input_data = bytes(32773)

# Expected output: 3 chunks
# Chunk 0: 16384 bytes
# Chunk 1: 16384 bytes  
# Chunk 2: 5 bytes (remainder)

assert len(chunks) == 3
assert len(chunks[0]) == 16384
assert len(chunks[1]) == 16384
assert len(chunks[2]) == 5
```

---

## Appendix B: Integration Test Examples

These examples from the Rust `main.rs` show complete working flows.

### B.1 WebRTC Send Flow

```python
# 1. Connect to signaling server
info = ClientInfoWithoutId(
    alias="test",
    version="2.3",
    device_model="test",
    device_type="desktop",
    token="test"
)
connection = SignalingConnection.connect(
    "wss://public.localsend.org/v1/ws",
    info
)

# 2. Wait for peer to join
peer = wait_for_join(connection)

# 3. Prepare files
files = [FileDto(
    id="test-123-id",
    file_name="test.mp4",
    size=100,
    file_type="video/mp4"
)]

# 4. Configure PIN (optional)
pin_config = PinConfig(pin="456", max_tries=3)

# 5. Send offer with PIN verification
send_offer(
    connection,
    stun_servers=["stun:stun.l.google.com:19302"],
    target_id=peer.id,
    signing_key=generate_key(),
    expecting_public_key=None,
    pin=pin_config,
    files=files
)
```

### B.2 WebRTC Receive Flow

```python
# 1. Connect to signaling server (same as above)

# 2. Wait for offer
offer = wait_for_offer(connection)

# 3. Accept offer with own PIN
accept_offer(
    connection,
    stun_servers=["stun:stun.l.google.com:19302"],
    offer=offer,
    signing_key=generate_key(),
    expecting_public_key=None,
    pin=PinConfig(pin="123", max_tries=3)
)

# 4. Receive file list
files = wait_for_files()

# 5. Accept all files
selected_files = {f.id for f in files}
send_selection(selected_files)

# 6. Receive file data
for file in receiving_files:
    write_file(f"/path/{file.file_name}", file.binary_rx)
```

### B.3 HTTP Client Test Flow

```python
# 1. Create client with identity
client = LsHttpClient(private_key=PRIVATE_KEY, cert=CERT)

# 2. Exchange nonce
nonce = client.nonce(
    protocol="https",
    ip="localhost",
    port=53317
)

# 3. Register
register_dto = RegisterDto(
    alias="test",
    version="2.3",
    device_model="test",
    device_type="headless",
    token="test",
    port=53317,
    protocol="https",
    has_web_interface=False
)
response = client.register("https", "localhost", 53317, register_dto)

# 4. Prepare upload
prepare_dto = PrepareUploadRequestDto(
    info=register_dto,
    files={
        "test-123-id": FileDto(
            id="test-123-id",
            file_name="test.mp4",
            size=1000,
            file_type="video/mp4"
        )
    }
)
response = client.prepare_upload(
    "https", "localhost", 53317,
    public_key=response.public_key,
    payload=prepare_dto
)

# response contains sessionId and file tokens
```

---

## Appendix C: Implementation Differences (Rust vs Web)

> **Analysis Date:** 2025-12-30
>
> This appendix documents behavioral differences between the two official LocalSend implementations. Both are interoperable, but implementers should be aware of these variations.

### C.1 Summary Table

| Feature | Rust Core | Web Client | Interop Impact |
|---------|-----------|------------|----------------|
| Token verification (WebRTC) | Full verification with `verify_token_nonce()` | Token received but NOT verified | Web trusts unverified tokens |
| File transfer pipelining | Sequential (waits for status) | Pipelined (sends next header before status) | Both work, different ordering |
| Session ID format | UUID v4 | `Math.random().toString(36)` | Both unique, different entropy |
| File token format | UUID v4 | `Math.random().toString()` | Both work |
| Keep-alive method | WebSocket Ping frame | Empty text message `""` | Both accepted by server |
| Back-pressure wait | 100ms, until buffer=0 | 50ms, until buffer<1MiB | Both work |
| PAIR flow | Full implementation with user prompt | Auto-declines, waits for retry | Interoperable |
| Token refresh | Not implemented | Every 30 minutes | Web-specific for long sessions |

### C.2 Token Verification Gap

**This is the most significant difference between implementations.**

#### Rust Core (Full Verification)
```rust
// webrtc.rs:276-285
if let Some(expecting_public_key) = expecting_public_key {
    if !crypto::token::verify_token_nonce(
        &*expecting_public_key,
        &token,
        &nonce,
    ) {
        return Err(anyhow::anyhow!("Invalid token signature or nonce"));
    }
}
```

The Rust implementation:
1. Parses the token's 5 components
2. Validates hash method is `sha256`
3. Validates signature method matches key type
4. Verifies the nonce in the token matches the expected combined nonce
5. Recomputes the hash over `SPKI_DER || nonce`
6. Verifies the signature over the hash

#### Web Client (No Verification)
```typescript
// webrtc.ts:105-141
const tokenResponse = JSON.parse(tokenResponseRaw) as RTCTokenResponse;
if (tokenResponse.status === "INVALID_SIGNATURE") {
    console.error("Invalid signature");
    return;
}
// Token is stored but NEVER verified
console.log(`Received token: ${remoteToken}`);
```

The web client:
1. Receives the token
2. Checks only the status field
3. Logs the token but does not verify it

**Security Implications:**
- The WebRTC data channel is encrypted via DTLS, so confidentiality is maintained
- The gap affects identity verification, not data protection
- A malicious actor who can intercept signaling could potentially impersonate a peer to a web client
- Rust clients verify tokens, so they're protected even when communicating with web clients

### C.3 File Transfer Flow Differences

#### Rust Core (Sequential)
```
For each file:
  1. Send RTCSendFileHeaderRequest
  2. Send file data chunks
  3. Wait for RTCSendFileResponse
  4. Proceed to next file
```

#### Web Client (Pipelined)
```
For each file:
  1. Send RTCSendFileHeaderRequest
  2. Send file data chunks
  3. Send NEXT file's RTCSendFileHeaderRequest (or delimiter)
  4. Wait for RTCSendFileResponse for previous file
```

The web client sends the next file's header **before** receiving the status for the current file. This creates slight message reordering but improves throughput on high-latency connections.

Both approaches are interoperable because:
- The receiver processes messages in order
- Headers and data are clearly distinguishable (string vs binary)
- The delimiter marks the true end of transfers

### C.4 PAIR Flow Handling

The PAIR flow allows devices to establish trusted relationships for future connections.

#### Rust Core
```rust
RTCFileListResponse::Pair { public_key } => {
    // 1. Verify the token with provided public key
    // 2. Prompt user for confirmation via channel
    // 3. Send RTCPairResponse::Ok or RTCPairResponse::PairDeclined
    // 4. Wait for new RTCFileListResponse
}
```

#### Web Client
```typescript
if (fileListResponse.status === "PAIR") {
    console.log("Pairing required. Reject...");
    dataChannel.send(JSON.stringify({ status: "PAIR_DECLINED" }));
    // Wait for sender to retry with normal file list
    fileListResponseRaw = await receiveStringFromChunks(dataChannelStream);
}
```

The web client automatically declines pairing requests. This is intentional—browser sessions are typically ephemeral, so persistent pairing makes less sense.

### C.5 Crypto Algorithm Selection

Both implementations support Ed25519 and RSA-PSS, but with different capabilities:

| Implementation | Token Generation | Token Verification |
|----------------|-----------------|-------------------|
| Rust Core | Ed25519 only | Ed25519 + RSA-PSS |
| Web Client | Ed25519 (preferred) or RSA-PSS | Ed25519 + RSA-PSS |

The web client defaults to RSA-PSS because older Chrome versions don't support Ed25519 in WebCrypto. It attempts to upgrade to Ed25519 on startup:

```typescript
export async function upgradeToEd25519IfSupported(): Promise<void> {
    try {
        await window.crypto.subtle.generateKey(
            { name: "Ed25519" }, true, ["sign", "verify"]
        );
        selectedParams = cryptoParams.ed25519;
    } catch (e) {
        console.warn("Ed25519 not supported.");
    }
}
```

### C.6 Protocol Version Field

Both implementations use `"2.3"` as the version string in client info:

```json
{
    "alias": "Device Name",
    "version": "2.3",
    "deviceModel": "...",
    "deviceType": "DESKTOP",
    "token": "..."
}
```

This document refers to the protocol as "v3" because:
- HTTP endpoints use `/api/localsend/v3/*`
- This distinguishes it from the v2.1 LAN-only protocol
- The `version` field represents client capability, not protocol version

---

## References

- [LocalSend Protocol v2.1 (Official)](https://github.com/localsend/protocol)
- [Official Rust Implementation](https://github.com/localsend/localsend/tree/main/packages/core)
- [Official Web Implementation](https://github.com/localsend/localsend/tree/main/packages/web)
- Rust source files analyzed:
  - `core/src/crypto/nonce.rs` - Nonce generation (32 bytes, valid 16-128)
  - `core/src/crypto/token.rs` - Token format, Ed25519 & RSA-PSS support, SPKI DER key format, timestamp tokens
  - `core/src/crypto/cert.rs` - Certificate verification
  - `core/src/util/base64.rs` - Base64 encoding (URL_SAFE_NO_PAD)
  - `core/src/util/time.rs` - Unix timestamp handling (little-endian u64)
  - `core/src/http/client/mod.rs` - HTTP v3 client implementation
  - `core/src/webrtc/signaling.rs` - WebSocket signaling (3 unit tests)
  - `core/src/webrtc/webrtc.rs` - Data channel protocol, 16KB chunks, PIN handling (2 unit tests)
  - `core/src/main.rs` - Integration test examples
- Web source files analyzed (as of 2025-12-30):
  - `web/services/signaling.ts` - WebSocket signaling, keep-alive, message types
  - `web/services/webrtc.ts` - Data channel protocol, file transfer, PIN handling
  - `web/services/crypto.ts` - Token generation/verification, Ed25519/RSA-PSS support
  - `web/utils/base64.ts` - URL-safe base64 encoding
  - `web/utils/nonce.ts` - Nonce generation and validation

---

## Revision History

| Date | Changes |
|------|---------|
| 2026-05-06 | **§2, §6**: Corrected major inaccuracy — v3 HTTP endpoints for file transfer (`/prepare-upload`, `/upload`, `/cancel`) do not exist on any server. LAN transfers use v2 HTTP exclusively. v3 adds only `/nonce` and `/register` for WebRTC session setup. |
| 2026-05-06 | **§4.6**: Clarified RSA-PSS is verification-only; token generation is Ed25519-only. |
| 2026-05-06 | **§8.3**: Expanded PIN handling flow to show the retry loop with `PIN_REQUIRED`/`TOO_MANY_ATTEMPTS` responses and final `OK`. |
| 2026-05-06 | **Appendix A.1**: Fixed `deviceType` casing from `"desktop"` to `"DESKTOP"` (SCREAMING_SNAKE_CASE) in all 4 test vectors. |
| 2025-12-30 | Added Appendix C: Implementation Differences (Rust vs Web) |
| 2025-12-30 | Added Section 7.4: Keep-Alive mechanism (ping, token refresh) |
| 2025-12-30 | Added Sections 10.6-10.8: ICE gathering, token generation, JS config |
| 2025-12-30 | Updated Section 10.3: Buffer management with implementation comparison |
| 2025-12-30 | Added warning note to Section 4.5 about token verification gap |
| 2025-12-30 | Updated header to reference both Rust and Web implementations |
| 2025-12-30 | Added version naming clarification (v3 endpoints vs "2.3" version field) |
| 2025-12-28 | Added SPKI DER format clarification for token hashing |
| 2025-12-28 | Added timestamp-based tokens for discovery/HTTP |
| 2025-12-28 | Added salt type distinction (nonces vs timestamps) |
| 2025-12-28 | Added WebRTC ICE configuration section (port ranges, timeouts) |
| 2025-12-28 | Updated test vectors with SPKI DER format notes |
