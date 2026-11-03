# Official LocalSend interoperability tests

These are black-box contract tests between this repository's Go executable and
LocalSend's official Rust core. They adapt the externally observable assertions
from the official `packages/core/tests/v2_server.rs` and
`v2_web_download.rs` suites; they do not copy or reimplement the official client or
server.

Run them with Docker only:

```sh
just test-official
just test-official --test receiver
```

The runner always sparse-fetches the pinned official core into a temporary
directory, so the suite is self-contained and does not depend on anything under
`REFERENCE/`. Set `OFFICIAL_LOCALSEND_DIR` only when intentionally testing a
specific local checkout, or set `OFFICIAL_LOCALSEND_REF` while testing an
upstream revision.

The default native authority is LocalSend `main` at
`92e133aa43869e94d394fd04fa70c50a0f796a64` (2026-10-30), which is unreleased
work after the 1.18.2 release; the app still declares version `1.18.2+64`.

## Coverage

- Official Rust multicast discovery <-> Go receiver: real v2.2 UDP announcements and HTTP registration in both directions, in one network namespace.
- Official `LsHttpClientV2` -> Go receiver: registration, info, IPv4/IPv6,
  multi-file upload, persisted bytes, token rejection/retry, malformed upload,
  session exclusion/cancellation, PIN authentication, and PIN rate limiting.
- Official `LsHttpClientV2` -> Go Download API: info, prepare/download, session
  reuse, invalid session/file behavior, PIN authentication, and PIN rate
  limiting.
- Go forward sender -> official Rust V2 server: a real file upload with
  byte-for-byte verification by the official server, and a `--text` message
  that the server takes from the preview and answers with 204, as the official
  CLI does.

The official desktop-only `/show` tests, official web UI asset tests, and
server-decision modes that this headless implementation does not expose are not
portable protocol contracts and are intentionally excluded. The official core
currently has no equivalent black-box V3/WebRTC interoperability suite.

`Cargo.toml` points to `/opt/official-core`; that path is supplied by the Docker
runner. The Rust harness is not intended to run directly on the host.

The runner uses named Docker volumes for Cargo caches during local development.
CI supplies `OFFICIAL_INTEROP_CARGO_REGISTRY_DIR`,
`OFFICIAL_INTEROP_CARGO_GIT_DIR`, and `OFFICIAL_INTEROP_CARGO_TARGET_DIR` so
`actions/cache` can preserve those directories across ephemeral runners.
