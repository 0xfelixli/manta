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
	burstSpeedFloor   = 32 * 1024
)

func mantaPointInBody(fx, fy, wingScale float64) bool {
	const cx = 11.0
	dx := math.Abs(fx - cx)

	switch {
	case fy >= 2.6 && fy <= 5.2:
		t := (fy - 2.6) / 2.6
		inner := 1.55 - t*1.15
		outer := 2.45 + t*1.15
		return dx >= inner && dx <= outer
	case fy > 5.2 && fy <= 11.2:
		t := (fy - 5.2) / 6.0
		curve := math.Pow(t, 0.88)
		maxW := 3.1 + curve*(7.4*wingScale)
		return dx <= maxW
	case fy > 11.2 && fy <= 15.4:
		t := (fy - 11.2) / 4.2
		maxW := (10.5 * wingScale) * math.Pow(1.0-t, 1.25)
		if maxW < 0.75 {
			maxW = 0.75
		}
		return dx <= maxW
	case fy > 15.4 && fy <= 19.6:
		t := (fy - 15.4) / 4.2
		maxW := 0.72 * (1.0 - t*0.55)
		return dx <= maxW
	}
	return false
}

func mantaFrameIconPNG(wingScale float64) []byte {
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
					if mantaPointInBody(fx, fy, wingScale) {
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

func mantaTemplateIconPNG() []byte {
	return mantaFrameIconPNG(1.0)
}

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
					if math.Hypot(fx-cx, fy-cy) <= radius {
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

// Run starts the minimalist macOS menu bar status item.
func Run(cfg *config.AppConfig, mgr *core.Manager, client *api.Client) error {
	ctx, cancel := context.WithCancel(context.Background())

	onReady := func() {
		iconFrames := [][]byte{
			mantaFrameIconPNG(1.00), // Neutral glide
			mantaFrameIconPNG(1.10), // Wide wingspan
			mantaFrameIconPNG(1.00), // Neutral glide
			mantaFrameIconPNG(0.76), // Swept wing stroke
		}
		if len(iconFrames[0]) > 0 {
			systray.SetTemplateIcon(iconFrames[0], iconFrames[0])
		}
		systray.SetTitle("")
		systray.SetTooltip("Manta")

		flapWings := func(cycles int) {
			for c := 0; c < cycles; c++ {
				for _, f := range iconFrames {
					if len(f) > 0 {
						systray.SetTemplateIcon(f, f)
					}
					time.Sleep(120 * time.Millisecond)
				}
			}
			if len(iconFrames[0]) > 0 {
				systray.SetTemplateIcon(iconFrames[0], iconFrames[0])
			}
		}

		dotOnline := statusDotPNG(16, 185, 129) // Emerald (#10B981)
		dotBusy := statusDotPNG(245, 158, 11)   // Amber (#F59E0B)
		dotOffline := statusDotPNG(244, 63, 94) // Coral (#F43F5E)

		// ── 1. Two-Line Compact Telemetry Header ───────────────────────────
		mStatus := systray.AddMenuItem("Connecting...", "Click to copy Exit IP (or start proxy when offline)")
		if len(dotBusy) > 0 {
			mStatus.SetIcon(dotBusy)
		}
		mTelemetry := systray.AddMenuItem("↓ 0 B/s   ↑ 0 B/s", "")
		mTelemetry.Disable()

		systray.AddSeparator()

		// ── 2. Core Switches (2 clean checkboxes) ──────────────────────────
		mTun := systray.AddMenuItemCheckbox("TUN Mode", "Full-system virtual network interface", false)
		mSysProxy := systray.AddMenuItemCheckbox("System Proxy", "macOS HTTP/HTTPS/SOCKS5 proxy", false)

		systray.AddSeparator()

		// ── 3. Mode & Nodes (3 concise submenus) ───────────────────────────
		mMode := systray.AddMenuItem("Mode: Rule", "")
		mModeRule := mMode.AddSubMenuItemCheckbox("Rule", "", true)
		mModeGlobal := mMode.AddSubMenuItemCheckbox("Global", "", false)
		mModeDirect := mMode.AddSubMenuItemCheckbox("Direct", "", false)

		mPrimaryMenu := systray.AddMenuItem("Node: -", "")
		mBestNode := mPrimaryMenu.AddSubMenuItem("⚡ Auto-Select Fastest", "Benchmark and switch to lowest-latency node")
		mPrimaryMenu.AddSeparator()
		primarySlots := make([]*nodeSlot, maxPrimaryNodes)
		for i := 0; i < maxPrimaryNodes; i++ {
			it := mPrimaryMenu.AddSubMenuItemCheckbox("-", "", false)
			it.Hide()
			primarySlots[i] = &nodeSlot{item: it}
		}

		mGroupsRoot := systray.AddMenuItem("Policy Groups", "")
		extraGroups := make([]*extraGroupSlot, maxExtraGroups)
		for gIdx := 0; gIdx < maxExtraGroups; gIdx++ {
			gMenu := mGroupsRoot.AddSubMenuItem("-", "")
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

		// ── 4. Dashboard & Collapsed Tools Submenu ─────────────────────────
		mOpenTUI := systray.AddMenuItem("Open Dashboard", "Open manta tui in Terminal")

		mMore := systray.AddMenuItem("More", "")
		mUpdateSub := mMore.AddSubMenuItem("Update Subscription", "")
		mFlush := mMore.AddSubMenuItem("Flush DNS & Connections", "")
		mCopyEnv := mMore.AddSubMenuItem("Copy Shell Proxy Command", "")
		mMore.AddSeparator()
		mAllowLan := mMore.AddSubMenuItemCheckbox("Allow LAN", "", cfg.AllowLan)
		mPrefSortSpeed := mMore.AddSubMenuItemCheckbox("Sort Nodes by Latency", "", cfg.SortBySpeed)
		mPrefAutostart := mMore.AddSubMenuItemCheckbox("Launch at Login", "", autostart.IsEnabled())

		systray.AddSeparator()

		// ── 5. Quit / Power Off ────────────────────────────────────────────
		mPowerOff := systray.AddMenuItem("Power Off", "Stop proxy daemon and quit")
		mQuit := systray.AddMenuItem("Hide Icon", "Close menu bar icon (keep proxy running)")

		var (
			mu             sync.Mutex
			online         bool
			tunOn          bool
			sysOn          bool
			allowLan       = cfg.AllowLan
			sortBySpeed    = cfg.SortBySpeed
			countryCode    string
			exitIPStr      string
			quotaShort     string
			primaryGroup   string
			curUp          int64
			curDown        int64
			lastBarTitle   = "__init__"
			lastTelemText  string
			lastStatusText string
			lastStatusIcon int
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

		updateTelemetryLine := func() {
			mu.Lock()
			down := curDown
			up := curUp
			q := quotaShort
			mu.Unlock()

			text := fmt.Sprintf("↓ %s/s   ↑ %s/s", formatBytes(down), formatBytes(up))
			if q != "" {
				text += "   •   " + q
			}
			mu.Lock()
			changed := text != lastTelemText
			if changed {
				lastTelemText = text
			}
			mu.Unlock()
			if changed {
				mTelemetry.SetTitle(text)
			}
		}

		updateStatusHeader := func() {
			mu.Lock()
			isOnline := online
			ip := exitIPStr
			cc := countryCode
			mu.Unlock()

			if !isOnline {
				setStatusAppearance("Offline  —  Click to Start", 3)
				return
			}
			if ip != "" && cc != "" {
				setStatusAppearance(fmt.Sprintf("Online  •  %s (%s)", ip, cc), 1)
			} else if ip != "" {
				setStatusAppearance(fmt.Sprintf("Online  •  %s", ip), 1)
			} else {
				setStatusAppearance("Online", 1)
			}
		}

		updateTitle := func() {
			mu.Lock()
			isOnline := online
			isTun := tunOn
			cc := countryCode
			down := curDown
			up := curUp
			mu.Unlock()

			if !isOnline {
				setBarTitle("OFF")
				return
			}
			if down >= burstSpeedFloor || up >= burstSpeedFloor {
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
			info, err := api.FetchExitIP(cfg.MixedPort)
			if err != nil {
				return
			}
			mu.Lock()
			countryCode = info.CountryCode
			exitIPStr = info.IP
			mu.Unlock()
			updateStatusHeader()
			updateTitle()
		}

		refreshQuota := func() {
			if mgr != nil {
				if latestCfg, err := config.Load(mgr.Paths); err == nil {
					cfg = latestCfg
				}
			}
			short := ""
			if meta, ok := cfg.Profiles[cfg.ActiveProfile]; ok && meta.Total > 0 {
				used := float64(meta.Upload+meta.Download) / (1 << 30)
				tot := float64(meta.Total) / (1 << 30)
				short = fmt.Sprintf("%.1f / %.0f GB", used, tot)
			}
			mu.Lock()
			quotaShort = short
			mu.Unlock()
			updateTelemetryLine()
		}

		refreshState := func() {
			refreshQuota()

			_, err := client.Version()
			if err != nil {
				mu.Lock()
				online = false
				tunOn = false
				mu.Unlock()
				updateStatusHeader()
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

			mu.Lock()
			online = true
			tunOn = liveCfg.Tun.Enable
			sysOn = isSysOn
			allowLan = liveCfg.AllowLan
			sortFast := sortBySpeed
			mu.Unlock()

			updateStatusHeader()
			setCheck(mTun, liveCfg.Tun.Enable)
			setCheck(mSysProxy, isSysOn)
			setCheck(mAllowLan, liveCfg.AllowLan)

			mode := strings.ToLower(liveCfg.Mode)
			setCheck(mModeRule, mode == "rule")
			setCheck(mModeGlobal, mode == "global")
			setCheck(mModeDirect, mode == "direct")
			mMode.SetTitle(fmt.Sprintf("Mode: %s", titleCaseMode(mode)))

			primaryName, primaryInfo, others := classifyGroups(proxies)
			mu.Lock()
			primaryGroup = primaryName
			mu.Unlock()

			if primaryName != "" {
				nowDelay := latestDelay(proxies, primaryInfo.Now)
				nowShort := shortNodeName(primaryInfo.Now)
				if nowDelay > 0 {
					mPrimaryMenu.SetTitle(fmt.Sprintf("Node: %s (%dms)", nowShort, nowDelay))
				} else {
					mPrimaryMenu.SetTitle(fmt.Sprintf("Node: %s", nowShort))
				}
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

			if len(others) > 0 {
				mGroupsRoot.Show()
				for gIdx := 0; gIdx < maxExtraGroups; gIdx++ {
					eg := extraGroups[gIdx]
					if gIdx < len(others) {
						gName := others[gIdx]
						gInfo := proxies[gName]
						mu.Lock()
						eg.groupName = gName
						mu.Unlock()

						eg.menu.SetTitle(fmt.Sprintf("%s: %s", cleanMenuText(gName), shortNodeName(gInfo.Now)))
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
			} else {
				mGroupsRoot.Hide()
			}

			updateTitle()
		}

		go func() {
			refreshState()
			refreshExitIP()
		}()

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
					updateTelemetryLine()
					updateTitle()
				}
				time.Sleep(2 * time.Second)
			}
		}()

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

		go func() {
			for {
				select {
				case <-ctx.Done():
					return

				case <-mStatus.ClickedCh:
					mu.Lock()
					isOnline := online
					ipToCopy := exitIPStr
					mu.Unlock()
					if !isOnline && mgr != nil {
						setStatusAppearance("Starting...", 2)
						go func() {
							_, _ = mgr.Start()
							refreshState()
							refreshExitIP()
						}()
					} else {
						if ipToCopy != "" {
							_ = copyToClipboard(ipToCopy)
							setStatusAppearance("✓ Copied "+ipToCopy, 1)
						}
						go refreshExitIP()
					}

				case <-mTun.ClickedCh:
					mu.Lock()
					wantTun := !tunOn
					isOnline := online
					mu.Unlock()
					go func() {
						if !wantTun {
							_ = client.SetTun(false)
							sysproxy.RestoreDefaultDNS()
							if mgr != nil {
								cfg.TunEnabled = false
								_ = config.Save(mgr.Paths, cfg)
							}
							refreshState()
							return
						}
						setStatusAppearance("Enabling TUN...", 2)
						if isOnline {
							_ = client.SetTun(true)
							if live, err := client.GetConfigs(); err == nil && live.Tun.Enable {
								sysproxy.EnsureTunDNS()
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
							sysproxy.EnsureTunDNS()
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
						mBestNode.SetTitle("⏳ Testing...")
						go flapWings(6)
						proxies, err := client.GetProxies()
						if err != nil {
							mBestNode.SetTitle("⚡ Auto-Select Fastest")
							return
						}
						gName, gInfo, _ := classifyGroups(proxies)
						if gName == "" || len(gInfo.All) == 0 {
							mBestNode.SetTitle("⚡ Auto-Select Fastest")
							return
						}
						bestNode, _ := benchmarkFastestNode(client, gInfo.All)
						if bestNode != "" {
							_ = client.SelectProxy(gName, bestNode)
							refreshState()
							refreshExitIP()
						}
						mBestNode.SetTitle("⚡ Auto-Select Fastest")
					}()

				case <-mCopyEnv.ClickedCh:
					cmdStr := fmt.Sprintf(
						"export http_proxy=http://127.0.0.1:%d https_proxy=http://127.0.0.1:%d all_proxy=socks5://127.0.0.1:%d",
						cfg.MixedPort, cfg.MixedPort, cfg.MixedPort,
					)
					_ = copyToClipboard(cmdStr)

				case <-mFlush.ClickedCh:
					go func() {
						_ = client.CloseAllConnections()
						_ = client.FlushFakeIP()
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
						setStatusAppearance("Updating Subscription...", 2)
						if newMeta, err := config.FetchProfile(mgr.Paths, cfg.ActiveProfile, meta.URL); err == nil {
							cfg.Profiles[cfg.ActiveProfile] = newMeta
							mu.Lock()
							cfg.TunEnabled = tunOn
							mu.Unlock()
							_ = config.Save(mgr.Paths, cfg)
							if config.BuildRuntimeConfig(mgr.Paths, cfg) == nil {
								_ = client.ReloadConfig(mgr.Paths.RuntimeFile)
							}
						}
						refreshState()
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

// shortNodeName strips verbose provider marketing suffixes (after "|" or "｜") for compact root titles.
func shortNodeName(name string) string {
	s := cleanMenuText(name)
	for _, sep := range []string{"|", "｜"} {
		if idx := strings.Index(s, sep); idx > 0 {
			s = strings.TrimSpace(s[:idx])
		}
	}
	return s
}

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
		return fmt.Sprintf("%s  (%dms)", label, d)
	}
	return label
}

// cleanMenuText keeps native macOS color emoji selectors (like ♻️) intact while replacing
// the U+1F1F9 U+1F1FC pair that renders as a missing-glyph tofu box (☒) on CH/A Macs.
func cleanMenuText(s string) string {
	return strings.ReplaceAll(s, "\U0001F1F9\U0001F1FC", "🌐")
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
