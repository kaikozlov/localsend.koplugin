require("busted.runner")()
local helper = require("spec.spec_helper")

local UIManager = require("ui/uimanager")
local InfoMessage = require("ui/widget/infomessage")
local Notification = require("ui/widget/notification")
local ButtonDialog = require("ui/widget/buttondialog")
local util = require("util")
local json = require("json")
local logger = require("logger")
local T = require("ffi/util").template
local _ = require("gettext")

describe("localsend_discovery", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.reset_state()
        helper.reset_localsend_state()
    end)

    -- Load a fresh discovery module wired to real KOReader deps. json_dep lets a
    -- test substitute a controlled decoder without clobbering the real json module.
    local function init_discovery(json_dep)
        package.loaded["localsend_discovery"] = nil
        local discovery = require("localsend_discovery")
        discovery.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            ButtonDialog = ButtonDialog,
            util = util,
            json = json_dep or json,
            logger = logger,
            T = T,
            _ = _,
        }, { binary_path = "/tmp/localsend" })
        return discovery
    end

    describe("parseDevices", function()
        local discovery

        before_each(function()
            discovery = init_discovery()
        end)

        it("returns empty array for nil input", function()
            assert.are.same({}, discovery.parseDevices(nil))
        end)

        it("returns empty array for empty string", function()
            assert.are.same({}, discovery.parseDevices(""))
        end)

        it("returns empty array for invalid JSON", function()
            assert.are.same({}, discovery.parseDevices("not valid json"))
        end)

        it("parses LAN devices from JSON", function()
            local json_mock = {
                decode = function()
                    return {
                        lan = {
                            {
                                ip = "192.168.1.50",
                                port = 53317,
                                alias = "iPhone",
                                version = "2.1",
                                protocol = "https",
                            },
                        },
                        webrtc = {},
                    }
                end,
            }
            discovery = init_discovery(json_mock)
            local devices = discovery.parseDevices('{"lan":[{"ip":"192.168.1.50"}]}')
            assert.equals(1, #devices)
            assert.equals("lan", devices[1].type)
            assert.equals("iPhone", devices[1].alias)
            assert.equals("192.168.1.50", devices[1].ip)
            assert.equals(53317, devices[1].port)
            assert.equals("https", devices[1].protocol)
        end)

        it("parses WebRTC devices from JSON", function()
            local json_mock = {
                decode = function()
                    return {
                        lan = {},
                        webrtc = {
                            { id = "abc-123", alias = "Browser", version = "2.1" },
                        },
                    }
                end,
            }
            discovery = init_discovery(json_mock)
            local devices = discovery.parseDevices('{"webrtc":[{"id":"abc-123"}]}')
            assert.equals(1, #devices)
            assert.equals("webrtc", devices[1].type)
            assert.equals("Browser", devices[1].alias)
            assert.equals("abc-123", devices[1].id)
        end)

        it("parses mixed LAN and WebRTC devices", function()
            local json_mock = {
                decode = function()
                    return {
                        lan = {
                            { ip = "192.168.1.50", port = 53317, alias = "Phone", version = "2.1", protocol = "https" },
                        },
                        webrtc = {
                            { id = "abc-123", alias = "Browser", version = "2.1" },
                        },
                    }
                end,
            }
            discovery = init_discovery(json_mock)
            local devices = discovery.parseDevices("{}")
            assert.equals(2, #devices)
            assert.equals("lan", devices[1].type)
            assert.equals("webrtc", devices[2].type)
        end)
    end)

    describe("getDeviceDisplayText", function()
        local discovery
        before_each(function()
            discovery = init_discovery()
        end)

        it("formats LAN device with IP", function()
            assert.equals("[LAN] iPhone (192.168.1.50)", discovery.getDeviceDisplayText({ type = "lan", alias = "iPhone", ip = "192.168.1.50" }))
        end)

        it("formats WebRTC device without IP", function()
            assert.equals("[WebRTC] Browser", discovery.getDeviceDisplayText({ type = "webrtc", alias = "Browser", id = "abc-123" }))
        end)
    end)

    describe("getCachedDevices", function()
        local discovery
        before_each(function()
            discovery = init_discovery()
        end)

        it("returns empty array when no devices cached", function()
            assert.are.same({}, discovery.getCachedDevices())
        end)

        it("returns cached devices from ServerState", function()
            local state = require("localsend_state")
            state.ServerState.discovered_devices = {
                { type = "lan", alias = "Phone", ip = "192.168.1.50" },
            }
            assert.equals(1, #discovery.getCachedDevices())
            assert.equals("Phone", discovery.getCachedDevices()[1].alias)
        end)
    end)

    describe("showDeviceSelector", function()
        local discovery
        before_each(function()
            discovery = init_discovery()
        end)

        it("shows info message when no devices found", function()
            local callback_called = false
            local callback_device = "not_nil"
            discovery.showDeviceSelector({}, function(device)
                callback_called = true
                callback_device = device
            end)
            assert.is_true(callback_called)
            assert.is_nil(callback_device)
            assert.equals(1, #helper.state.notifications_shown)
            assert.truthy(helper.state.notifications_shown[1].text:match("No devices found"))
        end)

        it("shows button dialog for available devices", function()
            discovery.showDeviceSelector({
                { type = "lan", alias = "Phone", ip = "192.168.1.50" },
            }, function() end)
            local dialog = helper.find_dialog("ButtonDialog")
            assert.is_not_nil(dialog)
            assert.equals("Select target device", dialog.title)
        end)
    end)

    describe("scan timeout behavior", function()
        local discovery
        local constants

        before_each(function()
            package.loaded["localsend_constants"] = nil
            discovery = init_discovery()
            constants = require("localsend_constants")
        end)

        it("SCAN_MAX_POLL_DURATION is defined", function()
            assert.is_not_nil(constants.SCAN_MAX_POLL_DURATION)
            assert.is_number(constants.SCAN_MAX_POLL_DURATION)
        end)

        it("SCAN_MAX_POLL_DURATION has reasonable value (5-120 seconds)", function()
            assert.is_true(constants.SCAN_MAX_POLL_DURATION >= 5)
            assert.is_true(constants.SCAN_MAX_POLL_DURATION <= 120)
        end)

        it("SCAN_POLL_INTERVAL is defined", function()
            assert.is_not_nil(constants.SCAN_POLL_INTERVAL)
            assert.is_number(constants.SCAN_POLL_INTERVAL)
        end)
    end)
end)
