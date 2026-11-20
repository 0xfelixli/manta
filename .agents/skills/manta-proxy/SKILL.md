---
name: manta-proxy
description: >-
  Operate the local `manta` CLI proxy manager (powered by mihomo / Clash Meta) on macOS.
  Use this skill whenever an AI agent needs to start or stop the local proxy daemon,
  route shell commands (`curl`, `git`, `gh`, `brew`, etc.) through `127.0.0.1:7890`,
  inspect proxy groups, measure node latency, switch exit regions/nodes, change routing
  mode (`rule`/`global`/`direct`), manage Clash subscriptions, or toggle macOS system proxy.
---

# Manta Proxy — AI Agent Operating Runbook

`manta` is installed at `~/.local/bin/manta` (in `PATH`) and manages a background `mihomo` daemon exposing:
- **Mixed HTTP + SOCKS5 inbound**: `127.0.0.1:7890`
- **External Controller REST API**: `http://127.0.0.1:9090`
- **Config & logs directory**: `~/.config/manta/`

## Core Rules for AI Agents

1. **NEVER run `manta tui`** — it is an interactive full-screen TTY dashboard and will hang non-interactive shells. Always use non-interactive CLI subcommands below.
2. **Prefer per-command proxy env over global system changes** — when only a CLI command needs proxy access, pass proxy environment variables inline instead of changing macOS system proxy or starting TUN mode.
3. **Never leak subscription secrets** — `~/.config/manta/config.yaml` and `~/.config/manta/profiles/*.yaml` contain private subscription URLs and credentials. Never print their full contents or commit them to any repository.
4. **Always quote group and node names** — names frequently contain emojis, spaces, and `|` characters (e.g., `"🚀节点选择"`, `"🇯🇵AWS日本01 | 电信移动联通推荐"`).

---

## Quick Health Check

Run the bundled verification script before making network requests through the proxy:

```bash
bash scripts/check_proxy.sh
```

Or manually (use `--ip` to also verify exit IP, country/city, and ISP):

```bash
manta status --ip
curl -x http://127.0.0.1:7890 -sS --max-time 8 -o /dev/null -w "HTTP %{http_code} in %{time_total}s\n" https://www.gstatic.com/generate_204
```

Expected: `Status: running` and `HTTP 204`.

---

## Standard Workflows

### 1. Start or Stop the Proxy Daemon

```bash
manta up       # Start local HTTP/SOCKS5 proxy on 127.0.0.1:7890
manta status   # Confirm daemon PID, active profile, and mode
manta down     # Stop daemon
```

> On first launch with a new profile, `mihomo` may take 3–5 seconds to initialize GeoIP data before `127.0.0.1:9090` responds. If `manta node ls` returns `connection refused` immediately after `manta up`, wait a few seconds and retry or inspect `manta logs`.

### 2. Route a Command Through Manta (Recommended for AI)

For `curl`:
```bash
curl -x http://127.0.0.1:7890 https://example.com
```

For general CLI tools (`git`, `gh`, `npm`, `pip`, `go`, `brew`, etc.):
```bash
http_proxy=http://127.0.0.1:7890 https_proxy=http://127.0.0.1:7890 all_proxy=socks5://127.0.0.1:7890 <command>
```

Or inside a subshell:
```bash
eval "$(manta env)" && <command>
```

### 3. Inspect, Benchmark, and Switch Exit Nodes / Regions

1. List all proxy groups and their types:
   ```bash
   manta node ls
   ```
2. **Only `[Selector]` groups can be switched manually** (e.g., `🚀节点选择`, `🤖AI网站`, `🎬媒体解锁`, `GLOBAL`). Groups marked `[URLTest]` or `[Fallback]` (e.g., `♻️自动选择`, `🇯🇵日本节点`) auto-select by latency.
3. List nodes inside a group:
   ```bash
   manta node ls "🚀节点选择"
   ```
4. Measure real-time latency across all nodes in a group:
   ```bash
   manta node test "🚀节点选择"
   ```
5. Switch to a specific node (use the exact name from `node ls`):
   ```bash
   manta node use "🚀节点选择" "🇯🇵AWS日本01 | 电信移动联通推荐"
   ```
6. Re-verify connectivity after switching:
   ```bash
   curl -x http://127.0.0.1:7890 -sS --max-time 8 -o /dev/null -w "HTTP %{http_code} in %{time_total}s\n" https://www.gstatic.com/generate_204
   ```

### 4. Switch Routing Mode

```bash
manta mode          # Inspect current mode
manta mode rule     # Rule-based split routing (default)
manta mode global   # Route all proxy traffic through the GLOBAL selector
manta mode direct   # Bypass proxy (direct connection)
```

### 5. Manage Subscription Profiles

```bash
manta sub ls
manta sub add <name> "<clash-subscription-url>"
manta sub use <name>      # Switches active profile and restarts daemon if running
manta sub update [name]   # Refreshes profile YAML from its saved URL
```

### 6. System-Wide Traffic (Only When Explicitly Requested)

- **macOS system proxy** (affects browsers and macOS GUI apps):
  ```bash
  manta sysproxy status
  manta sysproxy on
  manta sysproxy off
  ```
- **Full-system TUN capture** (captures all apps + terminal traffic via `utun`, requires `sudo`):
  ```bash
  sudo manta up --tun
  sudo manta down
  ```
  Do not invoke `sudo manta up --tun` unless the user explicitly asks for TUN mode.

---

## Troubleshooting

| Symptom | Action |
| :--- | :--- |
| `manta status` shows `stopped` | Run `manta up`, then re-check `manta status`. |
| `connection refused` on `127.0.0.1:9090` right after `manta up` | Wait 3–5 seconds for `mihomo` startup/GeoIP load, then check `manta logs`. |
| Selected node times out (`HTTP 000` / curl timeout) | Run `manta node test "<group>"`, pick a node with `< 300ms` latency, and switch via `manta node use`. |
| `permission denied stopping pid ...` | Daemon was started with `sudo manta up --tun`; stop it with `sudo manta down`. |
| `manta sub add` fails with `HTTP 403` | The subscription token is expired/blocked by the provider; ask the user for a fresh Clash subscription URL. |
