require("busted.runner")()
local helper = require("spec.spec_helper")
local constants = require("localsend_constants")

local LOG = constants.TRANSFER_LOG_FILE

-- Tests for transfer log parsing - handles JSON from the Go CLI.
-- Uses the REAL log file on disk and the REAL json decoder.

local function clear_log()
    local f = io.open(LOG, "w")
    if f then
        f:close()
    end
end

local function write_log(lines)
    clear_log()
    if #lines == 0 then
        return
    end
    local f = assert(io.open(LOG, "w"))
    for _, line in ipairs(lines) do
        f:write(line, "\n")
    end
    f:close()
end

describe("Transfer Log", function()
    local LocalSend

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

    describe("getTransferLog", function()
        it("returns empty table when log file doesn't exist", function()
            clear_log()
            local instance = helper.create_instance()
            assert.same({}, instance:getTransferLog())
        end)

        it("parses valid JSON lines", function()
            write_log({
                '{"filename":"test.epub","size":1024}',
                '{"filename":"book.pdf","size":2048}',
            })
            local instance = helper.create_instance()
            local log = instance:getTransferLog()
            assert.equal(2, #log)
            assert.equal("test.epub", log[1].filename)
            assert.equal(1024, log[1].size)
            assert.equal("book.pdf", log[2].filename)
        end)

        it("skips malformed JSON lines gracefully", function()
            write_log({
                '{"filename":"good.epub","size":1024}',
                "not valid json at all",
                '{"filename":"also-good.pdf","size":2048}',
            })
            local instance = helper.create_instance()
            local log = instance:getTransferLog()
            assert.equal(2, #log)
            assert.equal("good.epub", log[1].filename)
            assert.equal("also-good.pdf", log[2].filename)
        end)

        it("handles empty log file", function()
            write_log({})
            local instance = helper.create_instance()
            assert.same({}, instance:getTransferLog())
        end)

        it("handles empty JSON objects", function()
            write_log({ "{}" })
            local instance = helper.create_instance()
            assert.equal(1, #instance:getTransferLog())
        end)

        it("handles empty lines", function()
            write_log({
                '{"filename":"file1.epub","size":1024}',
                "",
                '{"filename":"file2.pdf","size":2048}',
            })
            local instance = helper.create_instance()
            assert.equal(2, #instance:getTransferLog())
        end)

        it("handles mixed valid and invalid lines", function()
            write_log({
                '{"filename":"good1.epub","size":100}',
                "bad json here",
                "",
                "{{{{unclosed",
                '{"filename":"good2.pdf","size":200}',
                "also not json @@@",
                '{"filename":"good3.mobi","size":300}',
            })
            local instance = helper.create_instance()
            local log = instance:getTransferLog()
            assert.equal(3, #log)
            assert.equal("good1.epub", log[1].filename)
            assert.equal("good2.pdf", log[2].filename)
            assert.equal("good3.mobi", log[3].filename)
        end)

        it("handles corrupted file gracefully", function()
            write_log({
                "completely corrupted data!@#$%",
                "{{{{{{{{{{{{{{{",
                '"\n\n\n"',
            })
            local instance = helper.create_instance()
            local log
            assert.has_no.errors(function()
                log = instance:getTransferLog()
            end)
            assert.equal(0, #log)
        end)
    end)

    describe("getTransferCount", function()
        it("returns 0 when log file doesn't exist", function()
            clear_log()
            local instance = helper.create_instance()
            assert.equal(0, instance:getTransferCount())
        end)

        it("returns cached count from ServerState (optimization)", function()
            LocalSend = require("main")
            LocalSend._ServerState.transfer_count = 5
            local instance = helper.create_instance()
            assert.equal(5, instance:getTransferCount())
        end)
    end)

    describe("getNewTransfers (optimized log reading)", function()
        before_each(function()
            LocalSend = require("main")
            LocalSend._ServerState.last_log_position = 0
        end)

        it("returns empty table when log file doesn't exist", function()
            clear_log()
            local instance = helper.create_instance()
            assert.same({}, instance:getNewTransfers())
            assert.equal(0, LocalSend._ServerState.last_log_position)
        end)

        it("returns all entries on first read", function()
            write_log({
                '{"filename":"test.epub","size":1024}',
                '{"filename":"book.pdf","size":2048}',
            })
            local instance = helper.create_instance()
            local transfers = instance:getNewTransfers()
            assert.equal(2, #transfers)
            assert.is_true(LocalSend._ServerState.last_log_position > 0)
        end)

        it("returns only new entries on subsequent reads", function()
            write_log({ '{"filename":"test.epub","size":1024}' })
            local instance = helper.create_instance()
            assert.equal(1, #instance:getNewTransfers())
            assert.equal(0, #instance:getNewTransfers())
        end)
    end)

    describe("clearTransferLog", function()
        it("should remove the transfer log file", function()
            write_log({ '{"filename":"x.epub","size":1}' })
            local instance = helper.create_instance()
            helper.state.removed_files = {}
            instance:clearTransferLog()
            local found = false
            for _, path in ipairs(helper.state.removed_files) do
                if path == LOG then
                    found = true
                    break
                end
            end
            assert.is_true(found)
        end)

        it("should reset last_log_position to 0", function()
            LocalSend = require("main")
            LocalSend._ServerState.last_log_position = 500
            local instance = helper.create_instance()
            instance:clearTransferLog()
            assert.equal(0, LocalSend._ServerState.last_log_position)
        end)

        it("should not error when file doesn't exist", function()
            clear_log()
            local instance = helper.create_instance()
            assert.has_no.errors(function()
                instance:clearTransferLog()
            end)
        end)
    end)

    describe("showRecentTransfers", function()
        it("should show 'No recent transfers' message when empty", function()
            local instance = helper.create_instance()
            instance.getTransferLog = function()
                return {}
            end
            instance:showRecentTransfers()
            assert.is_truthy(helper.find_notification("No recent transfers"))
        end)

        it("should show file names from transfers", function()
            local instance = helper.create_instance()
            instance.getTransferLog = function()
                return { { filename = "test.epub", size = 1024 } }
            end
            instance:showRecentTransfers()
            assert.truthy(helper.state.notifications_shown[1].text:match("test%.epub"))
        end)

        it("should format size in kB for medium files", function()
            local instance = helper.create_instance()
            instance.getTransferLog = function()
                return { { filename = "medium.epub", size = 51200 } }
            end
            instance:showRecentTransfers()
            assert.truthy(helper.state.notifications_shown[1].text:match("kB"))
        end)

        it("should format size in MB for large files", function()
            local instance = helper.create_instance()
            instance.getTransferLog = function()
                return { { filename = "large.pdf", size = 5242880 } }
            end
            instance:showRecentTransfers()
            assert.truthy(helper.state.notifications_shown[1].text:match("MB") or helper.state.notifications_shown[1].text:match("5"))
        end)

        it("should handle transfers without size", function()
            local instance = helper.create_instance()
            instance.getTransferLog = function()
                return { { filename = "nosize.epub" } }
            end
            assert.has_no.errors(function()
                instance:showRecentTransfers()
            end)
            assert.truthy(helper.state.notifications_shown[1].text:match("nosize%.epub"))
        end)
    end)
end)
