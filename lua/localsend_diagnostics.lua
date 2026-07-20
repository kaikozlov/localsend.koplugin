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

local function probeLocalAPI(instance, host)
    local scheme = instance.use_https and "https" or "http"
    local url = scheme .. "://" .. (host or "127.0.0.1") .. ":" .. tostring(instance.port) .. "/api/localsend/v1/info"
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

local function localAPIProbeSucceeded(probe)
    local code = probe and probe:match("^HTTP (%d+)")
    return code and code:sub(1, 1) == "2" or false
end

-- An HTTP status means curl completed a request. Connection/TLS failures and
-- missing curl do not produce one, and those are the only cases where the
-- process/listener fallback may declare the receiver started.
local function localAPIProbeAnswered(probe)
    return probe and probe:match("^HTTP %d%d%d") ~= nil
end

-- The check passed on process/listener evidence rather than a real API response.
-- This only happens when the API probe is present but never returned HTTP 2xx.
local function selfTestUsedFallback(self_test)
    return self_test and self_test.api_probe ~= nil and not localAPIProbeSucceeded(self_test.api_probe)
end

local function redactNetworkInfo(value)
    value = tostring(value or "unavailable")
    value = value:gsub("MAC:%s*[^\n]+", "MAC: [redacted]")
    value = value:gsub('SSID:%s*"[^\n]*"', 'SSID: "[redacted]"')
    return value
end

local function extractIPv4s(value)
    local seen, ips = {}, {}
    for line in tostring(value or ""):gmatch("[^\n]+") do
        local ip = line:match("IPv4:%s*(%d+%.%d+%.%d+%.%d+)")
        if ip and ip ~= "127.0.0.1" and not seen[ip] then
            seen[ip] = true
            table.insert(ips, ip)
        end
    end
    -- Some older device backends return a compact, unlabeled string.
    if #ips == 0 then
        local ip = tostring(value or ""):match("(%d+%.%d+%.%d+%.%d+)")
        if ip and ip ~= "127.0.0.1" and not seen[ip] then
            seen[ip] = true
            table.insert(ips, ip)
        end
    end
    return ips
end

local function probeLANAPIs(instance, network_info)
    local results = {}
    for _, ip in ipairs(extractIPv4s(network_info)) do
        table.insert(results, { ip = ip, detail = probeLocalAPI(instance, ip) })
    end
    return results
end

local function sanitizeCmdline(value)
    local parts = {}
    local redact_next = false
    for part in tostring(value or ""):gmatch("[^%z%s]+") do
        if redact_next then
            table.insert(parts, "[redacted]")
            redact_next = false
        else
            table.insert(parts, part)
            if part == "-p" or part == "--pin" then
                redact_next = true
            end
        end
    end
    return table.concat(parts, " ")
end

local function receiverProcessStatus()
    local pid = tonumber(readFirstLine(constants.PID_FILE) or "")
    if not pid then
        return { running = false, detail = "no receiver PID" }
    end
    local proc_path = "/proc/" .. tostring(pid)
    if not deps.util.pathExists(proc_path) then
        return { running = false, pid = pid, detail = "stale receiver PID" }
    end
    return {
        running = true,
        pid = pid,
        detail = sanitizeCmdline(readFile(proc_path .. "/cmdline") or "unavailable"),
    }
end

local function listenerStatus(instance)
    local port = tonumber(instance.port)
    if not port then
        return { ok = false, detail = "invalid port" }
    end
    local port_hex = string.format("%04X", port)
    local found, scopes = false, {}
    for _, item in ipairs({ { "/proc/net/tcp", "tcp" }, { "/proc/net/tcp6", "tcp6" }, { "/proc/net/udp", "udp" }, { "/proc/net/udp6", "udp6" } }) do
        local content = readFile(item[1]) or ""
        for line in content:gmatch("[^\n]+") do
            local address, state = line:match("%s[%dA-Fa-f]+:%s*([%dA-Fa-f]+):" .. port_hex .. "%s+[%dA-Fa-f]+:[%dA-Fa-f]+%s+(%x%x)")
            if address and ((item[2]:match("^tcp") and state == "0A") or item[2]:match("^udp")) then
                found = true
                local scope = address:match("^0+$") and "all interfaces" or (address == "0100007F" and "loopback only" or "specific interface")
                table.insert(scopes, item[2] .. ": " .. scope)
            end
        end
    end
    return { ok = found, detail = found and table.concat(scopes, ", ") or "no TCP/UDP listener found in /proc/net" }
end

local function tcpListenerReady(listeners)
    -- UDP alone can mean discovery is up before the HTTP(S) API listens.
    return listeners and listeners.ok and tostring(listeners.detail or ""):match("tcp") ~= nil
end

-- Older Kindles ship curl/OpenSSL that cannot handshake the receiver's Ed25519
-- certificate, so HTTPS API probes fail even while transfers from modern clients
-- succeed. When curl never gets an HTTP status, fall back to process + TCP
-- listener evidence instead of claiming the receiver did not start.
local function fallbackReceiverReady(instance, pid_file)
    local process
    if pid_file then
        local pid = tonumber(readFirstLine(pid_file) or "")
        if not pid then
            process = { running = false, detail = "no receiver PID" }
        else
            local proc_path = "/proc/" .. tostring(pid)
            if not deps.util.pathExists(proc_path) then
                process = { running = false, pid = pid, detail = "stale receiver PID" }
            else
                process = {
                    running = true,
                    pid = pid,
                    detail = sanitizeCmdline(readFile(proc_path .. "/cmdline") or "unavailable"),
                }
            end
        end
    else
        process = receiverProcessStatus()
    end
    local listeners = listenerStatus(instance)
    if process.running and tcpListenerReady(listeners) then
        return {
            ok = true,
            process = process,
            listeners = listeners,
            detail = "listening (" .. tostring(listeners.detail) .. ")",
        }
    end
    return {
        ok = false,
        process = process,
        listeners = listeners,
    }
end

local function captureEvidence()
    local crash_path = (paths.data_dir or "KOReader data directory") .. "/crash.log"
    return {
        captured_at = os.date("%Y-%m-%dT%H:%M:%S%z"),
        backend = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES),
        scan = readTail(constants.SCAN_LOG_FILE, REPORT_TAIL_BYTES),
        send = readTail(constants.SEND_OUTPUT_FILE, REPORT_TAIL_BYTES),
        transfers = readTail(constants.TRANSFER_LOG_FILE, REPORT_TAIL_BYTES),
        crash = readTail(crash_path, REPORT_TAIL_BYTES),
        crash_path = crash_path,
        lifecycle = readTail(constants.LIFECYCLE_LOG_FILE, REPORT_TAIL_BYTES),
    }
end

local function lastSendEvidence()
    local current = serverState() and serverState().last_send or nil
    if current then
        return current
    end
    if not deps.json or not deps.json.decode then
        return nil
    end
    local content = readFile(constants.LAST_SEND_EVIDENCE_FILE)
    if not content or content == "" then
        return nil
    end
    local ok, value = pcall(deps.json.decode, content)
    return ok and type(value) == "table" and value or nil
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

function M._setAsyncCollectOverride(fn)
    M._asyncCollectOverride = fn
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
        if localAPIProbeSucceeded(probe) then
            stopDiagnosticServer(instance)
            return { ok = true, detail = probe, log_path = DIAG_SERVER_OUTPUT_FILE }
        end
        if not localAPIProbeAnswered(probe) then
            local fallback = fallbackReceiverReady(instance, DIAG_SERVER_PID_FILE)
            if fallback.ok then
                stopDiagnosticServer(instance)
                return {
                    ok = true,
                    detail = fallback.detail .. "; API probe: " .. tostring(probe),
                    log_path = DIAG_SERVER_OUTPUT_FILE,
                    process = fallback.process,
                    listeners = fallback.listeners,
                    api_probe = probe,
                }
            end
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
        table.insert(checks, { ok = true, label = deps._("LAN connected"), detail = ip and ("IP " .. ip) or nil })
    else
        table.insert(checks, {
            ok = false,
            label = deps._("LAN connected"),
            hint = deps._("Enable Wi-Fi and connect to the same network as your other devices."),
        })
    end

    if not report.binary_exists then
        table.insert(checks, {
            ok = false,
            label = deps._("Receiver program present"),
            hint = deps._("The LocalSend receiver program is missing. Reinstall the plugin from Updates."),
        })
    elseif not report.binary_runs then
        table.insert(checks, {
            ok = false,
            label = deps._("Receiver program runs"),
            hint = deps._(
                "The receiver is installed but cannot run. Reinstall the package matching this device and preserve executable permissions."
            ),
        })
    elseif report.arch_mismatch then
        table.insert(checks, {
            ok = false,
            label = deps._("Receiver package matches this device"),
            detail = deps.T(deps._("package %1, device %2"), tostring(report.binary_arch), tostring(report.arch)),
            hint = deps._("Install the LocalSend package matching this device."),
        })
    else
        table.insert(checks, {
            ok = true,
            label = deps._("Receiver program runs"),
            detail = (report.binary_version or ""):gsub("%s+$", ""),
        })
    end

    local save_status = report.settings and report.settings.save_dir_status or deps._("not configured")
    if save_status:match("^writable:") then
        table.insert(checks, { ok = true, label = deps._("Receive folder writable"), detail = save_status })
    else
        table.insert(checks, {
            ok = false,
            label = deps._("Receive folder writable"),
            detail = save_status,
            hint = deps._("Choose an existing folder that KOReader can write to."),
        })
    end

    if report.server.self_test and report.server.self_test.ok then
        table.insert(checks, { ok = true, label = deps._("Receiver self-test"), detail = report.server.self_test.detail })
    else
        table.insert(checks, {
            ok = false,
            label = deps._("Receiver self-test"),
            detail = report.server.self_test and report.server.self_test.detail or deps._("unknown"),
            hint = deps._("The receiver did not start and answer on this device. Check the backend log below."),
        })
    end

    if report.firewall and not report.firewall.managed then
        table.insert(checks, { info = true, label = deps._("Firewall"), detail = report.firewall.detail })
    elseif report.firewall and report.firewall.ok then
        table.insert(checks, { ok = true, label = deps._("Firewall rules"), detail = report.firewall.detail })
    else
        table.insert(checks, {
            ok = false,
            label = deps._("Firewall rules"),
            detail = report.firewall and report.firewall.detail or deps._("unknown"),
            hint = deps.T(deps._("The plugin could not open its firewall rules for port %1."), tostring(instance.port)),
        })
    end

    if instance.use_webrtc then
        if report.network_online then
            table.insert(checks, { ok = true, label = deps._("Internet reachable (WebRTC)"), detail = deps._("online") })
        else
            table.insert(checks, {
                ok = false,
                label = deps._("Internet reachable (WebRTC)"),
                hint = deps._("WebRTC needs Internet access. Connect to the Internet or disable WebRTC for LAN-only transfers."),
            })
        end
    end

    local ls = report.last_send or lastSendEvidence()
    if ls then
        local age = os.time() - (ls.time or 0)
        local age_str = age < 60 and deps._("just now") or deps.T(deps._("%1 min ago"), math.floor(age / 60))
        if ls.status == "in_progress" or ls.status == "awaiting_pin" then
            table.insert(checks, {
                info = true,
                label = deps._("Last send"),
                detail = (ls.message or ls.status) .. "  (" .. age_str .. ")",
            })
        elseif ls.success then
            table.insert(checks, {
                ok = true,
                label = deps._("Last send"),
                detail = (ls.message or deps._("OK")) .. "  (" .. age_str .. ")",
            })
        else
            table.insert(checks, {
                ok = false,
                label = deps._("Last send"),
                detail = (ls.message or deps._("failed")) .. "  (" .. age_str .. ")",
                hint = deps._("A recent send failed. Use Transfer failed? for a focused explanation."),
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
        table.insert(lines, deps.T(deps.N_("Result: all %1 check passed", "Result: all %1 checks passed", passed), passed))
    else
        table.insert(lines, deps.T(deps._("Result: %1 passed, %2 to fix"), passed, failed))
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
        table.insert(lines, deps._("If another device still cannot find this one, choose “Can't find a device?”."))
    end
    return table.concat(lines, "\n")
end

function M.collect(instance, options)
    options = options or {}
    local evidence = options.evidence or captureEvidence()
    local binary = binaryStatus(instance)
    local server_probe = options.server_probe or activeServerProbe(instance)
    local firewall_probe = options.firewall_probe or firewallProbe(instance)
    local network_info = safeCall(function()
        if deps.Device and deps.Device.retrieveNetworkInfo then
            return deps.Device:retrieveNetworkInfo()
        end
        return nil
    end, nil)

    local last_send = lastSendEvidence()
    local send_log = evidence.send
    if (not send_log or send_log == "") and last_send and last_send.raw_output and last_send.raw_output ~= "" then
        send_log = last_send.raw_output
    end

    return {
        generated_at = os.date("%Y-%m-%d %H:%M:%S"),
        generated_timezone = os.date("%z"),
        generated_unix = os.time(),
        plugin_version = paths.plugin_version or "unknown",
        arch = instance.getDeviceArch and (instance:getDeviceArch() or "unknown") or "unknown",
        device_info = safeCall(function()
            return deps.Device and deps.Device.info and deps.Device:info() or "unknown"
        end, "unknown"),
        platform = safeCall(function()
            if deps.Device:isKindle() then
                return "Kindle"
            elseif deps.Device.isKobo and deps.Device:isKobo() then
                return "Kobo"
            elseif deps.Device.isRemarkable and deps.Device:isRemarkable() then
                return "reMarkable"
            elseif deps.Device.isPocketBook and deps.Device:isPocketBook() then
                return "PocketBook"
            elseif deps.Device.isAndroid and deps.Device:isAndroid() then
                return "Android"
            end
            return "other"
        end, "unknown"),
        firmware = deps.Device and deps.Device.firmware_rev or nil,
        koreader_version = safeCall(function()
            return deps.Version and deps.Version:getCurrentRevision() or "unknown"
        end, "unknown"),
        kernel = commandOutput({ "uname", "-r" }) or "unknown",
        runtime_arch = commandOutput({ "uname", "-m" }) or "unknown",
        recovery_mode = instance.recovery_mode == true,
        reinstall_required = instance.reinstall_required == true,
        module_load_errors = instance.module_load_errors or {},
        plugin_path = paths.plugin_path or "unknown",
        binary_path = paths.binary_path or "unknown",
        binary_exists = binary.exists,
        binary_runs = binary.runs,
        binary_version = binary.version_output,
        binary_arch = binary.arch,
        arch_mismatch = binary.mismatch,
        network_connected = networkFlag("isConnected"),
        network_online = networkFlag("isOnline"),
        network_info = redactNetworkInfo(network_info),
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
            backend_log_path = constants.SERVER_OUTPUT_FILE,
            transfer_log_path = constants.TRANSFER_LOG_FILE,
            process = server_probe.process or receiverProcessStatus(),
            listeners = server_probe.listeners or listenerStatus(instance),
        },
        firewall = firewall_probe,
        discovery = M._last_discovery_result,
        last_send = last_send,
        logs = {
            captured_at = evidence.captured_at,
            backend_before_check = evidence.backend,
            backend_after_check = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES),
            scan = evidence.scan or readTail(constants.SCAN_LOG_FILE, REPORT_TAIL_BYTES),
            send = send_log,
            transfers = evidence.transfers,
            lifecycle = evidence.lifecycle or readTail(constants.LIFECYCLE_LOG_FILE, REPORT_TAIL_BYTES),
            crash = evidence.crash,
            crash_log_path = evidence.crash_path,
        },
    }
end

function M.collectAsync(instance, callback)
    if M._asyncCollectOverride then
        M._asyncCollectOverride(instance, callback)
        return
    end

    local was_running = instance.isRunning and instance:isRunning() or false
    local evidence = captureEvidence()

    local function finish(server_probe, firewall_probe)
        callback(M.collect(instance, { server_probe = server_probe, firewall_probe = firewall_probe, evidence = evidence }))
    end

    local function firewallStatus()
        if M._firewallProbeOverride then
            return M._firewallProbeOverride(instance)
        end
        if instance.checkFirewall then
            return instance:checkFirewall()
        end
        return { managed = false, ok = true, detail = "firewall inspection unavailable" }
    end

    local function stopTestReceiver(server_probe, firewall_probe)
        instance:stopServer({
            callback = function(success)
                if not success then
                    server_probe = {
                        ok = false,
                        detail = "receiver started but could not be stopped after its lifecycle test",
                        log_path = constants.SERVER_OUTPUT_FILE,
                    }
                end
                finish(server_probe, firewall_probe)
            end,
        })
    end

    local function startAndProbe()
        -- This deliberately calls the same start method as the normal plugin.
        -- It therefore validates its settings, command construction, firewall
        -- setup, PID handling, and readiness behavior as one lifecycle.
        instance:start(true)
        local function poll(attempt)
            local probe = probeLocalAPI(instance)
            local fallback = nil
            if not localAPIProbeSucceeded(probe) and not localAPIProbeAnswered(probe) then
                fallback = fallbackReceiverReady(instance)
            end
            if localAPIProbeSucceeded(probe) or (fallback and fallback.ok) then
                local network_info = safeCall(function()
                    return deps.Device and deps.Device.retrieveNetworkInfo and deps.Device:retrieveNetworkInfo() or nil
                end, nil)
                local detail, process, listeners, lan_probes
                if localAPIProbeSucceeded(probe) then
                    detail = probe
                    process = receiverProcessStatus()
                    listeners = listenerStatus(instance)
                    lan_probes = probeLANAPIs(instance, network_info)
                else
                    detail = fallback.detail .. "; API probe: " .. tostring(probe)
                    process = fallback.process
                    listeners = fallback.listeners
                    -- The local curl cannot reach the receiver's HTTPS API on this
                    -- device (the same reason this self-test used the process/listener
                    -- fallback), so probing the LAN IP would just reproduce that
                    -- failure. Record the addresses without claiming a probe result so
                    -- the report does not contradict the "ready" conclusion.
                    lan_probes = {}
                    for _, ip in ipairs(extractIPv4s(network_info)) do
                        table.insert(lan_probes, { ip = ip, detail = "not probed (local API probe unavailable on this device)" })
                    end
                end
                local server_probe = {
                    ok = true,
                    detail = detail,
                    log_path = constants.SERVER_OUTPUT_FILE,
                    lan_probes = lan_probes,
                    process = process,
                    listeners = listeners,
                    api_probe = probe,
                }
                local firewall_probe = firewallStatus()
                if was_running then
                    -- The freshly started receiver is the restored receiver.
                    finish(server_probe, firewall_probe)
                else
                    stopTestReceiver(server_probe, firewall_probe)
                end
            elseif attempt >= 50 then
                local failed = fallback or fallbackReceiverReady(instance)
                local server_probe = {
                    ok = false,
                    detail = probe,
                    log_path = constants.SERVER_OUTPUT_FILE,
                    process = failed.process,
                    listeners = failed.listeners,
                    api_probe = probe,
                }
                local firewall_probe = firewallStatus()
                instance:stopServer({
                    callback = function()
                        if was_running then
                            -- Preserve the user's original desired state even
                            -- after a failed probe; start() will surface details
                            -- in the backend log included with the report.
                            instance:start(true)
                        end
                        finish(server_probe, firewall_probe)
                    end,
                })
            else
                deps.UIManager:scheduleIn(0.1, function()
                    poll(attempt + 1)
                end)
            end
        end
        poll(1)
    end

    if not was_running then
        startAndProbe()
        return
    end

    instance:stopServer({
        callback = function(success)
            if success then
                startAndProbe()
            else
                finish({
                    ok = false,
                    detail = "could not stop the running receiver for its lifecycle test",
                    log_path = constants.SERVER_OUTPUT_FILE,
                }, {
                    managed = false,
                    ok = false,
                    detail = "not tested because the running receiver could not be stopped",
                })
            end
        end,
    })
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
    table.insert(lines, "Generated: " .. report.generated_at .. " " .. tostring(report.generated_timezone or ""))

    addSection(lines, "Reproduction details (fill in before posting)")
    table.insert(lines, "What happened: ")
    table.insert(lines, "Expected result: ")
    table.insert(lines, "Steps to reproduce: ")
    table.insert(lines, "Transfer direction: to / from e-reader")
    table.insert(lines, "Sender app and release: ")
    table.insert(lines, "File/folder type and approximate size: ")
    table.insert(lines, "Did either device sleep or disconnect? ")
    table.insert(lines, "Network setup (guest Wi-Fi, VLAN, repeater): ")

    addSection(lines, "Summary")
    table.insert(lines, formatCheckSummary(report.checks or {}))

    addSection(lines, "Plugin")
    table.insert(lines, "Version: " .. tostring(report.plugin_version))
    table.insert(lines, "KOReader revision: " .. tostring(report.koreader_version or "unknown"))
    table.insert(lines, "Platform: " .. tostring(report.platform or "unknown"))
    table.insert(lines, "Device: " .. tostring(report.device_info))
    table.insert(lines, "Firmware: " .. tostring(report.firmware or "not exposed by KOReader"))
    table.insert(lines, "Kernel: " .. tostring(report.kernel or "unknown"))
    table.insert(lines, "Runtime architecture: " .. tostring(report.runtime_arch or "unknown"))
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
    table.insert(lines, "Recovery mode: " .. boolLabel(report.recovery_mode))
    table.insert(lines, "Reinstall required: " .. boolLabel(report.reinstall_required))
    if report.module_load_errors and #report.module_load_errors > 0 then
        table.insert(lines, "Module load errors: " .. table.concat(report.module_load_errors, "; "))
    else
        table.insert(lines, "Module load errors: none")
    end

    addSection(lines, "Network")
    table.insert(lines, statusLabel(report.network_connected, "LAN connected"))
    table.insert(lines, statusLabel(report.network_online, "Internet reachable"))
    table.insert(lines, "Network info:")
    table.insert(lines, tostring(report.network_info or "unavailable"))
    table.insert(lines, "  SSID and MAC address are redacted in this public report.")
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

    addSection(lines, "Receiver lifecycle test")
    local self_test_ok = report.server.self_test and report.server.self_test.ok
    local lifecycle_label = "Real receiver starts and local API responds"
    if self_test_ok and selfTestUsedFallback(report.server.self_test) then
        -- Passed on process/listener evidence because the local API probe is
        -- unavailable on this device (e.g. curl cannot do Ed25519 TLS).
        lifecycle_label = "Real receiver starts and is listening on its port"
    end
    table.insert(lines, statusLabel(self_test_ok, lifecycle_label))
    table.insert(lines, "Result: " .. tostring(report.server.self_test and report.server.self_test.detail or "unknown"))
    local lan_probes = report.server.self_test and report.server.self_test.lan_probes or {}
    if #lan_probes == 0 then
        table.insert(lines, "LAN address probes: no non-loopback IPv4 address found")
    else
        for _, probe in ipairs(lan_probes) do
            table.insert(lines, "LAN address probe " .. tostring(probe.ip) .. ": " .. tostring(probe.detail))
        end
    end
    table.insert(lines, "Receiver process: " .. tostring(report.server.process and report.server.process.detail or "unknown"))
    table.insert(lines, "Receiver PID: " .. tostring(report.server.process and report.server.process.pid or "none"))
    table.insert(lines, "Listeners: " .. tostring(report.server.listeners and report.server.listeners.detail or "unknown"))
    table.insert(lines, "Backend log: " .. tostring(report.server.backend_log_path))
    table.insert(lines, "Transfer log: " .. tostring(report.server.transfer_log_path))

    addSection(lines, "Firewall")
    table.insert(
        lines,
        statusLabel(
            report.firewall and report.firewall.ok,
            report.firewall and report.firewall.managed and "Firewall rules installed by receiver" or "Plugin-managed firewall"
        )
    )
    table.insert(lines, "Result: " .. tostring(report.firewall and report.firewall.detail or "unknown"))

    addSection(lines, "Backend log before troubleshooting")
    table.insert(lines, "Captured: " .. tostring(report.logs.captured_at or "unknown"))
    table.insert(lines, report.logs.backend_before_check or "No pre-check backend log found.")

    addSection(lines, "Lifecycle test backend log")
    table.insert(lines, report.logs.backend_after_check or "No lifecycle-test backend log found.")

    addSection(lines, "Recent discovery scan log")
    table.insert(lines, report.logs.scan or "No discovery scan log found.")

    addSection(lines, "Recent send log")
    table.insert(lines, report.logs.send or "No send log found.")

    addSection(lines, "Recent transfer log")
    table.insert(lines, report.logs.transfers or "No transfer log found.")

    addSection(lines, "Last plugin send")
    if report.last_send then
        table.insert(lines, "Time: " .. os.date("%Y-%m-%dT%H:%M:%S%z", report.last_send.time or 0))
        table.insert(lines, "Status: " .. tostring(report.last_send.status or (report.last_send.success and "succeeded" or "failed")))
        table.insert(lines, "Success: " .. boolLabel(report.last_send.success))
        table.insert(lines, "Message: " .. tostring(report.last_send.message or "unknown"))
        table.insert(lines, "Exit code: " .. tostring(report.last_send.exit_code or "unknown"))
        table.insert(lines, "Category: " .. tostring(report.last_send.error_category or "none"))
        table.insert(lines, "Protocol: " .. tostring(report.last_send.protocol or "unknown"))
        table.insert(lines, "Recipient: " .. tostring(report.last_send.recipient or "unknown"))
        table.insert(lines, "File: " .. tostring(report.last_send.filename or "unknown"))
        table.insert(lines, "Size: " .. tostring(report.last_send.size or "unknown"))
        table.insert(lines, "Duration: " .. tostring(report.last_send.duration_seconds or "unknown") .. " second(s)")
    else
        table.insert(lines, "No send has completed in this KOReader session.")
    end

    addSection(lines, "Receiver/power/network lifecycle")
    table.insert(lines, report.logs.lifecycle or "No lifecycle events recorded.")

    addSection(lines, "Latest discovery test")
    if report.discovery then
        table.insert(lines, "Performed: " .. tostring(report.discovery.recorded_at or "unknown"))
        table.insert(lines, "Multicast loopback: " .. boolLabel(report.discovery.loopback))
        table.insert(lines, "Peers: " .. tostring(report.discovery.peers or 0))
        table.insert(lines, "UDP peers: " .. tostring(report.discovery.udp_peers or 0))
        table.insert(lines, "HTTP register peers: " .. tostring(report.discovery.register_peers or 0))
        table.insert(lines, "Local IPs: " .. table.concat(report.discovery.local_ips or {}, ", "))
        table.insert(lines, "Seen devices: " .. table.concat(report.discovery.seen_aliases or {}, ", "))
        table.insert(lines, "Bind error: " .. tostring(report.discovery.bind_error or "none"))
        table.insert(lines, "Register bind error: " .. tostring(report.discovery.register_bind_error or "none"))
    else
        table.insert(lines, "No discovery test has been run in this KOReader session.")
    end

    addSection(lines, "Bug report")
    table.insert(lines, "Attach this report and KOReader crash.log if you open a GitHub issue.")
    table.insert(lines, "crash.log: " .. tostring(report.logs.crash_log_path))
    table.insert(lines, "Note: review the report before posting publicly — it can include")
    table.insert(lines, "device aliases, file names, routing paths, and private IP addresses.")
    table.insert(lines, "MAC addresses and Wi-Fi SSIDs are redacted by default.")

    return table.concat(lines, "\n")
end

function M.getReportText(instance, report)
    report = report or M.collect(instance)
    report.checks = checksFromReport(instance, report)
    return M.formatReport(report)
end

local function saveDirectoryIsWritable(report)
    local status = report.settings and report.settings.save_dir_status or ""
    return status:match("^writable:") ~= nil
end

-- Reduce the diagnostic report to one user-facing conclusion and one useful
-- next action. Raw checks and logs remain available through Technical details.
function M.classifyReport(_, report)
    report = report or {}
    if not report.network_connected then
        return {
            id = "offline",
            title = deps._("Wi-Fi is not connected"),
            text = deps._("Connect this device to the same Wi-Fi network as the phone or computer you want to use."),
            action = "connect_wifi",
            action_label = deps._("Connect Wi-Fi"),
        }
    end
    if not report.binary_exists then
        return {
            id = "binary_missing",
            title = deps._("LocalSend needs to be reinstalled"),
            text = deps._("The receiver program is missing from the plugin installation."),
            action = "reinstall",
            action_label = deps._("Reinstall"),
        }
    end
    if not report.binary_runs or report.arch_mismatch then
        return {
            id = "binary_invalid",
            title = deps._("The installed package does not work on this device"),
            text = deps._("Reinstall the LocalSend package that matches this device. Also preserve executable permissions when extracting it."),
            action = "reinstall",
            action_label = deps._("Reinstall"),
        }
    end
    if not saveDirectoryIsWritable(report) then
        return {
            id = "save_dir",
            title = deps._("Files cannot be saved to the selected folder"),
            text = deps._("Choose an existing folder that KOReader can write to, then try the transfer again."),
            action = "choose_folder",
            action_label = deps._("Choose folder"),
        }
    end
    if not (report.server and report.server.self_test and report.server.self_test.ok) then
        return {
            id = "server",
            title = deps._("The LocalSend receiver could not start"),
            text = deps._("The receiver did not answer on this device. The backend log may explain why."),
            action = "backend_log",
            action_label = deps._("View log"),
        }
    end
    if report.firewall and report.firewall.managed and not report.firewall.ok then
        return {
            id = "firewall",
            title = deps._("LocalSend could not open its network port"),
            text = deps.T(deps._("This device could not allow LocalSend through its firewall on port %1."), tostring(constants.DEFAULT_PORT)),
            action = "details",
            action_label = deps._("View details"),
        }
    end
    return {
        id = "healthy",
        title = deps._("LocalSend is ready on this device"),
        text = deps._(
            "The receiver starts correctly and the save folder is writable. If another device still cannot find this one, test device discovery next."
        ),
        action = "discovery",
        action_label = deps._("Test discovery"),
    }
end

local function showTryWithoutHTTPS(instance)
    local ConfirmBox = require("ui/widget/confirmbox")
    deps.UIManager:show(ConfirmBox:new({
        text = deps._("This disables encrypted LocalSend transfers until you turn HTTPS back on in Settings. The receiver will restart."),
        ok_text = deps._("Disable HTTPS"),
        ok_callback = function()
            instance.use_https = false
            if deps.G_reader_settings then
                deps.G_reader_settings:makeFalse("LocalSend_use_https")
            elseif _G.G_reader_settings then
                _G.G_reader_settings:makeFalse("LocalSend_use_https")
            end
            instance:restart()
        end,
    }))
end

local function runResultAction(instance, result)
    if result.action == "connect_wifi" then
        deps.NetworkMgr:runWhenConnected(function()
            M.showGuidedCheck(instance)
        end)
    elseif result.action == "reinstall" then
        instance:checkForUpdates()
    elseif result.action == "choose_folder" then
        instance:showSaveDirPicker()
    elseif result.action == "backend_log" then
        M.showRecentBackendLog()
    elseif result.action == "discovery" then
        M.showDiscoveryHelp(instance)
    elseif result.action == "disable_https" then
        showTryWithoutHTTPS(instance)
    elseif result.action == "allowed_files" then
        deps.UIManager:show(deps.InfoMessage:new({
            text = deps._("Open LocalSend Settings and review Allowed extensions and File type routing."),
        }))
    elseif result.action == "support_report" then
        M.showBugReport(instance)
    else
        M.showDiagnostics(instance)
    end
end

local function showResult(instance, result, title)
    local TextViewer = require("ui/widget/textviewer")
    local dialog
    local secondary_buttons = {
        {
            text = deps._("Technical details"),
            callback = function()
                deps.UIManager:close(dialog)
                M.showDiagnostics(instance)
            end,
        },
    }
    local button_rows = {}
    if result.action and result.action_label then
        table.insert(button_rows, {
            {
                text = result.action_label,
                callback = function()
                    deps.UIManager:close(dialog)
                    runResultAction(instance, result)
                end,
            },
        })
    end
    table.insert(secondary_buttons, {
        text = deps._("Close"),
        callback = function()
            deps.UIManager:close(dialog)
        end,
    })
    table.insert(button_rows, secondary_buttons)
    dialog = TextViewer:new({
        title = title or deps._("LocalSend check"),
        text = result.title .. "\n\n" .. result.text,
        buttons_table = button_rows,
        show_menu = false,
    })
    deps.UIManager:show(dialog)
end

function M.showGuidedCheck(instance)
    local progress = deps.InfoMessage:new({
        text = deps._("Checking LocalSend…"),
        dismissable = false,
    })
    deps.UIManager:show(progress)
    -- Let the progress message reach the e-ink screen before any shell probes.
    deps.UIManager:scheduleIn(0.1, function()
        M.collectAsync(instance, function(report)
            report.checks = checksFromReport(instance, report)
            M._last_report = report
            local formatted, report_text = pcall(M.formatReport, report)
            if formatted then
                M.saveReportText(report_text)
            end
            deps.UIManager:close(progress)
            showResult(instance, M.classifyReport(instance, report), deps._("LocalSend check"))
        end)
    end)
end

-- Best-effort write of a report to the plugin cache dir
-- (data_dir/cache/localsend, matching the update module's cache subdir).
-- Returns the written path, or nil on failure. Not device-specific.
function M.saveReportText(text, filename, directory)
    local report_dir = directory or ((paths.data_dir or "") .. "/cache/localsend")
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

local function withReportAsync(instance, progress_text, callback, force_refresh)
    local last_generated = M._last_report and M._last_report.generated_unix
    if not force_refresh and last_generated and os.time() - last_generated < 300 then
        callback(M._last_report)
        return
    end
    local progress = deps.InfoMessage:new({
        text = progress_text,
        dismissable = false,
    })
    deps.UIManager:show(progress)
    deps.UIManager:scheduleIn(0.1, function()
        M.collectAsync(instance, function(report)
            report.checks = checksFromReport(instance, report)
            M._last_report = report
            deps.UIManager:close(progress)
            callback(report)
        end)
    end)
end

local function showDiagnosticsWithReport(instance, report)
    local TextViewer = require("ui/widget/textviewer")
    local text = M.getReportText(instance, report)
    local saved = M.saveReportText(text)
    if saved then
        text = text .. "\n\nSaved to: " .. saved
    end
    deps.UIManager:show(TextViewer:new({
        title = deps._("LocalSend diagnostics"),
        text = text,
    }))
end

function M.showDiagnostics(instance)
    withReportAsync(instance, deps._("Collecting technical details…"), function(report)
        showDiagnosticsWithReport(instance, report)
    end)
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

local function showBugReportWithReport(instance, collected_report)
    local TextViewer = require("ui/widget/textviewer")
    local report = M.getReportText(instance, collected_report)

    -- Best-effort: append the tail of KOReader's crash.log so users paste it
    -- alongside the diagnostics report when filing an issue.
    local crash_log_path = collected_report.logs and collected_report.logs.crash_log_path
        or ((paths.data_dir and (paths.data_dir .. "/crash.log")) or "KOReader data directory/crash.log")
    local crash_tail = collected_report.logs and collected_report.logs.crash or readTail(crash_log_path, REPORT_TAIL_BYTES)
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
    -- Put the report where received files normally go so it is easy to find
    -- over USB. Fall back to the plugin cache if that folder is unavailable.
    local export_dir = instance.save_dir
    local valid = instance.validateSaveDir and instance:validateSaveDir(export_dir)
    local saved = valid and M.saveReportText(text, "localsend-bugreport.txt", export_dir) or M.saveReportText(text, "localsend-bugreport.txt")
    if saved then
        text = text .. "\n\nSaved to: " .. saved
    end

    local dialog
    local buttons = {
        {
            {
                text = deps._("Copy report"),
                callback = function()
                    if deps.Device and deps.Device.input and deps.Device.input.setClipboardText then
                        deps.Device.input.setClipboardText(text)
                        deps.UIManager:show(deps.Notification:new({ text = deps._("Support report copied") }))
                    end
                end,
            },
            {
                text = deps._("Close"),
                callback = function()
                    deps.UIManager:close(dialog)
                end,
            },
        },
    }
    dialog = TextViewer:new({
        title = deps._("LocalSend support report"),
        text = text,
        buttons_table = buttons,
        text_type = "code",
    })
    deps.UIManager:show(dialog)
end

function M.showBugReport(instance)
    withReportAsync(instance, deps._("Creating support report…"), function(report)
        showBugReportWithReport(instance, report)
    end, true)
end

function M.captureTransferEvidence()
    local last_send = lastSendEvidence()
    return {
        last_send = last_send,
        backend = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES) or "",
        send_log = readTail(constants.SEND_OUTPUT_FILE, REPORT_TAIL_BYTES) or (last_send and last_send.raw_output) or "",
    }
end

local function errorLines(text)
    local lines = {}
    for line in tostring(text or ""):gmatch("[^\n]+") do
        local lower = line:lower()
        if
            lower:match("error")
            or lower:match("failed")
            or lower:match("rejected")
            or lower:match("refused")
            or lower:match("invalid")
            or lower:match("timed? ?out")
            or lower:match("unexpected eof")
            or lower:match("bodywriteaborted")
            or lower:match("noteof")
            or lower:match("broken pipe")
            or lower:match("connection reset")
            or lower:match("no space left")
        then
            table.insert(lines, line)
        end
    end
    return table.concat(lines, "\n")
end

function M.diagnoseTransfer(_, captured)
    captured = captured or M.captureTransferEvidence()
    local last_send = captured.last_send
    local recent_failed_send = last_send and not last_send.success and os.time() - (last_send.time or 0) <= 300
    local send_message = recent_failed_send and tostring(last_send.message or "") or ""
    local evidence = (send_message .. "\n" .. errorLines(captured.send_log) .. "\n" .. errorLines(captured.backend)):lower()

    if evidence:match("no space left") or evidence:match("read%-only") or evidence:match("file io") then
        return {
            id = "storage",
            title = deps._("The receiving device could not save the file"),
            text = deps._("Its storage may be full, unavailable, or read-only. Check free space or choose another receive folder."),
            action = "choose_folder",
            action_label = deps._("Choose folder"),
        }
    elseif evidence:match("all files rejected") or evidence:match("rejected by extension") or evidence:match("extension filter") then
        return {
            id = "file_rejected",
            title = deps._("The receiving device rejected this file"),
            text = deps._("Review Allowed extensions and File type routing on the receiving device."),
            action = "allowed_files",
            action_label = deps._("Review settings"),
        }
    elseif evidence:match("pin") or evidence:match("status code: 401") or evidence:match("too many failed attempts") then
        return {
            id = "pin",
            title = deps._("The PIN was not accepted"),
            text = deps._("Enter the PIN shown by the receiving device and try again."),
            action = "details",
            action_label = deps._("View details"),
        }
    elseif
        evidence:match("tls handshake")
        or evidence:match("x509")
        or evidence:match("certificate verification")
        or evidence:match("certificate[^\n]*error")
        or evidence:match("certificate[^\n]*failed")
        or evidence:match("certificate[^\n]*invalid")
        or evidence:match("certificate[^\n]*expired")
        or evidence:match("https[^\n]*error")
        or evidence:match("https[^\n]*failed")
    then
        return {
            id = "https",
            title = deps._("The secure connection failed"),
            text = deps._("The devices could not establish an HTTPS connection. You can temporarily test without HTTPS."),
            action = "disable_https",
            action_label = deps._("Try without HTTPS"),
        }
    elseif
        evidence:match("connection refused")
        or evidence:match("tcp connect")
        or evidence:match("not running localsend")
        or evidence:match("device is not running")
        or evidence:match("connection failed")
        or evidence:match("connection timed out")
    then
        return {
            id = "recipient_unavailable",
            title = deps._("The other device is not reachable"),
            text = deps._("Make sure LocalSend is open on the other device and both devices are connected to the same Wi-Fi network."),
            action = "discovery",
            action_label = deps._("Find devices"),
        }
    elseif
        evidence:match("bodywriteaborted")
        or evidence:match("noteof")
        or evidence:match("broken pipe")
        or evidence:match("connection reset")
        or evidence:match("unexpected eof")
    then
        return {
            id = "interrupted",
            title = deps._("The connection ended during the transfer"),
            text = deps._(
                "Keep both devices awake and connected to Wi-Fi, then retry. If it happens again, create a support report immediately afterward."
            ),
            action = "support_report",
            action_label = deps._("Create report"),
        }
    elseif evidence:match("checksum") then
        return {
            id = "checksum",
            title = deps._("The received file did not pass verification"),
            text = deps._("The transfer was incomplete or corrupted. Retry it while both devices have a stable Wi-Fi connection."),
            action = "support_report",
            action_label = deps._("Create report"),
        }
    elseif evidence:match("rejected") or evidence:match("status code: 400") or evidence:match("status code: 403") then
        return {
            id = "rejected",
            title = deps._("The receiving device rejected the transfer"),
            text = deps._(
                "The available logs do not identify a safe automatic fix. Create a support report immediately after reproducing the failure."
            ),
            action = "support_report",
            action_label = deps._("Create report"),
        }
    end

    return {
        id = "unknown",
        title = deps._("No recent transfer error was recognized"),
        text = deps._("Try the transfer once more, then return here immediately. A support report will include the newest LocalSend logs."),
        action = "support_report",
        action_label = deps._("Create report"),
    }
end

function M.showTransferCheck(instance)
    -- Capture the previous receiver/send logs before the lifecycle test stops
    -- and restores the normal receiver, which may rotate or truncate them.
    local captured = M.captureTransferEvidence()
    local progress = deps.InfoMessage:new({
        text = deps._("Testing the LocalSend receiver…"),
        dismissable = false,
    })
    deps.UIManager:show(progress)
    deps.UIManager:scheduleIn(0.1, function()
        M.collectAsync(instance, function(report)
            report.checks = checksFromReport(instance, report)
            M._last_report = report
            deps.UIManager:close(progress)

            local lifecycle_result = M.classifyReport(instance, report)
            if lifecycle_result.id ~= "healthy" then
                showResult(instance, lifecycle_result, deps._("Transfer failed"))
                return
            end
            showResult(instance, M.diagnoseTransfer(instance, captured), deps._("Transfer failed"))
        end)
    end)
end

function M.showDiscoveryHelp(instance)
    local ConfirmBox = require("ui/widget/confirmbox")
    deps.UIManager:show(ConfirmBox:new({
        text = deps._(
            "Open LocalSend on your phone or computer and keep it visible during the test. "
                .. "Both devices should be connected to the same Wi-Fi network.\n\n"
                .. "The LocalSend receiver may restart briefly."
        ),
        ok_text = deps._("Start test"),
        ok_callback = function()
            M.showDiscoveryTest(instance)
        end,
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
    if (r.peers or 0) > 0 and not r.loopback then
        table.insert(lines, "Multicast reception: OK (another device responded)")
        table.insert(lines, "Self-loopback: inconclusive")
    else
        table.insert(lines, "Multicast self-test: " .. (r.loopback and "OK" or "FAILED"))
    end
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

local function classifyDiscoveryResult(r)
    r = r or {}
    if r.bind_error and r.bind_error ~= "" then
        return {
            title = deps._("LocalSend could not use its discovery port"),
            text = deps._("Another process may be using the LocalSend network port. Restart KOReader and try again."),
        }
    elseif (r.peers or 0) > 0 then
        local aliases = type(r.seen_aliases) == "table" and #r.seen_aliases > 0 and table.concat(r.seen_aliases, ", ") or nil
        return {
            title = deps._("Another LocalSend device was found"),
            text = aliases and deps.T(deps._("Device discovery is working. Found: %1"), aliases)
                or deps._("Device discovery is working on this network."),
        }
    elseif not r.loopback then
        return {
            title = deps._("This network is blocking device discovery"),
            text = deps._("Try a non-guest Wi-Fi network and disable AP or client isolation in the router settings."),
        }
    end
    return {
        title = deps._("No other LocalSend device answered"),
        text = deps._(
            "This device's network test passed. Keep LocalSend open on the other device and check its firewall or Local Network permission."
        ),
    }
end

local function showDiscoveryResult(instance, result)
    local TextViewer = require("ui/widget/textviewer")
    local dialog
    dialog = TextViewer:new({
        title = deps._("Device discovery"),
        text = result.title .. "\n\n" .. result.text,
        show_menu = false,
        buttons_table = {
            {
                {
                    text = deps._("Technical details"),
                    callback = function()
                        deps.UIManager:close(dialog)
                        local details = TextViewer:new({
                            title = deps._("Discovery details"),
                            text = M._last_discovery_text or deps._("No discovery details are available."),
                            text_type = "code",
                        })
                        deps.UIManager:show(details)
                    end,
                },
                {
                    text = deps._("Close"),
                    callback = function()
                        deps.UIManager:close(dialog)
                    end,
                },
            },
        },
    })
    deps.UIManager:show(dialog)
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

local function finishDiscoveryTest(instance, restart_after)
    instance:closeFirewall()
    if restart_after then
        instance:start(true)
    end
end

function M._pollDiscoveryTest(instance, attempts, deadline, restart_after)
    local result = readNetTestResult()
    if result then
        result.recorded_at = os.date("%Y-%m-%dT%H:%M:%S%z")
        M._last_discovery_result = result
        os.remove(constants.NETTEST_OUTPUT_FILE)
        os.remove(constants.NETTEST_PID_FILE)
        local text = M.formatDiscoveryResult(instance, result)
        M._last_discovery_text = text
        finishDiscoveryTest(instance, restart_after)
        showDiscoveryResult(instance, classifyDiscoveryResult(result))
        return
    end
    if os.time() > deadline or attempts > 30 then
        killStaleNetTest()
        os.remove(constants.NETTEST_OUTPUT_FILE)
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("The discovery test timed out. Use Check LocalSend, then try again."),
            timeout = 4,
        }))
        finishDiscoveryTest(instance, restart_after)
        return
    end
    deps.UIManager:scheduleIn(0.5, function()
        M._pollDiscoveryTest(instance, attempts + 1, deadline, restart_after)
    end)
end

function M._launchNetTest(instance, restart_after)
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
    M._pollDiscoveryTest(instance, 0, os.time() + constants.NETTEST_DURATION + 5, restart_after)
end

function M.showDiscoveryTest(instance)
    if not deps.util.pathExists(paths.binary_path or "") then
        deps.UIManager:show(deps.InfoMessage:new({
            icon = "notice-warning",
            text = deps._("The localsend binary is missing. Reinstall the plugin first."),
        }))
        return
    end
    -- nettest needs the receiver's port. If the normal server is active, stop it
    -- briefly and restore it after the result or timeout.
    if instance.isRunning and instance:isRunning() then
        local progress = deps.InfoMessage:new({
            text = deps._("Restarting LocalSend briefly for the discovery test…"),
            dismissable = false,
        })
        deps.UIManager:show(progress)
        instance:stopServer({
            callback = function(success)
                deps.UIManager:close(progress)
                if success then
                    M._launchNetTest(instance, true)
                else
                    deps.UIManager:show(deps.InfoMessage:new({
                        icon = "notice-warning",
                        text = deps._("LocalSend could not be stopped for the discovery test."),
                    }))
                end
            end,
        })
    else
        M._launchNetTest(instance, false)
    end
end

return M
