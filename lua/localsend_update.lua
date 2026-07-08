-- localsend_update.lua
-- Update system for LocalSend plugin
-- Handles checking for updates, downloading, and installing

local lsutils = require("localsend_utils")

local M = {}

-- Constants
M.GITHUB_RELEASE_URL = "https://api.github.com/repos/kaikozlov/localsend.koplugin/releases/latest"
M.REINSTALL_MARKER_FILE = ".reinstall_required"
M.TMP_TELEMETRY_PATTERN = "fm-out-*"
M.CACHE_SUBDIR = "localsend"

-- Dependencies container (set via M.init)
local deps = {}

-- Path configuration (set via M.init)
local cache_dir = nil

-- Initialize module with dependencies
-- @param d table Dependencies: { UIManager, InfoMessage, Notification, NetworkMgr, util, ffiutil, json, logger, T, _, cache_dir }
function M.init(d)
    deps = d
    cache_dir = d.cache_dir
end

-- Persist and mirror update availability state.
-- @param instance table LocalSend instance
-- @param tag_name string|nil Release tag (e.g., "v2.1.0") or nil to clear
local function setUpdateAvailable(instance, tag_name)
    local value = tag_name or ""
    instance.update_available_tag = value
    deps.G_reader_settings:saveSetting("LocalSend_update_available_tag", value)
end

-- Get the cache directory for update temp files, creating if needed
-- @return string Path to cache directory
local function getUpdateCacheDir()
    local dir = cache_dir .. "/" .. M.CACHE_SUBDIR
    deps.util.makePath(dir)
    return dir
end

-- Clean up all files in the update cache directory
function M.cleanupCache()
    local dir = cache_dir .. "/" .. M.CACHE_SUBDIR
    if deps.util.directoryExists(dir) then
        deps.ffiutil.purgeDir(dir)
    end
end

-- Check if reinstall marker file exists
-- @param plugin_path string Path to plugin directory
-- @return boolean True if reinstall is required
function M.isReinstallRequired(plugin_path)
    local marker_path = plugin_path .. "/" .. M.REINSTALL_MARKER_FILE
    local f = io.open(marker_path, "r")
    if f then
        f:close()
        return true
    end
    return false
end

-- Create reinstall marker file (called on update failure)
-- @param plugin_path string Path to plugin directory
function M.setReinstallRequired(plugin_path)
    local marker_path = plugin_path .. "/" .. M.REINSTALL_MARKER_FILE
    local open_ok, f = pcall(io.open, marker_path, "w")
    if open_ok and f then
        local write_ok = pcall(function()
            f:write("Update failed at " .. os.date("%Y-%m-%d %H:%M:%S") .. "\n")
        end)
        f:close()
        if not write_ok then
            deps.logger.dbg("[LocalSend] Failed to write reinstall marker")
        end
    end
end

-- Clear reinstall marker file (called on successful update)
-- @param plugin_path string Path to plugin directory
function M.clearReinstallRequired(plugin_path)
    local marker_path = plugin_path .. "/" .. M.REINSTALL_MARKER_FILE
    deps.util.removeFile(marker_path)
end

-- Clear Kindle framework telemetry files that fill up /tmp
-- These are Amazon telemetry upload queue files (fm-out-*) that accumulate
-- when telemetry uploads fail. They're safe to remove: meant to be uploaded
-- and deleted, cleared on reboot anyway. On non-Kindle devices, this is a no-op.
function M.clearTmpTelemetryFiles()
    -- Use ls + Lua pattern matching instead of find (busybox find may lack -delete)
    local handle = io.popen("ls -1 /tmp/ 2>/dev/null")
    if not handle then
        return
    end

    local ok, err = pcall(function()
        for filename in handle:lines() do
            -- Match fm-out-* pattern
            if filename:match("^fm%-out%-") then
                os.remove("/tmp/" .. filename)
            end
        end
    end)
    handle:close()

    if not ok then
        deps.logger.dbg("[LocalSend] Error clearing telemetry files:", err)
    end
end

-- Build a curl command for fetching JSON from a URL
-- @param output_file string Path to write response body
-- @param url string URL to fetch
-- @return string Shell-escaped curl command that outputs HTTP status code
function M.buildCurlCommand(output_file, url)
    return deps.util.shell_escape({
        "curl",
        "-s",
        "-o",
        output_file,
        "-w",
        "%{http_code}",
        "--connect-timeout",
        "10",
        "-H",
        "Accept: application/vnd.github.v3+json",
        url,
    })
end

-- Detect device architecture for selecting the right binary
-- @return string|nil Architecture name: "arm64", "armv7", "arm-legacy", or nil
function M.getDeviceArch()
    local handle = io.popen("uname -m")
    if not handle then
        return nil
    end
    local ok, arch = pcall(handle.read, handle, "*l")
    handle:close()
    if not ok then
        return nil
    end

    if not arch then
        return nil
    end

    -- Map uname output to our asset naming
    -- arm64/aarch64: 64-bit ARM (newer devices)
    -- armv7: 32-bit ARM with hardware float (most Kindles PW1+, returns "armv7l")
    -- armv5: legacy 32-bit ARM with soft float (K3, K4, older devices)
    if arch:match("^aarch64") or arch:match("^arm64") then
        return "arm64"
    elseif arch:match("^armv7") then
        return "armv7"
    elseif arch:match("^armv[56]") or arch:match("^arm") then
        -- armv5, armv6, or generic "arm" -> use legacy armv5 binary
        return "arm-legacy"
    end

    return nil
end

-- Perform the actual update download and installation
-- @param instance table LocalSend instance (unused, kept for API compatibility)
-- @param download_url string URL to download
-- @param asset_name string Name of the asset (unused, kept for API compatibility)
-- @param new_version string Version string
-- @param plugin_path string Path to plugin directory
function M.doPerformUpdate(instance, download_url, asset_name, new_version, plugin_path)
    -- Clear Kindle telemetry files that may fill up /tmp (no-op on non-Kindle)
    -- Still useful if any system operations use /tmp
    M.clearTmpTelemetryFiles()

    -- Clean up entire cache directory from any previous attempts (including orphaned files)
    M.cleanupCache()

    -- Now create fresh cache directory
    local update_cache = getUpdateCacheDir()
    local tmp_zip = update_cache .. "/localsend_update.zip"
    local tmp_extract = update_cache .. "/extract"

    -- Download the zip
    local cmd = deps.util.shell_escape({
        "curl",
        "-L",
        "-s",
        "-o",
        tmp_zip,
        "-w",
        "%{http_code}",
        "--connect-timeout",
        "30",
        "--max-time",
        "120",
        download_url,
    })

    local handle = io.popen(cmd)
    if not handle then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Failed to execute download command."),
        }))
        return
    end
    local ok, http_code = pcall(handle.read, handle, "*a")
    handle:close()
    if not ok then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Download failed: read error."),
        }))
        return
    end

    if http_code ~= "200" then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps.T(deps._("Download failed.\nHTTP status: %1"), http_code),
        }))
        deps.util.removeFile(tmp_zip)
        return
    end

    -- Verify zip was downloaded
    if not deps.util.pathExists(tmp_zip) then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Download failed: file not saved."),
        }))
        return
    end

    -- Create extraction directory
    deps.util.makePath(tmp_extract)

    -- Extract the zip (don't check return value - Lua 5.1 vs 5.2 incompatibility)
    os.execute(deps.util.shell_escape({ "unzip", "-o", tmp_zip, "-d", tmp_extract }))

    -- The zip contains localsend.koplugin/ folder
    local extracted_plugin = tmp_extract .. "/localsend.koplugin"

    if not deps.util.pathExists(extracted_plugin) then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Invalid update package structure."),
        }))
        M.cleanupCache()
        return
    end

    -- Track which lua files are in the update package (for orphan cleanup later)
    local new_lua_files = {}
    local track_handle = io.popen("ls " .. deps.util.shell_escape({ extracted_plugin }) .. "/*.lua 2>/dev/null")
    if track_handle then
        local ok, err = pcall(function()
            for lua_file in track_handle:lines() do
                local _, filename = deps.util.splitFilePathName(lua_file)
                if filename then
                    new_lua_files[filename] = true
                end
            end
        end)
        track_handle:close()
        if not ok then
            deps.logger.warn("[LocalSend] Error reading update package lua files:", err)
        end
    end

    -- Copy files to plugin directory
    -- Core files that must exist:
    local files_to_copy = { "main.lua", "_meta.lua", "localsend" }
    local copy_failed = false

    for _, file in ipairs(files_to_copy) do
        local src = extracted_plugin .. "/" .. file
        local dst = plugin_path .. "/" .. file

        if deps.util.pathExists(src) then
            -- Delete destination first so we can verify copy actually worked
            deps.util.removeFile(dst)
            os.execute(deps.util.shell_escape({ "cp", src, dst }))
            -- Verify copy succeeded by checking destination exists
            if not deps.util.pathExists(dst) then
                copy_failed = true
                deps.logger.err("[LocalSend] Failed to copy:", file)
            end
        else
            -- Core files are required - missing ones indicate a broken update package
            copy_failed = true
            deps.logger.err("[LocalSend] Core file missing from update package:", file)
        end
    end

    -- Also copy any additional .lua files (for future-proofing)
    local copy_handle = io.popen(deps.util.shell_escape({ "ls" }) .. " " .. deps.util.shell_escape({ extracted_plugin }) .. "/*.lua 2>/dev/null")
    if copy_handle then
        local process_ok, process_err = pcall(function()
            for lua_file in copy_handle:lines() do
                local _, filename = deps.util.splitFilePathName(lua_file)
                -- Skip files we already copied
                if filename and filename ~= "main.lua" and filename ~= "_meta.lua" then
                    local dst = plugin_path .. "/" .. filename
                    -- Delete destination first so we can verify copy actually worked
                    deps.util.removeFile(dst)
                    os.execute(deps.util.shell_escape({ "cp", lua_file, dst }))
                    -- Verify copy succeeded
                    if not deps.util.pathExists(dst) then
                        deps.logger.warn("[LocalSend] Failed to copy additional lua file:", filename)
                    else
                        deps.logger.dbg("[LocalSend] Copied additional lua file:", filename)
                    end
                end
            end
        end)
        copy_handle:close()
        if not process_ok then
            deps.logger.err("[LocalSend] Error processing lua files:", process_err)
        end
    end

    -- Make binary executable
    os.execute(deps.util.shell_escape({ "chmod", "+x", plugin_path .. "/localsend" }))

    -- Remove orphaned lua files (files that exist locally but not in the update)
    M.cleanupOrphanedLuaFiles(plugin_path, new_lua_files, copy_failed)

    -- Cleanup cache directory
    M.cleanupCache()

    if copy_failed then
        -- Mark that reinstall is required so user sees warning on restart
        M.setReinstallRequired(plugin_path)
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Update partially failed. Some files could not be copied. Please reinstall the plugin."),
        }))
        return
    end

    -- Success! Clear any previous reinstall marker
    M.clearReinstallRequired(plugin_path)
    setUpdateAvailable(instance, nil)
    deps.UIManager:show(deps.InfoMessage:new({
        text = deps.T(deps._("Update to %1 installed successfully!\n\nPlease restart KOReader for changes to take effect."), new_version),
    }))
end

-- Start update process with UI feedback
-- @param instance table LocalSend instance
-- @param download_url string URL to download
-- @param asset_name string Name of the asset
-- @param new_version string Version string
-- @param plugin_path string Path to plugin directory
function M.performUpdate(instance, download_url, asset_name, new_version, plugin_path)
    -- Stop server if running
    if instance:isRunning() then
        instance:stopServer()
    end

    deps.UIManager:show(deps.InfoMessage:new({
        text = deps._("Downloading update..."),
        timeout = 2,
    }))

    -- Give UI time to show the message
    deps.UIManager:scheduleIn(0.5, function()
        M.doPerformUpdate(instance, download_url, asset_name, new_version, plugin_path)
    end)
end

-- Remove lua files from plugin_path that aren't part of the update package
-- (orphans left behind by a prior version). Extracted from doPerformUpdate so it
-- can be unit-tested without a real download.
--
-- Safety: never remove critical files needed for recovery/versioning, and skip
-- cleanup entirely if the copy failed or we have no tracked files (avoids wiping
-- the plugin dir on a broken update).
--
-- @param plugin_path string Plugin directory to clean
-- @param new_lua_files table Set ({filename=true}) of lua files shipped in the update
-- @param copy_failed boolean True if the update copy step failed
-- @return table List of orphan filenames that were removed
function M.cleanupOrphanedLuaFiles(plugin_path, new_lua_files, copy_failed)
    local protected_files = {
        ["main.lua"] = true,
        ["localsend_update.lua"] = true,
        ["localsend_utils.lua"] = true,
        ["_meta.lua"] = true,
    }
    local removed = {}
    -- Safety check: don't cleanup if tracking failed (new_lua_files is empty)
    local has_tracked_files = next(new_lua_files) ~= nil
    if copy_failed or not has_tracked_files then
        return removed
    end
    local old_ls_handle = io.popen("ls " .. deps.util.shell_escape({ plugin_path }) .. "/*.lua 2>/dev/null")
    if not old_ls_handle then
        return removed
    end
    for old_file in old_ls_handle:lines() do
        local _, filename = deps.util.splitFilePathName(old_file)
        if filename and not new_lua_files[filename] and not protected_files[filename] then
            local rm_ok = deps.util.removeFile(plugin_path .. "/" .. filename)
            if rm_ok then
                table.insert(removed, filename)
                deps.logger.dbg("[LocalSend] Removed orphaned file:", filename)
            else
                deps.logger.warn("[LocalSend] Failed to remove orphaned file:", filename)
            end
        end
    end
    old_ls_handle:close()
    return removed
end

-- Calculate seconds until next update check
-- @param last_check number Timestamp of last check
-- @param interval_hours number Check interval in hours
-- @return number Delay in seconds
function M.getUpdateCheckDelay(last_check, interval_hours)
    local now = os.time()
    local interval_seconds = interval_hours * 3600
    local time_since_last = now - last_check
    local delay = interval_seconds - time_since_last
    -- If we're past due, schedule a short delay (60s) to not flood on startup
    if delay <= 0 then
        return 60 -- 1 minute delay for startup
    end
    return delay
end

-- Perform silent auto-check for updates
-- @param instance table LocalSend instance
-- @param plugin_version string Current plugin version
-- @param schedule_next function Callback to schedule next check
function M.doAutoCheckForUpdates(instance, plugin_version, schedule_next)
    local update_cache = getUpdateCacheDir()
    local tmp_file = update_cache .. "/update_check.json"
    local cmd = M.buildCurlCommand(tmp_file, M.GITHUB_RELEASE_URL)

    local handle = io.popen(cmd)
    if not handle then
        deps.logger.dbg("[LocalSend] Auto update check failed: io.popen returned nil")
        schedule_next()
        return
    end
    local ok, http_code = pcall(handle.read, handle, "*a")
    handle:close()
    if not ok then
        deps.logger.dbg("[LocalSend] Auto update check failed: read error")
        schedule_next()
        return
    end

    -- Update last check time regardless of result
    instance.last_update_check = os.time()
    deps.G_reader_settings:saveSetting("LocalSend_last_update_check", instance.last_update_check)

    if http_code ~= "200" then
        deps.logger.dbg("[LocalSend] Auto update check failed, HTTP:", http_code)
        deps.util.removeFile(tmp_file)
        schedule_next()
        return
    end

    local content = deps.util.readFromFile(tmp_file)
    deps.util.removeFile(tmp_file)

    if not content then
        schedule_next()
        return
    end

    local decode_ok, release = pcall(deps.json.decode, content)
    if not decode_ok or not release or not release.tag_name then
        schedule_next()
        return
    end

    local latest_version = release.tag_name:gsub("^v", "")
    local current_version = plugin_version:gsub("^v", "")

    -- Check if update available
    if lsutils.compareVersions(current_version, latest_version) < 0 then
        local should_notify = instance.update_available_tag ~= release.tag_name
        setUpdateAvailable(instance, release.tag_name)

        if should_notify then
            deps.UIManager:show(deps.Notification:new({
                text = deps.T(deps._("LocalSend update available: %1"), release.tag_name),
                timeout = 5,
            }))
        end
    else
        setUpdateAvailable(instance, nil)
    end

    -- Schedule next check
    schedule_next()
end

-- Perform manual check for updates with UI feedback
-- @param instance table LocalSend instance
-- @param plugin_version string Current plugin version
-- @param plugin_path string Path to plugin directory
function M.doCheckForUpdates(instance, plugin_version, plugin_path)
    deps.UIManager:show(deps.InfoMessage:new({
        text = deps._("Checking for updates..."),
        timeout = 2,
    }))

    local update_cache = getUpdateCacheDir()
    local tmp_file = update_cache .. "/update_check.json"
    local cmd = M.buildCurlCommand(tmp_file, M.GITHUB_RELEASE_URL)

    local handle = io.popen(cmd)
    if not handle then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Failed to execute update check command."),
        }))
        return
    end
    local ok, http_code = pcall(handle.read, handle, "*a")
    handle:close()
    if not ok then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Failed to read update check response."),
        }))
        return
    end

    if http_code ~= "200" then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps.T(deps._("Failed to check for updates.\nHTTP status: %1\n\nPlease check your internet connection."), http_code),
        }))
        deps.util.removeFile(tmp_file)
        return
    end

    local content = deps.util.readFromFile(tmp_file)
    deps.util.removeFile(tmp_file)

    if not content then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Failed to read update information."),
        }))
        return
    end

    local decode_ok, release = pcall(deps.json.decode, content)
    if not decode_ok or not release or not release.tag_name then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Failed to parse update information."),
        }))
        return
    end

    local latest_version = release.tag_name:gsub("^v", "")
    local current_version = plugin_version:gsub("^v", "")

    -- Check if we can auto-update (needed for both update and reinstall)
    local arch = M.getDeviceArch()
    local download_url, asset_name

    if arch and release.assets then
        download_url, asset_name = lsutils.findAssetForArch(release.assets, arch)
    end

    if lsutils.compareVersions(current_version, latest_version) >= 0 then
        setUpdateAvailable(instance, nil)

        -- Up to date - offer reinstall option if possible
        if download_url then
            local ConfirmBox = require("ui/widget/confirmbox")
            local up_to_date_msg = deps._("You're up to date!\n\nCurrent version: %1\nLatest version: %2\n\nReinstall anyway?")
            deps.UIManager:show(ConfirmBox:new({
                text = deps.T(up_to_date_msg, plugin_version, release.tag_name),
                ok_text = deps._("Reinstall"),
                cancel_text = deps._("Cancel"),
                ok_callback = function()
                    M.performUpdate(instance, download_url, asset_name, release.tag_name, plugin_path)
                end,
            }))
        else
            deps.UIManager:show(deps.InfoMessage:new({
                text = deps.T(deps._("You're up to date!\n\nCurrent version: %1\nLatest version: %2"), plugin_version, release.tag_name),
                timeout = 5,
            }))
        end
    else
        setUpdateAvailable(instance, release.tag_name)

        -- Update available

        local release_notes = release.body or deps._("No release notes available.")

        if download_url then
            -- Can auto-update. Use TextViewer so long release notes remain readable.
            local TextViewer = require("ui/widget/textviewer")
            local viewer
            viewer = TextViewer:new({
                title = deps._("Update available!"),
                text = deps.T(deps._("Current: %1\nLatest: %2\n\n%3"), plugin_version, release.tag_name, release_notes),
                buttons_table = {
                    {
                        {
                            text = deps._("Later"),
                            callback = function()
                                deps.UIManager:close(viewer)
                            end,
                        },
                        {
                            text = deps._("Install"),
                            callback = function()
                                deps.UIManager:close(viewer)
                                M.performUpdate(instance, download_url, asset_name, release.tag_name, plugin_path)
                            end,
                        },
                    },
                },
                add_default_buttons = false,
            })
            deps.UIManager:show(viewer)
        else
            -- Can't auto-update (unknown arch or no matching asset)
            local reason
            if not arch then
                reason = deps._("\n\nAuto-update not available: unknown device architecture.")
            else
                reason = deps.T(deps._("\n\nAuto-update not available: no package for %1 architecture."), arch)
            end

            deps.UIManager:show(deps.InfoMessage:new({
                text = deps.T(
                    deps._("Update available!\n\nCurrent: %1\nLatest: %2\n\n%3%4\n\nVisit GitHub to download manually."),
                    plugin_version,
                    release.tag_name,
                    release_notes,
                    reason
                ),
            }))
        end
    end
end

-- Check for updates (handles network prompting)
-- @param instance table LocalSend instance
-- @param plugin_version string Current plugin version
-- @param plugin_path string Path to plugin directory
function M.checkForUpdates(instance, plugin_version, plugin_path)
    deps.NetworkMgr:runWhenOnline(function()
        M.doCheckForUpdates(instance, plugin_version, plugin_path)
    end)
end

return M
