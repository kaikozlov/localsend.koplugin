# LocalSend protocol state: shipping v2.2 and experimental v3/WebRTC

This is an **unofficial, source-pinned interoperability note**. It is not a
claim that LocalSend currently ships a complete protocol v3.

The name “v3” refers to two different unfinished artifacts: Rust code containing
`/api/localsend/v3/*` and WebRTC scaffolding, and the official protocol
repository's v3 Mermaid draft. They do not describe one complete, enabled
protocol.

## Table of contents

- [1. Authority and status map](#1-authority-and-status-map)
- [2. Version strings are context-dependent](#2-version-strings-are-context-dependent)
- [3. Shipping LocalSend 1.18.x behavior: protocol v2.2](#3-shipping-localsend-118x-behavior-protocol-v22)
  - [3.1 Discovery, addresses, and DTO casing](#31-discovery-addresses-and-dto-casing)
  - [3.2 HTTPS identity and certificate verification](#32-https-identity-and-certificate-verification)
  - [3.3 Upload API and v2.2 checksum behavior](#33-upload-api-and-v22-checksum-behavior)
  - [3.4 Download API session behavior](#34-download-api-session-behavior)
- [4. Shared cryptographic primitives](#4-shared-cryptographic-primitives)
  - [4.1 Base64, nonces, and tokens](#41-base64-nonces-and-tokens)
  - [4.2 Conditional WebRTC token verification](#42-conditional-webrtc-token-verification)
  - [4.3 Generated certificates](#43-generated-certificates)
- [5. Dormant Rust HTTP-v3 scaffolding at af3aad33](#5-dormant-rust-http-v3-scaffolding-at-af3aad33)
  - [5.1 Actually routed endpoints](#51-actually-routed-endpoints)
  - [5.2 Register DTO differences from v2](#52-register-dto-differences-from-v2)
  - [5.3 HTTP nonce exchange is not the WebRTC nonce exchange](#53-http-nonce-exchange-is-not-the-webrtc-nonce-exchange)
  - [5.4 Dormant client-only transfer methods](#54-dormant-client-only-transfer-methods)
  - [5.5 Implemented HTTP-v3 errors](#55-implemented-http-v3-errors)
- [6. Dormant Rust signaling and WebRTC wire behavior](#6-dormant-rust-signaling-and-webrtc-wire-behavior)
  - [6.1 Signaling service and peer visibility](#61-signaling-service-and-peer-visibility)
  - [6.2 Signaling URL, identity, and messages](#62-signaling-url-identity-and-messages)
  - [6.3 STUN and ICE behavior](#63-stun-and-ice-behavior)
  - [6.4 Data channel configuration and framing](#64-data-channel-configuration-and-framing)
  - [6.5 Handshake and file-list exchange](#65-handshake-and-file-list-exchange)
  - [6.6 Actual file boundaries and acknowledgements](#66-actual-file-boundaries-and-acknowledgements)
  - [6.7 Pairing is incomplete](#67-pairing-is-incomplete)
  - [6.8 Buffer sizes and back-pressure](#68-buffer-sizes-and-back-pressure)
- [7. Official v3 Mermaid draft at bf371ab](#7-official-v3-mermaid-draft-at-bf371ab)
  - [7.1 HTTP draft versus implementation](#71-http-draft-versus-implementation)
  - [7.2 WebRTC draft versus implementation](#72-webrtc-draft-versus-implementation)
- [8. Separately pinned historical web implementation](#8-separately-pinned-historical-web-implementation)
- [9. Interoperability guidance](#9-interoperability-guidance)
- [Appendix A. Wire examples](#appendix-a-wire-examples)
- [Appendix B. Provenance and source map](#appendix-b-provenance-and-source-map)
- [Revision history](#revision-history)

---

## 1. Authority and status map

| Layer | Pin | What it establishes | Status |
|---|---|---|---|
| Official LocalSend implementation | [`af3aad33c965defc39ecff8d9a4396a851ce3cc1`](https://github.com/localsend/localsend/tree/af3aad33c965defc39ecff8d9a4396a851ce3cc1) | Shipping app version constant, enabled routes, Rust DTOs, signaling server, and dormant WebRTC code | **Protocol 2.2 ships; WebRTC is disabled** |
| Official protocol repository | [`62bd3406ec80d62f2ed46269cdc06c4dcc391083`](https://github.com/localsend/protocol/tree/62bd3406ec80d62f2ed46269cdc06c4dcc391083) | Normative prose for protocol v2.2 | **Current stable protocol text** |
| Official v3 Mermaid addition | [`bf371abbc24e90fefa42f43e1fd02f007f11611e`](https://github.com/localsend/protocol/commit/bf371abbc24e90fefa42f43e1fd02f007f11611e) | Design diagrams using version `3.0` | **Draft; conflicts with implementation** |
| Separate LocalSend web repository | [`ea5d55d34db2f21b84bf0ffe39d6342013b4ecd8`](https://github.com/localsend/web/tree/ea5d55d34db2f21b84bf0ffe39d6342013b4ecd8) | A historical Nuxt/TypeScript WebRTC implementation | **Historical only; not part of `af3aad33`** |

At the official implementation pin, the Flutter app declares:

```dart
const webRTCEnabled = false;
```

Initialization is guarded by that value. The shipping app therefore uses
protocol v2.2 over HTTP(S), with multicast/HTTP discovery. The Rust core and the
signaling server contain experimental v3/WebRTC building blocks, but they are
not an enabled, complete official-app transfer path.

The `af3aad33` monorepo contains the Flutter app, Rust core, Rust signaling
server, CLI, and static browser upload/download assets. It does **not** contain a
current `packages/web` Nuxt WebRTC client. Claims about the separate web
repository are isolated in [§8](#8-separately-pinned-historical-web-implementation)
and pinned independently.

---

## 2. Version strings are context-dependent

There is no single implemented “v3 version string.”

| Context | Value | Meaning |
|---|---|---|
| Shipping app and Rust v2 constant at `af3aad33` | `2.2` | Implemented LocalSend protocol version |
| Disabled app signaling path | `2.2` | It passes the same shipping `protocolVersion` into signaling identity |
| Rust signaling unit-test fixtures | `2.3` | Test data; the DTO accepts any string |
| Historical web repository at `ea5d55d` | `2.3` | That separate implementation's constant |
| Official v3 HTTP Mermaid draft at `bf371ab` | `3.0` | Draft diagram examples |

The signaling and v3 HTTP DTO fields are plain strings and do not enforce a
literal version. Do not infer that `2.3` is the shipping protocol, and do not
rewrite `3.0` from the official draft as if it described the pinned Rust code.
The Rust source calls this field the **Client Protocol Version**, not a generic
capability label.

---

## 3. Shipping LocalSend 1.18.x behavior: protocol v2.2

The official v2.2 prose remains the interoperability baseline. This section
retains the v2.2 details most relevant to implementations that also experiment
with the dormant v3 code.

### 3.1 Discovery, addresses, and DTO casing

Default v2.2 values:

| Transport | Address | Port |
|---|---|---:|
| IPv4 multicast UDP | `224.0.0.167` | 53317 |
| HTTP or HTTPS TCP | device address | 53317 |

The pinned Rust implementation also has a LocalSend IPv6 multicast extension:
`ff12::fd3a:e420` on port 53317. It creates sockets per eligible interface and
preserves an IPv6 source scope ID. This is implementation behavior beyond the
v2.2 prose's IPv4 default.

A link-local peer such as `fe80::1%3` is unusable if its interface scope is
lost. The Rust HTTP client maps scoped IPv6 addresses to an internal synthetic
hostname and resolves them back to the scoped socket address. Ordinary unscoped
IPv6 URL literals use brackets, for example
`https://[2001:db8::1]:53317/api/localsend/v2/register`.

V2 multicast and HTTP DTOs use:

- `fingerprint`, not `token`;
- `download`, not `hasWebInterface`;
- lowercase `deviceType` (`mobile`, `desktop`, `web`, `headless`, `server`);
- lowercase `protocol` (`http`, `https`).

The v2.2 multicast message continues to use `fingerprint`. The v3 HTTP and
signaling DTOs' use of `token` does not replace the shipping multicast field.

### 3.2 HTTPS identity and certificate verification

For v2.2 HTTPS, the fingerprint is the SHA-256 digest of the complete
certificate DER, encoded as uppercase hexadecimal. HTTP mode instead uses a
random identity string.

> [!NOTE]
> Mutual certificate behavior in this subsection is **shipping behavior from
> the pinned Rust implementation**, beyond the official v2.2 README's terse
> HTTPS description. It is not attributed to the protocol prose.

The pinned Rust non-web HTTPS server requires the client to present a
certificate. When browser-facing web service modes are enabled, presentation is
optional because browsers do not have the LocalSend client certificate, but any
certificate that is presented is still verified as a time-valid, properly
self-signed certificate. For v2 Register over TLS, the server also accepts the
claimed `fingerprint` as device identity only when it matches the verified
client certificate's DER fingerprint.

Rust HTTP clients present their own certificate and private key as their
LocalSend identity. They also verify the peer certificate during the TLS
handshake:

- for a known peer, require the expected certificate fingerprint, so request
  metadata or file bytes are not sent to a mismatching peer;
- for discovery, where no expected fingerprint is available yet, accept any
  certificate that passes the self-signature and time-validity checks, then
  obtain the peer identity from the response/certificate;
- hostnames are not the identity mechanism because peer certificates have no
  matching SAN.

Certificate verification is therefore a **known-peer versus unknown-peer**
distinction, not a v2-versus-v3 distinction.

### 3.3 Upload API and v2.2 checksum behavior

Shipping LAN upload uses only these v2 routes:

```text
POST /api/localsend/v2/prepare-upload[?pin=123456]
POST /api/localsend/v2/upload?sessionId=...&fileId=...&token=...
POST /api/localsend/v2/cancel?sessionId=...
```

`/prepare-upload` sends metadata and returns a session ID plus one file token
per accepted file. `204` means no file transfer is needed; preparation errors
include `400`, `401`, `403`, `409`, `429`, and `500` as specified by v2.2.

Protocol v2.2 adds checksum-mismatch behavior to `/upload`:

- a file metadata object may include a lowercase hexadecimal `sha256` digest;
- if supplied, the receiver verifies the completed upload;
- mismatch returns HTTP `422`;
- the sender may retry the same file using the existing session and file token;
- retry count is implementation policy, not part of the wire protocol.

The `/upload` error set is distinct from `/prepare-upload`: `400` for missing
parameters, `403` for invalid token or source IP, `409` for a conflicting
session, `422` for SHA-256 mismatch, and `500` for an internal receiver error.

File metadata may also carry optional RFC 3339 `metadata.modified` and
`metadata.accessed` timestamps. Implementations should retain available
sub-second precision.

### 3.4 Download API session behavior

Reverse transfer remains under v2:

```text
GET  /api/localsend/v2/info
POST /api/localsend/v2/prepare-download[?pin=...][&sessionId=...]
GET  /api/localsend/v2/download?sessionId=...&fileId=...
```

At the pinned Rust implementation:

- `GET /info` advertises `download: true` when the Download API is active;
- `POST /prepare-download` exchanges a valid PIN for a session ID and file list;
- an accepted client may refresh with its existing `sessionId` without resending
  the PIN;
- the accepted session is bound to the request's source IP, and a new session
  for that client replaces its previous session;
- the current session ID happens to be the client's `PeerIp` string, but clients
  should treat it as opaque;
- `GET /download` uses the session and file IDs; invalid sessions and unknown
  file IDs return `403`;
- a missing PIN returns `401` without counting as a failed guess;
- incorrect PINs are counted per source IP, and the third failed guess blocks
  subsequent attempts with `429`.

Browser links should remain relative to the address used for authentication.
Changing to another interface address can change the browser's apparent source
IP and invalidate the IP-bound session.

---

## 4. Shared cryptographic primitives

### 4.1 Base64, nonces, and tokens

Rust uses URL-safe base64 without padding.

Generated nonces are 32 cryptographically random bytes. Validation accepts
lengths from 16 through 128 bytes inclusive.

The Rust token format is:

```text
HASH_METHOD.HASH.SALT.SIGN_METHOD.SIGNATURE
```

For generated tokens:

```text
HASH_METHOD = sha256
HASH         = base64url_no_pad(SHA256(SPKI_DER || salt))
SIGN_METHOD  = ed25519
SIGNATURE    = base64url_no_pad(Ed25519_sign(HASH))
```

The public key input is SubjectPublicKeyInfo DER, not raw Ed25519 key bytes.
Rust generates only Ed25519 token-signing keys. Verification can parse both
`ed25519` and legacy `rsa-pss` public keys.

Two salt uses exist in the dormant code:

- **timestamp token:** eight-byte little-endian Unix seconds, rejected when more
  than one hour old; the FRB signaling identity path generates this token;
- **WebRTC nonce token:** the in-band combined nonce described in §6.5.

The generic v3 HTTP Register DTO also has a field named `token`, but the pinned
server handler does not verify it. Timestamp tokens therefore must not be
presented as implemented HTTP Register authentication.

### 4.2 Conditional WebRTC token verification

The Rust core implements full nonce-token verification, including token shape,
hash/signature identifiers, exact salt equality, recomputed
`SHA256(SPKI_DER || nonce)`, and signature verification.

It invokes that verification during the initial WebRTC token exchange **only if
the caller supplied `expecting_public_key`**. With `None`, the peer's token is
received but not authenticated at that stage. The pinned Flutter receive path
calls `acceptOffer` without an expected public key.

The sender's dormant Pair branch can later parse the public key supplied in a
`PAIR` response and verify the already received token against it. That does not
make the ordinary non-paired handshake unconditionally authenticated.

### 4.3 Generated certificates

The pinned Rust certificate generator creates:

| Property | Value |
|---|---|
| Key | RSA-2048 |
| Subject | `CN=LocalSend User` |
| SAN | none |
| Signature | self-signed RSA/SHA-256 |
| Practical validity | rcgen default, 1975 through 4096 |
| Fingerprint | SHA-256 of certificate DER, uppercase hexadecimal |

Token-signing Ed25519 keys are separate from this RSA TLS identity.

---

## 5. Dormant Rust HTTP-v3 scaffolding at af3aad33

### 5.1 Actually routed endpoints

The Rust server route table implements exactly two v3 paths:

```text
POST /api/localsend/v3/nonce
POST /api/localsend/v3/register
```

The source marks Register `TODO: not wired up yet`. The route is reachable, but
the handler only deserializes the request and returns local identity data. It
does not consume cached nonces or authenticate the request token.

### 5.2 Register DTO differences from v2

The implemented Rust v3 request is:

```json5
{
  "alias": "Nice Orange",
  "version": "2.2",            // plain string; not enforced
  "deviceModel": "Samsung",   // optional; omitted when absent
  "deviceType": "MOBILE",     // optional; SCREAMING_SNAKE_CASE
  "token": "opaque-or-signed-token",
  "port": 53317,
  "protocol": "HTTPS",        // SCREAMING_SNAKE_CASE
  "hasWebInterface": true      // defaults false; omitted when false on serialization
}
```

The response omits `port` and `protocol`:

```json5
{
  "alias": "Secret Banana",
  "version": "2.2",
  "deviceModel": "Windows",
  "deviceType": "DESKTOP",
  "token": "server-token",
  "hasWebInterface": true
}
```

Compared with v2, this is not “the same DTO plus one field.” It renames
`fingerprint` to `token`, renames `download` to `hasWebInterface`, and changes
`deviceType` and `protocol` wire casing to `SCREAMING_SNAKE_CASE`.

### 5.3 HTTP nonce exchange is not the WebRTC nonce exchange

`POST /api/localsend/v3/nonce` accepts:

```json
{"nonce":"<base64url-no-pad, decoded length 16..128>"}
```

It stores the received nonce and returns a newly generated 32-byte nonce:

```json
{"nonce":"<base64url-no-pad>"}
```

Both client and server keep 200-entry LRU caches for received and generated
nonces. The server key is the certificate public key when TLS identity is
available, otherwise the client IP.

No routed Register or WebRTC code consumes those HTTP nonce caches at this pin.
WebRTC performs a separate nonce exchange **inside the data channel after it
opens**. Calling HTTP `/nonce` is not a prerequisite for the implemented WebRTC
scaffolding.

### 5.4 Dormant client-only transfer methods

`LsHttpClientV3` contains methods targeting:

```text
POST /api/localsend/v3/prepare-upload
POST /api/localsend/v3/upload
POST /api/localsend/v3/cancel
```

No pinned server route handles them, and the WebRTC implementation does not call
them. There is no Rust v3 `/pair` client method either.

Their DTO/wire behavior is not identical to v2. In particular, the v3
`prepare_upload` method has no PIN query argument, while v2 explicitly supports
`?pin=...`; its nested `info` is the renamed uppercase-cased v3 Register DTO.
These methods are dormant client scaffolding, not an interoperable HTTP-v3 file
transfer.

### 5.5 Implemented HTTP-v3 errors

Implemented behavior must be separated from the draft's transfer statuses:

| Condition | Implemented response |
|---|---|
| Valid `/nonce` | `200` with `NonceResponse` |
| Invalid JSON/body | `400` JSON `{"message":"..."}` |
| Invalid base64 or nonce length | `400` JSON `{"message":"Invalid nonce format"}` or `{"message":"Invalid nonce"}` |
| Valid `/register` body | `200` with `RegisterResponseDto` |
| Internal handler failure | `500` JSON `{"message":"Internal server error"}` |
| Any other v3 path | `404` with an empty body from the route table |

The v2 transfer statuses `204`, `401`, `403`, `409`, `422`, and `429` are not
implemented v3 route behavior. They remain relevant only to shipping v2 routes
or to the unimplemented Mermaid design in §7.

---

## 6. Dormant Rust signaling and WebRTC wire behavior

Everything in this section describes code present at `af3aad33`, not an enabled
shipping feature.

### 6.1 Signaling service and peer visibility

The default disabled-app settings are:

```text
Signaling: wss://public.localsend.org/v1/ws
STUN:      stun:stun.localsend.org:5349
```

The signaling server assigns a UUID and exposes/relays peers only within a group
derived from the source address:

- IPv4: exact source address;
- IPv6: the source `/64` prefix.

It does not provide arbitrary global peer discovery, and it relays SDP rather
than file bytes.

### 6.2 Signaling URL, identity, and messages

The route is exactly:

```text
wss://<host>/v1/ws?d=<base64url-no-pad(JSON ClientInfoWithoutId)>
```

Example identity for the pinned app if the disabled path were enabled:

```json5
{
  "alias": "Nice Orange",
  "version": "2.2",
  "deviceModel": "Samsung",
  "deviceType": "MOBILE",
  "token": "timestamp-signed-token"
}
```

The server-to-client message tags are `HELLO`, `JOIN`, `UPDATE`, `LEFT`,
`OFFER`, `ANSWER`, and `ERROR`. Client-to-server tags are `UPDATE`, `OFFER`, and
`ANSWER`. Fields within message objects are camelCase. Device types are
SCREAMING_SNAKE_CASE.

`OFFER` and `ANSWER` carry `sessionId`, peer/target identity, and `sdp`. SDP is
UTF-8, zlib-compressed at best compression, then URL-safe base64 encoded without
padding.

The Rust signaling client sends a WebSocket Ping frame after 120 seconds without
an outgoing application message. No current web-client keep-alive behavior is
implied here; the independently pinned historical behavior is in §8.

### 6.3 STUN and ICE behavior

STUN is configurable and useful for server-reflexive candidates, but it is not
inherently required when host candidates can connect directly on a LAN. The
disabled Flutter app's current default is LocalSend's STUN URI above, not
Google's.

The Rust core accepts caller-provided STUN URLs and constructs one
`RTCIceServer` with only `urls`; credentials remain default. Authenticated TURN
is not represented by this API.

The peer connection otherwise uses library defaults. The code does **not** set a
custom ICE port range, disconnected timeout, candidate pool, or other tuning. It
reacts when the peer-connection state becomes `Disconnected`.

Both Rust offer and answer paths wait for complete ICE gathering before sending
the compressed SDP; this is non-trickle ICE.

### 6.4 Data channel configuration and framing

The sender creates one data channel:

```text
label = "data"
ordered = true
maxPacketLifeTime = unset
maxRetransmits = unset
protocol = unset
negotiated = unset
```

Wire framing:

- ordinary handshake/header/status messages are text JSON;
- large JSON messages and file bodies are binary chunks;
- binary output is repacked to at most 16 KiB per data-channel message;
- chunked JSON is followed by a text delimiter;
- the implementation emits text `"0"` as the delimiter;
- Rust delimiter recognition is broader than emission: any text message whose
  byte length is zero or one is treated as a delimiter.

A text message is therefore structural. Do not insert arbitrary text frames in
a file body.

### 6.5 Handshake and file-list exchange

The actual Rust sequence after the data channel opens is:

1. **In-band nonce exchange.** Sender sends its nonce; receiver replies with its
   nonce. Both compute `sender_nonce || receiver_nonce`.
2. **Token exchange.** Sender sends `RTCTokenRequest`; receiver replies with
   `RTCTokenResponse::Ok {token}`, `PinRequired {token}`, or
   `InvalidSignature`. Initial verification is conditional as described in
   §4.2.
3. **Optional receiver PIN.** If the receiver requires a PIN, it challenges the
   sender with `PIN_REQUIRED`, `OK`, or `TOO_MANY_ATTEMPTS` status messages.
4. **Optional sender PIN.** The sender may independently require the receiver to
   prove a PIN before disclosing the file list.
5. **File list.** Sender emits chunked `RTCPinSendingResponse::Ok {files}` and a
   delimiter.
6. **Selection.** Receiver returns chunked `RTCFileListResponse::Ok {files}`
   mapping accepted file IDs to file-specific tokens, then a delimiter; it may
   instead return `DECLINED`. File tokens generated by Rust are UUID v4 strings.

Core wire enums also define `PAIR` and related responses, but official-app
pairing is incomplete (§6.7).

### 6.6 Actual file boundaries and acknowledgements

The Rust sender does **not** send a delimiter after each file and does **not**
wait for one status inside each file loop.

Actual sender behavior:

```text
for each accepted file:
    send text RTCSendFileHeaderRequest {id, token}
    send binary file chunks
send one final text "0"
receive exactly one data-channel message
close
```

On the receiver, the next text frame closes the previous file. That text is
either the next file's header or the final delimiter. For each completed file,
the receiver:

1. waits for the application to provide an `RTCSendFileResponse` through the
   FRB `send_file_status` action;
2. serializes and sends that result to the sender;
3. if the boundary text is the final delimiter, waits briefly for output to
   flush and exits;
4. otherwise parses the same text as the next `RTCSendFileHeaderRequest`.

This is an application-status handshake; completion of the byte stream does not
automatically manufacture success.

`RTCSendFileResponse.error` is optional and omitted on success:

```json
{"id":"file-1","success":true}
```

A failure includes the field:

```json
{"id":"file-1","success":false,"error":"checksum mismatch"}
```

Because the Rust sender drains only one message after the final delimiter, it
does not consume and associate every per-file response in the way the official
Mermaid draft depicts. Implementers reproducing the pinned Rust wire must not
invent per-file delimiters; implementers designing a corrected protocol should
resolve this incomplete acknowledgement behavior explicitly.

### 6.7 Pairing is incomplete

Pair-related enum variants and a sender-side parser exist in the core. If the
sender receives `RTCFileListResponse::Pair {publicKey}`, it can verify the
remote token using that key, ask an application callback, and send `OK`,
`PAIR_DECLINED`, or `INVALID_SIGNATURE`.

The pinned official application does not complete that flow:

- the FRB sender bridge has `TODO: support pairing` and automatically answers
  the pairing decision `false`;
- the receiver selection API exposes only “accept a set” or “decline”; it has no
  Pair-request action;
- the core receiver path emits `OK` or `DECLINED`, not `PAIR`;
- Flutter receive-page accept/decline callbacks are no-ops and session setup is
  marked TODO.

Pairing is therefore wire-model scaffolding, not a complete official-app
feature.

### 6.8 Buffer sizes and back-pressure

Pinned Rust values:

| Layer | Value |
|---|---:|
| File-backed source read buffer | 512 KiB |
| File-backed source channel capacity | 16 chunks |
| WebRTC output chunk size | 16 KiB |
| Data-channel receive queue capacity | 16 messages |
| Back-pressure polling | every 100 ms until `buffered_amount() == 0` |

The 512 KiB source chunks are repacked into 16 KiB data-channel messages. The
old 1 KiB file-read claim was incorrect. Historical browser thresholds, where
relevant, are isolated in §8 rather than stated as current Rust behavior.

---

## 7. Official v3 Mermaid draft at bf371ab

Commit `bf371ab` added `v3/http-diagram.mermaid` and
`v3/webrtc-diagram.mermaid` to the official protocol repository. The pinned
protocol checkout at `62bd340` contains those files, while its normative prose
README remains v2.2.

Treat the Mermaid files as an official **draft design**, not as proof of shipped
routes or exact `af3aad33` wire behavior.

### 7.1 HTTP draft versus implementation

The HTTP diagram uses version `3.0` and depicts:

- multicast/legacy Register using `fingerprint` and `download`;
- `/nonce`, followed by an authenticated re-register using a nonce-signed token;
- token failure and PIN-required Register responses with status DTOs;
- `/prepare-upload?pin=...`;
- `202 PAIR_REQUESTED` and `POST /api/localsend/v3/pair`;
- `/api/localsend/v3/upload` and `/api/localsend/v3/cancel`;
- session/token/IP validation and nonce cleanup.

At `af3aad33`, only `/nonce` and `/register` are routed. Register uses the
`token`/`hasWebInterface` DTO from §5.2, performs no authentication, and returns
ordinary identity data. The draft's authenticated Register response DTOs,
`/pair`, transfer routes, statuses, and cleanup flow are unimplemented.

The dormant Rust client methods also do not exactly implement the draft:
notably, v3 `prepare_upload` has no PIN argument, and there is no v3 Pair client
method.

### 7.2 WebRTC draft versus implementation

The draft's high-level signaling, in-band nonce/token exchange, optional PINs,
and 16 KiB chunks correspond broadly to Rust types.

Its file loop does not match the pinned Rust sender. The diagram shows:

```text
header -> file chunks -> delimiter -> RTCSendFileResponse
```

for every file, followed by another final delimiter. Rust sends no per-file
delimiters; the next header implicitly terminates the preceding file, and only
one delimiter terminates the entire batch. Rust also receives only once after
that final delimiter instead of waiting once per file inside the loop.

The draft's Pair flow is also not exposed end to end by the pinned official app.

---

## 8. Separately pinned historical web implementation

A real Nuxt/TypeScript WebRTC implementation exists in the separate
[`localsend/web`](https://github.com/localsend/web) repository. The local
reference is pinned at `ea5d55d34db2f21b84bf0ffe39d6342013b4ecd8`.
It is **not** a current authority for the `af3aad33` monorepo or shipping
LocalSend 1.18.x.

Pin-specific historical behavior:

| Area | Historical web behavior at `ea5d55d` |
|---|---|
| Version | `protocolVersion = "2.3"` |
| Default STUN | `stun:stun.l.google.com:19302` |
| Signaling keep-alive | empty text WebSocket message every 120 seconds |
| Signaling identity refresh | new timestamp token sent with `UPDATE` every 30 minutes |
| Session ID | `Math.random().toString(36).substring(2, 15)` |
| File token | `Math.random().toString()` |
| Pair request | automatically sends `PAIR_DECLINED` |
| Output chunk | 16 KiB |
| Streaming back-pressure | while `bufferedAmount > 1 MiB`, sleep 50 ms |
| Final flush polling | while buffered amount is nonzero, sleep 50 ms |

The historical sender and receiver generate nonce-signed tokens but do not call
the repository's `verifyToken` function during the WebRTC handshake; they log or
store the remote token. That `verifyToken` helper itself validates an eight-byte
timestamp salt, not the combined WebRTC nonce.

Its file framing interoperates with the intended implicit-boundary model:

- it sends the first header, then each file's bytes;
- before waiting for that file's response, it sends the next header or the final
  delimiter, thereby closing the previous file;
- unlike the pinned Rust sender, it then reads one `RTCSendFileResponse` per
  file;
- its receiver automatically sends success after saving the previous file and
  omits `error` on success.

Its WebCrypto implementation defaults to RSA-PSS for older-browser support and
switches to Ed25519 when available. This is a historical browser implementation
difference; it does not change the pinned Rust core's Ed25519-only generation
and Ed25519/RSA-PSS verification behavior.

Do not copy these historical constants into current implementation guidance
without deliberately choosing compatibility with this exact commit.

---

## 9. Interoperability guidance

1. **For official-app interoperability, implement v2.2 first.** Use v2 multicast
   and HTTP routes, including v2.2 checksum mismatch `422` and the v2 Download
   API.
2. **Do not advertise complete official v3 support.** The app disables WebRTC,
   the Rust HTTP-v3 surface is incomplete, and the official v3 draft conflicts
   with the implementation.
3. **Keep the three nonce/token contexts separate:** timestamp signaling token,
   dormant HTTP `/nonce` caches, and the in-band WebRTC combined nonce.
4. **Use the correct DTO family:** v2 is lowercase with
   `fingerprint`/`download`; Rust v3/signaling is uppercase for enums with
   `token`/`hasWebInterface` where applicable; the official Mermaid draft has
   its own inconsistent shape.
5. **If experimenting with Rust WebRTC wire compatibility, reproduce actual
   boundaries:** next text header closes the previous file; one final `"0"`
   ends the batch.
6. **Treat token authentication as conditional** unless a verified expected
   public key or a completed pairing design supplies trust.
7. **Do not invent ICE settings.** At the pin, Rust sets only caller-provided ICE
   server URLs and otherwise uses defaults.
8. **Pin historical web compatibility explicitly.** Its version, STUN,
   keep-alive, token-verification gap, random identifiers, and buffering policy
   are not current monorepo facts.

---

## Appendix A. Wire examples

These examples are either direct DTO shapes or explicitly labeled fixture data.
They are not claims that the official app enables WebRTC.

### A.1 Signaling HELLO fixture

Rust unit tests use `2.3` as fixture data:

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

This proves serialization and casing, not an implemented `2.3` product
constant.

### A.2 Signaling offer and answer

Client to server:

```json
{
  "type": "OFFER",
  "sessionId": "sender-generated-session-id",
  "target": "target-peer-uuid",
  "sdp": "zlib-compressed-base64url-sdp"
}
```

Server to target:

```json
{
  "type": "OFFER",
  "peer": {
    "id": "sender-peer-uuid",
    "alias": "Cute Apple",
    "version": "2.2",
    "deviceType": "DESKTOP",
    "token": "timestamp-token"
  },
  "sessionId": "sender-generated-session-id",
  "sdp": "zlib-compressed-base64url-sdp"
}
```

`ANSWER` uses the same shapes with `type: "ANSWER"`.

### A.3 WebRTC data-channel DTOs

Nonce and token:

```json
{"nonce":"base64url-no-pad"}
```

```json
{"token":"sha256.<hash>.<combined-nonce>.ed25519.<signature>"}
```

```json
{"status":"OK","token":"sha256.<hash>.<combined-nonce>.ed25519.<signature>"}
```

File-list selection:

```json
{
  "status": "OK",
  "files": {
    "file-uuid-1": "token-uuid-1",
    "file-uuid-2": "token-uuid-2"
  }
}
```

Dormant Pair wire variant:

```json
{"status":"PAIR","publicKey":"-----BEGIN PUBLIC KEY-----\n..."}
```

File header and statuses:

```json
{"id":"file-uuid-1","token":"token-uuid-1"}
```

```json
{"id":"file-uuid-1","success":true}
```

```json
{"id":"file-uuid-1","success":false,"error":"application write failed"}
```

No fixed token string is included: the previously quoted token was not a test
vector in the pinned Rust source. Current Rust token tests use generated
round-trips rather than that external constant.

---

## Appendix B. Provenance and source map

All material claims above were checked against these pinned local references.
Paths are relative to the named checkout.

### B.1 Official implementation — `af3aad33`

- `app/lib/provider/network/webrtc/signaling_provider.dart` — WebRTC disabled,
  signaling URL, STUN default, and shipping version passed into signaling.
- `app/lib/provider/network/webrtc/webrtc_receiver.dart` — missing expected key,
  no-op receive callbacks, and unfinished session state.
- `packages/localsend_isolates/lib/constants.dart` — shipping protocol version
  `2.2` and IPv4 defaults.
- `packages/localsend_isolates/rust/src/api/webrtc.rs` — timestamp signaling
  token, pairing auto-decline, selection API, and application file-status action.
- `packages/core/src/model/discovery.rs` — v2 constant and v2/v3 enum casing.
- `packages/core/src/model/transfer.rs` — 512 KiB file-read buffer.
- `packages/core/src/crypto/{nonce,token,cert}.rs` — nonce, token, key, and
  certificate behavior.
- `packages/core/src/http/dto.rs` and `dto_v2.rs` — v3 versus v2 DTOs.
- `packages/core/src/http/client/v2.rs` and `client/v3.rs` — PIN and dormant v3
  client differences.
- `packages/core/src/http/client/server_cert_verifier.rs` and
  `packages/core/src/http/server/common/client_cert_verifier.rs` — client-side
  peer pinning plus server-side client-certificate requirements and validation.
- `packages/core/src/http/server/v2.rs` — TLS Register fingerprint binding.
- `packages/core/src/http/client/{url,scoped_host}.rs` — IPv6 URL handling.
- `packages/core/src/http/server/mod.rs` and `server/v3.rs` — actual route table,
  nonce caches, and unauthenticated Register handler.
- `packages/core/src/http/server/web.rs` and `server/common/pin.rs` — v2 Download
  sessions and PIN behavior.
- `packages/core/src/webrtc/{signaling,webrtc}.rs` — signaling DTOs, SDP,
  in-band handshake, framing, file acknowledgements, pairing variants, and ICE.
- `packages/core/src/multicast/mod.rs` and
  `packages/localsend_isolates/rust/src/api/discovery.rs` — IPv6 multicast and
  scope preservation.
- `server/src/main.rs`, `server/src/util/ip.rs`, and
  `server/src/controller/ws_controller.rs` — `/v1/ws`, grouping, and relay.

### B.2 Official protocol — `62bd340` / v3 addition `bf371ab`

- `README.md` — normative protocol v2.2, Upload API, `422`, Download API, and
  defaults.
- `CHANGELOG.md` — v2.2 checksum-mismatch change.
- `v3/http-diagram.mermaid` — draft `3.0` HTTP flow and unimplemented routes.
- `v3/webrtc-diagram.mermaid` — draft WebRTC flow and per-file delimiters.

### B.3 Historical web — `ea5d55d`

- `app/services/signaling.ts` — connection URL construction, keep-alive, and
  identity refresh.
- `app/services/webrtc.ts` — `2.3`, Google STUN, framing, per-file status drain,
  Pair decline, identifiers, chunks, and buffering.
- `app/services/crypto.ts` — browser Ed25519/RSA-PSS generation and timestamp
  verification helper.
- `app/services/store.ts` — signaling token refresh and default STUN use.
- `app/utils/{base64,nonce}.ts` — base64 and nonce behavior.

The separate web pin is historical evidence only. No unpinned integration
pseudocode or nonexistent monorepo web package is used as current official
implementation authority.

---

## Revision history

| Date | Change |
|---|---|
| 2026-08-14 | Rewrote the document around pinned authority layers; restored shipping v2.2/WebRTC-disabled status; separated dormant Rust wire behavior, the `bf371ab` official `3.0` Mermaid draft, and historical web pin `ea5d55d`; corrected nonce, STUN, token verification, DTO/routes/errors, signaling, pairing, file acknowledgements, optional error fields, buffers, ICE, appendices, and provenance while retaining valid v2.2 download/checksum material. |
