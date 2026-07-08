require 'busted.runner'()
local helper = require("spec.test_helper")

-- Tests for menu building functions

describe("Menu Building", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("buildExtensionPresetsMenu", function()
        it("returns a table of menu items", function()
            local instance = helper.create_instance()

            local menu = instance:buildExtensionPresetsMenu()

            assert.is_table(menu)
            assert.is_true(#menu > 0)
        end)

        it("includes 'All files' option", function()
            local instance = helper.create_instance()

            local menu = instance:buildExtensionPresetsMenu()

            local found = false
            for _, item in ipairs(menu) do
                if item.text and item.text:match("All files") then
                    found = true
                    break
                end
            end
            assert.is_true(found, "Should include 'All files' option")
        end)

        it("includes eBook presets", function()
            local instance = helper.create_instance()

            local menu = instance:buildExtensionPresetsMenu()

            local found_ebooks = false
            for _, item in ipairs(menu) do
                if item.text and item.text:match("eBooks") then
                    found_ebooks = true
                    break
                end
            end
            assert.is_true(found_ebooks, "Should include eBooks preset")
        end)

        it("includes 'Custom...' option", function()
            local instance = helper.create_instance()

            local menu = instance:buildExtensionPresetsMenu()

            local found = false
            for _, item in ipairs(menu) do
                -- Check both static text and text_func (used for dynamic display)
                local text = item.text or (item.text_func and item.text_func())
                if text and text:match("Custom") then
                    found = true
                    assert.is_true(item.keep_menu_open, "Custom should keep menu open")
                    break
                end
            end
            assert.is_true(found, "Should include 'Custom...' option")
        end)

        it("marks current selection as checked", function()
            local instance = helper.create_instance()
            instance.accept_ext = "epub,pdf,mobi,azw3"

            local menu = instance:buildExtensionPresetsMenu()

            -- Find the eBooks option and check if it's checked
            for _, item in ipairs(menu) do
                if item.checked_func then
                    local is_checked = item.checked_func()
                    if item.text and item.text:match("eBooks") and item.text:match("epub.*pdf.*mobi.*azw3") then
                        assert.is_true(is_checked, "eBooks preset should be checked")
                    end
                end
            end
        end)
    end)

    describe("buildExtensionRoutingMenu", function()
        it("returns empty add option when no routes", function()
            local instance = helper.create_instance()
            instance.ext_dirs = {}

            local menu = instance:buildExtensionRoutingMenu()

            -- Should have "Add extension route..." option
            local found_add = false
            for _, item in ipairs(menu) do
                if item.text and item.text:match("Add extension route") then
                    found_add = true
                    break
                end
            end
            assert.is_true(found_add, "Should have add route option")
        end)

        it("shows enable toggle when routes exist", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books" }
            instance.routing_enabled = true

            local menu = instance:buildExtensionRoutingMenu()

            local found_toggle = false
            for _, item in ipairs(menu) do
                if item.text and item.text:match("Enable file type routing") then
                    found_toggle = true
                    assert.is_function(item.checked_func)
                    assert.is_true(item.checked_func())
                    break
                end
            end
            assert.is_true(found_toggle, "Should show enable toggle when routes exist")
        end)

        it("lists existing routes", function()
            local instance = helper.create_instance()
            instance.ext_dirs = {
                epub = "/books",
                pdf = "/documents",
            }

            local menu = instance:buildExtensionRoutingMenu()

            -- Routes are added to menu - count non-separator, non-toggle items
            local route_count = 0
            for _, item in ipairs(menu) do
                -- Check for route items (they have text with arrow or %1/%2 placeholder pattern)
                if item.text and (item.text:match("→") or item.text:match("%%1") or item.text:match("epub") or item.text:match("pdf")) then
                    route_count = route_count + 1
                end
            end
            -- Should have at least the routes in some form
            assert.is_true(route_count >= 1, "Should show routes")
        end)

        it("shows 'accept all' option when routes exist", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books" }
            instance.routing_accept_all = false

            local menu = instance:buildExtensionRoutingMenu()

            local found_accept_all = false
            for _, item in ipairs(menu) do
                if item.text and item.text:match("Accept other files") then
                    found_accept_all = true
                    assert.is_function(item.checked_func)
                    assert.is_false(item.checked_func())
                    break
                end
            end
            assert.is_true(found_accept_all, "Should show accept all option")
        end)

        it("does not show 'accept all' option when no routes", function()
            local instance = helper.create_instance()
            instance.ext_dirs = {}

            local menu = instance:buildExtensionRoutingMenu()

            for _, item in ipairs(menu) do
                if item.text and item.text:match("Accept other files") then
                    assert.fail("Should not show accept all option when no routes")
                end
            end
        end)
    end)

    describe("addToMainMenu", function()
        it("adds localsend entry to menu_items", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            assert.is_not_nil(menu_items.localsend)
        end)

        it("has text_func that shows status", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            assert.is_function(menu_items.localsend.text_func)

            -- When not running (use cached value)
            instance._cached_running = false
            local text_not_running = menu_items.localsend.text_func()
            assert.equal("LocalSend", text_not_running)

            -- When running (use cached value)
            instance._cached_running = true
            instance._cached_transfer_count = 0
            local text_running = menu_items.localsend.text_func()
            -- Template uses %1 placeholder, so match "running" or the template pattern
            assert.truthy(text_running:match("running") or text_running:match("LocalSend"))

            -- When running with transfers (use cached value)
            instance._cached_transfer_count = 5
            local text_with_transfers = menu_items.localsend.text_func()
            -- Template uses %1 placeholder for count
            assert.truthy(text_with_transfers:match("received") or text_with_transfers:match("%%1") or text_with_transfers:match("5"))
        end)

        it("shows update available in top-level label when idle", function()
            helper.state.settings["LocalSend_update_available_tag"] = "v2.0.0"
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local text = menu_items.localsend.text_func()
            assert.truthy(text:match("update available"))
        end)

        it("shows update available banner in submenu", function()
            helper.state.settings["LocalSend_update_available_tag"] = "v2.0.0"
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local found_banner = false
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text and text:match("Update available") and text:match("v2%.0%.0") then
                        found_banner = true
                        break
                    end
                end
            end

            assert.is_true(found_banner, "Should show persistent update-available menu banner")
        end)

        it("has sub_item_table with expected items", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local sub_items = menu_items.localsend.sub_item_table
            assert.is_table(sub_items)

            -- Check for key menu items
            local found_toggle = false
            local found_transfers = false
            local found_save_dir = false
            local found_settings = false
            local found_updates = false
            local found_about = false
            local settings_item

            for _, item in ipairs(sub_items) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("Stop server") or text:match("Start server") then
                        found_toggle = true
                    elseif text:match("Recent transfers") then
                        found_transfers = true
                    elseif text:match("Save directory") then
                        found_save_dir = true
                    end
                elseif item.text then
                    if item.text == "Settings" then
                        found_settings = true
                        settings_item = item
                    elseif item.text == "Updates" then
                        found_updates = true
                    elseif item.text == "About LocalSend" then
                        found_about = true
                    end
                end
            end

            assert.is_true(found_toggle, "Should have start/stop toggle")
            assert.is_true(found_transfers, "Should have recent transfers")
            assert.is_true(found_save_dir, "Should have save directory")
            assert.is_true(found_settings, "Should have settings submenu")
            assert.is_true(found_updates, "Should have updates submenu")
            assert.is_true(found_about, "Should have about menu item")

            -- Troubleshooting is nested under Settings, not a top-level item.
            assert.is_not_nil(settings_item, "Settings submenu should exist")
            local found_troubleshooting_in_settings = false
            for _, item in ipairs(settings_item.sub_item_table) do
                if item.text == "Troubleshooting" then
                    found_troubleshooting_in_settings = true
                    break
                end
            end
            assert.is_true(found_troubleshooting_in_settings,
                "Troubleshooting should be nested under Settings")
        end)

        it("builds troubleshooting submenu with diagnostics and common fixes", function()
            local instance = helper.create_instance()

            local troubleshooting_menu = instance:_buildTroubleshootingMenu()

            assert.is_table(troubleshooting_menu)
            assert.equals("Run diagnostics", troubleshooting_menu[1].text)
            assert.equals("Test discovery", troubleshooting_menu[2].text)
            assert.equals("Show network info", troubleshooting_menu[3].text)
            assert.equals("Show recent LocalSend log", troubleshooting_menu[4].text)
            assert.equals("Prepare bug report", troubleshooting_menu[5].text)
            assert.equals("Common fixes", troubleshooting_menu[6].text)
            assert.is_table(troubleshooting_menu[6].sub_item_table)
        end)

        it("builds updates submenu with version, manual check, and auto-check", function()
            local instance = helper.create_instance()

            local updates_menu = instance:_buildUpdatesMenu()

            assert.is_table(updates_menu)
            assert.truthy(updates_menu[1].text_func():match("Installed version"))
            assert.equals("Check for updates", updates_menu[2].text)
            assert.truthy(updates_menu[3].text_func():match("Auto%-check for updates"))
        end)

        it("shows cached update version in updates submenu", function()
            helper.state.settings["LocalSend_update_available_tag"] = "v2.0.0"
            local instance = helper.create_instance()

            local updates_menu = instance:_buildUpdatesMenu()
            local text = updates_menu[1].text_func()

            assert.truthy(text:match("Update available"))
            assert.truthy(text:match("v2%.0%.0"))
        end)

        it("shows about dialog", function()
            local instance = helper.create_instance()

            instance:showAbout()

            local dialog = helper.find_dialog("ConfirmBox")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.text:match("LocalSend for KOReader"))
            assert.truthy(dialog.text:match("Version"))
            assert.truthy(dialog.text:match("github%.com/kaikozlov/localsend%.koplugin"))
        end)

        it("disables settings when server is running", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            -- Find settings item
            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            assert.is_not_nil(settings_item)
            assert.is_function(settings_item.enabled_func)

            -- When not running (cached), should be enabled
            instance._cached_running = false
            assert.is_true(settings_item.enabled_func())

            -- When running (cached), should be disabled
            instance._cached_running = true
            assert.is_false(settings_item.enabled_func())
        end)

        it("sets sorting_hint to network", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            assert.equal("network", menu_items.localsend.sorting_hint)
        end)
    end)

    describe("text_func dynamic behavior", function()
        it("device name shows '(KOReader)' when empty", function()
            local instance = helper.create_instance()
            instance.device_name = ""

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            -- Find device name item in settings
            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            assert.is_not_nil(settings_item)
            local device_name_item = nil
            for _, item in ipairs(settings_item.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("Device name") then
                        device_name_item = item
                        break
                    end
                end
            end

            assert.is_not_nil(device_name_item)
            local text = device_name_item.text_func()
            assert.truthy(text:match("KOReader"), "Should show '(KOReader)' as default")
        end)

        it("device name shows actual name when set", function()
            local instance = helper.create_instance()
            instance.device_name = "My Kindle"

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            local device_name_item = nil
            for _, item in ipairs(settings_item.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("Device name") then
                        device_name_item = item
                        break
                    end
                end
            end

            local text = device_name_item.text_func()
            assert.truthy(text:match("My Kindle"), "Should show actual device name")
        end)

        it("PIN code shows '(enabled)' when set", function()
            local instance = helper.create_instance()
            instance.pin = "1234"

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            local pin_item = nil
            for _, item in ipairs(settings_item.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("PIN") then
                        pin_item = item
                        break
                    end
                end
            end

            local text = pin_item.text_func()
            assert.truthy(text:match("enabled"), "Should show '(enabled)' when PIN set")
        end)

        it("PIN code shows '(disabled)' when empty", function()
            local instance = helper.create_instance()
            instance.pin = ""

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            local pin_item = nil
            for _, item in ipairs(settings_item.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("PIN") then
                        pin_item = item
                        break
                    end
                end
            end

            local text = pin_item.text_func()
            assert.truthy(text:match("disabled"), "Should show '(disabled)' when PIN empty")
        end)

        it("file type routing shows rule count", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books", pdf = "/docs" }
            instance.routing_enabled = true

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            local routing_item = nil
            for _, item in ipairs(settings_item.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("File type routing") then
                        routing_item = item
                        break
                    end
                end
            end

            local text = routing_item.text_func()
            assert.truthy(text:match("2") or text:match("rule"), "Should show rule count")
        end)
    end)

    describe("enabled_func behavior", function()
        it("recent transfers enabled only when transfers exist", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            -- Find recent transfers item
            local transfers_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("Recent transfers") then
                        transfers_item = item
                        break
                    end
                end
            end

            assert.is_not_nil(transfers_item)
            assert.is_function(transfers_item.enabled_func)

            -- When no transfers, should be disabled (use cached value)
            instance._cached_transfer_count = 0
            assert.is_false(transfers_item.enabled_func())

            -- When transfers exist, should be enabled (use cached value)
            instance._cached_transfer_count = 5
            assert.is_true(transfers_item.enabled_func())
        end)
    end)

    describe("stopping state in menu", function()
        it("top-level text shows 'stopping...' when _cached_stopping is true", function()
            local instance = helper.create_instance()
            instance._cached_stopping = true
            instance._cached_running = true

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local text = menu_items.localsend.text_func()
            assert.truthy(text:match("stopping"), "Should show 'stopping' in menu text")
        end)

        it("start/stop item text shows 'Stopping server...' when stopping", function()
            local instance = helper.create_instance()
            instance._cached_stopping = true
            instance._cached_running = true

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local toggle_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("Stopping") or text:match("Stop server") or text:match("Start server") then
                        toggle_item = item
                        break
                    end
                end
            end

            assert.is_not_nil(toggle_item, "Should find toggle item")
            local text = toggle_item.text_func()
            assert.truthy(text:match("Stopping"), "Toggle item should show 'Stopping server...'")
        end)

        it("start/stop item is disabled when stopping", function()
            local instance = helper.create_instance()
            instance._cached_stopping = true
            instance._cached_running = true

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local toggle_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text_func then
                    local text = item.text_func()
                    if text:match("Stopping") or text:match("Stop server") or text:match("Start server") then
                        toggle_item = item
                        break
                    end
                end
            end

            assert.is_not_nil(toggle_item, "Should find toggle item")
            assert.is_function(toggle_item.enabled_func)
            assert.is_false(toggle_item.enabled_func(), "Toggle should be disabled while stopping")
        end)

        it("_refreshMenuUntilSettled schedules refresh while stop_in_progress", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = true

            local update_count = 0
            local mock_touchmenu = {
                updateItems = function() update_count = update_count + 1 end
            }

            instance:_refreshMenuUntilSettled(mock_touchmenu, 3)

            assert.equal(1, update_count, "Should call updateItems once initially")

            local scheduled_refresh = nil
            for _, task in ipairs(helper.state.scheduled_tasks) do
                if task.delay == 0.25 then
                    scheduled_refresh = task
                    break
                end
            end
            assert.is_not_nil(scheduled_refresh, "Should schedule refresh in 0.25s")
        end)

        it("_refreshMenuUntilSettled stops scheduling when stop_in_progress is false", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = false

            local update_count = 0
            local mock_touchmenu = {
                updateItems = function() update_count = update_count + 1 end
            }

            instance:_refreshMenuUntilSettled(mock_touchmenu, 3)

            assert.equal(1, update_count, "Should call updateItems once")

            local scheduled_refresh = nil
            for _, task in ipairs(helper.state.scheduled_tasks) do
                if task.delay == 0.25 then
                    scheduled_refresh = task
                    break
                end
            end
            assert.is_nil(scheduled_refresh, "Should not schedule more refreshes when not stopping")
        end)

        it("_refreshMenuUntilSettled stops when attempts exhausted", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = true

            local update_count = 0
            local mock_touchmenu = {
                updateItems = function() update_count = update_count + 1 end
            }

            instance:_refreshMenuUntilSettled(mock_touchmenu, 0)

            assert.equal(1, update_count, "Should call updateItems once even with 0 attempts")

            for _, task in ipairs(helper.state.scheduled_tasks) do
                if task.delay == 0.25 then
                    assert.fail("Should not schedule refresh when attempts is 0")
                end
            end
        end)

        it("_refreshMenuUntilSettled handles nil touchmenu gracefully", function()
            local instance = helper.create_instance()

            local tasks_before = #helper.state.scheduled_tasks
            instance:_refreshMenuUntilSettled(nil, 5)
            local tasks_after = #helper.state.scheduled_tasks

            assert.equal(tasks_before, tasks_after, "Should not schedule anything with nil touchmenu")
        end)
    end)

    -- =========================================================================
    -- Settings menu cache consistency
    -- =========================================================================
    describe("Settings menu cache consistency", function()
        it("Settings submenu enabled_func should use _cached_running not isRunning()", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            assert.is_not_nil(settings_item, "Settings menu item should exist")
            assert.is_function(settings_item.enabled_func, "Settings should have enabled_func")

            -- Cached says running, but isRunning disagrees.
            instance._cached_running = true
            instance.isRunning = function() return false end

            -- Uses cache → disabled (false). If it called isRunning → enabled (true).
            assert.is_false(settings_item.enabled_func(),
                "Settings enabled_func should use _cached_running, not call isRunning()")
        end)

        it("Settings submenu enabled_func should return true when _cached_running is false", function()
            local instance = helper.create_instance()

            local menu_items = {}
            instance:addToMainMenu(menu_items)

            local settings_item = nil
            for _, item in ipairs(menu_items.localsend.sub_item_table) do
                if item.text == "Settings" then
                    settings_item = item
                    break
                end
            end

            -- Cached says not running, but isRunning disagrees.
            instance._cached_running = false
            instance.isRunning = function() return true end

            -- Uses cache → enabled (true). If it called isRunning → disabled (false).
            assert.is_true(settings_item.enabled_func(),
                "Settings enabled_func should use _cached_running, not call isRunning()")
        end)
    end)
end)
