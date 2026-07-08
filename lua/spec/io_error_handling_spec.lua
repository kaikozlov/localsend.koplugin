require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")

-- Tests for io.open() / readFromFile failure handling.
-- Real util methods are wrapped (save+restore) so the binary shim is still
-- found while we inject failure for specific runtime files.

describe("I/O Error Handling", function()
    local LocalSend
    local path_exists_map
    local orig_pathExists, orig_readFromFile, orig_io_open

    setup(function()
        helper.setup_complete()
        orig_pathExists = util.pathExists
        orig_readFromFile = util.readFromFile
        orig_io_open = io.open
    end)

    teardown(function()
        util.pathExists = orig_pathExists
        util.readFromFile = orig_readFromFile
        io.open = orig_io_open
    end)

    before_each(function()
        helper.before_each()
        path_exists_map = {}
        _G._test_readFromFile_returns_nil = nil
        _G._test_readFromFile_content = nil

        util.pathExists = function(path)
            if path_exists_map[path] ~= nil then
                return path_exists_map[path]
            end
            return orig_pathExists(path)
        end

        util.readFromFile = function(path)
            if path:match("pid$") and _G._test_readFromFile_returns_nil then
                return nil
            end
            return _G._test_readFromFile_content
        end
        io.open = orig_io_open
    end)

    describe("io.open() failure handling", function()
        describe("isRunning", function()
            it("returns false when PID file exists but cannot be read", function()
                path_exists_map["/tmp/localsend_koreader.pid"] = true
                _G._test_readFromFile_returns_nil = true

                local instance = helper.create_instance()
                assert.is_false(instance:isRunning())
            end)
        end)

        describe("getTransferLog", function()
            it("returns empty table when log file exists but cannot be opened", function()
                path_exists_map["/tmp/localsend_transfers.log"] = true
                io.open = function(path, mode)
                    if path:match("transfers%.log$") then
                        return nil
                    end
                    return orig_io_open(path, mode)
                end

                local instance = helper.create_instance()
                assert.same({}, instance:getTransferLog())
            end)
        end)

        describe("getTransferCount", function()
            it("returns 0 when log file exists but cannot be opened", function()
                path_exists_map["/tmp/localsend_transfers.log"] = true
                io.open = function(path, mode)
                    if path:match("transfers%.log$") then
                        return nil
                    end
                    return orig_io_open(path, mode)
                end

                local instance = helper.create_instance()
                assert.equal(0, instance:getTransferCount())
            end)
        end)

        describe("exportExtRouting", function()
            it("returns nil when config file cannot be opened for writing", function()
                io.open = function(path, mode)
                    if path:match("ext_routing%.json$") then
                        return nil
                    end
                    return orig_io_open(path, mode)
                end

                local instance = helper.create_instance()
                instance.routing_enabled = true
                instance.ext_dirs = { epub = "/books" }
                assert.is_nil(instance:exportExtRouting())
            end)
        end)
    end)

    describe("JSON encode failure handling", function()
        it("returns nil when json.encode throws", function()
            local json = require("json")
            local orig_encode = json.encode
            json.encode = function(_t)
                error("encode failed")
            end

            local instance = helper.create_instance()
            instance.routing_enabled = true
            instance.ext_dirs = { epub = "/books" }
            assert.is_nil(instance:exportExtRouting())

            json.encode = orig_encode
        end)
    end)
end)
