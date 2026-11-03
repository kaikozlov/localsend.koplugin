#!/usr/bin/env bash
set -euo pipefail

expected_ref="${OFFICIAL_LOCALSEND_WEB_REF:-8fd3e7f6522f2ddc7c4cb0d237bb995ed95313c0}"
tmp_root=""

cleanup() {
  if [[ -n "$tmp_root" ]]; then
    rm -rf "$tmp_root"
  fi
}
trap cleanup EXIT

if [[ -n "${OFFICIAL_LOCALSEND_WEB_DIR:-}" ]]; then
  web_root="$OFFICIAL_LOCALSEND_WEB_DIR"
else
  tmp_root="$(mktemp -d)"
  web_root="$tmp_root/web"
  echo "Fetching LocalSend Web at $expected_ref"
  git init -q "$web_root"
  git -C "$web_root" remote add origin https://github.com/localsend/web.git
  git -C "$web_root" fetch -q --depth=1 origin "$expected_ref"
  git -C "$web_root" checkout -q --detach FETCH_HEAD
fi

web_root="$(cd "$web_root" && pwd)"
actual_ref="$(git -C "$web_root" rev-parse HEAD)"
if [[ "$actual_ref" != "$expected_ref" ]]; then
  echo "LocalSend Web ref mismatch: got $actual_ref, want $expected_ref" >&2
  exit 1
fi

webrtc="$web_root/app/services/webrtc.ts"
signaling="$web_root/app/services/signaling.ts"
data_channel="$web_root/app/utils/dataChannel.ts"

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
require_literal "$data_channel" 'export const MAX_BUFFERED_AMOUNT = 1024 * 1024; // 1 MiB'
require_literal "$webrtc" 'status: "PAIR_DECLINED"'
require_literal "$webrtc" 'const fileStatus = await dataChannelStream.readNext();'
require_literal "$signaling" '}, 120 * 1000);'
require_literal "$signaling" '30 * 60 * 1000,'

echo "LocalSend Web source contract matches $expected_ref"
