# manta

A sleek Go CLI & TUI proxy manager powered by `mihomo` (Clash Meta) — supporting local HTTP/SOCKS5 inbound, macOS system proxy, full-system TUN capture, and a mouse-enabled terminal dashboard.

## Features

- **Lifecycle management**: `manta init`, `manta up`, `manta down`, `manta status`, `manta logs`
- **Subscription profiles**: `manta sub add / ls / use / update` (auto-merges local ports & controller settings)
- **Full-system TUN capture**: `sudo manta up --tun` captures all browser, desktop app, and terminal traffic via `utun` + Fake-IP DNS
- **macOS system proxy**: `manta sysproxy on / off / status` (or `manta up --sys` / `manta down --sys`)
- **Shell proxy env**: `eval "$(manta env)"` / `eval "$(manta env --unset)"`
- **Interactive TUI (`manta tui`)**: live traffic speeds, concurrent latency testing, keyboard & mouse click/scroll support

## Prerequisites

```bash
brew install go mihomo
```

## Build & Install

```bash
go mod tidy
go build -o manta ./cmd/manta
mkdir -p ~/.local/bin && cp manta ~/.local/bin/manta
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

# 4. Open interactive TUI (click/scroll or keyboard)
manta tui

# 5. Stop proxy
sudo manta down        # (or `manta down --sys`)
```
