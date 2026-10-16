# bridge

**Send files between your machines over LAN — or an optional self-hosted relay when you're not on the same network.**

Not a sync tool. Not KDE Connect. Pair once with a PIN, then push files from the CLI, interactive menu, or system tray.

[![platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-black?style=flat-square)](https://github.com/bocchisad/bridge)
[![go](https://img.shields.io/badge/go-1.22%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![license](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)](./LICENSE)
[![version](https://img.shields.io/badge/version-0.1.0-3b82f6?style=flat-square)](https://github.com/bocchisad/bridge)

## Features

- **LAN-first** — mDNS discovery, direct TLS between daemons
- **Optional relay** — self-hosted byte proxy for cross-network transfers (E2E TLS; relay sees ciphertext)
- **PIN pairing** — 6-digit code, then peers trust by certificate fingerprint
- **CLI + menu + tray** — `bridge`, `bridge menu`, `bridge tray`
- **Files & folders** — folders stream as tar; SHA-256 verified on receive

## Quick start

```bash
git clone https://github.com/bocchisad/bridge.git
cd bridge
go build -o bridge ./cmd/bridge

# interactive menu (recommended)
./bridge
# 1) Start daemon → 2/3) Pair → 5) Send file
```

### Classic commands

```bash
# on each machine
./bridge daemon

# device A
./bridge pair
# → shows PIN

# device B
./bridge pair 123456
./bridge pair 123456 --addr 192.168.1.10:7331   # if mDNS fails

# send
./bridge send <peer-name> ~/Documents/notes.pdf

# receiver (if auto_accept is false)
./bridge incoming
./bridge accept <transfer_id>
```

### Tray

```bash
./bridge tray
```

| Action | What it does |
|--------|----------------|
| Show pairing PIN | notification + PIN in terminal |
| Send to peer | file dialog → send |
| Incoming | Accept / Reject |
| Open inbox | `~/Downloads/bridge` |

**Linux file picker:** install `zenity` (`sudo pacman -S zenity`). Falls back to `kdialog` / `yad` / `wofi`.

## Internet (relay)

```bash
./bridge relay --listen :7330 --token 'long-secret'
```

On both clients, `~/.config/bridge/config.json`:

```json
{
  "relay_url": "vps.example.com:7330",
  "relay_token": "long-secret",
  "auto_accept": true
}
```

Restart daemons. LAN is still preferred when peers are visible via mDNS.

## Config

| Path | Purpose |
|------|---------|
| `~/.config/bridge/config.json` | name, ports, inbox, relay, `auto_accept` |
| `~/.config/bridge/identity.json` | device id |
| `~/.config/bridge/cert.pem` / `key.pem` | TLS identity |
| `~/.config/bridge/peers.json` | paired devices |
| `~/Downloads/bridge/` | default inbox |

- `BRIDGE_CONFIG_DIR` — override config directory (multi-instance tests)

## Threat model

| Protects against | Does **not** protect against |
|------------------|------------------------------|
| Casual LAN neighbors | Malware on an already-paired device |
| Relay operator reading file contents (E2E TLS) | Someone who sees the PIN during pairing |

## Build

```bash
go build -o bridge ./cmd/bridge

# cross-compile
GOOS=darwin GOARCH=arm64 go build -o bridge-darwin-arm64 ./cmd/bridge
GOOS=linux  GOARCH=amd64 go build -o bridge-linux-amd64  ./cmd/bridge
```

| OS | Notes |
|----|--------|
| **Arch / Linux** | Tray needs a StatusNotifier host (panel/dock). File picker: `zenity` recommended. |
| **macOS** | Tray in the menu bar; file picker via `osascript`. |

> On NTFS mounts (e.g. `/DATA` on dual-boot), run the binary from `~/bin` — Linux may deny `./bridge` exec on NTFS.

## Commands

| Command | Description |
|---------|-------------|
| `bridge` / `bridge menu` | interactive CLI |
| `bridge daemon` | background agent |
| `bridge tray` | system tray (starts daemon if needed) |
| `bridge relay` | optional relay server |
| `bridge status` | local device info |
| `bridge peers` | paired peers + online state |
| `bridge pair` / `pair <PIN>` | pairing (`--addr host:port`) |
| `bridge send <peer> <path>` | send file or folder |
| `bridge incoming` / `accept` / `reject` | incoming transfers |

## License

[MIT](./LICENSE)
