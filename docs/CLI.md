# LocalSend CLI

The LocalSend backend can be used as a standalone command-line tool without KOReader.

## Building

```bash
go build -o localsend
```

### Cross-compiling for ARM devices

```bash
# Full build (compile Go + package into release zips)
just release

# Package only (skip Go compilation, reuse existing binaries)
just release -p
```

`just release` applies the 32-bit ARM runtime compatibility overlay required
by older Kindle Linux 2.6.31 kernels. The manual command below is suitable only
for devices with a modern Linux kernel. See
[Legacy Linux kernel compatibility](LEGACY_KERNEL_COMPATIBILITY.md) for the
support boundary, known syscall risks, and release verification requirements.

Or build manually:

```bash
# armv7 (32-bit, for modern Linux kernels)
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags="-s -w" -o localsend

# arm64 (64-bit)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o localsend
```

## Version

```bash
./localsend --version
```

Prints `<version> <goos>/<arch>` (for example `vX.Y.Z linux/arm64`). The
architecture is injected at build time and matches the asset names (`arm64`,
`armv7`, `arm-legacy`); the KOReader plugin compares it against the device's
architecture to flag a mismatched package.

## Scanning for Devices

```bash
# Scan for LocalSend devices on the network
./localsend scan

# Scan with JSON output (for scripting)
./localsend scan --json

# Scan only LAN devices
./localsend scan --lan

# Scan with custom timeout
./localsend scan -t 10
```

## Diagnosing discovery (nettest)

`nettest` checks whether this device can send and receive LocalSend multicast
discovery packets, and whether other LocalSend devices are on the LAN. It
announces itself to the multicast group and counts peers that respond either
with a UDP announcement or with an HTTP register call to the discovery port
(the official app's primary response path). The KOReader plugin's "Test
discovery" troubleshooting action uses it to attribute a "device not
discovered" problem to this device/network vs. the other device(s).

```bash
# Human-readable output
./localsend nettest

# JSON output (consumed by the plugin)
./localsend nettest --json

# Run for 5 seconds
./localsend nettest -d 5
```

JSON result fields:

| Field                 | Description                                                    |
|-----------------------|----------------------------------------------------------------|
| `loopback`            | `true` if this device received its own multicast probe        |
| `bind_error`          | non-empty if the UDP discovery port could not be bound         |
| `peers`               | distinct other LocalSend devices seen (union of both paths)    |
| `udp_peers`           | peers seen via UDP announcements                               |
| `register_peers`      | peers seen via HTTP register calls                             |
| `register_bind_error` | non-empty if the TCP register listener could not bind          |
| `seen_aliases`        | device names (aliases) of responding peers, capped at 10       |
| `local_ips`           | this device's IPv4 addresses                                   |
| `duration_ms`         | how long the test ran                                          |

## Receiving Files

```bash
# Basic receive mode
./localsend recv -d ~/Downloads

# With device name and PIN
./localsend recv -d ~/Downloads -n "My Server" -p 1234

# Filter by file type
./localsend recv -d ~/Downloads -a epub,pdf,mobi

# With extension routing
./localsend recv -d ~/Downloads --ext-routing routing.json
```

## Sending Files

```bash
# Send to a LAN device by IP
./localsend send --ip 192.168.1.50 myfile.epub

# Send via WebRTC (use ID from scan --json)
./localsend send -w --target <peer-uuid> myfile.epub

# Send with PIN
./localsend send --ip 192.168.1.50 -p 1234 myfile.epub

# Send a directory (preserves structure)
./localsend send --ip 192.168.1.50 ./my-folder/

# Send with custom device name
./localsend send --ip 192.168.1.50 -n "My Computer" myfile.epub
```

## CLI Flags

### recv command

| Flag | Description |
| ---- | ----------- |
| `-d, --dir` | Save directory for received files |
| `-n, --devname` | Device name advertised on the network |
| `-p, --pin` | PIN code required for transfers |
| `-a, --accept-ext` | Comma-separated list of allowed extensions |
| `--ext-routing` | Path to extension routing config (JSON) |
| `--https` | Enable HTTPS (default: true) |
| `-w, --webrtc` | Enable WebRTC/v3 protocol (default: true) |
| `-l, --log` | Path to transfer log file (JSON lines format) |
| `--on-transfer` | Shell command to run after each transfer |
| `--config-dir` | Config directory for trusted devices |
| `--require-pairing` | Require PAIR before accepting WebRTC transfers |
| `--stun-servers` | Custom STUN servers for WebRTC |
| `--signaling-id-file` | Write WebRTC signaling ID to file |

### send command

| Flag | Description |
| ---- | ----------- |
| `--ip` | Target device IP address |
| `-f, --file` | File or directory to send |
| `-p, --pin` | PIN code for authentication |
| `-n, --devname` | Device name shown to receiver |
| `--https` | Use HTTPS (default: true) |
| `-w, --webrtc` | Send via WebRTC signaling server |
| `-t, --target` | Target peer ID (required for WebRTC) |
| `--preserve-structure` | Keep subdirectory structure (default: true) |
| `--config-dir` | Config directory for trusted devices |
| `--stun-servers` | Custom STUN servers for WebRTC |
| `--dapi` | Use Download API (reverse transfer) |

### scan command

| Flag | Description |
| ---- | ----------- |
| `-t, --timeout` | Scan duration in seconds (default: 4) |
| `-n, --lan` | Enable LAN discovery (mDNS/UDP) |
| `-l, --legacy` | Enable legacy HTTP subnet scan |
| `-w, --webrtc` | Enable WebRTC signaling discovery |
| `-j, --json` | Output results as JSON |
| `-e, --exclude-id-file` | File with signaling ID to exclude |
| `--devname` | Device name shown to other peers |

## Trusted Devices (PAIR)

LocalSend supports device pairing to skip PIN verification for trusted devices.

```bash
# Enable trusted devices with config directory
./localsend recv -d ~/Downloads --config-dir ~/.config/localsend

# Require pairing for all WebRTC transfers
./localsend recv -d ~/Downloads --config-dir ~/.config/localsend --require-pairing
```

When `--config-dir` is set:
- Paired devices are stored in `trusted_devices.json`
- Maximum 100 devices (oldest evicted when full)
- Trusted devices skip PIN verification automatically

## Custom STUN Servers

For corporate networks or privacy-conscious users:

```bash
./localsend recv -d ~/Downloads --stun-servers stun:stun.example.com:3478

./localsend send -w --target <id> --stun-servers stun:stun.example.com:3478 myfile.epub
```

Default: Google STUN servers (`stun:stun.l.google.com:19302`)

## Extension Routing

Extension routing lets you save different file types to different directories. Create a JSON file with extension-to-directory mappings:

```json
{
  "epub": "/home/user/Books",
  "pdf": "/home/user/Documents",
  "mobi": "/home/user/Books",
  "cbz": "/home/user/Comics",
  "default": "/home/user/Downloads"
}
```

**Format:**
- Keys are lowercase file extensions (without the dot)
- Values are absolute directory paths
- The special `"default"` key specifies where unrouted files go
- If `"default"` is omitted, unrouted files are rejected

**Usage:**
```bash
./localsend recv -d ~/Downloads --ext-routing ~/routing.json
```

### Example Configurations

**E-reader focused (strict - only accept specific types):**
```json
{
  "epub": "/mnt/us/documents/Books",
  "pdf": "/mnt/us/documents/PDFs",
  "mobi": "/mnt/us/documents/Books",
  "azw3": "/mnt/us/documents/Books"
}
```

**General purpose (accept all, route specific types):**
```json
{
  "epub": "/home/user/Books",
  "pdf": "/home/user/Documents",
  "mp3": "/home/user/Music",
  "default": "/home/user/Downloads"
}
```
