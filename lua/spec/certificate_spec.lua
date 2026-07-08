require("busted.runner")()
local helper = require("spec.spec_helper")

-- Tests for certificate management and restart functionality
-- Note: setupCertificates and saveCertificates have been removed.
-- Go now manages certificates directly in a certs/ folder next to the binary.

describe("Certificate Management", function()
    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
        helper.mock_os_execute()
        helper.mock_os_remove()
    end)

    describe("rotateCertificates", function()
        it("should show confirmation dialog before removing certificates", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            local confirm = helper.find_dialog("ConfirmBox")
            assert.is_truthy(confirm, "Should show ConfirmBox")
            assert.is_truthy(confirm.text:match("TLS certificates"), "Should mention TLS certificates")
            assert.equal("Delete", confirm.ok_text, "OK button should say 'Delete'")
            assert.equal("Cancel", confirm.cancel_text, "Cancel button should say 'Cancel'")
        end)

        it("should remove certificates from certs folder after confirmation", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            -- Simulate clicking "Delete" on the ConfirmBox
            local confirm = helper.find_dialog("ConfirmBox")
            assert.is_truthy(confirm, "Should show ConfirmBox")
            confirm.ok_callback()

            local found_rm_key = false
            local found_rm_crt = false
            for _, path in ipairs(helper.state.removed_files) do
                if path:match("certs/server%.key%.pem") then
                    found_rm_key = true
                end
                if path:match("certs/server%.crt") then
                    found_rm_crt = true
                end
            end
            assert.is_true(found_rm_key, "Should remove key from certs folder")
            assert.is_true(found_rm_crt, "Should remove cert from certs folder")
        end)

        it("should remove exactly 2 certificate files after confirmation", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            -- Simulate clicking "Delete"
            local confirm = helper.find_dialog("ConfirmBox")
            confirm.ok_callback()

            -- Count certificate file removals
            local rm_count = 0
            for _, path in ipairs(helper.state.removed_files) do
                if path:match("certs/server%.key%.pem") or path:match("certs/server%.crt") then
                    rm_count = rm_count + 1
                end
            end
            assert.equal(2, rm_count, "Should remove 2 certificate files")
        end)

        it("should show notification about certificate rotation after confirmation", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            -- Simulate clicking "Delete"
            local confirm = helper.find_dialog("ConfirmBox")
            confirm.ok_callback()

            local notification = helper.find_notification("Certificates cleared")
            assert.is_truthy(notification, "Should show rotation notification")
        end)

        it("notification should mention new certificates will be generated", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            -- Simulate clicking "Delete"
            local confirm = helper.find_dialog("ConfirmBox")
            confirm.ok_callback()

            local notification = helper.find_notification("generated on next start")
            assert.is_truthy(notification, "Should mention regeneration on next start")
        end)

        it("notification should have timeout", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            -- Simulate clicking "Delete"
            local confirm = helper.find_dialog("ConfirmBox")
            confirm.ok_callback()

            local notification = helper.find_notification("Certificates cleared")
            assert.equal(3, notification.timeout)
        end)

        it("should not remove certificates if user cancels", function()
            local instance = helper.create_instance()

            instance:rotateCertificates()

            -- User clicks "Cancel" - we just don't invoke ok_callback
            local confirm = helper.find_dialog("ConfirmBox")
            assert.is_truthy(confirm, "Should show ConfirmBox")

            -- No ok_callback invoked, so check no cert files were removed
            local rm_count = 0
            for _, path in ipairs(helper.state.removed_files) do
                if path:match("certs/server%.key%.pem") or path:match("certs/server%.crt") then
                    rm_count = rm_count + 1
                end
            end
            assert.equal(0, rm_count, "Should not remove certificates when cancelled")
        end)
    end)

    describe("restart", function()
        it("stops server then starts when running", function()
            local instance = helper.create_instance()

            local stop_called = false
            local start_called = false
            local stop_called_first = false

            instance.isRunning = function()
                return true
            end
            instance.stopServer = function(self, options)
                stop_called = true
                if not start_called then
                    stop_called_first = true
                end
                if options and options.callback then
                    options.callback(true)
                end
            end
            instance.start = function()
                start_called = true
            end

            instance:restart()

            assert.is_true(stop_called, "Should call stopServer")
            assert.is_true(start_called, "Should call start")
            assert.is_true(stop_called_first, "Should stop before starting")
        end)

        it("only starts when not running", function()
            local instance = helper.create_instance()

            local stop_called = false
            local start_called = false

            instance.isRunning = function()
                return false
            end
            instance.stopServer = function(self, silent)
                stop_called = true
            end
            instance.start = function()
                start_called = true
            end

            instance:restart()

            assert.is_false(stop_called, "Should not call stopServer when not running")
            assert.is_true(start_called, "Should call start")
        end)
    end)
end)
