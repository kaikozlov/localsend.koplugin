-- localsend_discovery.lua
-- Device discovery for LocalSend plugin
-- Handles scanning for nearby devices and presenting selection UI

local state = require("localsend_state")
local constants = require("localsend_constants")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}

-- Path configuration (set via M.init)
local binary_path = nil

-- Initialize module with dependencies
-- @param d table Dependencies: { UIManager, InfoMessage, Notification, ButtonDialog, util, json, logger, T, _ }
-- @param paths table Paths: { binary_path }
function M.init(d, paths)
    deps = d
    binary_path = paths.binary_path
end

-- Parse JSON output from localsend scan --json
-- @param json_str string JSON string from scan command
-- @return table Array of device objects with type, alias, ip/id, port, version, protocol
function M.parseDevices(json_str)
    if not json_str or json_str == "" then
        return {}
    end

    local ok, result = pcall(deps.json.decode, json_str)
    if not ok or type(result) ~= "table" then
        deps.logger.warn("[LocalSend] Failed to parse scan JSON:", json_str)
        return {}
    end

    local devices = {}

    -- Parse LAN devices
    if result.lan and type(result.lan) == "table" then
        for _, dev in ipairs(result.lan) do
            table.insert(devices, {
                type = "lan",
                alias = dev.alias or "Unknown",
                ip = dev.ip,
                port = dev.port or 53317,
                version = dev.version or "",
                protocol = dev.protocol or "https",
            })
        end
    end

    -- Parse WebRTC devices
    if result.webrtc and type(result.webrtc) == "table" then
        for _, dev in ipairs(result.webrtc) do
            table.insert(devices, {
                type = "webrtc",
                alias = dev.alias or "Unknown",
                id = dev.id,
                version = dev.version or "",
            })
        end
    end

    return devices
end

-- Check if a scan process is still running
-- @return boolean True if scan is in progress
local function isScanProcessRunning()
    if not deps.util.pathExists(constants.SCAN_OUTPUT_FILE .. ".pid") then
        return false
    end

    local content = deps.util.readFromFile(constants.SCAN_OUTPUT_FILE .. ".pid")
    if not content then
        return false
    end
    local pid = tonumber(content:match("^(%d+)"))
    if not pid then
        return false
    end
    return deps.util.pathExists("/proc/" .. pid)
end

-- Start device discovery scan in background
-- @param callback function Called with devices array when scan completes
-- @param options table Optional settings: { use_webrtc = bool, device_name = string }
function M.scanDevices(callback, options)
    local ServerState = state.ServerState
    options = options or {}

    -- Prevent concurrent scans
    if ServerState.scan_in_progress then
        deps.logger.dbg("[LocalSend] Scan already in progress")
        return
    end

    ServerState.scan_in_progress = true
    ServerState.scan_cancelled = false -- Reset cancel flag
    ServerState.scan_start_time = os.time() -- Track start time for timeout guard

    -- Build scan command
    local args = {
        binary_path,
        "scan",
        "--json",
        "-t",
        tostring(constants.SCAN_TIMEOUT_SECONDS),
        "--legacy-timeout",
        tostring(constants.LEGACY_SCAN_TIMEOUT_SECONDS),
    }

    -- Add device name if provided (shows this name to other peers during scan)
    if options.device_name and options.device_name ~= "" then
        table.insert(args, "--devname")
        table.insert(args, options.device_name)
    end

    -- If WebRTC is disabled, only scan LAN and legacy devices
    if options.use_webrtc == false then
        table.insert(args, "--lan")
        table.insert(args, "--legacy")
    end

    -- Add self-filtering if signaling ID file exists (receiver is running)
    if deps.util.pathExists(constants.SIGNALING_ID_FILE) then
        table.insert(args, "-e")
        table.insert(args, constants.SIGNALING_ID_FILE)
    end

    local pid_file = constants.SCAN_OUTPUT_FILE .. ".pid"

    -- Keep structured JSON on stdout and preserve discovery diagnostics separately.
    local cmd = string.format(
        "(%s > %s 2> %s) & echo $! > %s",
        deps.util.shell_escape(args),
        deps.util.shell_escape({ constants.SCAN_OUTPUT_FILE }),
        deps.util.shell_escape({ constants.SCAN_LOG_FILE }),
        deps.util.shell_escape({ pid_file })
    )

    deps.logger.dbg("[LocalSend] Starting scan:", cmd)
    os.execute(cmd)

    -- Poll for completion
    local function checkScanComplete()
        -- Check if scan was cancelled - exit without calling callback
        if ServerState.scan_cancelled then
            os.remove(constants.SCAN_OUTPUT_FILE)
            os.remove(pid_file)
            return
        end

        -- Timeout guard: force completion if polling too long (guards against hung processes)
        local elapsed = os.time() - (ServerState.scan_start_time or 0)
        local timed_out = elapsed >= constants.SCAN_MAX_POLL_DURATION

        if not timed_out and isScanProcessRunning() then
            -- Still running, check again later
            deps.UIManager:scheduleIn(constants.SCAN_POLL_INTERVAL, checkScanComplete)
            return
        end

        if timed_out then
            deps.logger.warn("[LocalSend] Scan timed out after", elapsed, "seconds, killing process")
            -- Kill the hung process
            local content = deps.util.readFromFile(pid_file)
            if content then
                local pid = tonumber(content:match("^(%d+)"))
                if pid then
                    os.execute(deps.util.shell_escape({ "kill", "-9", tostring(pid) }) .. " 2>/dev/null")
                end
            end
        end

        -- Scan complete
        ServerState.scan_in_progress = false

        -- Read and parse results
        local output = deps.util.readFromFile(constants.SCAN_OUTPUT_FILE)
        local devices = M.parseDevices(output)

        -- Cache results
        ServerState.discovered_devices = devices

        -- Clean up temp files
        os.remove(constants.SCAN_OUTPUT_FILE)
        os.remove(pid_file)

        -- Call completion callback
        if callback then
            callback(devices)
        end
    end

    -- Start polling after a short delay
    deps.UIManager:scheduleIn(constants.SCAN_POLL_INTERVAL, checkScanComplete)
end

-- Cancel an in-progress scan
function M.cancelScan()
    local ServerState = state.ServerState
    local pid_file = constants.SCAN_OUTPUT_FILE .. ".pid"

    ServerState.scan_cancelled = true -- Signal to polling callback

    if deps.util.pathExists(pid_file) then
        local content = deps.util.readFromFile(pid_file)
        if content then
            local pid = tonumber(content:match("^(%d+)"))
            if pid then
                os.execute(deps.util.shell_escape({ "kill", "-9", tostring(pid) }) .. " 2>/dev/null")
            end
        end
        os.remove(pid_file)
    end

    os.remove(constants.SCAN_OUTPUT_FILE)
    ServerState.scan_in_progress = false
end

-- Get cached devices from last scan
-- @return table Array of device objects
function M.getCachedDevices()
    return state.ServerState.discovered_devices or {}
end

-- Build device display text for selection UI
-- @param device table Device object
-- @return string Display text like "[LAN] iPhone (192.168.1.50)"
function M.getDeviceDisplayText(device)
    if device.type == "lan" then
        return string.format("[LAN] %s (%s)", device.alias, device.ip)
    else
        return string.format("[WebRTC] %s", device.alias)
    end
end

-- Show device selector dialog
-- @param devices table Array of device objects
-- @param onSelect function Called with selected device (or nil if cancelled)
-- @param onRetry function Optional callback to retry scan (enables "Scan again" button)
function M.showDeviceSelector(devices, onSelect, onRetry)
    if not devices or #devices == 0 then
        if onRetry then
            -- Show dialog with retry option
            local dialog
            dialog = deps.ButtonDialog:new({
                title = deps._("No devices found"),
                info_text = deps._("Make sure LocalSend is running on the target device."),
                buttons = {
                    {
                        {
                            text = deps._("Scan again"),
                            callback = function()
                                deps.UIManager:close(dialog)
                                onRetry()
                            end,
                        },
                        {
                            text = deps._("Cancel"),
                            callback = function()
                                deps.UIManager:close(dialog)
                                if onSelect then
                                    onSelect(nil)
                                end
                            end,
                        },
                    },
                },
            })
            deps.UIManager:show(dialog)
        else
            -- Fallback to original behavior (no retry available)
            deps.UIManager:show(deps.InfoMessage:new({
                text = deps._("No devices found. Make sure LocalSend is running on the target device."),
                timeout = 4,
            }))
            if onSelect then
                onSelect(nil)
            end
        end
        return
    end

    -- Use local variable to avoid race condition if showDeviceSelector is called
    -- multiple times - each dialog's button callbacks reference their own local dialog
    local dialog

    -- Build button rows
    local buttons = {}
    for _, device in ipairs(devices) do
        table.insert(buttons, {
            {
                text = M.getDeviceDisplayText(device),
                callback = function()
                    deps.UIManager:close(dialog)
                    M._current_dialog = nil
                    if onSelect then
                        onSelect(device)
                    end
                end,
            },
        })
    end

    -- Add cancel button
    table.insert(buttons, {
        {
            text = deps._("Cancel"),
            callback = function()
                deps.UIManager:close(dialog)
                M._current_dialog = nil
                if onSelect then
                    onSelect(nil)
                end
            end,
        },
    })

    dialog = deps.ButtonDialog:new({
        title = deps._("Select target device"),
        buttons = buttons,
        dismiss_callback = function()
            M._current_dialog = nil
        end,
    })
    M._current_dialog = dialog -- Track for external closing
    deps.UIManager:show(dialog)
end

-- Show scanning progress dialog with cancel option
-- @param onCancel function Called if user cancels
-- @return table Dialog object (for closing)
function M.showScanningDialog(onCancel)
    local dialog
    dialog = deps.ButtonDialog:new({
        title = deps._("Scanning for devices..."),
        buttons = {
            {
                {
                    text = deps._("Cancel"),
                    callback = function()
                        deps.UIManager:close(dialog)
                        if onCancel then
                            onCancel()
                        end
                    end,
                },
            },
        },
    })
    deps.UIManager:show(dialog)
    return dialog
end

return M
