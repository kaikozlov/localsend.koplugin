require("busted.runner")()
local helper = require("spec.spec_helper")
local util = require("util")
local ffiUtil = require("ffi/util")

-- Tests for dialog functions: showSaveDirPicker, showDeviceNameDialog, showPinDialog,
-- showCustomExtDialog, showAddExtensionRouteDialog, showCustomExtensionDialog,
-- showExtensionDirPicker. Real KOReader widgets are used; input is injected by
-- overriding getInputText() on the live InputDialog instance.

describe("Dialog Functions", function()
    local DOCS, NEWDIR
    -- lock_home_folder / home_dir are KOReader globals (not LocalSend_*) read by
    -- localsend_dialogs.lua. getPickerStartPath tests write them through the
    -- settings proxy into the real G_reader_settings, and before_each only clears
    -- LocalSend_* keys — so save/restore them ourselves to avoid leaking into
    -- later specs (e.g. filepicker behaviour elsewhere).
    local orig_lock_home_folder, orig_home_dir

    local function _restore_home_settings()
        if orig_lock_home_folder == nil then
            G_reader_settings:delSetting("lock_home_folder")
        else
            G_reader_settings:saveSetting("lock_home_folder", orig_lock_home_folder)
        end
        if orig_home_dir == nil then
            G_reader_settings:delSetting("home_dir")
        else
            G_reader_settings:saveSetting("home_dir", orig_home_dir)
        end
    end

    setup(function()
        helper.setup_complete()
        DOCS = get_test_data_dir() .. "/docs"
        NEWDIR = get_test_data_dir() .. "/newdir"
        util.makePath(DOCS)
        orig_lock_home_folder = G_reader_settings:readSetting("lock_home_folder")
        orig_home_dir = G_reader_settings:readSetting("home_dir")
    end)

    teardown(function()
        pcall(ffiUtil.purgeDir, DOCS)
        pcall(ffiUtil.purgeDir, NEWDIR)
        _restore_home_settings()
    end)

    before_each(function()
        helper.before_each()
        pcall(ffiUtil.purgeDir, NEWDIR)
    end)

    after_each(function()
        _restore_home_settings()
    end)

    describe("showSaveDirPicker", function()
        it("should create PathChooser for directory selection", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance:showSaveDirPicker({ updateItems = function() end })
            local path_chooser = helper.find_dialog("PathChooser")
            assert.is_truthy(path_chooser)
            assert.is_true(path_chooser.select_directory)
            assert.is_false(path_chooser.select_file)
        end)

        it("should use getPickerStartPath for initial path", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            local picker_start_called = false
            instance.getPickerStartPath = function(self, path)
                picker_start_called = true
                return path
            end
            instance:showSaveDirPicker({ updateItems = function() end })
            assert.is_true(picker_start_called)
        end)

        it("should have onConfirm callback that validates directory", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance:showSaveDirPicker({ updateItems = function() end })
            local path_chooser = helper.find_dialog("PathChooser")
            assert.is_not_nil(path_chooser)
            assert.is_function(path_chooser.onConfirm)
        end)

        it("onConfirm should save valid directory", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance:showSaveDirPicker({ updateItems = function() end })
            local path_chooser = helper.state.dialogs_shown[1]
            path_chooser.onConfirm(NEWDIR)
            assert.equal(NEWDIR, instance.save_dir)
            assert.equal(NEWDIR, helper.state.settings["LocalSend_save_dir"])
        end)

        it("onConfirm should show error for invalid directory", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance.validateSaveDir = function()
                return false, "Not writable"
            end
            instance:showSaveDirPicker({ updateItems = function() end })
            local path_chooser = helper.state.dialogs_shown[1]
            path_chooser.onConfirm("/readonly/path")
            assert.is_truthy(helper.find_notification("Cannot use this directory"))
        end)
    end)

    describe("showDeviceNameDialog", function()
        it("should create InputDialog with current device name", function()
            local instance = helper.create_instance()
            instance.device_name = "My Kindle"
            instance:showDeviceNameDialog({ updateItems = function() end })
            local dialog = helper.find_dialog_with_title("InputDialog", "Device name")
            assert.is_truthy(dialog)
            assert.equal("My Kindle", dialog.input)
        end)

        it("should have description mentioning default name option", function()
            local instance = helper.create_instance()
            instance:showDeviceNameDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            assert.truthy(dialog.description:match("KOReader"))
        end)

        it("should validate device name on save", function()
            local instance = helper.create_instance()
            instance.device_name = ""
            local validate_called = false
            instance.validateDeviceName = function(_, name)
                validate_called = true
                return true
            end
            instance:showDeviceNameDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            dialog.getInputText = function()
                return "New Name"
            end
            dialog.buttons[1][2].callback()
            assert.is_true(validate_called)
        end)

        it("should show error for invalid device name", function()
            local instance = helper.create_instance()
            instance.validateDeviceName = function()
                return false, "Invalid characters"
            end
            instance:showDeviceNameDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            dialog.getInputText = function()
                return "Invalid<>Name"
            end
            dialog.buttons[1][2].callback()
            local found_error = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.icon == "notice-warning" then
                    found_error = true
                    break
                end
            end
            assert.is_true(found_error)
        end)

        it("cancel button should close dialog without changes", function()
            local instance = helper.create_instance()
            instance.device_name = "Original Name"
            instance:showDeviceNameDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            local cancel_button = dialog.buttons[1][1]
            assert.equal("Cancel", cancel_button.text)
            cancel_button.callback()
            assert.is_true(#helper.state.close_calls > 0)
            assert.equal("Original Name", instance.device_name)
        end)
    end)

    describe("showPinDialog", function()
        it("should create InputDialog for PIN", function()
            local instance = helper.create_instance()
            instance.pin = "1234"
            instance:showPinDialog({ updateItems = function() end })
            local dialog = helper.find_dialog_with_title("InputDialog", "PIN")
            assert.is_truthy(dialog)
            assert.equal("1234", dialog.input)
        end)

        it("should save PIN and update settings", function()
            local instance = helper.create_instance()
            instance.pin = ""
            instance:showPinDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            dialog.getInputText = function()
                return "5678"
            end
            dialog.buttons[1][2].callback()
            assert.equal("5678", instance.pin)
            assert.equal("5678", helper.state.settings["LocalSend_pin"])
        end)

        it("cancel button should close dialog without changes", function()
            local instance = helper.create_instance()
            instance.pin = "1234"
            instance:showPinDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            local cancel_button = dialog.buttons[1][1]
            assert.equal("Cancel", cancel_button.text)
            cancel_button.callback()
            assert.is_true(#helper.state.close_calls > 0)
            assert.equal("1234", instance.pin)
        end)
    end)

    describe("showCustomExtDialog", function()
        it("should create InputDialog for custom extensions", function()
            local instance = helper.create_instance()
            instance.accept_ext = "epub,pdf"
            instance:showCustomExtDialog()
            local dialog = helper.find_dialog_with_title("InputDialog", "extensions")
            assert.is_truthy(dialog)
            assert.equal("epub,pdf", dialog.input)
        end)

        it("should have comma-separated example in description", function()
            local instance = helper.create_instance()
            instance:showCustomExtDialog()
            local dialog = helper.state.dialogs_shown[1]
            assert.truthy(dialog.description:match("Comma%-separated"))
        end)

        it("should save custom extensions", function()
            local instance = helper.create_instance()
            instance.accept_ext = ""
            instance:showCustomExtDialog()
            local dialog = helper.state.dialogs_shown[1]
            dialog.getInputText = function()
                return "mobi,azw3"
            end
            dialog.buttons[1][2].callback()
            assert.equal("mobi,azw3", instance.accept_ext)
            assert.equal("mobi,azw3", helper.state.settings["LocalSend_accept_ext"])
        end)

        it("cancel button should close dialog without changes", function()
            local instance = helper.create_instance()
            instance.accept_ext = "epub,pdf"
            instance:showCustomExtDialog()
            local dialog = helper.state.dialogs_shown[1]
            local cancel_button = dialog.buttons[1][1]
            assert.equal("Cancel", cancel_button.text)
            cancel_button.callback()
            assert.is_true(#helper.state.close_calls > 0)
            assert.equal("epub,pdf", instance.accept_ext)
        end)
    end)

    describe("showAddExtensionRouteDialog", function()
        it("should show ButtonDialog with extension presets", function()
            local instance = helper.create_instance()
            instance:showAddExtensionRouteDialog({ updateItems = function() end })
            local dialog = helper.find_dialog("ButtonDialog")
            assert.is_truthy(dialog)
            assert.truthy(dialog.title:match("extension"))
        end)

        it("should have common ebook extension buttons", function()
            local instance = helper.create_instance()
            instance:showAddExtensionRouteDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            local found_epub, found_pdf = false, false
            for _, row in ipairs(dialog.buttons) do
                for _, button in ipairs(row) do
                    if button.text == "epub" then
                        found_epub = true
                    end
                    if button.text == "pdf" then
                        found_pdf = true
                    end
                end
            end
            assert.is_true(found_epub)
            assert.is_true(found_pdf)
        end)

        it("should have Custom option", function()
            local instance = helper.create_instance()
            instance:showAddExtensionRouteDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            local found_custom = false
            for _, row in ipairs(dialog.buttons) do
                for _, button in ipairs(row) do
                    if button.text:match("Custom") then
                        found_custom = true
                        break
                    end
                end
            end
            assert.is_true(found_custom)
        end)
    end)

    describe("showCustomExtensionDialog", function()
        it("should create InputDialog for custom extension", function()
            local instance = helper.create_instance()
            instance:showCustomExtensionDialog({ updateItems = function() end })
            local dialog = helper.find_dialog_with_title("InputDialog", "Extension to route")
            assert.is_truthy(dialog)
        end)

        it("should strip leading dot from extension", function()
            local instance = helper.create_instance()
            local picker_ext = nil
            instance.showExtensionDirPicker = function(_, ext)
                picker_ext = ext
            end
            instance:showCustomExtensionDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            dialog.getInputText = function()
                return ".epub"
            end
            dialog.buttons[1][2].callback()
            assert.equal("epub", picker_ext)
        end)

        it("should lowercase the extension", function()
            local instance = helper.create_instance()
            local picker_ext = nil
            instance.showExtensionDirPicker = function(_, ext)
                picker_ext = ext
            end
            instance:showCustomExtensionDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            dialog.getInputText = function()
                return "EPUB"
            end
            dialog.buttons[1][2].callback()
            assert.equal("epub", picker_ext)
        end)

        it("cancel button should close dialog without proceeding", function()
            local picker_called = false
            local instance = helper.create_instance()
            instance.showExtensionDirPicker = function()
                picker_called = true
            end
            instance:showCustomExtensionDialog({ updateItems = function() end })
            local dialog = helper.state.dialogs_shown[1]
            local cancel_button = dialog.buttons[1][1]
            assert.equal("Cancel", cancel_button.text)
            cancel_button.callback()
            assert.is_true(#helper.state.close_calls > 0)
            assert.is_false(picker_called)
        end)
    end)

    describe("showExtensionDirPicker", function()
        it("should create PathChooser for extension directory", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance:showExtensionDirPicker("epub", { updateItems = function() end })
            local chooser = helper.find_dialog("PathChooser")
            assert.is_truthy(chooser)
            assert.is_true(chooser.select_directory)
        end)

        it("onConfirm should add extension route", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance.ext_dirs = {}
            local route_added = false
            instance.addExtensionRoute = function(_, ext, dir)
                route_added = true
                assert.equal("pdf", ext)
                assert.equal(NEWDIR, dir)
            end
            instance:showExtensionDirPicker("pdf", { updateItems = function() end })
            local chooser = helper.state.dialogs_shown[1]
            chooser.onConfirm(NEWDIR)
            assert.is_true(route_added)
        end)

        it("onConfirm should show success notification", function()
            local instance = helper.create_instance()
            instance.save_dir = DOCS
            instance.ext_dirs = {}
            instance.addExtensionRoute = function() end
            instance:showExtensionDirPicker("epub", { updateItems = function() end })
            local chooser = helper.state.dialogs_shown[1]
            chooser.onConfirm(NEWDIR)
            local found_success = false
            for _, n in ipairs(helper.state.notifications_shown) do
                if n.text and (n.text:match("epub") or n.text:match(NEWDIR)) then
                    found_success = true
                    break
                end
            end
            assert.is_true(found_success)
        end)
    end)

    describe("dialog field cleanup", function()
        it("should NOT have device_name_dialog field after showDeviceNameDialog", function()
            local instance = helper.create_instance()
            assert.is_nil(instance.device_name_dialog)
            instance:showDeviceNameDialog({})
            assert.is_nil(instance.device_name_dialog)
        end)

        it("should NOT have pin_dialog field after showPinDialog", function()
            local instance = helper.create_instance()
            assert.is_nil(instance.pin_dialog)
            instance:showPinDialog({})
            assert.is_nil(instance.pin_dialog)
        end)

        it("instance should not accumulate dialog fields over time", function()
            local instance = helper.create_instance()
            local dialog_fields = {}
            for k in pairs(instance) do
                if type(k) == "string" and k:match("_dialog$") then
                    table.insert(dialog_fields, k)
                end
            end
            assert.equal(0, #dialog_fields)
        end)
    end)

    describe("getPickerStartPath", function()
        it("should return path unchanged when lock_home_folder is false", function()
            helper.state.settings["lock_home_folder"] = false
            helper.state.settings["home_dir"] = DOCS
            local instance = helper.create_instance()
            assert.equal(DOCS .. "/books", instance:getPickerStartPath(DOCS .. "/books"))
        end)

        it("should return parent when lock_home_folder is true and path is inside home", function()
            helper.state.settings["lock_home_folder"] = true
            helper.state.settings["home_dir"] = DOCS
            local instance = helper.create_instance()
            assert.equal(DOCS, instance:getPickerStartPath(DOCS .. "/books"))
        end)

        it("should return path unchanged when outside home_dir", function()
            helper.state.settings["lock_home_folder"] = true
            helper.state.settings["home_dir"] = DOCS
            local instance = helper.create_instance()
            assert.equal("/var/other", instance:getPickerStartPath("/var/other"))
        end)

        it("should not match partial directory names", function()
            helper.state.settings["lock_home_folder"] = true
            helper.state.settings["home_dir"] = "/tmp/doc"
            local instance = helper.create_instance()
            assert.equal(DOCS .. "/books", instance:getPickerStartPath(DOCS .. "/books"))
        end)
    end)
end)
