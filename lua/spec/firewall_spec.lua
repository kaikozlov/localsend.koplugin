require("busted.runner")()
local helper = require("spec.spec_helper")
local Device = require("device")

-- Tests for iptables firewall management functions

-- Helper to extract rule arguments from a shell-escaped iptables command
-- Commands look like: 'iptables' '-C' 'INPUT' '-p' 'tcp' ... (possibly with 2>/dev/null suffix)
local function extract_rule_key(cmd)
    -- Remove 2>/dev/null suffix if present
    cmd = cmd:gsub(" 2>/dev/null$", "")

    -- Extract all quoted arguments
    local args = {}
    for arg in cmd:gmatch("'([^']*)'") do
        table.insert(args, arg)
    end

    -- Skip 'iptables' and the flag ('-C', '-A', '-D')
    if #args >= 2 and args[1] == "iptables" then
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
    local orig_isKindle, orig_retrieveNetworkInfo

    -- Simulate iptables -C/-A/-D against an in-memory rule set.
    local function simulator(cmd)
        table.insert(os_execute_calls, cmd)
        if cmd:match("'iptables' '%-C'") then
            local rule = extract_rule_key(cmd)
            if rule and iptables_rules[rule] then
                return 0
            end
            return 1
        end
        if cmd:match("'iptables' '%-A'") then
            local rule = extract_rule_key(cmd)
            if rule then
                iptables_rules[rule] = true
            end
            return 0
        end
        if cmd:match("'iptables' '%-D'") then
            local rule = extract_rule_key(cmd)
            if rule then
                iptables_rules[rule] = nil
            end
            return 0
        end
        return 0
    end

    setup(function()
        helper.setup_complete()
        orig_isKindle = Device.isKindle
        orig_retrieveNetworkInfo = Device.retrieveNetworkInfo
    end)

    teardown(function()
        Device.isKindle = orig_isKindle
        Device.retrieveNetworkInfo = orig_retrieveNetworkInfo
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
                    if cmd:match("'iptables' '%-A' 'INPUT' '%-p' 'tcp'") and cmd:match("53317") then
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
                    if cmd:match("'iptables' '%-C'") then
                        table.insert(command_order, "check")
                    elseif cmd:match("'iptables' '%-A'") then
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
                    if cmd:match("'iptables' '%-D'") then
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

                local udp_deletes = 0
                for _, cmd in ipairs(os_execute_calls) do
                    if cmd:match("'iptables' '%-D'") and cmd:match("'%-p' 'udp'") and cmd:match("53317") then
                        udp_deletes = udp_deletes + 1
                    end
                end
                assert.equal(2, udp_deletes, "Should delete both UDP rules")
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
    end)

    describe("when iptables is unavailable", function()
        before_each(function()
            Device.isKindle = function()
                return false
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
