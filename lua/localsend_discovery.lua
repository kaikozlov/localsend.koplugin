-- localsend_discovery.lua
-- Device discovery for LocalSend plugin
-- Handles scanning for nearby devices and presenting selection UI

local state = require("localsend_state")
local constants = require("localsend_constants")
local power = require("localsend_power")
local firewall = require("localsend_firewall")

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

local function notifyActivityChanged()
    if deps.onActivityChanged then
        deps.onActivityChanged()
    end
end

-- Parse JSON output from localsend scan --json
-- @param json_str string JSON string from scan command
-- @return table Array of device objects with type, alias, ip/id, port, version, protocol
function M.parseDevices(json_str)
    if not json_str or json_str == "" then
        return {}, "Discovery returned no JSON output"
    end

    local ok, result = pcall(deps.json.decode, json_str)
    if not ok or type(result) ~= "table" then
        deps.logger.warn("[LocalSend] Failed to parse scan JSON:", json_str)
        return {}, "Discovery returned invalid JSON"
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

local function readPID(path)
    local content = deps.util.readFromFile(path)
    return content and tonumber(content:match("^(%d+)")) or nil
end

local function alive(pid)
    if not pid or not deps.util.pathExists("/proc/" .. pid) then
        return false
    end
    local stat = deps.util.readFromFile("/proc/" .. pid .. "/stat")
    return not (stat and stat:match("^%d+ %b() Z"))
end

local function scanRunning()
    return alive(readPID(constants.SCAN_OUTPUT_FILE .. ".pid")) or alive(readPID(constants.SCAN_OUTPUT_FILE .. ".supervisor"))
end

local function signalScan(signal)
    local pid = readPID(constants.SCAN_OUTPUT_FILE .. ".pid")
    if not alive(pid) then
        return
    end
    local cmdline = deps.util.readFromFile("/proc/" .. pid .. "/cmdline") or ""
    local owned = cmdline:find("localsend", 1, true) and ("\0" .. cmdline .. "\0"):find("\0scan\0", 1, true)
    -- The current supervisor owns a child that may still be between fork/exec.
    if owned or (state.ServerState.scan_in_progress and alive(readPID(constants.SCAN_OUTPUT_FILE .. ".supervisor"))) then
        os.execute(deps.util.shell_escape({ "kill", "-" .. signal, tostring(pid) }) .. " 2>/dev/null")
    end
end

local function removeScanFiles()
    for _, suffix in ipairs({ "", ".pid", ".supervisor", ".exit" }) do
        os.remove(constants.SCAN_OUTPUT_FILE .. suffix)
    end
end

local function finishScan()
    local ServerState = state.ServerState
    removeScanFiles()
    ServerState.scan_in_progress = false
    firewall.release("scan", ServerState.scan_port or constants.DEFAULT_PORT)
    ServerState.scan_port = nil
    power.release("scan")
    notifyActivityChanged()
end

-- Start device discovery scan in background
-- @param callback function Called with (devices, error) when scan completes;
--   errors are never presented as an empty successful scan
-- @param options table Optional settings: { use_webrtc = bool, use_https = bool, device_name = string, port = number }
function M.scanDevices(callback, options)
    local ServerState = state.ServerState
    options = options or {}

    -- Prevent concurrent scans
    if ServerState.scan_in_progress then
        deps.logger.dbg("[LocalSend] Scan already in progress")
        if callback then
            callback(nil, deps._("Another discovery scan is in progress."))
        end
        return false
    end

    ServerState.scan_op_id = (ServerState.scan_op_id or 0) + 1
    local op_id = ServerState.scan_op_id
    ServerState.scan_in_progress = true
    ServerState.scan_cancelled = false -- Reset cancel flag
    ServerState.scan_cancel_started_at = nil
    ServerState.scan_start_time = os.time() -- Track start time for timeout guard
    ServerState.scan_port = options.port or constants.DEFAULT_PORT
    power.acquire("scan")
    local opened = firewall.acquire("scan", ServerState.scan_port, options.use_webrtc ~= false)
    if not opened.ok then
        finishScan()
        if callback then
            callback(nil, opened.detail)
        end
        return false
    end
    notifyActivityChanged()
    -- Ensure the PID/output observed by this op cannot belong to a completed
    -- prior scan. The diagnostics log is overwritten by the launch redirection.
    removeScanFiles()

    -- Build scan command
    local args = {
        binary_path,
        "scan",
        "--json",
        "-t",
        tostring(constants.SCAN_TIMEOUT_SECONDS),
        "--legacy-timeout",
        tostring(constants.LEGACY_SCAN_TIMEOUT_SECONDS),
        "--port",
        tostring(ServerState.scan_port),
        "--control-socket",
        constants.CONTROL_SOCKET_FILE,
        "--https=" .. tostring(options.use_https ~= false),
        "--devname",
        options.device_name and options.device_name ~= "" and options.device_name or "KOReader",
    }

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
    -- The waiter records the actual exit status only after the Go child has
    -- shut down its temporary backend. Keep the waiter PID for the fork/exec gap.
    local cmd = string.format(
        '( (exec %s > %s 2> %s) & child=$!; echo "$child" > %s; wait "$child"; status=$?; echo "$status" > %s ) & echo $! > %s',
        deps.util.shell_escape(args),
        deps.util.shell_escape({ constants.SCAN_OUTPUT_FILE }),
        deps.util.shell_escape({ constants.SCAN_LOG_FILE }),
        deps.util.shell_escape({ constants.SCAN_OUTPUT_FILE .. ".pid" }),
        deps.util.shell_escape({ constants.SCAN_OUTPUT_FILE .. ".exit" }),
        deps.util.shell_escape({ constants.SCAN_OUTPUT_FILE .. ".supervisor" })
    )
    deps.logger.dbg("[LocalSend] Starting scan:", cmd)
    local launched = os.execute(cmd)
    if launched ~= 0 and launched ~= true then
        finishScan()
        if callback then
            callback(nil, deps._("Failed to launch device discovery."))
        end
        return false
    end

    -- Poll for completion
    local timeout_error
    local function checkScanComplete()
        if ServerState.scan_op_id ~= op_id then
            return
        end
        -- Timeout guard: force completion if polling too long (guards against hung processes)
        local elapsed = os.time() - ServerState.scan_start_time
        if elapsed >= constants.SCAN_MAX_POLL_DURATION then
            timeout_error = deps._("Device discovery timed out.")
            ServerState.scan_cancelled = true
            ServerState.scan_cancel_started_at = ServerState.scan_cancel_started_at or os.time()
        end
        if ServerState.scan_cancelled then
            local age = os.time() - (ServerState.scan_cancel_started_at or os.time())
            signalScan(age >= 2 and "KILL" or "TERM")
        end
        local status = readPID(constants.SCAN_OUTPUT_FILE .. ".exit")
        if scanRunning() or (status == nil and elapsed < 2) then
            -- Still running, check again later
            deps.UIManager:scheduleIn(constants.SCAN_POLL_INTERVAL, checkScanComplete)
            return
        end
        local cancelled = ServerState.scan_cancelled
        local devices, err
        if timeout_error then
            err = timeout_error
        elseif not cancelled then
            if status ~= 0 then
                err = deps.util.readFromFile(constants.SCAN_LOG_FILE)
                if not err or err == "" then
                    err = deps._("Device discovery failed.")
                end
            else
                devices, err = M.parseDevices(deps.util.readFromFile(constants.SCAN_OUTPUT_FILE))
                if not err then
                    ServerState.discovered_devices = devices -- Cache results
                end
            end
        end
        if err then
            devices = nil
        end
        finishScan()

        -- Call completion callback
        if callback and (not cancelled or timeout_error) then
            callback(devices, err)
        end
    end

    -- Start polling after a short delay
    deps.UIManager:scheduleIn(constants.SCAN_POLL_INTERVAL, checkScanComplete)
    return true
end

-- Cancel an in-progress scan
-- Cancellation owns no cleanup until the actual child and waiter have exited.
function M.cancelScan()
    local ServerState = state.ServerState
    if not ServerState.scan_in_progress then
        return
    end
    ServerState.scan_cancelled = true -- Signal to polling callback
    ServerState.scan_cancel_started_at = ServerState.scan_cancel_started_at or os.time()
    signalScan("TERM")
end

-- Synchronously stop an active scan for PluginLoader/KOReader teardown.
function M.stopActiveScanSync(usleep)
    local ServerState = state.ServerState
    if not ServerState.scan_in_progress and not scanRunning() then
        return true
    end
    M.cancelScan()
    local sleep = usleep or deps.usleep
    for attempt = 1, 60 do
        signalScan(attempt > 40 and "KILL" or "TERM")
        if not scanRunning() then
            ServerState.scan_op_id = (ServerState.scan_op_id or 0) + 1
            finishScan()
            return true
        end
        if sleep then
            sleep(50 * 1000)
        else
            os.execute("sleep 0.05")
        end
    end
    -- Keep ownership and polling alive if the OS has not completed termination.
    deps.logger.err("[LocalSend] Discovery process did not exit; retaining firewall ownership")
    return false
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

    -- Add scan/cancel actions below the discovered devices.
    local actions = {}
    if onRetry then
        table.insert(actions, {
            text = deps._("Scan again"),
            callback = function()
                deps.UIManager:close(dialog)
                M._current_dialog = nil
                onRetry()
            end,
        })
    end
    table.insert(actions, {
        text = deps._("Cancel"),
        callback = function()
            deps.UIManager:close(dialog)
            M._current_dialog = nil
            if onSelect then
                onSelect(nil)
            end
        end,
    })
    table.insert(buttons, actions)

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
