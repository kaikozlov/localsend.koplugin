local DataStorage = require("datastorage")
local Device = require("device")
local Dispatcher = require("dispatcher")
local InfoMessage = require("ui/widget/infomessage")
local Notification = require("ui/widget/notification")
local InputDialog = require("ui/widget/inputdialog")
local ButtonDialog = require("ui/widget/buttondialog")
local NetworkMgr = require("ui/network/manager")
local PathChooser = require("ui/widget/pathchooser")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local ffiutil = require("ffi/util")
local logger = require("logger")
local util = require("util")
local _ = require("gettext")
local T = ffiutil.template
local json = require("json")
local PluginShare = require("pluginshare")

-- Critical modules (required for recovery mode)
local lsutils = require("localsend_utils")
local lsupdate = require("localsend_update")
local constants = require("localsend_constants")

-- Optional modules - load with pcall for graceful degradation
local RECOVERY_MODE = false
local module_load_errors = {}

local function tryRequire(name)
    local ok, mod = pcall(require, name)
    if ok then
        return mod
    else
        table.insert(module_load_errors, name .. ": " .. tostring(mod))
        return nil
    end
end

local state = tryRequire("localsend_state")
local lsrouting = tryRequire("localsend_routing")
local lstransfers = tryRequire("localsend_transfers")
local lsdialogs = tryRequire("localsend_dialogs")
local lsfirewall = tryRequire("localsend_firewall")
local lsserver = tryRequire("localsend_server")
local lssender = tryRequire("localsend_sender")
local lsdiagnostics = tryRequire("localsend_diagnostics")

-- Check if any critical optional modules failed
if not state or not lsserver then
    RECOVERY_MODE = true
    logger.warn("[LocalSend] Entering recovery mode due to missing modules")
    for _, err in ipairs(module_load_errors) do
        logger.warn("[LocalSend] Module load error:", err)
    end
end

-- Import utility functions from localsend_utils module
local isValidPath = lsutils.isValidPath
local isValidPort = lsutils.isValidPort
local validateDeviceName = lsutils.validateDeviceName

local data_dir = DataStorage:getFullDataDir()
local cache_dir = data_dir .. "/cache"
local plugin_path = data_dir .. "/plugins/localsend.koplugin"

-- ServerState is now in localsend_state.lua module (nil in recovery mode)
local ServerState = state and state.ServerState or nil

-- Log the ServerState table address to verify it persists across widget instances
if ServerState then
    logger.warn("[LocalSend] Module loaded, ServerState table:", tostring(ServerState))
end

-- Load plugin metadata safely (wrap dofile in pcall)
local plugin_meta
local meta_path = plugin_path .. "/_meta.lua"
local ok, result = pcall(dofile, meta_path)
if ok and type(result) == "table" then
    plugin_meta = result
else
    logger.warn("[LocalSend] Failed to load _meta.lua:", result)
    plugin_meta = { version = "unknown", name = "LocalSend" }
end
local PLUGIN_VERSION = plugin_meta.version or "unknown"
local GITHUB_URL = "https://github.com/kaikozlov/localsend.koplugin"
local GITHUB_URL_DISPLAY = "github.com/kaikozlov/localsend.koplugin"
local binary_path = plugin_path .. "/localsend"
local certs_path = plugin_path .. "/certs" -- Certs folder next to binary (managed by Go)

-- Check if a previous update failed and reinstall is required
local REINSTALL_REQUIRED = lsupdate.isReinstallRequired(plugin_path)
if REINSTALL_REQUIRED then
    logger.warn("[LocalSend] Reinstall required due to previous failed update")
end

-- Check if binary exists
if not util.pathExists(binary_path) then
    return { disabled = true }
end

-- Helper function to initialize the update module (used in both normal and recovery mode)
local function initUpdateModule()
    lsupdate.init({
        UIManager = UIManager,
        InfoMessage = InfoMessage,
        Notification = Notification,
        NetworkMgr = NetworkMgr,
        util = util,
        ffiutil = ffiutil,
        json = json,
        logger = logger,
        T = T,
        _ = _,
        G_reader_settings = G_reader_settings,
        cache_dir = cache_dir,
    })
end

local LocalSend = WidgetContainer:extend({
    name = "LocalSend",
    is_doc_only = false,
})

-- =============================================================================
-- WIDGET LIFECYCLE WARNING
-- =============================================================================
-- This init() function runs on EVERY widget recreation, including:
--   - Opening a different book
--   - Switching between file manager and reader views
--   - Some suspend/resume scenarios
--
-- DO NOT add code with side effects (network prompts, UI dialogs, notifications)
-- without proper guards. Use ServerState flags to ensure once-per-session behavior.
-- =============================================================================

function LocalSend:init()
    logger.dbg("[LocalSend] init() starting")

    -- In recovery mode, only initialize update functionality
    if RECOVERY_MODE then
        self:_initRecoveryMode()
        return
    end

    -- The Go backend always binds the LocalSend protocol port (recv has no port
    -- flag), so a stale LocalSend_port setting must not make the firewall rules
    -- and diagnostics diverge from the port actually in use. The legacy key is
    -- still cleared by deletePluginSettings.
    self.port = constants.DEFAULT_PORT
    self.save_dir = G_reader_settings:readSetting("LocalSend_save_dir") or constants.DEFAULT_SAVE_DIR
    self.device_name = G_reader_settings:readSetting("LocalSend_device_name") or ""
    self.use_https = G_reader_settings:nilOrTrue("LocalSend_use_https")
    self.autostart = G_reader_settings:isTrue("LocalSend_autostart")
    self.pin = G_reader_settings:readSetting("LocalSend_pin") or ""
    self.accept_ext = G_reader_settings:readSetting("LocalSend_accept_ext") or ""
    self.use_webrtc = G_reader_settings:isTrue("LocalSend_use_webrtc") -- Experimental, off by default
    self.ext_dirs = G_reader_settings:readSetting("LocalSend_ext_dirs") or {} -- Extension routing: ext -> dir
    self.routing_accept_all = G_reader_settings:isTrue("LocalSend_routing_accept_all") -- Accept unrouted files to default dir
    self.routing_enabled = G_reader_settings:isTrue("LocalSend_routing_enabled") -- Whether routing is active

    -- Auto update check settings
    self.auto_update_check = G_reader_settings:nilOrTrue("LocalSend_auto_update_check")
    self.update_check_interval_hours = G_reader_settings:readSetting("LocalSend_update_check_interval_hours") or constants.DEFAULT_UPDATE_CHECK_INTERVAL_HOURS
    self.last_update_check = G_reader_settings:readSetting("LocalSend_last_update_check") or 0
    self.update_available_tag = G_reader_settings:readSetting("LocalSend_update_available_tag") or ""

    -- Initialize update module with dependencies
    initUpdateModule()

    -- Clear Kindle telemetry files once per session (no-op on non-Kindle)
    -- These fm-out-* files accumulate in /tmp and can fill the 64MB tmpfs
    -- Guard with ServerState flag to avoid unnecessary disk I/O on every widget recreation
    if not ServerState.telemetry_cleaned then
        ServerState.telemetry_cleaned = true
        lsupdate.clearTmpTelemetryFiles()
    end

    -- Initialize optional modules with dependencies (guarded for recovery mode)
    if lsrouting then
        lsrouting.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            InputDialog = InputDialog,
            PathChooser = PathChooser,
            json = json,
            logger = logger,
            T = T,
            _ = _,
            G_reader_settings = G_reader_settings,
        })
    end

    if lstransfers then
        lstransfers.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            util = util,
            json = json,
            logger = logger,
            T = T,
            _ = _,
        })
    end

    if lsdialogs then
        lsdialogs.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            InputDialog = InputDialog,
            PathChooser = PathChooser,
            util = util,
            logger = logger,
            T = T,
            _ = _,
            G_reader_settings = G_reader_settings,
        })
    end

    if lsfirewall then
        lsfirewall.init({
            Device = Device,
            util = util,
            logger = logger,
        })
    end

    if lsserver then
        lsserver.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            Device = Device,
            PluginShare = PluginShare,
            util = util,
            logger = logger,
            T = T,
            _ = _,
        }, {
            binary_path = binary_path,
            plugin_path = plugin_path,
        })
    end

    if lssender then
        lssender.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            InputDialog = InputDialog,
            ButtonDialog = ButtonDialog,
            PathChooser = PathChooser,
            NetworkMgr = NetworkMgr,
            util = util,
            json = json,
            logger = logger,
            T = T,
            _ = _,
        }, {
            binary_path = binary_path,
        })
    end

    if lsdiagnostics then
        lsdiagnostics.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            Device = Device,
            NetworkMgr = NetworkMgr,
            util = util,
            json = json,
            logger = logger,
            T = T,
            _ = _,
        }, {
            binary_path = binary_path,
            plugin_path = plugin_path,
            plugin_version = PLUGIN_VERSION,
            data_dir = data_dir,
        })
    end

    -- Cache for menu rendering (avoids disk I/O on every menu open)
    -- Updated via _updateCache() on state changes
    self._cached_running = false
    self._cached_stopping = false
    self._cached_transfer_count = 0

    -- Create instance-specific task references for proper unscheduling
    -- (See UIManager docs: anonymous functions cannot be unscheduled)
    self.check_sentinel_task = function()
        self:_checkSentinelFile()
    end
    self.resume_start_task = function()
        self:start(true) -- silent=true to suppress notification
    end
    self.check_update_task = function()
        self:_autoCheckForUpdates()
    end

    -- Clean up orphaned resources from previous crashes
    self:_cleanupOrphanedResources()

    -- Automatic server startup logic:
    -- 1. Resume after suspend (widget was destroyed) - silent restart
    -- 2. Autostart on fresh KOReader launch - silent (no WiFi prompts on startup)
    -- Both paths silently skip if offline - no WiFi prompts during widget recreation.
    -- Users can manually start the server from the menu if they want WiFi prompted.
    logger.dbg(
        "[LocalSend] init() autostart check:",
        "autostart=",
        tostring(self.autostart),
        "user_stopped=",
        tostring(ServerState.user_stopped),
        "was_running_before_suspend=",
        tostring(ServerState.was_running_before_suspend)
    )
    if ServerState.was_running_before_suspend and not ServerState.user_stopped then
        ServerState.was_running_before_suspend = false
        self:_startWhenConnected(true) -- silent - no notification, no WiFi prompt
    elseif self.autostart and not ServerState.user_stopped then
        self:_startWhenConnected(true) -- silent - no WiFi prompt (will start silently if connected)
    end

    -- Sync cache with actual state (server may be running from previous widget instance)
    self:_updateCache()

    -- Register event handlers based on current state
    self:registerEvents()

    -- Schedule auto update check if enabled
    if self.auto_update_check then
        self:_scheduleUpdateCheck()
    end

    self.ui.menu:registerToMainMenu(self)
    self:onDispatcherRegisterActions()
end

-- Recovery mode initialization - minimal setup for reinstall capability
function LocalSend:_initRecoveryMode()
    logger.warn("[LocalSend] Initializing in recovery mode")

    -- Initialize only the update module (critical for recovery)
    initUpdateModule()

    -- Clear Kindle telemetry files even in recovery mode.
    -- No ServerState guard here because:
    -- 1. In recovery mode, ServerState is nil (the state module failed to load)
    -- 2. This is an idempotent operation - safe to run on every widget recreation
    -- 3. /tmp filling up affects device stability regardless of plugin state
    lsupdate.clearTmpTelemetryFiles()

    self.ui.menu:registerToMainMenu(self)
end

-- Clean up orphaned resources from previous crashes (stale PID file, firewall rules)
function LocalSend:_cleanupOrphanedResources()
    lsserver.cleanupOrphanedResources(self)
end

-- Cleanup when KOReader exits (not when switching documents)
-- Note: onCloseWidget is called when switching books, so we don't stop the server there.
-- Instead, we stop on Exit event which is only triggered when KOReader actually closes.
function LocalSend:onExit()
    if self:isRunning() then
        self:stopServer()
        logger.dbg("[LocalSend] Server stopped on KOReader exit")
    end
end

-- Dynamic event registration (KOSync pattern)
-- Only register power/network handlers when server is running or expected to run
-- This reduces event processing overhead when the plugin is idle
function LocalSend:registerEvents()
    -- Keep handlers registered if:
    -- 1. Server is currently running
    -- 2. Autostart is enabled and user hasn't stopped it
    -- 3. Server was running before suspend/disconnect (so we can restart on resume)
    local should_register = self:isRunning()
        or (self.autostart and not ServerState.user_stopped)
        or ServerState.was_running_before_suspend
        or ServerState.was_running_before_disconnect
    if should_register then
        -- Server running or expected to run: register handlers
        self.onSuspend = self._onSuspend
        self.onResume = self._onResume
        self.onEnterStandby = self._onEnterStandby
        self.onLeaveStandby = self._onLeaveStandby
        self.onNetworkDisconnected = self._onNetworkDisconnected
        self.onNetworkConnected = self._onNetworkConnected
        logger.dbg("[LocalSend] Event handlers registered")
    else
        -- Server not running: unregister handlers to reduce overhead
        self.onSuspend = nil
        self.onResume = nil
        self.onEnterStandby = nil
        self.onLeaveStandby = nil
        self.onNetworkDisconnected = nil
        self.onNetworkConnected = nil
        logger.dbg("[LocalSend] Event handlers unregistered")
    end
end

-- Event handler implementations (underscore-prefixed for dynamic registration)
-- Stop server before device suspends (WiFi will be disabled)
function LocalSend:_onSuspend()
    logger.dbg("[LocalSend] onSuspend")
    -- Unschedule polling before stopping
    self:_unschedulePolling()
    self:_unscheduleResume()
    self:_unscheduleUpdateCheck()

    if self:isRunning() then
        ServerState.was_running_before_suspend = true
        self:stopServer()
        logger.dbg("[LocalSend] Server stopped for suspend")
    else
        ServerState.was_running_before_suspend = false
    end
end

-- Restart server after device resumes (if it was running before)
function LocalSend:_onResume()
    logger.dbg("[LocalSend] onResume")

    -- Reschedule update check after resume
    if self.auto_update_check then
        self:_scheduleUpdateCheck()
    end

    if ServerState.was_running_before_suspend and not ServerState.user_stopped then
        if NetworkMgr:isConnected() then
            -- Network already available (fast reconnect or didn't disconnect)
            ServerState.was_running_before_suspend = false
            self:start(true) -- silent=true to suppress notification
        else
            -- Network not ready yet - keep flag set and let _onNetworkConnected handle it
            logger.dbg("[LocalSend] Waiting for network to restart server")
        end
    end
end

-- Same handling for standby (light sleep)
function LocalSend:_onEnterStandby()
    logger.dbg("[LocalSend] onEnterStandby")
    -- Unschedule polling before stopping
    self:_unschedulePolling()

    if self:isRunning() then
        ServerState.was_running_before_suspend = true
        self:stopServer()
        logger.dbg("[LocalSend] Server stopped for standby")
    else
        ServerState.was_running_before_suspend = false
    end
end

function LocalSend:_onLeaveStandby()
    logger.dbg("[LocalSend] onLeaveStandby")
    if ServerState.was_running_before_suspend and not ServerState.user_stopped then
        if NetworkMgr:isConnected() then
            -- Network already available
            ServerState.was_running_before_suspend = false
            self:start(true) -- silent=true to suppress notification
        else
            -- Network not ready yet - keep flag set and let _onNetworkConnected handle it
            logger.dbg("[LocalSend] Waiting for network to restart server")
        end
    end
end

-- Handle network disconnect (e.g., user manually turns off WiFi)
function LocalSend:_onNetworkDisconnected()
    logger.dbg("[LocalSend] onNetworkDisconnected")
    if self:isRunning() then
        ServerState.was_running_before_disconnect = true
        self:stopServer()
        logger.dbg("[LocalSend] Server stopped due to network disconnect")
    else
        ServerState.was_running_before_disconnect = false
    end
end

-- Handle network reconnect
function LocalSend:_onNetworkConnected()
    logger.dbg("[LocalSend] onNetworkConnected")
    -- Restart if we were waiting for network after suspend OR after disconnect
    local should_restart = (ServerState.was_running_before_suspend or ServerState.was_running_before_disconnect) and not ServerState.user_stopped
    if should_restart then
        -- Clear both flags
        ServerState.was_running_before_suspend = false
        ServerState.was_running_before_disconnect = false
        self:start(true) -- silent=true to suppress notification
        logger.dbg("[LocalSend] Server restarted after network reconnect")
    end
end

-- Called by PluginLoader when user deletes the plugin via KOReader's plugin management.
-- Cleans up all persistent state: G_reader_settings keys, file-based state (certs,
-- ext_routing.json, reinstall marker, tmp files), and resets in-memory ServerState.
-- Must be safe to call multiple times (idempotent) and must not throw errors.
function LocalSend:deletePluginSettings()
    logger.info("[LocalSend] deletePluginSettings: removing all plugin data")

    -- 1. Remove all G_reader_settings keys
    local settings_keys = {
        "LocalSend_port",
        "LocalSend_save_dir",
        "LocalSend_device_name",
        "LocalSend_use_https",
        "LocalSend_autostart",
        "LocalSend_pin",
        "LocalSend_accept_ext",
        "LocalSend_use_webrtc",
        "LocalSend_ext_dirs",
        "LocalSend_routing_accept_all",
        "LocalSend_routing_enabled",
        "LocalSend_auto_update_check",
        "LocalSend_update_check_interval_hours",
        "LocalSend_last_update_check",
        "LocalSend_update_available_tag",
    }
    for _, key in ipairs(settings_keys) do
        G_reader_settings:delSetting(key)
    end

    -- 2. Remove file-based state in plugin directory
    local files_to_remove = {
        plugin_path .. "/ext_routing.json",
        plugin_path .. "/" .. lsupdate.REINSTALL_MARKER_FILE,
    }
    for _, filepath in ipairs(files_to_remove) do
        if util.pathExists(filepath) then
            local ok, err = os.remove(filepath)
            if not ok then
                logger.warn("[LocalSend] deletePluginSettings: failed to remove", filepath, err)
            end
        end
    end

    -- 3. Remove TLS certs directory
    if util.pathExists(certs_path) then
        local ok, err = ffiutil.purgeDir(certs_path)
        if not ok then
            logger.warn("[LocalSend] deletePluginSettings: failed to remove certs dir:", err)
        end
    end

    -- 4. Remove temporary runtime files (PID, logs, etc.)
    local tmp_files = {
        constants.PID_FILE,
        constants.TRANSFER_LOG_FILE,
        constants.TRANSFER_NOTIFY_FILE,
        constants.SIGNALING_ID_FILE,
        constants.SEND_PID_FILE,
        constants.SEND_OUTPUT_FILE,
        constants.SCAN_OUTPUT_FILE,
        constants.SERVER_OUTPUT_FILE,
    }
    for _, filepath in ipairs(tmp_files) do
        if util.pathExists(filepath) then
            local ok, err = os.remove(filepath)
            if not ok then
                logger.warn("[LocalSend] deletePluginSettings: failed to remove", filepath, err)
            end
        end
    end

    -- 5. Reset in-memory session state
    if ServerState then
        ServerState.user_stopped = false
        ServerState.was_running_before_suspend = false
        ServerState.was_running_before_disconnect = false
        ServerState.last_log_position = 0
        ServerState.transfer_count = 0
        ServerState.last_sentinel_value = nil
        ServerState.telemetry_cleaned = false
        ServerState.discovered_devices = {}
        ServerState.scan_in_progress = false
        ServerState.send_in_progress = false
        ServerState.scan_cancelled = false
        ServerState.send_cancelled = false
        ServerState.server_op_id = 0
        ServerState.stop_in_progress = false
    end

    -- 6. Reset cross-plugin visibility
    if PluginShare then
        PluginShare.localsend_running = nil
    end
end

-- Lifecycle: flush settings before shutdown
function LocalSend:onFlushSettings()
    -- Settings are saved immediately on change via G_reader_settings,
    -- so nothing to do here. This method exists for KOReader lifecycle compliance.
    logger.dbg("[LocalSend] onFlushSettings")
end

-- Unschedule helpers for proper task cleanup
-- These use stored task references so UIManager can actually unschedule them
function LocalSend:_unschedulePolling()
    if self.check_sentinel_task then
        UIManager:unschedule(self.check_sentinel_task)
    end
end

function LocalSend:_unscheduleResume()
    if self.resume_start_task then
        UIManager:unschedule(self.resume_start_task)
    end
end

-- Update cached state values (called on state changes to avoid disk I/O in menu)
function LocalSend:_updateCache()
    self._cached_running = self:isRunning()
    self._cached_stopping = ServerState.stop_in_progress
    self._cached_transfer_count = self:getTransferCount()
end

-- Keep menu state refreshed while async stop/start transitions settle.
function LocalSend:_refreshMenuUntilSettled(touchmenu_instance, attempts)
    if not touchmenu_instance then
        return
    end

    touchmenu_instance:updateItems()

    if attempts <= 0 then
        return
    end

    if ServerState.stop_in_progress then
        UIManager:scheduleIn(0.25, function()
            self:_refreshMenuUntilSettled(touchmenu_instance, attempts - 1)
        end)
    end
end

-- Server lifecycle functions (delegated to localsend_server module)
function LocalSend:_waitForServerReady(attempts_remaining, silent, on_ready, on_failure)
    lsserver.waitForServerReady(self, attempts_remaining, silent, on_ready, on_failure)
end

function LocalSend:_waitForProcessExit(pid, attempts_remaining, force, callback)
    lsserver.waitForProcessExit(pid, attempts_remaining, force, callback)
end

function LocalSend:_onServerStarted(silent, effective_name)
    lsserver.onServerStarted(self, silent, effective_name)
end

function LocalSend:_onServerStartFailed(silent)
    lsserver.onServerStartFailed(self, silent)
end

-- Cleanup scheduled tasks when widget is destroyed (document switch, view change)
-- Note: Server process continues running - only Lua-side tasks are cleaned up
function LocalSend:onCloseWidget()
    logger.dbg("[LocalSend] onCloseWidget")

    -- Unschedule polling task
    self:_unschedulePolling()
    self.check_sentinel_task = nil

    -- Unschedule any pending resume task
    self:_unscheduleResume()
    self.resume_start_task = nil

    -- Unschedule update check task
    self:_unscheduleUpdateCheck()
    self.check_update_task = nil

    -- Note: Server process continues running - new widget instance
    -- will take over polling responsibility in init() if server is running
end

-- Firewall functions (delegated to localsend_firewall module)
function LocalSend:openFirewall()
    if not isValidPort(self.port) then
        logger.err("[LocalSend] Invalid port, cannot configure firewall")
        return { managed = false, ok = false, detail = "invalid port" }
    end
    if not lsfirewall then
        return { managed = false, ok = true, detail = "firewall module unavailable" }
    end
    return lsfirewall.openFirewall(self.port, self.use_webrtc)
end

function LocalSend:closeFirewall()
    if not isValidPort(self.port) then
        logger.err("[LocalSend] Invalid port, cannot configure firewall")
        return { managed = false, ok = false, detail = "invalid port" }
    end
    if not lsfirewall then
        return { managed = false, ok = true, detail = "firewall module unavailable" }
    end
    return lsfirewall.closeFirewall(self.port)
end

function LocalSend:testFirewall()
    if not isValidPort(self.port) then
        logger.err("[LocalSend] Invalid port, cannot test firewall")
        return { managed = false, ok = false, detail = "invalid port" }
    end
    if not lsfirewall or not lsfirewall.selfTestFirewall then
        return { managed = false, ok = true, detail = "firewall module unavailable" }
    end
    return lsfirewall.selfTestFirewall(self.port, self.use_webrtc)
end

function LocalSend:validateDeviceName(name)
    local valid, err = validateDeviceName(name)
    if not valid and err then
        return false, _(err)
    end
    return valid
end

function LocalSend:validateSaveDir(path)
    -- Validate path is safe for shell operations
    if not isValidPath(path) then
        return false, _("Invalid path: must be an absolute path without special characters.")
    end

    -- Check if path exists
    if not util.pathExists(path) then
        -- Try to create it
        local ok, err = util.makePath(path)
        if not ok then
            logger.warn("[LocalSend] Failed to create directory:", err)
            return false, _("Directory does not exist and could not be created.")
        end
    end

    -- Check if writable by trying to create a temp file
    local test_file = path .. "/.localsend_write_test"
    local f = io.open(test_file, "w")
    if not f then
        return false, _("Directory is not writable.")
    end
    f:close()
    os.remove(test_file)

    return true
end

-- Transfer logging functions (delegated to localsend_transfers module)
function LocalSend:getTransferLog()
    return lstransfers.getTransferLog()
end

function LocalSend:getNewTransfers()
    return lstransfers.getNewTransfers()
end

function LocalSend:getTransferCount()
    return lstransfers.getTransferCount()
end

function LocalSend:clearTransferLog()
    lstransfers.clearTransferLog()
end

function LocalSend:_checkForNewTransfers()
    lstransfers.checkForNewTransfers(self)
end

function LocalSend:_checkSentinelFile()
    lstransfers.checkSentinelFile(self)
end

-- Start the LocalSend server
-- @param silent boolean If true, suppress the startup notification (used for resume from sleep)
function LocalSend:start(silent)
    -- Block server start if reinstall is required (plugin may be in broken state)
    if REINSTALL_REQUIRED then
        if not silent then
            UIManager:show(InfoMessage:new({
                icon = "notice-warning",
                text = _("Cannot start LocalSend: plugin reinstall required.\n\nPlease use 'Check for updates' to reinstall."),
            }))
        end
        return
    end
    lsserver.start(self, silent)
end

-- Start server when network is available.
-- For autostart: silently skip if offline (no WiFi prompts during widget recreation)
-- For manual start: use runWhenConnected to prompt for WiFi if needed
-- @param silent boolean If true, suppress notifications and don't prompt for WiFi
function LocalSend:_startWhenConnected(silent)
    logger.dbg("[LocalSend] _startWhenConnected: silent=", tostring(silent), ", isConnected=", tostring(NetworkMgr:isConnected()))
    if NetworkMgr:isConnected() then
        -- Network ready, start immediately
        self:start(silent)
    elseif silent then
        -- Silent mode (autostart/resume): don't prompt for WiFi, just skip
        -- This prevents WiFi prompts during widget recreation (book opens, view switches)
        logger.dbg("[LocalSend] Offline, silent mode - skipping start")
    else
        -- Non-silent (manual start): prompt for WiFi
        NetworkMgr:runWhenConnected(function()
            if not ServerState.user_stopped then
                self:start(silent)
            end
        end)
    end
end

function LocalSend:isRunning()
    -- In recovery mode, lsserver is nil - server cannot be running
    if not lsserver then
        return false
    end
    return lsserver.isRunning()
end

function LocalSend:stopServer(options)
    return lsserver.stopServer(self, options)
end

function LocalSend:_cleanupServerState()
    lsserver.cleanupServerState(self)
end

function LocalSend:stop()
    lsserver.stop(self)
end

function LocalSend:restart()
    lsserver.restart(self)
end

function LocalSend:onToggleLocalSend()
    lsserver.toggle(self)
end

-- UI dialog functions (delegated to localsend_dialogs module)
function LocalSend:getPickerStartPath(path)
    return lsdialogs.getPickerStartPath(path)
end

function LocalSend:showSaveDirPicker(touchmenu_instance)
    lsdialogs.showSaveDirPicker(self, touchmenu_instance)
end

function LocalSend:showDeviceNameDialog(touchmenu_instance)
    lsdialogs.showDeviceNameDialog(self, touchmenu_instance)
end

function LocalSend:showPinDialog(touchmenu_instance)
    lsdialogs.showPinDialog(self, touchmenu_instance)
end

function LocalSend:showCustomExtDialog(touchmenu_instance)
    lsdialogs.showCustomExtDialog(self, touchmenu_instance)
end

function LocalSend:buildExtensionPresetsMenu()
    return lsdialogs.buildExtensionPresetsMenu(self)
end

-- Extension routing functions (delegated to localsend_routing module)
function LocalSend:exportExtRouting()
    return lsrouting.exportExtRouting(self.routing_enabled, self.ext_dirs, self.routing_accept_all, self.save_dir, plugin_path)
end

function LocalSend:addExtensionRoute(ext, dir)
    lsrouting.addExtensionRoute(self, ext, dir)
end

function LocalSend:removeExtensionRoute(ext)
    lsrouting.removeExtensionRoute(self, ext)
end

function LocalSend:showAddExtensionRouteDialog(touchmenu_instance)
    lsrouting.showAddExtensionRouteDialog(self, touchmenu_instance)
end

function LocalSend:showCustomExtensionDialog(touchmenu_instance)
    lsrouting.showCustomExtensionDialog(self, touchmenu_instance)
end

function LocalSend:showExtensionDirPicker(ext, touchmenu_instance)
    lsrouting.showExtensionDirPicker(self, ext, touchmenu_instance)
end

function LocalSend:refreshRoutingMenu(touchmenu_instance)
    lsrouting.refreshRoutingMenu(self, touchmenu_instance)
end

function LocalSend:buildExtensionRoutingMenu()
    return lsrouting.buildExtensionRoutingMenu(self)
end

function LocalSend:showRecentTransfers()
    lstransfers.showRecentTransfers(self)
end

-- Send file flow functions (delegated to localsend_sender module)
function LocalSend:showFileSendFlow()
    if lssender then
        -- Open firewall for scanning (needs UDP 53317 for multicast discovery)
        -- Note: Firewall is opened temporarily; we don't close it after since:
        -- 1. WebRTC sends also need ports open
        -- 2. User may do multiple scans/sends
        -- 3. If receiver is also running, ports are already open
        -- 4. Ports are closed on KOReader exit anyway
        self:openFirewall()
        lssender.showFileSendFlow(self)
    end
end

function LocalSend:sendCurrentBook()
    if not lssender then
        return
    end

    -- Get current document path
    local doc_path = self.ui.document and self.ui.document.file
    if not doc_path then
        UIManager:show(InfoMessage:new({
            text = _("No book is currently open."),
            timeout = 3,
        }))
        return
    end

    -- Open firewall for scanning/sending
    self:openFirewall()
    lssender.showFileSendFlow(self, doc_path)
end

-- Dispatcher event handlers for quick-menu/gesture actions.
function LocalSend:onShowLocalSendFileSendFlow()
    self:showFileSendFlow()
end

function LocalSend:onSendCurrentBookWithLocalSend()
    self:sendCurrentBook()
end

function LocalSend:rotateCertificates()
    local ConfirmBox = require("ui/widget/confirmbox")
    UIManager:show(ConfirmBox:new({
        text = _("This will delete the current TLS certificates.\n\nTrusted devices may need to re-verify the connection.\n\nContinue?"),
        ok_text = _("Delete"),
        cancel_text = _("Cancel"),
        ok_callback = function()
            -- Remove certificates from the certs folder next to the binary
            -- Go will generate new ones on next start
            os.remove(certs_path .. "/server.key.pem")
            os.remove(certs_path .. "/server.crt")

            UIManager:show(InfoMessage:new({
                text = _("Certificates cleared. New certificates will be generated on next start."),
                timeout = 3,
            }))
        end,
    }))
end

function LocalSend:getDeviceArch()
    return lsupdate.getDeviceArch()
end

function LocalSend:performUpdate(download_url, asset_name, new_version)
    lsupdate.performUpdate(self, download_url, asset_name, new_version, plugin_path)
end

function LocalSend:doPerformUpdate(download_url, asset_name, new_version)
    lsupdate.doPerformUpdate(self, download_url, asset_name, new_version, plugin_path)
end

-- =========================================================================
-- Auto Update Check Methods
-- =========================================================================

-- Calculate seconds until next update check
function LocalSend:_getUpdateCheckDelay()
    return lsupdate.getUpdateCheckDelay(self.last_update_check, self.update_check_interval_hours)
end

-- Schedule the next update check
function LocalSend:_scheduleUpdateCheck()
    if not self.auto_update_check then
        return
    end

    local delay = self:_getUpdateCheckDelay()
    logger.dbg("[LocalSend] Scheduling update check in", delay, "seconds")
    UIManager:scheduleIn(delay, self.check_update_task)
end

-- Unschedule update check task
function LocalSend:_unscheduleUpdateCheck()
    if self.check_update_task then
        UIManager:unschedule(self.check_update_task)
    end
end

-- Auto update check (silent, uses Notification not ConfirmBox)
function LocalSend:_autoCheckForUpdates()
    -- If offline, silently skip and reschedule
    if not NetworkMgr:isOnline() then
        self:_scheduleUpdateCheck()
        return
    end

    -- Create schedule_next callback for the update module
    local schedule_next = function()
        self:_scheduleUpdateCheck()
    end

    lsupdate.doAutoCheckForUpdates(self, PLUGIN_VERSION, schedule_next)
end

function LocalSend:checkForUpdates()
    lsupdate.checkForUpdates(self, PLUGIN_VERSION, plugin_path)
end

function LocalSend:_openProjectPage()
    if Device.openLink then
        local ok, opened = pcall(function()
            return Device:openLink(GITHUB_URL)
        end)
        if ok and opened then
            return
        end
    end

    if Device.input and Device.input.setClipboardText then
        pcall(function()
            Device.input.setClipboardText(GITHUB_URL)
        end)
        UIManager:show(Notification:new({
            text = _("GitHub link copied to clipboard"),
            timeout = 3,
        }))
        return
    end

    UIManager:show(InfoMessage:new({
        text = T(_("Project page:\n%1"), GITHUB_URL),
        timeout = 5,
    }))
end

function LocalSend:showDiagnostics()
    if lsdiagnostics then
        lsdiagnostics.showDiagnostics(self)
    end
end

function LocalSend:runDiscoveryTest()
    if lsdiagnostics then
        lsdiagnostics.showDiscoveryTest(self)
    end
end

function LocalSend:showNetworkInfo()
    if lsdiagnostics then
        lsdiagnostics.showNetworkInfo()
    end
end

function LocalSend:showRecentBackendLog()
    if lsdiagnostics then
        lsdiagnostics.showRecentBackendLog()
    end
end

function LocalSend:showBugReportInfo()
    if lsdiagnostics then
        lsdiagnostics.showBugReport(self)
    end
end

function LocalSend:showAbout()
    local ConfirmBox = require("ui/widget/confirmbox")
    local description = plugin_meta.description or _("Send and receive files using LocalSend protocol.")
    local arch = self:getDeviceArch() or _("unknown")
    local text = T(_("LocalSend for KOReader\n\nVersion: %1\nArchitecture: %2\n\n%3\n\n%4"), PLUGIN_VERSION, arch, description, GITHUB_URL_DISPLAY)

    UIManager:show(ConfirmBox:new({
        text = text,
        ok_text = _("GitHub"),
        cancel_text = _("Close"),
        ok_callback = function()
            self:_openProjectPage()
        end,
    }))
end

function LocalSend:addToMainMenu(menu_items)
    -- Recovery mode: show minimal menu with only reinstall option
    if RECOVERY_MODE then
        menu_items.localsend = {
            text = _("LocalSend (Recovery Mode)"),
            sorting_hint = "network",
            sub_item_table = {
                {
                    text = _("Plugin Error - Reinstall Required"),
                    enabled_func = function()
                        return false
                    end,
                },
                {
                    text = _("Missing modules:"),
                    enabled_func = function()
                        return false
                    end,
                },
                {
                    text_func = function()
                        local errors = {}
                        for _, err in ipairs(module_load_errors) do
                            -- Extract just the module name
                            local mod_name = err:match("^([^:]+)")
                            if mod_name then
                                table.insert(errors, "  • " .. mod_name)
                            end
                        end
                        return table.concat(errors, "\n")
                    end,
                    enabled_func = function()
                        return false
                    end,
                    separator = true,
                },
                {
                    text_func = function()
                        return T(_("Reinstall plugin (%1)"), PLUGIN_VERSION)
                    end,
                    keep_menu_open = true,
                    callback = function()
                        self:checkForUpdates()
                    end,
                },
            },
        }
        return
    end

    menu_items.localsend = {
        text_func = function()
            if REINSTALL_REQUIRED then
                return _("LocalSend (⚠ Reinstall Required)")
            end
            if self._cached_stopping then
                return _("LocalSend (stopping...)")
            end
            if self._cached_running then
                if self._cached_transfer_count > 0 then
                    return T(_("LocalSend (%1 received)"), self._cached_transfer_count)
                end
                return _("LocalSend (running)")
            end
            if self.update_available_tag ~= "" then
                return _("LocalSend (update available)")
            end
            return _("LocalSend")
        end,
        sorting_hint = "network",
        -- Add check indicator for running state
        checked_func = function()
            return self._cached_running
        end,
        -- Quick toggle via long-press (SSH plugin pattern)
        hold_callback = function(touchmenu_instance)
            self:onToggleLocalSend()
            self:_refreshMenuUntilSettled(touchmenu_instance, 16)
        end,
        sub_item_table = self:_buildMainMenu(),
    }
end

-- Build the main menu items (extracted to allow conditional warning item)
function LocalSend:_buildMainMenu()
    local menu = {}

    -- Add warning item at top if reinstall is required
    if REINSTALL_REQUIRED then
        table.insert(menu, {
            text = _("⚠ Previous update failed - Reinstall required"),
            enabled_func = function()
                return false
            end,
            separator = true,
        })
    end

    if self.update_available_tag ~= "" then
        table.insert(menu, {
            text_func = function()
                return T(_("⬆ Update available: %1"), self.update_available_tag)
            end,
            enabled_func = function()
                return false
            end,
            separator = true,
        })
    end

    -- Start/Stop server
    table.insert(menu, {
        text_func = function()
            if self._cached_stopping then
                return _("Stopping server...")
            end
            if self._cached_running then
                return _("Stop server")
            else
                return _("Start server")
            end
        end,
        keep_menu_open = true,
        checked_func = function()
            return self._cached_running
        end,
        enabled_func = function()
            if self._cached_stopping then
                return false
            end
            -- Allow stopping if running, but block starting if reinstall required
            return self._cached_running or not REINSTALL_REQUIRED
        end,
        check_callback_updates_menu = true,
        callback = function(touchmenu_instance)
            self:onToggleLocalSend()
            self:_refreshMenuUntilSettled(touchmenu_instance, 16)
        end,
    })

    -- Recent transfers
    table.insert(menu, {
        text_func = function()
            if self._cached_transfer_count > 0 then
                return T(_("Recent transfers (%1)"), self._cached_transfer_count)
            end
            return _("Recent transfers")
        end,
        enabled_func = function()
            return self._cached_transfer_count > 0
        end,
        callback = function()
            self:showRecentTransfers()
        end,
    })

    -- Save directory
    table.insert(menu, {
        text_func = function()
            return T(_("Save directory (%1)"), self.save_dir)
        end,
        keep_menu_open = true,
        enabled_func = function()
            return not self._cached_running
        end,
        callback = function(touchmenu_instance)
            self:showSaveDirPicker(touchmenu_instance)
        end,
    })

    -- Settings submenu
    table.insert(menu, {
        text = _("Settings"),
        enabled_func = function()
            return not self._cached_running
        end,
        sub_item_table = {
            {
                text_func = function()
                    if self.device_name ~= "" then
                        return T(_("Device name (%1)"), self.device_name)
                    else
                        return _("Device name (KOReader)")
                    end
                end,
                keep_menu_open = true,
                callback = function(touchmenu_instance)
                    self:showDeviceNameDialog(touchmenu_instance)
                end,
            },
            {
                text_func = function()
                    if self.routing_enabled and next(self.ext_dirs) then
                        return _("Allowed extensions (using routing)")
                    elseif self.accept_ext ~= "" then
                        return T(_("Allowed extensions (%1)"), self.accept_ext)
                    else
                        return _("Allowed extensions (all)")
                    end
                end,
                enabled_func = function()
                    return not (self.routing_enabled and next(self.ext_dirs))
                end,
                sub_item_table_func = function()
                    return self:buildExtensionPresetsMenu()
                end,
                help_text = _("When file type routing is enabled, allowed extensions are determined by the routing rules."),
            },
            {
                text_func = function()
                    local count = 0
                    for _ in pairs(self.ext_dirs) do
                        count = count + 1
                    end
                    if count > 0 then
                        if self.routing_enabled then
                            return T(_("File type routing (%1 rules)"), count)
                        else
                            return T(_("File type routing (disabled, %1 rules)"), count)
                        end
                    else
                        return _("File type routing")
                    end
                end,
                sub_item_table_func = function()
                    return self:buildExtensionRoutingMenu()
                end,
                help_text = _("Route different file types to different directories (e.g., EPUBs to Books folder, PDFs to Documents)."),
            },
            {
                text_func = function()
                    if self.pin ~= "" then
                        return _("PIN code (enabled)")
                    else
                        return _("PIN code (disabled)")
                    end
                end,
                keep_menu_open = true,
                callback = function(touchmenu_instance)
                    self:showPinDialog(touchmenu_instance)
                end,
                separator = true,
            },
            {
                text = _("Use HTTPS"),
                checked_func = function()
                    return self.use_https
                end,
                callback = function()
                    self.use_https = not self.use_https
                    G_reader_settings:flipNilOrTrue("LocalSend_use_https")
                end,
                help_text = _("HTTPS encrypts transfers for security. Disable only if you experience compatibility issues."),
            },
            {
                text = _("Start with KOReader"),
                checked_func = function()
                    return self.autostart
                end,
                callback = function()
                    self.autostart = not self.autostart
                    G_reader_settings:flipNilOrFalse("LocalSend_autostart")
                end,
                help_text = _("Automatically start the LocalSend server when KOReader launches."),
            },
            {
                text = _("Enable WebRTC Support (Experimental)"),
                checked_func = function()
                    return self.use_webrtc
                end,
                callback = function()
                    self.use_webrtc = not self.use_webrtc
                    G_reader_settings:flipNilOrFalse("LocalSend_use_webrtc")
                end,
                help_text = _("Connect to public signaling server for WebRTC transfers. Requires internet access."),
            },
            {
                text = _("Rotate certificates"),
                keep_menu_open = true,
                callback = function()
                    self:rotateCertificates()
                end,
                help_text = _("Generate new TLS certificates. Use if you experience connection issues or want to reset trusted device pairings."),
            },
            {
                text = _("Troubleshooting"),
                sub_item_table_func = function()
                    return self:_buildTroubleshootingMenu()
                end,
            },
        },
        separator = true,
    })

    -- Send file
    if lssender then
        table.insert(menu, {
            text = _("Send file..."),
            callback = function()
                self:showFileSendFlow()
            end,
            help_text = _("Send a file to another LocalSend device on the network."),
        })

        -- Send current book (only in reader view)
        table.insert(menu, {
            text = _("Send current book"),
            enabled_func = function()
                return self.ui.document ~= nil
            end,
            callback = function()
                self:sendCurrentBook()
            end,
            help_text = _("Send the currently open book to another LocalSend device."),
            separator = true,
        })
    end

    -- Updates
    table.insert(menu, {
        text = _("Updates"),
        sub_item_table_func = function()
            return self:_buildUpdatesMenu()
        end,
    })

    -- About
    table.insert(menu, {
        text = _("About LocalSend"),
        callback = function()
            self:showAbout()
        end,
    })

    return menu
end

function LocalSend:_buildTroubleshootingMenu()
    local menu = {
        {
            text = _("Run diagnostics"),
            keep_menu_open = true,
            callback = function()
                self:showDiagnostics()
            end,
            help_text = _("Run checks for network, binary, server, and firewall, then show detailed logs " .. "and report information."),
        },
        {
            text = _("Test discovery"),
            keep_menu_open = true,
            callback = function()
                self:runDiscoveryTest()
            end,
            help_text = _(
                "Check whether this device can send/receive LocalSend multicast discovery packets "
                    .. "and whether other devices are visible, to explain why a device isn't being found."
            ),
        },
        {
            text = _("Show network info"),
            keep_menu_open = true,
            callback = function()
                self:showNetworkInfo()
            end,
        },
        {
            text = _("Show recent LocalSend log"),
            keep_menu_open = true,
            callback = function()
                self:showRecentBackendLog()
            end,
        },
        {
            text = _("Prepare bug report"),
            keep_menu_open = true,
            callback = function()
                self:showBugReportInfo()
            end,
            separator = true,
        },
        {
            text = _("Common fixes"),
            sub_item_table = {
                {
                    text = _("Restart LocalSend server"),
                    keep_menu_open = true,
                    callback = function()
                        self:restart()
                    end,
                },
                {
                    text = _("Rotate certificates"),
                    keep_menu_open = true,
                    callback = function()
                        self:rotateCertificates()
                    end,
                },
                {
                    text = _("Use HTTPS"),
                    checked_func = function()
                        return self.use_https
                    end,
                    callback = function()
                        self.use_https = not self.use_https
                        _G.G_reader_settings:flipNilOrTrue("LocalSend_use_https")
                    end,
                    help_text = _("Disable temporarily if another LocalSend app cannot connect over HTTPS."),
                },
                {
                    text = _("Enable WebRTC Support (Experimental)"),
                    checked_func = function()
                        return self.use_webrtc
                    end,
                    callback = function()
                        self.use_webrtc = not self.use_webrtc
                        _G.G_reader_settings:flipNilOrFalse("LocalSend_use_webrtc")
                    end,
                    help_text = _("Disable temporarily if startup or discovery fails while WebRTC signaling " .. "is enabled."),
                },
                {
                    text = _("Check for updates / reinstall"),
                    keep_menu_open = true,
                    callback = function()
                        self:checkForUpdates()
                    end,
                },
            },
        },
    }
    return menu
end

function LocalSend:_buildAutoUpdateMenuItem()
    return {
        text_func = function()
            if self.auto_update_check then
                local intervals = { [12] = "12h", [24] = "24h", [72] = "3 days", [168] = "Weekly" }
                local label = intervals[self.update_check_interval_hours] or (self.update_check_interval_hours .. "h")
                return T(_("Auto-check for updates (%1)"), label)
            else
                return _("Auto-check for updates")
            end
        end,
        checked_func = function()
            return self.auto_update_check
        end,
        hold_callback = function(touchmenu_instance)
            self.auto_update_check = not self.auto_update_check
            G_reader_settings:flipNilOrTrue("LocalSend_auto_update_check")
            if self.auto_update_check then
                self:_scheduleUpdateCheck()
            else
                self:_unscheduleUpdateCheck()
            end
            if touchmenu_instance then
                touchmenu_instance:updateItems()
            end
        end,
        sub_item_table_func = function()
            local intervals = {
                { value = 12, text = _("Every 12 hours") },
                { value = 24, text = _("Every 24 hours") },
                { value = 72, text = _("Every 3 days") },
                { value = 168, text = _("Weekly (default)") },
            }
            local submenu = {
                {
                    text = _("Enable auto-check"),
                    checked_func = function()
                        return self.auto_update_check
                    end,
                    callback = function()
                        self.auto_update_check = not self.auto_update_check
                        G_reader_settings:flipNilOrTrue("LocalSend_auto_update_check")
                        if self.auto_update_check then
                            self:_scheduleUpdateCheck()
                        else
                            self:_unscheduleUpdateCheck()
                        end
                    end,
                    separator = true,
                },
            }
            local radio_items = lsutils.buildRadioMenu(intervals, function()
                return self.update_check_interval_hours
            end, function(v)
                self.update_check_interval_hours = v
                G_reader_settings:saveSetting("LocalSend_update_check_interval_hours", v)
            end, function()
                return self.auto_update_check
            end)
            for _, item in ipairs(radio_items) do
                table.insert(submenu, item)
            end
            return submenu
        end,
    }
end

function LocalSend:_buildUpdatesMenu()
    return {
        {
            text_func = function()
                if self.update_available_tag ~= "" then
                    return T(_("Update available: %1 → %2"), PLUGIN_VERSION, self.update_available_tag)
                end
                return T(_("Installed version: %1"), PLUGIN_VERSION)
            end,
            keep_menu_open = true,
            callback = function()
                self:checkForUpdates()
            end,
        },
        {
            text = _("Check for updates"),
            keep_menu_open = true,
            callback = function()
                self:checkForUpdates()
            end,
            separator = true,
        },
        self:_buildAutoUpdateMenuItem(),
    }
end

function LocalSend:onDispatcherRegisterActions()
    Dispatcher:registerAction("toggle_localsend_server", { category = "none", event = "ToggleLocalSend", title = _("Toggle LocalSend server"), general = true })
    Dispatcher:registerAction(
        "send_file_localsend",
        { category = "none", event = "ShowLocalSendFileSendFlow", title = _("LocalSend: send file"), general = true }
    )
    Dispatcher:registerAction("send_current_book_localsend", {
        category = "none",
        event = "SendCurrentBookWithLocalSend",
        title = _("LocalSend: send current book"),
        general = true,
        reader = true,
        separator = true,
    })
end

-- Expose ServerState for testing purposes
-- Production code should NOT access this directly
LocalSend._ServerState = ServerState

return LocalSend
