-- LocalSend utility functions
-- Extracted for testability

local M = {}

-- Resolve the directory containing a Lua module from debug.getinfo().source.
-- KOReader may load external plugins from arbitrary extra_plugin_paths, so the
-- plugin must not assume it lives below DataStorage:getFullDataDir()/plugins.
function M.moduleDir(source, fallback)
    if type(source) == "string" then
        local path = source:match("^@(.+)/[^/]+$")
        if path and path ~= "" then
            return path
        end
    end
    return fallback
end

-- Select a complete plugin installation. Normally source_dir wins, including
-- KOReader extra_plugin_paths. The fallback only wins when source_dir is a
-- partial/symlinked development tree without the shipped binary.
function M.resolvePluginDir(source_dir, fallback, path_exists)
    path_exists = path_exists or function()
        return false
    end
    if source_dir and path_exists(source_dir .. "/localsend") then
        return source_dir
    end
    if fallback and path_exists(fallback .. "/localsend") then
        return fallback
    end
    return source_dir or fallback
end

-- Follow KOReader's own home-folder precedence for an unset receive path.
function M.defaultSaveDir(device, settings, data_dir)
    local configured_home = settings and settings:readSetting("home_dir")
    if type(configured_home) == "string" and configured_home ~= "" then
        return configured_home
    end
    if device and type(device.home_dir) == "string" and device.home_dir ~= "" then
        return device.home_dir
    end
    return data_dir
end

-- Validate that a path is safe for shell operations
function M.isValidPath(path)
    if path == nil or path == "" then
        return false
    end
    -- Reject paths with null bytes
    if path:find("%z") then
        return false
    end
    -- Must be absolute path
    if not path:match("^/") then
        return false
    end
    -- No command substitution patterns
    if path:find("`") or path:find("%$%(") then
        return false
    end
    return true
end

-- Validate that a port number is safe for shell operations
function M.isValidPort(port)
    if port == nil then
        return false
    end
    local num = tonumber(port)
    if num == nil then
        return false
    end
    if num < 1 or num > 65535 then
        return false
    end
    -- Ensure it's an integer
    if num ~= math.floor(num) then
        return false
    end
    return true
end

-- Compare semantic versions
-- Returns: -1 if v1 < v2, 0 if equal, 1 if v1 > v2
function M.compareVersions(v1, v2)
    local function parseVersion(v)
        local parts = {}
        for num in string.gmatch(v:gsub("^v", ""), "(%d+)") do
            table.insert(parts, tonumber(num) or 0)
        end
        return parts
    end

    local p1, p2 = parseVersion(v1), parseVersion(v2)
    for i = 1, math.max(#p1, #p2) do
        local n1, n2 = p1[i] or 0, p2[i] or 0
        if n1 < n2 then
            return -1
        end
        if n1 > n2 then
            return 1
        end
    end
    return 0
end

-- Find download asset URL for given architecture
function M.findAssetForArch(assets, arch)
    local pattern = "localsend%-koplugin%-" .. arch .. "%.zip$"
    for _, asset in ipairs(assets) do
        if asset.name and asset.name:match(pattern) then
            return asset.browser_download_url, asset.name
        end
    end
    return nil, nil
end

-- Normalize curly quotes to straight quotes
function M.normalizeApostrophes(str)
    if str == nil then
        return nil
    end
    -- Replace curly single quotes (U+2018, U+2019) with straight quote
    return str:gsub("\xe2\x80\x98", "'"):gsub("\xe2\x80\x99", "'")
end

-- Validate device name for LocalSend
function M.validateDeviceName(name)
    -- Empty or nil name is valid (will use random name)
    if name == nil or name == "" then
        return true
    end

    -- Check length (reasonable limit)
    if #name > 64 then
        return false, "Device name is too long (max 64 characters)."
    end

    -- Normalize curly quotes to straight for validation
    local normalized = M.normalizeApostrophes(name)

    -- Only allow alphanumeric, spaces, hyphens, underscores, and apostrophes
    if not normalized:match("^[%w%s%-_']+$") then
        return false, "Device name can only contain letters, numbers, spaces, hyphens, underscores, and apostrophes."
    end

    return true
end

--- Build a radio button menu from a list of options
-- @param options table Array of {value, text} pairs
-- @param get_value function Returns the current selected value
-- @param set_value function(value) Called when an option is selected
-- @param enabled_func function Optional: returns whether options are enabled
-- @return table Menu items with radio = true
function M.buildRadioMenu(options, get_value, set_value, enabled_func)
    local menu = {}
    for _, opt in ipairs(options) do
        table.insert(menu, {
            text = opt.text,
            checked_func = function()
                return get_value() == opt.value
            end,
            radio = true,
            enabled_func = enabled_func,
            callback = function()
                set_value(opt.value)
            end,
        })
    end
    return menu
end

return M
