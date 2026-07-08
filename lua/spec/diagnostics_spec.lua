require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")
local Device = require("device")
local NetworkMgr = require("ui/network/manager")

-- Tests for LocalSend troubleshooting/diagnostics flow

describe("LocalSend diagnostics", function()
    local original_io_open
    local original_io_popen
    local original_os_remove
    local original_os_execute

    local orig_pathExists, orig_retrieveNetworkInfo, orig_isKindle, orig_isConnected, orig_isOnline

    local files

    local function fake_file(content)
        local pos = 1
        content = content or ""
        return {
            read = function(_, mode)
                if mode == "*l" then
                    local line = content:match("([^\n]*)", pos)
                    return line
                end
                if mode == "*a" then
                    local s = content:sub(pos)
                    pos = #content + 1
                    return s
                end
                return content
            end,
            seek = function(_, whence, offset)
                offset = offset or 0
                if whence == "end" then
                    pos = #content + 1 + offset
                    return #content
                elseif whence == "set" then
                    pos = offset + 1
                    return pos
                elseif whence == "cur" then
                    pos = pos + offset
                    return pos
                end
                return pos
            end,
            close = function() end,
            write = function() end,
        }
    end

    local function mock_io()
        _G.io.open = function(path, mode)
            if mode and mode:match("w") then
                return fake_file("")
            end
            if files[path] then
                return fake_file(files[path])
            end
            return nil
        end

        _G.io.popen = function(cmd)
            local output = ""
            if cmd:match("curl") then
                output = "200"
            elseif cmd:match("%-%-version") then
                output = "v1.3.0 linux/arm64\n"
            elseif cmd:match("command %-v iptables") then
                output = "/sbin/iptables\n"
            elseif cmd:match("^'df'") then
                output = "Filesystem     1K-blocks   Used Available Use% Mounted on\n" .. "/dev/root        1000000 500000    500000  50% /mnt/us\n"
            elseif cmd:match("iptables") then
                output = ""
            end
            return fake_file(output)
        end

        _G.os.remove = function(path)
            table.insert(helper.state.removed_files, path)
            return true
        end
    end

    local function mock_server_probe(result)
        result = result or { ok = true, detail = "HTTP 200", log_path = "/tmp/localsend_diag_server.out" }
        local diagnostics = require("localsend_diagnostics")
        diagnostics._setServerProbeOverride(function()
            return result
        end)
    end

    local function mock_firewall_probe(result)
        result = result
            or {
                managed = true,
                ok = true,
                detail = "open: iptables rules open; verify: tcp/53317: open, udp/53317: open; close: iptables rules closed",
                status = "tcp/53317: open, udp/53317: open",
            }
        local diagnostics = require("localsend_diagnostics")
        diagnostics._setFirewallProbeOverride(function()
            return result
        end)
    end

    setup(function()
        original_io_open = io.open
        original_io_popen = io.popen
        original_os_remove = os.remove
        original_os_execute = os.execute
        orig_pathExists = util.pathExists
        orig_retrieveNetworkInfo = Device.retrieveNetworkInfo
        orig_isKindle = Device.isKindle
        orig_isConnected = NetworkMgr.isConnected
        orig_isOnline = NetworkMgr.isOnline
    end)

    teardown(function()
        _G.io.open = original_io_open
        _G.io.popen = original_io_popen
        _G.os.remove = original_os_remove
        _G.os.execute = original_os_execute
        util.pathExists = orig_pathExists
        Device.retrieveNetworkInfo = orig_retrieveNetworkInfo
        Device.isKindle = orig_isKindle
        NetworkMgr.isConnected = orig_isConnected
        NetworkMgr.isOnline = orig_isOnline
    end)

    -- Apply device/network/util overrides for a scenario (replaces the old
    -- setup_complete opts, which the real-KOReader helper ignores).
    local function apply_mocks(opts)
        opts = opts or {}
        Device.retrieveNetworkInfo = function()
            return opts.network_info or "wlan0: 192.168.1.100"
        end
        Device.isKindle = function()
            return opts.is_kindle ~= false
        end
        NetworkMgr.isConnected = function()
            return opts.is_connected ~= false
        end
        NetworkMgr.isOnline = function()
            return opts.is_online == true
        end
        util.pathExists = function(path)
            if files[path] ~= nil then
                return true
            end
            if path == "/tmp/localsend_koreader.pid" then
                return true
            end
            if path == "/proc/12345" then
                return true
            end
            return orig_pathExists(path)
        end
    end

    before_each(function()
        files = {
            ["/tmp/localsend_koreader.pid"] = "12345\n",
            ["/proc/12345/cmdline"] = "/tmp/koreader/plugins/localsend.koplugin/localsend\0" .. "recv\0-d\0/mnt/us/documents\0",
            ["/tmp/localsend_server.out"] = "backend log line\n",
            ["/tmp/localsend_send.out"] = "send log line\n",
            ["/tmp/localsend_transfers.log"] = '{"filename":"book.epub"}\n',
        }
        helper.before_each()
        helper.setup_complete()
        apply_mocks({ is_kindle = true, is_connected = true, is_online = false })
        mock_io()
        mock_server_probe()
        mock_firewall_probe()
    end)

    it("formats a report with plugin, network, server, firewall, and log details", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.truthy(report:match("LocalSend Diagnostics"))
        assert.truthy(report:match("Version:"))
        assert.truthy(report:match("Architecture:"))
        assert.truthy(report:match("LAN connected"))
        assert.truthy(report:match("Internet reachable"))
        assert.truthy(report:match("wlan0: 192%.168%.1%.100"))
        assert.truthy(report:match("Server self%-test"))
        assert.truthy(report:match("Temporary server starts and local API responds"))
        assert.truthy(report:match("Result: HTTP 200"))
        assert.truthy(report:match("tcp/53317"))
        assert.truthy(report:match("backend log line"))
        assert.truthy(report:match("book%.epub"))
        assert.truthy(report:match("Binary is executable / runs"))
        assert.truthy(report:match("Binary arch:"))
        assert.truthy(report:match("Save directory space: 488%.3 MB free"))
        assert.truthy(report:match("TLS certificate: not generated yet"))
        assert.truthy(report:match("review the report before posting publicly"))
    end)

    it("does not include the PIN value in diagnostics", function()
        local instance = helper.create_instance()
        instance.pin = "9876"
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.falsy(report:match("9876"))
        assert.truthy(report:match("PIN enabled: yes"))
    end)

    it("reports server self-test failures", function()
        mock_server_probe({
            ok = false,
            detail = "failed (curl: not found)",
            log_path = "/tmp/localsend_diag_server.out",
        })
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.collect(instance)

        assert.is_false(report.server.self_test.ok)
        assert.truthy(report.server.self_test.detail:match("failed"))
        assert.truthy(report.server.self_test.detail:match("curl: not found"))
    end)

    it("shows diagnostics in a TextViewer", function()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend diagnostics", dialog.title)
        assert.truthy(dialog.text:match("LocalSend Diagnostics"))
    end)

    it("saves the diagnostics report to the plugin cache dir", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local path = diagnostics.saveReportText("hello report")

        assert.truthy(path)
        assert.truthy(path:match("cache/localsend/localsend%-report%.txt$"))
    end)

    it("shows the saved path in the diagnostics view", function()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.truthy(dialog.text:match("Saved to:"))
    end)

    it("shows bug report guidance with diagnostics", function()
        local instance = helper.create_instance()

        instance:showBugReportInfo()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend bug report", dialog.title)
        assert.truthy(dialog.text:match("Steps to reproduce"))
        assert.truthy(dialog.text:match("LocalSend Diagnostics"))
    end)

    it("does not prompt for Wi-Fi when diagnostics run offline", function()
        apply_mocks({ is_connected = false, is_online = false })
        mock_io()
        mock_server_probe()
        mock_firewall_probe()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("LAN connected"))
        assert.truthy(dialog.text:match("Server self%-test"))
    end)

    it("shows recent backend log separately", function()
        local instance = helper.create_instance()

        instance:showRecentBackendLog()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend backend log", dialog.title)
        assert.truthy(dialog.text:match("backend log line"))
    end)

    it("diagnostics summary reports all checks passed when healthy", function()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend diagnostics", dialog.title)
        assert.truthy(dialog.text:match("all %d+ checks passed"))
        assert.truthy(dialog.text:match("LAN connected"))
        assert.truthy(dialog.text:match("Server self%-test"))
        assert.truthy(dialog.text:match("53317")) -- computer-firewall tip
    end)

    it("diagnostics summary flags missing network with hints", function()
        apply_mocks({ is_connected = false, is_online = false })
        mock_io()
        mock_server_probe()
        mock_firewall_probe()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("to fix")) -- "N to fix" summary
        assert.truthy(dialog.text:match("Enable Wi%-Fi")) -- LAN hint
        assert.truthy(dialog.text:match("Server self%-test"))
    end)

    it("flags a binary that exists but cannot run", function()
        -- Simulate a non-executable / wrong-architecture binary: --version prints nothing.
        _G.io.popen = function(cmd)
            if cmd:match("curl") then
                return fake_file("200")
            end
            if cmd:match("%-%-version") then
                return fake_file("")
            end
            if cmd:match("iptables") then
                return fake_file("")
            end
            return fake_file("")
        end
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.truthy(report:match("Binary is executable / runs"))
        assert.truthy(report:match("wrong architecture package")) -- only shown on failure
    end)

    it("treats shell exec-error output as a binary that cannot run", function()
        -- A wrong-arch binary doesn't print nothing: the shell prints an error to
        -- stderr, which commandOutput captures via 2>&1. That must not count as
        -- the binary running.
        _G.io.popen = function(cmd)
            if cmd:match("curl") then
                return fake_file("200")
            end
            if cmd:match("%-%-version") then
                return fake_file("sh: /tmp/koreader/plugins/localsend.koplugin/localsend: " .. "cannot execute binary file: Exec format error\n")
            end
            if cmd:match("iptables") then
                return fake_file("")
            end
            return fake_file("")
        end
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.collect(instance)

        assert.is_false(report.binary_runs)
        assert.is_nil(report.binary_arch)
        assert.is_false(report.arch_mismatch)
    end)

    it("does not flag a mismatch for dev builds reporting raw GOARCH", function()
        -- A local `go build` without ldflags reports GOARCH "arm", which is not in
        -- the release tag vocabulary (armv7/arm64/arm-legacy) and must not be
        -- compared against the device arch.
        _G.io.popen = function(cmd)
            if cmd:match("curl") then
                return fake_file("200")
            end
            if cmd:match("%-%-version") then
                return fake_file("v1.3.0 linux/arm\n")
            end
            if cmd:match("iptables") then
                return fake_file("")
            end
            return fake_file("")
        end
        local instance = helper.create_instance()
        instance.getDeviceArch = function()
            return "armv7"
        end
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.collect(instance)

        assert.is_true(report.binary_runs)
        assert.equals("arm", report.binary_arch)
        assert.is_false(report.arch_mismatch)
    end)

    it("flags a binary/device architecture mismatch", function()
        -- --version reports arm64; force the device arch to arm-legacy to trigger a mismatch.
        local instance = helper.create_instance()
        instance.getDeviceArch = function()
            return "arm-legacy"
        end
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.collect(instance)
        assert.is_true(report.arch_mismatch)
        assert.equals("arm64", report.binary_arch)

        local text = diagnostics.formatReport(report)
        assert.truthy(text:match("does not match the device"))
    end)

    it("saves the bug report and shows the saved path", function()
        local instance = helper.create_instance()

        instance:showBugReportInfo()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("Saved to:"))
        assert.truthy(dialog.text:match("localsend%-bugreport%.txt"))
    end)

    it("includes a crash.log tail in the bug report", function()
        local crash_log = require("datastorage"):getFullDataDir() .. "/crash.log"
        files[crash_log] = "some crash line\n"
        local instance = helper.create_instance()

        instance:showBugReportInfo()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("crash%.log"))
        assert.truthy(dialog.text:match("some crash line"))
    end)

    it("discovery test attributes multicast failure", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, { loopback = false, peers = 0 })

        assert.truthy(text:match("NOT working"))
        assert.truthy(text:match("AP/client isolation"))
    end)

    it("discovery test attributes zero peers to the other side", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, { loopback = true, peers = 0 })

        assert.truthy(text:match("Multicast works on this device"))
        assert.truthy(text:match("no other LocalSend devices responded"))
    end)

    it("discovery test reports healthy when peers are seen", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, { loopback = true, peers = 2 })

        assert.truthy(text:match("Discovery is healthy"))
    end)

    it("discovery test treats peers>0 as healthy even when loopback failed", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, { loopback = false, peers = 2 })

        assert.truthy(text:match("Discovery is healthy"))
        assert.falsy(text:match("NOT working"))
    end)

    it("discovery test shows the peer breakdown and this device's IP", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, {
            loopback = true,
            peers = 2,
            udp_peers = 1,
            register_peers = 2,
            local_ips = { "192.168.1.100" },
        })

        assert.truthy(text:match("UDP announce: 1"))
        assert.truthy(text:match("HTTP register: 2"))
        assert.truthy(text:match("This device's IP: 192%.168%.1%.100"))
    end)

    it("discovery test surfaces a register listener bind failure", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, {
            loopback = true,
            peers = 0,
            register_bind_error = "address already in use",
        })

        assert.truthy(text:match("Register listener: could not bind"))
        assert.truthy(text:match("address already in use"))
    end)

    it("discovery poll parses the nettest JSON output end to end", function()
        -- Regression test: this exercises readNetTestResult against the real
        -- KOReader json module, which a formatter-only test cannot catch.
        files["/tmp/localsend_nettest.json"] = '{"loopback":true,"bind_error":"","peers":1,"local_ips":["192.168.1.100"],"duration_ms":3000}'
        local instance = helper.create_instance()
        local closed = false
        instance.closeFirewall = function()
            closed = true
        end
        local diagnostics = require("localsend_diagnostics")

        diagnostics._pollDiscoveryTest(instance, 0, os.time() + 5)

        assert.is_true(closed)
        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend discovery test", dialog.title)
        assert.truthy(dialog.text:match("Discovery is healthy"))
    end)

    it("kills a stale nettest process when the poll times out", function()
        files["/tmp/localsend_nettest.pid"] = "4242\n"
        helper.mock_os_execute()
        local instance = helper.create_instance()
        instance.closeFirewall = function() end
        local diagnostics = require("localsend_diagnostics")

        -- Deadline already passed: the poll must give up and clean up the probe.
        diagnostics._pollDiscoveryTest(instance, 0, os.time() - 1)

        assert.truthy(helper.find_execute_call("kill 4242"))
        assert.is_not_nil(helper.find_notification("timed out"))
    end)

    it("discovery test lists aliases of responding devices", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, {
            loopback = true,
            peers = 2,
            seen_aliases = { "Kai's Phone", "MacBook" },
        })

        assert.truthy(text:match("Devices that responded"))
        assert.truthy(text:match("Kai's Phone"))
        assert.truthy(text:match("MacBook"))
    end)

    it("discovery test omits the aliases line when none were seen", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local text = diagnostics.formatDiscoveryResult(instance, { loopback = true, peers = 0 })

        assert.falsy(text:match("Devices that responded"))
    end)

    describe("diagnostics send-side coverage", function()
        local state

        before_each(function()
            state = require("localsend_state")
            state.ServerState.last_send = nil
        end)
        after_each(function()
            state.ServerState.last_send = nil
        end)

        it("flags a recent failed send with a hint", function()
            state.ServerState.last_send = {
                success = false,
                message = "Device is not running LocalSend",
                time = os.time(),
            }
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local text = diagnostics.getReportText(instance)

            assert.truthy(text:match("Last send"))
            assert.truthy(text:match("Device is not running LocalSend"))
            assert.truthy(text:match("to fix"))
            assert.truthy(text:match("recipient is not running LocalSend")) -- hint
        end)

        it("reports a recent successful send", function()
            state.ServerState.last_send = {
                success = true,
                message = "Sent book.epub to Phone",
                time = os.time(),
            }
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local text = diagnostics.getReportText(instance)

            assert.truthy(text:match("Last send"))
            assert.truthy(text:match("Sent book%.epub to Phone"))
            assert.falsy(text:match("to fix"))
        end)

        it("omits send status when no send has run this session", function()
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local text = diagnostics.getReportText(instance)

            assert.falsy(text:match("Last send"))
            assert.falsy(text:match("skipped"))
        end)
    end)

    it("diagnostics suggests Test discovery when all checks pass", function()
        local state = require("localsend_state")
        state.ServerState.last_send = nil
        finally(function()
            state.ServerState.last_send = nil
        end)
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("Test discovery"))
        assert.truthy(dialog.text:match("can't find"))
    end)
end)
