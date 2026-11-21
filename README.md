<div align="center">

```text
      __  __    _    _   _ _____  _    
     |  \/  |  / \  | \ | |_   _|/ \   
     | |\/| | / _ \ |  \| | | | / _ \  
     | |  | |/ ___ \| |\  | | |/ ___ \ 
     |_|  |_/_/   \_\_| \_| |_/_/   \_\
```

**A sleek, single-binary macOS CLI, TUI & Menu Bar proxy manager with an embedded `mihomo` (Clash Meta) engine.**  
Designed for both human power users and autonomous AI agents.

[![Release](https://img.shields.io/github/v/release/0xfelixli/manta?style=flat-square&color=0d9488)](https://github.com/0xfelixli/manta/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-0xfelixli%2Ftap%2Fmanta-2563eb?style=flat-square)](https://github.com/0xfelixli/homebrew-tap)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8?style=flat-square)](https://github.com/0xfelixli/manta)
[![Platform](https://img.shields.io/badge/platform-macOS%20(Apple%20Silicon%20%2F%20Intel)-18181b?style=flat-square)](https://github.com/0xfelixli/manta)

</div>

---

## Highlights

- **True Single Binary**: Embeds `mihomo v1.19.34` (with `gVisor` / `mixed` / `system` TUN stacks) directly inside `manta`. Zero external runtime dependencies.
- **Oceanic Mineral TUI (`manta tui`)**:
  - **`1 Dashboard`**: Inline clickable switches (`Daemon`, `RULE / GLOBAL / DIRECT`, `TUN Capture` with native macOS Touch ID / password prompt, `SysProxy`, `Allow LAN`), subscription traffic quota & expiration (`Subscription-Userinfo`), live Exit IP & geolocation, download sparkline with peak rate, clickable Proxy Groups overview, and real-time active connections with `[Close All]` (`x`).
  - **`2 Proxies`**: Split-pane group & node manager with mouse click/scroll, fuzzy search (`/`), speed sorting (`o`), and 3-bar latency signal indicators (`▪▪▪ 78ms`).
  - **`3 Settings`**: Persistent preferences (`~/.config/manta/config.yaml`) for default sort order, startup tab, auto-test on open, and macOS `Login Start` (`LaunchAgent`).
- **Native macOS Menu Bar (`manta menubar -d`)**: Custom Manta Ray template icon in the top-right macOS menu bar with live bandwidth / country code and quick controls.
- **Custom Routing Rules & Connection Control**: Prepend persistent high-priority rules (`manta rule add / ls / rm`) ahead of subscription rules with instant hot-reload, or sever active connections & flush Fake-IP cache (`manta conn close-all`).
- **AI Agent First**: Bundles an Antigravity / Claude skill (`.agents/skills/manta-proxy/`), structured `--json` outputs, one-shot smart failover (`manta node best --region JP --json`), and dynamic Fish / Zsh / Bash completions.

---

## Install

### Homebrew (Recommended — 2-second prebuilt binary install)

```bash
brew install 0xfelixli/tap/manta
```

### Build from Source

```bash
brew install go
CGO_ENABLED=1 go build -tags with_gvisor -o manta ./cmd/manta
mkdir -p ~/.local/bin && rm -f ~/.local/bin/manta && cp manta ~/.local/bin/manta
```

---

## Quick Start

```bash
# 1. Initialize ~/.config/manta/ and auto-install shell completions
manta init

# 2. Add a Clash YAML subscription
manta sub add main "https://your-clash-subscription-url"

# 3. Start proxy (with full-system TUN capture or macOS system proxy)
manta up --tun         # Native macOS Touch ID / password prompt -> full-system utun capture
# OR
manta up --sys         # Local HTTP/SOCKS5 (127.0.0.1:7890) + macOS system proxy

# 4. Inspect live status & exit IP
manta status --ip

# 5. Launch interactive TUI or macOS menu bar icon
manta tui
manta menubar -d

# 6. Power off everything cleanly (daemon + system proxy + menu bar)
manta down
```

---

## CLI Reference & TUI Shortcuts

| Command | Description |
| :--- | :--- |
| `manta up [--tun \| --sys]` | Start embedded proxy daemon (optionally with TUN or macOS system proxy) |
| `manta down` | Power off daemon, disable macOS system proxy, and close menu bar |
| `manta status [--ip] [--json]` | Show daemon state, TUN/SysProxy status, subscription quota, and live Exit IP |
| `manta tui` | Open the 3-tab interactive terminal dashboard (`Dashboard`, `Proxies`, `Settings`) |
| `manta menubar [-d]` | Start the native macOS menu bar icon (`-d` runs detached in background) |
| `manta sub add / ls / use / update` | Manage Clash YAML subscriptions and inspect traffic quota & expiration |
| `manta node ls / test / use` | Inspect groups, benchmark node latency (`--json`), and switch nodes |
| `manta node best [group] [--region CC]` | Auto-benchmark and switch to the lowest-latency healthy node (`--json`) |
| `manta rule ls / add / rm` | Manage persistent high-priority custom routing rules (`DOMAIN-SUFFIX,...`) |
| `manta conn ls / close-all` | List active connections (`--json`) or close all connections & flush Fake-IP |
| `manta autostart [on\|off\|status]` | Manage macOS Launch at Login (`~/Library/LaunchAgents`) |
| `manta mode [rule\|global\|direct]` | Get or switch active routing mode |
| `manta sysproxy [on\|off\|status]` | Toggle macOS system HTTP/HTTPS/SOCKS5 proxy |
| `manta config [set <key> <val>]` | Inspect or persist user preferences (`sort_by_speed`, `mode`, `last_tab`, etc.) |
| `manta completion [fish\|zsh\|bash]` | Auto-install shell completions with dynamic group/node/profile Tab completion |
