#!/usr/bin/env bash
set -euo pipefail

web_root="${1:-REFERENCE/OFFICIAL_LOCALSEND/web}"
expected_ref="ea5d55d34db2f21b84bf0ffe39d6342013b4ecd8"
tmp_root=""

cleanup() {
  if [[ -n "$tmp_root" ]]; then
    rm -rf "$tmp_root"
  fi
}
trap cleanup EXIT

if [[ ! -d "$web_root/.git" && ! -f "$web_root/.git" ]]; then
  tmp_root="$(mktemp -d)"
  web_root="$tmp_root/web"
  git init -q "$web_root"
  git -C "$web_root" remote add origin https://github.com/localsend/web.git
  git -C "$web_root" fetch -q --depth=1 origin "$expected_ref"
  git -C "$web_root" checkout -q --detach FETCH_HEAD
fi

actual_ref="$(git -C "$web_root" rev-parse HEAD)"
if [[ "$actual_ref" != "$expected_ref" ]]; then
  echo "LocalSend Web ref mismatch: got $actual_ref, want $expected_ref" >&2
  exit 1
fi

webrtc="$web_root/app/services/webrtc.ts"
signaling="$web_root/app/services/signaling.ts"

require_literal() {
  local file="$1"
  local literal="$2"
  if ! grep -Fq "$literal" "$file"; then
    echo "Pinned LocalSend Web contract changed: missing '$literal' in $file" >&2
    exit 1
  fi
}

require_literal "$webrtc" 'export const protocolVersion = "2.3";'
require_literal "$webrtc" 'export const defaultStun = ["stun:stun.l.google.com:19302"];'
require_literal "$webrtc" 'const CHUNK_SIZE = 16 * 1024; // 16 KiB'
require_literal "$webrtc" 'const MAX_BUFFERED_AMOUNT = 1024 * 1024; // 1 MiB'
require_literal "$webrtc" 'status: "PAIR_DECLINED"'
require_literal "$webrtc" 'const fileStatus = await dataChannelStream.readNext();'
require_literal "$signaling" '}, 120 * 1000);'
require_literal "$signaling" '30 * 60 * 1000,'

echo "LocalSend Web source contract matches $expected_ref"
