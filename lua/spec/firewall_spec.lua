require("busted.runner")()
local helper = require("spec.spec_helper")
local Device = require("device")

-- Tests for iptables firewall management functions

-- Helper to extract rule arguments from a shell-escaped iptables command
-- Commands look like: 'iptables' '-C' 'INPUT' ... or '/usr/sbin/iptables' '-C' ...
local function extract_rule_key(cmd)
    -- Remove 2>/dev/null suffix if present
    cmd = cmd:gsub(" 2>/dev/null$", "")

    -- Extract all quoted arguments
    local args = {}
    for arg in cmd:gmatch("'([^']*)'") do
        table.insert(args, arg)
    end

    -- Skip the iptables binary path and the flag ('-C', '-A', '-D')
    if #args >= 2 and args[1]:match("iptables$") then
        local rule_parts = {}
        for i = 3, #args do
            table.insert(rule_parts, args[i])
        end
        return table.concat(rule_parts, " ")
    end

    return nil
end

describe("Firewall Management", function()
    local iptables_rules
    local os_execute_calls
    local orig_isKindle, orig_retrieveNetworkInfo, orig_pathExists

    -- Simulate iptables -C/-A/-D against an in-memory rule set.
    local function simulator(cmd)
        table.insert(os_execute_calls, cmd)
        if cmd:match("iptables' '%-C'") then
            local rule = extract_rule_key(cmd)
            if rule and iptables_rules[rule] then
                return 0
            end
            return 1
        end
        if cmd:match("iptables' '%-A'") then
            local rule = extract_rule_key(cmd)
            if rule then
                iptables_rules[rule] = true
            end
            return 0
        end
        if cmd:match("iptables' '%-D'") then
            local rule = extract_rule_key(cmd)
            if rule and iptables_rules[rule] then
                iptables_rules[rule] = nil
                return 0
            end
            return 1
        end
        return 0
    end

    setup(function()
        helper.setup_complete()
        orig_isKindle = Device.isKindle
        orig_retrieveNetworkInfo = Device.retrieveNetworkInfo
        orig_pathExists = require("util").pathExists
    end)

    teardown(function()
        Device.isKindle = orig_isKindle
        Device.retrieveNetworkInfo = orig_retrieveNetworkInfo
        require("util").pathExists = orig_pathExists
    end)

    before_each(function()
        helper.before_each()
        iptables_rules = {}
        os_execute_calls = {}
        Device.isKindle = function()
            return false
        end
        Device.retrieveNetworkInfo = function()
            return "WiFi"
        end
        -- Keep default tests independent of tools installed in the container.
        -- They exercise the PATH resolver through the command simulator.
        require("util").pathExists = function(path)
            if path == "/usr/sbin/iptables" or path == "/sbin/iptables" then
                return false
            end
            return orig_pathExists(path)
        end
        helper.mock_os_execute(simulator)
    end)

    describe("on Kindle devices", function()
        before_each(function()
            Device.isKindle = function()
                return true
            end
        end)

        describe("openFirewall", function()
            it("onExit removes rules opened for sender-only use", function()
                local instance = helper.create_instance()
                instance.port = "53317"
                instance:openFirewall()
                instance.isRunning = function()
                    return false
                end

                instance:onExit()

                assert.same({}, iptables_rules)
            end)
            it("adds TCP rules for the configured port", function()
                local instance = helper.create_instance()
                instance.port = "53317"

                instance:openFirewall()

                -- Should have added INPUT and OUTPUT TCP rules
                assert.is_not_nil(iptables_rules["INPUT -p tcp --dport 53317 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT"])
                assert.is_not_nil(iptables_rules["OUTPUT -p tcp --sport 53317 -m conntrack --ctstate ESTABLISHED -j ACCEPT"])
            end)

            it("adds UDP rules for discovery", function()
                local instance = helper.create_instance()
                instance.port = "53317"

                instance:openFirewall()

                assert.is_not_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])
                assert.is_not_nil(iptables_rules["OUTPUT -p udp --sport 53317 -j ACCEPT"])
            end)

            it("adds WebRTC UDP port range when enabled", function()
                local instance = helper.create_instance()
                instance.port = "53317"
                instance.use_webrtc = true

                instance:openFirewall()

                assert.is_not_nil(iptables_rules["INPUT -p udp --dport 50000:50100 -j ACCEPT"])
                assert.is_not_nil(iptables_rules["OUTPUT -p udp --sport 50000:50100 -j ACCEPT"])
            end)

            it("does not add WebRTC rules when disabled", function()
                local instance = helper.create_instance()
                instance.port = "53317"
                instance.use_webrtc = false

                instance:openFirewall()

                assert.is_nil(iptables_rules["INPUT -p udp --dport 50000:50100 -j ACCEPT"])
                assert.is_nil(iptables_rules["OUTPUT -p udp --sport 50000:50100 -j ACCEPT"])
            end)

            it("does not add duplicate rules (idempotent)", function()
                local instance = helper.create_instance()
                instance.port = "53317"

                -- Pre-add a rule
                iptables_rules["INPUT -p tcp --dport 53317 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT"] = true

                local add_count = 0
                local base_execute = os.execute
                _G.os.execute = function(cmd)
                    if cmd:match("iptables' '%-A' 'INPUT' '%-p' 'tcp'") and cmd:match("53317") then
                        add_count = add_count + 1
                    end
                    return base_execute(cmd)
                end

                instance:openFirewall()

                -- Should not have tried to add the rule again (check should have found it)
                assert.equal(0, add_count, "Should not add duplicate rule")
            end)

            it("checks rule existence (-C) before adding (-A)", function()
                local instance = helper.create_instance()
                instance.port = "53317"

                local command_order = {}
                local base_execute = os.execute
                _G.os.execute = function(cmd)
                    if cmd:match("iptables' '%-C'") then
                        table.insert(command_order, "check")
                    elseif cmd:match("iptables' '%-A'") then
                        table.insert(command_order, "add")
                    end
                    return base_execute(cmd)
                end

                instance:openFirewall()

                -- Find first check and first add
                local first_check_idx = nil
                local first_add_idx = nil
                for i, cmd_type in ipairs(command_order) do
                    if cmd_type == "check" and not first_check_idx then
                        first_check_idx = i
                    elseif cmd_type == "add" and not first_add_idx then
                        first_add_idx = i
                    end
                end

                assert.is_not_nil(first_check_idx, "Should have called iptables -C")
                assert.is_not_nil(first_add_idx, "Should have called iptables -A")
                assert.is_true(first_check_idx < first_add_idx, "Check (-C) should come before add (-A)")
            end)

            it("rejects invalid port", function()
                local instance = helper.create_instance()
                instance.port = "invalid"

                -- Clear calls
                os_execute_calls = {}

                instance:openFirewall()

                -- Should not have called any iptables commands
                local iptables_calls = 0
                for _, cmd in ipairs(os_execute_calls) do
                    if cmd:match("iptables") then
                        iptables_calls = iptables_calls + 1
                    end
                end
                assert.equal(0, iptables_calls, "Should not call iptables with invalid port")
            end)
        end)

        describe("selfTestFirewall", function()
            it("opens, verifies, and closes the LocalSend rules", function()
                local instance = helper.create_instance()
                instance.port = "53317"

                local result = instance:testFirewall()

                assert.is_true(result.managed)
                assert.is_true(result.ok)
                assert.truthy(result.detail:match("open: iptables rules open"))
                assert.truthy(result.detail:match("verify: tcp/53317: open, udp/53317: open"))
                assert.truthy(result.detail:match("close: iptables rules closed"))
                assert.is_nil(iptables_rules["INPUT -p tcp --dport 53317 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT"])
                assert.is_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])
            end)
        end)

        describe("closeFirewall", function()
            it("removes TCP rules", function()
                -- Pre-add rules
                iptables_rules["INPUT -p tcp --dport 53317 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT"] = true
                iptables_rules["OUTPUT -p tcp --sport 53317 -m conntrack --ctstate ESTABLISHED -j ACCEPT"] = true

                local instance = helper.create_instance()
                instance.port = "53317"

                instance:closeFirewall()

                -- Check that delete commands were issued
                local found_delete = false
                for _, cmd in ipairs(os_execute_calls) do
                    if cmd:match("iptables' '%-D'") then
                        found_delete = true
                        break
                    end
                end
                assert.is_true(found_delete, "Should issue delete commands")
            end)

            it("removes UDP rules", function()
                iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"] = true
                iptables_rules["OUTPUT -p udp --sport 53317 -j ACCEPT"] = true

                local instance = helper.create_instance()
                instance.port = "53317"

                instance:closeFirewall()

                assert.is_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])
                assert.is_nil(iptables_rules["OUTPUT -p udp --sport 53317 -j ACCEPT"])
            end)

            it("attempts to remove WebRTC rules (ignoring errors)", function()
                local instance = helper.create_instance()
                instance.port = "53317"

                instance:closeFirewall()

                -- Should attempt to remove WebRTC rules with 2>/dev/null
                local found_webrtc_cleanup = false
                for _, cmd in ipairs(os_execute_calls) do
                    if cmd:match("50000:50100") and cmd:match("2>/dev/null") then
                        found_webrtc_cleanup = true
                        break
                    end
                end
                assert.is_true(found_webrtc_cleanup, "Should attempt WebRTC cleanup")
            end)

            it("rejects invalid port", function()
                local instance = helper.create_instance()
                instance.port = "99999" -- Out of range

                os_execute_calls = {}

                instance:closeFirewall()

                local iptables_calls = 0
                for _, cmd in ipairs(os_execute_calls) do
                    if cmd:match("iptables") then
                        iptables_calls = iptables_calls + 1
                    end
                end
                assert.equal(0, iptables_calls, "Should not call iptables with invalid port")
            end)
        end)
    end)

    describe("on non-Kindle devices with iptables", function()
        before_each(function()
            Device.isKindle = function()
                return false
            end
        end)

        it("openFirewall configures iptables", function()
            local instance = helper.create_instance()
            instance.port = "53317"

            instance:openFirewall()

            assert.is_not_nil(iptables_rules["INPUT -p tcp --dport 53317 -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT"])
            assert.is_not_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])
        end)

        it("closeFirewall removes iptables rules", function()
            local instance = helper.create_instance()
            instance.port = "53317"
            iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"] = true

            instance:closeFirewall()

            assert.is_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])
        end)

        it("falls back to PATH once and caches the resolved command", function()
            local instance = helper.create_instance()
            instance.port = "53317"

            instance:openFirewall()
            instance:openFirewall()

            local probes = 0
            for _, cmd in ipairs(os_execute_calls) do
                if cmd:match("command %-v iptables") then
                    probes = probes + 1
                end
            end
            assert.equal(1, probes)
        end)
    end)

    describe("when iptables is unavailable", function()
        local orig_pathExists

        before_each(function()
            Device.isKindle = function()
                return false
            end
            local util = require("util")
            orig_pathExists = util.pathExists
            util.pathExists = function(path)
                if path == "/usr/sbin/iptables" or path == "/sbin/iptables" then
                    return false
                end
                return orig_pathExists(path)
            end
        end)

        after_each(function()
            if orig_pathExists then
                require("util").pathExists = orig_pathExists
            end
        end)

        it("reports unmanaged without changing rules", function()
            local base_execute = os.execute
            _G.os.execute = function(cmd)
                table.insert(os_execute_calls, cmd)
                if cmd:match("command %-v iptables") then
                    return 1
                end
                return base_execute(cmd)
            end
            local instance = helper.create_instance()
            instance.port = "53317"

            local result = instance:openFirewall()

            assert.is_false(result.managed)
            assert.is_true(result.ok)
            assert.is_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])
        end)
    end)

    describe("when iptables is only available at an absolute path", function()
        local orig_pathExists
        local available_path

        before_each(function()
            Device.isKindle = function()
                return false
            end
            available_path = "/usr/sbin/iptables"
            local util = require("util")
            orig_pathExists = util.pathExists
            util.pathExists = function(path)
                if path == "/usr/sbin/iptables" or path == "/sbin/iptables" then
                    return path == available_path
                end
                return orig_pathExists(path)
            end
            helper.mock_os_execute(function(cmd)
                if cmd:match("command %-v iptables") then
                    table.insert(os_execute_calls, cmd)
                    return 1
                end
                return simulator(cmd)
            end)
        end)

        after_each(function()
            if orig_pathExists then
                require("util").pathExists = orig_pathExists
            end
        end)

        it("opens firewall using /usr/sbin/iptables", function()
            local instance = helper.create_instance()
            instance.port = "53317"

            local result = instance:openFirewall()

            assert.is_true(result.managed)
            assert.is_true(result.ok)
            assert.is_not_nil(iptables_rules["INPUT -p udp --dport 53317 -j ACCEPT"])

            local used_absolute = false
            for _, cmd in ipairs(os_execute_calls) do
                if cmd:match("/usr/sbin/iptables") then
                    used_absolute = true
                    break
                end
            end
            assert.is_true(used_absolute)
        end)

        it("falls back to /sbin/iptables", function()
            available_path = "/sbin/iptables"
            local instance = helper.create_instance()
            instance.port = "53317"

            local result = instance:openFirewall()

            assert.is_true(result.managed)
            assert.is_true(result.ok)
            local used_sbin = false
            for _, cmd in ipairs(os_execute_calls) do
                if cmd:match("'/sbin/iptables'") then
                    used_sbin = true
                    break
                end
            end
            assert.is_true(used_sbin)
        end)
    end)

    describe("with legacy iptables that does not support -C", function()
        local orig_pathExists, orig_io_popen
        local rule_counts
        local foreign_lines
        local delete_failure
        local saw_list

        local function listingForChain(chain)
            local lines = {
                "Chain " .. chain .. " (policy ACCEPT)",
                "target prot opt source destination",
            }
            for _, foreign in ipairs(foreign_lines) do
                if foreign.chain == chain then
                    table.insert(lines, foreign.line)
                end
            end
            for rule, count in pairs(rule_counts) do
                if count > 0 and rule:match("^" .. chain .. " ") then
                    local proto = rule:match("%-p ([^ ]+)") or "all"
                    local dport = rule:match("%-%-dport ([^ ]+)")
                    local sport = rule:match("%-%-sport ([^ ]+)")
                    local port_detail = ""
                    if dport then
                        local marker = dport:match(":") and "dpts:" or "dpt:"
                        port_detail = " " .. proto .. " " .. marker .. dport
                    elseif sport then
                        local marker = sport:match(":") and "spts:" or "spt:"
                        port_detail = " " .. proto .. " " .. marker .. sport
                    end
                    table.insert(lines, "ACCEPT " .. proto .. " -- 0.0.0.0/0 0.0.0.0/0" .. port_detail)
                end
            end
            return table.concat(lines, "\n") .. "\n"
        end

        before_each(function()
            Device.isKindle = function()
                return true
            end
            rule_counts = {}
            foreign_lines = {}
            delete_failure = nil
            saw_list = false

            local util = require("util")
            orig_pathExists = util.pathExists
            util.pathExists = function(path)
                if path == "/usr/sbin/iptables" then
                    return true
                end
                if path == "/sbin/iptables" then
                    return false
                end
                return orig_pathExists(path)
            end

            orig_io_popen = io.popen
            _G.io.popen = function(cmd)
                local chain = cmd:match("iptables' '%-L' '([^']+)'")
                if chain then
                    saw_list = true
                    local output = listingForChain(chain)
                    return {
                        read = function()
                            return output
                        end,
                        close = function() end,
                    }
                end
                return orig_io_popen(cmd)
            end

            helper.mock_os_execute(function(cmd)
                table.insert(os_execute_calls, cmd)
                if cmd:match("command %-v iptables") then
                    return 1
                end
                if cmd:match("iptables' '%-C'") then
                    return 2 -- iptables v1.3.8: unknown command
                end
                if cmd:match("iptables' '%-A'") then
                    local rule = extract_rule_key(cmd)
                    rule_counts[rule] = (rule_counts[rule] or 0) + 1
                    return 0
                end
                if cmd:match("iptables' '%-D'") then
                    local rule = extract_rule_key(cmd)
                    if delete_failure and rule == delete_failure then
                        return 2
                    end
                    if (rule_counts[rule] or 0) > 0 then
                        rule_counts[rule] = rule_counts[rule] - 1
                        return 0
                    end
                    return 1
                end
                return 0
            end)
        end)

        after_each(function()
            if orig_pathExists then
                require("util").pathExists = orig_pathExists
            end
            if orig_io_popen then
                _G.io.popen = orig_io_popen
            end
        end)

        it("uses rule listing fallback and remains idempotent", function()
            local instance = helper.create_instance()
            instance.port = "53317"
            instance.use_webrtc = true

            instance:openFirewall()
            instance:openFirewall()
            local result = require("localsend_firewall").checkFirewall("53317", true)

            assert.is_true(saw_list)
            assert.equal(1, rule_counts["INPUT -p udp --dport 53317 -j ACCEPT"])
            assert.equal(1, rule_counts["OUTPUT -p udp --sport 53317 -j ACCEPT"])
            assert.equal(1, rule_counts["INPUT -p udp --dport 50000:50100 -j ACCEPT"])
            assert.is_true(result.managed)
            assert.is_true(result.ok)
            assert.matches("tcp/53317: open", result.detail)
            assert.matches("udp/53317: open", result.detail)
            assert.matches("udp/50000:50100: open", result.detail)
        end)

        it("does not mistake a restricted foreign rule for its own rule", function()
            table.insert(foreign_lines, {
                chain = "INPUT",
                line = "ACCEPT udp -- 192.0.2.1 0.0.0.0/0 udp dpt:53317",
            })
            local instance = helper.create_instance()
            instance.port = "53317"

            local result = instance:openFirewall()

            assert.is_true(result.ok)
            assert.equal(1, rule_counts["INPUT -p udp --dport 53317 -j ACCEPT"])
        end)

        it("removes every duplicate left by older plugin runs", function()
            local input_rule = "INPUT -p udp --dport 53317 -j ACCEPT"
            rule_counts[input_rule] = 3
            local instance = helper.create_instance()
            instance.port = "53317"

            instance:closeFirewall()

            assert.equal(0, rule_counts[input_rule])
        end)

        it("reports an exact-rule cleanup failure", function()
            local input_rule = "INPUT -p udp --dport 53317 -j ACCEPT"
            rule_counts[input_rule] = 1
            delete_failure = input_rule
            local instance = helper.create_instance()
            instance.port = "53317"

            local result = instance:closeFirewall()

            assert.is_false(result.ok)
            assert.matches("failed to delete", result.detail)
        end)
    end)

    describe("iptables command injection protection", function()
        before_each(function()
            Device.isKindle = function()
                return true
            end
            os_execute_calls = {}
        end)

        it("properly escapes ports with shell metacharacters", function()
            local instance = helper.create_instance()
            -- Simulate a malicious port that somehow bypassed validation
            -- With shell_escape, these characters get quoted safely
            local malicious_port = "53317; rm -rf /"
            instance.port = malicious_port

            os_execute_calls = {}
            instance:openFirewall()

            -- With isValidPort check, no commands should be issued
            -- But even if they were, shell_escape would quote them safely
            for _, cmd in ipairs(os_execute_calls) do
                -- If any command was issued, the dangerous characters should be quoted
                if cmd:match("iptables") then
                    -- The malicious string should be inside quotes, not executable
                    assert.is_not.match(cmd, "[^']rm %-rf")
                end
            end
        end)

        it("properly escapes backticks", function()
            local instance = helper.create_instance()
            instance.port = "53317`whoami`"

            os_execute_calls = {}
            instance:openFirewall()

            -- isValidPort should reject this, but shell_escape also protects
            for _, cmd in ipairs(os_execute_calls) do
                if cmd:match("iptables") then
                    -- Backticks should be inside single quotes (safely escaped)
                    assert.is_not.match(cmd, "[^']`whoami`")
                end
            end
        end)

        it("properly escapes $() command substitution", function()
            local instance = helper.create_instance()
            instance.port = "$(cat /etc/passwd)"

            os_execute_calls = {}
            instance:openFirewall()

            -- isValidPort should reject this
            for _, cmd in ipairs(os_execute_calls) do
                if cmd:match("iptables") then
                    assert.is_not.match(cmd, "[^']%$%(")
                end
            end
        end)
    end)
end)
