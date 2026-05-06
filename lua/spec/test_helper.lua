-- spec/test_helper.lua
-- Shared mocking utilities for LocalSend test suite
--
-- Usage:
--   require 'busted.runner'()
--   local helper = require("spec.test_helper")
--
--   describe("MyFeature", function()
--       helper.setup_complete()  -- In setup() or before_each()
--   end)

local M = {}

-- Storage for test state that needs to be accessible across tests
M.state = {
    settings = {},
    notifications_shown = {},
    dialogs_shown = {},
    os_execute_calls = {},
    scheduled_tasks = {},
    unscheduled_tasks = {},
    removed_files = {},
    purged_dirs = {},
    close_calls = {},
}

-- Reset all state between tests
function M.reset_state()
    M.state.settings = {}
    M.state.notifications_shown = {}
    M.state.dialogs_shown = {}
    M.state.os_execute_calls = {}
    M.state.scheduled_tasks = {}
    M.state.unscheduled_tasks = {}
    M.state.removed_files = {}
    M.state.purged_dirs = {}
    M.state.close_calls = {}
end

-- Mock ffi/util module
function M.mock_ffi_util()
    package.loaded["ffi/util"] = {
        template = function(s, ...)
            local args = {...}
            local result = s
            for i, v in ipairs(args) do
                result = result:gsub("%%" .. i, tostring(v))
            end
            return result
        end,
        usleep = function() end,
        sleep = function() end,
        isSubProcessDone = function() return true end,
        terminateSubProcess = function() end,
        purgeDir = function(dir)
            table.insert(M.state.purged_dirs, dir)
            return true
        end,
    }
end

-- Mock datastorage module
function M.mock_datastorage(data_dir)
    data_dir = data_dir or "/tmp/koreader"
    package.loaded["datastorage"] = {
        getFullDataDir = function() return data_dir end,
    }
end

-- Mock device module
function M.mock_device(opts)
    opts = opts or {}
    package.loaded["device"] = {
        isKindle = function() return opts.is_kindle or false end,
        retrieveNetworkInfo = function()
            return opts.network_info or "WiFi: 192.168.1.100"
        end,
    }
end

-- Mock dispatcher module
function M.mock_dispatcher()
    package.loaded["dispatcher"] = {
        registerAction = function() end,
    }
end

-- Mock UI widgets
function M.mock_ui_widgets()
    package.loaded["ui/widget/infomessage"] = {
        new = function(self, o)
            table.insert(M.state.notifications_shown, o)
            return o
        end,
    }
    package.loaded["ui/widget/notification"] = {
        new = function(self, o)
            table.insert(M.state.notifications_shown, o)
            return o
        end,
    }
    package.loaded["ui/widget/inputdialog"] = {
        new = function(self, o)
            o._type = "InputDialog"
            table.insert(M.state.dialogs_shown, o)
            o.getInputText = function()
                return o._mock_input or o.input or ""
            end
            o.onShowKeyboard = function() end
            return o
        end,
    }
    package.loaded["ui/widget/pathchooser"] = {
        new = function(self, o)
            o._type = "PathChooser"
            table.insert(M.state.dialogs_shown, o)
            return o
        end,
    }
    package.loaded["ui/widget/buttondialog"] = {
        new = function(self, o)
            o._type = "ButtonDialog"
            table.insert(M.state.dialogs_shown, o)
            return o
        end,
    }
    package.loaded["ui/widget/confirmbox"] = {
        new = function(self, o)
            o._type = "ConfirmBox"
            table.insert(M.state.dialogs_shown, o)
            if o.ok_callback then
                o._ok_callback = o.ok_callback
            end
            return o
        end,
    }
end

-- Mock UIManager
function M.mock_uimanager()
    package.loaded["ui/uimanager"] = {
        show = function() end,
        close = function(self, dialog)
            table.insert(M.state.close_calls, dialog)
        end,
        scheduleIn = function(self, delay, callback)
            table.insert(M.state.scheduled_tasks, { delay = delay, callback = callback })
        end,
        unschedule = function(self, callback)
            table.insert(M.state.unscheduled_tasks, callback)
        end,
        preventStandby = function() end,
        allowStandby = function() end,
        getElapsedTimeSinceBoot = function() return { sec = 0, usec = 0 } end,
    }
end

-- Mock NetworkManager
function M.mock_network_manager(opts)
    opts = opts or {}
    package.loaded["ui/network/manager"] = {
        isOnline = function() return opts.is_online ~= false end,
        isConnected = function() return opts.is_connected ~= false end,
        runWhenOnline = function(self, callback)
            if opts.is_online ~= false then callback() end
        end,
        runWhenConnected = function(self, callback)
            if opts.is_connected ~= false then callback() end
        end,
        willRerunWhenOnline = function(self, callback)
            if opts.is_online == false then
                if callback then callback() end
                return true
            end
            return false
        end,
        willRerunWhenConnected = function(self, callback)
            if opts.is_connected == false then
                if callback then callback() end
                return true
            end
            return false
        end,
    }
end

-- Mock WidgetContainer base class
function M.mock_widget_container()
    local WidgetContainer = {}
    WidgetContainer.__index = WidgetContainer

    function WidgetContainer:extend(o)
        o = o or {}
        setmetatable(o, self)
        self.__index = self
        o.__index = o
        return o
    end

    function WidgetContainer:new(o)
        o = o or {}
        setmetatable(o, self)
        if o.init then o:init() end
        return o
    end

    package.loaded["ui/widget/container/widgetcontainer"] = WidgetContainer
end

-- Mock logger
function M.mock_logger(capture)
    local logger = {
        calls = { err = {}, warn = {}, info = {}, dbg = {} },
    }

    local function make_log_fn(level)
        return function(...)
            if capture then
                local args = {...}
                table.insert(logger.calls[level], table.concat(args, " "))
            end
        end
    end

    logger.err = make_log_fn("err")
    logger.warn = make_log_fn("warn")
    logger.info = make_log_fn("info")
    logger.dbg = make_log_fn("dbg")

    package.loaded["logger"] = logger
    return logger
end

-- Mock util module with common implementations
function M.mock_util(opts)
    opts = opts or {}

    package.loaded["util"] = {
        shell_escape = function(t)
            local escaped = {}
            for _, v in ipairs(t) do
                if v == nil then
                    table.insert(escaped, "''")
                else
                    table.insert(escaped, "'" .. tostring(v):gsub("'", "'\\''") .. "'")
                end
            end
            return table.concat(escaped, " ")
        end,

        pathExists = opts.pathExists or function(path)
            if path == "/tmp/koreader/plugins/localsend.koplugin" then return true end
            if path == "/tmp/koreader/plugins/localsend.koplugin/localsend" then return true end
            return false
        end,

        makePath = opts.makePath or function(path) return true end,

        readFromFile = opts.readFromFile or function(path)
            if path:match("^/proc/%d+/cmdline$") then
                return "/tmp/localsend\0recv\0"
            end
            return nil
        end,

        removeFile = opts.removeFile or function(path)
            table.insert(M.state.removed_files, path)
            return true
        end,

        directoryExists = opts.directoryExists or function(path)
            -- Default: cache directory exists
            if path:match("/cache/localsend") then return true end
            return false
        end,

        splitFilePathName = function(file)
            if file == nil or file == "" then return "", "" end
            if not file:find("/") then return "", file end
            return file:match("(.*/)(.*)")
        end,

        getFriendlySize = function(size)
            if size >= 1048576 then
                return string.format("%.1f MB", size / 1048576)
            elseif size >= 1024 then
                return string.format("%.1f KB", size / 1024)
            else
                return string.format("%d B", size)
            end
        end,
    }
end

-- Mock gettext
function M.mock_gettext()
    package.loaded["gettext"] = setmetatable({}, {
        __call = function(_, s) return s end,
    })
end

-- Simple but functional JSON encoder for tests
local function simple_json_encode(t)
    if type(t) ~= "table" then return tostring(t) end
    local parts = {}
    for k, v in pairs(t) do
        local val
        if type(v) == "string" then
            val = '"' .. v .. '"'
        elseif type(v) == "table" then
            val = simple_json_encode(v)
        else
            val = tostring(v)
        end
        table.insert(parts, '"' .. k .. '":' .. val)
    end
    table.sort(parts) -- For deterministic output
    return "{" .. table.concat(parts, ",") .. "}"
end

-- Mock json (opt-in only)
-- By default, the real json module from KOReader (LuaJSON) is used.
-- Call mock_json() only when you need custom encode/decode behavior.
function M.mock_json(opts)
    opts = opts or {}
    package.loaded["json"] = {
        encode = opts.encode or simple_json_encode,
        decode = opts.decode or function(s) return {} end,
    }
end

-- Mock G_reader_settings
function M.mock_settings()
    _G.G_reader_settings = {
        readSetting = function(self, key) return M.state.settings[key] end,
        saveSetting = function(self, key, value) M.state.settings[key] = value end,
        isTrue = function(self, key) return M.state.settings[key] == true end,
        nilOrTrue = function(self, key) return M.state.settings[key] ~= false end,
        flipNilOrTrue = function(self, key)
            M.state.settings[key] = not self:nilOrTrue(key)
        end,
        flipNilOrFalse = function(self, key)
            M.state.settings[key] = not self:isTrue(key)
        end,
        _settings = M.state.settings,
        _reset = function()
            for k in pairs(M.state.settings) do M.state.settings[k] = nil end
        end,
    }
end

-- Mock dofile for _meta.lua
function M.mock_dofile(version)
    version = version or "v1.1.1"
    _G.dofile = function(path)
        if path:match("_meta%.lua$") then
            return { version = version }
        end
        error("dofile not mocked for: " .. path)
    end
end

-- Mock os.execute
function M.mock_os_execute(handler)
    _G.os.execute = function(cmd)
        table.insert(M.state.os_execute_calls, cmd)
        if handler then
            return handler(cmd)
        end
        return 0
    end
end

-- Mock os.remove
function M.mock_os_remove()
    _G.os.remove = function(path)
        table.insert(M.state.removed_files, path)
        return true
    end
end

-- Mock pluginshare
function M.mock_pluginshare()
    package.loaded["pluginshare"] = {}
end

-- Mock localsend_state module
function M.mock_localsend_state()
    package.loaded["localsend_state"] = {
        ServerState = {
            user_stopped = false,
            was_running_before_suspend = false,
            was_running_before_disconnect = false,
            last_log_position = 0,
            transfer_count = 0,
            last_sentinel_value = nil,
            -- Send-related state
            discovered_devices = {},
            scan_in_progress = false,
            send_in_progress = false,
            scan_cancelled = false,
            send_cancelled = false,
            server_op_id = 0,
            stop_in_progress = false,
        },
    }
    -- Expose for testing (matches LocalSend._ServerState pattern)
    package.loaded["localsend_state"]._ServerState = package.loaded["localsend_state"].ServerState
end

-- Reset localsend_state module to fresh state
function M.reset_localsend_state()
    if package.loaded["localsend_state"] then
        local state = package.loaded["localsend_state"]
        state.ServerState.user_stopped = false
        state.ServerState.was_running_before_suspend = false
        state.ServerState.was_running_before_disconnect = false
        state.ServerState.last_log_position = 0
        state.ServerState.transfer_count = 0
        state.ServerState.last_sentinel_value = nil
        state.ServerState.discovered_devices = {}
        state.ServerState.scan_in_progress = false
        state.ServerState.send_in_progress = false
        state.ServerState.scan_cancelled = false
        state.ServerState.send_cancelled = false
        state.ServerState.server_op_id = 0
        state.ServerState.stop_in_progress = false
    end
end

-- Load localsend_utils (real module, not mocked)
function M.load_localsend_utils()
    package.loaded["localsend_utils"] = require("localsend_utils")
end

-- Load localsend_constants (real module, not mocked)
function M.load_localsend_constants()
    package.loaded["localsend_constants"] = require("localsend_constants")
end

-- Load localsend_update (real module, not mocked)
function M.load_localsend_update()
    package.loaded["localsend_update"] = require("localsend_update")
end

-- Load localsend_routing (real module, not mocked)
function M.load_localsend_routing()
    package.loaded["localsend_routing"] = require("localsend_routing")
end

-- Load localsend_transfers (real module, not mocked)
function M.load_localsend_transfers()
    package.loaded["localsend_transfers"] = require("localsend_transfers")
end

-- Load localsend_dialogs (real module, not mocked)
function M.load_localsend_dialogs()
    package.loaded["localsend_dialogs"] = require("localsend_dialogs")
end

-- Load localsend_firewall (real module, not mocked)
function M.load_localsend_firewall()
    package.loaded["localsend_firewall"] = require("localsend_firewall")
end

-- Load localsend_server (real module, not mocked)
function M.load_localsend_server()
    package.loaded["localsend_server"] = require("localsend_server")
end

-- Load localsend_discovery (real module, not mocked)
function M.load_localsend_discovery()
    package.loaded["localsend_discovery"] = require("localsend_discovery")
end

-- Load localsend_sender (real module, not mocked)
function M.load_localsend_sender()
    package.loaded["localsend_sender"] = require("localsend_sender")
end

-- Clear cached main module to get fresh instance
function M.reset_main()
    package.loaded["main"] = nil
    package.loaded["localsend_constants"] = nil  -- Also reset constants module
    package.loaded["localsend_update"] = nil  -- Also reset update module
    package.loaded["localsend_routing"] = nil  -- Also reset routing module
    package.loaded["localsend_transfers"] = nil  -- Also reset transfers module
    package.loaded["localsend_dialogs"] = nil  -- Also reset dialogs module
    package.loaded["localsend_firewall"] = nil  -- Also reset firewall module
    package.loaded["localsend_server"] = nil  -- Also reset server module
    package.loaded["localsend_discovery"] = nil  -- Also reset discovery module
    package.loaded["localsend_sender"] = nil  -- Also reset sender module
end

-- Complete setup - call all standard mocks
-- This replaces the 80+ lines of setup() in most test files
function M.setup_complete(opts)
    opts = opts or {}

    -- Clear any broken module state from previous test blocks
    -- This ensures fresh module loading even if prior tests broke the cache
    M.reset_main()

    M.mock_ffi_util()
    M.mock_datastorage(opts.data_dir)
    M.mock_device(opts.device)
    M.mock_dispatcher()
    M.mock_ui_widgets()
    M.mock_uimanager()
    M.mock_network_manager(opts.network)
    M.mock_widget_container()
    M.mock_logger(opts.capture_logs)
    M.mock_util(opts.util)
    M.mock_gettext()
    -- Load real json module (LuaJSON) — specs can override package.loaded["json"] if needed
    if not package.loaded["json"] then
        package.loaded["json"] = require("json")
    end
    if opts.json then M.mock_json(opts.json) end
    M.mock_settings()
    M.mock_dofile(opts.version)
    M.mock_pluginshare()
    M.mock_localsend_state()
    M.load_localsend_utils()
    M.load_localsend_constants()
    M.load_localsend_update()
    M.load_localsend_routing()
    M.load_localsend_transfers()
    M.load_localsend_dialogs()
    M.load_localsend_firewall()
    M.load_localsend_server()
    M.load_localsend_discovery()
    M.load_localsend_sender()
end

-- Standard before_each that resets state
function M.before_each()
    M.reset_state()
    M.reset_localsend_state()
    M.reset_main()
end

-- Create a standard LocalSend instance for testing
function M.create_instance()
    local LocalSend = require("main")
    return LocalSend:new{
        ui = { menu = { registerToMainMenu = function() end } }
    }, LocalSend
end

-- Helper to find notification by pattern
function M.find_notification(pattern)
    for _, n in ipairs(M.state.notifications_shown) do
        if n.text and n.text:match(pattern) then
            return n
        end
    end
    return nil
end

-- Helper to find os.execute call by pattern
function M.find_execute_call(pattern)
    for _, cmd in ipairs(M.state.os_execute_calls) do
        if cmd:match(pattern) then
            return cmd
        end
    end
    return nil
end

-- Helper to find dialog by type
function M.find_dialog(dialog_type)
    for _, d in ipairs(M.state.dialogs_shown) do
        if d._type == dialog_type then
            return d
        end
    end
    return nil
end

-- Helper to find dialog by type and title pattern
function M.find_dialog_with_title(dialog_type, title_pattern)
    for _, d in ipairs(M.state.dialogs_shown) do
        if d._type == dialog_type and d.title and d.title:match(title_pattern) then
            return d
        end
    end
    return nil
end

return M
