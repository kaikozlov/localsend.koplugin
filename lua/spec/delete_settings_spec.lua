--- Tests for deletePluginSettings hook
-- Verifies that the LocalSend plugin correctly cleans up all persistent state
-- when deletePluginSettings() is called by PluginLoader.

require("busted.runner")()

local helper = require("spec.spec_helper")
local util = require("util")

-- All setting keys used by the plugin (must match main.lua deletePluginSettings)
local ALL_SETTINGS_KEYS = {
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

local function plugin_dir()
    return helper.runtime_plugin_dir()
end

-- Certs directory that should be purged
local function certs_dir()
    return plugin_dir() .. "/certs"
end

-- Plugin-path based files that deletePluginSettings should remove
local function plugin_files()
    return {
        plugin_dir() .. "/ext_routing.json",
        plugin_dir() .. "/.reinstall_required",
    }
end

-- Temporary runtime files (from constants)
local TMP_FILES = {
    "/tmp/localsend_koreader.pid",
    "/tmp/localsend_transfers.log",
    "/tmp/localsend_notify",
    "/tmp/localsend_signaling.id",
    "/tmp/localsend_send.pid",
    "/tmp/localsend_send.out",
    "/tmp/localsend_scan.json",
    "/tmp/localsend_server.out",
}

describe("deletePluginSettings", function()
    local instance

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()

        -- Populate all settings with sample data.
        local test_defaults = {
            LocalSend_port = "53317",
            LocalSend_save_dir = "/documents",
            LocalSend_device_name = "TestDevice",
            LocalSend_use_https = true,
            LocalSend_autostart = false,
            LocalSend_pin = "1234",
            LocalSend_accept_ext = "epub,pdf",
            LocalSend_use_webrtc = false,
            LocalSend_ext_dirs = { epub = "/books" },
            LocalSend_routing_accept_all = false,
            LocalSend_routing_enabled = false,
            LocalSend_auto_update_check = true,
            LocalSend_update_check_interval_hours = 168,
            LocalSend_last_update_check = os.time(),
            LocalSend_update_available_tag = "v1.2.0",
        }
        for _, key in ipairs(ALL_SETTINGS_KEYS) do
            G_reader_settings:saveSetting(key, test_defaults[key])
        end

        instance = helper.create_instance()
    end)

    describe("removes all G_reader_settings keys", function()
        it("removes every known settings key", function()
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is.not_nil(G_reader_settings:readSetting(key), "Expected " .. key .. " to be set before cleanup")
            end

            instance:deletePluginSettings()

            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is_nil(G_reader_settings:readSetting(key), "Expected " .. key .. " to be nil after cleanup")
            end
        end)

        it("removes settings even when only some are set", function()
            helper.reset_settings()
            G_reader_settings:saveSetting("LocalSend_port", "53317")
            G_reader_settings:saveSetting("LocalSave_dir", "/documents") -- unrelated typo key
            G_reader_settings:saveSetting("LocalSend_autostart", true)

            instance:deletePluginSettings()

            assert.is_nil(G_reader_settings:readSetting("LocalSend_port"))
            assert.is_nil(G_reader_settings:readSetting("LocalSend_autostart"))
            assert.is.not_nil(G_reader_settings:readSetting("LocalSave_dir"))
        end)
    end)

    describe("removes plugin directory files", function()
        it("removes ext_routing.json and reinstall marker", function()
            for _, f in ipairs(plugin_files()) do
                local fh = assert(io.open(f, "w"))
                fh:write("x")
                fh:close()
            end

            instance:deletePluginSettings()

            for _, expected_path in ipairs(plugin_files()) do
                assert.is_false(util.pathExists(expected_path), "Expected " .. expected_path .. " to be removed")
            end
        end)
    end)

    describe("removes TLS certs directory", function()
        it("purges the certs directory", function()
            local dir = certs_dir()
            util.makePath(dir)
            local fh = assert(io.open(dir .. "/server.crt", "w"))
            fh:write("x")
            fh:close()
            assert.is_true(util.pathExists(dir))

            instance:deletePluginSettings()

            assert.is_false(util.pathExists(dir), "Expected certs directory to be purged")
        end)
    end)

    describe("removes temporary runtime files", function()
        it("removes PID, log, and other tmp files", function()
            for _, f in ipairs(TMP_FILES) do
                local fh = assert(io.open(f, "w"))
                fh:write("x")
                fh:close()
            end

            instance:deletePluginSettings()

            for _, f in ipairs(TMP_FILES) do
                assert.is_false(util.pathExists(f), "Expected " .. f .. " to be removed")
            end
        end)
    end)

    describe("resets in-memory ServerState", function()
        it("resets all ServerState fields to defaults", function()
            local ss = require("localsend_state").ServerState
            ss.user_stopped = true
            ss.was_running_before_suspend = true
            ss.transfer_count = 5
            ss.last_log_position = 100
            ss.server_op_id = 42
            ss.discovered_devices = { "device1" }
            ss.stop_in_progress = true
            -- Populate every runtime-added field too. A prior version of this
            -- test skipped these, so it couldn't catch a regression that dropped
            -- them from deletePluginSettings (they were in fact missing — see
            -- the fix that added them). Seed them so the assertion below is
            -- actually meaningful.
            ss.telemetry_cleaned = true
            ss.last_send = { success = true, message = "sent", time = 12345 }
            ss.scan_start_time = 99
            ss.scan_in_progress = true
            ss.send_in_progress = true

            instance:deletePluginSettings()

            assert.is_false(ss.user_stopped)
            assert.is_false(ss.was_running_before_suspend)
            assert.is_false(ss.was_running_before_disconnect)
            assert.are.equal(0, ss.last_log_position)
            assert.are.equal(0, ss.transfer_count)
            assert.is_nil(ss.last_sentinel_value)
            assert.is_false(ss.telemetry_cleaned)
            assert.are.same({}, ss.discovered_devices)
            assert.is_false(ss.scan_in_progress)
            assert.is_false(ss.send_in_progress)
            assert.is_false(ss.scan_cancelled)
            assert.is_false(ss.send_cancelled)
            assert.are.equal(0, ss.server_op_id)
            assert.is_false(ss.stop_in_progress)
            assert.is_nil(ss.last_send, "last_send should be cleared")
            assert.is_nil(ss.scan_start_time, "scan_start_time should be cleared")
        end)
    end)

    describe("resets PluginShare", function()
        it("clears localsend_running flag", function()
            local PluginShare = require("pluginshare")
            PluginShare.localsend_running = true

            instance:deletePluginSettings()

            assert.is_nil(PluginShare.localsend_running)
        end)
    end)

    describe("idempotency", function()
        it("does not error when called with no existing settings", function()
            helper.reset_settings()
            local ok, err = pcall(function()
                instance:deletePluginSettings()
            end)
            assert.is_true(ok, "deletePluginSettings threw: " .. tostring(err))
        end)

        it("is safe to call twice", function()
            instance:deletePluginSettings()
            local ok, err = pcall(function()
                instance:deletePluginSettings()
            end)
            assert.is_true(ok, "second deletePluginSettings threw: " .. tostring(err))
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is_nil(G_reader_settings:readSetting(key), "Expected " .. key .. " to remain nil after second call")
            end
        end)
    end)

    describe("PluginLoader integration", function()
        it("has deletePluginSettings method on the plugin class", function()
            assert.is.truthy(instance.deletePluginSettings)
            assert.are.equal("function", type(instance.deletePluginSettings))
        end)
    end)

    describe("settings snapshot diff", function()
        it("leaves no LocalSend keys in G_reader_settings after cleanup", function()
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                G_reader_settings:saveSetting(key, "test_value")
            end

            instance:deletePluginSettings()

            local remaining = {}
            for key in pairs(G_reader_settings.data) do
                if key:match("^LocalSend_") then
                    table.insert(remaining, key)
                end
            end
            assert.are.same({}, remaining, "These LocalSend_* keys were not cleaned up: " .. table.concat(remaining, ", "))
        end)

        it("does not remove unrelated settings", function()
            G_reader_settings:saveSetting("some_other_plugin_key", "keep_me")
            G_reader_settings:saveSetting("LocalSend_port", "53317")

            instance:deletePluginSettings()

            assert.are.equal("keep_me", G_reader_settings:readSetting("some_other_plugin_key"))
            assert.is_nil(G_reader_settings:readSetting("LocalSend_port"))
        end)
    end)
end)
