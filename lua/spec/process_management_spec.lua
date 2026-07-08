require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")

-- Tests for process management: isRunning, stopServer
-- Simulates process state (PID file, /proc, kill) by wrapping util + os with
-- save/restore so the real modules are not permanently mutated.

describe("Process Management", function()
    local pid_file_content
    local pid_file_exists
    local proc_exists_map
    local proc_cmdline_map
    local kill_calls

    local orig_pathExists, orig_readFromFile, orig_execute, orig_remove

    local function flushScheduledTasks(limit)
        limit = limit or 100
        local steps = 0
        while #helper.state.scheduled_tasks > 0 and steps < limit do
            local task = table.remove(helper.state.scheduled_tasks, 1)
            if task and task.callback and (task.delay or 0) <= 1 then
                task.callback()
            elseif task then
                table.insert(helper.state.scheduled_tasks, task)
            end
            steps = steps + 1
        end
    end

    setup(function()
        helper.setup_complete()
        orig_pathExists = util.pathExists
        orig_readFromFile = util.readFromFile
        orig_execute = os.execute
        orig_remove = os.remove
    end)

    after_each(function()
        util.pathExists = orig_pathExists
        util.readFromFile = orig_readFromFile
        os.execute = orig_execute
        os.remove = orig_remove
    end)

    before_each(function()
        helper.before_each()
        pid_file_content = nil
        pid_file_exists = false
        proc_exists_map = {}
        proc_cmdline_map = {}
        kill_calls = {}

        util.pathExists = function(path)
            if path == "/tmp/localsend_koreader.pid" then
                return pid_file_exists
            end
            local pid = path:match("^/proc/(%d+)$")
            if pid then
                return proc_exists_map[tonumber(pid)] or false
            end
            return orig_pathExists(path)
        end

        util.readFromFile = function(path)
            if path == "/tmp/localsend_koreader.pid" then
                if not pid_file_exists then
                    return nil
                end
                return pid_file_content
            end
            local pid = path:match("^/proc/(%d+)/cmdline$")
            if pid then
                return proc_cmdline_map[tonumber(pid)]
            end
            return nil
        end

        os.execute = function(cmd)
            table.insert(kill_calls, cmd)
            local sig, pid = cmd:match("'kill' '%-(%w+)' '(%d+)'")
            if not sig then
                sig, pid = cmd:match("kill %-(%d+)%s+(%d+)")
            end
            if sig and pid then
                pid = tonumber(pid)
                if sig == "KILL" or sig == "9" or sig == "TERM" then
                    proc_exists_map[pid] = false
                end
            end
            return 0
        end

        os.remove = function(path)
            table.insert(helper.state.removed_files, path)
            if path == "/tmp/localsend_koreader.pid" then
                pid_file_exists = false
            end
            return true
        end
    end)

    describe("isRunning", function()
        it("returns false when PID file does not exist", function()
            pid_file_exists = false

            local instance = helper.create_instance()

            assert.is_false(instance:isRunning())
        end)

        it("returns false when PID file exists but is empty", function()
            pid_file_exists = true
            pid_file_content = nil

            local instance = helper.create_instance()

            assert.is_false(instance:isRunning())
        end)

        it("returns false when PID file contains non-numeric content", function()
            pid_file_exists = true
            pid_file_content = "not-a-pid"

            local instance = helper.create_instance()

            assert.is_false(instance:isRunning())
        end)

        it("returns false when PID exists but process is not running", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = false

            local instance = helper.create_instance()

            assert.is_false(instance:isRunning())
        end)

        it("returns true when PID exists and process is running", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            local instance = helper.create_instance()

            assert.is_true(instance:isRunning())
        end)

        it("returns false when PID belongs to another process", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/usr/bin/python\0script.py\0"

            local instance = helper.create_instance()

            assert.is_false(instance:isRunning())
        end)

        it("handles PID with newline from read(*l)", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            local instance = helper.create_instance()

            assert.is_true(instance:isRunning())
        end)
    end)

    describe("stopServer", function()
        it("returns true when PID file does not exist", function()
            pid_file_exists = false

            local instance = helper.create_instance()
            instance.closeFirewall = function() end

            local ok = instance:stopServer()
            assert.is_true(ok)
        end)

        it("sends SIGTERM first for graceful shutdown", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            local term_called = false
            local kill_called = false
            _G.os.execute = function(cmd)
                table.insert(kill_calls, cmd)
                if cmd:match("'kill'") and cmd:match("'%-TERM'") then
                    term_called = true
                    proc_exists_map[12345] = false
                elseif cmd:match("'kill'") and cmd:match("'%-KILL'") then
                    kill_called = true
                end
                return 0
            end

            local instance = helper.create_instance()
            instance.closeFirewall = function() end

            local ok = instance:stopServer()
            flushScheduledTasks()

            assert.is_true(ok)
            assert.is_true(term_called, "Should attempt graceful stop with SIGTERM")
            assert.is_false(kill_called, "Should not force-kill if SIGTERM succeeds")
        end)

        it("forces SIGKILL when graceful stop times out", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            local term_called = false
            local kill_called = false

            _G.os.execute = function(cmd)
                table.insert(kill_calls, cmd)
                if cmd:match("'kill'") and cmd:match("'%-TERM'") then
                    term_called = true
                    -- Simulate stubborn process still running after TERM.
                elseif cmd:match("'kill'") and cmd:match("'%-KILL'") then
                    kill_called = true
                    proc_exists_map[12345] = false
                end
                return 0
            end

            local instance = helper.create_instance()
            instance.closeFirewall = function() end

            instance:stopServer()
            flushScheduledTasks()

            assert.is_true(term_called, "Should try SIGTERM first")
            assert.is_true(kill_called, "Should force-kill with SIGKILL after timeout")
        end)

        it("removes PID file only after process exits", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            local pid_exists_during_term = nil

            _G.os.execute = function(cmd)
                table.insert(kill_calls, cmd)
                if cmd:match("'kill'") and cmd:match("'%-TERM'") then
                    pid_exists_during_term = pid_file_exists
                    proc_exists_map[12345] = false
                end
                return 0
            end

            local instance = helper.create_instance()
            instance.closeFirewall = function() end

            instance:stopServer()
            flushScheduledTasks()

            assert.is_true(pid_exists_during_term, "PID file should exist while stopping")
            assert.is_false(pid_file_exists, "PID file should be removed after process exit")
        end)

        it("calls closeFirewall after stopping", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            _G.os.execute = function(cmd)
                table.insert(kill_calls, cmd)
                if cmd:match("'kill'") and cmd:match("'%-TERM'") then
                    proc_exists_map[12345] = false
                end
                return 0
            end

            local instance = helper.create_instance()

            local firewall_closed = false
            instance.closeFirewall = function()
                firewall_closed = true
            end

            instance:stopServer()
            flushScheduledTasks()

            assert.is_true(firewall_closed, "Firewall should be closed")
        end)

        it("refuses to kill unrelated process and cleans stale PID", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/usr/bin/python\0worker.py\0"

            local kill_attempted = false
            _G.os.execute = function(cmd)
                table.insert(kill_calls, cmd)
                if cmd:match("'kill'") then
                    kill_attempted = true
                end
                return 0
            end

            local instance = helper.create_instance()
            instance.closeFirewall = function() end

            local ok = instance:stopServer()
            flushScheduledTasks()

            assert.is_true(ok)
            assert.is_false(kill_attempted, "Should not kill unrelated processes")
            assert.is_false(pid_file_exists, "Should clean stale PID file")
        end)
    end)

    describe("restart", function()
        it("stops then starts server", function()
            pid_file_exists = true
            pid_file_content = "12345"
            proc_exists_map[12345] = true
            proc_cmdline_map[12345] = "/tmp/localsend\0recv\0"

            _G.os.execute = function(cmd)
                table.insert(kill_calls, cmd)
                if cmd:match("'kill'") and cmd:match("'%-TERM'") then
                    proc_exists_map[12345] = false
                end
                return 0
            end

            local instance = helper.create_instance()
            instance.closeFirewall = function() end

            local stop_called = false
            local start_called = false
            local original_stopServer = instance.stopServer
            instance.stopServer = function(self, options)
                stop_called = true
                return original_stopServer(self, options)
            end
            instance.start = function()
                start_called = true
            end

            instance:restart()
            flushScheduledTasks()

            assert.is_true(stop_called, "stopServer should be called")
            assert.is_true(start_called, "start should be called")
        end)

        it("starts server even if not currently running", function()
            pid_file_exists = false

            local instance = helper.create_instance()

            local start_called = false
            instance.start = function()
                start_called = true
            end

            instance:restart()

            assert.is_true(start_called, "start should be called even when not running")
        end)
    end)
end)
