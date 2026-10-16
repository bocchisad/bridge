# bridge

Send files and folders between your machines (Mac ↔ Linux) over the local network, with an optional self-hosted relay for when you're not on the same LAN.

Not a sync tool. Not KDE Connect. Just pair once, then `bridge send`.

## Quick start

```bash
# build
go build -o bridge ./cmd/bridge

# interactive menu (recommended)
./bridge
# or: ./bridge menu
# 1) Start daemon → 2/3) Pair → 5) Send file

# classic commands also work:
./bridge daemon
./bridge pair
./bridge pair 123456 --addr 192.168.1.10:7331
./bridge send <peer-name> ~/Documents/notes.pdf
./bridge incoming && ./bridge accept <transfer_id>
```

Tray UI (optional — starts daemon if needed, notifications, send/accept):

```bash
./bridge tray
```

- **Show pairing PIN** — desktop notification + PIN in terminal
- **Send to peer** — pick peer → file dialog → send
- **Incoming** — Accept / Reject with notifications
- **Open inbox** — `~/Downloads/bridge`

## Internet transfers

Run a relay on a VPS (or any reachable host):

```bash
./bridge relay --listen :7330 --token 'long-secret'
```

On both clients, edit `~/.config/bridge/config.json`:

```json
{
  "relay_url": "vps.example.com:7330",
  "relay_token": "long-secret"
}
```

Restart daemons. LAN is still preferred when peers are discoverable via mDNS.

## Config

| Path | Purpose |
|------|---------|
| `~/.config/bridge/config.json` | name, ports, inbox, relay |
| `~/.config/bridge/identity.json` | device id |
| `~/.config/bridge/cert.pem` / `key.pem` | TLS identity |
| `~/.config/bridge/peers.json` | paired devices |
| `~/Downloads/bridge/` | default inbox |

Environment:

- `BRIDGE_CONFIG_DIR` — override config directory (useful for local multi-instance tests)

## Threat model

**Protects against:** casual LAN neighbors, and a relay operator reading file contents (TLS is end-to-end between daemons; relay only proxies bytes).

**Does not protect against:** malware on an already-paired device, or someone who sees/enters the PIN during the short pairing window.

## Build notes

- **Arch Linux:** `go build -o bridge ./cmd/bridge`. Tray needs a working StatusNotifier/AppIndicator host; file picker uses `zenity` if installed.
- **macOS:** same build command. Tray uses the menu bar; file picker uses a native `osascript` dialog.

```bash
GOOS=darwin GOARCH=arm64 go build -o bridge-darwin-arm64 ./cmd/bridge
GOOS=linux GOARCH=amd64 go build -o bridge-linux-amd64 ./cmd/bridge
```

## Commands

| Command | Description |
|---------|-------------|
| `bridge daemon` | background agent |
| `bridge relay` | optional relay server |
| `bridge tray` | system tray UI |
| `bridge status` | local device info |
| `bridge peers` | paired peers + online state |
| `bridge pair` / `bridge pair <PIN>` | pairing |
| `bridge send <peer> <path>` | send file or folder |
| `bridge incoming` / `accept` / `reject` | incoming transfers |

## License

MIT
