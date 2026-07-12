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

    local function finish_async_report()
        local task = helper.state.scheduled_tasks[#helper.state.scheduled_tasks]
        assert.is_not_nil(task, "expected report collection to be scheduled")
        task.callback()
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
        -- Most UI tests only care about rendering an already collected report.
        -- Keep those synchronous; lifecycle-specific tests clear this override
        -- and exercise the real orchestration below.
        local diagnostics = require("localsend_diagnostics")
        diagnostics._setAsyncCollectOverride(function(instance, callback)
            callback(diagnostics.collect(instance))
        end)
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
        assert.truthy(report:match("Receiver lifecycle test"))
        assert.truthy(report:match("Real receiver starts and local API responds"))
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
        files["/proc/12345/cmdline"] = "/tmp/localsend\0recv\0-p\09876\0"
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.falsy(report:match("9876"))
        assert.truthy(report:match("PIN enabled: yes"))
    end)

    it("redacts SSID and MAC address from the public report", function()
        apply_mocks({
            network_info = 'Interface: wlan0\nMAC: AA:BB:CC:DD:EE:FF\nSSID: "Home Network"\nIPv4: 192.168.1.100',
        })
        mock_io()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.falsy(report:match("AA:BB:CC"))
        assert.falsy(report:match("Home Network"))
        assert.truthy(report:match("MAC: %[redacted%]"))
        assert.truthy(report:match('SSID: "%[redacted%]"'))
        assert.truthy(report:match("192%.168%.1%.100"))
    end)

    it("includes the KOReader and runtime environment", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.truthy(report:match("KOReader revision:"))
        assert.truthy(report:match("Platform: Kindle"))
        assert.truthy(report:match("Kernel:"))
        assert.truthy(report:match("Runtime architecture:"))
        assert.truthy(report:match("Recovery mode: no"))
    end)

    it("reports whether the receiver is listening beyond loopback", function()
        files["/proc/net/tcp"] = "  sl  local_address rem_address   st\n   0: 00000000:D045 00000000:0000 0A\n"
        files["/proc/net/udp"] = "  sl  local_address rem_address   st\n   1: 00000000:D045 00000000:0000 07\n"
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.collect(instance)

        assert.is_true(report.server.listeners.ok)
        assert.truthy(report.server.listeners.detail:match("tcp: all interfaces"))
        assert.truthy(report.server.listeners.detail:match("udp: all interfaces"))
    end)

    it("includes timestamped power and network lifecycle evidence", function()
        files["/tmp/localsend_lifecycle.log"] = "2026-07-12T12:00:00-0500\tsuspend\n2026-07-12T12:00:03-0500\tnetwork_disconnected\n"
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.truthy(report:match("Receiver/power/network lifecycle"))
        assert.truthy(report:match("suspend"))
        assert.truthy(report:match("network_disconnected"))
    end)

    it("uses retained raw output after the temporary send log is removed", function()
        files["/tmp/localsend_send.out"] = nil
        local state = require("localsend_state")
        state.ServerState.last_send = {
            success = false,
            message = "Send failed",
            time = os.time(),
            raw_output = "raw tcp failure detail",
            exit_code = 1,
            error_category = "connection",
        }
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.truthy(report:match("raw tcp failure detail"))
        assert.truthy(report:match("Exit code: 1"))
        assert.truthy(report:match("Category: connection"))
    end)

    it("loads retained send evidence after a KOReader restart", function()
        files["/tmp/localsend_send.out"] = nil
        files["/tmp/localsend_last_send.json"] =
            [[{"success":false,"message":"Send failed","time":1770000000,"raw_output":"persisted raw failure","exit_code":1,"error_category":"timeout"}]]
        require("localsend_state").ServerState.last_send = nil
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")

        local report = diagnostics.getReportText(instance)

        assert.truthy(report:match("persisted raw failure"))
        assert.truthy(report:match("Category: timeout"))
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
        finish_async_report()

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
        finish_async_report()

        local dialog = helper.find_dialog("TextViewer")
        assert.truthy(dialog.text:match("Saved to:"))
    end)

    it("shows bug report guidance with diagnostics", function()
        local instance = helper.create_instance()

        instance:showBugReportInfo()
        finish_async_report()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend support report", dialog.title)
        assert.truthy(dialog.text:match("Steps to reproduce"))
        assert.truthy(dialog.text:match("LocalSend Diagnostics"))
    end)

    it("always refreshes evidence when creating a support report", function()
        local instance = helper.create_instance()
        local diagnostics = require("localsend_diagnostics")
        diagnostics._last_report = diagnostics.collect(instance)
        files["/tmp/localsend_server.out"] = "new failure after cached check\n"
        local collected = false
        diagnostics._setAsyncCollectOverride(function(target, callback)
            collected = true
            callback(diagnostics.collect(target))
        end)

        instance:showBugReportInfo()
        assert.is_false(collected)
        finish_async_report()

        assert.is_true(collected)
        local dialog = helper.find_dialog_with_title("TextViewer", "LocalSend support report")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("new failure after cached check"))
    end)

    it("does not prompt for Wi-Fi when diagnostics run offline", function()
        apply_mocks({ is_connected = false, is_online = false })
        mock_io()
        mock_server_probe()
        mock_firewall_probe()
        local instance = helper.create_instance()

        instance:showDiagnostics()
        finish_async_report()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("LAN connected"))
        assert.truthy(dialog.text:match("Receiver lifecycle test"))
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
        finish_async_report()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend diagnostics", dialog.title)
        assert.truthy(dialog.text:match("all %d+ checks passed"))
        assert.truthy(dialog.text:match("LAN connected"))
        assert.truthy(dialog.text:match("Receiver lifecycle test"))
        assert.truthy(dialog.text:match("53317")) -- computer-firewall tip
    end)

    it("diagnostics summary flags missing network with hints", function()
        apply_mocks({ is_connected = false, is_online = false })
        mock_io()
        mock_server_probe()
        mock_firewall_probe()
        local instance = helper.create_instance()

        instance:showDiagnostics()
        finish_async_report()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("to fix")) -- "N to fix" summary
        assert.truthy(dialog.text:match("Enable Wi%-Fi")) -- LAN hint
        assert.truthy(dialog.text:match("Receiver lifecycle test"))
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
        finish_async_report()

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
        finish_async_report()

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
        assert.equals("Device discovery", dialog.title)
        assert.truthy(dialog.text:match("Another LocalSend device was found"))
        local report = diagnostics.collect(instance)
        assert.is_not_nil(report.discovery)
        assert.equals(1, report.discovery.peers)
        assert.is_not_nil(report.discovery.recorded_at)
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
            assert.truthy(text:match("Use Transfer failed%?"))
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

    it("diagnostics suggests Can't find a device when all checks pass", function()
        local state = require("localsend_state")
        state.ServerState.last_send = nil
        finally(function()
            state.ServerState.last_send = nil
        end)
        local instance = helper.create_instance()

        instance:showDiagnostics()
        finish_async_report()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("Can't find a device%?"))
        assert.truthy(dialog.text:match("cannot find"))
    end)

    describe("guided troubleshooting UX", function()
        it("shows progress before running checks asynchronously", function()
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")
            local collected = false
            diagnostics._setAsyncCollectOverride(function(_, callback)
                collected = true
                callback({
                    network_connected = true,
                    binary_exists = true,
                    binary_runs = true,
                    arch_mismatch = false,
                    settings = { save_dir_status = "writable: /mnt/us/documents" },
                    server = { self_test = { ok = true, detail = "HTTP 200" } },
                    firewall = { managed = true, ok = true, detail = "open" },
                    logs = {},
                })
            end)

            instance:runTroubleshootingCheck()

            assert.is_false(collected)
            assert.is_not_nil(helper.find_notification("Checking LocalSend"))
            local task = helper.state.scheduled_tasks[#helper.state.scheduled_tasks]
            assert.is_not_nil(task)
            task.callback()
            assert.is_true(collected)

            local dialog = helper.find_dialog_with_title("TextViewer", "LocalSend check")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.text:match("ready"))
            assert.is_table(dialog.buttons_table)
        end)

        it("classifies a missing binary with a direct reinstall action", function()
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local result = diagnostics.classifyReport(instance, {
                network_connected = true,
                binary_exists = false,
                binary_runs = false,
                arch_mismatch = false,
                settings = { save_dir_status = "writable: /mnt/us/documents" },
                server = { self_test = { ok = false, detail = "binary missing" } },
                firewall = { managed = false, ok = true },
                logs = {},
            })

            assert.equals("binary_missing", result.id)
            assert.equals("Reinstall", result.action_label)
        end)

        it("classifies an invalid save folder before attempting transfer fixes", function()
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local result = diagnostics.classifyReport(instance, {
                network_connected = true,
                binary_exists = true,
                binary_runs = true,
                arch_mismatch = false,
                settings = { save_dir_status = "exists but not writable: /mnt/onboard/books" },
                server = { self_test = { ok = true, detail = "HTTP 200" } },
                firewall = { managed = true, ok = true },
                logs = {},
            })

            assert.equals("save_dir", result.id)
            assert.equals("Choose folder", result.action_label)
        end)

        it("turns connection-refused send evidence into a device discovery action", function()
            local state = require("localsend_state")
            state.ServerState.last_send = {
                success = false,
                message = "tcp connect error: connection refused",
                time = os.time(),
            }
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local result = diagnostics.diagnoseTransfer(instance)

            assert.equals("recipient_unavailable", result.id)
            assert.equals("Find devices", result.action_label)
        end)

        it("recognizes receiver storage errors from the backend log", function()
            files["/tmp/localsend_server.out"] = "Upload error: no space left on device\n"
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local result = diagnostics.diagnoseTransfer(instance)

            assert.equals("storage", result.id)
            assert.equals("Choose folder", result.action_label)
        end)

        it("recognizes a connection interrupted during a long transfer", function()
            files["/tmp/localsend_server.out"] = "Upload failed: unexpected EOF\n"
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local result = diagnostics.diagnoseTransfer(instance)

            assert.equals("interrupted", result.id)
            assert.truthy(result.text:match("awake"))
            assert.equals("Create report", result.action_label)
        end)

        it("does not treat normal HTTPS startup logging as a transfer failure", function()
            files["/tmp/localsend_server.out"] = "INFO Loading https certificate\nINFO Waiting for files\n"
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")

            local result = diagnostics.diagnoseTransfer(instance)

            assert.equals("unknown", result.id)
            assert.truthy(result.title:match("No recent transfer error"))
        end)

        it("stops, tests, and restores a running receiver", function()
            local instance = helper.create_instance()
            local running = true
            instance.isRunning = function()
                return running
            end
            local stop_count, start_count = 0, 0
            instance.stopServer = function(_, options)
                stop_count = stop_count + 1
                running = false
                options.callback(true)
            end
            instance.start = function(_, silent)
                assert.is_true(silent)
                start_count = start_count + 1
                running = true
            end
            local diagnostics = require("localsend_diagnostics")
            diagnostics._setAsyncCollectOverride(nil)
            local report

            diagnostics.collectAsync(instance, function(value)
                report = value
            end)

            assert.is_not_nil(report)
            assert.equals(1, stop_count)
            assert.equals(1, start_count)
            assert.is_true(running)
            assert.is_true(report.server.self_test.ok)
            assert.equals("192.168.1.100", report.server.self_test.lan_probes[1].ip)
            assert.equals("HTTP 200", report.server.self_test.lan_probes[1].detail)
            assert.is_true(report.firewall.managed)
            assert.truthy(report.firewall.detail:match("iptables"))
        end)

        it("starts, tests, and stops a receiver that was originally stopped", function()
            local instance = helper.create_instance()
            local running = false
            local stop_count, start_count = 0, 0
            instance.isRunning = function()
                return running
            end
            instance.start = function(_, silent)
                assert.is_true(silent)
                start_count = start_count + 1
                running = true
            end
            instance.stopServer = function(_, options)
                stop_count = stop_count + 1
                running = false
                options.callback(true)
            end
            local diagnostics = require("localsend_diagnostics")
            diagnostics._setAsyncCollectOverride(nil)
            local report

            diagnostics.collectAsync(instance, function(value)
                report = value
            end)

            assert.is_not_nil(report)
            assert.equals(1, start_count)
            assert.equals(1, stop_count)
            assert.is_false(running)
            assert.is_true(report.server.self_test.ok)
        end)

        it("preserves the failure log before the lifecycle start replaces it", function()
            files["/tmp/localsend_server.out"] = "original upload failure: unexpected EOF\n"
            local instance = helper.create_instance()
            instance.isRunning = function()
                return false
            end
            instance.start = function()
                files["/tmp/localsend_server.out"] = "fresh receiver startup log\n"
            end
            instance.stopServer = function(_, options)
                options.callback(true)
            end
            local diagnostics = require("localsend_diagnostics")
            diagnostics._setAsyncCollectOverride(nil)
            local report

            diagnostics.collectAsync(instance, function(value)
                report = value
            end)

            assert.is_not_nil(report)
            assert.truthy(report.logs.backend_before_check:match("original upload failure"))
            assert.truthy(report.logs.backend_after_check:match("fresh receiver startup"))
        end)

        it("runs the active lifecycle check before diagnosing a failed transfer", function()
            local instance = helper.create_instance()
            local diagnostics = require("localsend_diagnostics")
            local collected = false
            diagnostics._setAsyncCollectOverride(function(_, callback)
                collected = true
                callback({
                    generated_at = "2026-07-12 12:00:00",
                    generated_unix = os.time(),
                    network_connected = true,
                    binary_exists = true,
                    binary_runs = true,
                    arch_mismatch = false,
                    settings = { save_dir_status = "writable: /mnt/us/documents" },
                    server = { self_test = { ok = true, detail = "HTTP 200" } },
                    firewall = { managed = true, ok = true, detail = "open" },
                    logs = {},
                })
            end)

            instance:showTransferTroubleshooting()

            assert.is_false(collected)
            assert.is_not_nil(helper.find_notification("Testing the LocalSend receiver"))
            helper.state.scheduled_tasks[#helper.state.scheduled_tasks].callback()
            assert.is_true(collected)
            local dialog = helper.find_dialog_with_title("TextViewer", "Transfer failed")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.text:match("No recent transfer error"))
        end)

        it("explains discovery in plain language before starting the test", function()
            local instance = helper.create_instance()

            instance:showDiscoveryHelp()

            local dialog = helper.find_dialog("ConfirmBox")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.text:match("Open LocalSend on your phone or computer"))
            assert.equals("Start test", dialog.ok_text)
        end)

        it("briefly stops and restores a running receiver for discovery", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            local stopped = false
            instance.stopServer = function(_, options)
                stopped = true
                options.callback(true)
            end
            local diagnostics = require("localsend_diagnostics")
            local original_launch = diagnostics._launchNetTest
            local restart_after
            diagnostics._launchNetTest = function(_, restart)
                restart_after = restart
            end
            finally(function()
                diagnostics._launchNetTest = original_launch
            end)

            diagnostics.showDiscoveryTest(instance)

            assert.is_true(stopped)
            assert.is_true(restart_after)
        end)

        it("exports support reports to the configured receive folder", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"

            instance:showBugReportInfo()
            finish_async_report()

            local dialog = helper.find_dialog_with_title("TextViewer", "LocalSend support report")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.text:match("/mnt/us/documents/localsend%-bugreport%.txt"))
            assert.is_table(dialog.buttons_table)
        end)
    end)
end)
