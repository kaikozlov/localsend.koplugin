-- localsend_firewall.lua
-- iptables firewall management for LocalSend plugin.
-- Owns LocalSend firewall rule definitions, open/close, verification, and self-test.

local constants = require("localsend_constants")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}

-- Initialize module with dependencies
-- @param d table Dependencies: { Device, util, logger }
function M.init(d)
    deps = d
end

local function execute(cmd)
    local result = os.execute(cmd)
    return result == 0
end

local function iptablesAvailable()
    return execute("command -v iptables >/dev/null 2>&1")
end

local function ruleLabel(rule_args)
    local proto, dport
    for i, arg in ipairs(rule_args) do
        if arg == "-p" then
            proto = rule_args[i + 1]
        end
        if arg == "--dport" then
            dport = rule_args[i + 1]
        end
    end
    if proto and dport then
        return proto .. "/" .. dport
    end
    return table.concat(rule_args, " ")
end

local function inputRules(port, use_webrtc)
    local rules = {
        {
            "INPUT",
            "-p",
            "tcp",
            "--dport",
            port,
            "-m",
            "conntrack",
            "--ctstate",
            "NEW,ESTABLISHED",
            "-j",
            "ACCEPT",
        },
        { "INPUT", "-p", "udp", "--dport", port, "-j", "ACCEPT" },
    }
    if use_webrtc then
        table.insert(rules, {
            "INPUT",
            "-p",
            "udp",
            "--dport",
            constants.WEBRTC_PORT_RANGE,
            "-j",
            "ACCEPT",
        })
    end
    return rules
end

local function outputRules(port, use_webrtc)
    local rules = {
        {
            "OUTPUT",
            "-p",
            "tcp",
            "--sport",
            port,
            "-m",
            "conntrack",
            "--ctstate",
            "ESTABLISHED",
            "-j",
            "ACCEPT",
        },
        { "OUTPUT", "-p", "udp", "--sport", port, "-j", "ACCEPT" },
    }
    if use_webrtc then
        table.insert(rules, {
            "OUTPUT",
            "-p",
            "udp",
            "--sport",
            constants.WEBRTC_PORT_RANGE,
            "-j",
            "ACCEPT",
        })
    end
    return rules
end

local function allRules(port, use_webrtc)
    local rules = inputRules(port, use_webrtc)
    for _, rule in ipairs(outputRules(port, use_webrtc)) do
        table.insert(rules, rule)
    end
    return rules
end

-- Check if an iptables rule exists.
local function iptablesRuleExists(rule_args)
    local cmd_args = { "iptables", "-C" }
    for _, arg in ipairs(rule_args) do
        table.insert(cmd_args, arg)
    end
    return execute(deps.util.shell_escape(cmd_args) .. " 2>/dev/null")
end

-- Add iptables rule only if it doesn't already exist.
-- @return boolean ok, string detail
local function iptablesAddIfMissing(rule_args)
    if iptablesRuleExists(rule_args) then
        return true, "exists"
    end
    local cmd_args = { "iptables", "-A" }
    for _, arg in ipairs(rule_args) do
        table.insert(cmd_args, arg)
    end
    if execute(deps.util.shell_escape(cmd_args) .. " 2>/dev/null") then
        return true, "added"
    end
    return false, ruleLabel(rule_args)
end

-- Delete iptables rule (silently ignores if rule doesn't exist)
local function iptablesDelete(rule_args)
    local cmd_args = { "iptables", "-D" }
    for _, arg in ipairs(rule_args) do
        table.insert(cmd_args, arg)
    end
    return execute(deps.util.shell_escape(cmd_args) .. " 2>/dev/null")
end

local function unmanagedResult(detail)
    return { managed = false, ok = true, detail = detail or "iptables not available" }
end

-- Open firewall ports for LocalSend.
-- @param port string|number The port to open
-- @param use_webrtc boolean Whether to also open WebRTC ports
-- @return table { managed = bool, ok = bool, detail = string }
function M.openFirewall(port, use_webrtc)
    if not iptablesAvailable() then
        return unmanagedResult()
    end

    port = tostring(port)
    local failures = {}
    for _, rule in ipairs(allRules(port, use_webrtc)) do
        local ok, detail = iptablesAddIfMissing(rule)
        if not ok then
            table.insert(failures, detail)
        end
    end

    if #failures > 0 then
        local detail = "failed to add " .. table.concat(failures, ", ")
        deps.logger.err("[LocalSend] Firewall open failed for port " .. port .. ": " .. detail)
        return { managed = true, ok = false, detail = detail }
    end
    deps.logger.dbg("[LocalSend] Firewall opened for port " .. port)
    if use_webrtc then
        deps.logger.dbg("[LocalSend] Firewall opened for WebRTC UDP ports (50000-50100)")
    end
    return { managed = true, ok = true, detail = "iptables rules open" }
end

-- Close firewall ports for LocalSend.
-- Missing rules are ignored; this is cleanup.
-- @param port string|number The port to close
-- @return table { managed = bool, ok = bool, detail = string }
function M.closeFirewall(port)
    if not iptablesAvailable() then
        return unmanagedResult()
    end

    port = tostring(port)
    -- Always remove WebRTC rules during cleanup, even when WebRTC is currently off,
    -- so old rules do not survive a setting change.
    for _, rule in ipairs(allRules(port, true)) do
        iptablesDelete(rule)
    end
    deps.logger.dbg("[LocalSend] Firewall closed for port " .. port)
    return { managed = true, ok = true, detail = "iptables rules closed" }
end

-- Verify that required INPUT rules are present. OUTPUT rules are implementation
-- cleanup details; incoming TCP/UDP reachability is the user-visible requirement.
-- @param port string|number The LocalSend port
-- @param use_webrtc boolean Whether WebRTC range should be open
-- @return table { managed = bool, ok = bool, detail = string, status = string }
function M.checkFirewall(port, use_webrtc)
    if not iptablesAvailable() then
        return unmanagedResult("iptables not available; no plugin-managed firewall on this device")
    end

    port = tostring(port)
    local parts, missing = {}, {}
    for _, rule in ipairs(inputRules(port, use_webrtc)) do
        local label = ruleLabel(rule)
        local exists = iptablesRuleExists(rule)
        table.insert(parts, label .. ": " .. (exists and "open" or "missing"))
        if not exists then
            table.insert(missing, label)
        end
    end

    local status = table.concat(parts, ", ")
    return {
        managed = true,
        ok = #missing == 0,
        detail = status,
        status = status,
        missing = missing,
    }
end

-- Actively test firewall management: open LocalSend rules, verify INPUT rules,
-- then close them. This is what diagnostics uses.
-- @return table { managed = bool, ok = bool, detail = string, status = string }
function M.selfTestFirewall(port, use_webrtc)
    if not iptablesAvailable() then
        return unmanagedResult("iptables not available; no plugin-managed firewall on this device")
    end

    local open_result = M.openFirewall(port, use_webrtc)
    local check_result = M.checkFirewall(port, use_webrtc)
    local close_result = M.closeFirewall(port)

    local ok = open_result.ok and check_result.ok and close_result.ok
    local detail = "open: "
        .. tostring(open_result.detail)
        .. "; verify: "
        .. tostring(check_result.detail)
        .. "; close: "
        .. tostring(close_result.detail)
    return {
        managed = true,
        ok = ok,
        detail = detail,
        status = check_result.status,
    }
end

return M
