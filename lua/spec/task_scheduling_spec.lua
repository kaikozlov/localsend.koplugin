require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")

-- Tests for LocalSend task scheduling patterns (Issues #1 and #2 from optimization doc)
-- These tests verify proper UIManager task management to prevent battery drain

describe("LocalSend Task Scheduling", function()
    local orig_readFromFile

    setup(function()
        helper.setup_complete()
        orig_readFromFile = util.readFromFile
    end)

    after_each(function()
        util.readFromFile = orig_readFromFile
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("task reference initialization", function()
        it("should create check_sentinel_task function in init()", function()
            local instance = helper.create_instance()

            assert.is_function(instance.check_sentinel_task, "check_sentinel_task should be created in init()")
        end)

        it("should create resume_start_task function in init()", function()
            local instance = helper.create_instance()

            assert.is_function(instance.resume_start_task, "resume_start_task should be created in init()")
        end)

        it("task references should be instance-specific (not shared)", function()
            local instance1 = helper.create_instance()
            local instance2 = helper.create_instance()

            assert.are_not.equal(instance1.check_sentinel_task, instance2.check_sentinel_task, "check_sentinel_task should be unique per instance")
            assert.are_not.equal(instance1.resume_start_task, instance2.resume_start_task, "resume_start_task should be unique per instance")
        end)
    end)

    describe("unschedule helper methods", function()
        it("should have _unschedulePolling method", function()
            local instance = helper.create_instance()

            assert.is_function(instance._unschedulePolling, "_unschedulePolling helper should exist")
        end)

        it("should have _unscheduleResume method", function()
            local instance = helper.create_instance()

            assert.is_function(instance._unscheduleResume, "_unscheduleResume helper should exist")
        end)

        it("_unschedulePolling should call UIManager:unschedule with sentinel task", function()
            local instance = helper.create_instance()

            helper.state.unscheduled_tasks = {}
            instance:_unschedulePolling()

            assert.equal(1, #helper.state.unscheduled_tasks, "Should have called UIManager:unschedule once (sentinel only)")
            assert.equal(instance.check_sentinel_task, helper.state.unscheduled_tasks[1], "Should unschedule check_sentinel_task")
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
        it("should not self-schedule (sentinel polling handles scheduling)", function()
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

            -- Should NOT schedule any tasks - sentinel polling handles that
            assert.equal(0, #helper.state.scheduled_tasks, "Should NOT self-schedule (sentinel handles polling)")
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
        it("when server already running, should schedule sentinel polling", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return true
            end

            -- Clear tasks from init
            helper.state.scheduled_tasks = {}

            instance:start()

            assert.equal(1, #helper.state.scheduled_tasks, "Should schedule sentinel task only")
            assert.equal(instance.check_sentinel_task, helper.state.scheduled_tasks[1].callback, "Should schedule check_sentinel_task for fast notifications")
        end)
    end)

    describe("_checkSentinelFile behavior", function()
        it("should trigger transfer check when sentinel content changes", function()
            local LocalSend = require("main")
            local instance = helper.create_instance()

            instance.isRunning = function()
                return true
            end

            local transfer_check_count = 0
            instance._checkForNewTransfers = function()
                transfer_check_count = transfer_check_count + 1
            end

            -- First call: sets last_sentinel_value AND triggers (first transfer fix)
            LocalSend._ServerState.last_sentinel_value = nil
            package.loaded["util"].readFromFile = function()
                return "12345"
            end
            instance:_checkSentinelFile()
            assert.equal(1, transfer_check_count, "First call should trigger (handles first transfer)")
            assert.equal("12345", LocalSend._ServerState.last_sentinel_value)

            -- Second call with same value: no trigger
            instance:_checkSentinelFile()
            assert.equal(1, transfer_check_count, "Same value should not trigger")

            -- Third call with different value: should trigger
            package.loaded["util"].readFromFile = function()
                return "67890"
            end
            instance:_checkSentinelFile()
            assert.equal(2, transfer_check_count, "Different value should trigger transfer check")
            assert.equal("67890", LocalSend._ServerState.last_sentinel_value)
        end)

        it("should not check if server not running", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return false
            end

            local read_called = false
            package.loaded["util"].readFromFile = function()
                read_called = true
                return "12345"
            end

            helper.state.scheduled_tasks = {}
            instance:_checkSentinelFile()

            assert.is_false(read_called, "Should not read file when server not running")
            assert.equal(0, #helper.state.scheduled_tasks, "Should not schedule next check when not running")
        end)

        it("should reschedule itself when server running", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return true
            end
            package.loaded["util"].readFromFile = function()
                return nil
            end

            helper.state.scheduled_tasks = {}
            instance:_checkSentinelFile()

            assert.equal(1, #helper.state.scheduled_tasks, "Should schedule next sentinel check")
            assert.equal(instance.check_sentinel_task, helper.state.scheduled_tasks[1].callback)
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
            instance:_checkSentinelFile()

            -- Should have updated cache to reflect server death
            assert.is_false(instance._cached_running, "_checkSentinelFile should update cache when server dies")

            -- Should have cleared PluginShare
            assert.is_nil(package.loaded["pluginshare"].localsend_running, "_checkSentinelFile should clear PluginShare when server dies")

            -- Should NOT schedule next check (server is dead)
            assert.equal(0, #helper.state.scheduled_tasks, "Should not schedule next check when server is dead")
        end)
    end)

    describe("task reference recovery after onCloseWidget", function()
        it("_onServerStarted should recreate check_sentinel_task if nil", function()
            local instance = helper.create_instance()

            -- Simulate onCloseWidget having nullified the task
            instance.check_sentinel_task = nil

            -- Mock dependencies for _onServerStarted
            instance.isRunning = function()
                return true
            end

            helper.state.scheduled_tasks = {}
            instance:_onServerStarted(true, "TestDevice")

            -- check_sentinel_task should have been recreated
            assert.is_function(instance.check_sentinel_task, "check_sentinel_task should be recreated if nil")

            -- Should have scheduled the recreated task
            assert.equal(1, #helper.state.scheduled_tasks, "Should schedule the recreated sentinel task")
            assert.equal(instance.check_sentinel_task, helper.state.scheduled_tasks[1].callback, "Should schedule the newly created check_sentinel_task")
        end)

        -- NOTE: Full suspend/resume integration is tested in lifecycle_spec.lua
        -- This file only tests task reference recovery, not the full lifecycle
    end)
end)
