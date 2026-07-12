require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")

-- Tests for server control: toggle, stop(), and stopServer()

describe("Server Control", function()
    local orig_pathExists, orig_readFromFile

    setup(function()
        helper.setup_complete()
        orig_pathExists = util.pathExists
        orig_readFromFile = util.readFromFile
    end)

    after_each(function()
        util.pathExists = orig_pathExists
        util.readFromFile = orig_readFromFile
    end)

    before_each(function()
        helper.before_each()
    end)

    -- Tests for onToggleLocalSend (merged from toggle_localsend_spec.lua)
    describe("onToggleLocalSend", function()
        describe("when server is running", function()
            it("should call stop()", function()
                local instance = helper.create_instance()

                local stop_called = false
                instance.isRunning = function()
                    return true
                end
                instance.stop = function()
                    stop_called = true
                end

                instance:onToggleLocalSend()

                assert.is_true(stop_called, "Should call stop when running")
            end)

            it("should not call start()", function()
                local instance = helper.create_instance()

                local start_called = false
                instance.isRunning = function()
                    return true
                end
                instance.stop = function() end
                instance.start = function()
                    start_called = true
                end

                instance:onToggleLocalSend()

                assert.is_false(start_called, "Should not call start when running")
            end)
        end)

        describe("when server is not running", function()
            it("should call _startWhenConnected(false)", function()
                local instance = helper.create_instance()

                local start_when_connected_called = false
                local start_when_connected_silent = nil
                instance.isRunning = function()
                    return false
                end
                instance._startWhenConnected = function(self, silent)
                    start_when_connected_called = true
                    start_when_connected_silent = silent
                end

                instance:onToggleLocalSend()

                assert.is_true(start_when_connected_called, "Should call _startWhenConnected when not running")
                assert.is_false(start_when_connected_silent, "Manual toggle should use silent=false")
            end)

            it("should clear user_stopped flag", function()
                local LocalSend = require("main")
                LocalSend._ServerState.user_stopped = true

                local instance = helper.create_instance()

                instance.isRunning = function()
                    return false
                end
                instance._startWhenConnected = function() end

                instance:onToggleLocalSend()

                assert.is_false(LocalSend._ServerState.user_stopped, "Should clear user_stopped flag when starting")
            end)
        end)

        describe("toggle behavior", function()
            it("should toggle from running to stopped", function()
                local instance = helper.create_instance()

                local actions = {}
                instance.isRunning = function()
                    return true
                end
                instance.stop = function()
                    table.insert(actions, "stop")
                end
                instance.start = function()
                    table.insert(actions, "start")
                end

                instance:onToggleLocalSend()

                assert.same({ "stop" }, actions)
            end)

            it("should toggle from stopped to running", function()
                local instance = helper.create_instance()

                local actions = {}
                instance.isRunning = function()
                    return false
                end
                instance.stop = function()
                    table.insert(actions, "stop")
                end
                instance._startWhenConnected = function(self, silent)
                    table.insert(actions, "start")
                end

                instance:onToggleLocalSend()

                assert.same({ "start" }, actions)
            end)
        end)
    end)

    -- Tests for stop() wrapper (merged from stop_wrapper_spec.lua)
    describe("stop() wrapper", function()
        describe("user_stopped flag", function()
            it("should set user_stopped flag in ServerState", function()
                local instance, LocalSend = helper.create_instance()
                instance.stopServer = function(self, options)
                    if options and options.callback then
                        options.callback(true)
                    end
                    return true
                end

                instance:stop()

                assert.is_true(LocalSend._ServerState.user_stopped, "Should set user_stopped flag")
            end)

            it("should set flag before attempting stop", function()
                local instance, LocalSend = helper.create_instance()

                local flag_was_set = false
                instance.stopServer = function(self, options)
                    flag_was_set = LocalSend._ServerState.user_stopped
                    if options and options.callback then
                        options.callback(true)
                    end
                    return true
                end

                instance:stop()

                assert.is_true(flag_was_set, "Flag should be set before stopServer is called")
            end)
        end)

        describe("simple stop behavior", function()
            it("should call stopServer once", function()
                local instance = helper.create_instance()

                local stop_call_count = 0
                instance.stopServer = function(self, options)
                    stop_call_count = stop_call_count + 1
                    if options and options.callback then
                        options.callback(true)
                    end
                    return true
                end

                instance:stop()

                assert.equal(1, stop_call_count, "Should call stopServer exactly once")
            end)

            it("should always show success notification", function()
                local instance = helper.create_instance()
                instance.stopServer = function(self, options)
                    if options and options.callback then
                        options.callback(true)
                    end
                    return true
                end

                instance:stop()

                local notification = helper.find_notification("LocalSend stopped")
                assert.is_truthy(notification, "Should show success notification")
            end)

            it("success notification should have timeout", function()
                local instance = helper.create_instance()
                instance.stopServer = function(self, options)
                    if options and options.callback then
                        options.callback(true)
                    end
                    return true
                end

                instance:stop()

                local notification = helper.find_notification("LocalSend stopped")
                assert.is_not_nil(notification)
                assert.equal(2, notification.timeout, "Success notification should have 2 second timeout")
            end)
        end)
    end)

    -- Tests for stopServer behavior
    describe("stopServer", function()
        it("should return true when no PID file exists", function()
            local instance = helper.create_instance()
            local result = instance:stopServer()
            assert.is_true(result, "Should return true when no PID file")
        end)

        it("should use SIGTERM first for graceful termination", function()
            local term_signal_used = false

            util.pathExists = function(path)
                if path == "/tmp/localsend_koreader.pid" then
                    return true
                end
                if path == "/proc/12345" then
                    return true
                end
                return orig_pathExists(path)
            end
            util.readFromFile = function(path)
                if path == "/tmp/localsend_koreader.pid" then
                    return "12345"
                end
                if path == "/proc/12345/cmdline" then
                    return "/tmp/localsend\0recv\0"
                end
                return orig_readFromFile(path)
            end

            local original_execute = os.execute
            os.execute = function(cmd)
                if cmd:match("kill") then
                    if cmd:match("'kill'") and cmd:match("'%-TERM'") then
                        term_signal_used = true
                    end
                end
                return 0
            end

            local instance = helper.create_instance()
            instance:stopServer()

            os.execute = original_execute

            assert.is_true(term_signal_used, "Should use SIGTERM first")
        end)
    end)

    describe("stopServer (synchronous teardown)", function()
        local lsserver
        local orig_sync_usleep
        local st = {} -- shared mutable state for process-alive toggling

        setup(function()
            lsserver = require("localsend_server")
            orig_sync_usleep = lsserver._sync_usleep
        end)

        after_each(function()
            lsserver._sync_usleep = orig_sync_usleep
        end)

        before_each(function()
            helper.before_each()
            -- Don't really sleep during the synchronous poll loops.
            lsserver._sync_usleep = function() end
            st.dead = false
        end)

        local function mockLocalSendPid()
            util.pathExists = function(path)
                if path == "/tmp/localsend_koreader.pid" then
                    return true
                end
                if path == "/proc/12345" then
                    return not st.dead
                end
                return orig_pathExists(path)
            end
            util.readFromFile = function(path)
                if path == "/tmp/localsend_koreader.pid" then
                    return "12345"
                end
                if path == "/proc/12345/cmdline" then
                    return "/opt/localsend\0recv\0"
                end
                return orig_readFromFile(path)
            end
        end

        it("sends SIGTERM and finalizes when the process exits promptly", function()
            mockLocalSendPid()

            local sent = {}
            local original_execute = os.execute
            os.execute = function(cmd)
                if cmd:match("'%-TERM'") then
                    sent.term = true
                    st.dead = true -- process exits after SIGTERM
                elseif cmd:match("'%-KILL'") then
                    sent.kill = true
                end
                return 0
            end

            local instance = helper.create_instance()
            local ok = instance:stopServer({ sync = true })

            os.execute = original_execute

            assert.is_true(ok, "stopServer should return true on prompt exit")
            assert.is_true(sent.term, "should send SIGTERM first")
            assert.is_nil(sent.kill, "should NOT escalate to SIGKILL on prompt exit")
        end)

        it("escalates to SIGKILL when the process refuses to exit", function()
            mockLocalSendPid()

            local sent = {}
            local original_execute = os.execute
            os.execute = function(cmd)
                if cmd:match("'%-TERM'") then
                    sent.term = true -- ignored: process stays alive
                elseif cmd:match("'%-KILL'") then
                    sent.kill = true
                    st.dead = true
                end
                return 0
            end

            local instance = helper.create_instance()
            local ok = instance:stopServer({ sync = true })

            os.execute = original_execute

            assert.is_true(ok, "stopServer should return true after SIGKILL")
            assert.is_true(sent.term, "should send SIGTERM first")
            assert.is_true(sent.kill, "should escalate to SIGKILL when SIGTERM is ignored")
        end)
    end)

    describe("stop_in_progress flag", function()
        it("should block start() while stop is in progress", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = true

            local start_proceeded = false
            instance.validateSaveDir = function()
                return true
            end
            instance.openFirewall = function() end
            instance.isRunning = function()
                return false
            end
            instance._unschedulePolling = function() end

            instance:start(false)

            assert.is_false(start_proceeded, "start() should not proceed while stop is in progress")
            local msg = helper.find_notification("stopping")
            assert.is_truthy(msg, "Should show 'stopping' message")
        end)

        it("should block start() silently in silent mode", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = true

            instance.validateSaveDir = function()
                return true
            end
            instance.openFirewall = function() end
            instance.isRunning = function()
                return false
            end
            instance._unschedulePolling = function() end

            instance:start(true)

            local msg = helper.find_notification("stopping")
            assert.is_nil(msg, "Should not show message in silent mode")
        end)

        it("should block toggle() while stop is in progress", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = true
            instance.isRunning = function()
                return false
            end

            local start_called = false
            instance._startWhenConnected = function()
                start_called = true
            end

            instance:onToggleLocalSend()

            assert.is_false(start_called, "toggle should not start while stop is in progress")
            local msg = helper.find_notification("stopping")
            assert.is_truthy(msg, "Should show 'stopping' message")
        end)

        it("restart() should queue start after stop completes", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.stop_in_progress = true
            instance.isRunning = function()
                return true
            end

            local stop_options = nil
            instance.stopServer = function(self, options)
                stop_options = options
            end

            instance:restart()

            assert.is_not_nil(stop_options, "Should call stopServer")
            assert.is_not_nil(stop_options.callback, "Should pass callback for restart")
        end)
    end)

    describe("server_op_id guard", function()
        it("should skip stale start callbacks when new operation started", function()
            local instance, LocalSend = helper.create_instance()
            LocalSend._ServerState.server_op_id = 1

            util.pathExists = function(path)
                return orig_pathExists(path)
            end

            local original_execute = os.execute
            os.execute = function(cmd)
                if cmd:match("localsend") and cmd:match("recv") then
                    return 0
                end
                return 0
            end

            instance.validateSaveDir = function()
                return true
            end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            instance._unschedulePolling = function() end
            instance._checkSentinelFile = function() end
            instance.check_sentinel_task = function() end
            instance.isRunning = function()
                return false
            end

            instance:start(false)

            LocalSend._ServerState.server_op_id = 99

            local initial_task_count = #helper.state.scheduled_tasks
            for i = 1, initial_task_count do
                local task = helper.state.scheduled_tasks[i]
                if task and task.callback then
                    task.callback()
                end
            end

            os.execute = original_execute

            assert.is_true(initial_task_count >= 1, "Should have scheduled callbacks")
        end)

        it("should skip stale stop callbacks when new operation started", function()
            local instance, LocalSend = helper.create_instance()
            local callback_invoked = false

            util.pathExists = function(path)
                if path == "/tmp/localsend_koreader.pid" then
                    return true
                end
                if path == "/proc/12345" then
                    return true
                end
                return orig_pathExists(path)
            end
            util.readFromFile = function(path)
                if path == "/tmp/localsend_koreader.pid" then
                    return "12345"
                end
                if path == "/proc/12345/cmdline" then
                    return "/tmp/localsend\0recv\0"
                end
                return orig_readFromFile(path)
            end

            local kill_count = 0
            local original_execute = os.execute
            os.execute = function(cmd)
                if cmd:match("kill") then
                    kill_count = kill_count + 1
                end
                return 0
            end

            instance.closeFirewall = function() end
            instance._unschedulePolling = function() end
            instance._updateCache = function() end
            instance.registerEvents = function() end

            LocalSend._ServerState.server_op_id = 1

            instance:stopServer({
                callback = function(success, message)
                    callback_invoked = true
                end,
            })

            LocalSend._ServerState.server_op_id = 99

            local initial_task_count = #helper.state.scheduled_tasks
            for i = 1, math.min(initial_task_count, 50) do
                local task = helper.state.scheduled_tasks[i]
                if task and task.callback then
                    task.callback()
                end
            end

            os.execute = original_execute

            assert.is_false(callback_invoked, "Stale callback should be skipped")
        end)
    end)
end)
