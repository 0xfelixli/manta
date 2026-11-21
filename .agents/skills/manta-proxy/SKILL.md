---
name: manta-proxy
description: >-
  Operate the local `manta` CLI proxy manager (powered by embedded mihomo / Clash Meta) on macOS.
  Use this skill whenever an AI agent needs to start or stop the local proxy daemon,
  route shell commands (`curl`, `git`, `gh`, `brew`, etc.) through `127.0.0.1:7890`,
  inspect proxy groups, measure node latency, automatically switch to the fastest node
  (`manta node best`), add custom routing rules (`manta rule`), close stale connections,
  change routing mode (`rule`/`global`/`direct`), manage Clash subscriptions, or toggle macOS system proxy.
---

# Manta Proxy — AI Agent Operating Runbook

`manta` is a single-binary proxy manager installed in `PATH` with an embedded `mihomo` engine exposing:
- **Mixed HTTP + SOCKS5 inbound**: `127.0.0.1:7890`
- **External Controller REST API**: `http://127.0.0.1:9090`
- **Config & logs directory**: `~/.config/manta/`

## Core Rules for AI Agents

1. **NEVER run `manta tui`** — it is an interactive full-screen TTY dashboard. Always use non-interactive CLI subcommands (pass `--json` for structured output).
2. **Use `manta node best` for one-step failover** — when a node is slow or blocked, run `manta node best --region JP --json` (or `SG`, `US`, `HK`, `TW`) to benchmark all candidate nodes, switch to the fastest one, and close stale connections in a single command.
3. **Never leak subscription secrets** — `~/.config/manta/config.yaml` and `~/.config/manta/profiles/*.yaml` contain private subscription URLs. Never print their URLs or commit them to any repository.
4. **Always quote group and node names** — names frequently contain emojis, spaces, and `|` characters.

---

## Quick Health Check (JSON or Human)

```bash
manta status --ip --json
curl -x http://127.0.0.1:7890 -sS --max-time 8 -o /dev/null -w "HTTP %{http_code} in %{time_total}s\n" https://www.gstatic.com/generate_204
```

Or run the bundled script:
```bash
bash scripts/check_proxy.sh
```

---

## Standard AI Workflows

### 1. One-Command Smart Node Selection (`manta node best`)

Automatically benchmark all nodes in the primary selector group, switch to the lowest-latency alive node, and close stale connections:

```bash
manta node best --json
manta node best --region JP --json
manta node best "🤖AI网站" --region US --json
```

### 2. Route a Single Command Through Manta

```bash
curl -x http://127.0.0.1:7890 https://example.com
http_proxy=http://127.0.0.1:7890 https_proxy=http://127.0.0.1:7890 all_proxy=socks5://127.0.0.1:7890 <command>
```

### 3. Inspect, Benchmark, and Switch Exit Nodes Manually

```bash
manta node ls --json
manta node test "🚀节点选择" --json
manta node use "🚀节点选择" "🇯🇵AWS日本01 | 电信移动联通推荐"
manta conn close-all   # Force active sockets onto the new node immediately
```

### 4. Custom Routing Rule Overrides (`manta rule`)

Add persistent high-priority rules that survive subscription updates:

```bash
manta rule ls
manta rule add "DOMAIN-SUFFIX,mycompany.com,DIRECT"
manta rule add "DOMAIN-KEYWORD,anthropic,🤖AI网站"
manta rule rm 1
```

### 5. Lifecycle, Modes & Subscriptions

```bash
manta up               # Start local HTTP/SOCKS5 proxy on 127.0.0.1:7890
manta up --tun         # Start with full-system TUN capture (prompts macOS Touch ID / password)
manta down             # Power off daemon, disable macOS system proxy, and close menu bar
manta mode rule        # Switch between rule, global, and direct
manta sub ls           # View profiles, traffic quota, and expiration dates
manta sub update       # Refresh active subscription and quota metadata
```
