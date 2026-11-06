require("busted.runner")()
local helper = require("spec.spec_helper")

local UIManager = require("ui/uimanager")
local InfoMessage = require("ui/widget/infomessage")
local Notification = require("ui/widget/notification")
local ButtonDialog = require("ui/widget/buttondialog")
local util = require("util")
local json = require("json")
local logger = require("logger")
local T = require("ffi/util").template
local _ = require("gettext")

describe("localsend_discovery", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.reset_state()
        helper.reset_localsend_state()
    end)

    -- Load a fresh discovery module wired to real KOReader deps. json_dep lets a
    -- test substitute a controlled decoder without clobbering the real json module.
    local function init_discovery(json_dep)
        package.loaded["localsend_discovery"] = nil
        local discovery = require("localsend_discovery")
        discovery.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            ButtonDialog = ButtonDialog,
            util = util,
            json = json_dep or json,
            logger = logger,
            T = T,
            _ = _,
        }, { binary_path = "/tmp/localsend" })
        return discovery
    end

    describe("parseDevices", function()
        local discovery

        before_each(function()
            discovery = init_discovery()
        end)

        it("returns empty array for nil input", function()
            assert.are.same({}, discovery.parseDevices(nil))
        end)

        it("returns empty array for empty string", function()
            assert.are.same({}, discovery.parseDevices(""))
        end)

        it("returns empty array for invalid JSON", function()
            assert.are.same({}, discovery.parseDevices("not valid json"))
        end)

        it("parses LAN devices from JSON", function()
            local json_mock = {
                decode = function()
                    return {
                        lan = {
                            {
                                ip = "192.168.1.50",
                                port = 53317,
                                alias = "iPhone",
                                version = "2.1",
                                protocol = "https",
                            },
                        },
                        webrtc = {},
                    }
                end,
            }
            discovery = init_discovery(json_mock)
            local devices = discovery.parseDevices('{"lan":[{"ip":"192.168.1.50"}]}')
            assert.equals(1, #devices)
            assert.equals("lan", devices[1].type)
            assert.equals("iPhone", devices[1].alias)
            assert.equals("192.168.1.50", devices[1].ip)
            assert.equals(53317, devices[1].port)
            assert.equals("https", devices[1].protocol)
        end)

        it("parses WebRTC devices from JSON", function()
            local json_mock = {
                decode = function()
                    return {
                        lan = {},
                        webrtc = {
                            { id = "abc-123", alias = "Browser", version = "2.1" },
                        },
                    }
                end,
            }
            discovery = init_discovery(json_mock)
            local devices = discovery.parseDevices('{"webrtc":[{"id":"abc-123"}]}')
            assert.equals(1, #devices)
            assert.equals("webrtc", devices[1].type)
            assert.equals("Browser", devices[1].alias)
            assert.equals("abc-123", devices[1].id)
        end)

        it("parses mixed LAN and WebRTC devices", function()
            local json_mock = {
                decode = function()
                    return {
                        lan = {
                            { ip = "192.168.1.50", port = 53317, alias = "Phone", version = "2.1", protocol = "https" },
                        },
                        webrtc = {
                            { id = "abc-123", alias = "Browser", version = "2.1" },
                        },
                    }
                end,
            }
            discovery = init_discovery(json_mock)
            local devices = discovery.parseDevices("{}")
            assert.equals(2, #devices)
            assert.equals("lan", devices[1].type)
            assert.equals("webrtc", devices[2].type)
        end)
    end)

    describe("getDeviceDisplayText", function()
        local discovery
        before_each(function()
            discovery = init_discovery()
        end)

        it("formats LAN device with IP", function()
            assert.equals("[LAN] iPhone (192.168.1.50)", discovery.getDeviceDisplayText({ type = "lan", alias = "iPhone", ip = "192.168.1.50" }))
        end)

        it("formats WebRTC device without IP", function()
            assert.equals("[WebRTC] Browser", discovery.getDeviceDisplayText({ type = "webrtc", alias = "Browser", id = "abc-123" }))
        end)
    end)

    describe("getCachedDevices", function()
        local discovery
        before_each(function()
            discovery = init_discovery()
        end)

        it("returns empty array when no devices cached", function()
            assert.are.same({}, discovery.getCachedDevices())
        end)

        it("returns cached devices from ServerState", function()
            local state = require("localsend_state")
            state.ServerState.discovered_devices = {
                { type = "lan", alias = "Phone", ip = "192.168.1.50" },
            }
            assert.equals(1, #discovery.getCachedDevices())
            assert.equals("Phone", discovery.getCachedDevices()[1].alias)
        end)
    end)

    describe("showDeviceSelector", function()
        local discovery
        before_each(function()
            discovery = init_discovery()
        end)

        it("shows info message when no devices found", function()
            local callback_called = false
            local callback_device = "not_nil"
            discovery.showDeviceSelector({}, function(device)
                callback_called = true
                callback_device = device
            end)
            assert.is_true(callback_called)
            assert.is_nil(callback_device)
            assert.equals(1, #helper.state.notifications_shown)
            assert.truthy(helper.state.notifications_shown[1].text:match("No devices found"))
        end)

        it("shows button dialog for available devices", function()
            discovery.showDeviceSelector({
                { type = "lan", alias = "Phone", ip = "192.168.1.50" },
            }, function() end)
            local dialog = helper.find_dialog("ButtonDialog")
            assert.is_not_nil(dialog)
            assert.equals("Select target device", dialog.title)
        end)

        it("allows scanning again when devices were found", function()
            local retry_called = false
            discovery.showDeviceSelector({
                { type = "lan", alias = "Phone", ip = "192.168.1.50" },
            }, function() end, function()
                retry_called = true
            end)

            local dialog = helper.find_dialog("ButtonDialog")
            local scan_again
            for _, row in ipairs(dialog.buttons) do
                for _, button in ipairs(row) do
                    if button.text == "Scan again" then
                        scan_again = button
                    end
                end
            end
            assert.is_not_nil(scan_again)
            scan_again.callback()
            assert.is_true(retry_called)
        end)
    end)

    describe("scan lifecycle", function()
        local discovery, constants, s, files, running, queued, holds
        local firewall, old_acquire, old_release, old_remove, old_execute
        local result, failure, calls

        before_each(function()
            constants = require("localsend_constants")
            s = require("localsend_state").ServerState
            files, running, queued = {}, {}, {}
            holds = {}
            result, failure, calls = nil, nil, 0
            firewall = require("localsend_firewall")
            old_acquire, old_release = firewall.acquire, firewall.release
            firewall.acquire = function(owner)
                holds[owner] = true
                return { ok = true }
            end
            firewall.release = function(owner)
                holds[owner] = nil
            end
            old_remove, old_execute = os.remove, os.execute
            os.remove = function(path)
                files[path] = nil
                return true
            end
            os.execute = function(command)
                if not command:find("kill", 1, true) then
                    files[constants.SCAN_OUTPUT_FILE .. ".pid"] = "4242"
                    files[constants.SCAN_OUTPUT_FILE .. ".supervisor"] = "4243"
                    running[4242], running[4243] = true, true
                end
                return 0
            end
            package.loaded["localsend_discovery"] = nil
            discovery = require("localsend_discovery")
            discovery.init({
                UIManager = {
                    scheduleIn = function(_, _, fn)
                        table.insert(queued, fn)
                    end,
                },
                util = {
                    readFromFile = function(path)
                        local pid = tonumber(path:match("^/proc/(%d+)/cmdline$"))
                        if pid and running[pid] then
                            return "localsend\0scan\0"
                        end
                        return files[path]
                    end,
                    pathExists = function(path)
                        local pid = tonumber(path:match("^/proc/(%d+)$"))
                        return pid and running[pid] or files[path] ~= nil
                    end,
                    shell_escape = util.shell_escape,
                },
                json = json,
                logger = logger,
                _ = _,
            }, { binary_path = "/tmp/localsend" })
        end)

        after_each(function()
            firewall.acquire, firewall.release = old_acquire, old_release
            os.remove, os.execute = old_remove, old_execute
            require("localsend_power").releaseAll()
        end)

        local function callback(devices, err)
            calls = calls + 1
            result, failure = devices, err
        end

        local function poll()
            local fn = table.remove(queued, 1)
            assert.is_not_nil(fn)
            fn()
        end

        local function exited(status, output, log)
            running[4242], running[4243] = nil, nil
            files[constants.SCAN_OUTPUT_FILE .. ".exit"] = tostring(status)
            files[constants.SCAN_OUTPUT_FILE] = output
            files[constants.SCAN_LOG_FILE] = log
        end

        it("reports backend failure without replacing successful cached devices", function()
            s.discovered_devices = { { alias = "Previous" } }
            discovery.scanDevices(callback)
            exited(1, "", "control socket permission denied")
            poll()
            assert.equals(1, calls)
            assert.is_nil(result)
            assert.equals("Previous", s.discovered_devices[1].alias)
        end)

        it("reports malformed successful output as an error", function()
            discovery.scanDevices(callback)
            exited(0, "not json")
            poll()
            assert.is_nil(result)
            assert.is_not_nil(failure)
        end)

        it("retains firewall ownership until a cancelled child and waiter exit", function()
            discovery.scanDevices(callback)
            discovery.cancelScan()
            poll()
            assert.is_true(s.scan_in_progress)
            assert.is_true(holds.scan)
            running[4242] = nil
            poll()
            assert.is_true(s.scan_in_progress)
            assert.is_true(holds.scan)
            exited(143, "")
            poll()
            assert.is_false(s.scan_in_progress)
            assert.is_nil(holds.scan)
            assert.equals(0, calls)
        end)

        it("reports a timeout only after the process has actually exited", function()
            discovery.scanDevices(callback)
            s.scan_start_time = os.time() - constants.SCAN_MAX_POLL_DURATION
            poll()
            assert.equals(0, calls)
            assert.is_true(holds.scan)
            exited(143, "")
            poll()
            assert.equals(1, calls)
            assert.is_nil(result)
            assert.is_not_nil(failure)
            assert.is_nil(holds.scan)
        end)

        it("synchronously waits for cancellation before releasing firewall ownership", function()
            discovery.scanDevices(callback)
            local sleeps = 0
            local stopped = discovery.stopActiveScanSync(function()
                sleeps = sleeps + 1
                assert.is_true(holds.scan)
                if sleeps == 3 then
                    exited(143, "")
                end
            end)
            assert.is_true(stopped)
            assert.is_false(s.scan_in_progress)
            assert.is_nil(holds.scan)
            poll()
            assert.equals(0, calls)
        end)

        it("retains ownership when the OS cannot terminate a child", function()
            discovery.scanDevices(callback)
            assert.is_false(discovery.stopActiveScanSync(function() end))
            assert.is_true(s.scan_in_progress)
            assert.is_true(holds.scan)
        end)

        it("rejects overlapping scans without affecting the active child", function()
            discovery.scanDevices(callback)
            local busy
            assert.is_false(discovery.scanDevices(function(_, err)
                busy = err
            end))
            assert.is_not_nil(busy)
            assert.is_true(s.scan_in_progress)
            assert.is_true(running[4242])
            assert.is_true(holds.scan)
        end)
    end)
end)
