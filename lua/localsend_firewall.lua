-- localsend_firewall.lua
-- iptables firewall management for LocalSend plugin.
-- Owns LocalSend firewall rule definitions, open/close, verification, and self-test.

local constants = require("localsend_constants")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}

-- Kindle firmware (and many other devices) ship iptables under /usr/sbin or
-- /sbin. Prefer those absolute paths so a slim KOReader PATH that omits sbin
-- does not make the plugin silently skip firewall management.
local IPTABLES_CANDIDATES = {
    "/usr/sbin/iptables",
    "/sbin/iptables",
}

-- Cached resolution: nil = not probed, false = missing, string = binary path.
local resolved_iptables = nil
local resolved_check_support = nil

-- Initialize module with dependencies
-- @param d table Dependencies: { Device, util, logger }
function M.init(d)
    deps = d
    resolved_iptables = nil
    resolved_check_support = nil
end

local function normalizeCommandStatus(result, kind, code)
    if result == true then
        return 0
    end
    if type(result) == "number" then
        -- Lua 5.1 returns the wait status while newer Lua versions return the
        -- exit code separately. Test doubles return the exit code directly.
        return result > 255 and math.floor(result / 256) or result
    end
    if result == nil and kind == "exit" and type(code) == "number" then
        return code
    end
    return nil
end

local function commandStatus(cmd)
    return normalizeCommandStatus(os.execute(cmd))
end

local function execute(cmd)
    return commandStatus(cmd) == 0
end

-- Resolve the iptables binary once per init. Absolute paths first, then PATH.
local function resolveIptables()
    if resolved_iptables ~= nil then
        if resolved_iptables == false then
            return nil
        end
        return resolved_iptables
    end

    for _, path in ipairs(IPTABLES_CANDIDATES) do
        if deps.util.pathExists(path) then
            resolved_iptables = path
            return path
        end
    end

    if execute("command -v iptables >/dev/null 2>&1") then
        resolved_iptables = "iptables"
        return "iptables"
    end

    resolved_iptables = false
    return nil
end

local function iptablesAvailable()
    return resolveIptables() ~= nil
end

local function iptablesBin()
    return resolveIptables() or "iptables"
end

-- Probe behavior rather than parsing a version string. A supported -C returns
-- 0 when this harmless rule exists or 1 when it does not; legacy iptables
-- rejects the command itself with status 2.
local function iptablesSupportsCheck()
    if resolved_check_support ~= nil then
        return resolved_check_support
    end
    local command = deps.util.shell_escape({ iptablesBin(), "-C", "INPUT", "-j", "ACCEPT" }) .. " 2>/dev/null"
    local status = commandStatus(command)
    resolved_check_support = status == 0 or status == 1
    return resolved_check_support
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

local function ruleArg(rule_args, name)
    for i, arg in ipairs(rule_args) do
        if arg == name then
            return rule_args[i + 1]
        end
    end
end

local function readCommand(cmd)
    local handle = io.popen(cmd)
    if not handle then
        return nil
    end
    local ok, output = pcall(handle.read, handle, "*a")
    handle:close()
    if not ok or type(output) ~= "string" then
        return nil
    end
    return output
end

-- iptables v1.3.8 on the affected Kindle predates -C/--check. Fall back to
-- the stable, numeric chain listing and match the user-visible rule effect:
-- ACCEPT for the expected protocol and source/destination port.
local function listedRuleExists(rule_args)
    local chain = rule_args[1]
    local proto = ruleArg(rule_args, "-p")
    local target = ruleArg(rule_args, "-j")
    local dport = ruleArg(rule_args, "--dport")
    local sport = ruleArg(rule_args, "--sport")
    if not chain or not proto or not target or (not dport and not sport) then
        return false
    end

    local command = deps.util.shell_escape({ iptablesBin(), "-L", chain, "-n" }) .. " 2>/dev/null"
    local output = readCommand(command)
    if not output then
        return false
    end

    local prefix = "^%s*" .. target:lower() .. "%s+" .. proto:lower() .. "%s+"
    local port = tostring(dport or sport):lower()
    local singular = (dport and "dpt:" or "spt:") .. port
    local plural = (dport and "dpts:" or "spts:") .. port
    for line in output:gmatch("[^\r\n]+") do
        line = line:lower()
        if line:match(prefix) and (line:find(singular, 1, true) or line:find(plural, 1, true)) then
            return true
        end
    end
    return false
end

local function ruleCommand(action, rule_args)
    local cmd_args = { iptablesBin(), action }
    for _, arg in ipairs(rule_args) do
        table.insert(cmd_args, arg)
    end
    return deps.util.shell_escape(cmd_args) .. " 2>/dev/null"
end

local function exactRuleExists(rule_args)
    return commandStatus(ruleCommand("-C", rule_args)) == 0
end

-- Delete only the exact plugin rule. A status of 1 means no matching rule
-- remains; other failures are reported instead of being mistaken for absence.
local function iptablesDelete(rule_args)
    local command = ruleCommand("-D", rule_args)
    for _ = 1, 64 do
        local status = commandStatus(command)
        if status == 1 then
            return true
        end
        if status ~= 0 then
            return false
        end
    end
    deps.logger.warn("[LocalSend] Firewall cleanup stopped after 64 duplicate rules: " .. ruleLabel(rule_args))
    return false
end

-- Check user-visible reachability. Modern iptables supports exact -C matching;
-- legacy implementations use a non-owning chain-listing fallback for diagnostics.
local function iptablesRuleExists(rule_args)
    if iptablesSupportsCheck() then
        return exactRuleExists(rule_args)
    end
    return listedRuleExists(rule_args)
end

-- Add one exact plugin rule. Without -C, first remove exact stale copies and
-- then append once; a similar-looking foreign rule can neither suppress this
-- add nor be removed by plugin cleanup.
-- @return boolean ok, string detail
local function iptablesAddIfMissing(rule_args)
    if iptablesSupportsCheck() then
        if exactRuleExists(rule_args) then
            return true, "exists"
        end
    elseif not iptablesDelete(rule_args) then
        return false, ruleLabel(rule_args)
    end

    if execute(ruleCommand("-A", rule_args)) then
        return true, "added"
    end
    return false, ruleLabel(rule_args)
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
    local failures = {}
    -- Always remove WebRTC rules during cleanup, even when WebRTC is currently off,
    -- so old rules do not survive a setting change.
    for _, rule in ipairs(allRules(port, true)) do
        if not iptablesDelete(rule) then
            table.insert(failures, ruleLabel(rule))
        end
    end
    if #failures > 0 then
        local detail = "failed to delete " .. table.concat(failures, ", ")
        deps.logger.err("[LocalSend] Firewall close failed for port " .. port .. ": " .. detail)
        return { managed = true, ok = false, detail = detail }
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
