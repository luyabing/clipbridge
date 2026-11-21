# ClipBridge

> Clipboard sharing between Ubuntu and Windows on the same LAN.  
> Auto-discovery, two-way text / image / file sync, pairing-code encryption, tray support.

[![License: MIT](https://img.shields.io/badge/license-MIT-6256F3.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8.svg)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Ubuntu%20%7C%20Windows-191B24.svg)](#requirements)

[简体中文](README.md) · English

ClipBridge discovers peers on the same subnet over UDP broadcast, or connects to a manually entered IP. It syncs text, PNG images, and file lists, runs in the tray, and can start at login.

It ships as a single static binary — end users do not need Go, GTK, or any other runtime.

## Features

- **Auto-discovery** over UDP broadcast on the local network.
- **Manual connection** with `IP` or `IP:port` when discovery is unavailable.
- **Two-way sync** for text, PNG images, and file lists. Text and images are written straight to the system clipboard.
- **File receiving** into `Downloads/ClipBridge/<timestamp>/`; the file list is also written to the clipboard when the platform allows it.
- **Pairing code (recommended)**: matching codes on both sides enable HMAC-authenticated handshakes and AES-256-GCM encrypted transport. Without a code, traffic is plain text and unauthenticated.
- **Tray and autostart**: pause syncing, open settings, open the receive folder, or toggle autostart from the tray.
- **Local control panel** at `http://127.0.0.1:45878/`, bound to loopback only.
- **Sensitive-content skip** for clipboard entries marked sensitive by password managers (best effort, platform dependent).
- **Size limits and safe filenames**: 16 MiB text, 64 MiB PNG, 512 MiB per file batch (default), 200 files max; received filenames are stripped of paths and dangerous characters.

## Requirements

- Ubuntu 22.04+ (Wayland or X11) and Windows 10 / 11.
- Both devices must reach each other on the local network.
- Go 1.25+ is only needed when building from source; tested with Go 1.27.4.

## Quick start

### Run from source

Go 1.25+ is required; no CGO, GTK headers, or other runtime dependencies.

```bash
git clone https://github.com/luyabing/clipbridge.git
cd clipbridge
./scripts/build.sh
./dist/clipbridge_linux_amd64
```

On Windows, run from the source directory:

```bat
scripts\build-windows.bat
dist\clipbridge_windows_amd64.exe
```

`dist/` holds local build output and is not committed; release binaries are published on the GitHub Releases page.

### Ubuntu

Install the `.deb` (recommended):

```bash
sudo apt install ./dist/clipbridge_1.0.0_amd64.deb
```

You can also download the `.deb` from GitHub Releases.

Start **ClipBridge** from the application list, or run `clipbridge`. Uninstall with:

```bash
sudo apt remove clipbridge
```

Or run the binary directly without installing:

```bash
chmod +x dist/clipbridge_linux_amd64
./dist/clipbridge_linux_amd64
```

The app starts in the system tray. If the tray icon is not visible, open <http://127.0.0.1:45878/> in a browser. GNOME needs AppIndicator / KStatusNotifierItem support (usually enabled by default on Ubuntu).

Install into your home directory and create a menu entry (no root required):

```bash
./scripts/install-linux.sh
```

Then start **ClipBridge** from the application list, or run `~/.local/bin/clipbridge`. Autostart is toggled from the tray or the control panel; no need to rerun the install script.

### Windows

Double-click `dist/clipbridge_windows_amd64.exe`.

- Windows Defender Firewall may ask for network access on first launch; allow **Private networks**.
- The app has no main window and lives in the tray. Double-click the tray icon or choose “打开设置…” to open the control panel.
- Autostart is toggled from the tray or the control panel.
- If the firewall prompt never appears, see “Windows firewall” below.

### First pairing

1. Start ClipBridge on both computers.
2. On the same LAN, the apps usually discover and connect within seconds.
3. If they do not connect, enter the other machine’s LAN IP under “手动添加 IP”, for example `192.168.122.5` with port `45876`.
4. Strongly recommended: set the **same** pairing code in Settings on both sides and save. Setting it on one side only fails with “配对码设置不一致”.
5. Copy text on either machine; it should paste on the other.

> The control panel binds to `127.0.0.1`, so it cannot be opened from the other machine. This is intentional.

## KVM / virtual machine networking

### libvirt default NAT (recommended)

The `default` network from `virsh net-list` usually uses `192.168.122.0/24`:

- Host (Ubuntu): `192.168.122.1` on `virbr0`.
- Windows VM: typically `192.168.122.x` from libvirt DHCP.

Check the Windows IPv4 address with `ipconfig`; on Ubuntu run:

```bash
virsh net-dhcp-leases default
ip neigh show dev virbr0
```

How to connect:

- On Windows, add `192.168.122.1` manually.
- On Ubuntu, add the VM’s address, for example `192.168.122.5`.
- Adding both is fine; duplicate connections converge to one.

In NAT mode the host can reach the VM and the VM can reach the host, so no port forwarding is needed.

### Windows firewall

Open PowerShell as Administrator and allow inbound ports:

```powershell
New-NetFirewallRule -DisplayName "ClipBridge TCP" -Direction Inbound -Protocol TCP -LocalPort 45876 -Action Allow -Profile Any
New-NetFirewallRule -DisplayName "ClipBridge UDP" -Direction Inbound -Protocol UDP -LocalPort 45875 -Action Allow -Profile Any
```

Only TCP `45876` and UDP `45875` need to be open. The control panel’s TCP `45878` is loopback-only and must not be forwarded.

### Ubuntu firewall

With UFW enabled:

```bash
sudo ufw allow 45875/udp
sudo ufw allow 45876/tcp
```

### QEMU user-mode / slirp networking

Not recommended. slirp only lets the VM reach the outside; the host cannot connect in. `hostfwd` is possible but the handshake advertises the VM’s internal port, which breaks reconnects. Prefer libvirt NAT, bridged networking, or macvtap.

## Usage

### Control panel

- **Devices**: connected peers, nearby devices, manual addresses.
- **Pause syncing**: stops sending and receiving while keeping the network connection.
- **Settings**: device name, pairing code, auto-connect, sensitive skip, text/image/file toggles, autostart.
- **Recent activity**: event summaries and sizes only; clipboard contents are never logged.
- **Open receive folder / config folder**.
- **Quit**.

Manual addresses are saved in the config; removing one stops future reconnects but does not necessarily drop an existing connection immediately.

### Tray menu

- Current status (connected peers / paused / waiting)
- Open settings…
- Pause syncing
- Start at login
- Open receive folder
- Quit ClipBridge

The tray uses D-Bus StatusNotifierItem on Linux and the native tray on Windows.

### Files and images

- Images are transferred as PNG and written to the remote clipboard, ready to paste into chat apps or editors.
- Files are saved to the receive folder and the file list is written to the clipboard. Open the folder from the control panel.
- Folders are not supported; archive them into a single file first.
- Duplicate names get `(1)`, `(2)`, and so on; existing files are never overwritten.
- The per-batch limit can be changed with `maxFileMB` in the config (1–2048).

### Command-line flags

```text
--headless        run without a tray icon
--debug           verbose logging
--config <path>   use a specific config file
--version         print the version
```

`--headless` is useful for debugging or environments without a tray; the control panel still works.

## Configuration

The config is created on first launch:

| Platform | Config and log directory |
| --- | --- |
| Ubuntu | `${XDG_CONFIG_HOME:-~/.config}/clipbridge/` |
| Windows | `%APPDATA%\clipbridge\` |

Contents:

- `config.json`: settings, mode `0600` on Linux.
- `clipbridge.log`: runtime log, rotated to `clipbridge.log.old` at 5 MiB.
- `icon.png`: cached tray / control-panel icon.

Main fields:

| Field | Default | Description |
| --- | --- | --- |
| `deviceName` | hostname | Name shown to other devices |
| `pairCode` | empty | Must match on both sides for auth + encryption; empty means plain text |
| `autoConnect` | `true` | Connect to discovered peers automatically |
| `syncText` | `true` | Sync text |
| `syncImage` | `true` | Sync images |
| `syncFiles` | `true` | Sync files |
| `skipSensitive` | `true` | Skip content marked sensitive |
| `paused` | `false` | Pause syncing |
| `manualPeers` | `[]` | Manual addresses |
| `tcpPort` | `45876` | Clipboard transport port |
| `discoveryPort` | `45875` | UDP discovery port |
| `webPort` | `45878` | Local control panel port |
| `maxFileMB` | `512` | Per-batch file size limit |
| `downloadDir` | empty | Override the receive folder; empty means `Downloads/ClipBridge` |

Port changes require a restart. If you change the TCP port, manual connections must use `IP:newport`, or rely on discovery broadcasting the new port. The three ports must be distinct.

The config is plain JSON; quit the app before editing it by hand to avoid it being overwritten.

### Ports

| Protocol | Default | Purpose | LAN exposure |
| --- | --- | --- | --- |
| UDP | `45875` | Discovery broadcast | Yes |
| TCP | `45876` | Clipboard and file transport | Yes |
| TCP | `45878` | Local control panel (`127.0.0.1`) | No |

## Security notes

- **No pairing code**: anyone who can reach the TCP port can connect and send clipboard data, and traffic is plain text. Only use this on a fully trusted temporary network.
- **With a pairing code**: the handshake uses HMAC-SHA256 to prove both sides know the code; every subsequent frame is encrypted and authenticated with AES-256-GCM. The code is never sent over the network, but it is stored in the local config.
- The pairing code doubles as the key-derivation secret and provides no forward secrecy. Use a long random code and avoid weak passphrases on untrusted networks.
- The control panel binds to `127.0.0.1` and validates `Host`, `Origin`, and a custom request header against DNS rebinding and CSRF. It has no password; other local users on the same machine could access it. Evaluate accordingly on shared computers.
- Logs contain event summaries, sizes, device names, and errors — never clipboard contents.
- Received filenames are stripped of paths and dangerous characters to prevent traversal, but do not sync executables from untrusted sources.
- Sensitive-content skipping depends on OS / password-manager markers; it is best effort, not a security boundary.

## Limitations

- Built for Ubuntu and Windows only; other platforms are untested.
- No folders, incremental sync, resume, or history.
- Simultaneous copies converge on whichever content each side received last; clipboards have no merge semantics.
- Syncing stops when the app exits. On Wayland, pasteability of clipboard content depends on the writing process staying alive (a Wayland design constraint).
- Auto-discovery relies on UDP broadcast and may fail across VLANs, some virtual networks, or strict firewalls; connect by IP in that case.
- Image echo suppression has a ~2.5 s window; another image copied within that window may be ignored.
- File integrity relies on TCP and size checks; there is no separate SHA checksum.

## Build and package

Go 1.25+ is required; no CGO or GTK headers.

```bash
./scripts/check.sh   # go test + go vet + shell syntax checks
./scripts/build.sh   # Linux, Windows, and deb
```

Outputs:

- `dist/clipbridge_linux_amd64` (static ELF)
- `dist/clipbridge_windows_amd64.exe` (GUI-subsystem PE with embedded icon)
- `dist/clipbridge_1.0.0_amd64.deb` (when `dpkg-deb` is available)

Build only the deb:

```bash
make deb
```

Native Windows build:

```bat
scripts\build-windows.bat
```

Or manually:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -H windowsgui -X main.version=1.0.0" \
  -o dist/clipbridge_windows_amd64.exe .
```

- `-H windowsgui` hides the console window.
- `rsrc_windows_amd64.syso` embeds the Windows icon and is committed. Regenerate it with:

```bash
go install github.com/akavel/rsrc@latest
rsrc -ico assets/icon.ico -o rsrc_windows_amd64.syso
```

To see console logs on Windows, drop `-H windowsgui` and run from a terminal.

## Troubleshooting

**Cannot connect**

1. Confirm both machines are on the same subnet and can `ping` each other.
2. Check Windows Firewall for TCP `45876` / UDP `45875`, and UFW on Ubuntu.
3. Pairing codes must match on both sides, or both be empty.
4. Check “Recent activity” and `clipbridge.log`; run with `--debug` for detail.
5. A port conflict logs “无法监听 TCP/控制面板”. Change the port or stop the other process.
6. If discovery fails, enter the IP manually.

**Tray icon missing**

GNOME needs the AppIndicator / KStatusNotifierItem extension. Without a tray, run with `--headless` and open <http://127.0.0.1:45878/>; autostart still works.

**Copying does nothing on Linux**

Run with `--debug` and check the “系统剪贴板监听” and event lines. Wayland needs compositor data-control support (GNOME 49+ natively; older versions fall back to XWayland). If XWayland is unavailable too, make sure `DISPLAY` or `WAYLAND_DISPLAY` is set.

**“剪贴桥已经在运行”**

One instance per config/port is allowed. A second launch opens the existing control panel. If nothing is running, check whether `45878` is taken or change `webPort`.

**Cannot paste received files on Windows**

The files are already in the receive folder; use the folder directly. Some apps reject file lists written programmatically — drag the file in manually.

## Project structure

```text
.
├── main.go                    entry point, flags, signal handling
├── internal/
│   ├── app/                   lifecycle, connections, discovery, files, web, tray
│   ├── config/                JSON config
│   ├── platform/              autostart, open folder, error dialogs
│   ├── proto/                 framing, handshake, auth, encryption
│   └── assets/                embedded icons
├── assets/                    SVG / PNG / ICO source icons
├── internal/app/web/          control panel frontend
├── scripts/                   build, deb packaging, install, checks
├── rsrc_windows_amd64.syso    Windows executable icon resource
└── dist/                      build output (not committed)
```

## License

MIT. See [LICENSE](LICENSE).
