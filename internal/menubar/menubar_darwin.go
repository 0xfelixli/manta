//go:build darwin

package menubar

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/autostart"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/core"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

const (
	maxPrimaryNodes   = 60
	maxExtraGroups    = 8
	maxExtraGroupNode = 45
	burstSpeedFloor   = 32 * 1024 // Only show live speed in top menu bar when >= 32 KB/s to prevent menu bar jitter
)

// mantaPointInBody evaluates whether normalized coordinates (x, y in [0, 22]) lie inside
// the Manta Ray silhouette. Used with 4x4 supersampling for crisp Retina anti-aliasing.
func mantaPointInBody(fx, fy float64) bool {
	const cx = 11.0
	dx := math.Abs(fx - cx)

	switch {
	case fy >= 2.6 && fy <= 5.2:
		// Cephalic horns (two graceful front fins with a smooth central head cove)
		t := (fy - 2.6) / 2.6
		inner := 1.55 - t*1.15
		outer := 2.45 + t*1.15
		return dx >= inner && dx <= outer

	case fy > 5.2 && fy <= 11.2:
		// Sweeping forward wingspan with slight organic curvature
		t := (fy - 5.2) / 6.0
		curve := math.Pow(t, 0.88)
		maxW := 3.1 + curve*7.4
		return dx <= maxW

	case fy > 11.2 && fy <= 15.4:
		// Scalloped trailing edge tapering into the tail base
		t := (fy - 11.2) / 4.2
		maxW := 10.5 * math.Pow(1.0-t, 1.25)
		if maxW < 0.75 {
			maxW = 0.75
		}
		return dx <= maxW

	case fy > 15.4 && fy <= 19.6:
		// Slender tapering whip tail
		t := (fy - 15.4) / 4.2
		maxW := 0.72 * (1.0 - t*0.55)
		return dx <= maxW
	}
	return false
}

// mantaTemplateIconPNG renders a 44x44 (@2x Retina) 4x4-supersampled anti-aliased Manta Ray
// template icon for macOS NSStatusBar (automatically adapts to light/dark menu bars).
func mantaTemplateIconPNG() []byte {
	const size = 44
	const samples = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hits := 0
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					fx := (float64(x) + (float64(sx)+0.5)/float64(samples)) * (22.0 / float64(size))
					fy := (float64(y) + (float64(sy)+0.5)/float64(samples)) * (22.0 / float64(size))
					if mantaPointInBody(fx, fy) {
						hits++
					}
				}
			}
			if hits > 0 {
				alpha := uint8(hits * 245 / (samples * samples))
				img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: alpha})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// statusDotPNG renders a crisp anti-aliased colored status indicator circle for menu items.
func statusDotPNG(r, g, b uint8) []byte {
	const size = 14
	const samples = 4
	const radius = 4.6
	cx, cy := float64(size)/2.0, float64(size)/2.0
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hits := 0
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					fx := float64(x) + (float64(sx)+0.5)/float64(samples)
					fy := float64(y) + (float64(sy)+0.5)/float64(samples)
					dist := math.Hypot(fx-cx, fy-cy)
					if dist <= radius {
						hits++
					}
				}
			}
			if hits > 0 {
				alpha := uint8(hits * 255 / (samples * samples))
				img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: alpha})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

type nodeSlot struct {
	item *systray.MenuItem
	name string
}

type extraGroupSlot struct {
	menu      *systray.MenuItem
	groupName string
	nodes     []*nodeSlot
}

// Run starts the macOS menu bar status item (blocks on the Cocoa main thread).
func Run(cfg *config.AppConfig, mgr *core.Manager, client *api.Client) error {
	ctx, cancel := context.WithCancel(context.Background())

	onReady := func() {
		if icon := mantaTemplateIconPNG(); len(icon) > 0 {
			systray.SetTemplateIcon(icon, icon)
		}
		systray.SetTitle("")
		systray.SetTooltip("Manta Proxy & VPN Manager")

		dotOnline := statusDotPNG(16, 185, 129)  // Emerald teal (#10B981)
		dotBusy := statusDotPNG(245, 158, 11)    // Warm amber (#F59E0B)
		dotOffline := statusDotPNG(244, 63, 94)  // Coral red (#F43F5E)

		// ── Section 1: Live Status & Telemetry Header ──────────────────────
		mStatus := systray.AddMenuItem("Manta Connecting...", "Click to start proxy (when offline) or open Dashboard (when online)")
		if len(dotBusy) > 0 {
			mStatus.SetIcon(dotBusy)
		}
		mExitIP := systray.AddMenuItem("Exit IP:  Checking...", "Click to copy Exit IP to Clipboard & refresh geolocation")
		mSpeed := systray.AddMenuItem("Traffic:  ↓ 0 B/s   ↑ 0 B/s", "Real-time proxy throughput")
		mSpeed.Disable()
		mQuota := systray.AddMenuItem("Quota:   -", "Active subscription traffic usage & expiration")
		mQuota.Disable()
		mQuota.Hide()

		systray.AddSeparator()

		// ── Section 2: Core Engine & Capture Toggles (1-Click Checkboxes) ──
		mTun := systray.AddMenuItemCheckbox("TUN Capture  (Full-System VPN)", "Capture all macOS app & terminal traffic via utun virtual interface", false)
		mSysProxy := systray.AddMenuItemCheckbox(fmt.Sprintf("macOS System Proxy  (127.0.0.1:%d)", cfg.MixedPort), "Configure macOS HTTP/HTTPS/SOCKS5 system proxy", false)
		mAllowLan := systray.AddMenuItemCheckbox(fmt.Sprintf("Allow LAN Sharing  (:%d)", cfg.MixedPort), "Allow other devices on your local network to use Manta", cfg.AllowLan)

		systray.AddSeparator()

		// ── Section 3: Routing Mode ────────────────────────────────────────
		mMode := systray.AddMenuItem("Routing Mode:  Rule", "Switch mihomo traffic routing mode")
		mModeRule := mMode.AddSubMenuItemCheckbox("Rule     — Smart Split Routing (CN Direct / Global Proxy)", "Route domestic traffic directly and overseas traffic via proxy", true)
		mModeGlobal := mMode.AddSubMenuItemCheckbox("Global   — Route All Traffic Through Proxy", "Force all connections through the GLOBAL proxy selector", false)
		mModeDirect := mMode.AddSubMenuItemCheckbox("Direct   — Direct Connection (Bypass Proxy)", "Bypass proxy for all connections", false)

		systray.AddSeparator()

		// ── Section 4: Smart Node Selection & Multi-Group Policy Switcher ──
		mBestNode := systray.AddMenuItem("⚡ Auto-Select Fastest Node", "Benchmark all nodes in the primary group and switch to the lowest-latency node")
		mPrimaryMenu := systray.AddMenuItem("Primary Group", "Switch active proxy node in primary group")
		primarySlots := make([]*nodeSlot, maxPrimaryNodes)
		for i := 0; i < maxPrimaryNodes; i++ {
			it := mPrimaryMenu.AddSubMenuItemCheckbox("-", "", false)
			it.Hide()
			primarySlots[i] = &nodeSlot{item: it}
		}

		extraGroups := make([]*extraGroupSlot, maxExtraGroups)
		for gIdx := 0; gIdx < maxExtraGroups; gIdx++ {
			gMenu := systray.AddMenuItem("-", "Switch active policy for this group")
			gMenu.Hide()
			gSlots := make([]*nodeSlot, maxExtraGroupNode)
			for nIdx := 0; nIdx < maxExtraGroupNode; nIdx++ {
				it := gMenu.AddSubMenuItemCheckbox("-", "", false)
				it.Hide()
				gSlots[nIdx] = &nodeSlot{item: it}
			}
			extraGroups[gIdx] = &extraGroupSlot{
				menu:  gMenu,
				nodes: gSlots,
			}
		}

		systray.AddSeparator()

		// ── Section 5: Network & Developer Tools ───────────────────────────
		mCopyEnv := systray.AddMenuItem("Copy Terminal Proxy Command", "Copy `export https_proxy=...` to macOS Clipboard")
		mFlush := systray.AddMenuItem("Close Active Connections & Flush DNS", "Terminate active connections and clear Fake-IP DNS cache")
		mUpdateSub := systray.AddMenuItem("Update Subscription", "Refresh active subscription profile and traffic quota")

		systray.AddSeparator()

		// ── Section 6: Dashboard, Preferences & Power ──────────────────────
		mOpenTUI := systray.AddMenuItem("Open Terminal Dashboard  (manta tui)", "Launch interactive terminal dashboard")
		mPrefs := systray.AddMenuItem("Preferences", "Configure Manta menu bar and startup behavior")
		mPrefSortSpeed := mPrefs.AddSubMenuItemCheckbox("Sort Nodes by Latency", "Sort nodes from lowest to highest latency", cfg.SortBySpeed)
		mPrefAutostart := mPrefs.AddSubMenuItemCheckbox("Launch Manta at Login", "Automatically start Manta when you log in to macOS", autostart.IsEnabled())
		mPrefShowSpeed := mPrefs.AddSubMenuItemCheckbox("Show Burst Speed in Menu Bar", "Display live download/upload speed in the top bar during active transfers (>= 32 KB/s)", true)

		systray.AddSeparator()

		mPowerOff := systray.AddMenuItem("⏻ Power Off All  (Stop Proxy & Quit)", "Stop proxy daemon, disable system proxy, and exit menu bar")
		mQuit := systray.AddMenuItem("Close Menu Bar Icon  (Keep Proxy Running)", "Hide menu bar icon while keeping the proxy daemon active")

		var (
			mu             sync.Mutex
			online         bool
			tunOn          bool
			sysOn          bool
			allowLan       = cfg.AllowLan
			sortBySpeed    = cfg.SortBySpeed
			showBurstSpeed = true
			countryCode    string
			exitIPStr      string
			primaryGroup   string
			curUp          int64
			curDown        int64
			lastBarTitle   = "__init__"
			lastSpeedText  string
			lastStatusText string
			lastStatusIcon int // 0=init, 1=online, 2=busy, 3=offline
		)

		setBarTitle := func(title string) {
			mu.Lock()
			if title == lastBarTitle {
				mu.Unlock()
				return
			}
			lastBarTitle = title
			mu.Unlock()
			systray.SetTitle(title)
		}

		setStatusAppearance := func(title string, iconKind int) {
			mu.Lock()
			titleChanged := title != lastStatusText
			iconChanged := iconKind != lastStatusIcon
			lastStatusText = title
			lastStatusIcon = iconKind
			mu.Unlock()

			if titleChanged {
				mStatus.SetTitle(title)
			}
			if iconChanged {
				switch iconKind {
				case 1:
					if len(dotOnline) > 0 {
						mStatus.SetIcon(dotOnline)
					}
				case 2:
					if len(dotBusy) > 0 {
						mStatus.SetIcon(dotBusy)
					}
				case 3:
					if len(dotOffline) > 0 {
						mStatus.SetIcon(dotOffline)
					}
				}
			}
		}

		updateTitle := func() {
			mu.Lock()
			isOnline := online
			isTun := tunOn
			cc := countryCode
			down := curDown
			up := curUp
			wantSpeed := showBurstSpeed
			mu.Unlock()

			if !isOnline {
				setBarTitle("OFF")
				return
			}
			// Show live speed only during active transfers (>= 32 KB/s) so idle keep-alive
			// packets never cause the macOS menu bar items to jitter horizontally.
			if wantSpeed && (down >= burstSpeedFloor || up >= burstSpeedFloor) {
				if down >= up {
					setBarTitle("↓" + compactSpeed(down))
				} else {
					setBarTitle("↑" + compactSpeed(up))
				}
				return
			}
			if cc != "" {
				setBarTitle(cc)
			} else if isTun {
				setBarTitle("TUN")
			} else {
				setBarTitle("")
			}
		}

		refreshExitIP := func() {
			mExitIP.SetTitle("Exit IP:  Checking...")
			info, err := api.FetchExitIP(cfg.MixedPort)
			if err != nil {
				mExitIP.SetTitle("Exit IP:  Lookup failed (click to retry)")
				return
			}
			mu.Lock()
			countryCode = info.CountryCode
			exitIPStr = info.IP
			mu.Unlock()
			locParts := []string{}
			if info.CountryCode != "" {
				locParts = append(locParts, info.CountryCode)
			}
			if info.City != "" {
				locParts = append(locParts, info.City)
			}
			loc := strings.Join(locParts, " • ")
			if loc != "" {
				mExitIP.SetTitle(fmt.Sprintf("Exit IP:  %s  (%s)", info.IP, loc))
			} else {
				mExitIP.SetTitle(fmt.Sprintf("Exit IP:  %s", info.IP))
			}
			updateTitle()
		}

		refreshQuotaDisplay := func() {
			if mgr != nil {
				if latestCfg, err := config.Load(mgr.Paths); err == nil {
					cfg = latestCfg
				}
			}
			prof := cfg.ActiveProfile
			if prof == "" {
				prof = "default"
			}
			mUpdateSub.SetTitle(fmt.Sprintf("Update Subscription  (%s)", prof))
			if meta, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
				if q := meta.QuotaSummary(); q != "" {
					mQuota.SetTitle(fmt.Sprintf("Quota:   %s  (%s)", q, prof))
					mQuota.Show()
					return
				}
			}
			mQuota.Hide()
		}

		refreshState := func() {
			refreshQuotaDisplay()

			_, err := client.Version()
			if err != nil {
				mu.Lock()
				online = false
				tunOn = false
				mu.Unlock()
				setStatusAppearance("Manta Offline  —  Click to Start Proxy", 3)
				setCheck(mTun, false)
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

			isSysOn := sysproxy.FastIsEnabled()
			modeUpper := strings.ToUpper(liveCfg.Mode)
			if modeUpper == "" {
				modeUpper = "RULE"
			}

			mu.Lock()
			online = true
			tunOn = liveCfg.Tun.Enable
			sysOn = isSysOn
			allowLan = liveCfg.AllowLan
			sortFast := sortBySpeed
			mu.Unlock()

			captureBadge := "Proxy :" + fmt.Sprintf("%d", cfg.MixedPort)
			if liveCfg.Tun.Enable && isSysOn {
				captureBadge = "TUN + SysProxy"
			} else if liveCfg.Tun.Enable {
				stack := liveCfg.Tun.Stack
				if stack == "" {
					stack = "Mixed"
				}
				captureBadge = "TUN " + stack
			} else if isSysOn {
				captureBadge = "SysProxy"
			}
			setStatusAppearance(fmt.Sprintf("Manta Online  •  %s  (%s)", modeUpper, captureBadge), 1)

			// Core capture checkboxes
			setCheck(mTun, liveCfg.Tun.Enable)
			if liveCfg.Tun.Enable {
				stack := liveCfg.Tun.Stack
				if stack == "" {
					stack = "mixed"
				}
				mTun.SetTitle(fmt.Sprintf("TUN Capture  (utun • %s)", stack))
			} else {
				mTun.SetTitle("TUN Capture  (Full-System VPN)")
			}
			setCheck(mSysProxy, isSysOn)
			setCheck(mAllowLan, liveCfg.AllowLan)

			// Routing mode checkboxes
			mode := strings.ToLower(liveCfg.Mode)
			setCheck(mModeRule, mode == "rule")
			setCheck(mModeGlobal, mode == "global")
			setCheck(mModeDirect, mode == "direct")
			mMode.SetTitle(fmt.Sprintf("Routing Mode:  %s", titleCaseMode(mode)))

			// Populate Primary Selector Group + Secondary Selector Groups
			primaryName, primaryInfo, others := classifyGroups(proxies)
			mu.Lock()
			primaryGroup = primaryName
			mu.Unlock()

			if primaryName != "" {
				nowDelay := latestDelay(proxies, primaryInfo.Now)
				nowTag := cleanMenuText(primaryInfo.Now)
				if nowDelay > 0 {
					nowTag = fmt.Sprintf("%s • %dms", nowTag, nowDelay)
				}
				mPrimaryMenu.SetTitle(fmt.Sprintf("%s:  %s", cleanMenuText(primaryName), nowTag))
				mPrimaryMenu.Show()

				orderedNodes := orderGroupNodes(primaryInfo.All, proxies, sortFast)
				for i := 0; i < maxPrimaryNodes; i++ {
					if i < len(orderedNodes) {
						nodeName := orderedNodes[i]
						mu.Lock()
						primarySlots[i].name = nodeName
						mu.Unlock()
						primarySlots[i].item.SetTitle(formatNodeMenuLabel(nodeName, proxies))
						setCheck(primarySlots[i].item, nodeName == primaryInfo.Now)
						primarySlots[i].item.Show()
					} else {
						primarySlots[i].item.Hide()
					}
				}
			} else {
				mPrimaryMenu.Hide()
			}

			// Populate secondary selector groups (AI网站, 媒体解锁, 日本节点, etc.)
			for gIdx := 0; gIdx < maxExtraGroups; gIdx++ {
				eg := extraGroups[gIdx]
				if gIdx < len(others) {
					gName := others[gIdx]
					gInfo := proxies[gName]
					mu.Lock()
					eg.groupName = gName
					mu.Unlock()

					nowDelay := latestDelay(proxies, gInfo.Now)
					nowTag := cleanMenuText(gInfo.Now)
					if nowDelay > 0 {
						nowTag = fmt.Sprintf("%s • %dms", nowTag, nowDelay)
					}
					eg.menu.SetTitle(fmt.Sprintf("%s:  %s", cleanMenuText(gName), nowTag))
					eg.menu.Show()

					orderedNodes := orderGroupNodes(gInfo.All, proxies, sortFast)
					for nIdx := 0; nIdx < maxExtraGroupNode; nIdx++ {
						if nIdx < len(orderedNodes) {
							nodeName := orderedNodes[nIdx]
							mu.Lock()
							eg.nodes[nIdx].name = nodeName
							mu.Unlock()
							eg.nodes[nIdx].item.SetTitle(formatNodeMenuLabel(nodeName, proxies))
							setCheck(eg.nodes[nIdx].item, nodeName == gInfo.Now)
							eg.nodes[nIdx].item.Show()
						} else {
							eg.nodes[nIdx].item.Hide()
						}
					}
				} else {
					eg.menu.Hide()
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

		// Live traffic stream (zero Cocoa calls when idle)
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
					speedText := fmt.Sprintf("Traffic:  ↓ %s/s   ↑ %s/s", formatBytes(sample.Down), formatBytes(sample.Up))
					mu.Lock()
					curUp = sample.Up
					curDown = sample.Down
					changed := speedText != lastSpeedText
					if changed {
						lastSpeedText = speedText
					}
					mu.Unlock()
					if changed {
						mSpeed.SetTitle(speedText)
					}
					updateTitle()
				}
				time.Sleep(2 * time.Second)
			}
		}()

		// Primary group node click handlers
		for i := 0; i < maxPrimaryNodes; i++ {
			slot := primarySlots[i]
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

		// Secondary group node click handlers
		for gIdx := 0; gIdx < maxExtraGroups; gIdx++ {
			eg := extraGroups[gIdx]
			for nIdx := 0; nIdx < maxExtraGroupNode; nIdx++ {
				slot := eg.nodes[nIdx]
				go func(groupSlot *extraGroupSlot, nodeSlot *nodeSlot) {
					for {
						select {
						case <-ctx.Done():
							return
						case <-nodeSlot.item.ClickedCh:
							mu.Lock()
							g := groupSlot.groupName
							n := nodeSlot.name
							mu.Unlock()
							if g != "" && n != "" {
								_ = client.SelectProxy(g, n)
								refreshState()
							}
						}
					}
				}(eg, slot)
			}
		}

		// Main menu action handlers
		go func() {
			for {
				select {
				case <-ctx.Done():
					return

				case <-mStatus.ClickedCh:
					mu.Lock()
					isOnline := online
					mu.Unlock()
					if !isOnline && mgr != nil {
						setStatusAppearance("Starting Manta Proxy...", 2)
						go func() {
							_, _ = mgr.Start()
							refreshState()
							refreshExitIP()
						}()
					} else {
						openTUIInTerminal()
					}

				case <-mExitIP.ClickedCh:
					mu.Lock()
					ipToCopy := exitIPStr
					mu.Unlock()
					if ipToCopy != "" {
						_ = copyToClipboard(ipToCopy)
						mExitIP.SetTitle(fmt.Sprintf("✓ Copied %s  —  Refreshing...", ipToCopy))
					}
					go refreshExitIP()

				case <-mTun.ClickedCh:
					mu.Lock()
					wantTun := !tunOn
					isOnline := online
					mu.Unlock()
					go func() {
						if !wantTun {
							setStatusAppearance("Disabling TUN Capture...", 2)
							_ = client.SetTun(false)
							if mgr != nil {
								cfg.TunEnabled = false
								_ = config.Save(mgr.Paths, cfg)
							}
							refreshState()
							return
						}
						setStatusAppearance("Enabling TUN Capture...", 2)
						if isOnline {
							_ = client.SetTun(true)
							if live, err := client.GetConfigs(); err == nil && live.Tun.Enable {
								if mgr != nil {
									cfg.TunEnabled = true
									_ = config.Save(mgr.Paths, cfg)
								}
								refreshState()
								return
							}
						}
						if mgr != nil {
							_, _ = mgr.StartTunElevated()
						}
						refreshState()
						refreshExitIP()
					}()

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

				case <-mAllowLan.ClickedCh:
					mu.Lock()
					wantLan := !allowLan
					mu.Unlock()
					_ = client.SetAllowLan(wantLan)
					if mgr != nil {
						cfg.AllowLan = wantLan
						_ = config.Save(mgr.Paths, cfg)
					}
					refreshState()

				case <-mModeRule.ClickedCh:
					_ = client.SetMode("rule")
					if mgr != nil {
						cfg.Mode = "rule"
						_ = config.Save(mgr.Paths, cfg)
					}
					refreshState()

				case <-mModeGlobal.ClickedCh:
					_ = client.SetMode("global")
					if mgr != nil {
						cfg.Mode = "global"
						_ = config.Save(mgr.Paths, cfg)
					}
					refreshState()

				case <-mModeDirect.ClickedCh:
					_ = client.SetMode("direct")
					if mgr != nil {
						cfg.Mode = "direct"
						_ = config.Save(mgr.Paths, cfg)
					}
					refreshState()

				case <-mBestNode.ClickedCh:
					go func() {
						mBestNode.SetTitle("⏳ Benchmarking nodes...")
						proxies, err := client.GetProxies()
						if err != nil {
							mBestNode.SetTitle("⚡ Auto-Select Fastest Node")
							return
						}
						gName, gInfo, _ := classifyGroups(proxies)
						if gName == "" || len(gInfo.All) == 0 {
							mBestNode.SetTitle("⚡ Auto-Select Fastest Node")
							return
						}
						bestNode, bestMs := benchmarkFastestNode(client, gInfo.All)
						if bestNode != "" {
							_ = client.SelectProxy(gName, bestNode)
							mBestNode.SetTitle(fmt.Sprintf("⚡ Fastest: %s (%dms)", cleanMenuText(bestNode), bestMs))
							refreshState()
							refreshExitIP()
							time.Sleep(4 * time.Second)
						} else {
							mBestNode.SetTitle("⚠ All nodes timed out")
							time.Sleep(3 * time.Second)
						}
						mBestNode.SetTitle("⚡ Auto-Select Fastest Node")
					}()

				case <-mCopyEnv.ClickedCh:
					cmdStr := fmt.Sprintf(
						"export http_proxy=http://127.0.0.1:%d https_proxy=http://127.0.0.1:%d all_proxy=socks5://127.0.0.1:%d",
						cfg.MixedPort, cfg.MixedPort, cfg.MixedPort,
					)
					_ = copyToClipboard(cmdStr)
					go func() {
						mCopyEnv.SetTitle("✓ Copied export command to Clipboard")
						time.Sleep(2500 * time.Millisecond)
						mCopyEnv.SetTitle("Copy Terminal Proxy Command")
					}()

				case <-mFlush.ClickedCh:
					go func() {
						_ = client.CloseAllConnections()
						_ = client.FlushFakeIP()
						mFlush.SetTitle("✓ Closed Connections & Flushed DNS")
						time.Sleep(2500 * time.Millisecond)
						mFlush.SetTitle("Close Active Connections & Flush DNS")
					}()

				case <-mUpdateSub.ClickedCh:
					go func() {
						if mgr == nil || cfg.ActiveProfile == "" {
							return
						}
						meta, ok := cfg.Profiles[cfg.ActiveProfile]
						if !ok || meta.URL == "" {
							return
						}
						mUpdateSub.SetTitle(fmt.Sprintf("⏳ Updating %s...", cfg.ActiveProfile))
						if newMeta, err := config.FetchProfile(mgr.Paths, cfg.ActiveProfile, meta.URL); err == nil {
							cfg.Profiles[cfg.ActiveProfile] = newMeta
							mu.Lock()
							cfg.TunEnabled = tunOn
							mu.Unlock()
							_ = config.Save(mgr.Paths, cfg)
							if config.BuildRuntimeConfig(mgr.Paths, cfg) == nil {
								_ = client.ReloadConfig(mgr.Paths.RuntimeFile)
							}
							mUpdateSub.SetTitle(fmt.Sprintf("✓ Updated %s", cfg.ActiveProfile))
							refreshState()
							time.Sleep(2500 * time.Millisecond)
						} else {
							mUpdateSub.SetTitle("✗ Subscription update failed")
							time.Sleep(2500 * time.Millisecond)
						}
						refreshQuotaDisplay()
					}()

				case <-mOpenTUI.ClickedCh:
					openTUIInTerminal()

				case <-mPrefSortSpeed.ClickedCh:
					mu.Lock()
					sortBySpeed = !sortBySpeed
					newSort := sortBySpeed
					mu.Unlock()
					setCheck(mPrefSortSpeed, newSort)
					if mgr != nil {
						cfg.SortBySpeed = newSort
						_ = config.Save(mgr.Paths, cfg)
					}
					refreshState()

				case <-mPrefAutostart.ClickedCh:
					if autostart.IsEnabled() {
						_ = autostart.Disable()
						setCheck(mPrefAutostart, false)
					} else {
						_ = autostart.Enable()
						setCheck(mPrefAutostart, true)
					}

				case <-mPrefShowSpeed.ClickedCh:
					mu.Lock()
					showBurstSpeed = !showBurstSpeed
					newShow := showBurstSpeed
					mu.Unlock()
					setCheck(mPrefShowSpeed, newShow)
					updateTitle()

				case <-mPowerOff.ClickedCh:
					if mgr != nil {
						_ = mgr.Stop()
					}
					systray.Quit()
					return

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

func titleCaseMode(mode string) string {
	switch strings.ToLower(mode) {
	case "global":
		return "Global"
	case "direct":
		return "Direct"
	default:
		return "Rule"
	}
}

// classifyGroups returns the primary selector group plus up to maxExtraGroups secondary selector groups.
func classifyGroups(proxies map[string]api.ProxyInfo) (string, api.ProxyInfo, []string) {
	var selectors []string
	for name, p := range proxies {
		if p.Type == "Selector" && name != "GLOBAL" {
			selectors = append(selectors, name)
		}
	}
	sort.Strings(selectors)

	primaryName := ""
	for _, name := range selectors {
		if strings.Contains(name, "节点选择") || strings.EqualFold(name, "PROXY") || strings.EqualFold(name, "Proxy") {
			primaryName = name
			break
		}
	}
	if primaryName == "" && len(selectors) > 0 {
		primaryName = selectors[0]
	}

	var others []string
	for _, name := range selectors {
		if name != primaryName {
			others = append(others, name)
		}
	}
	if primaryName == "" {
		if g, ok := proxies["GLOBAL"]; ok {
			return "GLOBAL", g, nil
		}
		return "", api.ProxyInfo{}, nil
	}
	return primaryName, proxies[primaryName], others
}

func latestDelay(proxies map[string]api.ProxyInfo, name string) int {
	cur := name
	seen := map[string]bool{}
	for cur != "" && !seen[cur] {
		seen[cur] = true
		p, ok := proxies[cur]
		if !ok {
			return 0
		}
		if p.Now == "" {
			if len(p.History) > 0 {
				return p.History[len(p.History)-1].Delay
			}
			return 0
		}
		cur = p.Now
	}
	return 0
}

func orderGroupNodes(nodes []string, proxies map[string]api.ProxyInfo, sortByFast bool) []string {
	out := make([]string, len(nodes))
	copy(out, nodes)
	if !sortByFast {
		return out
	}
	rank := func(name string) int {
		d := latestDelay(proxies, name)
		if d > 0 {
			return d
		}
		return 1_000_000
	}
	sort.SliceStable(out, func(i, j int) bool {
		return rank(out[i]) < rank(out[j])
	})
	return out
}

func formatNodeMenuLabel(nodeName string, proxies map[string]api.ProxyInfo) string {
	label := cleanMenuText(nodeName)
	if d := latestDelay(proxies, nodeName); d > 0 {
		return fmt.Sprintf("%s   •   %dms", label, d)
	}
	return label
}

// cleanMenuText strips invisible variation selectors so macOS NSMenu renders clean typography.
func cleanMenuText(s string) string {
	return strings.NewReplacer("\uFE0F", "", "\uFE0E", "").Replace(s)
}

func benchmarkFastestNode(client *api.Client, nodes []string) (string, int) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		bestName string
		bestMs   = 1_000_000
		sem      = make(chan struct{}, 10)
	)
	for _, n := range nodes {
		upper := strings.ToUpper(n)
		if upper == "DIRECT" || upper == "REJECT" || strings.Contains(n, "自动") || strings.Contains(n, "故障") {
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, err := client.TestDelay(name, "http://www.gstatic.com/generate_204", 3500)
			if err == nil && d > 0 {
				mu.Lock()
				if d < bestMs {
					bestMs = d
					bestName = name
				}
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	return bestName, bestMs
}

func copyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
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
