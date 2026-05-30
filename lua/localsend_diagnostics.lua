-- localsend_diagnostics.lua
-- Troubleshooting and diagnostics helpers for LocalSend plugin

local constants = require("localsend_constants")

local M = {}

-- Dependencies container (set via M.init)
local deps = {}
local paths = {}

local REPORT_TAIL_BYTES = 12 * 1024

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

local function readPID()
    local content = readFirstLine(constants.PID_FILE)
    if not content then
        return nil
    end
    return tonumber(content:match("^(%d+)"))
end

local function processInfo()
    local pid = readPID()
    if not pid then
        return {
            pid = nil,
            pid_file_exists = deps.util.pathExists(constants.PID_FILE),
            alive = false,
            cmdline = nil,
            is_localsend_recv = false,
        }
    end

    local proc_path = "/proc/" .. tostring(pid)
    local cmdline = readFile(proc_path .. "/cmdline")
    if cmdline then
        cmdline = cmdline:gsub("%z", " "):gsub("%s+$", "")
    end

    return {
        pid = pid,
        pid_file_exists = true,
        alive = deps.util.pathExists(proc_path),
        cmdline = cmdline,
        is_localsend_recv = cmdline ~= nil and cmdline:match("localsend") ~= nil and cmdline:match("recv") ~= nil,
    }
end

local function localServerProbe(instance, proc)
    if not proc.alive or not proc.is_localsend_recv then
        return "skipped (server is not running)"
    end

    local scheme = instance.use_https and "https" or "http"
    local url = scheme .. "://127.0.0.1:" .. tostring(instance.port) .. "/api/localsend/v1/info"
    local args = {"curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "--connect-timeout", "2", "--max-time", "3"}
    if instance.use_https then
        table.insert(args, "-k")
    end
    table.insert(args, url)

    local output = commandOutput(args)
    if output == nil or output == "" then
        return "failed (no response)"
    end
    return "HTTP " .. output
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

local function firewallStatus(instance)
    if not deps.Device or not deps.Device.isKindle or not deps.Device:isKindle() then
        return "not applicable (non-Kindle)"
    end

    local port = tostring(instance.port)
    local tcp = commandOutput({"iptables", "-C", "INPUT", "-p", "tcp", "--dport", port,
        "-m", "conntrack", "--ctstate", "NEW,ESTABLISHED", "-j", "ACCEPT"})
    local udp = commandOutput({"iptables", "-C", "INPUT", "-p", "udp", "--dport", port, "-j", "ACCEPT"})

    local parts = {
        "tcp/" .. port .. ": " .. (tcp == "" and "open" or "missing"),
        "udp/" .. port .. ": " .. (udp == "" and "open" or "missing"),
    }

    if instance.use_webrtc then
        local webrtc = commandOutput({
            "iptables", "-C", "INPUT", "-p", "udp", "--dport", constants.WEBRTC_PORT_RANGE, "-j", "ACCEPT",
        })
        local webrtc_status = webrtc == "" and "open" or "missing"
        table.insert(parts, "udp/" .. constants.WEBRTC_PORT_RANGE .. ": " .. webrtc_status)
    end

    return table.concat(parts, ", ")
end

function M.collect(instance)
    local proc = processInfo()
    local binary_exists = deps.util.pathExists(paths.binary_path or "")
    local binary_version = binary_exists and commandOutput({paths.binary_path, "--version"}) or nil
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
        binary_exists = binary_exists,
        binary_version = binary_version,
        network_connected = deps.NetworkMgr and deps.NetworkMgr:isConnected() or false,
        network_online = deps.NetworkMgr and deps.NetworkMgr:isOnline() or false,
        network_info = network_info,
        settings = {
            port = instance.port,
            save_dir = instance.save_dir,
            save_dir_status = saveDirStatus(instance),
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
            pid = proc.pid,
            pid_file_exists = proc.pid_file_exists,
            alive = proc.alive,
            is_localsend_recv = proc.is_localsend_recv,
            cmdline = proc.cmdline,
            local_probe = localServerProbe(instance, proc),
            backend_log_path = constants.SERVER_OUTPUT_FILE,
            transfer_log_path = constants.TRANSFER_LOG_FILE,
        },
        firewall = firewallStatus(instance),
        logs = {
            backend = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES),
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

    addSection(lines, "Plugin")
    table.insert(lines, "Version: " .. tostring(report.plugin_version))
    table.insert(lines, "Architecture: " .. tostring(report.arch))
    table.insert(lines, "Plugin path: " .. tostring(report.plugin_path))
    table.insert(lines, "Binary path: " .. tostring(report.binary_path))
    table.insert(lines, statusLabel(report.binary_exists, "Binary exists"))
    table.insert(lines, "Binary version: " .. tostring(report.binary_version or "unknown"))
    table.insert(lines, "Device: " .. tostring(report.device_info))

    addSection(lines, "Network")
    table.insert(lines, statusLabel(report.network_connected, "LAN connected"))
    table.insert(lines, statusLabel(report.network_online, "Internet reachable"))
    table.insert(lines, "Network info:")
    table.insert(lines, tostring(report.network_info or "unavailable"))

    addSection(lines, "Settings")
    table.insert(lines, "Device name: " .. tostring(report.settings.device_name))
    table.insert(lines, "Port: " .. tostring(report.settings.port))
    table.insert(lines, "Save directory: " .. tostring(report.settings.save_dir))
    table.insert(lines, "Save directory status: " .. tostring(report.settings.save_dir_status))
    table.insert(lines, "HTTPS: " .. boolLabel(report.settings.https))
    table.insert(lines, "WebRTC: " .. boolLabel(report.settings.webrtc))
    table.insert(lines, "PIN enabled: " .. boolLabel(report.settings.pin))
    table.insert(lines, "Allowed extensions: " .. tostring(report.settings.accept_ext))
    table.insert(lines, "Routing enabled: " .. boolLabel(report.settings.routing_enabled))
    table.insert(lines, "Routing accept other files: " .. boolLabel(report.settings.routing_accept_all))
    table.insert(lines, "Routing rules: " .. formatRoutes(report.settings.routing_rules))
    table.insert(lines, "Autostart: " .. boolLabel(report.settings.autostart))

    addSection(lines, "Server lifecycle")
    table.insert(lines, "PID file: " .. (report.server.pid_file_exists and "present" or "missing"))
    table.insert(lines, "PID: " .. tostring(report.server.pid or "none"))
    table.insert(lines, statusLabel(report.server.alive, "Process alive"))
    table.insert(lines, statusLabel(report.server.is_localsend_recv, "Process is LocalSend recv"))
    table.insert(lines, "Command line: " .. tostring(report.server.cmdline or "unknown"))
    table.insert(lines, "Local API probe: " .. tostring(report.server.local_probe))
    table.insert(lines, "Backend log: " .. tostring(report.server.backend_log_path))
    table.insert(lines, "Transfer log: " .. tostring(report.server.transfer_log_path))

    addSection(lines, "Firewall")
    table.insert(lines, tostring(report.firewall))

    addSection(lines, "Recent backend log")
    table.insert(lines, report.logs.backend or "No backend log found.")

    addSection(lines, "Recent send log")
    table.insert(lines, report.logs.send or "No send log found.")

    addSection(lines, "Recent transfer log")
    table.insert(lines, report.logs.transfers or "No transfer log found.")

    addSection(lines, "Bug report")
    table.insert(lines, "Attach this report and KOReader crash.log if you open a GitHub issue.")
    table.insert(lines, "crash.log: " .. tostring(report.logs.crash_log_path))

    return table.concat(lines, "\n")
end

function M.getReportText(instance)
    return M.formatReport(M.collect(instance))
end

function M.showDiagnostics(instance)
    local TextViewer = require("ui/widget/textviewer")
    deps.UIManager:show(TextViewer:new{
        title = deps._("LocalSend diagnostics"),
        text = M.getReportText(instance),
    })
end

function M.showNetworkInfo()
    local text
    if deps.Device and deps.Device.retrieveNetworkInfo then
        text = deps.Device:retrieveNetworkInfo()
    else
        text = deps._("Could not retrieve network info.")
    end
    deps.UIManager:show(deps.InfoMessage:new{
        text = text,
    })
end

function M.showServerStatus(instance)
    local report = M.collect(instance)
    local lines = {
        deps._("LocalSend server status"),
        "",
        "PID file: " .. (report.server.pid_file_exists and "present" or "missing"),
        "PID: " .. tostring(report.server.pid or "none"),
        "Process alive: " .. boolLabel(report.server.alive),
        "LocalSend recv: " .. boolLabel(report.server.is_localsend_recv),
        "Local API probe: " .. tostring(report.server.local_probe),
        "Backend log: " .. tostring(report.server.backend_log_path),
    }
    deps.UIManager:show(deps.InfoMessage:new{
        text = table.concat(lines, "\n"),
    })
end

function M.showRecentBackendLog()
    local TextViewer = require("ui/widget/textviewer")
    deps.UIManager:show(TextViewer:new{
        title = deps._("LocalSend backend log"),
        text = readTail(constants.SERVER_OUTPUT_FILE, REPORT_TAIL_BYTES) or deps._("No backend log found."),
    })
end

function M.showBugReport(instance)
    local TextViewer = require("ui/widget/textviewer")
    local report = M.getReportText(instance)
    local checklist = deps._("When reporting a LocalSend issue, please include:\n\n" ..
        "• Device model and firmware\n" ..
        "• KOReader version\n" ..
        "• LocalSend plugin version\n" ..
        "• Sender app/version\n" ..
        "• Steps to reproduce\n" ..
        "• KOReader crash.log\n" ..
        "• The diagnostics report below\n\n%1")
    deps.UIManager:show(TextViewer:new{
        title = deps._("LocalSend bug report"),
        text = deps.T(checklist, report),
    })
end

return M
