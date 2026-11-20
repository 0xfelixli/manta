# manta

A sleek Go CLI, TUI & macOS menu bar proxy manager powered by `mihomo` (Clash Meta) — designed for both human power users and AI agents.

## Features

- **Lifecycle management**: `manta init`, `manta up`, `manta down`, `manta status [--ip]`, `manta logs`
- **Subscription profiles**: `manta sub add / ls / use / update` (auto-merges local ports & controller settings)
- **Full-system TUN capture**: `sudo manta up --tun` captures all browser, desktop app, and terminal traffic via `utun` + Fake-IP DNS
- **macOS system proxy**: `manta sysproxy on / off / status` (or `manta up --sys` / `manta down --sys`)
- **Shell proxy env**: `eval "$(manta env)"` / `eval "$(manta env --unset)"`
- **Interactive TUI (`manta tui`)**:
  - **Dashboard tab**: inline interactive controls (`Daemon`, `RULE/GLOBAL/DIRECT` segmented switch, `TUN`, `SysProxy`, `Allow LAN`, `Update Sub`), live Exit IP & geolocation, speed sparkline, clickable Proxy Groups overview, and real-time Connections
  - **Proxies tab**: mouse click & scroll, fuzzy search (`/`), one-click sort by speed (`o`), concurrent latency testing (`t`/`T`)
- **Native macOS menu bar (`manta menubar -d`)**: live speed / exit country badge in the top-right menu bar with quick mode, SysProxy, and node switching
- **Built-in AI Agent Skill**: `.agents/skills/manta-proxy/` runbook and health-check script so AI agents can operate `manta` non-interactively

## Install

### Homebrew (macOS — Recommended, true single-binary with embedded `mihomo`)

```bash
brew install 0xfelixli/tap/manta
```

### Go Install

```bash
go install github.com/0xfelixli/manta/cmd/manta@latest
```

### Build from Source

```bash
brew install go
go mod tidy
CGO_ENABLED=1 go build -o manta ./cmd/manta
mkdir -p ~/.local/bin && rm -f ~/.local/bin/manta && cp manta ~/.local/bin/manta
```

## Quick Start

```bash
# 1. Initialize ~/.config/manta/
manta init

# 2. Add a Clash YAML subscription
manta sub add main "https://your-subscription-url"

# 3. Start proxy (choose one mode)
sudo manta up --tun    # Full-system TUN capture (browsers + apps + terminal)
# OR
manta up --sys         # Local HTTP/SOCKS5 (127.0.0.1:7890) + macOS system proxy

# 4. Check status & exit IP
manta status --ip

# 5. Open interactive Dashboard / TUI, or dock to macOS menu bar
manta tui
manta menubar -d

# 6. Stop proxy
sudo manta down        # (or `manta down --sys`)
```
