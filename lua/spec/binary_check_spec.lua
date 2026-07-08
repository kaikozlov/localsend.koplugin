require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for binary existence check behavior.
-- main.lua evaluates util.pathExists(binary) at module load and returns a
-- disabled table when the binary is absent. We exercise this against the REAL
-- filesystem by creating/removing the actual shim binary the helper installs.

describe("Binary Existence Check", function()
    local bin_path

    setup(function()
        helper.setup_complete()
        bin_path = helper.runtime_plugin_dir() .. "/localsend"
    end)

    before_each(function()
        helper.before_each()
    end)

    local function set_binary(exists)
        if exists then
            local f = assert(io.open(bin_path, "w"))
            f:write("#!/bin/sh\necho shim\n")
            f:close()
        else
            os.remove(bin_path)
        end
        package.loaded["main"] = nil
    end

    describe("when binary is missing", function()
        before_each(function()
            set_binary(false)
        end)

        it("returns disabled module", function()
            local result = require("main")

            assert.is_table(result)
            assert.is_true(result.disabled, "Module should be disabled when binary missing")
        end)

        it("has only disabled field when binary missing", function()
            local result = require("main")

            local count = 0
            for _ in pairs(result) do
                count = count + 1
            end
            assert.equal(1, count, "Should have exactly 1 field (disabled)")
            assert.is_true(result.disabled)
        end)
    end)

    describe("when binary exists", function()
        before_each(function()
            set_binary(true)
        end)

        it("returns full module", function()
            local result = require("main")

            assert.is_nil(result.disabled, "Module should not be disabled when binary exists")
            assert.is_not_nil(result.name, "Module should have name field")
            assert.equal("LocalSend", result.name)
        end)

        local required_methods = { "init", "start", "isRunning" }
        for _, method in ipairs(required_methods) do
            it("has " .. method .. " method", function()
                local result = require("main")
                assert.is_function(result[method], "Module should have " .. method .. " method")
            end)
        end
    end)
end)
