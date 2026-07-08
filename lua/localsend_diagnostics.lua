-- localsend_diagnostics.lua
-- Troubleshooting and diagnostics helpers for LocalSend plugin

local constants = require("localsend_constants")
-- localsend_state holds ServerState (persists across widget recreations); the sender
-- records each send's outcome there so diagnostics can report send-side health.
local has_state, state_module = pcall(require, "localsend_state")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}
local paths = {}

local REPORT_TAIL_BYTES = 12 * 1024
local DIAG_SERVER_PID_FILE = "/tmp/localsend_diag_server.pid"
local DIAG_SERVER_OUTPUT_FILE = "/tmp/localsend_diag_server.out"

function M.init(d, p)
    deps = d
    paths = p or {}
end

local function boolLabel(value)
    return value and "yes" or "no"
end

local function statusLabel(ok, value)
    if ok then
        return "✓ " .. value
    end
    return "✗ " .. value
end

local function safeCall(fn, fallback)
    local ok, result = pcall(fn)
    if ok then
        return result
    end
    return fallback
end

-- Safely read a boolean from NetworkMgr. Wrapped (like retrieveNetworkInfo) so a
-- missing method on an older KOReader build can't crash diagnostics.
local function networkFlag(method)
    return safeCall(function()
        return deps.NetworkMgr and deps.NetworkMgr[method] and deps.NetworkMgr[method](deps.NetworkMgr) or false
    end, false)
end

local function serverState()
    return has_state and state_module and state_module.ServerState or nil
end

local function readFile(path)
    local ok, f = pcall(io.open, path, "r")
    if not ok or not f then
        return nil
    end
    local read_ok, content = pcall(f.read, f, "*a")
    f:close()
    if read_ok then
        return content
    end
    return nil
end

local function readTail(path, max_bytes)
    local ok, f = pcall(io.open, path, "r")
    if not ok or not f then
        return nil
    end

    local size = f:seek("end") or 0
    local start = math.max(0, size - max_bytes)
    f:seek("set", start)
    local read_ok, content = pcall(f.read, f, "*a")
    f:close()

    if not read_ok then
        return nil
    end
    if start > 0 then
        return "... (showing last " .. tostring(max_bytes) .. " bytes)\n" .. content
    end
    return content
end

local function readFirstLine(path)
    local ok, f = pcall(io.open, path, "r")
    if not ok or not f then
        return nil
    end
    local read_ok, line = pcall(f.read, f, "*l")
    f:close()
    if read_ok then
        return line
    end
    return nil
end

local function commandOutput(args)
    if not deps.util or not deps.util.shell_escape then
        return nil
    end
    local handle = io.popen(deps.util.shell_escape(args) .. " 2>&1")
    if not handle then
        return nil
    end
    local ok, output = pcall(handle.read, handle, "*a")
    handle:close()
    if ok then
        return output and output:gsub("%s+$", "") or ""
    end
    return nil
end

local function probeLocalAPI(instance)
    local scheme = instance.use_https and "https" or "http"
    local url = scheme .. "://127.0.0.1:" .. tostring(instance.port) .. "/api/localsend/v1/info"
    local args = { "curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "--connect-timeout", "1", "--max-time", "1" }
    if instance.use_https then
        table.insert(args, "-k")
    end
    table.insert(args, url)

    local output = commandOutput(args)
    if output == nil or output == "" then
        return "failed (no response)"
    end
    -- curl -w '%{http_code}' prints exactly three digits; anything else is an
    -- error message (e.g. "sh: curl: not found") captured via 2>&1.
    if not output:match("^%d%d%d$") then
        return "failed (" .. output:sub(1, 80) .. ")"
    end
    if output == "000" then
        return "failed (connection error)"
    end
    return "HTTP " .. output
end

local function stopDiagnosticServer(instance)
    local pid = tonumber(readFirstLine(DIAG_SERVER_PID_FILE) or "")
    if pid then
        local proc_path = "/proc/" .. tostring(pid)
        local cmdline = readFile(proc_path .. "/cmdline")
        cmdline = cmdline and cmdline:gsub("%z", " ") or ""
        if cmdline:match("localsend") and cmdline:match("recv") then
            os.execute("kill -TERM " .. tostring(pid) .. " 2>/dev/null")
            os.execute("sleep 1")
            if deps.util.pathExists(proc_path) then
                os.execute("kill -KILL " .. tostring(pid) .. " 2>/dev/null")
            end
        end
    end
    os.remove(DIAG_SERVER_PID_FILE)
    if instance.closeFirewall then
        instance:closeFirewall()
    end
end

local function buildDiagnosticRecvArgs(instance)
    local args = { paths.binary_path, "recv", "-d", instance.save_dir, "-l", constants.TRANSFER_LOG_FILE }
    table.insert(args, "-n")
    table.insert(args, instance.device_name ~= "" and instance.device_name or "KOReader")
    if instance.pin ~= "" then
        table.insert(args, "-p")
        table.insert(args, instance.pin)
    end

    local effective_accept_ext = instance.accept_ext
    if instance.routing_enabled and next(instance.ext_dirs) then
        if not instance.routing_accept_all then
            local exts = {}
            for ext, _ in pairs(instance.ext_dirs) do
                table.insert(exts, ext)
            end
            effective_accept_ext = table.concat(exts, ",")
        else
            effective_accept_ext = ""
        end
    end
    if effective_accept_ext ~= "" then
        table.insert(args, "-a")
        table.insert(args, effective_accept_ext)
    end
    if not instance.use_https then
        table.insert(args, "--https=false")
    end
    if not instance.use_webrtc then
        table.insert(args, "-w=false")
    end
    if instance.exportExtRouting then
        local routing_path = instance:exportExtRouting()
        if routing_path then
            table.insert(args, "--ext-routing")
            table.insert(args, routing_path)
        end
    end
    table.insert(args, "--on-transfer")
    table.insert(args, "date +%s%N > " .. constants.TRANSFER_NOTIFY_FILE)
    if instance.use_webrtc then
        table.insert(args, "--signaling-id-file")
        table.insert(args, constants.SIGNALING_ID_FILE)
    end
    return args
end

function M._setServerProbeOverride(fn)
    M._serverProbeOverride = fn
end

function M._setFirewallProbeOverride(fn)
    M._firewallProbeOverride = fn
end

local function activeServerProbe(instance)
    if M._serverProbeOverride then
        return M._serverProbeOverride(instance)
    end
    if not deps.util.pathExists(paths.binary_path or "") then
        return { ok = false, detail = "binary missing", log_path = DIAG_SERVER_OUTPUT_FILE }
    end
    if instance.validateSaveDir then
        local valid, err = instance:validateSaveDir(instance.save_dir)
        if not valid then
            return { ok = false, detail = "invalid save directory: " .. tostring(err), log_path = DIAG_SERVER_OUTPUT_FILE }
        end
    end

    os.remove(DIAG_SERVER_OUTPUT_FILE)
    os.remove(DIAG_SERVER_PID_FILE)
    if instance.openFirewall then
        local firewall = instance:openFirewall()
        if type(firewall) == "table" and firewall.managed and not firewall.ok then
            stopDiagnosticServer(instance)
            return {
                ok = false,
                detail = "firewall open failed: " .. tostring(firewall.detail),
                log_path = DIAG_SERVER_OUTPUT_FILE,
            }
        end
    end

    local cmd = string.format(
        "(%s > %s 2>&1) & echo $! > %s",
        deps.util.shell_escape(buildDiagnosticRecvArgs(instance)),
        deps.util.shell_escape({ DIAG_SERVER_OUTPUT_FILE }),
        deps.util.shell_escape({ DIAG_SERVER_PID_FILE })
    )
    deps.logger.dbg("[LocalSend] Running diagnostic server probe")
    local start_result = os.execute(cmd)
    if start_result ~= 0 then
        stopDiagnosticServer(instance)
        return { ok = false, detail = "failed to start diagnostic server", log_path = DIAG_SERVER_OUTPUT_FILE }
    end

    local probe = "failed (no response)"
    for i = 1, 5 do
        probe = probeLocalAPI(instance)
        local code = probe and probe:match("^HTTP (%d+)")
        if code and code:sub(1, 1) == "2" then
            stopDiagnosticServer(instance)
            return { ok = true, detail = probe, log_path = DIAG_SERVER_OUTPUT_FILE }
        end
        if i < 5 then
            os.execute("sleep 1")
        end
    end

    stopDiagnosticServer(instance)
    return { ok = false, detail = probe, log_path = DIAG_SERVER_OUTPUT_FILE }
end

local function saveDirStatus(instance)
    local dir = instance.save_dir or ""
    if dir == "" then
        return "not configured"
    end
    if not deps.util.pathExists(dir) then
        return "missing: " .. dir
    end

    local test_path = dir .. "/.localsend_diag_write_test"
    local ok, f = pcall(io.open, test_path, "w")
    if not ok or not f then
        return "exists but not writable: " .. dir
    end
    f:write("ok")
    f:close()
    pcall(os.remove, test_path)
    return "writable: " .. dir
end

-- Free space for the filesystem containing `dir`, or "unknown". Full storage is a
-- common e-reader failure mode that otherwise surfaces as opaque transfer errors.
-- NOTE: the parser assumes single-line `df` output (BusyBox on the devices). GNU df
-- wraps long device names onto two lines, which breaks the regex and yields "unknown";
-- acceptable since that only affects a developer's local machine, not the e-reader.
local function diskFree(dir)
    if not dir or dir == "" then
        return "unknown"
    end
    local output = commandOutput({ "df", "-k", dir })
    if not output then
        return "unknown"
    end
    -- BusyBox/coreutils df: "... <1K-blocks> <Used> <Available> <Use%> <Mounted on>"
    local avail_kb = output:match("(%d+)%s+%d+%%")
    if not avail_kb then
        return "unknown"
    end
    return string.format("%.1f MB free", tonumber(avail_kb) / 1024)
end

-- TLS certificate status: existence plus age when available. "Rotate certificates"
-- is a common fix, so show why it might be needed.
local function certStatus()
    local crt = (paths.plugin_path or "") .. "/certs/server.crt"
    if not deps.util.pathExists(crt) then
        return "not generated yet (created on first HTTPS start)"
    end
    local lfs_ok, lfs = pcall(require, "libs/libkoreader-lfs")
    if lfs_ok and lfs and lfs.attributes then
        local attr_ok, mtime = pcall(lfs.attributes, crt, "modification")
        if attr_ok and type(mtime) == "number" then
            local age_days = math.floor((os.time() - mtime) / 86400)
            return "present, " .. age_days .. " day(s) old"
        end
    end
    return "present"
end

-- Architecture tags used by release packages and getDeviceArch(). A dev build without
-- ldflags reports raw GOARCH (e.g. "arm"), which is not in this set — we skip the
-- mismatch comparison for those to avoid false positives.
local KNOWN_ARCH_TAGS = {
    ["armv7"] = true,
    ["arm64"] = true,
    ["arm-legacy"] = true,
}

-- Runs `localsend --version` and classifies the binary. Shared by the full
-- diagnostics report (M.collect) and summary checks (M.runChecks).
-- `runs` requires the output to actually look like a version string: commandOutput
-- captures stderr too, so a wrong-architecture or non-executable binary produces
-- shell error text ("cannot execute binary file", "Permission denied") which is
-- non-empty but must not count as the binary running.
local function binaryStatus(instance)
    local exists = deps.util.pathExists(paths.binary_path or "")
    local version_output = exists and commandOutput({ paths.binary_path, "--version" }) or nil
    local runs = version_output ~= nil and version_output:match("v%d+%.%d+") ~= nil
    -- Arch token from "vX.Y.Z <goos>/<arch>"; only trust it when the binary ran.
    local arch = runs and version_output:match("/([%w%-]+)%s*$") or nil
    local device_arch = instance.getDeviceArch and (instance:getDeviceArch() or nil) or nil
    local mismatch = arch ~= nil and KNOWN_ARCH_TAGS[arch] and device_arch ~= nil and arch ~= device_arch or false
    return {
        exists = exists,
        runs = runs,
        version_output = version_output,
        arch = arch,
        device_arch = device_arch,
        mismatch = mismatch,
    }
end

local function firewallProbe(instance)
    if M._firewallProbeOverride then
        return M._firewallProbeOverride(instance)
    end
    if instance.testFirewall then
        return instance:testFirewall()
    end
    return { managed = false, ok = true, detail = "firewall module unavailable" }
end

local function checksFromReport(instance, report)
    local checks = {}

    local ip = report.network_info and report.network_info:match("(%d+%.%d+%.%d+%.%d+)") or nil
    if report.network_connected then
        table.insert(checks, { ok = true, label = "LAN connected", detail = ip and ("IP " .. ip) or nil })
    else
        table.insert(checks, {
            ok = false,
            label = "LAN connected",
            hint = "Enable Wi-Fi and connect to the same network as your other devices.",
        })
    end

    if not report.binary_exists then
        table.insert(checks, {
            ok = false,
            label = "Receiver binary present",
            hint = "The localsend binary is missing. Reinstall the plugin via " .. "Updates → Check for updates / reinstall.",
        })
    elseif not report.binary_runs then
        table.insert(checks, {
            ok = false,
            label = "Receiver binary runs",
            hint = "The binary is present but would not run. This usually means the wrong "
                .. "architecture package was installed, or the executable bit was lost when "
                .. "extracting the archive. Reinstall the package matching your device.",
        })
    elseif report.arch_mismatch then
        table.insert(checks, {
            ok = false,
            label = "Receiver binary architecture",
            detail = "binary " .. tostring(report.binary_arch) .. ", device " .. tostring(report.arch),
            hint = "The binary runs but its architecture does not match the device. Install the matching package.",
        })
    else
        table.insert(checks, {
            ok = true,
            label = "Receiver binary runs",
            detail = (report.binary_version or ""):gsub("%s+$", ""),
        })
    end

    if report.server.self_test and report.server.self_test.ok then
        table.insert(checks, { ok = true, label = "Server self-test", detail = report.server.self_test.detail })
    else
        table.insert(checks, {
            ok = false,
            label = "Server self-test",
            detail = report.server.self_test and report.server.self_test.detail or "unknown",
            hint = "The receiver did not start and answer locally. Check the diagnostic server self-test log below.",
        })
    end

    if report.firewall and not report.firewall.managed then
        table.insert(checks, { info = true, label = "Firewall", detail = report.firewall.detail })
    elseif report.firewall and report.firewall.ok then
        table.insert(checks, { ok = true, label = "Firewall self-test", detail = report.firewall.detail })
    else
        table.insert(checks, {
            ok = false,
            label = "Firewall self-test",
            detail = report.firewall and report.firewall.detail or "unknown",
            hint = "The plugin could not open the iptables rules for port "
                .. tostring(instance.port)
                .. ". Check permissions and iptables support on this device.",
        })
    end

    if instance.use_webrtc then
        if report.network_online then
            table.insert(checks, { ok = true, label = "Internet reachable (WebRTC)", detail = "online" })
        else
            table.insert(checks, {
                ok = false,
                label = "Internet reachable (WebRTC)",
                hint = "WebRTC needs Internet access to reach the signaling server. "
                    .. "Connect to the Internet, or disable WebRTC if you only need LAN transfers.",
            })
        end
    end

    local ls = serverState() and serverState().last_send or nil
    if ls then
        local age = os.time() - (ls.time or 0)
        local age_str = age < 60 and "just now" or string.format("%d min ago", math.floor(age / 60))
        if ls.success then
            table.insert(checks, {
                ok = true,
                label = "Last send",
                detail = (ls.message or "OK") .. "  (" .. age_str .. ")",
            })
        else
            table.insert(checks, {
                ok = false,
                label = "Last send",
                detail = (ls.message or "failed") .. "  (" .. age_str .. ")",
                hint = "A recent send failed. Common causes: the recipient is not running "
                    .. "LocalSend, a PIN mismatch, or the recipient's firewall blocks port "
                    .. tostring(instance.port)
                    .. ". Try Test discovery to confirm the recipient "
                    .. "is visible, then send again.",
            })
        end
    end

    return checks
end

local function formatCheckSummary(checks)
    local lines = {}
    local passed, failed = 0, 0
    for _, c in ipairs(checks or {}) do
        if not c.info then
            if c.ok then
                passed = passed + 1
            else
                failed = failed + 1
            end
        end
    end
    if failed == 0 then
        table.insert(lines, string.format("Result: all %d checks passed", passed))
    else
        table.insert(lines, string.format("Result: %d passed, %d to fix", passed, failed))
    end
    table.insert(lines, "")
    for _, c in ipairs(checks or {}) do
        local symbol = c.info and "•" or (c.ok and "✓" or "✗")
        local line = symbol .. " " .. tostring(c.label or "")
        if c.detail then
            line = line .. " — " .. tostring(c.detail)
        end
        table.insert(lines, line)
        if c.hint then
            for hint_line in c.hint:gmatch("[^\n]+") do
                table.insert(lines, "    → " .. hint_line)
            end
        end
    end
    if failed == 0 then
        table.insert(lines, "")
        table.insert(lines, "If other devices still can't find this one, run Test discovery to check multicast.")
    end
    return table.concat(lines, "\n")
end

function M.collect(instance)
    local binary = binaryStatus(instance)
    local server_probe = activeServerProbe(instance)
    local firewall_probe = firewallProbe(instance)
    local network_info = safeCall(function()
        if deps.Device and deps.Device.retrieveNetworkInfo then
            return deps.Device:retrieveNetworkInfo()
        end
        return nil
    end, nil)

    return {
        generated_at = os.date("%Y-%m-%d %H:%M:%S"),
        plugin_version = paths.plugin_version or "unknown",
        arch = instance.getDeviceArch and (instance:getDeviceArch() or "unknown") or "unknown",
        device_info = safeCall(function()
            return deps.Device and deps.Device.info and deps.Device:info() or "unknown"
        end, "unknown"),
        plugin_path = paths.plugin_path or "unknown",
        binary_path = paths.binary_path or "unknown",
        binary_exists = binary.exists,
        binary_runs = binary.runs,
        binary_version = binary.version_output,
        binary_arch = binary.arch,
        arch_mismatch = binary.mismatch,
        network_connected = networkFlag("isConnected"),
        network_online = networkFlag("isOnline"),
        network_info = network_info,
        settings = {
            port = instance.port,
            save_dir = instance.save_dir,
            save_dir_status = saveDirStatus(instance),
            save_dir_free = diskFree(instance.save_dir),
            tmp_free = diskFree("/tmp"),
            cert_status = certStatus(),
            device_name = instance.device_name ~= "" and instance.device_name or "KOReader",
            https = instance.use_https,
            webrtc = instance.use_webrtc,
            pin = instance.pin ~= "",
            accept_ext = instance.accept_ext ~= "" and instance.accept_ext or "all",
            routing_enabled = instance.routing_enabled,
            routing_rules = instance.ext_dirs,
            routing_accept_all = instance.routing_accept_all,
            autostart = instance.autostart,
        },
        server = {
            self_test = server_probe,
            probe_log_path = DIAG_SERVER_OUTPUT_FILE,
            backend_log_path = constants.SERVER_OUTPUT_FILE,
            transfer_log_path = constants.TRANSFER_LOG_FILE,
        },
        firewall = firewall_probe,
        logs = {
            backend = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES),
            diagnostic_server = readTail(DIAG_SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES),
            send = readTail(constants.SEND_OUTPUT_FILE, REPORT_TAIL_BYTES),
            transfers = readTail(constants.TRANSFER_LOG_FILE, REPORT_TAIL_BYTES),
            crash_log_path = (paths.data_dir or "KOReader data directory") .. "/crash.log",
        },
    }
end

local function addSection(lines, title)
    table.insert(lines, "")
    table.insert(lines, "## " .. title)
end

local function formatRoutes(routes)
    if not routes or next(routes) == nil then
        return "none"
    end
    local parts = {}
    for ext, dir in pairs(routes) do
        table.insert(parts, ext .. " → " .. dir)
    end
    table.sort(parts)
    return table.concat(parts, ", ")
end

function M.formatReport(report)
    local lines = {}
    table.insert(lines, "LocalSend Diagnostics")
    table.insert(lines, "Generated: " .. report.generated_at)

    addSection(lines, "Summary")
    table.insert(lines, formatCheckSummary(report.checks or {}))

    addSection(lines, "Plugin")
    table.insert(lines, "Version: " .. tostring(report.plugin_version))
    table.insert(lines, "Architecture: " .. tostring(report.arch))
    table.insert(lines, "Plugin path: " .. tostring(report.plugin_path))
    table.insert(lines, "Binary path: " .. tostring(report.binary_path))
    table.insert(lines, statusLabel(report.binary_exists, "Binary exists"))
    table.insert(lines, statusLabel(report.binary_runs, "Binary is executable / runs"))
    table.insert(lines, "Binary version: " .. tostring(report.binary_version or "unknown"))
    table.insert(lines, "Binary arch: " .. tostring(report.binary_arch or "unknown") .. "  (device: " .. tostring(report.arch) .. ")")
    if report.binary_exists and not report.binary_runs then
        table.insert(lines, "  The binary is present but did not run. This usually means the")
        table.insert(lines, "  wrong architecture package was installed, or the executable bit")
        table.insert(lines, "  was lost when extracting the archive. Reinstall the matching package.")
    elseif report.arch_mismatch then
        table.insert(
            lines,
            "  ✗ Binary arch ("
                .. tostring(report.binary_arch)
                .. ") does not match the device ("
                .. tostring(report.arch)
                .. "). Install the matching package for this device."
        )
    end
    table.insert(lines, "Device: " .. tostring(report.device_info))

    addSection(lines, "Network")
    table.insert(lines, statusLabel(report.network_connected, "LAN connected"))
    table.insert(lines, statusLabel(report.network_online, "Internet reachable"))
    table.insert(lines, "Network info:")
    table.insert(lines, tostring(report.network_info or "unavailable"))
    table.insert(lines, "Discovery tip: if other apps can't find this device, also check the")
    table.insert(lines, "  sender's firewall (port " .. tostring(report.settings.port) .. " TCP+UDP) and Local Network permission.")

    addSection(lines, "Settings")
    table.insert(lines, "Device name: " .. tostring(report.settings.device_name))
    table.insert(lines, "Port: " .. tostring(report.settings.port))
    table.insert(lines, "Save directory: " .. tostring(report.settings.save_dir))
    table.insert(lines, "Save directory status: " .. tostring(report.settings.save_dir_status))
    table.insert(lines, "Save directory space: " .. tostring(report.settings.save_dir_free))
    table.insert(lines, "/tmp space: " .. tostring(report.settings.tmp_free))
    table.insert(lines, "HTTPS: " .. boolLabel(report.settings.https))
    table.insert(lines, "TLS certificate: " .. tostring(report.settings.cert_status))
    table.insert(lines, "WebRTC: " .. boolLabel(report.settings.webrtc))
    table.insert(lines, "PIN enabled: " .. boolLabel(report.settings.pin))
    table.insert(lines, "Allowed extensions: " .. tostring(report.settings.accept_ext))
    table.insert(lines, "Routing enabled: " .. boolLabel(report.settings.routing_enabled))
    table.insert(lines, "Routing accept other files: " .. boolLabel(report.settings.routing_accept_all))
    table.insert(lines, "Routing rules: " .. formatRoutes(report.settings.routing_rules))
    table.insert(lines, "Autostart: " .. boolLabel(report.settings.autostart))

    addSection(lines, "Server self-test")
    table.insert(lines, statusLabel(report.server.self_test and report.server.self_test.ok, "Temporary server starts and local API responds"))
    table.insert(lines, "Result: " .. tostring(report.server.self_test and report.server.self_test.detail or "unknown"))
    table.insert(lines, "Diagnostic server log: " .. tostring(report.server.probe_log_path))
    table.insert(lines, "Previous backend log: " .. tostring(report.server.backend_log_path))
    table.insert(lines, "Transfer log: " .. tostring(report.server.transfer_log_path))

    addSection(lines, "Firewall")
    table.insert(
        lines,
        statusLabel(
            report.firewall and report.firewall.ok,
            report.firewall and report.firewall.managed and "iptables open/verify/close self-test" or "Plugin-managed firewall"
        )
    )
    table.insert(lines, "Result: " .. tostring(report.firewall and report.firewall.detail or "unknown"))

    addSection(lines, "Recent backend log")
    table.insert(lines, report.logs.backend or "No backend log found.")

    addSection(lines, "Recent diagnostic server self-test log")
    table.insert(lines, report.logs.diagnostic_server or "No diagnostic server self-test log found.")

    addSection(lines, "Recent send log")
    table.insert(lines, report.logs.send or "No send log found.")

    addSection(lines, "Recent transfer log")
    table.insert(lines, report.logs.transfers or "No transfer log found.")

    addSection(lines, "Bug report")
    table.insert(lines, "Attach this report and KOReader crash.log if you open a GitHub issue.")
    table.insert(lines, "crash.log: " .. tostring(report.logs.crash_log_path))
    table.insert(lines, "Note: review the report before posting publicly — it can include")
    table.insert(lines, "device names, file names, and network addresses.")

    return table.concat(lines, "\n")
end

function M.getReportText(instance)
    local report = M.collect(instance)
    report.checks = checksFromReport(instance, report)
    return M.formatReport(report)
end

-- Best-effort write of a report to the plugin cache dir
-- (data_dir/cache/localsend, matching the update module's cache subdir).
-- Returns the written path, or nil on failure. Not device-specific.
function M.saveReportText(text, filename)
    local report_dir = (paths.data_dir or "") .. "/cache/localsend"
    if deps.util and deps.util.makePath then
        if not deps.util.makePath(report_dir) then
            return nil
        end
    end
    local path = report_dir .. "/" .. (filename or "localsend-report.txt")
    local f = io.open(path, "w")
    if not f then
        return nil
    end
    local ok = pcall(f.write, f, text)
    f:close()
    if not ok then
        return nil
    end
    return path
end

function M.showDiagnostics(instance)
    local TextViewer = require("ui/widget/textviewer")
    local text = M.getReportText(instance)
    local saved = M.saveReportText(text)
    if saved then
        text = text .. "\n\nSaved to: " .. saved
    end
    deps.UIManager:show(TextViewer:new({
        title = deps._("LocalSend diagnostics"),
        text = text,
    }))
end

function M.showNetworkInfo()
    local text
    if deps.Device and deps.Device.retrieveNetworkInfo then
        text = deps.Device:retrieveNetworkInfo()
    else
        text = deps._("Could not retrieve network info.")
    end
    deps.UIManager:show(deps.InfoMessage:new({
        text = text,
    }))
end

function M.showRecentBackendLog()
    local TextViewer = require("ui/widget/textviewer")
    deps.UIManager:show(TextViewer:new({
        title = deps._("LocalSend backend log"),
        text = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES) or deps._("No backend log found."),
    }))
end

function M.showBugReport(instance)
    local TextViewer = require("ui/widget/textviewer")
    local report = M.getReportText(instance)

    -- Best-effort: append the tail of KOReader's crash.log so users paste it
    -- alongside the diagnostics report when filing an issue.
    local crash_log_path = (paths.data_dir and (paths.data_dir .. "/crash.log")) or "KOReader data directory/crash.log"
    local crash_tail = readTail(crash_log_path, REPORT_TAIL_BYTES)
    local crash_section = "\n\n## crash.log (tail)\nPath: " .. crash_log_path .. "\n\n" .. (crash_tail or "(crash.log not found or empty)")

    local checklist = deps._(
        "When reporting a LocalSend issue, please include:\n\n"
            .. "• Device model and firmware\n"
            .. "• KOReader version\n"
            .. "• LocalSend plugin version\n"
            .. "• Sender app/version\n"
            .. "• Steps to reproduce\n"
            .. "• KOReader crash.log\n"
            .. "• The diagnostics report below\n\n"
            .. "Review the report before posting publicly — it can include device names, "
            .. "file names, and network addresses.\n\n%1"
    )
    local text = deps.T(checklist, report) .. crash_section

    -- Save the full bug report so users can attach the file (over USB/cloud)
    -- instead of transcribing it from an e-ink screen.
    local saved = M.saveReportText(text, "localsend-bugreport.txt")
    if saved then
        text = text .. "\n\nSaved to: " .. saved
    end

    deps.UIManager:show(TextViewer:new({
        title = deps._("LocalSend bug report"),
        text = text,
    }))
end

-- =============================================================================
-- Diagnostics summary checks
-- =============================================================================
-- Runs an ordered set of checks and returns a structured list used by the
-- diagnostics summary. Each entry:
--   { ok = bool, info = bool, label = string, detail = string?, hint = string? }

function M.runChecks(instance)
    local report = M.collect(instance)
    return checksFromReport(instance, report)
end

-- =============================================================================
-- Discovery self-test (multicast loopback + peer attribution)
-- =============================================================================
-- Runs the Go `nettest` subcommand, which reports whether this device can send/receive its own
-- multicast discovery probe (loopback) and how many other LocalSend devices are advertising.
-- Those two signals attribute a "device not discovered" problem to this
-- device/network vs. the other device(s), instead of a vague "doesn't work".

-- Pure formatter (testable): turn a nettest result table into a human-readable diagnosis.
function M.formatDiscoveryResult(instance, r)
    r = r or {}
    local port = tostring(instance.port or constants.DEFAULT_PORT)
    local lines = {}
    table.insert(lines, "LocalSend Discovery Test")
    table.insert(lines, "")
    table.insert(lines, "Multicast loopback: " .. (r.loopback and "OK" or "FAILED"))
    table.insert(
        lines,
        "Other devices seen: "
            .. tostring(r.peers or 0)
            .. " (UDP announce: "
            .. tostring(r.udp_peers or 0)
            .. ", HTTP register: "
            .. tostring(r.register_peers or 0)
            .. ")"
    )
    if type(r.seen_aliases) == "table" and #r.seen_aliases > 0 then
        table.insert(lines, "Devices that responded: " .. table.concat(r.seen_aliases, ", "))
    end
    if type(r.local_ips) == "table" and #r.local_ips > 0 then
        table.insert(lines, "This device's IP: " .. table.concat(r.local_ips, ", "))
    end
    if r.bind_error and r.bind_error ~= "" then
        table.insert(lines, "Bind error: " .. tostring(r.bind_error))
    end
    if r.register_bind_error and r.register_bind_error ~= "" then
        table.insert(lines, "Register listener: could not bind (" .. tostring(r.register_bind_error) .. ");")
        table.insert(lines, "  devices answering only via HTTP register were not counted.")
    end
    table.insert(lines, "")

    table.insert(lines, "Diagnosis")
    local diag_lines
    if r.bind_error and r.bind_error ~= "" then
        diag_lines = {
            "Could not bind the LocalSend discovery port (" .. port .. ").",
            "Another process may be using it, or the OS blocked it.",
            "Try restarting KOReader.",
        }
    elseif (r.peers or 0) > 0 then
        -- Receiving another device's announcement proves multicast RX works on this
        -- device, regardless of whether our own probe looped back.
        diag_lines = {
            "Discovery is healthy: multicast works and " .. tostring(r.peers) .. " other",
            "device(s) were seen. If transfers still fail, the problem is the transfer",
            "itself (HTTPS, PIN, sender app), not discovery.",
        }
        if not r.loopback then
            table.insert(diag_lines, "(Self-loopback read FAILED, but peer announcements were")
            table.insert(diag_lines, "received, so multicast is working — ignore the FAILED line above.)")
        end
    elseif not r.loopback then
        diag_lines = {
            "Multicast discovery is NOT working on this device/network. Likely causes:",
            "  • Router AP/client isolation or a guest network",
            "  • Switch IGMP/multicast snooping blocking the group",
            "  • A firewall on this device blocking UDP " .. port,
            "Try a different Wi-Fi network or disable AP isolation on the router.",
        }
    else
        diag_lines = {
            "Multicast works on this device, but no other LocalSend devices responded.",
            "Either no other device is running LocalSend right now, or the OTHER",
            "devices' firewalls block discovery. Open LocalSend on another device to check.",
        }
    end
    for _, dl in ipairs(diag_lines) do
        table.insert(lines, dl)
    end
    table.insert(lines, "")
    table.insert(lines, "Note: the firewall on the other device must also allow LocalSend on")
    table.insert(lines, "port " .. port .. " (TCP + UDP). See the Troubleshooting guide.")

    return table.concat(lines, "\n")
end

local function readNetTestResult()
    local content = readFile(constants.NETTEST_OUTPUT_FILE)
    if not content or content == "" then
        return nil
    end
    if not deps.json then
        return nil
    end
    local decode_ok, result = pcall(deps.json.decode, content)
    if not decode_ok or type(result) ~= "table" then
        return nil
    end
    return result
end

-- Kill a nettest probe that outlived the poll window so it does not hold the
-- discovery port after the troubleshooting test is abandoned.
local function killStaleNetTest()
    local pid = tonumber(readFirstLine(constants.NETTEST_PID_FILE) or "")
    if pid then
        os.execute("kill " .. tostring(pid) .. " 2>/dev/null")
    end
    os.remove(constants.NETTEST_PID_FILE)
end

function M._pollDiscoveryTest(instance, attempts, deadline)
    local result = readNetTestResult()
    if result then
        os.remove(constants.NETTEST_OUTPUT_FILE)
        os.remove(constants.NETTEST_PID_FILE)
        instance:closeFirewall()
        local text = M.formatDiscoveryResult(instance, result)
        local TextViewer = require("ui/widget/textviewer")
        deps.UIManager:show(TextViewer:new({
            title = deps._("LocalSend discovery test"),
            text = text,
        }))
        return
    end
    if os.time() > deadline or attempts > 30 then
        killStaleNetTest()
        os.remove(constants.NETTEST_OUTPUT_FILE)
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("Discovery test timed out. Is the binary working? Try Run diagnostics first."),
            timeout = 4,
        }))
        instance:closeFirewall()
        return
    end
    deps.UIManager:scheduleIn(0.5, function()
        M._pollDiscoveryTest(instance, attempts + 1, deadline)
    end)
end

function M._launchNetTest(instance)
    os.remove(constants.NETTEST_OUTPUT_FILE)
    -- Ensure UDP 53317 is reachable for the probe on Kindle. Troubleshooting is
    -- only reachable while the normal server is stopped, so nettest can own the port.
    instance:openFirewall()
    -- Record the probe's PID so a timed-out test can be killed before the firewall closes.
    local cmd = string.format(
        "%s nettest --json -d %d > %s 2>/dev/null & echo $! > %s",
        deps.util.shell_escape({ paths.binary_path }),
        constants.NETTEST_DURATION,
        deps.util.shell_escape({ constants.NETTEST_OUTPUT_FILE }),
        deps.util.shell_escape({ constants.NETTEST_PID_FILE })
    )
    deps.logger.dbg("[LocalSend] Running discovery test:", cmd)
    os.execute(cmd)
    deps.UIManager:show(deps.InfoMessage:new({
        text = deps._("Running discovery test… this takes a few seconds."),
        timeout = 3,
    }))
    M._pollDiscoveryTest(instance, 0, os.time() + constants.NETTEST_DURATION + 5)
end

function M.showDiscoveryTest(instance)
    if not deps.util.pathExists(paths.binary_path or "") then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("The localsend binary is missing. Reinstall the plugin first."),
        }))
        return
    end
    -- Settings/Troubleshooting is disabled while the normal server is running,
    -- so the discovery test can own the LocalSend port without stopping anything.
    -- nettest advertises itself, so the device is discoverable during the test.
    M._launchNetTest(instance)
end

return M
