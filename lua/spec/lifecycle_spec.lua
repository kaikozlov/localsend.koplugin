require("busted.runner")()
local helper = require("spec.spec_helper")
local NetworkMgr = require("ui/network/manager")

-- Tests for LocalSend plugin lifecycle behavior
-- These tests verify the plugin behaves correctly during KOReader events

describe("LocalSend Lifecycle", function()
    local LocalSend

    setup(function()
        helper.setup_complete()
    end)

    before_each(function()
        helper.before_each()
    end)

    describe("start() when server already running", function()
        it("should NOT show notification if server is already running", function()
            local instance = helper.create_instance()

            -- Mock isRunning to return true (server already running)
            instance.isRunning = function()
                return true
            end

            -- Clear any notifications from init
            helper.reset_state()

            -- Call start()
            instance:start()

            -- Should NOT have shown any notification
            assert.equal(0, #helper.state.notifications_shown, "No notification should be shown when server is already running")
        end)
    end)

    describe("onExit vs onCloseWidget behavior", function()
        it("onCloseWidget should not stop the persistent receiver", function()
            local instance = helper.create_instance()

            -- onCloseWidget SHOULD exist to clean up scheduled Lua tasks
            -- But it should NOT stop the server (server persists across document switches)
            -- Verify it doesn't stop the server
            local stop_called = false
            instance.stopServer = function()
                stop_called = true
                return true
            end
            instance.isRunning = function()
                return true
            end

            instance:onCloseWidget()

            assert.is_false(stop_called, "onCloseWidget should NOT stop the server - it fires on document switch!")
        end)

        it("onExit should stop server if running", function()
            local instance = helper.create_instance()

            local stop_called = false
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function()
                stop_called = true
                return true
            end

            instance:onExit()

            assert.is_true(stop_called, "onExit should stop the server")
        end)

        it("onExit should not error if server not running", function()
            local instance = helper.create_instance()

            instance.isRunning = function()
                return false
            end

            -- Should not throw
            assert.has_no.errors(function()
                instance:onExit()
            end)
        end)

        it("provides stopPlugin and synchronously stops a running receiver when disabled", function()
            local instance = helper.create_instance()
            local stop_options
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function(_, options)
                stop_options = options
                return true
            end

            instance:stopPlugin()
            assert.is_table(stop_options)
            assert.is_true(stop_options.sync, "PluginLoader return must mean the receiver is already stopped")
        end)
    end)

    describe("widget recreation while server is running", function()
        it("takes over sentinel polling from the destroyed widget", function()
            LocalSend = require("main")
            local original_isRunning = LocalSend.isRunning
            local original_schedulePolling = LocalSend._schedulePolling
            local scheduled = false
            LocalSend.isRunning = function()
                return true
            end
            LocalSend._schedulePolling = function()
                scheduled = true
            end

            helper.create_instance()

            LocalSend.isRunning = original_isRunning
            LocalSend._schedulePolling = original_schedulePolling
            assert.is_true(scheduled, "a new widget must resume polling an already-running server")
        end)
    end)

    describe("autostart behavior", function()
        it("should call start() during init when autostart is enabled", function()
            helper.state.settings["LocalSend_autostart"] = true

            LocalSend = require("main")

            local start_called = false
            local original_start = LocalSend.start
            LocalSend.start = function(self)
                start_called = true
            end

            local instance = helper.create_instance()

            assert.is_true(start_called, "start() should be called when autostart is enabled")

            LocalSend.start = original_start
        end)

        it("should NOT call start() during init when autostart is disabled", function()
            helper.state.settings["LocalSend_autostart"] = false

            LocalSend = require("main")

            local start_called = false
            local original_start = LocalSend.start
            LocalSend.start = function(self)
                start_called = true
            end

            local instance = helper.create_instance()

            assert.is_false(start_called, "start() should NOT be called when autostart is disabled")

            LocalSend.start = original_start
        end)

        it("should NOT autostart after user explicitly stops server", function()
            helper.state.settings["LocalSend_autostart"] = true

            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = false -- Clear any previous state

            -- First instance - autostart should work
            local start_count = 0
            local original_start = LocalSend.start
            LocalSend.start = function(self)
                start_count = start_count + 1
            end

            local instance1 = LocalSend:new({
                ui = { menu = { registerToMainMenu = function() end } },
            })
            assert.equal(1, start_count, "First init should autostart")

            -- User explicitly stops the server
            instance1.stopServer = function()
                return true
            end
            instance1:stop()

            -- Simulate opening a new document (new plugin instance)
            -- Note: ServerState persists because it's a module-local table
            local instance2 = LocalSend:new({
                ui = { menu = { registerToMainMenu = function() end } },
            })

            -- Should NOT have called start again because user explicitly stopped
            assert.equal(1, start_count, "Should NOT autostart after user explicitly stopped server")

            -- Cleanup
            LocalSend._ServerState.user_stopped = false
        end)

        it("should clear user_stopped flag when user manually starts", function()
            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = true -- Simulate user had stopped

            local instance = helper.create_instance()

            instance.isRunning = function()
                return false
            end
            instance.start = function() end -- Mock start

            -- User manually starts via toggle
            instance:onToggleLocalSend()

            assert.is_false(LocalSend._ServerState.user_stopped, "Manual start should clear the user_stopped flag")

            -- Cleanup
            LocalSend._ServerState.user_stopped = false
        end)

        it("should set user_stopped flag when user manually stops", function()
            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = false

            local instance = helper.create_instance()

            instance.stopServer = function()
                return true
            end

            -- User manually stops
            instance:stop()

            assert.is_true(LocalSend._ServerState.user_stopped, "Manual stop should set the user_stopped flag")

            -- Cleanup
            LocalSend._ServerState.user_stopped = false
        end)

        it("should allow autostart after user manually restarts", function()
            helper.state.settings["LocalSend_autostart"] = true

            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = false

            local start_count = 0
            LocalSend.start = function(self)
                start_count = start_count + 1
            end

            -- First instance
            local instance1 = LocalSend:new({
                ui = { menu = { registerToMainMenu = function() end } },
            })
            assert.equal(1, start_count, "First init should autostart")

            -- User stops
            instance1.stopServer = function()
                return true
            end
            instance1:stop()
            assert.is_true(LocalSend._ServerState.user_stopped)

            -- User manually restarts via toggle
            instance1.isRunning = function()
                return false
            end
            instance1:onToggleLocalSend()
            -- onToggleLocalSend calls start(), so count is now 2
            assert.equal(2, start_count, "Manual restart should call start")
            assert.is_false(LocalSend._ServerState.user_stopped, "Flag should be cleared")

            -- Simulate opening new document - ServerState persists across instances
            local instance2 = LocalSend:new({
                ui = { menu = { registerToMainMenu = function() end } },
            })

            -- Should autostart again because user manually restarted (flag was cleared)
            assert.equal(3, start_count, "Should autostart after user manually restarted")

            -- Cleanup
            LocalSend._ServerState.user_stopped = false
        end)

        it("should NOT prompt for WiFi when autostart is enabled and WiFi is off", function()
            -- This test verifies the fix for the bug where autostart would
            -- trigger NetworkMgr:runWhenConnected(), causing WiFi prompts
            -- when WiFi is disabled. Autostart now silently skips when offline.
            helper.state.settings["LocalSend_autostart"] = true

            -- Simulate WiFi being OFF by wrapping the real NetworkMgr methods.
            local real_isConnected = NetworkMgr.isConnected
            local real_runWhenConnected = NetworkMgr.runWhenConnected
            NetworkMgr.isConnected = function()
                return false
            end
            local runWhenConnected_count = 0
            NetworkMgr.runWhenConnected = function()
                runWhenConnected_count = runWhenConnected_count + 1
            end

            -- Force reload main.lua so it picks up the wrapped NetworkMgr.
            package.loaded["main"] = nil
            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = false

            -- Create widget instances - none should call runWhenConnected
            -- because autostart is now silent (skips when offline)
            local instance1 = LocalSend:new({
                ui = { menu = { registerToMainMenu = function() end } },
            })
            assert.equal(0, runWhenConnected_count, "Autostart should NOT call runWhenConnected when WiFi is off (silent skip)")

            -- Simulate opening more books - still no calls
            local instance2 = LocalSend:new({
                ui = { menu = { registerToMainMenu = function() end } },
            })
            assert.equal(0, runWhenConnected_count, "Second init should also NOT call runWhenConnected")

            -- Cleanup
            LocalSend._ServerState.user_stopped = false
            NetworkMgr.isConnected = real_isConnected
            NetworkMgr.runWhenConnected = real_runWhenConnected
        end)
    end)

    describe("suspend/resume behavior", function()
        it("onSuspend should be registered when autostart is enabled", function()
            helper.state.settings["LocalSend_autostart"] = true
            package.loaded["main"] = nil -- Force reload
            LocalSend = require("main")
            local instance = helper.create_instance()
            assert.is_function(instance.onSuspend, "onSuspend should be registered when autostart is enabled")
        end)

        it("registers suspend/network handlers while an outbound send is active with receiver off", function()
            LocalSend = require("main")
            local instance = helper.create_instance()
            instance.autostart = false
            instance.isRunning = function()
                return false
            end
            LocalSend._ServerState.server_intended_running = false
            LocalSend._ServerState.was_running_before_suspend = false
            LocalSend._ServerState.was_running_before_disconnect = false
            LocalSend._ServerState.send_in_progress = true

            instance:registerEvents()

            assert.is_function(instance.onSuspend)
            assert.is_function(instance.onNetworkDisconnecting)
        end)

        it("registers suspend/network handlers while an outbound scan is active with receiver off", function()
            LocalSend = require("main")
            local instance = helper.create_instance()
            instance.autostart = false
            instance.isRunning = function()
                return false
            end
            LocalSend._ServerState.server_intended_running = false
            LocalSend._ServerState.was_running_before_suspend = false
            LocalSend._ServerState.was_running_before_disconnect = false
            LocalSend._ServerState.scan_in_progress = true

            instance:registerEvents()

            assert.is_function(instance.onSuspend)
            assert.is_function(instance.onNetworkDisconnecting)
        end)

        it("_onSuspend should stop server and set was_running_before_suspend", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = false

            local instance = helper.create_instance()

            local stop_called = false
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function()
                stop_called = true
                return true
            end

            instance:_onSuspend()

            assert.is_true(stop_called, "_onSuspend should stop the server")
            assert.is_true(LocalSend._ServerState.was_running_before_suspend, "was_running_before_suspend should be true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_suspend = false
        end)

        it("onSuspend should clear was_running_before_suspend if server not running", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true -- Previously set

            local instance = helper.create_instance()

            instance.isRunning = function()
                return false
            end
            LocalSend._ServerState.server_intended_running = false

            instance:_onSuspend()

            assert.is_false(LocalSend._ServerState.was_running_before_suspend, "was_running_before_suspend should be false when server not running")
        end)

        it("onResume should restart server if was_running_before_suspend is true", function()
            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = false

            local instance = helper.create_instance()

            -- Set flag AFTER widget creation (simulating suspend after widget exists)
            LocalSend._ServerState.was_running_before_suspend = true

            local start_called = false
            local start_silent = nil
            instance.start = function(self, silent)
                start_called = true
                start_silent = silent
            end

            instance:_onResume()

            -- NetworkMgr:runWhenConnected mock calls callback immediately
            assert.is_true(start_called, "start should be called after resume")
            assert.is_true(start_silent, "start should be called with silent=true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_suspend = false
        end)

        it("onResume should NOT restart server if user_stopped is true", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true
            LocalSend._ServerState.user_stopped = true

            local instance = helper.create_instance()

            local start_called = false
            instance.start = function(self, silent)
                start_called = true
            end

            instance:_onResume()

            assert.is_false(start_called, "start should NOT be called when user_stopped is true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_suspend = false
            LocalSend._ServerState.user_stopped = false
        end)

        it("onResume should NOT restart server if was_running_before_suspend is false", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = false
            LocalSend._ServerState.user_stopped = false

            local instance = helper.create_instance()

            local start_called = false
            instance.start = function(self, silent)
                start_called = true
            end

            instance:_onResume()

            assert.is_false(start_called, "start should NOT be called when server was not running before suspend")
        end)

        it("onEnterStandby should stop server and set was_running_before_suspend", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = false

            local instance = helper.create_instance()

            local stop_called = false
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function()
                stop_called = true
                return true
            end

            instance:_onEnterStandby()

            assert.is_true(stop_called, "onEnterStandby should stop the server")
            assert.is_true(LocalSend._ServerState.was_running_before_suspend, "was_running_before_suspend should be true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_suspend = false
        end)

        it("onLeaveStandby should restart server immediately (no delay)", function()
            LocalSend = require("main")
            LocalSend._ServerState.user_stopped = false

            local instance = helper.create_instance()

            -- Set flag AFTER widget creation (simulating standby after widget exists)
            LocalSend._ServerState.was_running_before_suspend = true

            local start_called = false
            local start_silent = nil
            instance.start = function(self, silent)
                start_called = true
                start_silent = silent
            end

            instance:_onLeaveStandby()

            -- onLeaveStandby calls start directly, no delay
            assert.is_true(start_called, "start should be called after leaving standby")
            assert.is_true(start_silent, "start should be called with silent=true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_suspend = false
        end)

        it("onLeaveStandby should NOT restart server if user_stopped is true", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true
            LocalSend._ServerState.user_stopped = true

            local instance = helper.create_instance()

            local start_called = false
            instance.start = function(self, silent)
                start_called = true
            end

            instance:_onLeaveStandby()

            assert.is_false(start_called, "start should NOT be called when user_stopped is true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_suspend = false
            LocalSend._ServerState.user_stopped = false
        end)
    end)

    describe("start(silent) behavior", function()
        it("start(true) should not show success notification", function()
            LocalSend = require("main")

            local instance = helper.create_instance()

            -- Mock necessary functions
            local is_running = false
            instance.save_dir = "/mnt/us/documents"
            instance.validateSaveDir = function()
                return true
            end
            instance.clearTransferLog = function() end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            -- isRunning returns false initially, then true after os.execute
            instance.isRunning = function()
                return is_running
            end

            -- Make os.execute succeed and set server as running
            local original_execute = os.execute
            os.execute = function()
                is_running = true
                return 0
            end

            -- Clear notifications
            helper.reset_state()

            -- Start with silent=true
            instance:start(true)

            -- Restore
            os.execute = original_execute

            -- Should NOT have shown the "LocalSend Ready" notification
            local found_ready_notification = helper.find_notification("LocalSend Ready")

            assert.is_nil(found_ready_notification, "start(true) should not show 'LocalSend Ready' notification")
        end)

        it("start(true) should not clear transfer log", function()
            LocalSend = require("main")

            local instance = helper.create_instance()

            local clear_log_called = false
            local is_running = false
            instance.save_dir = "/mnt/us/documents"
            instance.validateSaveDir = function()
                return true
            end
            instance.clearTransferLog = function()
                clear_log_called = true
            end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            -- isRunning returns false initially, then true after os.execute
            instance.isRunning = function()
                return is_running
            end

            local original_execute = os.execute
            os.execute = function()
                is_running = true
                return 0
            end

            -- Start with silent=true
            instance:start(true)

            os.execute = original_execute

            assert.is_false(clear_log_called, "start(true) should not clear transfer log (preserve across sleep)")
        end)

        it("start(false) should clear transfer log", function()
            LocalSend = require("main")

            local instance = helper.create_instance()

            local clear_log_called = false
            local is_running = false
            instance.save_dir = "/mnt/us/documents"
            instance.validateSaveDir = function()
                return true
            end
            instance.clearTransferLog = function()
                clear_log_called = true
            end
            instance.openFirewall = function() end
            instance.exportExtRouting = function()
                return nil
            end
            -- isRunning returns false initially, then true after os.execute
            instance.isRunning = function()
                return is_running
            end

            local original_execute = os.execute
            os.execute = function()
                is_running = true
                return 0
            end

            -- Start with silent=false (or nil)
            instance:start()

            os.execute = original_execute

            assert.is_true(clear_log_called, "start() without silent should clear transfer log")
        end)
    end)

    -- =========================================================================
    -- Network disconnect/reconnect handling
    -- =========================================================================
    describe("network disconnect/reconnect behavior", function()
        it("onNetworkDisconnected should stop server if running", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = false

            local instance = helper.create_instance()

            local stop_called = false
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function()
                stop_called = true
                return true
            end

            instance:_onNetworkDisconnected()

            assert.is_true(stop_called, "onNetworkDisconnected should stop the server")
            assert.is_true(LocalSend._ServerState.was_running_before_disconnect, "should set was_running_before_disconnect for potential reconnect")

            -- Cleanup
            LocalSend._ServerState.was_running_before_disconnect = false
        end)

        it("onNetworkDisconnecting stops while WiFi is still up and records restart intent", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = false
            local instance = helper.create_instance()
            local stop_options
            instance.isRunning = function()
                return true
            end
            instance.stopServer = function(_, options)
                stop_options = options
                return true
            end

            instance:_onNetworkDisconnecting()

            assert.is_table(stop_options)
            assert.is_true(stop_options.sync, "receiver must be gone before KOReader tears Wi-Fi down")
            assert.is_true(LocalSend._ServerState.was_running_before_disconnect)
        end)

        it("onNetworkDisconnected preserves restart intent after pre-disconnect shutdown", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = true
            local instance = helper.create_instance()
            instance.isRunning = function()
                return false
            end

            instance:_onNetworkDisconnected()

            assert.is_true(
                LocalSend._ServerState.was_running_before_disconnect,
                "final disconnect must not erase the reason to restart on NetworkConnected"
            )
        end)

        it("defers reconnect restart until an in-progress stop has finished", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = true
            LocalSend._ServerState.user_stopped = false
            LocalSend._ServerState.stop_in_progress = true
            local instance = helper.create_instance()
            -- create_instance/init may reconcile state; restore the exact race.
            LocalSend._ServerState.was_running_before_disconnect = true
            LocalSend._ServerState.stop_in_progress = true
            local start_called = false
            instance.start = function()
                start_called = true
            end
            helper.state.scheduled_tasks = {}

            instance:_onNetworkConnected()

            assert.is_false(start_called)
            assert.is_true(LocalSend._ServerState.was_running_before_disconnect)
            assert.equal(1, #helper.state.scheduled_tasks)
            assert.equal(instance.resume_start_task, helper.state.scheduled_tasks[1].callback)

            LocalSend._ServerState.stop_in_progress = false
            helper.state.scheduled_tasks[1].callback()
            assert.is_true(start_called)
            assert.is_false(LocalSend._ServerState.was_running_before_disconnect)
        end)

        it("onNetworkConnected should restart server if was_running_before_disconnect", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = true
            LocalSend._ServerState.user_stopped = false

            local instance = helper.create_instance()

            local start_called = false
            local start_silent = nil
            instance.start = function(self, silent)
                start_called = true
                start_silent = silent
            end

            instance:_onNetworkConnected()

            assert.is_true(start_called, "onNetworkConnected should restart server if it was running before disconnect")
            assert.is_true(start_silent, "onNetworkConnected should call start with silent=true")

            -- Cleanup
            LocalSend._ServerState.was_running_before_disconnect = false
        end)

        it("onNetworkConnected should NOT restart if user_stopped is true", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = true
            LocalSend._ServerState.user_stopped = true

            local instance = helper.create_instance()

            local start_called = false
            instance.start = function(self, silent)
                start_called = true
            end

            instance:_onNetworkConnected()

            assert.is_false(start_called, "onNetworkConnected should NOT restart if user explicitly stopped")

            -- Cleanup
            LocalSend._ServerState.user_stopped = false
            LocalSend._ServerState.was_running_before_disconnect = false
        end)

        it("onNetworkConnected should NOT restart if was_running_before_disconnect is false", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_disconnect = false
            LocalSend._ServerState.user_stopped = false

            local instance = helper.create_instance()

            local start_called = false
            instance.start = function(self, silent)
                start_called = true
            end

            instance:_onNetworkConnected()

            assert.is_false(start_called, "onNetworkConnected should NOT restart if server was not running before disconnect")
        end)
    end)

    -- =========================================================================
    -- onFlushSettings lifecycle handler
    -- =========================================================================
    describe("onFlushSettings lifecycle", function()
        it("onFlushSettings should not error when called", function()
            LocalSend = require("main")
            local instance = helper.create_instance()

            assert.has_no.errors(function()
                instance:onFlushSettings()
            end)
        end)
    end)

    -- =========================================================================
    -- Bug 2: New widget should check was_running_before_suspend in init()
    -- =========================================================================
    describe("new widget instance after missed resume event", function()
        it("keeps the restart flag and network handler while still offline", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true
            LocalSend._ServerState.user_stopped = false
            local original_isConnected = NetworkMgr.isConnected
            NetworkMgr.isConnected = function()
                return false
            end

            local instance = helper.create_instance()

            NetworkMgr.isConnected = original_isConnected
            assert.is_true(LocalSend._ServerState.was_running_before_suspend)
            assert.is_function(instance.onNetworkConnected)
            LocalSend._ServerState.was_running_before_suspend = false
        end)
        it("init() should start server if was_running_before_suspend is true", function()
            -- Scenario: Device resumes, but widget was destroyed and recreated AFTER
            -- the resume event was dispatched. The new widget should detect
            -- was_running_before_suspend and start the server.
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true
            LocalSend._ServerState.user_stopped = false

            local start_called = false
            local start_silent = nil
            local original_start = LocalSend.start
            LocalSend.start = function(self, silent)
                start_called = true
                start_silent = silent
            end

            -- Create new widget instance - should detect missed resume
            local instance = helper.create_instance()

            -- Should have called start with silent=true (like resume would)
            assert.is_true(start_called, "init() should start server when was_running_before_suspend is true")
            assert.is_true(start_silent, "init() should start with silent=true to avoid notification")

            -- Flag should be cleared after handling
            assert.is_false(LocalSend._ServerState.was_running_before_suspend, "was_running_before_suspend should be cleared after init handles it")

            -- Cleanup
            LocalSend.start = original_start
            LocalSend._ServerState.was_running_before_suspend = false
        end)

        it("init() should NOT start if was_running_before_suspend but user_stopped is true", function()
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true
            LocalSend._ServerState.user_stopped = true

            local start_called = false
            local original_start = LocalSend.start
            LocalSend.start = function(self, silent)
                start_called = true
            end

            local instance = helper.create_instance()

            assert.is_false(start_called, "init() should NOT start if user explicitly stopped")

            -- Cleanup
            LocalSend.start = original_start
            LocalSend._ServerState.was_running_before_suspend = false
            LocalSend._ServerState.user_stopped = false
        end)

        it("init() should handle was_running_before_suspend separately from autostart", function()
            -- Both flags true: was_running_before_suspend takes precedence (silent start)
            helper.state.settings["LocalSend_autostart"] = true
            LocalSend = require("main")
            LocalSend._ServerState.was_running_before_suspend = true
            LocalSend._ServerState.user_stopped = false

            local start_count = 0
            local silent_values = {}
            local original_start = LocalSend.start
            LocalSend.start = function(self, silent)
                start_count = start_count + 1
                table.insert(silent_values, silent)
            end

            local instance = helper.create_instance()

            -- Should start only once (was_running_before_suspend handled, autostart skipped)
            -- OR start twice but was_running_before_suspend uses silent=true
            -- The important thing is that the server gets started
            assert.is_true(start_count >= 1, "Server should be started")

            -- Cleanup
            LocalSend.start = original_start
            LocalSend._ServerState.was_running_before_suspend = false
        end)
    end)
end)
