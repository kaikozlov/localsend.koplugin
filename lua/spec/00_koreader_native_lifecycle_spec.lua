require("busted.runner")()

local helper = require("spec.spec_helper")

describe("LocalSend native KOReader lifecycle", function()
    local UIManager
    local current_fm

    setup(function()
        UIManager = require("ui/uimanager")
    end)

    before_each(function()
        helper.before_each()
        G_reader_settings:saveSetting("LocalSend_save_dir", get_test_data_dir())
    end)

    after_each(function()
        if current_fm then
            helper.close_filemanager(current_fm)
            current_fm = nil
        else
            UIManager:quit()
        end
    end)

    it("is discovered by the real PluginLoader", function()
        local PluginLoader = require("pluginloader")
        local found
        for _, plugin in ipairs(PluginLoader:_discover()) do
            if plugin.name == "localsend" or plugin.name == "localsend.koplugin" then
                found = plugin
                break
            end
        end
        assert.is_truthy(found, "localsend.koplugin should be discovered")
        assert.is_false(found.disabled)
        assert.is_truthy(found.main:match("localsend%.koplugin/main%.lua$"))
    end)

    it("instantiates through FileManager with real KOReader modules", function()
        local instance, fm = helper.load_via_filemanager()
        current_fm = fm
        assert.is_truthy(instance)
        assert.are.equal(get_test_data_dir(), instance.save_dir)
        assert.are.equal("53317", tostring(instance.port))
    end)

    it("registers a real KOReader main-menu entry", function()
        local instance, fm = helper.load_via_filemanager()
        current_fm = fm
        local menu_items = {}
        instance:addToMainMenu(menu_items)
        local item = menu_items.localsend
        assert.is_truthy(item)
        assert.are.equal("network", item.sorting_hint)
        assert.are.equal("LocalSend", item.text_func())
        assert.is_truthy(item.sub_item_table)
        assert.is_true(#item.sub_item_table >= 6)
    end)

    it("keeps ServerState across widget recreation", function()
        local first, first_fm = helper.load_via_filemanager()
        current_fm = first_fm
        local state = require("localsend_state").ServerState
        -- telemetry_cleaned is set during real init; confirm the first instance
        -- actually flipped it (not just that the field exists).
        assert.is_true(state.telemetry_cleaned,
            "real PluginLoader init should have run clearTmpTelemetryFiles guard")

        -- Mutate state the way the running plugin would, then recreate the
        -- widget instance (as KOReader does on a view switch) WITHOUT reloading
        -- the module. ServerState is module-level, so it must survive.
        state.transfer_count = 7
        state.user_stopped = true

        helper.close_filemanager(first_fm)
        current_fm = nil

        local LocalSend = require("main")
        local second = LocalSend:new({ ui = { menu = { registerToMainMenu = function() end } } })
        assert.is_truthy(second, "second instance should instantiate")
        -- Same table identity (module-level state, not per-instance).
        assert.are.equal(state, require("localsend_state").ServerState)
        -- And the values the first instance wrote must persist to the second.
        assert.are.equal(7, second._ServerState.transfer_count)
        assert.is_true(second._ServerState.user_stopped)
        second:onCloseWidget()
    end)
end)
