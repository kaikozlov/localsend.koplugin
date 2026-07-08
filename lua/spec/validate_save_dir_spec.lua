require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")
local ffiUtil = require("ffi/util")

-- Tests for validateSaveDir. Happy paths exercise the REAL filesystem under an
-- isolated temp dir; the two failure paths (cannot create, not writable) stub
-- util.makePath / io.open with save+restore.

describe("validateSaveDir", function()
    local instance
    local tmp_root

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
        tmp_root = get_test_data_dir() .. "/vsd-" .. tostring(os.time()) .. "-" .. math.random(1000000)
        util.makePath(tmp_root)
        instance = helper.create_instance()
    end)

    after_each(function()
        pcall(ffiUtil.purgeDir, tmp_root)
    end)

    describe("path validation", function()
        it("rejects nil path", function()
            local valid, err = instance:validateSaveDir(nil)
            assert.is_false(valid)
            assert.is_not_nil(err)
        end)

        it("rejects empty path", function()
            local valid, err = instance:validateSaveDir("")
            assert.is_false(valid)
            assert.is_not_nil(err)
        end)

        it("rejects relative paths", function()
            local valid, err = instance:validateSaveDir("relative/path")
            assert.is_false(valid)
            assert.truthy(err:match("absolute path"))
        end)

        it("rejects paths with command substitution", function()
            assert.is_false((instance:validateSaveDir("/path/$(whoami)")))
            assert.is_false((instance:validateSaveDir("/path/`id`")))
        end)
    end)

    describe("directory existence (real filesystem)", function()
        it("accepts an existing writable directory", function()
            local dir = tmp_root .. "/existing"
            util.makePath(dir)
            local valid, err = instance:validateSaveDir(dir)
            assert.is_true(valid)
            assert.is_nil(err)
        end)

        it("creates a non-existent directory when possible", function()
            local dir = tmp_root .. "/created"
            assert.is_false(util.pathExists(dir))
            local valid = instance:validateSaveDir(dir)
            assert.is_true(valid)
            assert.is_true(util.pathExists(dir), "Path should exist after makePath")
        end)
    end)

    describe("failure paths (targeted stubs)", function()
        local orig_makePath, orig_io_open

        before_each(function()
            orig_makePath = util.makePath
            orig_io_open = io.open
        end)

        after_each(function()
            util.makePath = orig_makePath
            io.open = orig_io_open
        end)

        it("rejects a directory that cannot be created", function()
            util.makePath = function(_path)
                return nil, "Failed to create directory"
            end
            local valid, err = instance:validateSaveDir(tmp_root .. "/nope")
            assert.is_false(valid)
            assert.truthy(err:match("could not be created"))
        end)

        it("rejects a directory that is not writable", function()
            local dir = tmp_root .. "/readonly"
            util.makePath(dir)
            io.open = function(path, mode)
                if mode == "w" and path:match("%.localsend_write_test$") then
                    return nil
                end
                return orig_io_open(path, mode)
            end
            local valid, err = instance:validateSaveDir(dir)
            assert.is_false(valid)
            assert.truthy(err:match("not writable"))
        end)
    end)

    describe("cleanup", function()
        it("cleans up the write-test file after a successful check", function()
            local dir = tmp_root .. "/cleanup"
            util.makePath(dir)
            instance:validateSaveDir(dir)

            local found_cleanup = false
            for _, path in ipairs(helper.state.removed_files) do
                if path:match("%.localsend_write_test$") then
                    found_cleanup = true
                    break
                end
            end
            assert.is_true(found_cleanup, "Test file should be cleaned up")
        end)
    end)
end)
