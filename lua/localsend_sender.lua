-- localsend_sender.lua
-- File sending for LocalSend plugin
-- Handles file selection and send process management

local state = require("localsend_state")
local constants = require("localsend_constants")
local discovery = require("localsend_discovery")
local power = require("localsend_power")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}

-- Path configuration (set via M.init)
local binary_path = nil
local SEND_EVIDENCE_BYTES = 12 * 1024

local function tailString(value, max_bytes)
    value = tostring(value or "")
    if #value <= max_bytes then
        return value
    end
    return "... (showing last " .. tostring(max_bytes) .. " bytes)\n" .. value:sub(-max_bytes)
end

local function fileSize(path)
    local ok, f = pcall(io.open, path, "rb")
    if not ok or not f then
        return nil
    end
    local size = f:seek("end")
    f:close()
    return size
end

local function persistLastSend(value)
    if not deps.json or not deps.json.encode then
        return
    end
    local encode_ok, encoded = pcall(deps.json.encode, value)
    if not encode_ok or not encoded then
        return
    end
    local open_ok, f = pcall(io.open, constants.LAST_SEND_EVIDENCE_FILE, "w")
    if not open_ok or not f then
        return
    end
    pcall(f.write, f, encoded)
    f:close()
end

-- Initialize module with dependencies
-- @param d table Dependencies: { UIManager, InfoMessage, Notification, InputDialog, PathChooser, NetworkMgr, util, json, logger, T, _ }
-- @param paths table Paths: { binary_path }
function M.init(d, paths)
    deps = d
    binary_path = paths.binary_path

    -- Also initialize discovery module
    discovery.init({
        UIManager = d.UIManager,
        InfoMessage = d.InfoMessage,
        Notification = d.Notification,
        ButtonDialog = d.ButtonDialog,
        util = d.util,
        json = d.json,
        logger = d.logger,
        T = d.T,
        _ = d._,
        onActivityChanged = d.onActivityChanged,
    }, paths)
end

local function notifyActivityChanged()
    if deps.onActivityChanged then
        deps.onActivityChanged()
    end
end

-- Check if a send operation is in progress
-- @return boolean True if send is in progress
function M.isSendInProgress()
    return state.ServerState.send_in_progress
end

-- Read a numeric PID from a plugin-owned PID file.
local function readPID(path)
    if not deps.util.pathExists(path) then
        return nil
    end
    local content = deps.util.readFromFile(path)
    return content and tonumber(content:match("^(%d+)")) or nil
end

local function isPIDRunning(path)
    local pid = readPID(path)
    return pid ~= nil and deps.util.pathExists("/proc/" .. pid)
end

local function isLocalSendSendProcess(pid)
    if not pid or not deps.util.pathExists("/proc/" .. pid) then
        return false
    end
    local cmdline = deps.util.readFromFile("/proc/" .. pid .. "/cmdline")
    if not cmdline or cmdline == "" then
        return false
    end
    local framed = "\0" .. cmdline .. "\0"
    return cmdline:find("localsend", 1, true) ~= nil and framed:find("\0send\0", 1, true) ~= nil
end

local function isSendProcessRunning()
    return isLocalSendSendProcess(readPID(constants.SEND_PID_FILE))
end

local function isSendSupervisorRunning()
    return isPIDRunning(constants.SEND_SUPERVISOR_PID_FILE)
end

local function signalSendProcess(signal, allow_current_starting_child)
    local pid = readPID(constants.SEND_PID_FILE)
    local owned = isLocalSendSendProcess(pid)
    if not owned and allow_current_starting_child then
        -- Between fork and exec the PID is ours but its cmdline is still the
        -- shell wrapper. Only trust that state for this in-memory active send
        -- while its freshly-created supervisor is also alive.
        owned = state.ServerState.send_in_progress and pid ~= nil and deps.util.pathExists("/proc/" .. pid) and isSendSupervisorRunning()
    end
    if not owned then
        return false
    end
    os.execute(deps.util.shell_escape({ "kill", "-" .. signal, tostring(pid) }) .. " 2>/dev/null")
    return true
end

local function cleanupSendFiles()
    os.remove(constants.SEND_OUTPUT_FILE)
    os.remove(constants.SEND_OUTPUT_FILE .. ".exit")
    os.remove(constants.SEND_PID_FILE)
    os.remove(constants.SEND_SUPERVISOR_PID_FILE)
end

-- Send a file to a device
-- @param device table Device object (from discovery)
-- @param filepath string Path to file to send
-- @param pin string Optional PIN code
-- @param callback function Called with success boolean and message string
-- @param options table Optional settings: { device_name = string }
function M.sendFile(device, filepath, pin, callback, options)
    local ServerState = state.ServerState
    options = options or {}

    -- Prevent concurrent sends
    if ServerState.send_in_progress then
        if callback then
            callback(false, deps._("Another send operation is in progress"))
        end
        return
    end

    -- Validate file exists
    if not deps.util.pathExists(filepath) then
        if callback then
            callback(false, deps._("File does not exist"))
        end
        return
    end

    ServerState.send_op_id = (ServerState.send_op_id or 0) + 1
    local op_id = ServerState.send_op_id
    ServerState.send_in_progress = true
    ServerState.send_cancelled = false -- Reset cancel flag
    ServerState.send_cancel_started_at = nil
    power.acquire("send")
    notifyActivityChanged()
    local send_started_at = os.time()
    local send_size = fileSize(filepath)

    -- Build send command based on device type
    local args = { binary_path, "send" }

    -- Add device name if provided
    if options.device_name and options.device_name ~= "" then
        table.insert(args, "-n")
        table.insert(args, options.device_name)
    end

    if device.type == "lan" then
        -- V2 HTTP send
        table.insert(args, "--ip")
        table.insert(args, device.ip)
        if device.protocol == "https" then
            table.insert(args, "--https")
        else
            table.insert(args, "--https=false")
        end
    else
        -- V3 WebRTC send
        table.insert(args, "--webrtc")
        table.insert(args, "--target")
        table.insert(args, device.id)
    end

    -- Add PIN if provided
    if pin and pin ~= "" then
        table.insert(args, "-p")
        table.insert(args, pin)
    end

    -- Add file path
    table.insert(args, filepath)

    -- Old completed-send artifacts must not be mistaken for this operation's
    -- child/supervisor/exit status. An actually-running send is already blocked
    -- by ServerState above.
    cleanupSendFiles()

    -- A small supervisor records the exit status, but SEND_PID_FILE always
    -- contains the actual Go process PID. `exec` makes that identity stable
    -- across BusyBox/vendor shells so cancellation cannot merely kill a wrapper.
    local exit_file = constants.SEND_OUTPUT_FILE .. ".exit"
    local cmd = string.format(
        [[((exec %s > %s 2>&1) & child=$!; printf '%%s\n' "$child" > %s; wait "$child"; status=$?; printf '%%s\n' "$status" > %s) & echo $! > %s]],
        deps.util.shell_escape(args),
        deps.util.shell_escape({ constants.SEND_OUTPUT_FILE }),
        deps.util.shell_escape({ constants.SEND_PID_FILE }),
        deps.util.shell_escape({ exit_file }),
        deps.util.shell_escape({ constants.SEND_SUPERVISOR_PID_FILE })
    )

    deps.logger.dbg("[LocalSend] Starting send:", cmd)
    local launched = os.execute(cmd)
    if launched ~= true and launched ~= 0 then
        ServerState.send_in_progress = false
        power.release("send")
        notifyActivityChanged()
        cleanupSendFiles()
        if callback then
            callback(false, deps._("Failed to start send process"))
        end
        return
    end

    -- Extract filename for display
    local _, filename = deps.util.splitFilePathName(filepath)

    local function recordOutcome(success, message, output, exit_code, status)
        ServerState.last_send = {
            status = status or (success and "succeeded" or "failed"),
            success = success,
            message = message,
            time = os.time(),
            started_at = send_started_at,
            duration_seconds = math.max(0, os.time() - send_started_at),
            exit_code = exit_code,
            error_category = success and nil or M.categorizeError(output),
            raw_output = tailString(output, SEND_EVIDENCE_BYTES),
            protocol = device.type == "lan" and "V2 " .. tostring(device.protocol or "http") or "V3 WebRTC",
            recipient = device.alias or device.ip or device.id or "unknown",
            recipient_ip = device.type == "lan" and device.ip or nil,
            filename = filename,
            size = send_size,
        }
        persistLastSend(ServerState.last_send)
    end

    recordOutcome(nil, deps._("Send in progress"), "", nil, "in_progress")

    -- Keep an actionable status dialog visible for the whole transfer. The
    -- existing send state and PID supervisor remain authoritative; this dialog
    -- is only the user-facing control for the already-supported cancellation
    -- path. It is non-dismissable so closing the UI cannot leave a transfer
    -- running with no way to cancel it.
    local send_dialog
    local function closeSendDialog()
        if send_dialog then
            deps.UIManager:close(send_dialog)
            send_dialog = nil
        end
    end
    send_dialog = deps.ButtonDialog:new({
        title = deps.T(deps._("Sending %1 to %2..."), filename, device.alias),
        dismissable = false,
        buttons = {
            {
                {
                    text = deps._("Cancel"),
                    callback = function()
                        if ServerState.send_cancelled then
                            return
                        end
                        M.cancelSend()
                        closeSendDialog()
                        deps.UIManager:show(deps.Notification:new({
                            text = deps._("Cancelling send..."),
                            timeout = 2,
                        }))
                    end,
                },
            },
        },
    })
    deps.UIManager:show(send_dialog)

    -- Poll for completion
    local function checkSendComplete()
        if ServerState.send_op_id ~= op_id then
            closeSendDialog()
            return
        end
        -- Check if send was cancelled - show "Cancelled" not "Send failed".
        -- Wait until the real Go child is gone before releasing the power hold.
        if ServerState.send_cancelled then
            local elapsed = os.time() - (ServerState.send_cancel_started_at or os.time())
            if isSendProcessRunning() then
                signalSendProcess(elapsed >= constants.SEND_CANCEL_GRACE_SECONDS and "KILL" or "TERM", true)
                deps.UIManager:scheduleIn(constants.SEND_POLL_INTERVAL, checkSendComplete)
                return
            end
            if isSendSupervisorRunning() and elapsed < constants.SEND_CANCEL_GRACE_SECONDS then
                -- The supervisor may not have published the child PID yet, or
                -- the child may still be between fork and exec. Give it a bounded
                -- chance to reach the validated Go cmdline before cleanup.
                deps.UIManager:scheduleIn(constants.SEND_POLL_INTERVAL, checkSendComplete)
                return
            end
            if isPIDRunning(constants.SEND_PID_FILE) then
                -- At the end of the bounded launch/cancel grace this is still a
                -- child created by the current active supervisor, so stopping the
                -- pre-exec wrapper is safe even though it never reached Go.
                signalSendProcess("KILL", true)
                deps.UIManager:scheduleIn(constants.SEND_POLL_INTERVAL, checkSendComplete)
                return
            end
            -- The child is dead; a cancelled send does not consume exit status.
            local output = deps.util.readFromFile(constants.SEND_OUTPUT_FILE) or ""
            recordOutcome(false, deps._("Cancelled"), output, nil, "cancelled")
            closeSendDialog()
            cleanupSendFiles()
            ServerState.send_in_progress = false
            ServerState.send_cancel_started_at = nil
            power.release("send")
            notifyActivityChanged()
            deps.UIManager:show(deps.Notification:new({
                text = deps._("Send cancelled"),
                timeout = 2,
            }))
            if callback then
                callback(false, deps._("Cancelled"))
            end
            return
        end

        if isSendProcessRunning() or (isSendSupervisorRunning() and not deps.util.pathExists(exit_file)) then
            -- Still running, or the supervisor is completing the exit-status handoff.
            deps.UIManager:scheduleIn(constants.SEND_POLL_INTERVAL, checkSendComplete)
            return
        end

        -- Send complete
        closeSendDialog()
        ServerState.send_in_progress = false
        power.release("send")
        notifyActivityChanged()

        -- Check exit code
        local exit_code = nil
        if deps.util.pathExists(exit_file) then
            local exit_content = deps.util.readFromFile(exit_file)
            if exit_content then
                exit_code = tonumber(exit_content:match("^(%d+)"))
            end
            os.remove(exit_file)
        end

        -- Read output
        local output = deps.util.readFromFile(constants.SEND_OUTPUT_FILE) or ""

        -- Clean up temp files
        os.remove(constants.SEND_OUTPUT_FILE)
        os.remove(constants.SEND_PID_FILE)
        os.remove(constants.SEND_SUPERVISOR_PID_FILE)

        -- Determine success and message
        local success = (exit_code == 0)
        local message

        if success then
            message = deps.T(deps._("Sent %1 to %2"), filename, device.alias)
            deps.UIManager:show(deps.Notification:new({
                text = message,
                timeout = 3,
            }))
        else
            -- Use categorizeError for consistent error handling
            local error_category = M.categorizeError(output)
            if error_category == "rejected" then
                message = deps._("Transfer was rejected by the recipient")
            elseif error_category == "wrong_pin" or error_category == "pin_required" then
                recordOutcome(false, deps._("PIN required"), output, exit_code, "awaiting_pin")
                -- Prompt for PIN and retry
                M.showPINDialog(device, function(entered_pin)
                    if entered_pin and entered_pin ~= "" then
                        -- Retry with PIN
                        M.sendFile(device, filepath, entered_pin, callback, options)
                    else
                        -- User cancelled PIN entry
                        deps.UIManager:show(deps.Notification:new({
                            text = deps._("Send cancelled"),
                            timeout = 2,
                        }))
                        if callback then
                            callback(false, "Cancelled")
                        end
                    end
                end)
                return -- Don't show error or call callback - PIN dialog handles it
            elseif error_category == "connection_refused" then
                message = deps._("Device is not running LocalSend")
            elseif error_category == "rate_limited" then
                message = deps._("Too many failed attempts - try again later")
            elseif error_category == "connection" then
                message = deps._("Connection failed")
            elseif error_category == "timeout" then
                message = deps._("Connection timed out")
            else
                message = deps._("Send failed")
            end

            deps.UIManager:show(deps.InfoMessage:new({
                icon = "notice-warning",
                text = message,
                timeout = 4,
            }))
        end

        -- Record the outcome so diagnostics can report send-side
        -- health (ServerState persists across widget recreations).
        recordOutcome(success, message, output, exit_code)

        if callback then
            callback(success, message)
        end
    end

    -- Start polling after a short delay
    deps.UIManager:scheduleIn(constants.SEND_POLL_INTERVAL, checkSendComplete)
end

-- Show picker for sending a file or folder
-- @param device table Target device
-- @param start_path string Optional start path for picker
-- @param callback function Called with success boolean and message string
-- @param options table Optional settings: { device_name = string }
local function showFilePicker(device, start_path, callback, options)
    -- Default start path
    start_path = start_path or constants.DEFAULT_SAVE_DIR

    -- Ensure path exists
    if not deps.util.pathExists(start_path) then
        start_path = "/"
    end

    local picker = deps.PathChooser:new({
        path = start_path,
        select_file = true,
        -- CLI/protocol walk directories (preserve-structure by default); keep the
        -- picker aligned with the long-press "Send with LocalSend" button.
        select_directory = true,
        onConfirm = function(filepath)
            -- Send the selected file or folder
            M.sendFile(device, filepath, nil, callback, options)
        end,
        close_callback = function()
            if callback then
                callback(false, deps._("Cancelled"))
            end
        end,
    })
    deps.UIManager:show(picker)
end

-- Helper to extract common send flow options from instance
-- @param instance table LocalSend plugin instance
-- @return table scan_options, table send_options, string start_path
local function getSendFlowConfig(instance)
    local device_name = instance and instance.device_name
    local scan_options = {
        use_webrtc = instance and instance.use_webrtc,
        device_name = device_name, -- Pass to scan so we show correct name to peers
    }
    local send_options = { device_name = device_name }
    local start_path = instance:getPickerStartPath(instance.save_dir)
    return scan_options, send_options, start_path
end

-- Helper to scan for devices and show selector
-- @param instance table LocalSend plugin instance (for settings)
-- @param onDeviceSelected function Called with (device, send_options, start_path) when device selected
local function scanAndSelectDevice(instance, onDeviceSelected)
    local scan_options, send_options, start_path = getSendFlowConfig(instance)

    -- Show scanning dialog
    local scanning_dialog = discovery.showScanningDialog(function()
        discovery.cancelScan()
    end)

    -- Define scan completion handler (used for initial scan and retries)
    local function onScanComplete(devices)
        -- Close scanning dialog
        if scanning_dialog then
            deps.UIManager:close(scanning_dialog)
            scanning_dialog = nil
        end

        -- Retry callback for "Scan again" button
        local function onRetry()
            -- Show scanning dialog again
            scanning_dialog = discovery.showScanningDialog(function()
                discovery.cancelScan()
            end)
            -- Rescan
            discovery.scanDevices(onScanComplete, scan_options)
        end

        -- Show device selector with retry option
        discovery.showDeviceSelector(devices, function(device)
            if device then
                onDeviceSelected(device, send_options, start_path)
            end
        end, onRetry)
    end

    -- Start device scan
    discovery.scanDevices(onScanComplete, scan_options)
end

-- Helper to show device selector with cached devices (no rescan)
-- @param instance table LocalSend plugin instance (for settings)
-- @param onDeviceSelected function Called with (device, send_options, start_path) when device selected
local function selectCachedDevice(instance, onDeviceSelected)
    local _, send_options, start_path = getSendFlowConfig(instance)
    local devices = state.ServerState.discovered_devices or {}

    -- Retry callback triggers a fresh scan
    local function onRetry()
        scanAndSelectDevice(instance, onDeviceSelected)
    end

    discovery.showDeviceSelector(devices, function(device)
        if device then
            onDeviceSelected(device, send_options, start_path)
        end
    end, onRetry)
end

-- Forward declaration for mutual recursion
local showSendMoreDialog

-- Show "send more files?" dialog after successful send
-- @param instance table LocalSend plugin instance (for settings)
showSendMoreDialog = function(instance)
    -- Callback for after send completes - show dialog again on success
    local function onSendComplete(success, _)
        if success then
            showSendMoreDialog(instance)
        end
    end

    -- Handler when device is selected
    local function onDeviceSelected(device, send_options, start_path)
        showFilePicker(device, start_path, onSendComplete, send_options)
    end

    local dialog
    dialog = deps.ButtonDialog:new({
        title = deps._("Send more files?"),
        buttons = {
            {
                {
                    text = deps._("Yes"),
                    callback = function()
                        deps.UIManager:close(dialog)
                        selectCachedDevice(instance, onDeviceSelected)
                    end,
                },
            },
            {
                {
                    text = deps._("Scan again"),
                    callback = function()
                        deps.UIManager:close(dialog)
                        scanAndSelectDevice(instance, onDeviceSelected)
                    end,
                },
            },
            {
                {
                    text = deps._("No"),
                    callback = function()
                        deps.UIManager:close(dialog)
                    end,
                },
            },
        },
    })
    deps.UIManager:show(dialog)
end

-- Main entry point: scan for devices, select one, choose file, and send
-- @param instance table LocalSend plugin instance (for accessing settings)
-- @param preset_file string Optional preset file path (e.g., current book)
function M.showFileSendFlow(instance, preset_file)
    local ServerState = state.ServerState

    -- Prevent concurrent sends
    if ServerState.send_in_progress then
        deps.UIManager:show(deps.InfoMessage:new({
            text = deps._("A send operation is already in progress"),
            timeout = 3,
        }))
        return
    end

    -- Ensure network is connected.
    -- Prefer willRerunWhenConnected for cleaner control flow in recursive entrypoints.
    if deps.NetworkMgr.willRerunWhenConnected then
        if deps.NetworkMgr:willRerunWhenConnected(function()
            M.showFileSendFlow(instance, preset_file)
        end) then
            return
        end
    elseif not deps.NetworkMgr:isConnected() then
        deps.NetworkMgr:runWhenConnected(function()
            M.showFileSendFlow(instance, preset_file)
        end)
        return
    end

    -- Handler when device is selected
    local function onDeviceSelected(device, send_options, start_path)
        -- Callback to show "send more?" dialog on success
        local function onSendComplete(success, _)
            if success then
                showSendMoreDialog(instance)
            end
        end

        if preset_file then
            -- Send preset file directly
            M.sendFile(device, preset_file, nil, onSendComplete, send_options)
        else
            -- Show file picker
            showFilePicker(device, start_path, onSendComplete, send_options)
        end
    end

    scanAndSelectDevice(instance, onDeviceSelected)
end

-- Cancel an in-progress send. The polling completion path owns final cleanup
-- and the power hold so it cannot race an orphaned sender.
function M.cancelSend()
    local ServerState = state.ServerState
    if not ServerState.send_in_progress then
        power.release("send")
        return
    end

    ServerState.send_cancelled = true
    ServerState.send_cancel_started_at = ServerState.send_cancel_started_at or os.time()
    signalSendProcess("TERM", true)
end

-- Synchronous teardown for PluginLoader/KOReader exit. No UIManager callback is
-- allowed to be required after this returns.
function M.stopActiveOperationsSync()
    local ServerState = state.ServerState

    discovery.stopActiveScanSync(deps.usleep)
    ServerState.send_op_id = (ServerState.send_op_id or 0) + 1 -- invalidate poll callback

    if ServerState.send_in_progress or isSendProcessRunning() or isSendSupervisorRunning() then
        signalSendProcess("TERM", true)
        for _ = 1, 20 do
            if not isSendProcessRunning() then
                break
            end
            if deps.usleep then
                deps.usleep(50 * 1000)
            end
        end
        if isSendProcessRunning() then
            signalSendProcess("KILL", true)
            for _ = 1, 10 do
                if not isSendProcessRunning() then
                    break
                end
                if deps.usleep then
                    deps.usleep(50 * 1000)
                end
            end
        end
        -- Once the child is gone, the waiter should return immediately. Do not
        -- signal a supervisor PID from /tmp: if it were stale/reused, that could
        -- target an unrelated process.
        for _ = 1, 10 do
            if not isSendSupervisorRunning() then
                break
            end
            if deps.usleep then
                deps.usleep(50 * 1000)
            end
        end
    end

    cleanupSendFiles()
    ServerState.send_in_progress = false
    ServerState.send_cancelled = false
    ServerState.send_cancel_started_at = nil
    power.release("send")
    notifyActivityChanged()
end

-- Categorize an error message from send output
-- @param error_msg string Error message from CLI output
-- @return string Category: "pin_required", "wrong_pin", "rejected", "connection_refused",
--                          "rate_limited", "connection", "timeout", or "unknown"
function M.categorizeError(error_msg)
    if not error_msg or error_msg == "" then
        return "unknown"
    end

    local msg_lower = error_msg:lower()

    -- Rate limiting / too many attempts (check first - more specific)
    if msg_lower:match("too many") or msg_lower:match("blocked") or msg_lower:match("rate") then
        return "rate_limited"
    end

    -- PIN-related errors
    if msg_lower:match("pin required") or msg_lower:match("401") or msg_lower:match("invalid pin") then
        return "pin_required"
    elseif msg_lower:match("wrong pin") or msg_lower:match("incorrect pin") then
        return "wrong_pin"
    end

    -- Rejection errors
    if msg_lower:match("rejected") or msg_lower:match("declined") then
        return "rejected"
    end

    -- Connection refused (device not running - check before generic connection)
    if msg_lower:match("connection refused") or msg_lower:match("refused") then
        return "connection_refused"
    end

    -- Generic connection errors
    if msg_lower:match("connection") or msg_lower:match("connect") then
        return "connection"
    end

    -- Timeout errors
    if msg_lower:match("timeout") or msg_lower:match("timed out") then
        return "timeout"
    end

    return "unknown"
end

-- Show PIN input dialog (exposed for testing)
-- @param device table Device object
-- @param callback function Called with PIN string or nil if cancelled
function M.showPINDialog(device, callback)
    local dialog
    dialog = deps.InputDialog:new({
        title = deps.T(deps._("Enter PIN for %1"), device.alias),
        input_type = "number",
        input_hint = deps._("PIN code"),
        buttons = {
            {
                {
                    text = deps._("Cancel"),
                    id = "close",
                    callback = function()
                        deps.UIManager:close(dialog)
                        if callback then
                            callback(nil)
                        end
                    end,
                },
                {
                    text = deps._("OK"),
                    is_enter_default = true,
                    callback = function()
                        local pin = dialog:getInputText()
                        deps.UIManager:close(dialog)
                        if callback then
                            callback(pin)
                        end
                    end,
                },
            },
        },
    })
    deps.UIManager:show(dialog)
    dialog:onShowKeyboard()
end

return M
