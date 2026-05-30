--- Tests for deletePluginSettings hook
-- Verifies that the LocalSend plugin correctly cleans up all persistent state
-- when deletePluginSettings() is called by PluginLoader.

require 'busted.runner'()

local helper = require("spec/test_helper")

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

-- Plugin-path based files that deletePluginSettings should remove
local PLUGIN_DIR = "/tmp/koreader/plugins/localsend.koplugin"
local PLUGIN_FILES = {
    PLUGIN_DIR .. "/ext_routing.json",
    PLUGIN_DIR .. "/.reinstall_required",
}

-- Certs directory that should be purged
local CERTS_DIR = PLUGIN_DIR .. "/certs"

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
    local instance, _ -- _ = LocalSend class

    before_each(function()
        helper.before_each()
        helper.setup_complete({
            data_dir = "/tmp/koreader",
            util = {
                pathExists = function(path)
                    -- Simulate existing files for cleanup tests
                    for _, f in ipairs(PLUGIN_FILES) do
                        if path == f then return true end
                    end
                    if path == CERTS_DIR then return true end
                    for _, f in ipairs(TMP_FILES) do
                        if path == f then return true end
                    end
                    if path == PLUGIN_DIR then return true end
                    if path == PLUGIN_DIR .. "/localsend" then return true end
                    return false
                end,
                removeFile = function(path)
                    table.insert(helper.state.removed_files, path)
                    return true
                end,
            },
        })

        -- Mock os.remove to capture calls (not included in setup_complete by default)
        helper.mock_os_remove()

        -- Populate all settings with sample data (use appropriate types)
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
            _G.G_reader_settings:saveSetting(key, test_defaults[key])
        end

        instance, _ = helper.create_instance()
    end)

    describe("removes all G_reader_settings keys", function()
        it("removes every known settings key", function()
            -- Verify settings exist before cleanup
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is.not_nil(_G.G_reader_settings:readSetting(key),
                    "Expected " .. key .. " to be set before cleanup")
            end

            instance:deletePluginSettings()

            -- Verify all settings are gone
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is_nil(_G.G_reader_settings:readSetting(key),
                    "Expected " .. key .. " to be nil after cleanup")
            end
        end)

        it("removes settings even when only some are set", function()
            -- Reset and set only a subset
            _G.G_reader_settings._reset()
            _G.G_reader_settings:saveSetting("LocalSend_port", "53317")
            _G.G_reader_settings:saveSetting("LocalSave_dir", "/documents")
            -- Note: LocalSave_dir is a typo — wrong key. Only LocalSend_port should be removed.
            _G.G_reader_settings:saveSetting("LocalSend_autostart", true)

            instance:deletePluginSettings()

            assert.is_nil(_G.G_reader_settings:readSetting("LocalSend_port"))
            assert.is_nil(_G.G_reader_settings:readSetting("LocalSend_autostart"))
            -- The unrelated key should remain (it's not ours to manage)
            assert.is.not_nil(_G.G_reader_settings:readSetting("LocalSave_dir"))
        end)
    end)

    describe("removes plugin directory files", function()
        it("removes ext_routing.json and reinstall marker", function()
            instance:deletePluginSettings()

            -- Check that os.remove was called for plugin-path files
            for _, expected_path in ipairs(PLUGIN_FILES) do
                local found = false
                for _, removed in ipairs(helper.state.removed_files) do
                    if removed == expected_path then
                        found = true
                        break
                    end
                end
                assert.is_true(found,
                    "Expected " .. expected_path .. " to be removed")
            end
        end)
    end)

    describe("removes TLS certs directory", function()
        it("purges the certs directory", function()
            instance:deletePluginSettings()

            -- Check that purgeDir was called for certs
            local found = false
            for _, dir in ipairs(helper.state.purged_dirs) do
                if dir == CERTS_DIR then
                    found = true
                    break
                end
            end
            assert.is_true(found, "Expected certs directory to be purged")
        end)
    end)

    describe("removes temporary runtime files", function()
        it("removes PID, log, and other tmp files", function()
            instance:deletePluginSettings()

            for _, expected_path in ipairs(TMP_FILES) do
                local found = false
                for _, removed in ipairs(helper.state.removed_files) do
                    if removed == expected_path then
                        found = true
                        break
                    end
                end
                assert.is_true(found,
                    "Expected " .. expected_path .. " to be removed")
            end
        end)
    end)

    describe("resets in-memory ServerState", function()
        it("resets all ServerState fields to defaults", function()
            -- Set some non-default values
            local ss = package.loaded["localsend_state"].ServerState
            ss.user_stopped = true
            ss.was_running_before_suspend = true
            ss.transfer_count = 5
            ss.last_log_position = 100
            ss.server_op_id = 42
            ss.discovered_devices = { "device1" }
            ss.stop_in_progress = true

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
        end)
    end)

    describe("resets PluginShare", function()
        it("clears localsend_running flag", function()
            package.loaded["pluginshare"].localsend_running = true

            instance:deletePluginSettings()

            assert.is_nil(package.loaded["pluginshare"].localsend_running)
        end)
    end)

    describe("idempotency", function()
        it("does not error when called with no existing settings", function()
            _G.G_reader_settings._reset()

            local ok, err = pcall(function() instance:deletePluginSettings() end)
            assert.is_true(ok, "deletePluginSettings threw: " .. tostring(err))
        end)

        it("is safe to call twice", function()
            instance:deletePluginSettings()

            local ok, err = pcall(function() instance:deletePluginSettings() end)
            assert.is_true(ok, "second deletePluginSettings threw: " .. tostring(err))

            -- Verify settings are still clean
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is_nil(_G.G_reader_settings:readSetting(key),
                    "Expected " .. key .. " to remain nil after second call")
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
            -- Set all keys
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                _G.G_reader_settings:saveSetting(key, "test_value")
            end

            -- Verify they exist
            for _, key in ipairs(ALL_SETTINGS_KEYS) do
                assert.is.not_nil(_G.G_reader_settings:readSetting(key))
            end

            instance:deletePluginSettings()

            -- Snapshot: no LocalSend_* keys should remain
            local remaining = {}
            for key, _ in pairs(helper.state.settings) do
                if key:match("^LocalSend_") then
                    table.insert(remaining, key)
                end
            end

            assert.are.same({}, remaining,
                "These LocalSend_* keys were not cleaned up: " ..
                table.concat(remaining, ", "))
        end)

        it("does not remove unrelated settings", function()
            _G.G_reader_settings:saveSetting("some_other_plugin_key", "keep_me")
            _G.G_reader_settings:saveSetting("LocalSend_port", "53317")

            instance:deletePluginSettings()

            assert.are.equal("keep_me",
                _G.G_reader_settings:readSetting("some_other_plugin_key"))
            assert.is_nil(_G.G_reader_settings:readSetting("LocalSend_port"))
        end)
    end)
end)
