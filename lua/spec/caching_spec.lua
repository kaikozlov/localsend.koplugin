require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for caching isRunning() and getTransferCount()
-- These tests verify cached values are used to avoid disk I/O on every menu render

describe("LocalSend State Caching", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("cache initialization", function()
        it("_cached_running should default to false", function()
            local instance = helper.create_instance()

            assert.is_false(instance._cached_running, "_cached_running should default to false")
        end)

        it("_cached_transfer_count should default to 0", function()
            local instance = helper.create_instance()

            assert.equal(0, instance._cached_transfer_count, "_cached_transfer_count should default to 0")
        end)
    end)

    describe("_updateCache method", function()
        it("_updateCache should update _cached_running from isRunning()", function()
            local instance = helper.create_instance()

            -- Mock isRunning to return true
            instance.isRunning = function()
                return true
            end
            instance.getTransferCount = function()
                return 0
            end

            instance:_updateCache()

            assert.is_true(instance._cached_running, "_updateCache should sync _cached_running with isRunning()")
        end)

        it("_updateCache should update _cached_transfer_count from getTransferCount()", function()
            local instance = helper.create_instance()

            -- Mock getTransferCount to return 5
            instance.isRunning = function()
                return false
            end
            instance.getTransferCount = function()
                return 5
            end

            instance:_updateCache()

            assert.equal(5, instance._cached_transfer_count, "_updateCache should sync _cached_transfer_count with getTransferCount()")
        end)
    end)

    describe("cache invalidation on state changes", function()
        it("start() should reconcile cache when server is already running", function()
            local instance = helper.create_instance()
            local update_cache_calls = 0
            local original_updateCache = instance._updateCache
            instance._updateCache = function(self)
                update_cache_calls = update_cache_calls + 1
                original_updateCache(self)
            end

            instance.isRunning = function()
                return true
            end

            instance:start()

            assert.equal(1, update_cache_calls, "start should reconcile the cached state")
            assert.is_true(instance._cached_running, "Cache should reflect running state after start()")
        end)

        it("stopServer() should update cache after stopping", function()
            local instance = helper.create_instance()

            -- Track _updateCache calls
            local update_cache_calls = 0
            local original_updateCache = instance._updateCache
            instance._updateCache = function(self)
                update_cache_calls = update_cache_calls + 1
                original_updateCache(self)
            end

            -- Mock closeFirewall to avoid issues
            instance.closeFirewall = function() end

            -- Mock: PID file exists but process is not alive
            -- This tests the path that actually stops and updates cache
            local original_pathExists = package.loaded["util"].pathExists
            package.loaded["util"].pathExists = function(path)
                if path:match("localsend_koreader%.pid$") then
                    return true -- PID file exists
                end
                if path:match("^/proc/%d+$") then
                    return false -- Process not alive
                end
                return original_pathExists(path)
            end

            -- Mock readFromFile to return a fake PID
            local original_readFromFile = package.loaded["util"].readFromFile
            package.loaded["util"].readFromFile = function(path)
                if path:match("localsend_koreader%.pid$") then
                    return "12345" -- Fake PID
                end
                return original_readFromFile and original_readFromFile(path) or nil
            end

            -- Mock os.remove to avoid actual file operations
            local original_remove = os.remove
            os.remove = function()
                return true
            end

            update_cache_calls = 0
            instance:stopServer()

            -- Restore
            package.loaded["util"].pathExists = original_pathExists
            package.loaded["util"].readFromFile = original_readFromFile
            os.remove = original_remove

            -- stopServer should call _updateCache when process stopped
            assert.is_true(update_cache_calls > 0, "stopServer() should call _updateCache after stopping")
        end)

        it("_checkForNewTransfers should update cache when transfers found", function()
            local instance = helper.create_instance()

            local update_cache_called = false
            instance._updateCache = function(self)
                update_cache_called = true
            end

            instance.isRunning = function()
                return true
            end
            instance.getNewTransfers = function()
                return { "file1.pdf", "file2.epub" }
            end

            instance:_checkForNewTransfers()

            assert.is_true(update_cache_called, "_checkForNewTransfers should call _updateCache when new files detected")
        end)
    end)

    describe("menu uses cached values", function()
        it("text_func should use _cached_running instead of isRunning()", function()
            local instance = helper.create_instance()

            -- Replace isRunning with a tracking version
            local original_isRunning = instance.isRunning
            local isRunning_calls_in_text_func = 0
            instance.isRunning = function(self)
                isRunning_calls_in_text_func = isRunning_calls_in_text_func + 1
                return original_isRunning(self)
            end

            -- Set up cached state
            instance._cached_running = true
            instance._cached_transfer_count = 3

            -- Get the menu table from addToMainMenu
            local menu_items = {}
            instance:addToMainMenu(menu_items)

            -- Reset counter before calling text_func
            isRunning_calls_in_text_func = 0

            -- Call the main menu item's text_func
            local text = menu_items.localsend.text_func()

            assert.equal(0, isRunning_calls_in_text_func, "text_func should use _cached_running, not call isRunning()")
            assert.equal("LocalSend (3 received)", text, "text_func should show cached transfer count")
        end)

        it("text_func should use _cached_transfer_count instead of getTransferCount()", function()
            local instance = helper.create_instance()

            -- Replace getTransferCount with a tracking version
            local original_getTransferCount = instance.getTransferCount
            local getTransferCount_calls_in_text_func = 0
            instance.getTransferCount = function(self)
                getTransferCount_calls_in_text_func = getTransferCount_calls_in_text_func + 1
                return original_getTransferCount(self)
            end

            -- Set up cached state
            instance._cached_running = true
            instance._cached_transfer_count = 7

            -- Get the menu table
            local menu_items = {}
            instance:addToMainMenu(menu_items)

            -- Reset counter
            getTransferCount_calls_in_text_func = 0

            -- Call text_func
            local text = menu_items.localsend.text_func()

            assert.equal(0, getTransferCount_calls_in_text_func, "text_func should use _cached_transfer_count, not call getTransferCount()")
        end)

        it("sub-menu Start/Stop text_func should use _cached_running", function()
            local instance = helper.create_instance()

            -- Track isRunning calls
            local isRunning_calls = 0
            instance.isRunning = function(self)
                isRunning_calls = isRunning_calls + 1
                return false
            end

            instance._cached_running = true

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            isRunning_calls = 0

            -- First sub-item is Start/Stop toggle
            local text = menu_items.localsend.sub_item_table[1].text_func()

            assert.equal(0, isRunning_calls, "Start/Stop text_func should use _cached_running")
            assert.equal("Stop server", text, "Should show 'Stop server' when _cached_running is true")
        end)

        it("Recent transfers text_func should use _cached_transfer_count", function()
            local instance = helper.create_instance()

            -- Track getTransferCount calls
            local getTransferCount_calls = 0
            instance.getTransferCount = function(self)
                getTransferCount_calls = getTransferCount_calls + 1
                return 0
            end

            instance._cached_transfer_count = 5

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            getTransferCount_calls = 0

            -- Second sub-item is Recent transfers
            local text = menu_items.localsend.sub_item_table[2].text_func()

            assert.equal(0, getTransferCount_calls, "Recent transfers text_func should use _cached_transfer_count")
            assert.equal("Recent transfers (5)", text, "Should show cached count")
        end)

        it("Recent transfers enabled_func should use _cached_transfer_count", function()
            local instance = helper.create_instance()

            -- Track getTransferCount calls
            local getTransferCount_calls = 0
            instance.getTransferCount = function(self)
                getTransferCount_calls = getTransferCount_calls + 1
                return 0
            end

            instance._cached_transfer_count = 3

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            getTransferCount_calls = 0

            -- Second sub-item is Recent transfers
            local enabled = menu_items.localsend.sub_item_table[2].enabled_func()

            assert.equal(0, getTransferCount_calls, "Recent transfers enabled_func should use _cached_transfer_count")
            assert.is_true(enabled, "Should be enabled when _cached_transfer_count > 0")
        end)
    end)

    describe("cache sync on init for existing server", function()
        it("init should sync cache if server is already running", function()
            local LocalSend = require("main")

            -- Temporarily make isRunning return true
            local orig_isRunning = LocalSend.isRunning
            LocalSend.isRunning = function()
                return true
            end
            LocalSend.getTransferCount = function()
                return 2
            end

            local instance = helper.create_instance()

            -- Restore
            LocalSend.isRunning = orig_isRunning

            -- Cache should reflect the running state
            assert.is_true(instance._cached_running, "Cache should sync to running state during init")
            assert.equal(2, instance._cached_transfer_count, "Cache should sync transfer count during init")
        end)
    end)

    -- =========================================================================
    -- Transfer count caching in ServerState (optimization for e-readers)
    -- =========================================================================
    describe("ServerState transfer_count optimization", function()
        it("clearTransferLog should reset ServerState.transfer_count to 0", function()
            local LocalSend = require("main")
            LocalSend._ServerState.transfer_count = 5

            local instance = helper.create_instance()

            instance:clearTransferLog()

            assert.equal(0, LocalSend._ServerState.transfer_count, "clearTransferLog should reset transfer_count to 0")
        end)

        it("getTransferCount should return ServerState.transfer_count", function()
            local LocalSend = require("main")
            LocalSend._ServerState.transfer_count = 42

            local instance = helper.create_instance()

            local count = instance:getTransferCount()

            assert.equal(42, count, "getTransferCount should return cached ServerState.transfer_count")
        end)

        it("getTransferCount should NOT read file when using ServerState cache", function()
            local LocalSend = require("main")
            LocalSend._ServerState.transfer_count = 10

            local instance = helper.create_instance()

            -- Mock io.open to track if it's called for transfer log
            local io_open_called_for_log = false
            local original_io_open = io.open
            io.open = function(path, mode)
                if path and path:match("localsend_transfers%.log") then
                    io_open_called_for_log = true
                end
                return original_io_open(path, mode)
            end

            local count = instance:getTransferCount()

            io.open = original_io_open

            assert.is_false(io_open_called_for_log, "getTransferCount should NOT read file when using ServerState cache")
        end)
    end)
end)
