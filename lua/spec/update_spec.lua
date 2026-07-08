require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for self-update functionality

describe("Self-Update", function()
    local LocalSend
    local http_responses
    local file_contents
    local logger
    local util, json_mod
    local orig_pathExists, orig_readFromFile, orig_json_decode, orig_io_open, orig_io_popen
    local NetworkMgr = require("ui/network/manager")
    local orig_isOnline, orig_isConnected, orig_runWhenOnline
    local orig_textviewer

    setup(function()
        helper.setup_complete()
        util = require("util")
        json_mod = require("json")
        orig_pathExists = util.pathExists
        orig_readFromFile = util.readFromFile
        orig_json_decode = json_mod.decode
        orig_io_open = io.open
        orig_io_popen = io.popen
        orig_isOnline = NetworkMgr.isOnline
        orig_isConnected = NetworkMgr.isConnected
        orig_runWhenOnline = NetworkMgr.runWhenOnline
        -- package.loaded["ui/widget/textviewer"] is replaced in several it blocks
        -- below to fake the release-notes viewer. Capture the real module so we can
        -- restore it both after every test (after_each) and at file teardown —
        -- busted is single-process, so a leak here poisons diagnostics_spec.
        orig_textviewer = package.loaded["ui/widget/textviewer"]
    end)

    teardown(function()
        util.pathExists = orig_pathExists
        util.readFromFile = orig_readFromFile
        json_mod.decode = orig_json_decode
        _G.io.open = orig_io_open
        _G.io.popen = orig_io_popen
        NetworkMgr.isOnline = orig_isOnline
        NetworkMgr.isConnected = orig_isConnected
        NetworkMgr.runWhenOnline = orig_runWhenOnline
        package.loaded["ui/widget/textviewer"] = orig_textviewer
        helper.restore_capture_logger()
    end)

    after_each(function()
        -- Each it block that fakes the viewer must not leave the fake behind for
        -- the next test; restore the real TextViewer unconditionally.
        package.loaded["ui/widget/textviewer"] = orig_textviewer
    end)

    before_each(function()
        helper.setup_complete({ capture_logs = true })
        logger = package.loaded["logger"]
        helper.reset_state()
        package.loaded["main"] = nil
        http_responses = { code = "200" }
        file_contents = {}
        -- The update flow only runs when the network looks up; pin it online so
        -- the container's flaky DNS doesn't gate these tests.
        NetworkMgr.isOnline = function()
            return true
        end
        NetworkMgr.isConnected = function()
            return true
        end
        NetworkMgr.runWhenOnline = function(self, callback)
            if callback then
                callback()
            end
        end

        -- Extend util.pathExists to check file_contents
        util.pathExists = function(path)
            if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                return true
            end
            if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                return true
            end
            if file_contents[path] ~= nil then
                return true
            end
            return false
        end

        -- Override util.readFromFile to read from file_contents
        util.readFromFile = function(path)
            return file_contents[path]
        end

        -- Custom json decode for release parsing
        json_mod.decode = function(s)
            if s:match('"tag_name"') then
                local result = { assets = {} }
                result.tag_name = s:match('"tag_name":"([^"]+)"')
                result.body = s:match('"body":"([^"]*)"')
                for name, url in s:gmatch('"name":"([^"]+)"[^}]*"browser_download_url":"([^"]+)"') do
                    table.insert(result.assets, { name = name, browser_download_url = url })
                end
                return result
            end
            return {}
        end

        -- Mock io.popen for curl and uname
        local original_io_popen = io.popen
        _G.io.popen = function(cmd)
            if cmd == "uname -m" then
                return {
                    read = function()
                        return "armv7l"
                    end,
                    close = function() end,
                }
            end
            if cmd:match("curl") then
                return {
                    read = function()
                        return http_responses.code
                    end,
                    close = function() end,
                }
            end
            if cmd:match("^'ls'") then
                local files = http_responses.ls_files or {}
                local i = 0
                return {
                    lines = function()
                        return function()
                            i = i + 1
                            return files[i]
                        end
                    end,
                    close = function() end,
                }
            end
            return original_io_popen(cmd)
        end

        -- Mock io.open for file_contents
        local original_io_open = io.open
        _G.io.open = function(path, mode)
            if mode == "r" and file_contents[path] then
                local content = file_contents[path]
                return {
                    read = function(self, fmt)
                        return content
                    end,
                    close = function() end,
                }
            end
            if mode == "w" then
                return {
                    write = function(self, data)
                        file_contents[path] = data
                    end,
                    close = function() end,
                }
            end
            return original_io_open(path, mode)
        end

        helper.mock_os_execute()
        helper.mock_os_remove()
    end)

    -- Helper to setup successful download scenario
    local function setup_successful_download()
        http_responses.code = "200"
        package.loaded["util"].pathExists = function(path)
            if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                return true
            end
            if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                return true
            end
            -- Cache-based update paths
            if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                return true
            end
            if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin" then
                return true
            end
            if path:match("/tmp/koreader%-test%-data/cache/localsend/extract/localsend.koplugin/") then
                return true
            end
            -- Also return true for destination files after copy
            if path:match("/tmp/koreader%-test%-data/plugins/localsend.koplugin/.*%.lua$") then
                return true
            end
            if path:match("/tmp/koreader%-test%-data/plugins/localsend.koplugin/localsend$") then
                return true
            end
            return false
        end
    end

    -- =======================================================================
    -- checkForUpdates
    -- =======================================================================
    describe("checkForUpdates", function()
        local up_to_date_cases = {
            { "v1.1.1", "same version" },
            { "v1.0.0", "older version" },
        }

        for _, tc in ipairs(up_to_date_cases) do
            it("shows 'up to date' when remote is " .. tc[2], function()
                file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = string.format('{"tag_name":"%s","body":"Release notes"}', tc[1])

                local instance = helper.create_instance()
                instance:checkForUpdates()

                assert.is_truthy(helper.find_notification("up to date"))
            end)
        end

        local http_error_cases = {
            { "404", "not found" },
            { "500", "server error" },
            { "429", "rate limit" },
        }

        for _, tc in ipairs(http_error_cases) do
            it("shows error on HTTP " .. tc[1] .. " (" .. tc[2] .. ")", function()
                http_responses.code = tc[1]

                local instance = helper.create_instance()
                instance:checkForUpdates()

                local err = helper.find_notification("Failed to check") or helper.find_notification("HTTP status")
                assert.is_truthy(err, "Should show error for HTTP " .. tc[1])
            end)
        end

        it("shows update available with matching asset", function()
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = [[
                {"tag_name":"v2.0.0","body":"New features","assets":[
                    {"name":"localsend-koplugin-armv7.zip","browser_download_url":"https://example.com/armv7.zip"}
                ]}
            ]]

            local viewer_shown = false
            package.loaded["ui/widget/textviewer"] = {
                new = function(self, o)
                    viewer_shown = true
                    assert.equals("Update available!", o.title)
                    assert.truthy(o.text:match("v2%.0%.0") or o.text:match("Update") or o.text:match("%%1"))
                    assert.truthy(o.text:match("New features"))
                    return o
                end,
            }

            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.is_true(viewer_shown, "Should show release notes viewer")
        end)

        it("shows full release notes without truncating", function()
            local long_notes = string.rep("0123456789", 40) .. "END"
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = string.format(
                [[
                {"tag_name":"v2.0.0","body":"%s","assets":[
                    {"name":"localsend-koplugin-armv7.zip","browser_download_url":"https://example.com/armv7.zip"}
                ]}
            ]],
                long_notes
            )

            local shown_text
            package.loaded["ui/widget/textviewer"] = {
                new = function(self, o)
                    shown_text = o.text
                    return o
                end,
            }

            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.truthy(shown_text:match("END"), "Release notes should not be truncated")
            assert.is_nil(shown_text:match("%.%.%.$"), "Release notes should not have truncation ellipsis")
        end)

        it("shows info when no matching asset for architecture", function()
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = [[
                {"tag_name":"v2.0.0","body":"New features","assets":[
                    {"name":"localsend-koplugin-arm64.zip","browser_download_url":"https://example.com/arm64.zip"}
                ]}
            ]]

            local instance = helper.create_instance()
            instance:checkForUpdates()

            local info = helper.find_notification("no package")
                or helper.find_notification("Update available")
                or helper.find_notification("Auto%-update not available")
                or helper.find_notification("architecture")
            assert.is_truthy(info, "Should indicate update info with architecture note")
        end)

        it("handles status file read failure", function()
            local original_io_open = _G.io.open
            _G.io.open = function(path, mode)
                if path == "/tmp/koreader-test-data/cache/localsend/update_check.json" and mode == "r" then
                    return nil
                end
                return original_io_open(path, mode)
            end

            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.is_truthy(helper.find_notification("Failed to read update information"))
        end)

        it("handles malformed JSON response", function()
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = "not valid json {{{{"
            package.loaded["json"].decode = function(s)
                error("JSON parse error")
            end

            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.is_truthy(helper.find_notification("Failed to parse"))
        end)

        it("handles JSON without tag_name field", function()
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = '{"message":"Not Found"}'
            package.loaded["json"].decode = function(s)
                return { message = "Not Found" }
            end

            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.is_truthy(helper.find_notification("Failed to parse"))
        end)

        it("cleans up temp file on HTTP failure", function()
            http_responses.code = "500"

            local instance = helper.create_instance()
            instance:checkForUpdates()

            local cleaned = false
            for _, path in ipairs(helper.state.removed_files) do
                if path == "/tmp/koreader-test-data/cache/localsend/update_check.json" then
                    cleaned = true
                    break
                end
            end
            assert.is_true(cleaned, "Should clean up temp file")
        end)

        it("uses NetworkMgr:runWhenOnline", function()
            local run_when_online_called = false
            NetworkMgr.runWhenOnline = function()
                run_when_online_called = true
            end

            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.is_true(run_when_online_called)
        end)

        it("does NOT show manual 'No network' error when offline", function()
            NetworkMgr.isOnline = function()
                return false
            end
            NetworkMgr.runWhenOnline = function() end

            helper.reset_state()
            local instance = helper.create_instance()
            instance:checkForUpdates()

            assert.is_nil(helper.find_notification("No network connection"))
        end)
    end)

    -- =======================================================================
    -- performUpdate
    -- =======================================================================
    describe("performUpdate", function()
        it("stops server if running before update", function()
            local stop_called = false
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function()
                stop_called = true
                return true
            end
            instance.doPerformUpdate = function() end

            instance:performUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_true(stop_called)
        end)
    end)

    -- =======================================================================
    -- doPerformUpdate
    -- =======================================================================
    describe("doPerformUpdate", function()
        it("cleans up on download failure", function()
            http_responses.code = "500"

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_truthy(helper.find_notification("Download failed"))

            local cleaned = false
            for _, path in ipairs(helper.state.removed_files) do
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    cleaned = true
                    break
                end
            end
            assert.is_true(cleaned, "Should clean up temp zip")
        end)

        it("extracts and copies files on success", function()
            setup_successful_download()

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")
            assert.is_truthy(helper.find_execute_call("unzip"), "Should run unzip")
            assert.is_truthy(helper.find_execute_call("^'cp'"), "Should copy files")
            assert.is_truthy(helper.find_execute_call("'chmod' '%+x'"), "Should make binary executable")
        end)

        it("shows success message after update", function()
            setup_successful_download()

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_truthy(helper.find_notification("successfully"))
        end)

        it("handles extraction failure gracefully", function()
            http_responses.code = "200"
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    return true
                end
                -- extracted_plugin directory doesn't exist (simulates extraction failure)
                return false
            end

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            -- Now detected via pathExists check, shows "Invalid update package"
            assert.is_truthy(helper.find_notification("Invalid update package"))
        end)

        it("handles invalid package structure", function()
            http_responses.code = "200"
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin" then
                    return false
                end
                return false
            end

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_truthy(helper.find_notification("Invalid update package"))
        end)

        it("handles core file copy failure gracefully", function()
            http_responses.code = "200"
            -- Source files exist, but destination main.lua fails to appear after copy
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin" then
                    return true
                end
                if path:match("/tmp/koreader%-test%-data/cache/localsend/extract/localsend.koplugin/") then
                    return true
                end
                -- Destination files: main.lua copy "fails", others succeed
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/main.lua" then
                    return false
                end
                if path:match("/tmp/koreader%-test%-data/plugins/localsend.koplugin/.*%.lua$") then
                    return true
                end
                return false
            end

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            local found_error = false
            for _, msg in ipairs(logger.calls.err) do
                if msg:match("Failed to copy") and msg:match("main%.lua") then
                    found_error = true
                    break
                end
            end
            assert.is_true(found_error, "Should log error for failed file copy")
            assert.is_truthy(helper.find_notification("partially failed"))
        end)

        it("continues copying after individual file failure", function()
            setup_successful_download()

            local copy_commands = {}
            helper.mock_os_execute(function(cmd)
                if cmd:match("^'cp'") then
                    table.insert(copy_commands, cmd)
                    if #copy_commands == 1 then
                        return 1
                    end
                end
                return 0
            end)

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_true(#copy_commands >= 3, "Should attempt all core files")
        end)

        it("handles additional Lua file copying", function()
            setup_successful_download()
            http_responses.ls_files = {
                "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin/localsend_utils.lua",
            }

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_truthy(helper.find_execute_call("localsend_utils%.lua"))
        end)

        it("handles additional Lua file copy failure", function()
            http_responses.code = "200"
            http_responses.ls_files = {
                "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin/localsend_utils.lua",
            }
            -- Source files exist, but destination localsend_utils.lua fails to appear
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin" then
                    return true
                end
                if path:match("/tmp/koreader%-test%-data/cache/localsend/extract/localsend.koplugin/") then
                    return true
                end
                -- Core files copy succeeds
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/main.lua" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/_meta.lua" then
                    return true
                end
                -- Additional lua file copy "fails"
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend_utils.lua" then
                    return false
                end
                return false
            end

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            local found_warn = false
            for _, msg in ipairs(logger.calls.warn) do
                if msg:match("Failed to copy additional") then
                    found_warn = true
                    break
                end
            end
            assert.is_true(found_warn)
            assert.is_truthy(helper.find_notification("successfully"), "Should still show success")
        end)

        it("handles chmod failure gracefully", function()
            setup_successful_download()
            -- chmod return value is not checked, so this test just verifies
            -- that the update still succeeds regardless of chmod result

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            assert.is_truthy(helper.find_notification("successfully"))
        end)

        it("skips orphan cleanup when file tracking fails", function()
            -- This test simulates the case where the ls command to track new_lua_files
            -- returns empty (e.g., io.popen fails). Orphan cleanup should be skipped
            -- to avoid incorrectly removing valid files.
            http_responses.code = "200"
            http_responses.ls_files = {} -- Empty - simulates tracking failure

            local removed_files = {}
            local original_remove = os.remove
            os.remove = function(path)
                table.insert(removed_files, path)
                return original_remove(path)
            end

            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin" then
                    return true
                end
                if path:match("/tmp/koreader%-test%-data/cache/localsend/extract/localsend.koplugin/") then
                    return true
                end
                if path:match("/tmp/koreader%-test%-data/plugins/localsend.koplugin/.*%.lua$") then
                    return true
                end
                return false
            end

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            -- Should not remove any orphaned files when tracking is empty
            local orphan_removal_attempted = false
            for _, path in ipairs(removed_files) do
                -- Check if any .lua files in plugin_path were removed during orphan cleanup
                -- (excluding temp files and core files deleted before copy)
                if path:match("/tmp/koreader%-test%-data/plugins/localsend.koplugin/localsend_.*%.lua$") then
                    orphan_removal_attempted = true
                    break
                end
            end
            assert.is_false(orphan_removal_attempted, "Should not attempt orphan cleanup with empty tracking")

            os.remove = original_remove
        end)

        it("handles core file missing from update package", function()
            http_responses.code = "200"
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/plugins/localsend.koplugin/localsend" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/localsend_update.zip" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin/main.lua" then
                    return true
                end
                if path == "/tmp/koreader-test-data/cache/localsend/extract/localsend.koplugin/localsend" then
                    return true
                end
                -- _meta.lua is missing from update package
                return false
            end

            local instance = helper.create_instance()
            instance:doPerformUpdate("https://example.com/update.zip", "update.zip", "v2.0.0")

            -- Core file missing should now be an error, not a warning
            local found_err = false
            for _, msg in ipairs(logger.calls.err) do
                if msg:match("Core file missing from update package") and msg:match("_meta%.lua") then
                    found_err = true
                    break
                end
            end
            assert.is_true(found_err)
            -- Should show partial failure since core file is missing
            assert.is_truthy(helper.find_notification("partially failed"))
        end)
    end)

    -- =======================================================================
    -- Version comparison integration
    -- =======================================================================
    describe("version comparison integration", function()
        it("correctly identifies older version needing update", function()
            local orig_dofile = _G.dofile
            _G.dofile = function(path)
                if path:match("_meta%.lua$") then
                    return { version = "v1.0.0" }
                end
                return orig_dofile(path)
            end

            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = [[
                {"tag_name":"v1.1.0","body":"Bug fixes","assets":[
                    {"name":"localsend-koplugin-armv7.zip","browser_download_url":"https://example.com/armv7.zip"}
                ]}
            ]]

            local viewer_shown = false
            package.loaded["ui/widget/textviewer"] = {
                new = function(self, o)
                    viewer_shown = true
                    return o
                end,
            }

            local instance = helper.create_instance()
            instance:checkForUpdates()

            _G.dofile = orig_dofile
            assert.is_true(viewer_shown, "Should offer update from 1.0.0 to 1.1.0")
        end)
    end)

    -- =======================================================================
    -- Auto-update settings and scheduling (merged from auto_update_check_spec.lua)
    -- =======================================================================
    describe("auto-update settings", function()
        it("should load auto_update_check setting (default true via nilOrTrue)", function()
            G_reader_settings:delSetting("LocalSend_auto_update_check")
            local instance = helper.create_instance()
            assert.is_true(instance.auto_update_check, "auto_update_check should default to true")
        end)

        it("should respect auto_update_check when explicitly disabled", function()
            helper.state.settings["LocalSend_auto_update_check"] = false
            local instance = helper.create_instance()
            assert.is_false(instance.auto_update_check, "auto_update_check should be false when explicitly disabled")
        end)

        it("should load update_check_interval_hours setting (default 168 weekly)", function()
            local instance = helper.create_instance()
            assert.equal(168, instance.update_check_interval_hours, "update_check_interval_hours should default to 168 (weekly)")
        end)

        it("should create check_update_task function in init()", function()
            local instance = helper.create_instance()
            assert.is_function(instance.check_update_task, "check_update_task should be created in init()")
        end)

        it("stores update_available_tag after auto-check finds a newer release", function()
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = [[
                {"tag_name":"v2.0.0","body":"New features"}
            ]]

            local instance = helper.create_instance()
            instance:_autoCheckForUpdates()

            assert.equals("v2.0.0", instance.update_available_tag)
            assert.equals("v2.0.0", helper.state.settings["LocalSend_update_available_tag"])
        end)

        it("clears update_available_tag when already up to date", function()
            helper.state.settings["LocalSend_update_available_tag"] = "v2.0.0"
            file_contents["/tmp/koreader-test-data/cache/localsend/update_check.json"] = [[
                {"tag_name":"v1.1.1","body":"Current release"}
            ]]

            local instance = helper.create_instance()
            instance:_autoCheckForUpdates()

            assert.equals("", instance.update_available_tag)
            assert.equals("", helper.state.settings["LocalSend_update_available_tag"])
        end)
    end)

    describe("auto-update scheduling", function()
        it("should have _scheduleUpdateCheck method", function()
            local instance = helper.create_instance()
            assert.is_function(instance._scheduleUpdateCheck, "_scheduleUpdateCheck helper should exist")
        end)

        it("should have _unscheduleUpdateCheck method", function()
            local instance = helper.create_instance()
            assert.is_function(instance._unscheduleUpdateCheck, "_unscheduleUpdateCheck helper should exist")
        end)
    end)

    describe("auto-update onCloseWidget cleanup", function()
        it("should unschedule check_update_task on widget close", function()
            local instance = helper.create_instance()
            local task_ref = instance.check_update_task

            helper.state.unscheduled_tasks = {}
            instance:onCloseWidget()

            local found_unschedule = false
            for _, task in ipairs(helper.state.unscheduled_tasks) do
                if task == task_ref then
                    found_unschedule = true
                    break
                end
            end

            assert.is_true(found_unschedule, "Should unschedule check_update_task on widget close")
        end)

        it("should nil out check_update_task reference", function()
            local instance = helper.create_instance()
            assert.is_function(instance.check_update_task)

            instance:onCloseWidget()

            assert.is_nil(instance.check_update_task, "check_update_task should be nil after onCloseWidget")
        end)
    end)

    -- =======================================================================
    -- clearTmpTelemetryFiles
    -- =======================================================================
    describe("clearTmpTelemetryFiles", function()
        it("removes fm-out-* files from /tmp", function()
            -- Mock io.popen to return fake telemetry files
            local original_io_popen = io.popen
            _G.io.popen = function(cmd)
                if cmd:match("^ls %-1 /tmp/") then
                    local files = { "fm-out-123", "fm-out-456", "other-file.txt" }
                    local i = 0
                    return {
                        lines = function()
                            return function()
                                i = i + 1
                                return files[i]
                            end
                        end,
                        close = function() end,
                    }
                end
                return original_io_popen(cmd)
            end

            -- Track os.remove calls
            local removed = {}
            local original_os_remove = os.remove
            _G.os.remove = function(path)
                table.insert(removed, path)
                return true
            end

            local lsupdate = require("localsend_update")
            lsupdate.clearTmpTelemetryFiles()

            -- Should have removed exactly the fm-out-* files
            assert.equals(2, #removed)
            assert.truthy(removed[1]:match("fm%-out%-123"))
            assert.truthy(removed[2]:match("fm%-out%-456"))

            _G.io.popen = original_io_popen
            _G.os.remove = original_os_remove
        end)

        it("handles io.popen failure gracefully", function()
            local original_io_popen = io.popen
            _G.io.popen = function()
                return nil
            end

            local lsupdate = require("localsend_update")
            -- Should not error
            assert.has_no.errors(function()
                lsupdate.clearTmpTelemetryFiles()
            end)

            _G.io.popen = original_io_popen
        end)

        it("exposes TMP_TELEMETRY_PATTERN constant", function()
            local lsupdate = require("localsend_update")
            assert.equals("fm-out-*", lsupdate.TMP_TELEMETRY_PATTERN)
        end)
    end)
end)
