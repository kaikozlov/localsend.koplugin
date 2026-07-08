-- localsend_routing.lua
-- Extension routing system for LocalSend plugin
-- Routes different file types to different directories

local M = {}

-- Dependencies container (set via M.init)
local deps = {}

-- Initialize module with dependencies
-- @param d table Dependencies: { UIManager, InfoMessage, InputDialog, PathChooser, json, logger, T, _, G_reader_settings }
function M.init(d)
    deps = d
end

-- Export extension routing config to JSON file for CLI
-- @param routing_enabled boolean Whether routing is enabled
-- @param ext_dirs table Extension to directory mapping
-- @param routing_accept_all boolean Whether to accept unrouted files
-- @param save_dir string Default save directory
-- @param plugin_path string Path to plugin directory
-- @return string|nil Path to exported config file, or nil if routing disabled
function M.exportExtRouting(routing_enabled, ext_dirs, routing_accept_all, save_dir, plugin_path)
    if not routing_enabled or not next(ext_dirs) then
        return nil -- Routing disabled or no routes configured
    end

    local config = {}
    for ext, dir in pairs(ext_dirs) do
        config[ext] = dir
    end

    -- Only include default if "accept all" is enabled
    if routing_accept_all then
        config["default"] = save_dir
    end

    local path = plugin_path .. "/ext_routing.json"
    local open_ok, f = pcall(io.open, path, "w")
    if open_ok and f then
        local write_ok, err = pcall(function()
            f:write(deps.json.encode(config))
        end)
        f:close()
        if not write_ok then
            deps.logger.warn("[LocalSend] Failed to write extension routing config:", err)
            return nil
        end
        deps.logger.dbg("[LocalSend] Exported extension routing config to", path)
        return path
    elseif not open_ok then
        deps.logger.warn("[LocalSend] Failed to open extension routing config file:", f)
    end
    return nil
end

-- Add an extension route
-- @param instance table LocalSend instance
-- @param ext string File extension (without dot)
-- @param dir string Directory path
function M.addExtensionRoute(instance, ext, dir)
    ext = string.lower(ext)
    -- Auto-enable routing when adding first route
    if not next(instance.ext_dirs) and not instance.routing_enabled then
        instance.routing_enabled = true
        deps.G_reader_settings:saveSetting("LocalSend_routing_enabled", true)
    end
    instance.ext_dirs[ext] = dir
    deps.G_reader_settings:saveSetting("LocalSend_ext_dirs", instance.ext_dirs)
end

-- Remove an extension route
-- @param instance table LocalSend instance
-- @param ext string File extension (without dot)
function M.removeExtensionRoute(instance, ext)
    ext = string.lower(ext)
    instance.ext_dirs[ext] = nil
    deps.G_reader_settings:saveSetting("LocalSend_ext_dirs", instance.ext_dirs)
end

-- Show dialog to add a new extension route
-- @param instance table LocalSend instance
-- @param touchmenu_instance table Touch menu instance for updates
function M.showAddExtensionRouteDialog(instance, touchmenu_instance)
    -- Common extension presets for e-readers
    local ButtonDialog = require("ui/widget/buttondialog")
    local ext_presets = {
        { "epub", "pdf", "mobi" },
        { "azw3", "cbz", "cbr" },
    }

    local dialog
    local buttons = {}
    for _, row in ipairs(ext_presets) do
        local button_row = {}
        for _, ext in ipairs(row) do
            table.insert(button_row, {
                text = ext,
                callback = function()
                    deps.UIManager:close(dialog)
                    instance:showExtensionDirPicker(ext, touchmenu_instance)
                end,
            })
        end
        table.insert(buttons, button_row)
    end

    -- Add custom option
    table.insert(buttons, {
        {
            text = deps._("Custom..."),
            callback = function()
                deps.UIManager:close(dialog)
                instance:showCustomExtensionDialog(touchmenu_instance)
            end,
        },
    })

    dialog = ButtonDialog:new({
        title = deps._("Select extension to route"),
        buttons = buttons,
    })
    deps.UIManager:show(dialog)
end

-- Show dialog for custom extension input
-- @param instance table LocalSend instance
-- @param touchmenu_instance table Touch menu instance for updates
function M.showCustomExtensionDialog(instance, touchmenu_instance)
    local dialog
    dialog = deps.InputDialog:new({
        title = deps._("Extension to route"),
        description = deps._("Enter file extension (without dot)"),
        input = "",
        input_hint = "epub",
        buttons = {
            {
                {
                    text = deps._("Cancel"),
                    id = "close",
                    callback = function()
                        deps.UIManager:close(dialog)
                    end,
                },
                {
                    text = deps._("Next"),
                    is_enter_default = true,
                    callback = function()
                        local ext = dialog:getInputText()
                        if ext and ext ~= "" then
                            ext = string.lower(ext:gsub("^%.", "")) -- Remove leading dot if present
                            deps.UIManager:close(dialog)
                            instance:showExtensionDirPicker(ext, touchmenu_instance)
                        end
                    end,
                },
            },
        },
    })
    deps.UIManager:show(dialog)
    dialog:onShowKeyboard()
end

-- Show directory picker for an extension
-- @param instance table LocalSend instance
-- @param ext string File extension
-- @param touchmenu_instance table Touch menu instance for updates
function M.showExtensionDirPicker(instance, ext, touchmenu_instance)
    local start_path = instance:getPickerStartPath(instance.ext_dirs[ext] or instance.save_dir)
    local path_chooser = deps.PathChooser:new({
        title = deps.T(deps._("Select directory for .%1 files"), ext),
        select_directory = true,
        select_file = false,
        path = start_path,
        onConfirm = function(path)
            local valid, err = instance:validateSaveDir(path)
            if valid then
                instance:addExtensionRoute(ext, path)
                instance:refreshRoutingMenu(touchmenu_instance)
                deps.UIManager:show(deps.InfoMessage:new({
                    text = deps.T(deps._(".%1 files will be saved to:\n%2"), ext, path),
                    timeout = 3,
                }))
            else
                deps.UIManager:show(deps.InfoMessage:new({
                    icon = "notice-warning",
                    text = deps.T(deps._("Cannot use this directory: %1"), err),
                }))
            end
        end,
    })
    deps.UIManager:show(path_chooser)
end

-- Refresh the routing menu by rebuilding its item_table in-place
-- This is needed because TouchMenu caches sub_item_table_func results
-- @param instance table LocalSend instance
-- @param touchmenu_instance table Touch menu instance
function M.refreshRoutingMenu(instance, touchmenu_instance)
    if not touchmenu_instance then
        return
    end
    -- Rebuild the menu items
    local new_items = instance:buildExtensionRoutingMenu()
    -- Clear and repopulate the existing item_table in-place
    local item_table = touchmenu_instance.item_table
    if not item_table then
        -- Fallback for test mocks or edge cases
        touchmenu_instance:updateItems()
        return
    end
    for i = #item_table, 1, -1 do
        item_table[i] = nil
    end
    for i, item in ipairs(new_items) do
        item_table[i] = item
    end
    touchmenu_instance:updateItems()
end

-- Build the extension routing menu
-- @param instance table LocalSend instance
-- @return table Menu items
function M.buildExtensionRoutingMenu(instance)
    local menu = {}

    -- Enable/disable toggle (shown first when routes exist)
    local has_routes = next(instance.ext_dirs) ~= nil
    if has_routes then
        table.insert(menu, {
            text = deps._("Enable file type routing"),
            checked_func = function()
                return instance.routing_enabled
            end,
            callback = function()
                instance.routing_enabled = not instance.routing_enabled
                deps.G_reader_settings:flipNilOrFalse("LocalSend_routing_enabled")
            end,
            help_text = deps._(
                "When enabled, files are routed to directories based on extension. " .. "When disabled, all files go to the main save directory."
            ),
            separator = true,
        })
    end

    -- Show existing routes
    for ext, dir in pairs(instance.ext_dirs) do
        local captured_ext = ext -- Capture for closure
        table.insert(menu, {
            text = deps.T(deps._(".%1 → %2"), ext, dir),
            keep_menu_open = true,
            callback = function(touchmenu_instance)
                -- Show options: change directory or remove
                local ButtonDialog = require("ui/widget/buttondialog")
                local dialog
                dialog = ButtonDialog:new({
                    title = deps.T(deps._("Route for .%1"), captured_ext),
                    buttons = {
                        {
                            {
                                text = deps._("Change directory"),
                                callback = function()
                                    deps.UIManager:close(dialog)
                                    instance:showExtensionDirPicker(captured_ext, touchmenu_instance)
                                end,
                            },
                        },
                        {
                            {
                                text = deps._("Remove route"),
                                callback = function()
                                    deps.UIManager:close(dialog)
                                    instance:removeExtensionRoute(captured_ext)
                                    instance:refreshRoutingMenu(touchmenu_instance)
                                    deps.UIManager:show(deps.InfoMessage:new({
                                        text = deps.T(deps._("Route for .%1 removed"), captured_ext),
                                        timeout = 2,
                                    }))
                                end,
                            },
                        },
                    },
                })
                deps.UIManager:show(dialog)
            end,
        })
    end

    -- Add separator after routes (mark the last route item)
    if has_routes and #menu > 0 then
        menu[#menu].separator = true
    end

    -- Add new route option
    table.insert(menu, {
        text = deps._("Add extension route..."),
        keep_menu_open = true,
        callback = function(touchmenu_instance)
            instance:showAddExtensionRouteDialog(touchmenu_instance)
        end,
    })

    -- Only show "accept all" option when routes exist
    if has_routes then
        table.insert(menu, {
            text = deps._("Accept other files → main directory"),
            checked_func = function()
                return instance.routing_accept_all
            end,
            callback = function()
                instance.routing_accept_all = not instance.routing_accept_all
                deps.G_reader_settings:flipNilOrFalse("LocalSend_routing_accept_all")
            end,
            help_text = deps._(
                "When enabled, files without a specific route are saved to the main "
                    .. "save directory. When disabled, only routed file types are accepted."
            ),
        })
    end

    return menu
end

return M
