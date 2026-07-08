require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")
local ffiUtil = require("ffi/util")
local Device = require("device")

-- Tests for network info display in the server start notification.
-- Wraps the real Device.retrieveNetworkInfo (save+restore) and uses a real
-- temp save directory.

describe("Network Info Display", function()
    local SAVE
    local orig_retrieveNetworkInfo

    setup(function()
        helper.setup_complete()
        SAVE = get_test_data_dir() .. "/save"
        util.makePath(SAVE)
        orig_retrieveNetworkInfo = Device.retrieveNetworkInfo
    end)

    teardown(function()
        pcall(ffiUtil.purgeDir, SAVE)
    end)

    before_each(function()
        helper.before_each()
    end)

    after_each(function()
        Device.retrieveNetworkInfo = orig_retrieveNetworkInfo
    end)

    local function with_network_info(fn)
        Device.retrieveNetworkInfo = fn
    end

    local function create_instance_and_start()
        local instance = helper.create_instance()
        instance.save_dir = SAVE
        instance.port = "53317"
        instance.clearTransferLog = function() end
        instance.openFirewall = function() end
        instance.exportExtRouting = function()
            return nil
        end

        local check_count = 0
        instance.isRunning = function()
            check_count = check_count + 1
            return check_count > 1
        end

        helper.reset_state()
        instance:start()
        return instance
    end

    describe("when retrieveNetworkInfo is available", function()
        it("shows network info in success notification", function()
            with_network_info(function()
                return "WiFi: 192.168.1.100"
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("192%.168%.1%.100") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("includes WiFi info when returned by device", function()
            with_network_info(function()
                return "Interface: wlan0\nIP: 10.0.0.42\nSSID: MyNetwork"
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and (n.text:match("10%.0%.0%.42") or n.text:match("wlan0")) then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)
    end)

    describe("when retrieveNetworkInfo is nil (old KOReader)", function()
        it("shows success message without network info", function()
            with_network_info(nil)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("LocalSend Ready") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("still shows device name", function()
            with_network_info(nil)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("KOReader") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)
    end)

    describe("when retrieveNetworkInfo returns empty string", function()
        it("shows success notification without network section", function()
            with_network_info(function()
                return ""
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("LocalSend Ready") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)
    end)

    describe("notification content structure", function()
        it("includes device name in notification", function()
            with_network_info(function()
                return "WiFi"
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("KOReader") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("shows custom device name when configured", function()
            helper.state.settings["LocalSend_device_name"] = "My Kindle"
            with_network_info(function()
                return "WiFi"
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("My Kindle") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("has timeout on success notification", function()
            with_network_info(function()
                return "WiFi"
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("LocalSend Ready") and n.timeout then
                    found = true
                    assert.is_true(n.timeout > 0)
                    break
                end
            end
            assert.is_true(found)
        end)

        it("shows PIN status when PIN is configured", function()
            helper.state.settings["LocalSend_pin"] = "1234"
            with_network_info(function()
                return "WiFi"
            end)
            create_instance_and_start()
            local found = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("PIN") then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("does not show PIN status when PIN is not configured", function()
            with_network_info(function()
                return "WiFi"
            end)
            create_instance_and_start()
            local found_pin = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and n.text:match("LocalSend Ready") then
                    if n.text:match("PIN") then
                        found_pin = true
                    end
                    break
                end
            end
            assert.is_false(found_pin)
        end)
    end)
end)
