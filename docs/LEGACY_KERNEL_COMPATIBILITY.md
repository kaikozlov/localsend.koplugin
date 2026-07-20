# Legacy Linux kernel compatibility

LocalSend supports older e-readers on a best-effort basis. This document records the compatibility boundary for Kindle vendor kernels so release-build changes do not accidentally reintroduce failures such as [issue #15](https://github.com/kaikozlov/localsend.koplugin/issues/15).

## Verified devices and firmware

Two Kindle vendor-kernel boundaries have been audited:

| Update | Device | Kernel | ARM syscall table |
|---|---|---|---|
| `update_kindle_5.6.1.1.bin` | Original Paperwhite (`yoshime3`) | `2.6.31-rt11-lab126` | 368 entries (0–367) |
| `Update_kindle_2.5.8_B009.bin` | Kindle DX Graphite | `2.6.22.19-lab126` | 352 entries (0–351) |

The diagnostics report for issue #15 identifies KOReader's `KindlePaperWhite` model, and the extracted firmware identifies the platform as `yoshime3`, the original Paperwhite (PW/PW1):

```text
System Software Version: 035-juno_6011_celeste_yoshime3-268989
Linux version 2.6.31-rt11-lab126
```

The firmware update and its extracted contents are not committed to this repository. The reproduction commands below refer to a working directory of your choice as `$FW_DIR`, which holds this layout:

```text
$FW_DIR/
├── update_kindle_5.6.1.1.bin
└── kindle-firmware-5.6.1.1/
    ├── imx50_yoshime/
    │   ├── uImage
    │   ├── uImage.sig
    │   ├── vmlinux       # zero-byte placeholder in this package
    │   └── zImage        # kernel image containing kallsyms
    └── rootfs/
```

The source updates have these identities:

```text
Kindle Paperwhite 5.6.1.1:
SHA-256: 6be62d30df26894b08083dbc55d23774756710c3a44c9a1d0075dd02d12090c9
Size:    217523739 bytes

Kindle DX Graphite 2.5.8:
SHA-256: 902cc39cee4d663c1ddd4301b07ed084aad8fa35fb752a278b7ea2a5c3088887
Size:    5560209 bytes
```

The DXG package is an OTA V1 update containing incremental rootfs patches and
intermediate/final kernels, not a complete root filesystem. KindleTool reports
target device `Kindle DX Graphite`. The final `555370010-kernel.tar.gz` contains
an uncompressed U-Boot `uImage`; its compressed ARM payload reports:

```text
Linux version 2.6.22.19-lab126 (build@defiant) (gcc version 4.1.2) #3 PREEMPT Thu Jan 13 18:13:20 PST 2011
```

## Reproducing the firmware audit

Set `FW_DIR` to the working directory where you will download and extract the firmware. It is not part of this repository, so choose any local path:

```bash
export FW_DIR=/path/to/firmware/audit
mkdir -p "$FW_DIR"
```

### 1. Verify and unpack the update

Use [KindleTool](https://github.com/NiLuJe/KindleTool) to extract the Amazon update directly into a directory. Build or install its `kindletool` executable per its repository, then verify and unpack the update into `$FW_DIR`:

```bash
sha256sum "$FW_DIR/update_kindle_5.6.1.1.bin"

kindletool extract \
    "$FW_DIR/update_kindle_5.6.1.1.bin" \
    "$FW_DIR/kindle-firmware-5.6.1.1"
```

Do not infer the model from the issue title. Inspect both the extracted root filesystem and the U-Boot image metadata:

```bash
head -n 1 \
    "$FW_DIR/kindle-firmware-5.6.1.1/rootfs/etc/version.txt"

file \
    "$FW_DIR/kindle-firmware-5.6.1.1/imx50_yoshime/uImage"

strings \
    "$FW_DIR/kindle-firmware-5.6.1.1/imx50_yoshime/zImage" \
    | grep -m1 'Linux version'
```

The extracted root filesystem also establishes the userspace baseline:

```bash
file "$FW_DIR/kindle-firmware-5.6.1.1/rootfs/lib/libc-2.12.1.so"
strings "$FW_DIR/kindle-firmware-5.6.1.1/rootfs/bin/busybox" \
    | grep -m1 'BusyBox v'
strings "$FW_DIR/kindle-firmware-5.6.1.1/rootfs/usr/bin/curl" \
    | grep -m1 'curl '
```

This firmware contains EGLIBC 2.12.1, BusyBox 1.17.1, and curl 7.33.0. A libc export such as `accept4` or `epoll_pwait` is not proof that the kernel implements the syscall: libc may provide a wrapper that receives `ENOSYS` from the kernel.

That curl/OpenSSL build also cannot complete a TLS handshake with the receiver's Ed25519 certificate. Phone and laptop LocalSend clients can, so transfers may succeed while an HTTPS `curl` self-check fails. **Check LocalSend** therefore falls back to process and TCP listener evidence when the local API probe cannot get an HTTP status.

### 2. Recover the kernel symbols

The extracted `vmlinux` is empty, but `zImage` is an uncompressed ARM kernel image with a compressed `kallsyms` table. The table was recovered with `vmlinux-to-elf` commit `19683fb95b29cd31362d49e6f48ab8368f96cbdf`:

```bash
rm -rf /tmp/vmlinux-to-elf /tmp/vte-venv
gh repo clone marin-m/vmlinux-to-elf /tmp/vmlinux-to-elf -- --depth=1
python3 -m venv /tmp/vte-venv
/tmp/vte-venv/bin/pip install -e /tmp/vmlinux-to-elf

/tmp/vte-venv/bin/kallsyms-finder \
    "$FW_DIR/kindle-firmware-5.6.1.1/imx50_yoshime/zImage" \
    > /tmp/kindle-kallsyms.txt
```

Relevant recovered addresses are:

```text
c0008000 T _stext
c0122044 T sys_call_table
c0122604 t sys_syscall
c015e73c T sys_ni_syscall
c01ca1a4 T sys_epoll_wait
c02f216c T sys_accept
```

`sys_syscall` immediately follows the table, so the table contains `(0xc0122604 - 0xc0122044) / 4 = 368` 32-bit entries, numbered 0 through 367.

Running the same pinned recovery process on the DXG final kernel yields:

```text
c0008000 T _stext
c00a5044 T sys_call_table
c00a55c4 t sys_syscall
c00dba64 T sys_ni_syscall
c0140a28 T sys_epoll_wait
c0140fa8 T sys_epoll_create
c01421a4 T sys_eventfd
c021aa34 T sys_accept
```

Its syscall dispatcher compares the ARM syscall number against 352 before
loading from the table and branches to `sys_ni_syscall` when it is out of
range. Thus `(0xc00a55c4 - 0xc00a5044) / 4 = 352`, with entries 0 through 351.

### 3. Decode the actual ARM syscall table

The presence of a function symbol is insufficient: the architecture's syscall-table entry must point to it. The following script reads selected little-endian table entries directly from `zImage` and resolves them against the recovered symbols:

```bash
python3 - "$FW_DIR" <<'PY'
import sys
from pathlib import Path
import struct

fw_dir = Path(sys.argv[1])
image = (fw_dir / "kindle-firmware-5.6.1.1/imx50_yoshime/zImage").read_bytes()
symbols = {}
for line in Path("/tmp/kindle-kallsyms.txt").read_text().splitlines():
    try:
        address, kind, name = line.split(" ", 2)
        symbols.setdefault(int(address, 16), []).append(name)
    except ValueError:
        pass

base_address = 0xC0008000
sys_call_table = 0xC0122044
table_offset = sys_call_table - base_address

for number, expected in [
    (252, "epoll_wait"),
    (285, "accept"),
    (346, "epoll_pwait"),
    (356, "eventfd2"),
    (357, "epoll_create1"),
    (358, "dup3"),
    (359, "pipe2"),
    (360, "inotify_init1"),
    (365, "recvmmsg"),
    (366, "accept4"),
]:
    target = struct.unpack_from("<I", image, table_offset + number * 4)[0]
    print(number, expected, hex(target), symbols.get(target, ["<unknown>"]))
PY
```

The decoded table, rather than the nominal upstream kernel version, is the authoritative static evidence for this firmware. For the DXG kernel, the same decoding gives:

```text
250 epoll_create   sys_epoll_create
251 epoll_ctl      sys_epoll_ctl
252 epoll_wait     sys_epoll_wait
285 accept         sys_accept
346 epoll_pwait    sys_ni_syscall
351 eventfd        sys_eventfd
356 eventfd2       OUT OF TABLE
357 epoll_create1  OUT OF TABLE
358 dup3           OUT OF TABLE
359 pipe2          OUT OF TABLE
360 inotify_init1  OUT OF TABLE
366 accept4        OUT OF TABLE
```

The legacy process primitives used by the overlay are also present: `pipe`
(42), `fcntl` (55), and `dup2` (63).

### 4. Inspect the release binary

Build an unstripped 32-bit compatibility binary so Go symbols and disassembly remain available:

```bash
rm -rf /tmp/localsend-arm-audit /tmp/localsend-arm-audit-overlay
mkdir -p /tmp/localsend-arm-audit-overlay
overlay="$(go run ./tools/armcompat \
    -output-dir /tmp/localsend-arm-audit-overlay)"

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 \
    go build -overlay="$overlay" -o /tmp/localsend-arm-audit .

go tool objdump \
    -s 'internal/runtime/syscall/linux.EpollWait' \
    /tmp/localsend-arm-audit

go tool objdump -s 'runtime.netpollinit' /tmp/localsend-arm-audit
go tool objdump -s 'syscall.legacyAccept' /tmp/localsend-arm-audit
go tool objdump -s 'syscall.legacyPipe' /tmp/localsend-arm-audit
go tool objdump -s 'syscall.forkAndExecInChild1' /tmp/localsend-arm-audit
```

The disassembly must contain the modern probes and legacy ARM syscalls 250
(`epoll_create`), 252 (`epoll_wait`), 351 (`eventfd`), 285 (`accept`), 42
(`pipe`), 63 (`dup2`), and the required `fcntl` setup. Source guards in the
overlay generator make a changed Go implementation fail before release.

### 5. Exercise target-selected paths with QEMU user mode

The pinned development image includes QEMU user mode. CI runs the deterministic
compatibility audit against the exact packaged `arm-legacy` and `armv7` binaries:

```bash
just release
just test-armcompat
```

For each 32-bit ARM package, the audit uses Docker's isolated loopback network,
requires no Internet access, forces the audited Kindle syscall gaps with the
seccomp launcher, performs repeated API requests and a complete V2 transfer,
and rejects the run unless the guest trace contains every modern failure and
legacy fallback, the on-transfer shell callback completes, and the transferred
file matches. It also verifies that the ARM overlay fails compilation if reused
by an arm64 build.

For manual investigation, QEMU can force Go to select the audited DXG kernel
version while tracing guest syscall numbers:

```bash
qemu-arm \
    -r 2.6.22.19-lab126 \
    -strace \
    /tmp/localsend-arm-audit --version
```

For an isolated receiver and complete V2 transfer, use a private user/network namespace so an installed desktop LocalSend instance cannot already own port 53317:

```bash
go build -o /tmp/localsend-host-audit .
cc -O2 -Wall -Wextra \
    -o /tmp/kindle-enosys \
    tools/armcompat/testdata/kindle_enosys.c
printf 'legacy kernel transfer audit\n' > /tmp/legacy-input.txt
rm -rf /tmp/legacy-recv /tmp/legacy-config
mkdir -p /tmp/legacy-recv /tmp/legacy-config

unshare -Urn bash <<'SH'
set -eu
ip link set lo up
/tmp/kindle-enosys \
    qemu-arm -r 2.6.22.19-lab126 -strace \
    /tmp/localsend-arm-audit recv \
    --https=false --webrtc=false \
    --devname AuditKindle \
    --dir /tmp/legacy-recv \
    --config-dir /tmp/legacy-config \
    --on-transfer "printf callback-ok > /tmp/legacy-callback" \
    >/tmp/legacy-recv.out 2>/tmp/legacy-recv.trace &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT

for attempt in $(seq 1 100); do
    if curl -fsS --max-time 1 \
        http://127.0.0.1:53317/api/localsend/v2/info \
        >/tmp/legacy-info.json; then
        break
    fi
    sleep 0.05
done

/tmp/localsend-host-audit send \
    --https=false --ip 127.0.0.1 --devname AuditSender \
    /tmp/legacy-input.txt
SH

sha256sum /tmp/legacy-input.txt /tmp/legacy-recv/legacy-input.txt
test "$(cat /tmp/legacy-callback)" = callback-ok
rg 'epoll_create1\(|epoll_create\(|eventfd2\(|eventfd\(' /tmp/legacy-recv.trace
rg 'accept4\(|accept\(|pipe2\(|pipe\(|dup3\(|dup2\(' /tmp/legacy-recv.trace
rg 'epoll_wait\(' /tmp/legacy-recv.trace
! rg -q 'epoll_pwait\(' /tmp/legacy-recv.trace
```

The Linux seccomp launcher (`tools/armcompat/testdata/kindle_enosys.c`) returns
`ENOSYS` for the modern host calls corresponding to the DXG gaps:
`epoll_create1`, flagged `eventfd2`, flagged `accept4`, flagged `pipe2`, `dup3`,
`recvmmsg`, `sendmmsg`, `getrandom`, and `prlimit64`. QEMU maps guest legacy
`eventfd`, `accept`, and `pipe` to the flagged host variants with flags 0, so
the launcher permits those exact calls.

`epoll_pwait` cannot be fault-injected this way. QEMU user mode implements guest
`epoll_wait` by calling host `epoll_pwait`, so a host-level `ENOSYS` would also
break the patched path. Confirm it with the guest trace and disassembly.

A successful audit exercises all five overlay families: netpoll initialization,
network polling, TCP accept, the process status pipe, and child descriptor
remapping. The on-transfer callback proves that the `pipe2`/`dup3` fallbacks
work through a complete `os/exec` operation.

This approximates the Kindle-shaped failures that matter for the V2 receive path where QEMU's host mapping allows it. It is still not a full syscall-table emulator. Static table inspection and final testing on the actual Kindle remain necessary.

## Support boundary

Some older Kindles run Amazon-modified Linux 2.6 kernels. The device from issue #15 reports Linux `2.6.31-rt11-lab126`, while Go 1.24 and later officially require Linux 3.2 or newer. Architecture support also lagged the generic Linux introduction of some syscalls; in particular, Go's own Go 1.23 source records that ARM did not gain `accept4` until Linux 2.6.36.

Consequently:

- compatibility with Linux 2.6 is **best effort**, not guaranteed by the Go toolchain;
- selecting `arm-legacy` changes the ARM instruction baseline (`GOARM=5`), not the required Linux syscalls;
- a successful cross-build or QEMU user-mode run does not prove compatibility with the device kernel; and
- receiver startup, accepting an HTTP connection, discovery, and a complete transfer must be tested on a representative device or full-system old-kernel environment.

## Confirmed `epoll_pwait` failure

Go's 32-bit ARM runtime uses `epoll_pwait` for network polling. The Amazon 2.6.31 kernel in issue #15 returns `ENOSYS`, causing a fatal runtime error before the receiver can listen:

```text
runtime: epollwait on fd 13 failed with 38
fatal error: runtime: netpoll failed
```

The firmware audit confirms why: ARM syscall-table entry 346 points to `sys_ni_syscall`, while entry 252 points to the working `sys_epoll_wait` implementation.

This was a latent compatibility problem, not a regression introduced by Go 1.26. The published v1.3.0 ARMv7 binary was built with Go 1.23.12 and also invokes `epoll_pwait`. The issue surfaced only when the plugin was run on a kernel that does not implement that syscall.

`just release` generates a scoped Go build overlay through `tools/armcompat`.
For 32-bit ARM release builds, it restores `epoll_wait`, legacy netpoll
initialization, `accept`, and the process-creation fallbacks described below.
ARM64 builds are unchanged. The generator targets the pinned Go 1.26 runtime
layout and deliberately fails if any expected runtime, poll, syscall, pipe, or
exec source no longer matches. Other Go versions are unsupported until their
actual source trees and a complete release build are validated.

## Confirmed `accept4` gap

Fixing `epoll_pwait` alone is insufficient. The firmware's ARM syscall-table entry 366 also points to `sys_ni_syscall`, even though an unreferenced `sys_accept4` function is present elsewhere in the image. Entry 285 points to the working legacy `sys_accept` implementation.

Go 1.23 retained an ARM-only fallback from `accept4` to `accept` because ARM did not expose `accept4` until Linux 2.6.36. Go 1.24 removed that fallback when its minimum supported Linux version became 3.2. Current Go networking calls `accept4` without an `ENOSYS` fallback, so after the netpoll fix the receiver would fail when it enters the TCP accept loop.

A QEMU-traced API request and complete V2 transfer before this fix showed that every incoming connection reached `accept4`. QEMU succeeded by forwarding syscall 366 to the modern host kernel; the extracted Kindle table proves that the same call returns `ENOSYS` on the device. The 32-bit ARM release overlay now restores the former `accept` fallback: it tries `accept4`, handles the legacy-kernel errors recognized by Go 1.23, and uses ARM syscall 285 (`accept`) before applying close-on-exec and nonblocking mode. A seccomp fault-injection run against the packaged `arm-legacy` binary forced each flagged `accept4` call to return `ENOSYS`; the API request and complete V2 transfer then succeeded through traced legacy `accept` calls.

## Confirmed Linux 2.6.22 initialization and process gaps

The DXG table ends at syscall 351. Modern Go calls `epoll_create1` (357) and
`eventfd2` (356), in that order, when network polling is initialized. Both are
fatal without fallbacks. The overlay retries `epoll_create` (250) and `eventfd`
(351), then applies close-on-exec and nonblocking state with separate `fcntl`
calls. Descriptors are closed if setup fails.

The KOReader plugin also always configures an on-transfer shell callback.
Go's process path uses `pipe2` (359) for the child status pipe and `dup3` (358)
for descriptor remapping; Go 1.26 has no pre-2.6.27 fallback. The overlay retries
legacy `pipe` (42), applies `FD_CLOEXEC` while `ForkLock` is held, and retries
`dup2` (63), applying `FD_CLOEXEC` where the original `dup3` requested it.

The deterministic packaged-binary audit forces all four modern calls to
`ENOSYS`, completes a V2 transfer, and requires the shell callback to create
its sentinel file through traced legacy calls.

## Verified syscall gaps

The current binary contains or can reach syscalls newer than this firmware. The statuses below combine the decoded Kindle syscall table, current Go source, linked binary symbols, and the QEMU-traced V2 workflow.

| Syscall | PW 5.6.1.1 | DXG 2.5.8 | Compatibility status |
|---|---|---|---|
| `pipe` (42), `dup2` (63) | Present | Present | Process fallbacks used only after modern calls return `ENOSYS`. |
| `epoll_create` (250), `epoll_wait` (252) | Present | Present | Runtime overlay initialization and polling paths. |
| `accept` (285) | Present | Present | Incoming TCP fallback. |
| `epoll_pwait` (346) | `sys_ni_syscall` | `sys_ni_syscall` | Replaced by `epoll_wait`. |
| `eventfd` (351) | Present | Present, final table entry | DXG fallback for netpoll wakeups. |
| `eventfd2` (356), `epoll_create1` (357) | Present | Outside table | DXG probes fall back to legacy calls plus `fcntl`. |
| `dup3` (358), `pipe2` (359) | Present | Outside table | DXG `os/exec` paths fall back to `dup2` and `pipe` plus `fcntl`. |
| `inotify_init1` (360) | Present | Outside table | No current receiver path calls it; direct users still receive `ENOSYS`. |
| `recvmmsg` (365) | `sys_ni_syscall` | Outside table | Future batch-UDP risk; normal V2 and current Pion/mDNS paths use ordinary reads. |
| `accept4` (366) | `sys_ni_syscall` | Outside table | Falls back to `accept`, then applies close-on-exec and nonblocking state. |
| `prlimit64` (369) | Outside table | Outside table | Startup initialization tolerates failure; explicit operations may fail. |
| `sendmmsg` (374) | Outside table | Outside table | Same future batch-UDP risk as `recvmmsg`. |
| `getrandom` (384) | Outside table | Outside table | Crypto path falls back to `/dev/urandom`. |
| `copy_file_range` (391) | Outside table | Outside table | Version-gated; generic copying remains available. |
| `futex_time64` (422) | Outside table | Outside table | Go selects legacy `futex` 240 for both audited kernel releases. |
| `pidfd_*`, `clone3` | Outside table | Outside table | Go probes support and retains older process-management paths in the audited workflow. |
| `faccessat2`, `fchmodat2` | Outside table | Outside table | Go handles `ENOSYS` and uses older paths where semantics allow it. |

The current deterministic workflow verifies receiver startup, repeated API
requests, a complete V2 transfer, and the on-transfer `os/exec` callback while
forcing every overlay-covered modern syscall to return `ENOSYS`. This remains
a scoped workflow audit, not proof that every exported CLI or dependency path
supports Linux 2.6.22.

## Maintenance risk

The overlay depends on private Go runtime and standard-library source paths and implementation details. CI pins the release toolchain, so it will not change unexpectedly there, but every intentional Go upgrade must treat the overlay as version-sensitive.

The strict source checks make an incompatible Go update fail the release build rather than silently omit a patch. They do not guarantee that a newer Go release has not introduced another Linux 3.2+ assumption elsewhere.

When changing the Go version or networking dependencies:

1. Review Go's Linux minimum-version notes and ARM runtime changes.
2. Run the overlay unit tests and `just check`.
3. Build all packages with `just release`.
4. Run `just test-armcompat` to verify every modern probe and legacy fallback, the ARM-only guard, a complete V2 transfer, and the on-transfer process callback.
5. Inspect an unstripped binary when source-level or disassembly confirmation is needed.
6. Test `--version`, receiver startup, a local API request, discovery, and a complete transfer on an affected Kindle.
7. Test WebRTC separately when it is intended to be supported on that device.

QEMU user mode is useful for inspecting guest syscall selection and target Go code. By itself it forwards syscalls to the host kernel; `kindle_enosys` injects the audited gaps, with flags-zero exceptions where QEMU implements guest legacy calls through newer host entry points. Guest `epoll_wait` versus `epoll_pwait` must still be checked with `-strace` and objdump because QEMU implements both through host `epoll_pwait`. The definitive compatibility test remains the complete workflow on the target device.
