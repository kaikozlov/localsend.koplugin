require 'busted.runner'()
local helper = require("spec.test_helper")

-- Tests for LocalSend troubleshooting/diagnostics flow

describe("LocalSend diagnostics", function()
    local original_io_open
    local original_io_popen
    local original_os_remove

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
                output = "v1.3.0\n"
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

    setup(function()
        original_io_open = io.open
        original_io_popen = io.popen
        original_os_remove = os.remove
    end)

    teardown(function()
        _G.io.open = original_io_open
        _G.io.popen = original_io_popen
        _G.os.remove = original_os_remove
    end)

    before_each(function()
        files = {
            ["/tmp/localsend_koreader.pid"] = "12345\n",
            ["/proc/12345/cmdline"] = "/tmp/koreader/plugins/localsend.koplugin/localsend\0" ..
                "recv\0-d\0/mnt/us/documents\0",
            ["/tmp/localsend_server.out"] = "backend log line\n",
            ["/tmp/localsend_send.out"] = "send log line\n",
            ["/tmp/localsend_transfers.log"] = "{\"filename\":\"book.epub\"}\n",
        }
        helper.before_each()
        helper.setup_complete({
            device = {
                is_kindle = true,
                network_info = "wlan0: 192.168.1.100",
                info = "Kindle Test Device",
            },
            network = {
                is_connected = true,
                is_online = false,
            },
            util = {
                pathExists = function(path)
                    if path == "/tmp/koreader/plugins/localsend.koplugin" then return true end
                    if path == "/tmp/koreader/plugins/localsend.koplugin/localsend" then return true end
                    if path == "/mnt/us/documents" then return true end
                    if path == "/tmp/localsend_koreader.pid" then return true end
                    if path == "/proc/12345" then return true end
                    if files[path] ~= nil then return true end
                    return false
                end,
            },
        })
        mock_io()
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
        assert.truthy(report:match("PID: 12345"))
        assert.truthy(report:match("Process is LocalSend recv"))
        assert.truthy(report:match("Local API probe: HTTP 200"))
        assert.truthy(report:match("tcp/53317"))
        assert.truthy(report:match("backend log line"))
        assert.truthy(report:match("book%.epub"))
    end)

    it("shows diagnostics in a TextViewer", function()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend diagnostics", dialog.title)
        assert.truthy(dialog.text:match("LocalSend Diagnostics"))
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
        helper.setup_complete({
            network = {
                is_connected = false,
                is_online = false,
            },
            util = {
                pathExists = function(path)
                    if path == "/tmp/koreader/plugins/localsend.koplugin" then return true end
                    if path == "/tmp/koreader/plugins/localsend.koplugin/localsend" then return true end
                    return false
                end,
            },
        })
        mock_io()
        local instance = helper.create_instance()

        instance:showDiagnostics()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.truthy(dialog.text:match("LAN connected"))
        assert.truthy(dialog.text:match("server is not running"))
    end)

    it("shows recent backend log separately", function()
        local instance = helper.create_instance()

        instance:showRecentBackendLog()

        local dialog = helper.find_dialog("TextViewer")
        assert.is_not_nil(dialog)
        assert.equals("LocalSend backend log", dialog.title)
        assert.truthy(dialog.text:match("backend log line"))
    end)
end)
