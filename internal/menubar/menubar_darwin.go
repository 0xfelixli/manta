//go:build darwin

package menubar

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

const maxQuickNodes = 25

// Run starts the macOS menu bar status item (blocks on the Cocoa main thread).
func Run(cfg *config.AppConfig, client *api.Client) error {
	ctx, cancel := context.WithCancel(context.Background())

	onReady := func() {
		systray.SetTitle("● Manta")
		systray.SetTooltip("Manta Proxy Manager")

		// Status info items (disabled)
		mStatus := systray.AddMenuItem("Status: Connecting...", "")
		mStatus.Disable()
		mExitIP := systray.AddMenuItem("Exit IP: Checking...", "")
		mExitIP.Disable()
		mTun := systray.AddMenuItem("TUN: -", "")
		mTun.Disable()
		mSpeed := systray.AddMenuItem("Traffic: ↑ 0 B/s  ↓ 0 B/s", "")
		mSpeed.Disable()

		systray.AddSeparator()

		// Mode submenu
		mMode := systray.AddMenuItem("Routing Mode", "Switch mihomo routing mode")
		mModeRule := mMode.AddSubMenuItemCheckbox("Rule", "Rule-based split routing", true)
		mModeGlobal := mMode.AddSubMenuItemCheckbox("Global", "Route all traffic via GLOBAL", false)
		mModeDirect := mMode.AddSubMenuItemCheckbox("Direct", "Direct connection", false)

		// System proxy toggle
		mSysProxy := systray.AddMenuItemCheckbox("macOS System Proxy", "Toggle HTTP/HTTPS/SOCKS5 system proxy", false)

		// Quick switch nodes submenu (pre-allocated slots for clean Cocoa updates)
		mNodesMenu := systray.AddMenuItem("Quick Switch Node", "Switch active node in primary selector group")
		type nodeSlot struct {
			item *systray.MenuItem
			name string
		}
		slots := make([]*nodeSlot, maxQuickNodes)
		for i := 0; i < maxQuickNodes; i++ {
			it := mNodesMenu.AddSubMenuItemCheckbox("-", "", false)
			it.Hide()
			slots[i] = &nodeSlot{item: it}
		}

		systray.AddSeparator()

		mCheckIP := systray.AddMenuItem("Refresh Exit IP", "Re-query exit IP and geolocation")
		mOpenTUI := systray.AddMenuItem("Open Dashboard (TUI)", "Open manta tui in Terminal")
		mQuit := systray.AddMenuItem("Quit Menu Bar", "Close menu bar item (keeps proxy running)")

		var (
			mu           sync.Mutex
			online       bool
			countryCode  string
			primaryGroup string
			sysOn        bool
			curUp        int64
			curDown      int64
		)

		updateTitle := func() {
			mu.Lock()
			isOnline := online
			cc := countryCode
			down := curDown
			up := curUp
			mu.Unlock()

			if !isOnline {
				systray.SetTitle("○ Manta")
				return
			}
			// Show live speed when traffic >= 1 KB/s, otherwise show compact country/online badge.
			if down >= 1024 || up >= 1024 {
				if down >= up {
					systray.SetTitle("● ↓" + compactSpeed(down))
				} else {
					systray.SetTitle("● ↑" + compactSpeed(up))
				}
				return
			}
			if cc != "" {
				systray.SetTitle("● " + cc)
			} else {
				systray.SetTitle("● Manta")
			}
		}

		refreshExitIP := func() {
			mExitIP.SetTitle("Exit IP: Checking...")
			info, err := api.FetchExitIP(cfg.MixedPort)
			if err != nil {
				mExitIP.SetTitle("Exit IP: lookup failed")
				return
			}
			mu.Lock()
			countryCode = info.CountryCode
			mu.Unlock()
			loc := strings.TrimSpace(strings.Join([]string{info.CountryCode, info.City}, " "))
			mExitIP.SetTitle(fmt.Sprintf("Exit IP: %s (%s)", info.IP, loc))
			updateTitle()
		}

		refreshState := func() {
			ver, err := client.Version()
			if err != nil {
				mu.Lock()
				online = false
				mu.Unlock()
				mStatus.SetTitle("Status: Offline (run `manta up`)")
				mTun.SetTitle("TUN: -")
				updateTitle()
				return
			}

			liveCfg, err := client.GetConfigs()
			if err != nil {
				return
			}
			proxies, err := client.GetProxies()
			if err != nil {
				return
			}

			isSysOn := detectSysProxy()

			mu.Lock()
			online = true
			sysOn = isSysOn
			mu.Unlock()

			mStatus.SetTitle(fmt.Sprintf("Status: Online • mihomo %s", ver.Version))
			if liveCfg.Tun.Enable {
				stack := liveCfg.Tun.Stack
				if stack == "" {
					stack = "mixed"
				}
				mTun.SetTitle(fmt.Sprintf("TUN: ON (utun • %s)", stack))
			} else {
				mTun.SetTitle("TUN: OFF")
			}

			// Mode checkboxes
			mode := strings.ToLower(liveCfg.Mode)
			setCheck(mModeRule, mode == "rule")
			setCheck(mModeGlobal, mode == "global")
			setCheck(mModeDirect, mode == "direct")
			mMode.SetTitle(fmt.Sprintf("Routing Mode (%s)", strings.ToUpper(mode)))

			// System proxy checkbox
			setCheck(mSysProxy, isSysOn)

			// Find primary selector group
			grpName, grp := pickPrimaryGroup(proxies)
			mu.Lock()
			primaryGroup = grpName
			mu.Unlock()

			if grpName != "" {
				mNodesMenu.SetTitle(fmt.Sprintf("%s (%s)", grpName, grp.Now))
				for i := 0; i < maxQuickNodes; i++ {
					if i < len(grp.All) {
						nodeName := grp.All[i]
						mu.Lock()
						slots[i].name = nodeName
						mu.Unlock()
						slots[i].item.SetTitle(nodeName)
						setCheck(slots[i].item, nodeName == grp.Now)
						slots[i].item.Show()
					} else {
						slots[i].item.Hide()
					}
				}
			}

			updateTitle()
		}

		// Initial load
		go func() {
			refreshState()
			refreshExitIP()
		}()

		// Periodic state refresh (every 5s)
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refreshState()
				}
			}
		}()

		// Live traffic stream
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				ch, err := client.StreamTraffic(ctx)
				if err != nil {
					time.Sleep(3 * time.Second)
					continue
				}
				for sample := range ch {
					mu.Lock()
					curUp = sample.Up
					curDown = sample.Down
					mu.Unlock()
					mSpeed.SetTitle(fmt.Sprintf("Traffic: ↑ %s/s  ↓ %s/s", formatBytes(sample.Up), formatBytes(sample.Down)))
					updateTitle()
				}
				time.Sleep(2 * time.Second)
			}
		}()

		// Node slot click handlers
		for i := 0; i < maxQuickNodes; i++ {
			slot := slots[i]
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-slot.item.ClickedCh:
						mu.Lock()
						g := primaryGroup
						n := slot.name
						mu.Unlock()
						if g != "" && n != "" {
							_ = client.SelectProxy(g, n)
							refreshState()
							go refreshExitIP()
						}
					}
				}
			}()
		}

		// Menu action handlers
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-mModeRule.ClickedCh:
					_ = client.SetMode("rule")
					refreshState()
				case <-mModeGlobal.ClickedCh:
					_ = client.SetMode("global")
					refreshState()
				case <-mModeDirect.ClickedCh:
					_ = client.SetMode("direct")
					refreshState()
				case <-mSysProxy.ClickedCh:
					mu.Lock()
					wantOn := !sysOn
					mu.Unlock()
					if services, err := sysproxy.ResolveServices(""); err == nil {
						if wantOn {
							_ = sysproxy.Enable("127.0.0.1", cfg.MixedPort, services)
						} else {
							_ = sysproxy.Disable(services)
						}
					}
					refreshState()
				case <-mCheckIP.ClickedCh:
					go refreshExitIP()
				case <-mOpenTUI.ClickedCh:
					openTUIInTerminal()
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}

	onExit := func() {
		cancel()
	}

	systray.Run(onReady, onExit)
	return nil
}

func setCheck(item *systray.MenuItem, checked bool) {
	if checked {
		item.Check()
	} else {
		item.Uncheck()
	}
}

func detectSysProxy() bool {
	services, err := sysproxy.ResolveServices("")
	if err != nil || len(services) == 0 {
		return false
	}
	statuses, err := sysproxy.Status(services)
	if err != nil {
		return false
	}
	for _, st := range statuses {
		if strings.HasPrefix(st.HTTP, "on ") || strings.HasPrefix(st.SOCKS, "on ") {
			return true
		}
	}
	return false
}

func pickPrimaryGroup(proxies map[string]api.ProxyInfo) (string, api.ProxyInfo) {
	var selectors []string
	for name, p := range proxies {
		if p.Type == "Selector" {
			selectors = append(selectors, name)
		}
	}
	sort.Strings(selectors)
	for _, name := range selectors {
		if strings.Contains(name, "节点选择") || strings.EqualFold(name, "PROXY") || strings.EqualFold(name, "Proxy") {
			return name, proxies[name]
		}
	}
	for _, name := range selectors {
		if name != "GLOBAL" {
			return name, proxies[name]
		}
	}
	if len(selectors) > 0 {
		return selectors[0], proxies[selectors[0]]
	}
	return "", api.ProxyInfo{}
}

func openTUIInTerminal() {
	exe, err := os.Executable()
	if err != nil {
		exe = "manta"
	}
	script := fmt.Sprintf(`tell application "Terminal"
activate
do script "%s tui"
end tell`, exe)
	_ = exec.Command("osascript", "-e", script).Start()
}

func compactSpeed(bps int64) string {
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(bps)/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(bps)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bps)
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
