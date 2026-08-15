require("busted.runner")()
-- spec_helper.lua
--
-- Real-KOReader test helper for the LocalSend plugin.
--
-- This runs inside busted-koreader with /opt/koplugin-dev/commonrequire.lua as
-- the busted --helper, so every KOReader module (UIManager, Device, util,
-- NetworkMgr, widgets, G_reader_settings, ...) is the REAL module — no mocks.
--
-- This helper only provides:
--   * A binary shim + settings isolation so require("main") instantiates the
--     plugin exactly as KOReader would on-device.
--   * Transparent SPIES (call-through) on UIManager.show/close/scheduleIn and
--     os.execute/os.remove so specs can observe what the plugin did without
--     stubbing KOReader out. Spies call the real implementation; os.execute is
--     the lone exception (it is captured, not run, so tests never actually
--     launch the receiver or touch iptables).
--
-- Drop-in replacement for the old mock-based test_helper.lua.

local M = {}

local UIManager = require("ui/uimanager")
local DataStorage = require("datastorage")
local ffiUtil = require("ffi/util")
local util = require("util")

-- Optional: serialize the live widget tree on test failure so the dump lands in
-- the busted output right under the failing assertion. No-op if busted's
-- mediator isn't reachable. Gated on KO_DEBUG_UI_DUMP (default on) so CI can
-- quiet it with KO_DEBUG_UI_DUMP=0.
local _ui_dump_enabled = os.getenv("KO_DEBUG_UI_DUMP") ~= "0"

-- Capture the real os.execute/os.remove before any spy wraps them (spies install
-- later in setup). prepare_runtime_plugin must actually mkdir/cp/chmod.
local _real_os_execute = os.execute
local _real_os_remove = os.remove

-- Widget classes, keyed by the name find_dialog() / state.dialogs_shown use.
local WidgetClasses = {
    InfoMessage = require("ui/widget/infomessage"),
    Notification = require("ui/widget/notification"),
    InputDialog = require("ui/widget/inputdialog"),
    ButtonDialog = require("ui/widget/buttondialog"),
    ConfirmBox = require("ui/widget/confirmbox"),
    TextViewer = require("ui/widget/textviewer"),
    PathChooser = require("ui/widget/pathchooser"),
}

local NotificationClasses = { InfoMessage = true, Notification = true }

--- Identify a widget instance by its class table (Widget:new sets metatable = class).
function M.widget_class(widget)
    if type(widget) ~= "table" then
        return nil
    end
    local mt = getmetatable(widget)
    for name, cls in pairs(WidgetClasses) do
        if mt == cls then
            return name
        end
    end
    return nil
end

-- =============================================================================
-- Capture state (same field names as the old helper so specs need few changes)
-- =============================================================================
M.state = {
    notifications_shown = {}, -- InfoMessage / Notification instances
    dialogs_shown = {}, -- InputDialog / ButtonDialog / ConfirmBox / TextViewer / PathChooser
    os_execute_calls = {},
    scheduled_tasks = {},
    unscheduled_tasks = {},
    removed_files = {},
    close_calls = {},
}

function M.reset_state()
    M.state.notifications_shown = {}
    M.state.dialogs_shown = {}
    M.state.os_execute_calls = {}
    M.state.scheduled_tasks = {}
    M.state.unscheduled_tasks = {}
    M.state.removed_files = {}
    M.state.close_calls = {}
    M.state.purged_dirs = {}
end

-- =============================================================================
-- Spies (transparent — call through to the real implementation)
-- =============================================================================
local saved = {}
local spies_installed = false

local function record_shown(widget)
    local cls = M.widget_class(widget)
    if cls then
        widget._type = widget._type or cls -- tag for find_dialog() compatibility
        if NotificationClasses[cls] then
            table.insert(M.state.notifications_shown, widget)
        else
            table.insert(M.state.dialogs_shown, widget)
        end
    end
end

--- Install transparent spies. Capture-once, but always re-assert the wrapped
-- refs so a spec that overwrote os.execute / UIManager.show between tests gets
-- reset on the next before_each.
-- opts.execute_handler: optional fn(cmd)->exitcode overriding os.execute's return.
-- opts.execute_result:  exitcode returned when no handler is set (default 0).
function M.install_spies(opts)
    opts = opts or {}
    if not spies_installed then
        spies_installed = true
        saved.show = UIManager.show
        saved.close = UIManager.close
        saved.scheduleIn = UIManager.scheduleIn
        saved.unschedule = UIManager.unschedule
        saved.execute = os.execute
        saved.remove = os.remove
        saved.removeFile = util.removeFile
        saved.purgeDir = ffiUtil.purgeDir
        saved.popen = io.popen
    end

    UIManager.show = function(self, widget, ...)
        record_shown(widget)
        -- Best-effort call-through: some widgets' Show handler needs a fuller UI
        -- pipeline than a headless unit test provides. Recording is what specs
        -- assert on, so never let a render-time error escape.
        pcall(saved.show, UIManager, widget, ...)
    end
    UIManager.close = function(self, widget, ...)
        table.insert(M.state.close_calls, widget)
        pcall(saved.close, UIManager, widget, ...)
    end
    UIManager.scheduleIn = function(self, delay, callback, ...)
        table.insert(M.state.scheduled_tasks, { delay = delay, callback = callback })
        return saved.scheduleIn(UIManager, delay, callback, ...)
    end
    UIManager.unschedule = function(self, callback, ...)
        table.insert(M.state.unscheduled_tasks, callback)
        return saved.unschedule(UIManager, callback, ...)
    end

    M._execute_handler = nil
    M._execute_result = opts.execute_result or 0
    -- os.execute: capture but do NOT actually run (no launching receivers / iptables in tests).
    os.execute = function(cmd)
        table.insert(M.state.os_execute_calls, cmd)
        if M._execute_handler then
            return M._execute_handler(cmd)
        end
        return M._execute_result
    end
    -- os.remove: capture and really remove.
    os.remove = function(path, ...)
        table.insert(M.state.removed_files, path)
        return saved.remove(path, ...)
    end
    -- util.removeFile: capture but do NOT really remove (the update flow removes
    -- destination files as part of a virtual copy; really deleting would corrupt
    -- the runtime plugin shim for later tests).
    util.removeFile = function(path, ...)
        table.insert(M.state.removed_files, path)
        return true
    end
    -- ffi/util purgeDir: capture and really purge.
    M.state.purged_dirs = {}
    ffiUtil.purgeDir = function(dir, ...)
        table.insert(M.state.purged_dirs, dir)
        return saved.purgeDir(dir, ...)
    end
end

function M.restore_spies()
    if not spies_installed then
        return
    end
    spies_installed = false
    UIManager.show = saved.show
    UIManager.close = saved.close
    UIManager.scheduleIn = saved.scheduleIn
    UIManager.unschedule = saved.unschedule
    os.execute = saved.execute
    os.remove = saved.remove
    util.removeFile = saved.removeFile
    ffiUtil.purgeDir = saved.purgeDir
    io.popen = saved.popen
end

-- Back-compat shims that the old helper exposed.
function M.mock_os_execute(handler)
    M.install_spies()
    M._execute_handler = handler
end

function M.mock_os_remove()
    M.install_spies()
end

-- =============================================================================
-- Plugin runtime: binary shim + isolated settings
-- =============================================================================
local function shell_quote(path)
    return "'" .. tostring(path):gsub("'", "'\\''") .. "'"
end

local function file_exists(path)
    local f = io.open(path, "r")
    if f then
        f:close()
        return true
    end
    return false
end

function M.runtime_plugin_dir()
    return DataStorage:getFullDataDir() .. "/plugins/localsend.koplugin"
end

local function source_dir()
    local dir = get_plugin_path and get_plugin_path() or "/opt/plugin/lua"
    if not file_exists(dir .. "/_meta.lua") and file_exists(dir .. "/lua/_meta.lua") then
        dir = dir .. "/lua"
    end
    return dir
end

--- Materialise the installed plugin layout exactly where KOReader discovers it.
-- Copy the Lua modules too: main.lua intentionally derives its runtime root from
-- its own source path so external extra_plugin_paths work in production.
function M.prepare_runtime_plugin()
    local plugin_dir = M.runtime_plugin_dir()
    local src = source_dir()

    _real_os_execute("rm -rf " .. shell_quote(plugin_dir) .. " && mkdir -p " .. shell_quote(plugin_dir))
    _real_os_execute("cp " .. shell_quote(src) .. "/*.lua " .. shell_quote(plugin_dir) .. "/")
    if file_exists(src .. "/locale/localsend.pot") then
        _real_os_execute("cp -R " .. shell_quote(src .. "/locale") .. " " .. shell_quote(plugin_dir .. "/locale"))
    end

    -- Direct require("main") specs should exercise the same copied path that
    -- PluginLoader does, rather than the source checkout beside spec/.
    local runtime_pattern = plugin_dir .. "/?.lua"
    if not package.path:find(runtime_pattern, 1, true) then
        package.path = runtime_pattern .. ";" .. package.path
    end

    local bin = assert(io.open(plugin_dir .. "/localsend", "w"))
    bin:write("#!/bin/sh\n")
    bin:write('case "$1" in\n')
    bin:write("  --version) echo 'localsend test-shim v0.0.0/test' ; exit 0 ;;\n")
    bin:write("  *) echo 'localsend test shim: not launching receiver' >&2 ; exit 1 ;;\n")
    bin:write("esac\n")
    bin:close()
    _real_os_execute("chmod +x " .. shell_quote(plugin_dir .. "/localsend"))

    -- Deterministic, side-effect-free defaults (do NOT pin save_dir — specs that
    -- care about the default value need it unset).
    G_reader_settings:saveSetting("LocalSend_auto_update_check", false)
    G_reader_settings:saveSetting("LocalSend_autostart", false)

    return plugin_dir
end

local plugin_prepared = false
function M.prepare_plugin()
    if plugin_prepared then
        -- A prior spec may have removed the shim binary (e.g. binary_check_spec
        -- exercises the missing-binary path). main.lua refuses to load without
        -- it, so re-materialise whenever it's gone rather than assuming the
        -- one-shot install survived the whole suite.
        local bin = M.runtime_plugin_dir() .. "/localsend"
        local f = io.open(bin, "r")
        if f then
            f:close()
        else
            M.prepare_runtime_plugin()
        end
        return
    end
    M.prepare_runtime_plugin()
    plugin_prepared = true
end

-- Modules whose top-level state must be fresh per instance.
local function reset_loaded_plugin_modules()
    local loaded_power = package.loaded["localsend_power"]
    if loaded_power and loaded_power.releaseAll then
        pcall(loaded_power.releaseAll)
    end
    for _, name in ipairs({
        "main",
        "localsend_constants",
        "localsend_update",
        "localsend_routing",
        "localsend_transfers",
        "localsend_dialogs",
        "localsend_firewall",
        "localsend_power",
        "localsend_server",
        "localsend_discovery",
        "localsend_sender",
        "localsend_diagnostics",
        "localsend_state",
    }) do
        package.loaded[name] = nil
    end

    -- Keep require("main") from scanning the real container /tmp on every spec.
    -- main.lua only calls clearTmpTelemetryFiles when ServerState.telemetry_cleaned
    -- is false, so seed the fresh state as "already cleaned" instead of
    -- monkeypatching localsend_update.clearTmpTelemetryFiles. Specs that exercise
    -- clearTmpTelemetryFiles directly still call the production function.
    local ok, state = pcall(require, "localsend_state")
    if ok and state and state.ServerState then
        state.ServerState.telemetry_cleaned = true
    end
end

function M.reset_localsend_state()
    local ok, state = pcall(require, "localsend_state")
    if not (ok and state and state.ServerState) then
        return
    end
    local s = state.ServerState
    s.user_stopped = false
    s.was_running_before_suspend = false
    s.was_running_before_disconnect = false
    s.last_log_position = 0
    s.transfer_count = 0
    s.last_sentinel_value = nil
    s.discovered_devices = {}
    s.scan_in_progress = false
    s.scan_op_id = 0
    s.send_in_progress = false
    s.send_op_id = 0
    s.scan_cancelled = false
    s.send_cancelled = false
    s.send_cancel_started_at = nil
    s.server_op_id = 0
    s.stop_in_progress = false
    s.lifecycle_events = {}
    -- Fields the reset above missed. telemetry_cleaned gates a once-per-session
    -- init in main.lua; without clearing it the first spec to flip it sticks it
    -- on for the rest of the suite. last_send / scan_start_time are set at
    -- runtime and also leak across specs. (polling_generation is intentionally
    -- not touched — it is deprecated and caching_spec asserts it stays nil.)
    s.telemetry_cleaned = false
    s.last_send = nil
    s.scan_start_time = nil
end

local SETTING_PREFIX = "LocalSend_"
function M.reset_settings()
    -- G_reader_settings is a real LuaSettings; iterate its data table.
    local data = G_reader_settings.data
    if data then
        for key in pairs(data) do
            if type(key) == "string" and key:sub(1, #SETTING_PREFIX) == SETTING_PREFIX then
                G_reader_settings:delSetting(key)
            end
        end
    end
    -- Re-apply the deterministic defaults prepare_plugin() sets.
    G_reader_settings:saveSetting("LocalSend_auto_update_check", false)
    G_reader_settings:saveSetting("LocalSend_autostart", false)
end

-- Back-compat: old helper exposed state.settings as the settings store.
-- Specs that wrote helper.state.settings["LocalSend_x"] = v now use G_reader_settings.
M.state.settings = setmetatable({}, {
    __index = function(_, key)
        return G_reader_settings:readSetting(key)
    end,
    __newindex = function(_, key, value)
        G_reader_settings:saveSetting(key, value)
    end,
    __pairs = function()
        return pairs(G_reader_settings.data)
    end,
})

-- =============================================================================
-- Headless UI singleton
-- =============================================================================
-- KOReader >= 2026.05 (PR #15323, "Folder shortcuts: standard folders") made
-- FileChooser:refreshPath() / genItemTable() dereference self.ui.folder_shortcuts,
-- and PathChooser:init() resolves self.ui from readerui.instance or
-- filemanager.instance. On-device one of those singletons always exists; in
-- headless create_instance() tests neither does, so PathChooser:new() crashed
-- during init (filechooser.lua:336: attempt to index field 'ui'). Provide a
-- faithful stand-in carrying a REAL FileManagerShortcuts — exactly what a real
-- FileManager registers as its "folder_shortcuts" module — so the real
-- refreshPath/genItemTable code paths run instead of being short-circuited.
-- Tagged so cleanup only ever clears our own stand-in, never a real FileManager
-- singleton (e.g. one created by load_via_filemanager).
local HEADLESS_FM_TAG = "_localsend_headless_filemanager"

local function install_headless_filemanager_instance()
    local FileManager = require("apps/filemanager/filemanager")
    if FileManager.instance ~= nil then
        return FileManager.instance -- a real singleton already exists; leave it
    end
    local FileManagerShortcuts = require("apps/filemanager/filemanagershortcuts")
    FileManager.instance = {
        [HEADLESS_FM_TAG] = true,
        folder_shortcuts = FileManagerShortcuts:new({}),
    }
    return FileManager.instance
end

local function clear_headless_filemanager_instance()
    local ok, FileManager = pcall(require, "apps/filemanager/filemanager")
    if ok and FileManager.instance and FileManager.instance[HEADLESS_FM_TAG] then
        FileManager.instance = nil
    end
end

-- =============================================================================
-- Instance creation
-- =============================================================================
--- Create a real plugin instance against live KOReader modules.
-- Does NOT reset modules/state — specs often monkey-patch the class (e.g.
-- LocalSend.start) between requiring it and creating an instance. Setup/before_each
-- own the resets; create_instance just instantiates the currently-loaded module.
function M.create_instance()
    M.prepare_plugin()
    install_headless_filemanager_instance()
    local LocalSend = require("main")
    return LocalSend:new({
        ui = { menu = { registerToMainMenu = function() end } },
    }), LocalSend
end

--- Load the plugin through the REAL PluginLoader + FileManager entry point
-- (the way KOReader actually instantiates it). Returns (instance, filemanager).
function M.load_via_filemanager()
    M.prepare_plugin()
    reset_loaded_plugin_modules()
    disable_plugins()
    load_plugin("localsend.koplugin")
    local UIManager = require("ui/uimanager")
    local Screen = require("device").screen
    local DataStorage = require("datastorage")
    local FileManager = require("apps/filemanager/filemanager")
    local PluginLoader = require("pluginloader")
    -- Drop any headless stand-in so FileManager:new() doesn't log an
    -- "instance mismatch" against it (FileManager.instance must be nil here).
    clear_headless_filemanager_instance()
    local fm = FileManager:new({ dimen = Screen:getSize(), root_path = DataStorage:getDataDir() })
    UIManager:show(fm)
    fastforward_ui_events()
    return PluginLoader:getPluginInstance("localsend"), fm
end

function M.close_filemanager(fm)
    local UIManager = require("ui/uimanager")
    if fm and fm.onClose then
        fm:onClose()
    end
    UIManager:quit()
end

-- =============================================================================
-- One-call setup (replaces the old setup_complete)
-- =============================================================================
function M.setup_complete(opts)
    opts = opts or {}
    clear_headless_filemanager_instance()
    M.prepare_plugin()
    M.reset_state()
    M.reset_settings()
    -- reset_localsend_state() is intentionally omitted here: reset_loaded_plugin_modules()
    -- nils package.loaded["localsend_state"], so the next require("main") builds a fresh
    -- ServerState from the module's default table anyway.
    reset_loaded_plugin_modules()
    M.install_spies(opts)
    M._execute_handler = nil
    M._execute_result = opts.execute_result or 0
    if opts.capture_logs then
        M.install_capture_logger()
    else
        local logger = require("logger")
        if logger and logger.setLevel then
            logger:setLevel(logger.levels and logger.levels.warn or 2)
        end
    end
    M.install_dump_on_failure()
    -- Default headless environment: a real device always has a FileManager or
    -- ReaderUI singleton, so PathChooser can resolve self.ui. Specs that build a
    -- real FileManager via load_via_filemanager() clear this first.
    install_headless_filemanager_instance()
end

function M.before_each()
    clear_headless_filemanager_instance()
    M.reset_state()
    M.reset_settings()
    -- reset_localsend_state() omitted for the same reason as setup_complete:
    -- the module nil below yields a fresh ServerState on the next require.
    reset_loaded_plugin_modules()
    M.install_spies()
    M._execute_handler = nil
    M._execute_result = 0
end

-- A recording logger installed when a spec asks for capture_logs. Records calls
-- per level into .calls so specs can assert on emitted messages.
local function new_capture_logger()
    local calls = {}
    return setmetatable({ calls = calls, levels = { err = 1, warn = 2, info = 3, dbg = 4 } }, {
        __index = function(t, method)
            return function(...)
                local list = t.calls[method]
                if not list then
                    list = {}
                    t.calls[method] = list
                end
                local parts = {}
                for _, a in ipairs({ ... }) do
                    table.insert(parts, tostring(a))
                end
                table.insert(list, table.concat(parts, "\t"))
            end
        end,
    })
end

function M.install_capture_logger()
    local cap = new_capture_logger()
    cap.setLevel = function() end
    -- Save the real logger so restore_capture_logger can put it back. Leaked
    -- capture loggers stick in package.loaded (busted is single-process) and
    -- silently swallow log output from every later spec file.
    if not M._saved_real_logger then
        M._saved_real_logger = package.loaded["logger"]
    end
    package.loaded["logger"] = cap
    M.capture_logger = cap
    return cap
end

function M.restore_capture_logger()
    if M._saved_real_logger then
        package.loaded["logger"] = M._saved_real_logger
        M.capture_logger = nil
    end
end

-- =============================================================================
-- Finders (same API as the old helper)
-- =============================================================================
function M.find_notification(pattern)
    for _, n in ipairs(M.state.notifications_shown) do
        if n.text and n.text:match(pattern) then
            return n
        end
    end
    return nil
end

function M.find_execute_call(pattern)
    for _, cmd in ipairs(M.state.os_execute_calls) do
        if cmd:match(pattern) then
            return cmd
        end
    end
    return nil
end

function M.find_dialog(dialog_type)
    for _, d in ipairs(M.state.dialogs_shown) do
        if d._type == dialog_type then
            return d
        end
    end
    return nil
end

function M.find_dialog_with_title(dialog_type, title_pattern)
    for _, d in ipairs(M.state.dialogs_shown) do
        if d._type == dialog_type and d.title and d.title:match(title_pattern) then
            return d
        end
    end
    return nil
end

-- =============================================================================
-- Widget tree dump on test failure
-- =============================================================================
-- Serializes UIManager._window_stack with serpent.block, filtering keys that
-- bloat output or contain cdata (which serpent chokes on / breaks stable
-- serialization). Lifted from rakuyomi's testing.lua — but deterministic: we
-- print it for humans to read, never ship it to a model.
local serpent = require("ffi/serpent")

local DUMP_IGNORED_KEYS = {
    key_events = true,
    ges_events = true,
    _xshaping = true,
    face = true,
    koptinterface = true,
    deinflector = true,
    -- bloats output without aiding triage
    item_table = true,
    -- cdata / hashes break stable serialization
    ftsize = true,
}

function M.dump_visible_ui()
    local stack = UIManager._window_stack or {}
    local visible = {}
    for i = #stack, 1, -1 do
        visible[#visible + 1] = stack[i]
        local w = stack[i].widget
        if w and w.covers_fullscreen then
            break
        end
    end

    local keyignore = setmetatable({}, {
        __index = function(_, key)
            return DUMP_IGNORED_KEYS[key] ~= nil or type(key) == "string" and key:sub(1, 1) == "_"
        end,
    })

    return serpent.block(visible, {
        maxlevel = 8,
        indent = "  ",
        nocode = true,
        comment = false,
        keyignore = keyignore,
    })
end

local _dump_hook_installed = false
--- Subscribe to busted's test-end event and dump the live widget tree when a
--- test fails. Call once from a spec's setup() — safe to call repeatedly.
function M.install_dump_on_failure()
    if not _ui_dump_enabled or _dump_hook_installed then
        return
    end
    local ok, busted = pcall(require, "busted")
    if not ok or not busted or not busted.subscribe then
        return
    end
    _dump_hook_installed = true
    busted.subscribe({ "test", "end" }, function(_, _, status)
        -- status is the string "success" / "failure" / "error" / "pending".
        if status == "failure" or status == "error" then
            io.stderr:write("\n--- visible UI at failure ---\n")
            io.stderr:write(M.dump_visible_ui())
            io.stderr:write("\n--- end UI dump ---\n")
        end
    end)
end

return M
