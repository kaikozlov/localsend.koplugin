require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for the "Send with LocalSend" button in the file long-press context menu.
-- Verifies CoverBrowser-style registration via FileManager.addFileDialogButtons
-- on FileManager / History / Collections / FileSearcher class tables.

local function get_localsend_row_func(widget)
    local buttons = widget.file_dialog_added_buttons
    if not buttons or not buttons.index then
        return nil
    end
    local idx = buttons.index["localsend_send"]
    if not idx then
        return nil
    end
    return buttons[idx]
end

local function count_button_rows(widget)
    local count = 0
    if not widget.file_dialog_added_buttons then
        return 0
    end
    for _, _row_func in ipairs(widget.file_dialog_added_buttons) do
        count = count + 1
    end
    return count
end

describe("File Context Menu Integration", function()
    local current_fm

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
        current_fm = nil
    end)

    after_each(function()
        if current_fm then
            helper.close_filemanager(current_fm)
            current_fm = nil
        end
        helper.reset_state()
    end)

    describe("button registration", function()
        it("registers via FileManager addFileDialogButtons on init", function()
            local instance
            instance, current_fm = helper.load_via_filemanager()
            assert.is_not_nil(instance)
            assert.is_not_nil(current_fm)

            local FileManager = require("apps/filemanager/filemanager")
            assert.is_not_nil(get_localsend_row_func(FileManager))
            -- Instance sees class-table registration through the widget metatable.
            assert.is_not_nil(get_localsend_row_func(current_fm))
        end)

        it("registers on History, Collections, and FileSearcher class tables", function()
            local instance
            instance, current_fm = helper.load_via_filemanager()
            assert.is_not_nil(instance)

            local FileManagerHistory = require("apps/filemanager/filemanagerhistory")
            local FileManagerCollection = require("apps/filemanager/filemanagercollection")
            local FileManagerFileSearcher = require("apps/filemanager/filemanagerfilesearcher")

            assert.is_not_nil(get_localsend_row_func(FileManagerHistory))
            assert.is_not_nil(get_localsend_row_func(FileManagerCollection))
            assert.is_not_nil(get_localsend_row_func(FileManagerFileSearcher))
        end)

        it("is replaced cleanly across widget recreations", function()
            local FileManager = require("apps/filemanager/filemanager")
            local _, fm = helper.load_via_filemanager()
            local count_before = count_button_rows(FileManager)
            assert.is_true(count_before >= 1)

            helper.close_filemanager(fm)
            local _second
            _second, current_fm = helper.load_via_filemanager()

            assert.equals(count_before, count_button_rows(FileManager))
            assert.is_not_nil(get_localsend_row_func(FileManager))
        end)

        it("unregisters from all surfaces on stopPlugin", function()
            local instance
            instance, current_fm = helper.load_via_filemanager()
            assert.is_not_nil(get_localsend_row_func(current_fm))

            instance.isRunning = function()
                return false
            end
            instance.closeFirewall = function() end
            instance:stopPlugin()

            local FileManager = require("apps/filemanager/filemanager")
            local FileManagerHistory = require("apps/filemanager/filemanagerhistory")
            local FileManagerCollection = require("apps/filemanager/filemanagercollection")
            local FileManagerFileSearcher = require("apps/filemanager/filemanagerfilesearcher")

            assert.is_nil(get_localsend_row_func(FileManager))
            assert.is_nil(get_localsend_row_func(FileManagerHistory))
            assert.is_nil(get_localsend_row_func(FileManagerCollection))
            assert.is_nil(get_localsend_row_func(FileManagerFileSearcher))
        end)
    end)

    describe("row_func behavior", function()
        local instance

        before_each(function()
            instance, current_fm = helper.load_via_filemanager()
        end)

        it("returns a button row for files", function()
            local row_func = get_localsend_row_func(current_fm)
            assert.is_not_nil(row_func)

            local row = row_func("/some/book.epub", true, nil)
            assert.is_table(row)
            assert.equals("Send with LocalSend", row[1].text)
        end)

        it("returns a button row for folders", function()
            -- Folder send is supported by the CLI (--preserve-structure).
            local row_func = get_localsend_row_func(current_fm)
            assert.is_not_nil(row_func)

            local row = row_func("/some/folder", false, nil)
            assert.is_table(row)
            assert.equals("Send with LocalSend", row[1].text)
        end)
    end)

    describe("callback wiring", function()
        local instance

        before_each(function()
            instance, current_fm = helper.load_via_filemanager()
        end)

        it("forwards the long-pressed file path through to localsend_sender", function()
            local sender = require("localsend_sender")
            local seen_preset
            local original = sender.showFileSendFlow
            sender.showFileSendFlow = function(_instance_arg, preset_file)
                seen_preset = preset_file
            end

            local ok, err = pcall(function()
                local row_func = get_localsend_row_func(current_fm)
                assert.is_not_nil(row_func)

                local row = row_func("/documents/my_book.epub", true, nil)
                assert.is_function(row[1].callback)
                row[1].callback()

                assert.equals("/documents/my_book.epub", seen_preset)
            end)

            sender.showFileSendFlow = original
            assert.is_true(ok, err)
        end)

        it("disabled when a send is in progress", function()
            require("localsend_state").ServerState.send_in_progress = true

            local row_func = get_localsend_row_func(current_fm)
            local row = row_func("/test.epub", true, nil)
            assert.is_false(row[1].enabled_func())
        end)

        it("enabled when no send is in progress", function()
            require("localsend_state").ServerState.send_in_progress = false

            local row_func = get_localsend_row_func(current_fm)
            local row = row_func("/test.epub", true, nil)
            assert.is_true(row[1].enabled_func())
        end)
    end)
end)
