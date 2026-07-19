require("busted.runner")()
local helper = require("spec.spec_helper")

local UIManager = require("ui/uimanager")
local InfoMessage = require("ui/widget/infomessage")
local Notification = require("ui/widget/notification")
local InputDialog = require("ui/widget/inputdialog")
local ButtonDialog = require("ui/widget/buttondialog")
local PathChooser = require("ui/widget/pathchooser")
local NetworkMgr = require("ui/network/manager")
local util = require("util")
local json = require("json")
local logger = require("logger")
local T = require("ffi/util").template
local _ = require("gettext")

describe("localsend_sender", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.reset_state()
        helper.reset_localsend_state()
    end)

    local function init_sender(network_mgr)
        package.loaded["localsend_sender"] = nil
        local sender = require("localsend_sender")
        sender.init({
            UIManager = UIManager,
            InfoMessage = InfoMessage,
            Notification = Notification,
            InputDialog = InputDialog,
            ButtonDialog = ButtonDialog,
            PathChooser = PathChooser,
            NetworkMgr = network_mgr or NetworkMgr,
            util = util,
            json = json,
            logger = logger,
            T = T,
            _ = _,
        }, { binary_path = "/tmp/localsend" })
        return sender
    end

    describe("isSendInProgress", function()
        local sender
        before_each(function()
            sender = init_sender()
        end)

        it("returns false when no send in progress", function()
            assert.is_false(sender.isSendInProgress())
        end)

        it("returns true when send is in progress", function()
            require("localsend_state").ServerState.send_in_progress = true
            assert.is_true(sender.isSendInProgress())
        end)
    end)

    describe("sendFile", function()
        local sender
        local orig_pathExists

        setup(function()
            orig_pathExists = util.pathExists
        end)

        teardown(function()
            util.pathExists = orig_pathExists
        end)

        before_each(function()
            util.pathExists = function(path)
                if path == "/test/file.epub" then
                    return true
                end
                return false
            end
            sender = init_sender()
        end)

        it("blocks concurrent sends", function()
            local state = require("localsend_state")
            state.ServerState.send_in_progress = true
            local callback_called, callback_success, callback_msg = false, true, ""
            sender.sendFile({ type = "lan", ip = "192.168.1.50", alias = "Phone" }, "/test/file.epub", nil, function(success, msg)
                callback_called = true
                callback_success = success
                callback_msg = msg
            end)
            assert.is_true(callback_called)
            assert.is_false(callback_success)
            assert.truthy(callback_msg:match("in progress"))
        end)

        it("fails for nonexistent file", function()
            local callback_called, callback_success = false, true
            sender.sendFile({ type = "lan", ip = "192.168.1.50", alias = "Phone" }, "/nonexistent/file.epub", nil, function(success)
                callback_called = true
                callback_success = success
            end)
            assert.is_true(callback_called)
            assert.is_false(callback_success)
        end)

        it("builds correct command for LAN device", function()
            sender.sendFile({ type = "lan", ip = "192.168.1.50", protocol = "https", alias = "Phone" }, "/test/file.epub", nil, nil)
            assert.is_true(#helper.state.os_execute_calls > 0)
            local cmd = helper.state.os_execute_calls[1]
            assert.truthy(cmd:match("send"))
            assert.truthy(cmd:match("%-%-ip"))
            assert.truthy(cmd:match("192%.168%.1%.50"))
            assert.truthy(cmd:match("%-%-https"))
        end)

        it("builds correct command for WebRTC device", function()
            sender.sendFile({ type = "webrtc", id = "abc-123", alias = "Browser" }, "/test/file.epub", nil, nil)
            assert.is_true(#helper.state.os_execute_calls > 0)
            local cmd = helper.state.os_execute_calls[1]
            assert.truthy(cmd:match("send"))
            assert.truthy(cmd:match("%-%-webrtc"))
            assert.truthy(cmd:match("%-%-target"))
            assert.truthy(cmd:match("abc%-123"))
        end)

        it("includes PIN in command when provided", function()
            sender.sendFile({ type = "lan", ip = "192.168.1.50", protocol = "https", alias = "Phone" }, "/test/file.epub", "1234", nil)
            local cmd = helper.state.os_execute_calls[1]
            assert.truthy(cmd:match("%-p"))
            assert.truthy(cmd:match("1234"))
        end)

        it("sets send_in_progress flag", function()
            local state = require("localsend_state")
            assert.is_false(state.ServerState.send_in_progress)
            sender.sendFile({ type = "lan", ip = "192.168.1.50", protocol = "https", alias = "Phone" }, "/test/file.epub", nil, nil)
            assert.is_true(state.ServerState.send_in_progress)
        end)

        it("retains raw failure evidence after cleaning up the send log", function()
            local original_read = util.readFromFile
            finally(function()
                util.readFromFile = original_read
            end)
            util.pathExists = function(path)
                return path == "/test/file.epub" or path == "/tmp/localsend_send.out.exit"
            end
            util.readFromFile = function(path)
                if path == "/tmp/localsend_send.out.exit" then
                    return "1\n"
                elseif path == "/tmp/localsend_send.out" then
                    return "connection reset by peer: raw detail"
                end
            end

            sender.sendFile({ type = "lan", ip = "192.168.1.50", protocol = "http", alias = "Phone" }, "/test/file.epub", nil, nil)
            helper.state.scheduled_tasks[#helper.state.scheduled_tasks].callback()

            local last = require("localsend_state").ServerState.last_send
            assert.is_not_nil(last)
            assert.is_false(last.success)
            assert.equals(1, last.exit_code)
            assert.equals("connection", last.error_category)
            assert.truthy(last.raw_output:match("raw detail"))
            assert.equals("V2 http", last.protocol)
            assert.equals("Phone", last.recipient)
            assert.equals("file.epub", last.filename)
        end)
    end)

    describe("cancelSend", function()
        local sender
        before_each(function()
            sender = init_sender()
        end)

        it("clears send_in_progress flag", function()
            local state = require("localsend_state")
            state.ServerState.send_in_progress = true
            sender.cancelSend()
            assert.is_false(state.ServerState.send_in_progress)
        end)
    end)

    describe("showFileSendFlow", function()
        local sender
        before_each(function()
            sender = init_sender()
        end)

        it("blocks when send already in progress", function()
            require("localsend_state").ServerState.send_in_progress = true
            sender.showFileSendFlow({
                getPickerStartPath = function(_, path)
                    return path
                end,
            })
            assert.is_not_nil(helper.find_notification("in progress"))
        end)

        it("starts device scan when network connected", function()
            sender.showFileSendFlow({
                getPickerStartPath = function(_, path)
                    return path
                end,
            })
            assert.is_true(#helper.state.os_execute_calls > 0)
            local cmd = helper.state.os_execute_calls[1]
            assert.truthy(cmd:match("scan"))
            assert.truthy(cmd:match("%-%-json"))
        end)

        it("uses willRerunWhenConnected to avoid duplicate flow when offline", function()
            local rerun_called, scan_called = false, false
            local offline_nm = {
                isConnected = function()
                    return false
                end,
                willRerunWhenConnected = function()
                    rerun_called = true
                    return true
                end,
                runWhenConnected = function()
                    scan_called = true
                end,
            }
            sender = init_sender(offline_nm)
            sender.showFileSendFlow({
                getPickerStartPath = function(_, path)
                    return path
                end,
            })
            assert.is_true(rerun_called)
            assert.is_false(scan_called)
            assert.equals(0, #helper.state.os_execute_calls)
        end)

        it("PathChooser allows selecting files and directories", function()
            local discovery = require("localsend_discovery")
            local orig_scan = discovery.scanDevices
            local orig_selector = discovery.showDeviceSelector

            discovery.scanDevices = function(callback, _options)
                callback({
                    {
                        type = "lan",
                        ip = "192.168.1.50",
                        alias = "Phone",
                        protocol = "https",
                    },
                })
            end
            discovery.showDeviceSelector = function(devices, onSelect, _onRetry)
                onSelect(devices[1])
            end

            local ok, err = pcall(function()
                sender.showFileSendFlow({
                    getPickerStartPath = function(_, path)
                        return path
                    end,
                    save_dir = "/tmp",
                })

                local chooser = helper.find_dialog("PathChooser")
                assert.is_not_nil(chooser)
                assert.is_true(chooser.select_file)
                assert.is_true(chooser.select_directory)
            end)

            discovery.scanDevices = orig_scan
            discovery.showDeviceSelector = orig_selector
            assert.is_true(ok, err)
        end)
    end)

    describe("error categorization", function()
        local sender
        before_each(function()
            sender = init_sender()
        end)

        it("categorizeError identifies PIN required errors", function()
            assert.equals("pin_required", sender.categorizeError("error: PIN required (401)"))
        end)
        it("categorizeError identifies wrong PIN errors", function()
            assert.equals("wrong_pin", sender.categorizeError("error: wrong PIN"))
        end)
        it("categorizeError identifies rejected errors", function()
            assert.equals("rejected", sender.categorizeError("error: transfer rejected by receiver"))
        end)
        it("categorizeError identifies connection refused (device not running)", function()
            assert.equals("connection_refused", sender.categorizeError("error: connection refused"))
        end)
        it("categorizeError identifies generic connection errors", function()
            assert.equals("connection", sender.categorizeError("error: connection reset by peer"))
        end)
        it("categorizeError identifies rate limiting", function()
            assert.equals("rate_limited", sender.categorizeError("error: too many attempts"))
        end)
        it("categorizeError identifies timeout errors", function()
            assert.equals("timeout", sender.categorizeError("error: timeout waiting for response"))
        end)
        it("categorizeError returns unknown for unrecognized errors", function()
            assert.equals("unknown", sender.categorizeError("error: some random error"))
        end)
        it("categorizeError handles nil input", function()
            assert.equals("unknown", sender.categorizeError(nil))
        end)
        it("categorizeError handles empty string", function()
            assert.equals("unknown", sender.categorizeError(""))
        end)
    end)

    describe("PIN dialog flow", function()
        local sender
        before_each(function()
            sender = init_sender()
        end)

        it("showPINDialog shows input dialog", function()
            sender.showPINDialog({ alias = "Test Device" }, function() end)
            local dialog = helper.find_dialog("InputDialog")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.title:match("PIN"))
        end)

        it("showPINDialog includes device name in title", function()
            sender.showPINDialog({ alias = "iPhone" }, function() end)
            local dialog = helper.find_dialog("InputDialog")
            assert.is_not_nil(dialog)
            assert.truthy(dialog.title:match("iPhone"))
        end)
    end)
end)
