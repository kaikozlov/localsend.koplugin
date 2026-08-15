require("busted.runner")()

local PluginShare = require("pluginshare")
local UIManager = require("ui/uimanager")

describe("LocalSend scoped power inhibition", function()
    local power
    local old_prevent, old_allow, old_pause
    local prevents, allows

    before_each(function()
        package.loaded["localsend_power"] = nil
        power = require("localsend_power")
        old_prevent, old_allow = UIManager.preventStandby, UIManager.allowStandby
        old_pause = PluginShare.pause_auto_suspend
        prevents, allows = 0, 0
        UIManager.preventStandby = function()
            prevents = prevents + 1
        end
        UIManager.allowStandby = function()
            allows = allows + 1
        end
        PluginShare.pause_auto_suspend = false
    end)

    after_each(function()
        power.releaseAll()
        UIManager.preventStandby, UIManager.allowStandby = old_prevent, old_allow
        PluginShare.pause_auto_suspend = old_pause
        package.loaded["localsend_power"] = nil
    end)

    it("coalesces overlapping scan/send/receive holds into one KOReader standby lock", function()
        assert.is_true(power.acquire("scan"))
        assert.is_true(power.acquire("send"))
        assert.is_true(power.acquire("receive"))
        assert.is_false(power.acquire("send"), "named holds must be idempotent")
        assert.equal(1, prevents)
        assert.is_true(PluginShare.pause_auto_suspend)

        power.release("scan")
        power.release("send")
        assert.equal(0, allows)
        power.release("receive")
        assert.equal(1, allows)
        assert.is_false(PluginShare.pause_auto_suspend)
    end)

    it("preserves another plugin's existing pause_auto_suspend request", function()
        PluginShare.pause_auto_suspend = true
        power.acquire("send")
        power.release("send")
        assert.is_true(PluginShare.pause_auto_suspend)
        assert.equal(1, prevents)
        assert.equal(1, allows)
    end)
end)
