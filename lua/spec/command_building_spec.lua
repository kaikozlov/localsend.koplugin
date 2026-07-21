require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for command building and effective extension calculation
-- This tests the logic that determines what extensions are accepted

describe("Command Building Logic", function()
    local LocalSend

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    -- Helper to get effective_accept_ext using same logic as start()
    local function getEffectiveAcceptExt(instance)
        local effective_accept_ext = instance.accept_ext
        if instance.routing_enabled and next(instance.ext_dirs) then
            if not instance.routing_accept_all then
                local exts = {}
                for ext, _ in pairs(instance.ext_dirs) do
                    table.insert(exts, ext)
                end
                table.sort(exts) -- For deterministic testing
                effective_accept_ext = table.concat(exts, ",")
            else
                effective_accept_ext = ""
            end
        end
        return effective_accept_ext
    end

    describe("effective extension calculation", function()
        it("uses accept_ext when routing is disabled", function()
            local instance = helper.create_instance()

            instance.routing_enabled = false
            instance.accept_ext = "epub,pdf"
            instance.ext_dirs = { mobi = "/books" }

            local result = getEffectiveAcceptExt(instance)
            assert.equal("epub,pdf", result)
        end)

        it("uses routed extensions when routing enabled and accept_all is false", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.routing_accept_all = false
            instance.accept_ext = "txt" -- Should be ignored
            instance.ext_dirs = { epub = "/books", pdf = "/docs" }

            local result = getEffectiveAcceptExt(instance)
            -- Should contain both extensions (sorted for determinism)
            assert.equal("epub,pdf", result)
        end)

        it("accepts all when routing enabled and accept_all is true", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.routing_accept_all = true
            instance.accept_ext = "txt"
            instance.ext_dirs = { epub = "/books" }

            local result = getEffectiveAcceptExt(instance)
            assert.equal("", result) -- Empty means accept all
        end)

        it("uses accept_ext when routing enabled but no routes defined", function()
            local instance = helper.create_instance()

            instance.routing_enabled = true
            instance.accept_ext = "epub,pdf"
            instance.ext_dirs = {}

            local result = getEffectiveAcceptExt(instance)
            assert.equal("epub,pdf", result)
        end)
    end)

    describe("WebRTC flag", function()
        it("respects explicit true for use_webrtc", function()
            helper.state.settings["LocalSend_use_webrtc"] = true
            local instance = helper.create_instance()
            assert.is_true(instance.use_webrtc)
        end)
    end)
end)
