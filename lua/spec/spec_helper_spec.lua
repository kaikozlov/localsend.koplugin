require("busted.runner")()

local helper = require("spec.spec_helper")

describe("spec_helper spy lifecycle", function()
    it("restores io.popen when spies are restored", function()
        local original_popen = io.popen

        helper.install_spies()
        helper.restore_spies()

        assert.are.equal(original_popen, io.popen)
    end)
end)
