require("busted.runner")()

-- Tests for localsend_i18n.lua — the plugin-local translation loader.
--
-- Hermetic: writes throwaway fake-language .po files into the loader's own
-- locale dir at setup and removes them at teardown, so no real translation file
-- is ever touched. The fixtures cover two-form, zero-form, nested three-form,
-- and malformed plural rules.
-- Language is selected via G_reader_settings:saveSetting("language", …) and
-- restored in teardown so nothing leaks into other specs.

describe("localsend_i18n", function()
    local I18n
    local fixture_paths = {}
    local logger
    local saved_lang
    local saved_logger_info

    -- The loader resolves its locale dir from its own source path via
    -- debug.getinfo; replicate that here so the fixtures land exactly where the
    -- loader will read them, regardless of cwd or how the module was required.
    local function loader_dir()
        local src = debug.getinfo(I18n.translate, "S").source:gsub("^@", "")
        return src:match("^(.*)/[^/]*$") or src:match("^(.*)\\[^\\]*$") or "."
    end

    local function write_fixture(name, content)
        local path = loader_dir() .. "/locale/" .. name
        local f = assert(io.open(path, "w"))
        f:write(content)
        f:close()
        return path
    end

    -- "zz": 2-form plural (same rule as English / pt-PT).
    local ZZ_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=2; plural=(n != 1);\n"
"Content-Type: text/plain; charset=UTF-8\n"
"Content-Transfer-Encoding: 8bit\n"

msgid "i18n:sentinel:singular"
msgstr "zz-translated"

#, fuzzy
msgid "i18n:sentinel:fuzzy"
msgstr "zz-fuzzy"

msgid "i18n:sentinel:escaped"
msgstr "line one\nline two \\ path \"quote\""

msgid "%1 item"
msgid_plural "%1 items"
msgstr[0] "zz-one"
msgstr[1] "zz-many"
]]

    -- "en_ZZ": an English regional variant. KOReader's en_GB is a translated
    -- locale, so only source English (C/en/en_US) should bypass plugin loading.
    local EN_ZZ_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=2; plural=(n != 1);\n"

msgid "i18n:sentinel:british"
msgstr "en-ZZ-translated"
]]

    -- "z3": 3-form plural (Polish rule). Exercises the ternary plural compiler.
    local Z3_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=3; plural=(n==1 ? 0 : n%10>=2 && n%10<=4 && (n%100<10 || n%100>=20) ? 1 : 2);\n"
"Content-Type: text/plain; charset=UTF-8\n"
"Content-Transfer-Encoding: 8bit\n"

msgid "%1 item"
msgid_plural "%1 items"
msgstr[0] "z3-one"
msgstr[1] "z3-few"
msgstr[2] "z3-many"
]]

    -- "zc": Czech's nested ternary form, as used by real KOReader plugins.
    local ZC_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=3; plural=(n==1 ? 0 : (n>=2 && n<=4 ? 1 : 2));\n"

msgid "%1 nested item"
msgid_plural "%1 nested items"
msgstr[0] "zc-one"
msgstr[1] "zc-few"
msgstr[2] "zc-many"
]]

    -- "z0": languages such as Chinese always select plural form zero.
    local Z0_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=1; plural=0;\n"

msgid "%1 zero item"
msgid_plural "%1 zero items"
msgstr[0] "z0-only"
]]

    -- "zi": malformed or unsafe expressions must not execute or crash the UI.
    local ZI_PO = [[
msgid ""
msgstr ""
"Plural-Forms: nplurals=2; plural=(error(\"must not execute\"));\n"

msgid "%1 invalid item"
msgid_plural "%1 invalid items"
msgstr[0] "zi-one"
msgstr[1] "zi-many"
]]

    setup(function()
        I18n = require("localsend_i18n")
        logger = require("logger")
        saved_lang = G_reader_settings:readSetting("language")
        saved_logger_info = logger.info
        table.insert(fixture_paths, write_fixture("zz.po", ZZ_PO))
        table.insert(fixture_paths, write_fixture("en_ZZ.po", EN_ZZ_PO))
        table.insert(fixture_paths, write_fixture("z3.po", Z3_PO))
        table.insert(fixture_paths, write_fixture("zc.po", ZC_PO))
        table.insert(fixture_paths, write_fixture("z0.po", Z0_PO))
        table.insert(fixture_paths, write_fixture("zi.po", ZI_PO))
    end)

    teardown(function()
        logger.info = saved_logger_info
        -- Restore the UI language so other specs are unaffected.
        if saved_lang == nil then
            G_reader_settings:delSetting("language")
        else
            G_reader_settings:saveSetting("language", saved_lang)
        end
        I18n.reset()
        for _, p in ipairs(fixture_paths) do
            pcall(os.remove, p)
        end
    end)

    before_each(function()
        I18n.reset()
    end)

    describe("translate()", function()
        it("returns the catalogue translation when present", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("zz-translated", I18n.translate("i18n:sentinel:singular"))
        end)

        it("passes the msgid through when the string is in no catalogue", function()
            G_reader_settings:saveSetting("language", "zz")
            -- Core gettext has no record of this sentinel either, so the
            -- fallback is the identity function.
            assert.are.equal("i18n:sentinel:unknown", I18n.translate("i18n:sentinel:unknown"))
        end)

        it("ignores fuzzy catalogue entries", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("i18n:sentinel:fuzzy", I18n.translate("i18n:sentinel:fuzzy"))
        end)

        it("decodes standard PO string escapes", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal('line one\nline two \\ path "quote"', I18n.translate("i18n:sentinel:escaped"))
        end)

        it("falls back from a region code to the language prefix (zz_ZZ -> zz)", function()
            G_reader_settings:saveSetting("language", "zz_ZZ")
            assert.are.equal("zz-translated", I18n.translate("i18n:sentinel:singular"))
        end)

        it("logs once when no catalogue exists for a non-English locale", function()
            local messages = {}
            logger.info = function(message)
                table.insert(messages, message)
            end
            G_reader_settings:saveSetting("language", "zy")

            assert.are.equal("i18n:sentinel:unknown", I18n.translate("i18n:sentinel:unknown"))
            assert.are.equal("i18n:sentinel:unknown", I18n.translate("i18n:sentinel:unknown"))
            assert.are.same({
                "localsend i18n: no catalogue for zy; using KOReader/English fallback",
            }, messages)
        end)

        it("skips loading entirely for source English and returns the msgid", function()
            G_reader_settings:saveSetting("language", "en")
            assert.are.equal("i18n:sentinel:singular", I18n.translate("i18n:sentinel:singular"))
        end)

        it("loads a catalogue for an English regional locale", function()
            G_reader_settings:saveSetting("language", "en_ZZ")
            assert.are.equal("en-ZZ-translated", I18n.translate("i18n:sentinel:british"))
        end)

        it("does not touch global gettext state (core _ still callable as a table)", function()
            -- Regression guard: the loader must never replace
            -- package.loaded["gettext"], which would break other plugins.
            G_reader_settings:saveSetting("language", "zz")
            I18n.translate("i18n:sentinel:singular")
            local gt = require("gettext")
            assert.is_table(gt)
            -- Core gettext is still a callable table, unaffected by us.
            assert.are.equal("core-unchanged", gt("core-unchanged"))
        end)
    end)

    describe("ngettext()", function()
        it("selects the singular form for n == 1 (2-form)", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("zz-one", I18n.ngettext("%1 item", "%1 items", 1))
        end)

        it("selects the plural form for n > 1 (2-form)", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("zz-many", I18n.ngettext("%1 item", "%1 items", 5))
        end)

        it("honours a 3-form Plural-Forms header (one/few/many)", function()
            G_reader_settings:saveSetting("language", "z3")
            assert.are.equal("z3-one", I18n.ngettext("%1 item", "%1 items", 1))
            assert.are.equal("z3-few", I18n.ngettext("%1 item", "%1 items", 2))
            assert.are.equal("z3-few", I18n.ngettext("%1 item", "%1 items", 4))
            assert.are.equal("z3-many", I18n.ngettext("%1 item", "%1 items", 5))
            assert.are.equal("z3-many", I18n.ngettext("%1 item", "%1 items", 12))
            assert.are.equal("z3-few", I18n.ngettext("%1 item", "%1 items", 22))
            assert.are.equal("z3-many", I18n.ngettext("%1 item", "%1 items", 100))
        end)

        it("honours nested ternary plural forms", function()
            G_reader_settings:saveSetting("language", "zc")
            assert.are.equal("zc-one", I18n.ngettext("%1 nested item", "%1 nested items", 1))
            assert.are.equal("zc-few", I18n.ngettext("%1 nested item", "%1 nested items", 3))
            assert.are.equal("zc-many", I18n.ngettext("%1 nested item", "%1 nested items", 5))
        end)

        it("honours a numeric zero plural rule", function()
            G_reader_settings:saveSetting("language", "z0")
            assert.are.equal("z0-only", I18n.ngettext("%1 zero item", "%1 zero items", 1))
            assert.are.equal("z0-only", I18n.ngettext("%1 zero item", "%1 zero items", 9))
        end)

        it("rejects unsafe plural expressions and uses the default rule", function()
            G_reader_settings:saveSetting("language", "zi")
            assert.are.equal("zi-one", I18n.ngettext("%1 invalid item", "%1 invalid items", 1))
            assert.are.equal("zi-many", I18n.ngettext("%1 invalid item", "%1 invalid items", 5))
        end)

        it("falls back to the core singular/plural for untranslated plurals", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("uniq-singular", I18n.ngettext("uniq-singular", "uniq-plural", 1))
            assert.are.equal("uniq-plural", I18n.ngettext("uniq-singular", "uniq-plural", 9))
        end)
    end)

    describe("catalogue template", function()
        it("includes device-name validation failures", function()
            local f = assert(io.open(loader_dir() .. "/locale/localsend.pot", "r"))
            local pot = f:read("*a")
            f:close()

            assert.is_truthy(pot:find('msgid "Device name is too long (max 64 characters)."', 1, true))
            -- xgettext wraps this long msgid across quoted continuation lines.
            assert.is_truthy(pot:find('"Device name can only contain letters, numbers, spaces, hyphens, underscores, "', 1, true))
            assert.is_truthy(pot:find('"and apostrophes."', 1, true))
        end)
    end)

    describe("getLang()", function()
        it("reads the language from G_reader_settings", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("zz", I18n.getLang())
        end)
    end)

    describe("reset()", function()
        it("reloads translations after a language change", function()
            G_reader_settings:saveSetting("language", "zz")
            assert.are.equal("zz-translated", I18n.translate("i18n:sentinel:singular"))
            -- Switch to English and force a reload.
            G_reader_settings:saveSetting("language", "en")
            I18n.reset()
            assert.are.equal("i18n:sentinel:singular", I18n.translate("i18n:sentinel:singular"))
        end)
    end)
end)
