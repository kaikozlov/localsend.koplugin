require("busted.runner")()

local helper = require("spec.spec_helper")

-- End-to-end i18n coverage against the real KOReader runtime. The synthetic
-- locale translates actual LocalSend UI strings, then the plugin is loaded via
-- PluginLoader + FileManager exactly as it is on-device.
describe("i18n real KOReader integration", function()
    local I18n
    local UIManager
    local current_fm
    local fixture_path
    local saved_lang

    local ZT_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=2; plural=(n != 1);\n"
"Content-Type: text/plain; charset=UTF-8\n"

msgid "LocalSend"
msgstr "ZT LocalSend"

msgid "Send and receive files using LocalSend protocol."
msgstr "ZT translated plugin description"

msgid "Settings"
msgstr "ZT Settings"

msgid "File type routing (%1 rule)"
msgid_plural "File type routing (%1 rules)"
msgstr[0] "ZT routing has %1 rule"
msgstr[1] "ZT routing has %1 rules"

msgid "Add extension route..."
msgstr "ZT Add route..."

msgid "GitHub"
msgstr "ZT Project page"

msgid "Close"
msgstr "ZT Close"
]]

    local function loader_dir()
        local src = debug.getinfo(I18n.translate, "S").source:gsub("^@", "")
        return src:match("^(.*)/[^/]*$") or src:match("^(.*)\\[^\\]*$") or "."
    end

    local function write_fixture()
        fixture_path = loader_dir() .. "/locale/zt.po"
        local f = assert(io.open(fixture_path, "w"))
        f:write(ZT_PO)
        f:close()
    end

    local function load_instance()
        local instance, fm = helper.load_via_filemanager()
        current_fm = fm
        assert.is_truthy(instance, "LocalSend should instantiate through the real PluginLoader")
        return instance
    end

    local function build_menu(instance)
        local menu_items = {}
        instance:addToMainMenu(menu_items)
        return assert(menu_items.localsend)
    end

    local function find_static_item(items, text)
        for _, item in ipairs(items) do
            if item.text == text then
                return item
            end
        end
    end

    setup(function()
        helper.setup_complete()
        I18n = require("localsend_i18n")
        UIManager = require("ui/uimanager")
        saved_lang = G_reader_settings:readSetting("language")
        write_fixture()
    end)

    teardown(function()
        if saved_lang == nil then
            G_reader_settings:delSetting("language")
        else
            G_reader_settings:saveSetting("language", saved_lang)
        end
        I18n.reset()
        if fixture_path then
            pcall(os.remove, fixture_path)
        end
    end)

    before_each(function()
        helper.before_each()
        G_reader_settings:saveSetting("language", "zt")
        G_reader_settings:saveSetting("LocalSend_save_dir", get_test_data_dir())
        I18n.reset()
    end)

    after_each(function()
        if current_fm then
            helper.close_filemanager(current_fm)
            current_fm = nil
        else
            UIManager:quit()
        end
    end)

    it("translates plugin metadata loaded by the real PluginLoader", function()
        local instance = load_instance()

        assert.are.equal("ZT LocalSend", instance.fullname)
        assert.are.equal("ZT translated plugin description", instance.description)
    end)

    it("translates the actual KOReader main menu", function()
        local instance = load_instance()
        local menu = build_menu(instance)

        assert.are.equal("ZT LocalSend", menu.text_func())
        assert.is_truthy(find_static_item(menu.sub_item_table, "ZT Settings"))
    end)

    it("uses catalogue plural forms in dynamic menu text", function()
        local instance = load_instance()
        instance.routing_enabled = true
        instance.ext_dirs = { epub = "/books" }
        local settings = assert(find_static_item(build_menu(instance).sub_item_table, "ZT Settings"))
        local routing_item = settings.sub_item_table[3]

        assert.are.equal("ZT routing has 1 rule", routing_item.text_func())
        instance.ext_dirs.pdf = "/documents"
        assert.are.equal("ZT routing has 2 rules", routing_item.text_func())
    end)

    it("translates strings in dependency-injected submodules", function()
        local instance = load_instance()
        instance.ext_dirs = {}

        local routing_menu = instance:buildExtensionRoutingMenu()

        assert.are.equal("ZT Add route...", routing_menu[#routing_menu].text)
    end)

    it("renders translated text in a real KOReader dialog widget", function()
        local instance = load_instance()

        instance:showAbout()

        local dialog = assert(helper.find_dialog("ConfirmBox"))
        assert.is_truthy(dialog.text:match("ZT translated plugin description", 1, true))
        assert.are.equal("ZT Project page", dialog.ok_text)
        assert.are.equal("ZT Close", dialog.cancel_text)
    end)
end)
