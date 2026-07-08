# Troubleshooting

This guide helps you diagnose and fix common LocalSend for KOReader problems.
The fastest path is the in-plugin diagnostics described below; if you still need
help, the same menu formats the information you should attach to a bug report.

## In-plugin diagnostics

Stop the LocalSend server first, then open **Menu → Network → LocalSend → Settings → Troubleshooting** for:

- **Run diagnostics** — runs pass/fail checks for network, binary, temporary
  server self-test, and plugin-managed firewall rules, then shows detailed
  plugin, device, settings, and log information. If `iptables` is available,
  diagnostics actively opens, verifies, and closes the LocalSend firewall rules;
  this is not limited to Kindle.
- **Test discovery** — checks whether this device can send and receive LocalSend
  multicast discovery packets and whether other LocalSend devices respond, to
  attribute a "device not discovered" problem to this device/network vs. the
  other device. The normal server is stopped before entering troubleshooting, so
  the test can own the LocalSend port without stopping/restarting anything.
- **Show network info** — displays KOReader's current network information,
  including IP addresses when available.
- **Show recent LocalSend log** — displays the receiver backend log captured at
  `/tmp/localsend_server.out`.
- **Prepare bug report** — formats the diagnostics details with a checklist of
  information to include in GitHub issues.

The **Common fixes** submenu lets you restart the server, rotate certificates,
toggle HTTPS/WebRTC, and reinstall the plugin without leaving the menu.

## Common symptoms

### Connection refused

The device is reachable, but the LocalSend receiver is not listening.

1. Stop the server, then open **Settings → Troubleshooting → Run diagnostics**.
   The diagnostics self-test starts a temporary receiver, probes the local API,
   and stops it again.
2. If the server self-test fails, check **Show recent LocalSend log** and the
   diagnostic server self-test log in the report.
3. On older Kindles, verify that you installed the `arm-legacy` package — the
   `armv7` binary will not run on 32-bit-only hardware.

### Device not discovered

- Make sure both devices are on the **same LAN/subnet** (not a guest network or
  VLAN).
- Disable **Wi-Fi isolation** (a.k.a. AP/client isolation) on your router.
- Check the firewall on your **phone/computer** as well — it must allow LocalSend
  on port 53317. See [Network & firewall](#network--firewall) below.
- On devices with `iptables`, confirm the plugin can open the LocalSend firewall
  rules. The diagnostics report actively tests open/verify/close for those rules.
- The device must be reachable on port **53317** (see _Connection refused_
  above).

### HTTP 400/403 during receive

- Check your **PIN** code, **allowed extensions**, and **file type routing**.
- Archives such as `.zip`, `.rar`, and `.7z` are received as files; the plugin
  does not extract them.

### HTTPS connection problems

Temporarily disable **Use HTTPS** from **Settings** or
**Troubleshooting → Common fixes**, then try again.

### WebRTC / signaling problems

WebRTC requires Internet access; LAN send/receive only requires local
connectivity. Temporarily disable **Enable WebRTC Support** if startup or
discovery is unreliable.

## Network & firewall

LocalSend communicates over port **53317**. Both devices must be able to reach
each other:

| Direction | Protocol | Port  | Purpose                        |
|-----------|----------|-------|--------------------------------|
| Incoming  | TCP      | 53317 | File transfer (HTTP/HTTPS API) |
| Incoming  | UDP      | 53317 | Multicast device discovery     |
| Outgoing  | TCP/UDP  | any   | Replies, discovery, WebRTC     |

On the e-reader, the receiver binds these ports automatically. If `iptables` is
available, the plugin also opens the LocalSend firewall rules on start. The most
common remaining blocker is therefore the firewall on your **phone or computer**.

### Computer / phone firewall (the other device)

Allow **incoming TCP and UDP on port 53317** for LocalSend:

- **Windows** — set the network to **Private** (Settings → Network → Properties);
  Windows Firewall is more restrictive on Public networks. If needed, add an
  inbound rule for TCP and UDP 53317.
- **macOS / iOS** — grant the **Local Network** permission (System Settings →
  Privacy & Security → Local Network) to LocalSend.
- **Linux (ufw)** — `sudo ufw allow 53317/tcp` and `sudo ufw allow 53317/udp`
- **Linux (firewalld)** — `sudo firewall-cmd --permanent --add-port=53317/tcp`,
  `--add-port=53317/udp`, then `sudo firewall-cmd --reload`

### Router settings

- Disable **AP isolation / client isolation** (frequently on by default for
  guest networks).
- Keep both devices on the same subnet/VLAN.

## Reporting a bug

When reporting a bug, include:

- the **bug report** (**Troubleshooting → Prepare bug report**) — it already
  contains the diagnostics report and the tail of KOReader's `crash.log`, and
- the full `crash.log` if the error is not in the included tail —
  `/mnt/us/koreader/crash.log` on Kindle, `.adds/koreader/crash.log` on Kobo.

Both **Run diagnostics** and **Prepare bug report** also save their output as a
text file under the KOReader data directory (`cache/localsend/localsend-report.txt`
and `cache/localsend/localsend-bugreport.txt`; the exact path is shown at the
bottom of the report). Copy the file off the device over USB instead of
transcribing from the screen.

If a log is long, paste the last ~100 lines around the error.

**Privacy:** review the report before posting it publicly. The PIN is redacted
automatically, but the report can still include your device name, file names,
and local network addresses.
