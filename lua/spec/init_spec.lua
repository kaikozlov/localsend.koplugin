require("busted.runner")()
local helper = require("spec.spec_helper")

local Dispatcher = require("dispatcher")
local NetworkMgr = require("ui/network/manager")

-- Tests for init() function

describe("init() function", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("settings loading", function()
        it("should always use the protocol port 53317", function()
            helper.state.settings["LocalSend_port"] = "12345"
            local instance = helper.create_instance()
            assert.equal("53317", instance.port)
        end)

        it("should load save_dir from settings", function()
            helper.state.settings["LocalSend_save_dir"] = "/custom/path"
            local instance = helper.create_instance()
            assert.equal("/custom/path", instance.save_dir)
        end)

        it("should use default save_dir when not set", function()
            local instance = helper.create_instance()
            assert.equal("/mnt/us/documents", instance.save_dir)
        end)

        it("should load device_name from settings", function()
            helper.state.settings["LocalSend_device_name"] = "My Device"
            local instance = helper.create_instance()
            assert.equal("My Device", instance.device_name)
        end)

        it("should default to empty device_name", function()
            local instance = helper.create_instance()
            assert.equal("", instance.device_name)
        end)

        it("should load pin from settings", function()
            helper.state.settings["LocalSend_pin"] = "1234"
            local instance = helper.create_instance()
            assert.equal("1234", instance.pin)
        end)

        it("should default to empty pin", function()
            local instance = helper.create_instance()
            assert.equal("", instance.pin)
        end)

        it("should load use_https from settings (default true)", function()
            local instance = helper.create_instance()
            assert.is_true(instance.use_https)
        end)

        it("should load use_https=false when explicitly disabled", function()
            helper.state.settings["LocalSend_use_https"] = false
            local instance = helper.create_instance()
            assert.is_false(instance.use_https)
        end)

        it("should load autostart from settings", function()
            helper.state.settings["LocalSend_autostart"] = true
            local LocalSend = require("main")
            LocalSend.start = function() end
            local instance = helper.create_instance()
            assert.is_true(instance.autostart)
        end)

        it("should load accept_ext from settings", function()
            helper.state.settings["LocalSend_accept_ext"] = "epub,pdf"
            local instance = helper.create_instance()
            assert.equal("epub,pdf", instance.accept_ext)
        end)

        it("should load use_webrtc from settings (default false)", function()
            local instance = helper.create_instance()
            assert.is_false(instance.use_webrtc)
        end)

        it("should load ext_dirs from settings", function()
            helper.state.settings["LocalSend_ext_dirs"] = { epub = "/books", pdf = "/docs" }
            local instance = helper.create_instance()
            assert.same({ epub = "/books", pdf = "/docs" }, instance.ext_dirs)
        end)

        it("should default ext_dirs to empty table", function()
            local instance = helper.create_instance()
            assert.same({}, instance.ext_dirs)
        end)

        it("should load routing_accept_all from settings", function()
            helper.state.settings["LocalSend_routing_accept_all"] = true
            local instance = helper.create_instance()
            assert.is_true(instance.routing_accept_all)
        end)

        it("should load routing_enabled from settings", function()
            helper.state.settings["LocalSend_routing_enabled"] = true
            local instance = helper.create_instance()
            assert.is_true(instance.routing_enabled)
        end)
    end)

    describe("menu registration", function()
        it("should register to main menu", function()
            local LocalSend = require("main")
            local menu_registered = false
            local instance = LocalSend:new({
                ui = {
                    menu = {
                        registerToMainMenu = function()
                            menu_registered = true
                        end,
                    },
                },
            })
            assert.is_true(menu_registered)
        end)
    end)

    describe("dispatcher registration", function()
        it("should call onDispatcherRegisterActions", function()
            local LocalSend = require("main")
            local dispatcher_called = false
            LocalSend.onDispatcherRegisterActions = function()
                dispatcher_called = true
            end
            local instance = helper.create_instance()
            assert.is_true(dispatcher_called)
        end)
    end)

    describe("autostart logic", function()
        local orig_isConnected

        before_each(function()
            orig_isConnected = NetworkMgr.isConnected
            -- Autostart only reaches self:start() when the network is up.
            NetworkMgr.isConnected = function()
                return true
            end
        end)

        after_each(function()
            NetworkMgr.isConnected = orig_isConnected
        end)

        it("should call start() when autostart enabled and not user_stopped", function()
            helper.state.settings["LocalSend_autostart"] = true
            local LocalSend = require("main")
            local start_called = false
            LocalSend.start = function()
                start_called = true
            end
            local instance = helper.create_instance()
            assert.is_true(start_called)
        end)

        it("should NOT call start() when autostart disabled", function()
            helper.state.settings["LocalSend_autostart"] = false
            local LocalSend = require("main")
            local start_called = false
            LocalSend.start = function()
                start_called = true
            end
            local instance = helper.create_instance()
            assert.is_false(start_called)
        end)

        it("should NOT call start() when user_stopped flag is set", function()
            helper.state.settings["LocalSend_autostart"] = true
            local LocalSend = require("main")
            LocalSend._ServerState.user_stopped = true
            local start_called = false
            LocalSend.start = function()
                start_called = true
            end
            local instance = helper.create_instance()
            assert.is_false(start_called)
        end)
    end)
end)

-- Tests for dispatcher action registration
describe("onDispatcherRegisterActions", function()
    local LocalSend
    local registered_actions = {}
    local orig_register

    setup(function()
        helper.setup_complete()
        orig_register = Dispatcher.registerAction
        Dispatcher.registerAction = function(_, action_id, action_def)
            registered_actions[action_id] = action_def
        end
    end)

    teardown(function()
        Dispatcher.registerAction = orig_register
    end)

    before_each(function()
        helper.before_each()
        for k in pairs(registered_actions) do
            registered_actions[k] = nil
        end
    end)

    describe("action registration", function()
        it("should register toggle_localsend_server action", function()
            helper.create_instance()
            assert.is_not_nil(registered_actions["toggle_localsend_server"])
        end)

        it("should set category to 'none'", function()
            helper.create_instance()
            assert.equal("none", registered_actions["toggle_localsend_server"].category)
        end)

        it("should set event to 'ToggleLocalSend'", function()
            helper.create_instance()
            assert.equal("ToggleLocalSend", registered_actions["toggle_localsend_server"].event)
        end)

        it("should have a title", function()
            helper.create_instance()
            assert.is_not_nil(registered_actions["toggle_localsend_server"].title)
            assert.truthy(
                registered_actions["toggle_localsend_server"].title:match("LocalSend")
                    or registered_actions["toggle_localsend_server"].title:match("Toggle")
            )
        end)

        it("should set general to true", function()
            helper.create_instance()
            assert.is_true(registered_actions["toggle_localsend_server"].general)
        end)

        it("should not put a separator between LocalSend actions", function()
            helper.create_instance()
            assert.is_nil(registered_actions["toggle_localsend_server"].separator)
            assert.is_nil(registered_actions["send_file_localsend"].separator)
        end)

        it("should register send_file_localsend action", function()
            helper.create_instance()
            assert.is_not_nil(registered_actions["send_file_localsend"])
            assert.equal("none", registered_actions["send_file_localsend"].category)
            assert.equal("ShowLocalSendFileSendFlow", registered_actions["send_file_localsend"].event)
            assert.is_true(registered_actions["send_file_localsend"].general)
            assert.truthy(registered_actions["send_file_localsend"].title:match("LocalSend"))
            assert.truthy(registered_actions["send_file_localsend"].title:match("send file"))
        end)

        it("should register send_current_book_localsend action", function()
            helper.create_instance()
            assert.is_not_nil(registered_actions["send_current_book_localsend"])
            assert.equal("none", registered_actions["send_current_book_localsend"].category)
            assert.equal("SendCurrentBookWithLocalSend", registered_actions["send_current_book_localsend"].event)
            assert.is_true(registered_actions["send_current_book_localsend"].general)
            assert.is_true(registered_actions["send_current_book_localsend"].reader)
            assert.is_true(registered_actions["send_current_book_localsend"].separator)
            assert.truthy(registered_actions["send_current_book_localsend"].title:match("LocalSend"))
            assert.truthy(registered_actions["send_current_book_localsend"].title:match("current book"))
        end)
    end)

    describe("dispatcher event handlers", function()
        it("should make send_file_localsend event resolve to showFileSendFlow", function()
            local instance = helper.create_instance()
            local called = false
            instance.showFileSendFlow = function()
                called = true
            end
            local action = registered_actions["send_file_localsend"]
            local handler_name = "on" .. action.event
            assert.is_function(instance[handler_name])
            instance[handler_name](instance)
            assert.is_true(called)
        end)

        it("should make send_current_book_localsend event resolve to sendCurrentBook", function()
            local instance = helper.create_instance()
            local called = false
            instance.sendCurrentBook = function()
                called = true
            end
            local action = registered_actions["send_current_book_localsend"]
            local handler_name = "on" .. action.event
            assert.is_function(instance[handler_name])
            instance[handler_name](instance)
            assert.is_true(called)
        end)
    end)

    describe("dispatcher integration", function()
        it("should be called during init", function()
            helper.create_instance()
            assert.is_not_nil(registered_actions["toggle_localsend_server"])
        end)
    end)
end)

-- Tests for _meta.lua loading
describe("_meta.lua loading", function()
    local original_dofile

    setup(function()
        original_dofile = _G.dofile
        helper.setup_complete()
    end)

    teardown(function()
        _G.dofile = original_dofile
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("when _meta.lua is missing", function()
        it("should gracefully handle missing _meta.lua file", function()
            _G.dofile = function(path)
                if path:match("_meta%.lua$") then
                    error("cannot open " .. path .. ": No such file or directory")
                end
            end
            local ok = pcall(function()
                return require("main")
            end)
            assert.is_true(ok, "Plugin should load gracefully when _meta.lua is missing")
        end)
    end)

    describe("when _meta.lua is corrupted", function()
        it("should gracefully handle corrupted _meta.lua file", function()
            _G.dofile = function(path)
                if path:match("_meta%.lua$") then
                    error("syntax error in " .. path)
                end
            end
            local ok = pcall(function()
                return require("main")
            end)
            assert.is_true(ok, "Plugin should load gracefully when _meta.lua has syntax error")
        end)
    end)

    describe("when _meta.lua returns nil", function()
        it("should gracefully handle when _meta.lua returns nil", function()
            _G.dofile = function(path)
                if path:match("_meta%.lua$") then
                    return nil
                end
            end
            local ok = pcall(function()
                return require("main")
            end)
            assert.is_true(ok, "Plugin should load gracefully when _meta.lua returns nil")
        end)
    end)

    describe("when _meta.lua is valid", function()
        it("should load version from valid _meta.lua", function()
            _G.dofile = function(path)
                if path:match("_meta%.lua$") then
                    return { version = "v1.2.3", name = "LocalSend" }
                end
            end
            local ok = pcall(function()
                return require("main")
            end)
            assert.is_true(ok)
        end)
    end)
end)
