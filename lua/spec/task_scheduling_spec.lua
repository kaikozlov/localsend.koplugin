require("busted.runner")()
local helper = require("spec.spec_helper")
local constants = require("localsend_constants")

-- Tests for LocalSend task scheduling patterns (Issues #1 and #2 from optimization doc)
-- These tests verify proper UIManager task management to prevent battery drain

describe("LocalSend Task Scheduling", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
        os.remove(constants.TRANSFER_LOG_FILE)
    end)

    describe("task reference initialization", function()
        it("task references should be instance-specific (not shared)", function()
            local instance1 = helper.create_instance()
            local instance2 = helper.create_instance()

            assert.are_not.equal(instance1.poll_receiver_task, instance2.poll_receiver_task, "poll_receiver_task should be unique per instance")
            assert.are_not.equal(instance1.resume_start_task, instance2.resume_start_task, "resume_start_task should be unique per instance")
        end)
    end)

    describe("unschedule helper methods", function()
        it("_unschedulePolling should call UIManager:unschedule with the receiver poll task", function()
            local instance = helper.create_instance()

            helper.state.unscheduled_tasks = {}
            instance:_unschedulePolling()

            assert.equal(1, #helper.state.unscheduled_tasks, "Should have called UIManager:unschedule once")
            assert.equal(instance.poll_receiver_task, helper.state.unscheduled_tasks[1], "Should unschedule poll_receiver_task")
        end)

        it("_unscheduleResume should call UIManager:unschedule with task reference", function()
            local instance = helper.create_instance()

            helper.state.unscheduled_tasks = {}
            instance:_unscheduleResume()

            assert.equal(1, #helper.state.unscheduled_tasks, "Should have called UIManager:unschedule once")
            assert.equal(instance.resume_start_task, helper.state.unscheduled_tasks[1], "Should unschedule resume_start_task")
        end)
    end)

    -- NOTE: onCloseWidget and onResume general behavior tests are in lifecycle_spec.lua
    -- This file only tests task-scheduling-specific behavior

    describe("checkForNewTransfers behavior", function()
        it("should not self-schedule (receiver polling handles scheduling)", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return true
            end
            instance.getNewTransfers = function()
                return {}
            end

            -- Clear tasks from init
            helper.state.scheduled_tasks = {}

            -- Call the internal check method
            instance:_checkForNewTransfers()

            assert.equal(0, #helper.state.scheduled_tasks, "Should NOT self-schedule (receiver poll handles that)")
        end)

        it("should NOT run if server stopped", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return false
            end
            local getNewTransfers_called = false
            instance.getNewTransfers = function()
                getNewTransfers_called = true
                return {}
            end

            instance:_checkForNewTransfers()

            assert.is_false(getNewTransfers_called, "Should NOT check transfers when server not running")
        end)
    end)

    describe("start() uses stored task reference", function()
        it("when server already running, should schedule receiver polling", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return true
            end

            -- Clear tasks from init
            helper.state.scheduled_tasks = {}

            instance:start()

            assert.equal(1, #helper.state.scheduled_tasks, "Should schedule the receiver poll only")
            assert.equal(instance.poll_receiver_task, helper.state.scheduled_tasks[1].callback)
        end)
    end)

    describe("_pollReceiver behavior", function()
        local function append_log(line)
            local f = assert(io.open(constants.TRANSFER_LOG_FILE, "a"))
            f:write(line)
            f:close()
        end

        it("announces each appended transfer exactly once", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end

            instance:_pollReceiver()
            assert.equal(0, #helper.state.notifications_shown, "No log yet: nothing to announce")

            append_log('{"filename":"one.epub","size":1}\n')
            instance:_pollReceiver()
            instance:_pollReceiver()
            assert.equal(1, #helper.state.notifications_shown)
            assert.is_truthy(helper.find_notification("one%.epub"))
        end)

        it("waits for a partially written log line to be completed", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end

            append_log('{"filename":"half.epub",')
            instance:_pollReceiver()
            assert.is_nil(helper.find_notification("half%.epub"))

            append_log('"size":1}\n')
            instance:_pollReceiver()
            assert.is_truthy(helper.find_notification("^File received: half%.epub$"))
        end)

        it("should not announce or reschedule when server not running", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return false
            end
            append_log('{"filename":"late.epub","size":1}\n')

            helper.reset_state()
            instance:_pollReceiver()

            assert.is_nil(helper.find_notification("late%.epub"))
            assert.equal(0, #helper.state.scheduled_tasks, "Should not schedule next check when not running")
        end)

        it("holds KOReader awake only while the backend busy marker exists", function()
            local instance = helper.create_instance()
            local constants = require("localsend_constants")
            local power = require("localsend_power")
            local util = require("util")
            local original_pathExists = util.pathExists
            finally(function()
                util.pathExists = original_pathExists
                power.release("receive")
            end)

            instance.isRunning = function()
                return true
            end
            util.pathExists = function(path)
                if path == constants.TRANSFER_BUSY_FILE then
                    return true
                end
                return original_pathExists(path)
            end
            instance:_pollReceiver()
            assert.is_true(power.isHeld("receive"))

            util.pathExists = function(path)
                if path == constants.TRANSFER_BUSY_FILE then
                    return false
                end
                return original_pathExists(path)
            end
            instance:_pollReceiver()
            assert.is_false(power.isHeld("receive"))
        end)

        it("should reschedule itself when server running", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return true
            end

            helper.state.scheduled_tasks = {}
            instance:_pollReceiver()

            assert.equal(1, #helper.state.scheduled_tasks, "Should schedule next receiver poll")
            assert.equal(instance.poll_receiver_task, helper.state.scheduled_tasks[1].callback)
            assert.equal(2, helper.state.scheduled_tasks[1].delay, "Should schedule with 2 second interval")
        end)

        it("should update cache and cleanup when server dies", function()
            local instance = helper.create_instance()

            -- Simulate server was running (cache says true)
            instance._cached_running = true
            package.loaded["pluginshare"].localsend_running = true

            -- But now isRunning returns false (server died)
            instance.isRunning = function()
                return false
            end

            helper.state.scheduled_tasks = {}
            instance:_pollReceiver()

            -- Should have updated cache to reflect server death
            assert.is_false(instance._cached_running, "_pollReceiver should update cache when server dies")

            -- Should have cleared PluginShare
            assert.is_nil(package.loaded["pluginshare"].localsend_running, "_pollReceiver should clear PluginShare when server dies")

            -- Should NOT schedule next check (server is dead)
            assert.equal(0, #helper.state.scheduled_tasks, "Should not schedule next check when server is dead")
        end)
    end)

    describe("task reference recovery after onCloseWidget", function()
        it("_onServerStarted should recreate poll_receiver_task if nil", function()
            local instance = helper.create_instance()

            -- Simulate onCloseWidget having nullified the task
            instance.poll_receiver_task = nil

            -- Mock dependencies for _onServerStarted
            instance.isRunning = function()
                return true
            end

            helper.state.scheduled_tasks = {}
            instance:_onServerStarted(true, "TestDevice")

            -- Should have scheduled the recreated task
            assert.equal(1, #helper.state.scheduled_tasks, "Should schedule the recreated receiver poll")
            assert.equal(
                instance.poll_receiver_task,
                helper.state.scheduled_tasks[1].callback,
                "Should schedule the newly created poll_receiver_task"
            )
        end)

        -- NOTE: Full suspend/resume integration is tested in lifecycle_spec.lua
        -- This file only tests task reference recovery, not the full lifecycle
    end)
end)
