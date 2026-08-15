require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")
local ffiUtil = require("ffi/util")

-- Tests for the start() function - the core server startup logic.
-- Uses a real temp save directory; os.execute is captured by the helper spy.

describe("start() function", function()
    local SAVE

    setup(function()
        helper.setup_complete()
        SAVE = get_test_data_dir() .. "/save"
        util.makePath(SAVE)
    end)

    teardown(function()
        pcall(ffiUtil.purgeDir, SAVE)
    end)

    before_each(function()
        helper.before_each()
    end)

    -- The helper spy records os.execute into helper.state.os_execute_calls and
    -- returns success (0) by default, so a "successful start" needs no setup.
    local function setup_successful_start() end

    describe("when server is already running", function()
        it("should exit early without showing notification", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            helper.reset_state()
            instance:start()
            assert.equal(0, #helper.state.notifications_shown)
        end)

        it("should not execute any commands", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            helper.reset_state()
            instance:start()
            assert.equal(0, #helper.state.os_execute_calls)
        end)
    end)

    describe("with invalid save directory", function()
        it("should show warning and not start", function()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.validateSaveDir = function()
                return false, "Cannot create directory"
            end
            instance.isRunning = function()
                return false
            end
            helper.reset_state()
            instance:start()
            assert.is_truthy(helper.find_notification("Invalid save directory"))
        end)
    end)

    describe("command building", function()
        local function make_instance()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            local check_count = 0
            instance.isRunning = function()
                check_count = check_count + 1
                return check_count > 1
            end
            return instance
        end

        it("should include save directory and transfer log", function()
            setup_successful_start()
            make_instance():start()
            local found_cmd = false
            for _, cmd in ipairs(helper.state.os_execute_calls) do
                if cmd:match("localsend") and cmd:match("recv") then
                    found_cmd = true
                    assert.truthy(cmd:find("'-d' '" .. SAVE .. "'", 1, true))
                    assert.truthy(cmd:match("'%-l' '/tmp/localsend_transfers%.log'"))
                    break
                end
            end
            assert.is_true(found_cmd)
        end)

        it("should pass native transfer notification and activity markers to the receiver", function()
            setup_successful_start()
            make_instance():start()
            local cmd = helper.find_execute_call("localsend")
            assert.is_truthy(cmd)
            assert.truthy(cmd:find("--notify-file", 1, true))
            assert.truthy(cmd:find("/tmp/localsend_notify", 1, true))
            assert.truthy(cmd:find("--busy-file", 1, true))
            assert.truthy(cmd:find("/tmp/localsend_busy", 1, true))
        end)

        it("should include device name when set", function()
            setup_successful_start()
            local instance = make_instance()
            instance.device_name = "My Kindle"
            instance:start()
            assert.is_truthy(helper.find_execute_call("'%-n' 'My Kindle'"))
        end)

        it("should include PIN when set", function()
            setup_successful_start()
            local instance = make_instance()
            instance.pin = "1234"
            instance:start()
            assert.is_truthy(helper.find_execute_call("'%-p' '1234'"))
        end)

        it("should include accept extensions when set", function()
            setup_successful_start()
            local instance = make_instance()
            instance.accept_ext = "epub,pdf"
            instance:start()
            assert.is_truthy(helper.find_execute_call("'%-a' 'epub,pdf'"))
        end)

        it("should include --https=false when HTTPS disabled", function()
            setup_successful_start()
            local instance = make_instance()
            instance.use_https = false
            instance:start()
            assert.is_truthy(helper.find_execute_call("'%-%-https=false'"))
        end)

        it("should include -w=false when WebRTC disabled", function()
            setup_successful_start()
            local instance = make_instance()
            instance.use_webrtc = false
            instance:start()
            assert.is_truthy(helper.find_execute_call("'%-w=false'"))
        end)

        it("should include extension routing config when enabled", function()
            setup_successful_start()
            local instance = make_instance()
            instance.exportExtRouting = function()
                return "/path/to/ext_routing.json"
            end
            instance:start()
            assert.is_truthy(helper.find_execute_call("'%-%-ext%-routing' '/path/to/ext_routing%.json'"))
        end)
    end)

    describe("startup sequence", function()
        local function make_instance()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.exportExtRouting = function()
                return nil
            end
            local check_count = 0
            instance.isRunning = function()
                check_count = check_count + 1
                return check_count > 1
            end
            return instance
        end

        it("should call clearTransferLog before starting", function()
            local instance = make_instance()
            local clear_called = false
            instance.clearTransferLog = function()
                clear_called = true
            end
            instance.openFirewall = function() end
            instance:start()
            assert.is_true(clear_called)
        end)

        it("should call openFirewall before starting", function()
            local instance = make_instance()
            local firewall_opened = false
            instance.clearTransferLog = function() end
            instance.openFirewall = function()
                firewall_opened = true
            end
            instance:start()
            assert.is_true(firewall_opened)
        end)
    end)

    describe("on successful start", function()
        local function make_instance()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            local check_count = 0
            instance.isRunning = function()
                check_count = check_count + 1
                return check_count > 1
            end
            return instance
        end

        it("should schedule sentinel polling for fast notifications", function()
            local instance = make_instance()
            instance:start()
            local found = false
            for _, task in ipairs(helper.state.scheduled_tasks) do
                if task.delay == 2 then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("should show success notification with device name", function()
            local instance = make_instance()
            instance.port = "53317"
            instance:start()
            assert.is_truthy(helper.find_notification("LocalSend Ready"))
        end)
    end)

    describe("on failed start", function()
        it("should show error when os.execute fails", function()
            helper.mock_os_execute(function()
                return 1
            end)
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.closeFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            instance.isRunning = function()
                return false
            end
            helper.reset_state()
            instance:start()
            assert.is_truthy(helper.find_notification("Failed to start"))
        end)

        it("should close firewall on failure", function()
            helper.mock_os_execute(function()
                return 1
            end)
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            instance.isRunning = function()
                return false
            end
            local firewall_closed = false
            instance.closeFirewall = function()
                firewall_closed = true
            end
            instance:start()
            assert.is_true(firewall_closed)
        end)

        it("should show timeout error when server doesn't become ready", function()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.closeFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            instance.isRunning = function()
                return false
            end

            helper.reset_state()
            instance:start()

            for _ = 1, 60 do
                if #helper.state.scheduled_tasks > 0 then
                    table.remove(helper.state.scheduled_tasks, 1).callback()
                else
                    break
                end
            end
            assert.is_truthy(helper.find_notification("5 seconds"))
        end)

        it("should close firewall on timeout", function()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            instance.isRunning = function()
                return false
            end
            local firewall_closed = false
            instance.closeFirewall = function()
                firewall_closed = true
            end

            helper.reset_state()
            instance:start()
            for _ = 1, 60 do
                if #helper.state.scheduled_tasks > 0 then
                    table.remove(helper.state.scheduled_tasks, 1).callback()
                else
                    break
                end
            end
            assert.is_true(firewall_closed)
        end)

        it("should poll up to 50 times before giving up", function()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.closeFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            local poll_count = 0
            instance.isRunning = function()
                poll_count = poll_count + 1
                return false
            end

            helper.reset_state()
            instance:start()
            for _ = 1, 60 do
                if #helper.state.scheduled_tasks > 0 then
                    table.remove(helper.state.scheduled_tasks, 1).callback()
                else
                    break
                end
            end
            assert.is_true(poll_count >= 50)
        end)

        it("should stop polling early when server becomes ready", function()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            local poll_count = 0
            instance.isRunning = function()
                poll_count = poll_count + 1
                return poll_count >= 3
            end

            instance:start()
            for _ = 1, 10 do
                if #helper.state.scheduled_tasks > 0 then
                    table.remove(helper.state.scheduled_tasks, 1).callback()
                else
                    break
                end
            end
            assert.is_true(poll_count >= 3)
        end)
    end)

    describe("routing-based extension filtering", function()
        local function make_instance()
            local instance = helper.create_instance()
            instance.save_dir = SAVE
            instance.routing_enabled = true
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            local check_count = 0
            instance.isRunning = function()
                check_count = check_count + 1
                return check_count > 1
            end
            return instance
        end

        it("should use routed extensions when routing enabled", function()
            local instance = make_instance()
            instance.ext_dirs = { epub = "/books", pdf = "/docs" }
            instance.routing_accept_all = false
            instance:start()
            local recv_cmd = helper.find_execute_call("localsend.*recv")
            assert.is_truthy(recv_cmd)
            assert.truthy(recv_cmd:find("'-a'", 1, true))
            assert.truthy(recv_cmd:match("epub"))
            assert.truthy(recv_cmd:match("pdf"))
        end)

        it("should accept all when routing_accept_all is true", function()
            local instance = make_instance()
            instance.ext_dirs = { epub = "/books" }
            instance.routing_accept_all = true
            instance:start()
            local recv_cmd = helper.find_execute_call("localsend.*recv")
            assert.is_truthy(recv_cmd)
            assert.is_nil(recv_cmd:find("'-a'", 1, true), "accept-all routing must not restrict recv extensions")
        end)
    end)
end)
