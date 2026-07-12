-- localsend_state.lua
-- Centralized state management for LocalSend plugin
-- This module holds state that persists across widget instances (view switches)

local constants = require("localsend_constants")

local M = {}

-- Module-local state that persists across widget instances
-- This is preferred over _G globals for tracking state within a single KOReader session
M.ServerState = {
    user_stopped = false, -- True when user explicitly stopped the server
    was_running_before_suspend = false, -- True if server was running before suspend/standby
    was_running_before_disconnect = false, -- True if server was running before network disconnect
    last_log_position = 0, -- Track transfer log read position across instances
    transfer_count = 0, -- Cached transfer count (avoids full file read on e-readers)
    last_sentinel_value = nil, -- Last known content of sentinel file for fast change detection
    telemetry_cleaned = false, -- True after clearTmpTelemetryFiles() has run (once per session)

    -- Send-related state
    discovered_devices = {}, -- Cached devices from last scan
    scan_in_progress = false, -- True while device scan is running
    send_in_progress = false, -- True while file send is in progress

    -- Cancellation flags (checked by polling callbacks to avoid stale callbacks)
    scan_cancelled = false, -- True when scan was cancelled by user
    send_cancelled = false, -- True when send was cancelled by user

    -- Server lifecycle operation tracking (prevents stale async callbacks)
    server_op_id = 0, -- Monotonic operation counter for start/stop transitions
    stop_in_progress = false, -- True while graceful stop is in progress
    lifecycle_events = {}, -- Recent timestamped server/power/network transitions
}

local MAX_LIFECYCLE_EVENTS = 50
local MAX_LIFECYCLE_LOG_BYTES = 12 * 1024

-- Record enough lifecycle context to correlate transfer failures with suspend,
-- network loss, and receiver restarts. The in-memory ring survives widget
-- recreation; the /tmp journal also survives a KOReader restart on most devices.
function M.recordLifecycle(event, detail)
    local entry = {
        time = os.time(),
        event = tostring(event or "unknown"),
        detail = detail and tostring(detail) or nil,
    }
    local events = M.ServerState.lifecycle_events
    table.insert(events, entry)
    while #events > MAX_LIFECYCLE_EVENTS do
        table.remove(events, 1)
    end

    local line = string.format("%s\t%s\t%s\n", os.date("%Y-%m-%dT%H:%M:%S%z", entry.time), entry.event, entry.detail or "")
    local previous = ""
    local read_ok, old = pcall(io.open, constants.LIFECYCLE_LOG_FILE, "r")
    if read_ok and old then
        previous = old:read("*a") or ""
        old:close()
    end
    local combined = previous .. line
    if #combined > MAX_LIFECYCLE_LOG_BYTES then
        combined = "... (older lifecycle events omitted)\n" .. combined:sub(-MAX_LIFECYCLE_LOG_BYTES)
    end
    local write_ok, f = pcall(io.open, constants.LIFECYCLE_LOG_FILE, "w")
    if write_ok and f then
        pcall(f.write, f, combined)
        f:close()
    end
end

-- Expose for testing (matches current LocalSend._ServerState pattern)
M._ServerState = M.ServerState

return M
