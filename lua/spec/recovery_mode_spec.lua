require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")
local ffiUtil = require("ffi/util")

-- Tests for recovery mode functionality

describe("Recovery Mode", function()
    local LocalSend
    local original_require

    setup(function()
        helper.setup_complete()
        original_require = _G.require
    end)

    teardown(function()
        _G.require = original_require
    end)

    before_each(function()
        helper.before_each()
    end)

    -- =======================================================================
    -- tryRequire function
    -- =======================================================================
    describe("tryRequire", function()
        it("should return module on successful require", function()
            -- Normal require should work
            LocalSend = require("main")
            local instance = helper.create_instance()

            -- If we got here, tryRequire worked for all modules
            assert.is_not_nil(instance)
        end)

        it("should return nil when module fails to load", function()
            -- Simulate a module that fails to load by breaking localsend_state
            local original_state = package.loaded["localsend_state"]
            package.loaded["localsend_state"] = nil

            -- Create a failing require for localsend_state
            local fail_require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end

            _G.require = fail_require
            package.loaded["main"] = nil

            local ok, result = pcall(function()
                return require("main")
            end)

            -- Should still load (in recovery mode)
            assert.is_true(ok, "Plugin should load even when localsend_state fails")

            -- Restore
            _G.require = original_require
            package.loaded["localsend_state"] = original_state
        end)
    end)

    -- =======================================================================
    -- Recovery mode triggers
    -- =======================================================================
    describe("recovery mode triggers", function()
        local original_state, original_server

        before_each(function()
            original_state = package.loaded["localsend_state"]
            original_server = package.loaded["localsend_server"]
        end)

        after_each(function()
            package.loaded["localsend_state"] = original_state
            package.loaded["localsend_server"] = original_server
            _G.require = original_require
            package.loaded["main"] = nil
        end)

        it("should enter recovery mode when localsend_state fails", function()
            package.loaded["localsend_state"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")

            -- Create instance - should use recovery mode init
            local menu_items = {}
            LocalSend.addToMainMenu(LocalSend, menu_items)

            -- Recovery mode menu should show "Recovery Mode" in text
            assert.is_truthy(menu_items.localsend)
            assert.is_truthy(menu_items.localsend.text:match("Recovery"))
        end)

        it("can be disabled safely even when a critical module failed before initialization", function()
            package.loaded["localsend_state"] = nil
            package.loaded["main"] = nil
            _G.require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end

            LocalSend = require("main")
            local instance = LocalSend:new({ ui = { menu = { registerToMainMenu = function() end } } })
            assert.is_true(instance.recovery_mode)
            assert.has_no.errors(function()
                instance:stopPlugin()
            end)
        end)

        it("should enter recovery mode when localsend_server fails", function()
            package.loaded["localsend_server"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_server" then
                    error("module 'localsend_server' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")

            local menu_items = {}
            LocalSend.addToMainMenu(LocalSend, menu_items)

            assert.is_truthy(menu_items.localsend.text:match("Recovery"))
        end)

        it("should NOT enter recovery mode when non-critical module fails", function()
            -- localsend_dialogs is not critical
            package.loaded["localsend_dialogs"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_dialogs" then
                    error("module 'localsend_dialogs' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            -- Should NOT be in recovery mode - normal mode uses text_func, not text
            local menu_text = menu_items.localsend.text or (menu_items.localsend.text_func and menu_items.localsend.text_func())
            assert.is_truthy(menu_text)
            assert.is_falsy(menu_text:match("Recovery"))
        end)
    end)

    -- =======================================================================
    -- Recovery mode menu
    -- =======================================================================
    describe("recovery mode menu", function()
        local original_state

        before_each(function()
            original_state = package.loaded["localsend_state"]
        end)

        after_each(function()
            package.loaded["localsend_state"] = original_state
            _G.require = original_require
            package.loaded["main"] = nil
        end)

        it("should show reinstall option in recovery menu", function()
            package.loaded["localsend_state"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")

            local menu_items = {}
            LocalSend.addToMainMenu(LocalSend, menu_items)

            -- Should have sub_item_table with reinstall option
            local sub_items = menu_items.localsend.sub_item_table
            assert.is_truthy(sub_items)

            local has_reinstall = false
            for _, item in ipairs(sub_items) do
                local text = item.text or (item.text_func and item.text_func())
                if text and text:match("Reinstall") then
                    has_reinstall = true
                    break
                end
            end

            assert.is_true(has_reinstall, "Recovery menu should have reinstall option")
        end)

        it("should show error message in recovery menu", function()
            package.loaded["localsend_state"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")

            local menu_items = {}
            LocalSend.addToMainMenu(LocalSend, menu_items)

            local sub_items = menu_items.localsend.sub_item_table
            local has_error_msg = false
            for _, item in ipairs(sub_items) do
                local text = item.text or (item.text_func and item.text_func())
                if text and (text:match("Error") or text:match("Reinstall Required")) then
                    has_error_msg = true
                    break
                end
            end

            assert.is_true(has_error_msg, "Recovery menu should show error message")
        end)
    end)

    -- =======================================================================
    -- _initRecoveryMode
    -- =======================================================================
    describe("_initRecoveryMode", function()
        local original_state

        before_each(function()
            original_state = package.loaded["localsend_state"]
        end)

        after_each(function()
            package.loaded["localsend_state"] = original_state
            _G.require = original_require
            package.loaded["main"] = nil
        end)

        it("should register menu even in recovery mode", function()
            package.loaded["localsend_state"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")

            local menu_registered = false
            local mock_menu = {
                registerToMainMenu = function()
                    menu_registered = true
                end,
            }

            local instance = LocalSend:new({
                ui = { menu = mock_menu },
            })

            assert.is_true(menu_registered, "Menu should be registered in recovery mode")
        end)

        it("should initialize update module in recovery mode", function()
            package.loaded["localsend_state"] = nil
            package.loaded["main"] = nil

            local fail_require = function(name)
                if name == "localsend_state" then
                    error("module 'localsend_state' not found")
                end
                return original_require(name)
            end
            _G.require = fail_require

            LocalSend = require("main")

            -- The fact that we can create an instance means update module was initialized
            local instance = helper.create_instance()

            -- checkForUpdates should be available
            assert.is_function(instance.checkForUpdates)
        end)
    end)
end)

-- =======================================================================
-- Protected files in update cleanup
-- =======================================================================
-- These tests drive the REAL localsend_update.cleanupOrphanedLuaFiles().
-- We can't rely on real files because the Docker container's io.open (Lua) and
-- io.popen("ls ...") (subprocess) see different filesystem views — files written
-- via io.open are often invisible to the `ls` subprocess the production code
-- shells out to. So instead we spy io.popen to feed a controlled old-file list,
-- and assert on what util.removeFile gets called with (captured, not run, by the
-- helper spy).
describe("Update orphan cleanup", function()
    local lsupdate
    local deps_mock
    local old_files_on_disk
    local real_io_popen

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
        old_files_on_disk = {}

        package.loaded["localsend_update"] = nil
        lsupdate = require("localsend_update")
        deps_mock = {
            UIManager = require("ui/uimanager"),
            InfoMessage = require("ui/widget/infomessage"),
            NetworkMgr = require("ui/network/manager"),
            util = require("util"),
            json = require("json"),
            logger = require("logger"),
            T = function(s, ...)
                return s
            end,
            _ = function(s)
                return s
            end,
            G_reader_settings = G_reader_settings,
        }
        lsupdate.init(deps_mock)

        -- Spy io.popen so cleanupOrphanedLuaFiles iterates a file list WE control
        -- (the production function does `io.popen("ls <dir>/*.lua")`). Only the
        -- `ls .../*.lua` call is faked; everything else passes through.
        real_io_popen = io.popen
        io.popen = function(cmd, ...)
            local expected = "ls " .. util.shell_escape({ "/fake/plugin" }) .. "/*.lua 2>/dev/null"
            if cmd == expected then
                local files = {}
                for _, f in ipairs(old_files_on_disk) do
                    table.insert(files, "/fake/plugin/" .. f)
                end
                local idx = 0
                local function iter()
                    idx = idx + 1
                    return files[idx]
                end
                -- Lua's generic for: `for v in handle:lines() do` calls lines()
                -- once to get an iterator, then calls that iterator each pass.
                return {
                    lines = function()
                        return iter
                    end,
                    close = function() end,
                }
            end
            return real_io_popen(cmd, ...)
        end
    end)

    after_each(function()
        io.popen = real_io_popen
    end)

    -- Helper: was a given filename removed (captured by the util.removeFile spy)?
    local function was_removed(filename)
        for _, path in ipairs(helper.state.removed_files) do
            if path:match("/" .. filename .. "$") then
                return true
            end
        end
        return false
    end

    describe("protected files are never deleted", function()
        it("keeps main.lua even when absent from the update package", function()
            -- plugin has main.lua + orphan.lua on disk; update ships neither.
            -- main.lua is protected, orphan.lua is a real orphan to remove.
            -- (update.lua ships at least one file so tracking is non-empty.)
            old_files_on_disk = { "main.lua", "orphan.lua", "shipped.lua" }
            local new_lua_files = { ["shipped.lua"] = true }

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.is_false(was_removed("main.lua"), "main.lua must never be deleted")
            assert.is_true(was_removed("orphan.lua"), "orphan.lua should be deleted")
        end)

        it("keeps localsend_update.lua (needed to finish the update)", function()
            old_files_on_disk = { "localsend_update.lua", "orphan.lua", "shipped.lua" }
            local new_lua_files = { ["shipped.lua"] = true }

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.is_false(was_removed("localsend_update.lua"))
            assert.is_true(was_removed("orphan.lua"))
        end)

        it("keeps localsend_utils.lua", function()
            old_files_on_disk = { "localsend_utils.lua", "orphan.lua", "shipped.lua" }
            local new_lua_files = { ["shipped.lua"] = true }

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.is_false(was_removed("localsend_utils.lua"))
            assert.is_true(was_removed("orphan.lua"))
        end)

        it("keeps _meta.lua", function()
            old_files_on_disk = { "_meta.lua", "orphan.lua", "shipped.lua" }
            local new_lua_files = { ["shipped.lua"] = true }

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.is_false(was_removed("_meta.lua"))
            assert.is_true(was_removed("orphan.lua"))
        end)
    end)

    describe("non-protected orphans are removed", function()
        it("deletes multiple orphan files", function()
            old_files_on_disk = { "main.lua", "old_module.lua", "deprecated.lua", "new_module.lua" }
            local new_lua_files = { ["main.lua"] = true, ["new_module.lua"] = true }

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.is_true(was_removed("old_module.lua"), "old_module.lua should be deleted")
            assert.is_true(was_removed("deprecated.lua"), "deprecated.lua should be deleted")
            assert.is_false(was_removed("main.lua"), "main.lua is protected")
            assert.is_false(was_removed("new_module.lua"), "new_module.lua is in the update")
        end)
    end)

    describe("cleanup safety", function()
        it("does not delete anything when copy_failed is true", function()
            old_files_on_disk = { "orphan.lua" }
            local new_lua_files = { ["other.lua"] = true }

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, true)

            assert.is_false(was_removed("orphan.lua"), "must not delete on failed copy")
        end)

        it("does not delete anything when new_lua_files is empty (tracking failed)", function()
            old_files_on_disk = { "orphan.lua" }
            local new_lua_files = {}

            lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.is_false(was_removed("orphan.lua"), "must not delete when no files tracked")
        end)

        it("returns the list of removed orphans", function()
            old_files_on_disk = { "orphan_a.lua", "orphan_b.lua", "main.lua" }
            local new_lua_files = { ["main.lua"] = true }

            local removed = lsupdate.cleanupOrphanedLuaFiles("/fake/plugin", new_lua_files, false)

            assert.are.same({ "orphan_a.lua", "orphan_b.lua" }, removed)
        end)
    end)
end)

-- =======================================================================
-- Reinstall marker file functionality
-- =======================================================================
describe("Reinstall marker file", function()
    local lsupdate
    local marker_file_exists
    local marker_file_content
    local written_files
    local restore_io_open, restore_util_removeFile

    setup(function()
        helper.setup_complete()
    end)

    after_each(function()
        if restore_io_open then
            _G.io.open = restore_io_open
        end
        if restore_util_removeFile then
            require("util").removeFile = restore_util_removeFile
        end
    end)

    before_each(function()
        helper.before_each()
        marker_file_exists = false
        marker_file_content = nil
        written_files = {}

        -- Mock io.open for marker file operations
        local original_io_open = _G.io.open
        restore_io_open = original_io_open
        _G.io.open = function(path, mode)
            if path:match("%.reinstall_required$") then
                if mode == "r" then
                    if marker_file_exists then
                        return {
                            read = function()
                                return marker_file_content
                            end,
                            close = function() end,
                        }
                    else
                        return nil
                    end
                elseif mode == "w" then
                    marker_file_exists = true
                    return {
                        write = function(self, data)
                            marker_file_content = data
                            written_files[path] = data
                        end,
                        close = function() end,
                    }
                end
            end
            return original_io_open(path, mode)
        end

        -- Mock os.remove for marker file
        local original_os_remove = _G.os.remove
        _G.os.remove = function(path)
            if path:match("%.reinstall_required$") then
                marker_file_exists = false
                marker_file_content = nil
                return true
            end
            return original_os_remove(path)
        end

        -- Override util.removeFile to handle marker file removal
        local util_mod = require("util")
        restore_util_removeFile = util_mod.removeFile
        util_mod.removeFile = function(path)
            if path:match("%.reinstall_required$") then
                marker_file_exists = false
                marker_file_content = nil
                return true
            end
            return true
        end

        -- Load the update module fresh
        package.loaded["localsend_update"] = nil
        lsupdate = require("localsend_update")

        -- Initialize with mocked dependencies
        lsupdate.init({
            UIManager = require("ui/uimanager"),
            InfoMessage = require("ui/widget/infomessage"),
            NetworkMgr = require("ui/network/manager"),
            util = require("util"),
            ffiutil = require("ffi/util"),
            json = require("json"),
            logger = require("logger"),
            T = function(s, ...)
                return s
            end,
            _ = function(s)
                return s
            end,
            G_reader_settings = G_reader_settings,
            cache_dir = (get_test_data_dir() .. "/cache"),
        })
    end)

    describe("isReinstallRequired", function()
        it("should return false when marker file does not exist", function()
            marker_file_exists = false

            local result = lsupdate.isReinstallRequired("/tmp/test_plugin")

            assert.is_false(result)
        end)

        it("should return true when marker file exists", function()
            marker_file_exists = true
            marker_file_content = "Update failed at 2024-01-01 12:00:00"

            local result = lsupdate.isReinstallRequired("/tmp/test_plugin")

            assert.is_true(result)
        end)
    end)

    describe("setReinstallRequired", function()
        it("should create marker file", function()
            marker_file_exists = false

            lsupdate.setReinstallRequired("/tmp/test_plugin")

            assert.is_true(marker_file_exists)
        end)

        it("should write timestamp to marker file", function()
            lsupdate.setReinstallRequired("/tmp/test_plugin")

            assert.is_truthy(marker_file_content)
            assert.is_truthy(marker_file_content:match("Update failed at"))
        end)
    end)

    describe("clearReinstallRequired", function()
        it("should remove marker file", function()
            marker_file_exists = true
            marker_file_content = "Update failed at 2024-01-01 12:00:00"

            lsupdate.clearReinstallRequired("/tmp/test_plugin")

            assert.is_false(marker_file_exists)
        end)
    end)

    describe("REINSTALL_MARKER_FILE constant", function()
        it("should be defined", function()
            assert.is_truthy(lsupdate.REINSTALL_MARKER_FILE)
        end)

        it("should be .reinstall_required", function()
            assert.equal(".reinstall_required", lsupdate.REINSTALL_MARKER_FILE)
        end)
    end)
end)

-- =======================================================================
-- Menu warning when reinstall required
-- =======================================================================
describe("Reinstall required menu warning", function()
    local LocalSend

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    -- Note: Testing the REINSTALL_REQUIRED flag requires mocking at module load time,
    -- which is complex. These tests verify the _buildMainMenu function behavior.

    describe("_buildMainMenu", function()
        it("should be a function", function()
            LocalSend = require("main")
            local instance = helper.create_instance()

            assert.is_function(instance._buildMainMenu)
        end)

        it("should return a table of menu items", function()
            LocalSend = require("main")
            local instance = helper.create_instance()

            local menu = instance:_buildMainMenu()

            assert.is_table(menu)
            assert.is_true(#menu >= 5, "Should have at least 5 menu items")
        end)

        it("should include Start/Stop server as first item (when no warning)", function()
            LocalSend = require("main")
            local instance = helper.create_instance()

            local menu = instance:_buildMainMenu()

            -- First item should be Start/Stop server (text_func based)
            assert.is_function(menu[1].text_func)
            local text = menu[1].text_func()
            assert.is_truthy(text:match("server") or text:match("Start") or text:match("Stop"))
        end)
    end)
end)
