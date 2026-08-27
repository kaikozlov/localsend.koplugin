require("busted.runner")()
local helper = require("spec.spec_helper")
local NetworkMgr = require("ui/network/manager")

describe("Receiver crash recovery", function()
    local original_is_connected

    setup(function()
        helper.setup_complete()
        original_is_connected = NetworkMgr.isConnected
    end)

    teardown(function()
        NetworkMgr.isConnected = original_is_connected
    end)

    before_each(function()
        helper.before_each()
        NetworkMgr.isConnected = function()
            return true
        end
    end)

    local function dead_intended_receiver()
        local instance, LocalSend = helper.create_instance()
        local ServerState = LocalSend._ServerState
        ServerState.server_intended_running = true
        ServerState.server_started_at = os.time()
        instance.isRunning = function()
            return false
        end
        helper.reset_state()
        return instance, ServerState
    end

    it("relaunches a receiver process that exits unexpectedly", function()
        local instance = dead_intended_receiver()
        local starts = {}
        instance.start = function(_, silent, options)
            table.insert(starts, { silent = silent, recovery = options and options.recovery })
        end

        instance:_checkSentinelFile()

        assert.equal(1, #helper.state.scheduled_tasks)
        assert.equal(2, helper.state.scheduled_tasks[1].delay)
        assert.equal(0, #starts)
        helper.state.scheduled_tasks[1].callback()
        assert.same({ { silent = true, recovery = true } }, starts)
    end)

    it("debounces duplicate death observations into one restart", function()
        local instance = dead_intended_receiver()

        instance:_checkSentinelFile()
        instance:_checkSentinelFile()

        assert.equal(1, #helper.state.scheduled_tasks)
    end)

    it("backs off and stops retrying a persistently crashing receiver", function()
        local instance, ServerState = dead_intended_receiver()
        local lsserver = require("localsend_server")
        local starts = 0
        instance.start = function(self)
            starts = starts + 1
            lsserver.onServerStartFailed(self, true)
        end

        instance:_checkSentinelFile()
        local task_index = 1
        while helper.state.scheduled_tasks[task_index] do
            helper.state.scheduled_tasks[task_index].callback()
            task_index = task_index + 1
        end

        assert.equal(5, starts)
        assert.same(
            { 2, 4, 8, 16, 30 },
            (function()
                local delays = {}
                for _, task in ipairs(helper.state.scheduled_tasks) do
                    table.insert(delays, task.delay)
                end
                return delays
            end)()
        )
        assert.is_true(ServerState.server_restart_exhausted)
    end)

    it("does not restart after an intentional user stop", function()
        local instance = dead_intended_receiver()
        local started = false
        instance.start = function()
            started = true
        end

        instance:_checkSentinelFile()
        local stale_callback = helper.state.scheduled_tasks[1].callback
        instance.stopServer = function(_, options)
            options.callback(true)
            return true
        end
        instance:stop()
        stale_callback()

        assert.is_false(started)
    end)

    it("does not restart while an intentional stop is in progress", function()
        local instance, ServerState = dead_intended_receiver()
        ServerState.stop_in_progress = true

        instance:_checkSentinelFile()

        assert.equal(0, #helper.state.scheduled_tasks)
    end)

    it("does not restart during suspend teardown", function()
        local instance = dead_intended_receiver()
        local started = false
        instance.start = function()
            started = true
        end
        instance:_checkSentinelFile()
        local stale_callback = helper.state.scheduled_tasks[1].callback

        instance:_onSuspend()
        stale_callback()

        assert.is_false(started)
    end)

    it("does not restart during network teardown", function()
        local instance = dead_intended_receiver()
        local started = false
        instance.start = function()
            started = true
        end
        instance:_checkSentinelFile()
        local stale_callback = helper.state.scheduled_tasks[1].callback

        instance:_onNetworkDisconnecting()
        stale_callback()

        assert.is_false(started)
    end)

    it("does not restart during plugin exit", function()
        local instance = dead_intended_receiver()
        local started = false
        instance.start = function()
            started = true
        end
        instance:_checkSentinelFile()
        local stale_callback = helper.state.scheduled_tasks[1].callback

        instance:onExit()
        stale_callback()

        assert.is_false(started)
    end)
end)
