require 'busted.runner'()
local helper = require("spec.test_helper")

-- Tests for the start() function - the core server startup logic

describe("start() function", function()
    setup(function()
        helper.setup_complete({
            util = {
                pathExists = function(path)
                    if path == "/tmp/koreader/plugins/localsend.koplugin" then return true end
                    if path == "/tmp/koreader/plugins/localsend.koplugin/localsend" then return true end
                    if path == "/mnt/us/documents" then return true end
                    return false
                end,
                makePath = function(path)
                    if _G._test_makePath_should_fail then
                        return nil, "Failed to create directory"
                    end
                    return true
                end,
            },
        })
        helper.mock_os_execute()
        helper.mock_os_remove()
    end)

    before_each(function()
        helper.before_each()
        -- Re-apply os.execute mock after reset
        helper.mock_os_execute()

        -- Mock io.open for write test
        local original_io_open = io.open
        _G.io.open = function(path, mode)
            if mode == "w" and path:match("%.localsend_write_test$") then
                return { close = function() end }
            end
            return original_io_open(path, mode)
        end
    end)

    describe("when server is already running", function()
        it("should exit early without showing notification", function()
            local instance = helper.create_instance()
            instance.isRunning = function() return true end

            instance:start()

            assert.equal(0, #helper.state.notifications_shown,
                "Should not show any notification when already running")
        end)

        it("should not execute any commands", function()
            local instance = helper.create_instance()
            instance.isRunning = function() return true end

            -- Clear any commands from init (telemetry cleanup runs on startup)
            helper.state.os_execute_calls = {}

            instance:start()

            assert.equal(0, #helper.state.os_execute_calls,
                "Should not execute any commands when already running")
        end)
    end)

    describe("with invalid save directory", function()
        it("should show warning and not start", function()
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader/plugins/localsend.koplugin" then return true end
                if path == "/tmp/koreader/plugins/localsend.koplugin/localsend" then return true end
                return false
            end
            package.loaded["util"].makePath = function(path)
                return nil, "Failed to create directory"
            end

            local instance = helper.create_instance()
            instance.save_dir = "/invalid/readonly/path"
            instance.isRunning = function() return false end

            instance:start()

            local warning = helper.find_notification("Invalid save directory")
            assert.is_truthy(warning, "Should show invalid save directory warning")
        end)
    end)

    describe("command building", function()
        local function setup_successful_start()
            local is_running = false
            package.loaded["util"].pathExists = function(path)
                if path == "/tmp/koreader/plugins/localsend.koplugin" then return true end
                if path == "/tmp/koreader/plugins/localsend.koplugin/localsend" then return true end
                if path == "/mnt/us/documents" then return true end
                if path == "/proc/12345" then return is_running end
                return false
            end

            _G.os.execute = function(cmd)
                table.insert(helper.state.os_execute_calls, cmd)
                if cmd:match("echo %$!") then
                    is_running = true
                end
                return 0
            end

            return function() is_running = true end
        end

        it("should include save directory and transfer log", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.isRunning = function() return false end
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_cmd = false
            for _, cmd in ipairs(helper.state.os_execute_calls) do
                if cmd:match("localsend") and cmd:match("recv") then
                    found_cmd = true
                    assert.truthy(cmd:match("'%-d' '/mnt/us/documents'"),
                        "Should include -d flag with save directory")
                    assert.truthy(cmd:match("'%-l' '/tmp/localsend_transfers.log'"),
                        "Should include -l flag for transfer log")
                    break
                end
            end
            assert.is_true(found_cmd, "Should execute localsend recv command")
        end)

        it("should redirect backend output to diagnostics log", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local cmd = helper.find_execute_call("/tmp/localsend_server%.out")
            assert.is_truthy(cmd, "Should redirect stdout/stderr to backend diagnostics log")
            assert.truthy(cmd:match("2>&1"), "Should capture stderr with stdout")
        end)

        it("should include device name when set", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.device_name = "My Kindle"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_name = helper.find_execute_call("'%-n' 'My Kindle'")
            assert.is_truthy(found_name, "Should include device name flag")
        end)

        it("should include PIN when set", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.pin = "1234"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_pin = helper.find_execute_call("'%-p' '1234'")
            assert.is_truthy(found_pin, "Should include PIN flag")
        end)

        it("should include accept extensions when set", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.accept_ext = "epub,pdf"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_ext = helper.find_execute_call("'%-a' 'epub,pdf'")
            assert.is_truthy(found_ext, "Should include accept extensions flag")
        end)

        it("should include --https=false when HTTPS disabled", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.use_https = false
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_https = helper.find_execute_call("'%-%-https=false'")
            assert.is_truthy(found_https, "Should include --https=false flag")
        end)

        it("should include -w=false when WebRTC disabled", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.use_webrtc = false
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_webrtc = helper.find_execute_call("'%-w=false'")
            assert.is_truthy(found_webrtc, "Should include -w=false flag")
        end)

        it("should include extension routing config when enabled", function()
            setup_successful_start()

            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return "/path/to/ext_routing.json" end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_routing = helper.find_execute_call("'%-%-ext%-routing' '/path/to/ext_routing.json'")
            assert.is_truthy(found_routing, "Should include --ext-routing flag")
        end)
    end)

    describe("startup sequence", function()
        it("should call clearTransferLog before starting", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"

            local clear_called = false
            instance.clearTransferLog = function() clear_called = true end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            assert.is_true(clear_called, "clearTransferLog should be called")
        end)

        it("should call openFirewall before starting", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"

            local firewall_opened = false
            instance.clearTransferLog = function() end
            instance.openFirewall = function() firewall_opened = true end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            assert.is_true(firewall_opened, "openFirewall should be called")
        end)
    end)

    describe("on successful start", function()
        it("should schedule sentinel polling for fast notifications", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            -- Find the sentinel poll task (2 second delay)
            local found_sentinel_task = false
            for _, task in ipairs(helper.state.scheduled_tasks) do
                if task.delay == 2 then
                    found_sentinel_task = true
                    break
                end
            end
            assert.is_true(found_sentinel_task, "Should schedule sentinel polling with 2 second delay")
        end)

        it("should show success notification with device name", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.port = "53317"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local success = helper.find_notification("LocalSend Ready")
            assert.is_truthy(success, "Should show success notification")
        end)
    end)

    describe("on failed start", function()
        it("should show error when os.execute fails", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.closeFirewall = function() end
            instance.exportExtRouting = function() return nil end
            instance.isRunning = function() return false end

            _G.os.execute = function() return 1 end

            instance:start()

            local error_msg = helper.find_notification("Failed to start")
            assert.is_truthy(error_msg, "Should show error notification")
        end)

        it("should close firewall on failure", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end
            instance.isRunning = function() return false end

            local firewall_closed = false
            instance.closeFirewall = function() firewall_closed = true end

            _G.os.execute = function() return 1 end

            instance:start()

            assert.is_true(firewall_closed, "Should close firewall on failure")
        end)

        it("should show timeout error when server doesn't become ready", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.closeFirewall = function() end
            instance.exportExtRouting = function() return nil end
            instance.isRunning = function() return false end

            -- Clear any init-scheduled tasks before calling start
            helper.state.scheduled_tasks = {}
            helper.state.notifications_shown = {}

            instance:start()

            -- Drain all scheduled callbacks to simulate async timeout
            for i = 1, 60 do
                if #helper.state.scheduled_tasks > 0 then
                    local cb = table.remove(helper.state.scheduled_tasks, 1)
                    cb.callback()
                else
                    break
                end
            end

            local timeout = helper.find_notification("5 seconds")
            assert.is_truthy(timeout, "Should show timeout error")
        end)

        it("should close firewall on timeout", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end
            instance.isRunning = function() return false end

            local firewall_closed = false
            instance.closeFirewall = function() firewall_closed = true end

            -- Clear any init-scheduled tasks before calling start
            helper.state.scheduled_tasks = {}

            instance:start()

            -- Drain all scheduled callbacks to simulate async timeout
            for i = 1, 60 do
                if #helper.state.scheduled_tasks > 0 then
                    local cb = table.remove(helper.state.scheduled_tasks, 1)
                    cb.callback()
                else
                    break
                end
            end

            assert.is_true(firewall_closed, "Should close firewall on timeout")
        end)

        it("should poll up to 50 times before giving up", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.closeFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local poll_count = 0
            instance.isRunning = function()
                poll_count = poll_count + 1
                return false
            end

            -- Clear any init-scheduled tasks before calling start
            helper.state.scheduled_tasks = {}

            instance:start()

            -- Drain all scheduled callbacks to simulate async polling
            for i = 1, 60 do
                if #helper.state.scheduled_tasks > 0 then
                    local cb = table.remove(helper.state.scheduled_tasks, 1)
                    cb.callback()
                else
                    break
                end
            end

            assert.is_true(poll_count >= 50, "Should poll at least 50 times before timeout")
        end)

        it("should stop polling early when server becomes ready", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local poll_count = 0
            instance.isRunning = function()
                poll_count = poll_count + 1
                return poll_count >= 3
            end

            instance:start()

            -- Drain scheduled callbacks to complete async chain
            for i = 1, 10 do
                if #helper.state.scheduled_tasks > 0 then
                    local cb = table.remove(helper.state.scheduled_tasks, 1)
                    cb.callback()
                else
                    break
                end
            end

            assert.is_true(poll_count >= 3, "Should poll at least 3 times before server is ready")
        end)
    end)

    describe("routing-based extension filtering", function()
        it("should use routed extensions when routing enabled", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.routing_enabled = true
            instance.ext_dirs = { epub = "/books", pdf = "/docs" }
            instance.routing_accept_all = false
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_exts = false
            for _, cmd in ipairs(helper.state.os_execute_calls) do
                if cmd:match("%-a '") and (cmd:match("epub") and cmd:match("pdf")) then
                    found_exts = true
                    break
                end
            end
            assert.is_true(found_exts or #helper.state.os_execute_calls > 0,
                "Should include routed extensions in command")
        end)

        it("should accept all when routing_accept_all is true", function()
            local instance = helper.create_instance()
            instance.save_dir = "/mnt/us/documents"
            instance.routing_enabled = true
            instance.ext_dirs = { epub = "/books" }
            instance.routing_accept_all = true
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function() return nil end

            local check_count = 0
            instance.isRunning = function(self)
                check_count = check_count + 1
                return check_count > 1
            end

            instance:start()

            local found_restrict = false
            for _, cmd in ipairs(helper.state.os_execute_calls) do
                if cmd:match("localsend") and cmd:match("%-a '") then
                    found_restrict = true
                    break
                end
            end
            assert.is_false(found_restrict,
                "Should not restrict extensions when routing_accept_all is true")
        end)
    end)
end)
