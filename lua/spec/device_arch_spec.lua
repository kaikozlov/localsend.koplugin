require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for getDeviceArch - maps `uname -m` output to asset architecture names.
-- Only io.popen("uname -m") is stubbed (targeted fault injection, like acsm's
-- http.request stub); everything else is real KOReader.

describe("getDeviceArch", function()
    local instance
    local uname_output
    local original_io_popen

    setup(function()
        helper.setup_complete()
        original_io_popen = io.popen
    end)

    before_each(function()
        helper.before_each()
        uname_output = nil
        io.popen = function(cmd)
            if cmd == "uname -m" then
                return {
                    read = function(_, fmt)
                        return uname_output
                    end,
                    close = function() end,
                }
            end
            return original_io_popen(cmd)
        end
        instance = helper.create_instance()
    end)

    after_each(function()
        io.popen = original_io_popen
    end)

    local function test_arch(input)
        uname_output = input
        return instance:getDeviceArch()
    end

    describe("64-bit ARM detection", function()
        local test_cases = {
            { "aarch64", "arm64" },
            { "arm64", "arm64" },
            { "aarch64_be", "arm64" },
        }
        for _, tc in ipairs(test_cases) do
            it("maps '" .. tc[1] .. "' to '" .. tc[2] .. "'", function()
                assert.equal(tc[2], test_arch(tc[1]))
            end)
        end
    end)

    describe("32-bit ARM v7 detection", function()
        local test_cases = {
            { "armv7l", "armv7" },
            { "armv7", "armv7" },
            { "armv7hl", "armv7" },
        }
        for _, tc in ipairs(test_cases) do
            it("maps '" .. tc[1] .. "' to '" .. tc[2] .. "'", function()
                assert.equal(tc[2], test_arch(tc[1]))
            end)
        end
    end)

    describe("legacy ARM detection (arm-legacy)", function()
        local test_cases = {
            { "armv5", "arm-legacy" },
            { "armv5tel", "arm-legacy" },
            { "armv6l", "arm-legacy" },
            { "arm", "arm-legacy" },
        }
        for _, tc in ipairs(test_cases) do
            it("maps '" .. tc[1] .. "' to '" .. tc[2] .. "'", function()
                assert.equal(tc[2], test_arch(tc[1]))
            end)
        end
    end)

    describe("unknown architecture handling", function()
        local test_cases = {
            { "x86_64", nil },
            { "i686", nil },
            { "", nil },
        }
        for _, tc in ipairs(test_cases) do
            local desc = tc[1] == "" and "empty output" or "'" .. tc[1] .. "'"
            it("returns nil for " .. desc, function()
                assert.is_nil(test_arch(tc[1]))
            end)
        end

        it("returns nil when uname fails", function()
            assert.is_nil(test_arch(nil))
        end)
    end)

    describe("io.popen failure handling", function()
        it("returns nil when io.popen returns nil", function()
            io.popen = function(cmd)
                if cmd == "uname -m" then
                    return nil
                end
            end
            assert.is_nil(instance:getDeviceArch())
        end)
    end)
end)
