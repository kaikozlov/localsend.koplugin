-- localsend_constants.lua
-- Shared constants for LocalSend plugin modules
-- This module provides a single source of truth for constants used across multiple modules

local M = {}

-- File paths (temporary files for server lifecycle)
M.PID_FILE = "/tmp/localsend_koreader.pid"
M.TRANSFER_LOG_FILE = "/tmp/localsend_transfers.log"
M.TRANSFER_NOTIFY_FILE = "/tmp/localsend_notify"
M.TRANSFER_BUSY_FILE = "/tmp/localsend_busy" -- Exists while an inbound file is actively being written
M.SIGNALING_ID_FILE = "/tmp/localsend_signaling.id" -- WebRTC signaling ID for self-filtering
M.SERVER_OUTPUT_FILE = "/tmp/localsend_server.out" -- Combined stdout/stderr from receiver process
M.LIFECYCLE_LOG_FILE = "/tmp/localsend_lifecycle.log" -- Timestamped plugin/server/power/network events

-- Send-related file paths
M.SEND_PID_FILE = "/tmp/localsend_send.pid" -- Actual Go sender PID
M.SEND_SUPERVISOR_PID_FILE = "/tmp/localsend_send_supervisor.pid" -- Shell waiter that records exit status
M.SEND_OUTPUT_FILE = "/tmp/localsend_send.out"
M.LAST_SEND_EVIDENCE_FILE = "/tmp/localsend_last_send.json"
M.SCAN_OUTPUT_FILE = "/tmp/localsend_scan.json"
M.SCAN_LOG_FILE = "/tmp/localsend_scan.log"

-- Diagnostics: nettest (multicast discovery self-test) output
M.NETTEST_OUTPUT_FILE = "/tmp/localsend_nettest.json"
M.NETTEST_PID_FILE = "/tmp/localsend_nettest.pid"
M.NETTEST_DURATION = 3 -- seconds the Go nettest probe listens

-- Polling intervals (seconds)
M.SENTINEL_POLL_INTERVAL = 2
M.SEND_POLL_INTERVAL = 0.5
M.SEND_CANCEL_GRACE_SECONDS = 2
M.SCAN_POLL_INTERVAL = 0.2

-- Network defaults
M.DEFAULT_PORT = "53317"
M.DEFAULT_SAVE_DIR = "/" -- Last-resort picker fallback; main.lua derives the platform-native home directory
M.WEBRTC_PORT_RANGE = "50000:50100"

-- Scan defaults
M.SCAN_TIMEOUT_SECONDS = 4 -- Multicast and WebRTC collection window
M.LEGACY_SCAN_TIMEOUT_SECONDS = 12 -- Bounded dual-protocol subnet scan deadline
M.SCAN_MAX_POLL_DURATION = 15 -- Maximum seconds to poll before giving up (guard against hung processes)

-- Update check defaults
M.DEFAULT_UPDATE_CHECK_INTERVAL_HOURS = 168 -- Weekly

return M
