-- localsend_power.lua
-- Scoped KOReader power inhibition for short-lived LocalSend work.
--
-- The receiver itself must not keep an e-reader awake indefinitely. Instead,
-- callers acquire named holds only while a scan or file transfer is active.
-- Named holds make repeated polling idempotent and let independent operations
-- overlap without unbalancing UIManager's standby counter.

local PluginShare = require("pluginshare")
local UIManager = require("ui/uimanager")

local M = {}
local holds = {}
local hold_count = 0
local previous_pause_auto_suspend = nil
local standby_prevented = false

local function beginInhibit()
    previous_pause_auto_suspend = PluginShare.pause_auto_suspend == true
    PluginShare.pause_auto_suspend = true
    UIManager:preventStandby()
    standby_prevented = true
end

local function endInhibit()
    if standby_prevented then
        UIManager:allowStandby()
        standby_prevented = false
    end
    -- Preserve a pre-existing keep-awake request from another plugin. If
    -- LocalSend was the component that raised the flag, restore its old value.
    if not previous_pause_auto_suspend then
        PluginShare.pause_auto_suspend = false
    end
    previous_pause_auto_suspend = nil
end

function M.acquire(source)
    source = tostring(source or "")
    if source == "" or holds[source] then
        return false
    end
    if hold_count == 0 then
        beginInhibit()
    end
    holds[source] = true
    hold_count = hold_count + 1
    return true
end

function M.release(source)
    source = tostring(source or "")
    if source == "" or not holds[source] then
        return false
    end
    holds[source] = nil
    hold_count = hold_count - 1
    if hold_count == 0 then
        endInhibit()
    end
    return true
end

function M.releaseAll()
    if hold_count == 0 then
        return
    end
    holds = {}
    hold_count = 0
    endInhibit()
end

function M.isHeld(source)
    return holds[tostring(source or "")] == true
end

function M.holdCount()
    return hold_count
end

return M
