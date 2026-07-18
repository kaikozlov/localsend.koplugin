# Troubleshooting

This guide helps you diagnose and fix common LocalSend for KOReader problems.
The fastest path is the in-plugin diagnostics described below; if you still need
help, the same menu formats the information you should attach to a bug report.

## In-plugin diagnostics

Open **Menu → Network → LocalSend → Troubleshooting**. It remains available while
the LocalSend receiver is running and provides these guided paths:

- **Check LocalSend** — checks Wi-Fi, the installed receiver program, receive
  folder, server lifecycle, and firewall. If the normal receiver is running, the
  check stops it, starts and probes the receiver through the plugin's real
  lifecycle, then restores its original running/stopped state. The readiness
  probe prefers a local HTTP(S) API response; on older devices whose system curl
  cannot complete the HTTPS handshake, it falls back to confirming the receiver
  process is running and listening on TCP. It shows one plain-language conclusion and a
  relevant action. The full report remains available through **Technical details**.
- **Can't find a device?** — explains how to prepare the other device, then tests
  LocalSend discovery. If necessary, the receiver is restarted briefly and
  restored automatically after the test.
- **Transfer failed?** — runs the same active receiver lifecycle test first, then
  uses recent LocalSend errors as supporting evidence for connection, storage,
  PIN, HTTPS, file-filter, checksum, and interrupted-transfer failures.
- **Create support report** — combines diagnostics and the tail of KOReader's
  `crash.log`, then saves `localsend-bugreport.txt` in the configured receive
  folder so it can be retrieved over USB.
- **Advanced** — exposes network details, raw backend and discovery information,
  the complete diagnostic report, and a manual server restart.

## Common symptoms

### Connection refused

The device is reachable, but the LocalSend receiver is not listening.

1. Open **Troubleshooting → Check LocalSend**. The check temporarily stops a
   running receiver when necessary, exercises the complete diagnostic lifecycle,
   and restores the normal receiver afterward.
2. If the receiver check fails, use its **View log** action or open **Advanced →
   Backend log**.
3. On older Kindles, verify that you installed the package matching the device
   architecture. `arm-legacy` is for older ARM instruction sets; it does not
   reduce the Linux syscall requirements of the Go runtime.
4. If the backend log reports `epollwait` failure 38 (`ENOSYS`) or another
   missing syscall, see [Legacy Linux kernel compatibility](LEGACY_KERNEL_COMPATIBILITY.md).

### Device not discovered

- Make sure both devices are on the **same LAN/subnet** (not a guest network or
  VLAN).
- Disable **Wi-Fi isolation** (a.k.a. AP/client isolation) on your router.
- Check the firewall on your **phone/computer** as well — it must allow LocalSend
  on port 53317. See [Network & firewall](#network--firewall) below.
- On devices with `iptables`, confirm the plugin installed the LocalSend firewall
  rules. Diagnostics inspect the rules created by the real receiver start without
  closing them underneath a running receiver.
- The device must be reachable on port **53317** (see _Connection refused_
  above).

### HTTP 400/403 during receive

- Check your **PIN** code, **allowed extensions**, and **file type routing**.
- Archives such as `.zip`, `.rar`, and `.7z` are received as files; the plugin
  does not extract them.

### HTTPS connection problems

Use **Troubleshooting → Transfer failed?**. If the recorded error points to TLS or
HTTPS, the result offers **Try without HTTPS** with a confirmation.

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

- the **support report** (**Troubleshooting → Create support report**) — it already
  contains the diagnostics report and the tail of KOReader's `crash.log`, and
- the full `crash.log` if the error is not in the included tail —
  `/mnt/us/koreader/crash.log` on Kindle, `.adds/koreader/crash.log` on Kobo.

**Check LocalSend** saves its technical report under the KOReader data directory
at `cache/localsend/localsend-report.txt`. **Create support report** saves the
complete `localsend-bugreport.txt` in the configured LocalSend receive folder;
the exact path is shown at the bottom. Copy that file off the device over USB
instead of transcribing from the screen.

If a log is long, paste the last ~100 lines around the error.

**Privacy:** review the report before posting it publicly. The PIN is redacted
automatically, as are Wi-Fi SSIDs and MAC addresses. The report can still include
device aliases, file names, routing paths, and private network addresses.

The report snapshots failure evidence before troubleshooting restarts anything.
It separates the pre-check backend log from the lifecycle-test log and includes
the latest send result, receiver/power/network event history, listener and LAN
probe status, the latest discovery test, KOReader/device/runtime information, and
the tail of `crash.log`. Fill in the reproduction fields at the top before posting.
