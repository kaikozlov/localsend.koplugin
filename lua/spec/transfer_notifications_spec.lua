require("busted.runner")()
local helper = require("spec.spec_helper")
local constants = require("localsend_constants")

local LOG = constants.TRANSFER_LOG_FILE

-- Tests for _checkForNewTransfers - polling for new file notifications.
-- Exercises the REAL transfer log on disk and the REAL json decoder.

-- Truncate the log to zero bytes (robust even if a leaked stub replaced os.remove).
local function clear_log()
    local f = io.open(LOG, "w")
    if f then
        f:close()
    end
end

local function write_log(lines)
    clear_log()
    if #lines == 0 then
        return 0
    end
    local f = assert(io.open(LOG, "w"))
    local total = 0
    for _, line in ipairs(lines) do
        f:write(line, "\n")
        total = total + #line + 1
    end
    f:close()
    return total
end

local function bytes_through(lines, n)
    local total = 0
    for i = 1, n do
        total = total + #lines[i] + 1
    end
    return total
end

describe("checkForNewTransfers", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
        clear_log()
    end)

    teardown(function()
        clear_log()
    end)

    describe("when server is not running", function()
        it("does nothing and does not schedule next check", function()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return false
            end
            helper.reset_state()
            instance:_checkForNewTransfers()
            assert.equal(0, #helper.state.notifications_shown)
            assert.equal(0, #helper.state.scheduled_tasks)
        end)
    end)

    describe("when server is running", function()
        local ServerState

        before_each(function()
            ServerState = require("main")._ServerState
            ServerState.last_log_position = 0
        end)

        it("shows notification for single new transfer", function()
            write_log({ '{"filename":"book.epub","size":1024}' })
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            instance:_checkForNewTransfers()
            assert.equal(1, #helper.state.notifications_shown)
            assert.truthy(helper.state.notifications_shown[1].text:match("received"))
        end)

        it("shows notification for multiple new transfers", function()
            write_log({
                '{"filename":"book1.epub","size":1024}',
                '{"filename":"book2.pdf","size":2048}',
                '{"filename":"book3.mobi","size":3072}',
            })
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            instance:_checkForNewTransfers()
            assert.equal(1, #helper.state.notifications_shown)
            assert.truthy(helper.state.notifications_shown[1].text:match("received"))
        end)

        it("does not show notification when no new transfers", function()
            local lines = { '{"filename":"old.epub","size":1024}' }
            local total = write_log(lines)
            ServerState.last_log_position = total
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            helper.reset_state()
            instance:_checkForNewTransfers()
            assert.equal(0, #helper.state.notifications_shown)
        end)

        it("updates last_log_position after checking", function()
            write_log({ '{"filename":"book.epub","size":1024}' })
            ServerState.last_log_position = 0
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            instance:_checkForNewTransfers()
            assert.is_true(ServerState.last_log_position > 0)
        end)

        it("does not self-schedule (sentinel polling handles scheduling)", function()
            clear_log()
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            helper.reset_state()
            instance:_checkForNewTransfers()
            assert.equal(0, #helper.state.scheduled_tasks)
        end)
    end)

    describe("incremental detection", function()
        it("only notifies about new transfers, not old ones", function()
            local lines = {
                '{"filename":"old1.epub","size":1024}',
                '{"filename":"old2.epub","size":2048}',
                '{"filename":"new.pdf","size":3072}',
            }
            local ServerState = require("main")._ServerState
            write_log(lines)
            ServerState.last_log_position = bytes_through(lines, 2)

            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            helper.reset_state()
            instance:_checkForNewTransfers()
            assert.equal(1, #helper.state.notifications_shown)
            assert.truthy(helper.state.notifications_shown[1].text:match("received"))
        end)
    end)

    describe("notification widget type", function()
        local ServerState

        before_each(function()
            ServerState = require("main")._ServerState
            ServerState.last_log_position = 0
        end)

        it("should use Notification (toast) instead of InfoMessage (modal)", function()
            write_log({ '{"filename":"book.epub","size":1024}' })
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            helper.reset_state()
            instance:_checkForNewTransfers()
            assert.equal(1, #helper.state.notifications_shown)
            local notification = helper.state.notifications_shown[1]
            assert.equal("Notification", helper.widget_class(notification))
            assert.truthy(notification.text:match("received"))
        end)

        it("should set appropriate timeout for toast notifications", function()
            write_log({ '{"filename":"book.epub","size":1024}' })
            local instance = helper.create_instance()
            instance.isRunning = function()
                return true
            end
            instance:_checkForNewTransfers()
            local timeout = helper.state.notifications_shown[1].timeout
            assert.is_truthy(timeout)
            assert.is_true(timeout >= 2 and timeout <= 5)
        end)
    end)
end)
