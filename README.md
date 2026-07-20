## LocalSend for KOReader

A KOReader plugin that enables sending and receiving files between devices using the [LocalSend](https://localsend.org/) protocol. Transfer ebooks, documents, and other files directly to and from your e-reader.

**[Download latest release](https://github.com/kaikozlov/localsend.koplugin/releases/latest)**

### Installation

1. Download the release for your device's architecture:

   | Architecture | Devices |
   | ------------ | ------- |
   | **armv7** | Kindle (all models), Kobo, reMarkable 2, PocketBook |
   | **arm64** | reMarkable Paper Pro |
   | **arm-legacy** | Kindle 3, Kindle DX, older 32-bit ARM devices |

   > **Not sure?** Try armv7 first. Use arm-legacy only if armv7 doesn't work on older hardware.

2. Extract `localsend.koplugin` to your KOReader plugins directory:
   - Kindle: `/mnt/us/koreader/plugins/`
   - Kobo: `/.adds/koreader/plugins/`
3. Restart KOReader

### Usage

**Receiving files:**
1. Go to **Menu → Network → LocalSend**
2. Configure your save directory and device name
3. Tap **Start server**
4. On your phone/computer, open LocalSend and send files to your e-reader

**Sending files:**
1. Go to **Menu → Network → LocalSend**
2. Tap **Send file...** or **Send current book**
3. Select a target device from the discovered list
4. Choose a file or folder to send (if using "Send file...")

Or long-press a file or folder in File Manager (also History, Collections, or File Search) and tap **Send with LocalSend**, then choose a target device.

### Settings

| Setting | Description |
| ------- | ----------- |
| Save directory | Destination folder for received files |
| Device name | Display name on the network (e.g., "My Kindle") |
| Allowed extensions | Comma-separated list of accepted file types (e.g., `epub,pdf,mobi`) |
| File type routing | Route files to different directories by type |
| PIN code | Required PIN for incoming transfers (optional) |
| Use HTTPS | Enable TLS encryption (recommended, on by default) |
| Use WebRTC | Enable v3 protocol for latest LocalSend apps |
| Start with KOReader | Auto-start server on launch |

### Updates

Open **Menu → Network → LocalSend → Updates** to keep the plugin current:

- **Installed version / Update available** — shows the installed version, or the cached latest version when an update has been found. Tap to check GitHub releases and review release notes.
- **Check for updates** — manually checks the latest GitHub release. If a matching package exists for your device architecture, you can install it directly from KOReader.
- **Auto-check for updates** — optional periodic background checks. When a newer release is found, LocalSend shows an update badge in the menu; updates are only installed after you confirm.

If you are already up to date, the updater can reinstall the current release. This is useful for recovering a damaged plugin install. Restart KOReader after installing or reinstalling an update.

### Troubleshooting

Open **Menu → Network → LocalSend → Troubleshooting** for a guided setup check, device-discovery help, transfer-error explanations, and an exportable support report. Troubleshooting remains available while the receiver is running.

For common symptoms and fixes (connection refused, device not discovered, HTTP 400/403, HTTPS and WebRTC issues), see the **[Troubleshooting guide](docs/TROUBLESHOOTING.md)**.

When reporting a bug, include the generated support report. It snapshots LocalSend evidence before running lifecycle checks, includes the tail of KOReader's `crash.log`, and redacts Wi-Fi SSIDs, MAC addresses, and PIN values by default. Review device aliases, file names, routing paths, and private IP addresses before posting publicly.

### Translations

The plugin ships its own translations from `lua/locale/`, independent of KOReader's core language packs. The UI language follows KOReader's **Language** setting automatically.

Want to help translate? Translations are contributed as `.po` files via pull requests — see the **[Translations guide](lua/locale/README.md)** for how to add a language, update strings, and validate your work.

### Compatibility

> **Kindle users:** Works best with firmware 5.16.3+. As of v1.4.2, support for older devices has been expanded, including earlier firmware and the legacy 2.6 kernel (e.g. Kindle Paperwhite 1st Gen).

Reported working on: Kindle Paperwhite 1st Gen, Kindle Paperwhite 10-12th Gen, Kindle Basic 10-11th Gen, Kindle Oasis, Kindle Colorsoft, Kindle Scribe, Kobo Clara/Forma/Libra Colour, Kobo Aura N236.

### License

MIT License

---

## CLI & Developer Documentation

The backend can be used as a standalone CLI tool. See [docs/CLI.md](docs/CLI.md) for:
- Standalone CLI usage and flags
- Extension routing configuration
- Building from source
