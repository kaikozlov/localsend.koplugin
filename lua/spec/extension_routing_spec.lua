require("busted.runner")()
local helper = require("spec.spec_helper")
local json = require("json")

-- Tests for extension routing functionality

describe("Extension Routing", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("addExtensionRoute", function()
        it("should lowercase extension", function()
            local instance = helper.create_instance()

            instance:addExtensionRoute("EPUB", "/books")

            assert.is_not_nil(instance.ext_dirs["epub"])
            assert.is_nil(instance.ext_dirs["EPUB"])
            assert.equal("/books", instance.ext_dirs["epub"])
        end)

        it("should auto-enable routing on first route", function()
            local instance = helper.create_instance()

            assert.is_false(instance.routing_enabled)

            instance:addExtensionRoute("epub", "/books")

            assert.is_true(instance.routing_enabled)
        end)

        it("should persist routes to settings", function()
            local instance = helper.create_instance()

            instance:addExtensionRoute("epub", "/books")
            instance:addExtensionRoute("pdf", "/docs")

            local saved = helper.state.settings["LocalSend_ext_dirs"]
            assert.is_not_nil(saved)
            assert.equal("/books", saved["epub"])
            assert.equal("/docs", saved["pdf"])
        end)

        it("should overwrite existing route for same extension", function()
            local instance = helper.create_instance()

            instance:addExtensionRoute("epub", "/old")
            instance:addExtensionRoute("epub", "/new")

            assert.equal("/new", instance.ext_dirs["epub"])
        end)
    end)

    describe("removeExtensionRoute", function()
        it("should remove route", function()
            local instance = helper.create_instance()

            instance:addExtensionRoute("epub", "/books")
            instance:addExtensionRoute("pdf", "/docs")

            instance:removeExtensionRoute("epub")

            assert.is_nil(instance.ext_dirs["epub"])
            assert.equal("/docs", instance.ext_dirs["pdf"])
        end)

        it("should handle case-insensitive removal", function()
            local instance = helper.create_instance()

            instance:addExtensionRoute("epub", "/books")
            instance:removeExtensionRoute("EPUB")

            assert.is_nil(instance.ext_dirs["epub"])
        end)

        it("should not error when removing non-existent route", function()
            local instance = helper.create_instance()

            assert.has_no.errors(function()
                instance:removeExtensionRoute("nonexistent")
            end)
        end)
    end)

    describe("exportExtRouting", function()
        it("should return nil when routing disabled", function()
            local instance = helper.create_instance()

            instance.routing_enabled = false
            instance.ext_dirs = { epub = "/books" }

            local result = instance:exportExtRouting()
            assert.is_nil(result)
        end)

        it("should return nil when no routes configured", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.ext_dirs = {}

            local result = instance:exportExtRouting()
            assert.is_nil(result)
        end)

        it("should not include default when routing_accept_all is false", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.routing_accept_all = false
            instance.ext_dirs = { epub = "/books" }
            instance.save_dir = "/default"

            -- Mock io.open to capture what's written
            local written_content = nil
            local mock_file = {
                write = function(self, content)
                    written_content = content
                end,
                close = function() end,
            }
            local original_io_open = io.open
            io.open = function(path, mode)
                if mode == "w" then
                    return mock_file
                end
                return original_io_open(path, mode)
            end

            instance:exportExtRouting()

            io.open = original_io_open

            assert.is_not_nil(written_content)
            assert.is_nil(written_content:match('"default"'))
        end)

        it("should include default when routing_accept_all is true", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.routing_accept_all = true
            instance.ext_dirs = { epub = "/books" }
            instance.save_dir = "/default"

            local written_content = nil
            local mock_file = {
                write = function(self, content)
                    written_content = content
                end,
                close = function() end,
            }
            local original_io_open = io.open
            io.open = function(path, mode)
                if mode == "w" then
                    return mock_file
                end
                return original_io_open(path, mode)
            end

            instance:exportExtRouting()

            io.open = original_io_open

            assert.is_not_nil(written_content)
            assert.is_not_nil(written_content:match('"default"'))
            assert.is_not_nil(written_content:match("/default"))
        end)

        it("returns nil when io.open fails", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.ext_dirs = { epub = "/books" }

            local original_io_open = io.open
            io.open = function(path, mode)
                if path:match("ext_routing%.json") then
                    return nil -- Simulate file open failure
                end
                return original_io_open(path, mode)
            end

            local result = instance:exportExtRouting()

            io.open = original_io_open

            assert.is_nil(result)
        end)

        it("returns nil when json.encode throws error", function()
            local orig_encode = json.encode
            json.encode = function(_t)
                error("JSON encoding error")
            end

            local instance = helper.create_instance()
            instance.routing_enabled = true
            instance.ext_dirs = { epub = "/books" }

            local mock_file = {
                write = function(_self, _content)
                    error("JSON error")
                end,
                close = function() end,
            }
            local original_io_open = io.open
            io.open = function(path, mode)
                if path:match("ext_routing%.json") and mode == "w" then
                    return mock_file
                end
                return original_io_open(path, mode)
            end

            local result = instance:exportExtRouting()

            io.open = original_io_open
            json.encode = orig_encode

            assert.is_nil(result)
        end)

        it("closes file even when write fails", function()
            local orig_encode = json.encode
            json.encode = function(_t)
                error("JSON encoding error")
            end

            local instance = helper.create_instance()
            instance.routing_enabled = true
            instance.ext_dirs = { epub = "/books" }

            local close_called = false
            local mock_file = {
                write = function(_self, _content)
                    error("Write error")
                end,
                close = function()
                    close_called = true
                end,
            }
            local original_io_open = io.open
            io.open = function(path, mode)
                if path:match("ext_routing%.json") and mode == "w" then
                    return mock_file
                end
                return original_io_open(path, mode)
            end

            instance:exportExtRouting()

            io.open = original_io_open
            json.encode = orig_encode

            assert.is_true(close_called, "Should close file even on write failure")
        end)
    end)

    describe("Route action dialog", function()
        it("should show action dialog with Change directory button", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books", pdf = "/docs" }
            instance.routing_enabled = true

            local menu = instance:buildExtensionRoutingMenu({ updateItems = function() end })

            -- Find route item with callback (route items have ".ext → dir" format)
            local route_item = nil
            for _, item in ipairs(menu) do
                if item.callback and item.text and item.text:match("→") and item.text:match("^%.") then
                    route_item = item
                    break
                end
            end

            assert.is_not_nil(route_item, "Should have a route item with callback")
            route_item.callback({ updateItems = function() end })

            -- Should show ButtonDialog with buttons
            assert.is_true(#helper.state.dialogs_shown > 0, "Should show action dialog")
            local dialog = helper.state.dialogs_shown[1]
            assert.is_not_nil(dialog.buttons)

            -- Check for Change directory button
            local found_change = false
            for _, row in ipairs(dialog.buttons) do
                for _, btn in ipairs(row) do
                    if btn.text and btn.text:match("Change directory") then
                        found_change = true
                        break
                    end
                end
            end
            assert.is_true(found_change, "Should have 'Change directory' button")
        end)

        it("should show action dialog with Remove route button", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books" }
            instance.routing_enabled = true

            local menu = instance:buildExtensionRoutingMenu({ updateItems = function() end })

            -- Find route item with callback (route items have ".ext → dir" format)
            local route_item = nil
            for _, item in ipairs(menu) do
                if item.callback and item.text and item.text:match("→") and item.text:match("^%.") then
                    route_item = item
                    break
                end
            end

            route_item.callback({ updateItems = function() end })

            local dialog = helper.state.dialogs_shown[1]
            local found_remove = false
            for _, row in ipairs(dialog.buttons) do
                for _, btn in ipairs(row) do
                    if btn.text and btn.text:match("Remove route") then
                        found_remove = true
                        break
                    end
                end
            end
            assert.is_true(found_remove, "Should have 'Remove route' button")
        end)

        it("Remove route button should remove the route", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books", pdf = "/docs" }
            instance.routing_enabled = true

            local menu = instance:buildExtensionRoutingMenu({ updateItems = function() end })

            -- Find route item with callback (route items have ".ext → dir" format)
            local route_item = nil
            for _, item in ipairs(menu) do
                if item.callback and item.text and item.text:match("→") and item.text:match("^%.") then
                    route_item = item
                    break
                end
            end
            route_item.callback({ updateItems = function() end })

            -- Find and click Remove route button
            local dialog = helper.state.dialogs_shown[1]
            local remove_button = nil
            for _, row in ipairs(dialog.buttons) do
                for _, btn in ipairs(row) do
                    if btn.text and btn.text:match("Remove route") then
                        remove_button = btn
                        break
                    end
                end
            end

            -- Count routes before
            local count_before = 0
            for _ in pairs(instance.ext_dirs) do
                count_before = count_before + 1
            end

            remove_button.callback()

            -- Count routes after
            local count_after = 0
            for _ in pairs(instance.ext_dirs) do
                count_after = count_after + 1
            end

            -- Should have one less route
            assert.equal(count_before - 1, count_after, "Should remove one route")

            -- Should show notification
            local found_notification = helper.find_notification("removed")
            assert.is_truthy(found_notification, "Should show removal notification")
        end)

        it("Change directory button should open path picker", function()
            local instance = helper.create_instance()
            instance.ext_dirs = { epub = "/books" }
            instance.routing_enabled = true
            instance.save_dir = "/mnt/us/documents"

            local picker_called = false
            instance.showExtensionDirPicker = function(self, ext, menu)
                picker_called = true
            end

            local menu = instance:buildExtensionRoutingMenu({ updateItems = function() end })

            -- Find route item with callback (route items have ".ext → dir" format)
            local route_item = nil
            for _, item in ipairs(menu) do
                if item.callback and item.text and item.text:match("→") and item.text:match("^%.") then
                    route_item = item
                    break
                end
            end
            route_item.callback({ updateItems = function() end })

            -- Find and click Change directory button
            local dialog = helper.state.dialogs_shown[1]
            local change_button = nil
            for _, row in ipairs(dialog.buttons) do
                for _, btn in ipairs(row) do
                    if btn.text and btn.text:match("Change directory") then
                        change_button = btn
                        break
                    end
                end
            end

            change_button.callback()

            assert.is_true(picker_called, "Should call showExtensionDirPicker")
        end)
    end)
end)
