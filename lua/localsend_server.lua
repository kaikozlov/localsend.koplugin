-- localsend_server.lua
-- Server lifecycle management for LocalSend plugin
-- Handles starting, stopping, and monitoring the LocalSend CLI process

local state = require("localsend_state")
local constants = require("localsend_constants")
local power = require("localsend_power")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}

-- Path configuration (set via M.init)
local binary_path = nil

-- Graceful-stop tuning
local STOP_POLL_ATTEMPTS = 20 -- 20 * 100ms = 2s

-- Synchronous-stop tuning (used when UIManager-driven polling may not run, e.g.
-- on KOReader exit or a forced plugin stop). usleep_fn is injected via M.init
-- (ffi/util.usleep); M._sync_usleep is a test seam to bypass real sleeping.
local SYNC_STOP_POLL_US = 50 * 1000 -- 50 ms per poll
local SYNC_STOP_GRACE_POLLS = 20 -- 20 * 50 ms = 1.0 s graceful window
local SYNC_STOP_KILL_POLLS = 10 -- 10 * 50 ms = 0.5 s after SIGKILL

local usleep_fn
M._sync_usleep = nil

local function sync_sleep(usec)
    if M._sync_usleep then
        M._sync_usleep(usec)
    elseif usleep_fn then
        usleep_fn(usec)
    end
end

local function readPIDFromFile()
    if not deps.util.pathExists(constants.PID_FILE) then
        return nil
    end

    local content = deps.util.readFromFile(constants.PID_FILE)
    if not content then
        return nil
    end

    return tonumber(content:match("^(%d+)"))
end

local function removePIDFile()
    if deps.util.pathExists(constants.PID_FILE) then
        os.remove(constants.PID_FILE)
    end
end

local function isProcessAlive(pid)
    return pid and deps.util.pathExists("/proc/" .. pid)
end

local function readProcessCmdline(pid)
    if not pid then
        return nil
    end

    return deps.util.readFromFile("/proc/" .. pid .. "/cmdline")
end

local function isLocalSendRecvProcess(pid)
    local cmdline = readProcessCmdline(pid)
    if not cmdline or cmdline == "" then
        return false
    end

    return cmdline:match("localsend") ~= nil and cmdline:match("recv") ~= nil
end

local function nextServerOpID()
    local ServerState = state.ServerState
    ServerState.server_op_id = (ServerState.server_op_id or 0) + 1
    return ServerState.server_op_id
end

local function isCurrentServerOp(op_id)
    return state.ServerState.server_op_id == op_id
end

-- Initialize module with dependencies
-- @param d table Dependencies: { UIManager, InfoMessage, Notification, Device, PluginShare, util, logger, T, _ }
-- @param paths table Paths: { binary_path, plugin_path }
function M.init(d, paths)
    deps = d
    binary_path = paths.binary_path
    usleep_fn = d.usleep
end

-- Check if the server process is running
-- @return boolean True if server is running
function M.isRunning()
    local pid = readPIDFromFile()
    if not pid then
        return false
    end

    if not isProcessAlive(pid) then
        return false
    end

    return isLocalSendRecvProcess(pid)
end

-- Non-blocking server startup wait using UIManager scheduling
-- @param instance table LocalSend instance
-- @param attempts_remaining number Remaining attempts
-- @param silent boolean Suppress notifications
-- @param on_ready function Callback when server is ready
-- @param on_failure function Callback when startup fails
function M.waitForServerReady(instance, attempts_remaining, silent, on_ready, on_failure)
    if attempts_remaining <= 0 then
        on_failure()
        return
    end
    if instance:isRunning() then
        on_ready()
        return
    end
    -- Non-blocking: schedule next check in 100ms
    deps.UIManager:scheduleIn(0.1, function()
        M.waitForServerReady(instance, attempts_remaining - 1, silent, on_ready, on_failure)
    end)
end

-- Non-blocking process exit wait using UIManager scheduling
-- @param pid number Process ID to wait for
-- @param attempts_remaining number Remaining attempts
-- @param force boolean Force kill with SIGKILL if timeout
-- @param callback function Callback with success boolean
function M.waitForProcessExit(pid, attempts_remaining, force, callback)
    local function isProcAlive(p)
        return p and deps.util.pathExists("/proc/" .. p)
    end

    if not isProcAlive(pid) then
        callback(true) -- Process exited successfully
        return
    end

    if attempts_remaining <= 0 then
        if force then
            -- Force kill with SIGKILL
            os.execute(deps.util.shell_escape({ "kill", "-KILL", tostring(pid) }))
            -- Give one more brief check after SIGKILL
            deps.UIManager:scheduleIn(0.2, function()
                callback(not isProcAlive(pid))
            end)
        else
            callback(false) -- Process did not exit
        end
        return
    end

    -- Schedule next check in 100ms
    deps.UIManager:scheduleIn(0.1, function()
        M.waitForProcessExit(pid, attempts_remaining - 1, force, callback)
    end)
end

-- Reconcile plugin state with actual process state to avoid UI/state drift.
-- @param instance table LocalSend instance
-- @return boolean running
function M.reconcileServerState(instance)
    local running = M.isRunning()
    local ServerState = state.ServerState

    if not running then
        ServerState.stop_in_progress = false
        deps.PluginShare.localsend_running = nil
        power.release("receive")
        os.remove(constants.TRANSFER_BUSY_FILE)
    else
        deps.PluginShare.localsend_running = true
    end

    instance:_updateCache()
    instance:registerEvents()
    return running
end

-- Called when server has been confirmed running after startup
-- @param instance table LocalSend instance
-- @param silent boolean Suppress notifications
-- @param effective_name string The device name used
function M.onServerStarted(instance, silent, effective_name)
    local ServerState = state.ServerState

    ServerState.stop_in_progress = false
    state.recordLifecycle("server_ready", "name=" .. tostring(effective_name) .. " port=" .. tostring(instance.port))
    M.reconcileServerState(instance)

    -- Recreate task references if they were nullified by onCloseWidget
    -- This can happen during suspend/resume cycles when the widget is closed
    if not instance.check_sentinel_task then
        instance.check_sentinel_task = function()
            instance:_checkSentinelFile()
        end
    end

    -- Start fast sentinel polling for responsive notifications
    instance:_unschedulePolling() -- Ensure no duplicate polling
    ServerState.last_sentinel_value = nil -- Reset to pick up current state
    deps.UIManager:scheduleIn(constants.SENTINEL_POLL_INTERVAL, instance.check_sentinel_task)

    if not silent then
        -- Build concise startup message for top notification
        local network_info = deps.Device.retrieveNetworkInfo and deps.Device:retrieveNetworkInfo() or nil
        local pin_status = instance.pin ~= "" and deps._("PIN") or nil

        local message_parts = { effective_name }

        -- Try to extract IP and show with port for manual connection
        local ip_addr = network_info and network_info:match("(%d+%.%d+%.%d+%.%d+)")
        if ip_addr then
            table.insert(message_parts, ip_addr .. ":" .. instance.port)
        end

        if pin_status then
            table.insert(message_parts, pin_status)
        end

        deps.UIManager:show(deps.Notification:new({
            text = deps._("LocalSend Ready") .. " - " .. table.concat(message_parts, " | "),
            timeout = 5,
        }))
    else
        deps.logger.dbg("[LocalSend] Server restarted after resume")
    end
end

-- Called when server startup has failed
-- @param instance table LocalSend instance
-- @param silent boolean Suppress notifications
function M.onServerStartFailed(instance, silent)
    state.recordLifecycle("server_start_failed", "readiness timeout")
    instance:closeFirewall()
    state.ServerState.stop_in_progress = false
    M.reconcileServerState(instance)

    if not silent then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("LocalSend process failed to start within 5 seconds. Check if the binary works."),
        }))
    else
        deps.logger.warn("[LocalSend] Failed to restart server after resume")
    end
end

-- Start the LocalSend server
-- @param instance table LocalSend instance
-- @param silent boolean If true, suppress the startup notification
function M.start(instance, silent)
    local ServerState = state.ServerState

    if ServerState.stop_in_progress then
        if not silent then
            deps.UIManager:show(deps.InfoMessage:new({
                text = deps._("LocalSend is stopping. Please wait a moment and try again."),
                timeout = 3,
            }))
        end
        return
    end

    local op_id = nextServerOpID()
    state.recordLifecycle("server_start_requested", "silent=" .. tostring(not not silent))

    -- If server is already running, just take over polling responsibility
    if instance:isRunning() then
        state.recordLifecycle("server_already_running")
        deps.logger.dbg("[LocalSend] Server already running, taking over polling")
        -- Sync cache with actual state
        M.reconcileServerState(instance)
        -- Start sentinel polling for fast notifications
        instance:_unschedulePolling()
        ServerState.last_sentinel_value = nil
        deps.UIManager:scheduleIn(constants.SENTINEL_POLL_INTERVAL, instance.check_sentinel_task)
        return
    end

    -- Validate save directory
    local valid, err = instance:validateSaveDir(instance.save_dir)
    if not valid then
        state.recordLifecycle("server_start_rejected", "invalid save directory: " .. tostring(err))
        if not silent then
            deps.UIManager:show(deps.InfoMessage:new({
                icon = "notice-warning",
                text = deps.T(deps._("Invalid save directory: %1"), err),
            }))
        end
        return
    end

    -- Clear old transfer log and reset count (only on fresh start, not resume)
    if not silent then
        instance:clearTransferLog()
    end

    -- Build command arguments table
    local args = { binary_path, "recv", "-d", instance.save_dir, "-l", constants.TRANSFER_LOG_FILE }

    -- Always pass device name (default to "KOReader" if not set)
    local effective_name = instance.device_name ~= "" and instance.device_name or "KOReader"
    table.insert(args, "-n")
    table.insert(args, effective_name)

    if instance.pin ~= "" then
        table.insert(args, "-p")
        table.insert(args, instance.pin)
    end

    -- Determine accept_ext based on routing or manual setting
    local effective_accept_ext = instance.accept_ext
    if instance.routing_enabled and next(instance.ext_dirs) then
        -- Routing is active: accept only routed extensions (unless accept_all is enabled)
        if not instance.routing_accept_all then
            local exts = {}
            for ext, _ in pairs(instance.ext_dirs) do
                table.insert(exts, ext)
            end
            effective_accept_ext = table.concat(exts, ",")
        else
            effective_accept_ext = "" -- Accept all
        end
    end

    if effective_accept_ext ~= "" then
        table.insert(args, "-a")
        table.insert(args, effective_accept_ext)
    end

    if not instance.use_https then
        table.insert(args, "--https=false")
    end

    if not instance.use_webrtc then
        table.insert(args, "-w=false")
    end

    -- Export and apply extension routing config if configured
    local routing_path = instance:exportExtRouting()
    if routing_path then
        table.insert(args, "--ext-routing")
        table.insert(args, routing_path)
    end

    -- Native integration markers avoid assumptions about vendor-shell date(1).
    table.insert(args, "--notify-file")
    table.insert(args, constants.TRANSFER_NOTIFY_FILE)
    table.insert(args, "--busy-file")
    table.insert(args, constants.TRANSFER_BUSY_FILE)

    -- Write signaling ID to file for self-filtering in scan (WebRTC mode only)
    if instance.use_webrtc then
        table.insert(args, "--signaling-id-file")
        table.insert(args, constants.SIGNALING_ID_FILE)
    end

    -- Open firewall before starting
    instance:openFirewall()

    -- Build final command: capture backend logs, run in background, and save PID
    local cmd = string.format(
        "(exec %s > %s 2>&1) & echo $! > %s",
        deps.util.shell_escape(args),
        deps.util.shell_escape({ constants.SERVER_OUTPUT_FILE }),
        deps.util.shell_escape({ constants.PID_FILE })
    )

    deps.logger.dbg("[LocalSend] Starting server: ", cmd)

    local result = os.execute(cmd)

    if result == 0 then
        -- Non-blocking wait for server readiness (max 5 seconds = 50 * 100ms)
        M.waitForServerReady(
            instance,
            50,
            silent,
            -- on_ready callback
            function()
                if not isCurrentServerOp(op_id) then
                    return
                end
                M.onServerStarted(instance, silent, effective_name)
            end,
            -- on_failure callback
            function()
                if not isCurrentServerOp(op_id) then
                    return
                end
                M.onServerStartFailed(instance, silent)
            end
        )
    else
        state.recordLifecycle("server_start_failed", "launcher exit status=" .. tostring(result))
        instance:closeFirewall()
        M.reconcileServerState(instance)
        if not silent then
            local info = deps.InfoMessage:new({
                icon = "notice-warning",
                text = deps._("Failed to start LocalSend server."),
            })
            deps.UIManager:show(info)
        else
            deps.logger.warn("[LocalSend] Failed to start server after resume")
        end
    end
end

-- Synchronously terminate a LocalSend receiver process without relying on
-- UIManager scheduling. Used on hard teardown (KOReader exit, forced plugin
-- stop) where scheduled follow-ups may never run. Sends SIGTERM, polls for
-- exit, then escalates to SIGKILL. Blocks the caller for up to roughly
-- (SYNC_STOP_GRACE_POLLS + SYNC_STOP_KILL_POLLS) * SYNC_STOP_POLL_US.
-- @param pid number Process ID to stop (already validated as ours).
-- @param finalizeStopped function Removes PID file, closes firewall, finishes.
-- @param finish function(success, message) Reports completion / failure.
-- @return boolean True if the process was stopped (or already gone).
local function stopServerSync(pid, finalizeStopped, finish)
    os.execute(deps.util.shell_escape({ "kill", "-TERM", tostring(pid) }) .. " 2>/dev/null")

    for _ = 1, SYNC_STOP_GRACE_POLLS do
        if not isProcessAlive(pid) then
            finalizeStopped()
            return true
        end
        sync_sleep(SYNC_STOP_POLL_US)
    end

    deps.logger.warn("[LocalSend] Synchronous graceful stop timed out, forcing kill", "pid", pid)
    os.execute(deps.util.shell_escape({ "kill", "-KILL", tostring(pid) }) .. " 2>/dev/null")
    for _ = 1, SYNC_STOP_KILL_POLLS do
        if not isProcessAlive(pid) then
            finalizeStopped()
            return true
        end
        sync_sleep(SYNC_STOP_POLL_US)
    end

    if isProcessAlive(pid) then
        deps.logger.err("[LocalSend] Synchronous stop failed to kill process", "pid", pid)
        finish(false, deps._("Failed to stop LocalSend process."))
        return false
    end
    finalizeStopped()
    return true
end

-- Stop the LocalSend server process gracefully (SIGTERM first, SIGKILL fallback).
-- @param instance table LocalSend instance
-- @param options table|nil Optional: { callback = function(success, message), sync = bool }
-- @return boolean True if stop operation was initiated
function M.stopServer(instance, options)
    options = options or {}
    local callback = options.callback
    local sync = options.sync
    local ServerState = state.ServerState
    local op_id = nextServerOpID()
    state.recordLifecycle("server_stop_requested", "sync=" .. tostring(not not sync))

    -- Unschedule Lua tasks first
    instance:_unschedulePolling()

    ServerState.stop_in_progress = true
    M.reconcileServerState(instance)

    local function finish(success, message)
        if not isCurrentServerOp(op_id) then
            return
        end

        ServerState.stop_in_progress = false
        state.recordLifecycle(success and "server_stopped" or "server_stop_failed", message)
        M.reconcileServerState(instance)

        if callback then
            callback(success, message)
        end
    end

    local function finalizeStopped()
        removePIDFile()
        instance:closeFirewall()
        finish(true)
    end

    local pid = readPIDFromFile()
    if not pid then
        finalizeStopped()
        return true
    end

    if not isProcessAlive(pid) then
        finalizeStopped()
        return true
    end

    if not isLocalSendRecvProcess(pid) then
        deps.logger.warn("[LocalSend] PID file points to non-LocalSend process; refusing to kill", "pid", pid)
        finalizeStopped()
        return true
    end

    if sync then
        -- Hard teardown (KOReader exit / forced stop): UIManager-driven polling
        -- may never run, so kill synchronously before returning.
        return stopServerSync(pid, finalizeStopped, finish)
    end

    -- Attempt graceful shutdown first
    os.execute(deps.util.shell_escape({ "kill", "-TERM", tostring(pid) }) .. " 2>/dev/null")

    M.waitForProcessExit(pid, STOP_POLL_ATTEMPTS, false, function(exited)
        if not isCurrentServerOp(op_id) then
            return
        end

        if exited then
            finalizeStopped()
            return
        end

        deps.logger.warn("[LocalSend] Graceful stop timed out, forcing kill", "pid", pid)
        M.waitForProcessExit(pid, 0, true, function(killed)
            if not isCurrentServerOp(op_id) then
                return
            end

            if killed then
                finalizeStopped()
                return
            end

            deps.logger.err("[LocalSend] Failed to stop process", "pid", pid)
            finish(false, deps._("Failed to stop LocalSend process."))
        end)
    end)

    return true
end

-- Clean up server state after stopping (PluginShare, cache, events)
-- @param instance table LocalSend instance
function M.cleanupServerState(instance)
    M.reconcileServerState(instance)
end

-- User-initiated stop with notification
-- @param instance table LocalSend instance
function M.stop(instance)
    local ServerState = state.ServerState
    -- Mark that user explicitly stopped the server this session
    -- This prevents autostart from restarting it when opening a new document
    ServerState.user_stopped = true
    instance:stopServer({
        callback = function(success, message)
            if success then
                deps.UIManager:show(deps.Notification:new({
                    text = deps._("LocalSend stopped"),
                    timeout = 2,
                }))
            else
                deps.UIManager:show(deps.InfoMessage:new({
                    icon = "notice-warning",
                    text = message or deps._("Failed to stop LocalSend process."),
                    timeout = 4,
                }))
            end
        end,
    })
end

-- Restart the server
-- @param instance table LocalSend instance
function M.restart(instance)
    local ServerState = state.ServerState
    ServerState.user_stopped = false

    if instance:isRunning() or ServerState.stop_in_progress then
        instance:stopServer({
            callback = function(success)
                if success then
                    instance:start()
                end
            end,
        })
        return
    end

    instance:start()
end

-- Toggle server state (start if stopped, stop if running)
-- @param instance table LocalSend instance
function M.toggle(instance)
    local ServerState = state.ServerState

    if ServerState.stop_in_progress then
        deps.UIManager:show(deps.InfoMessage:new({
            text = deps._("LocalSend is stopping. Please wait."),
            timeout = 3,
        }))
        return
    end

    if instance:isRunning() then
        instance:stop()
    else
        -- User is explicitly starting the server, clear the stopped flag
        -- so autostart can work again if they open another document
        ServerState.user_stopped = false
        -- Use _startWhenConnected(false) to prompt for WiFi if offline
        instance:_startWhenConnected(false)
    end
end

-- Clean up orphaned resources from previous crashes (stale PID file)
-- @param instance table LocalSend instance (for closeFirewall callback)
-- @return boolean True if cleanup was needed
function M.cleanupOrphanedResources(instance)
    local pid = readPIDFromFile()
    if not pid then
        if deps.util.pathExists(constants.PID_FILE) then
            removePIDFile()
            return true
        end
        return false
    end

    if not isProcessAlive(pid) then
        deps.logger.warn("[LocalSend] Found stale PID file, cleaning up")
        removePIDFile()
        -- Also clean up firewall rules (they may be orphaned)
        instance:closeFirewall()
        return true
    end

    if not isLocalSendRecvProcess(pid) then
        deps.logger.warn("[LocalSend] PID file points to unrelated process, cleaning up stale state", "pid", pid)
        removePIDFile()
        instance:closeFirewall()
        return true
    end

    return false
end

return M
