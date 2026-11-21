package tui

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/autostart"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/core"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

type tabView int

const (
	tabDashboard tabView = iota
	tabProxies
	tabSettings
)

type pane int

const (
	paneGroups pane = iota
	paneNodes
)

type toastKind int

const (
	toastNone toastKind = iota
	toastInfo
	toastBusy
	toastSuccess
	toastWarn
	toastError
)

// Messages
type stateLoadedMsg struct {
	version    string
	mode       string
	allowLan   bool
	tunOn      bool
	tunStack   string
	proxies    map[string]api.ProxyInfo
	sysProxyOn bool
	conns      []api.ConnectionItem
	totalUp    int64
	totalDown  int64
	err        error
}

type connsPollMsg struct {
	conns     []api.ConnectionItem
	totalUp   int64
	totalDown int64
	liveCfg   *api.ConfigsResp
}

type exitIPLoadedMsg struct {
	info *api.ExitIPInfo
	err  error
}

type trafficStartedMsg struct {
	ch <-chan api.TrafficSample
}

type trafficSampleMsg struct {
	sample api.TrafficSample
	ch     <-chan api.TrafficSample
}

type actionResultMsg struct {
	status     string
	kind       toastKind
	err        error
	reload     bool
	refreshIP  bool
	sysProxyOn *bool
}

type delayResultMsg struct {
	results  map[string]int // -1 indicates timeout/error
	label    string
	single   bool
	autoSort bool
}

type toastExpireMsg struct {
	seq int
}

type powerOffDoneMsg struct{}

// Oceanic Mineral design system — adaptive for both light (white) and dark terminals.
// 80% calm mineral neutrals (Zinc), 15% oceanic teal/blue focus, 5% restrained semantic accents.
var (
	// Brand identity: oceanic teal on soft seafoam
	badgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}).
			Background(lipgloss.AdaptiveColor{Light: "#CCFBF1", Dark: "#134E4A"}).
			Padding(0, 1)

	// Connection status pills
	pillOnlineStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0D9488", Dark: "#2DD4BF"}).
			Background(lipgloss.AdaptiveColor{Light: "#F0FDFA", Dark: "#115E59"}).
			Padding(0, 1)

	pillOfflineStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#BE123C", Dark: "#FDA4AF"}).
				Background(lipgloss.AdaptiveColor{Light: "#FFF1F2", Dark: "#881337"}).
				Padding(0, 1)

	// Navigation tabs: crisp oceanic blue on airy mist
	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#93C5FD"}).
			Background(lipgloss.AdaptiveColor{Light: "#EFF6FF", Dark: "#1E3A8A"}).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#A1A1AA"}).
				Padding(0, 1)

	headerRuleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#E4E4E7", Dark: "#27272A"})

	exitLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"})

	// Whisper-quiet card borders so content leads the hierarchy
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#E4E4E7", Dark: "#27272A"}).
			Padding(0, 1)

	cardTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#F4F4F5"})

	kvKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#A1A1AA"})

	kvValStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#F4F4F5"})

	// Paired traffic accents: teal upload + ocean-blue download
	speedUpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0D9488", Dark: "#2DD4BF"}).
			Bold(true)

	speedDownStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"}).
			Bold(true)

	// Inline switches: airy & restrained so stacked rows never feel heavy
	switchOnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}).
			Background(lipgloss.AdaptiveColor{Light: "#F0FDFA", Dark: "#134E4A"}).
			Padding(0, 1)

	switchOffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#A1A1AA"}).
			Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"}).
			Padding(0, 1)

	// Segmented mode control
	segActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#93C5FD"}).
			Background(lipgloss.AdaptiveColor{Light: "#DBEAFE", Dark: "#1E3A8A"}).
			Padding(0, 1)

	segInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#A1A1AA"}).
				Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"}).
				Padding(0, 1)

	// Subtle secondary actions (ghost-pill)
	miniBtnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"}).
			Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"}).
			Padding(0, 1)

	btnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#3F3F46", Dark: "#E4E4E7"}).
			Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"}).
			Padding(0, 1)

	btnAccentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#93C5FD"}).
			Background(lipgloss.AdaptiveColor{Light: "#EFF6FF", Dark: "#1E3A8A"}).
			Padding(0, 1)

	// Proxies split panes
	paneBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.AdaptiveColor{Light: "#3B82F6", Dark: "#60A5FA"}).
				Padding(0, 1)

	paneBorderInactive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.AdaptiveColor{Light: "#E4E4E7", Dark: "#27272A"}).
				Padding(0, 1)

	rowTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#27272A", Dark: "#E4E4E7"})

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#1E3A8A", Dark: "#EFF6FF"}).
				Background(lipgloss.AdaptiveColor{Light: "#DBEAFE", Dark: "#1E3A8A"})

	selectedRowInactiveStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.AdaptiveColor{Light: "#18181B", Dark: "#F4F4F5"}).
					Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"})

	activeMarkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0D9488", Dark: "#2DD4BF"})

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#71717A", Dark: "#71717A"})

	// Transient feedback toasts
	toastInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#3F3F46", Dark: "#D4D4D8"}).
			Background(lipgloss.AdaptiveColor{Light: "#F4F4F5", Dark: "#27272A"}).
			Padding(0, 1)

	toastBusyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#93C5FD"}).
			Background(lipgloss.AdaptiveColor{Light: "#EFF6FF", Dark: "#1E3A8A"}).
			Padding(0, 1)

	toastSuccessStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}).
				Background(lipgloss.AdaptiveColor{Light: "#F0FDFA", Dark: "#134E4A"}).
				Padding(0, 1)

	toastWarnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FCD34D"}).
			Background(lipgloss.AdaptiveColor{Light: "#FFFBEB", Dark: "#78350F"}).
			Padding(0, 1)

	toastErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#BE123C", Dark: "#FDA4AF"}).
			Background(lipgloss.AdaptiveColor{Light: "#FFF1F2", Dark: "#881337"}).
			Padding(0, 1)

	// Refined latency semantics (sage-teal / warm amber / rose-coral)
	delayGoodStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0D9488", Dark: "#2DD4BF"})

	delayMedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"})

	delayBadStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#E11D48", Dark: "#FB7185"})
)

// List items
type groupItem struct {
	name  string
	kind  string
	now   string
	count int
}

func (g groupItem) FilterValue() string { return g.name }

type nodeItem struct {
	name   string
	active bool
	delay  int
}

func (n nodeItem) FilterValue() string { return n.name }

type groupDelegate struct {
	focused *bool
}

func (d groupDelegate) Height() int                             { return 1 }
func (d groupDelegate) Spacing() int                            { return 0 }
func (d groupDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d groupDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	g, ok := item.(groupItem)
	if !ok {
		return
	}
	width := m.Width()
	if width < 10 {
		width = 20
	}
	rightTag := fmt.Sprintf("%d", g.count)
	if g.kind != "Selector" {
		rightTag = fmt.Sprintf("auto %d", g.count)
	}
	nameMax := width - len(rightTag) - 4
	if nameMax < 6 {
		nameMax = 6
	}
	name := ansi.Truncate(cleanDisplay(g.name), nameMax, "…")

	var line string
	if index == m.Index() {
		plain := joinLeftRight("▸ "+name, rightTag, width)
		if d.focused != nil && *d.focused {
			line = selectedRowStyle.Render(plain)
		} else {
			line = selectedRowInactiveStyle.Render(plain)
		}
	} else {
		line = joinLeftRight(rowTextStyle.Render("  "+name), dimStyle.Render(rightTag), width)
	}
	fmt.Fprint(w, zone.Mark(fmt.Sprintf("group:%d", index), line))
}

type nodeDelegate struct {
	focused *bool
}

func (d nodeDelegate) Height() int                             { return 1 }
func (d nodeDelegate) Spacing() int                            { return 0 }
func (d nodeDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d nodeDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	n, ok := item.(nodeItem)
	if !ok {
		return
	}
	width := m.Width()
	if width < 16 {
		width = 30
	}
	delayPlain := formatDelayPlain(n.delay)
	nameMax := width - 2 - 3 - lipgloss.Width(delayPlain)
	if nameMax < 8 {
		nameMax = 8
	}
	name := ansi.Truncate(cleanDisplay(n.name), nameMax, "…")

	var line string
	if index == m.Index() && d.focused != nil && *d.focused {
		prefix := "  "
		if n.active {
			prefix = "● "
		}
		line = selectedRowStyle.Render(joinLeftRight(prefix+name, delayPlain, width))
	} else {
		prefix := "  "
		if n.active {
			prefix = activeMarkStyle.Render("● ")
		}
		line = joinLeftRight(prefix+rowTextStyle.Render(name), formatDelayStyled(n.delay), width)
	}
	fmt.Fprint(w, zone.Mark(fmt.Sprintf("node:%d", index), line))
}

type model struct {
	cfg    *config.AppConfig
	mgr    *core.Manager
	client *api.Client
	ctx    context.Context
	cancel context.CancelFunc

	width  int
	height int

	activeTab tabView

	online     bool
	version    string
	mode       string
	allowLan   bool
	tunOn      bool
	tunStack   string
	sysProxyOn bool
	sortByFast bool

	up        int64
	down      int64
	peakDown  int64
	totalUp   int64
	totalDown int64
	conns     []api.ConnectionItem
	downHist  []int64

	exitIP     *api.ExitIPInfo
	exitIPErr  string
	checkingIP bool

	proxies map[string]api.ProxyInfo
	delays  map[string]int

	focus         pane
	groupsFocused *bool
	nodesFocused  *bool
	groupList     list.Model
	nodeList      list.Model
	spin          spinner.Model
	busy          bool
	initialized   bool

	toastText string
	toastType toastKind
	toastSeq  int
}

func newStyledList(title string, delegate list.ItemDelegate) list.Model {
	l := list.New(nil, delegate, 20, 10)
	l.Title = title
	l.SetShowHelp(false)
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"})
	l.Styles.TitleBar = lipgloss.NewStyle().Padding(0)
	l.Styles.StatusBar = dimStyle.Padding(0)
	l.Styles.PaginationStyle = dimStyle
	return l
}

// Run launches the interactive manta terminal dashboard.
func Run(cfg *config.AppConfig, mgr *core.Manager, client *api.Client) error {
	zone.NewGlobal()
	defer zone.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gf, nf := true, false
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = speedDownStyle

	initialTab := tabDashboard
	switch strings.ToLower(cfg.StartupTab) {
	case "proxies":
		initialTab = tabProxies
	case "last":
		if strings.EqualFold(cfg.LastTab, "proxies") {
			initialTab = tabProxies
		}
	default:
		// default "dashboard"
	}

	m := model{
		cfg:           cfg,
		mgr:           mgr,
		client:        client,
		ctx:           ctx,
		cancel:        cancel,
		activeTab:     initialTab,
		sortByFast:    cfg.SortBySpeed,
		proxies:       make(map[string]api.ProxyInfo),
		delays:        make(map[string]int),
		downHist:      make([]int64, 0, 32),
		focus:         paneGroups,
		groupsFocused: &gf,
		nodesFocused:  &nf,
		groupList:     newStyledList("Proxy Groups", groupDelegate{focused: &gf}),
		nodeList:      newStyledList("Nodes", nodeDelegate{focused: &nf}),
		spin:          sp,
		busy:          true,
		checkingIP:    true,
		toastText:     "Connecting to mihomo...",
		toastType:     toastBusy,
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *model) savePrefs() {
	if m.mgr == nil || m.cfg == nil {
		return
	}
	m.cfg.SortBySpeed = m.sortByFast
	if m.activeTab == tabProxies {
		m.cfg.LastTab = "proxies"
	} else if m.activeTab == tabDashboard {
		m.cfg.LastTab = "dashboard"
	}
	if sel, ok := m.groupList.SelectedItem().(groupItem); ok && sel.name != "" {
		m.cfg.LastGroup = sel.name
	}
	if m.mode != "" {
		m.cfg.Mode = strings.ToLower(m.mode)
	}
	m.cfg.AllowLan = m.allowLan
	m.cfg.TunEnabled = m.tunOn
	_ = config.Save(m.mgr.Paths, m.cfg)
}

// setToast shows a transient colored feedback banner on the single footer line.
// Non-busy toasts automatically fade back to contextual key hints after 4 seconds.
func (m *model) setToast(kind toastKind, text string) tea.Cmd {
	m.toastSeq++
	m.toastType = kind
	m.toastText = text
	if kind == toastBusy || kind == toastNone {
		return nil
	}
	seq := m.toastSeq
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return toastExpireMsg{seq: seq}
	})
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadStateCmd(), m.fetchExitIPCmd(), m.startTrafficCmd(), m.pollConnsTickCmd(), m.spin.Tick)
}

func (m model) pollConnsTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		var msg connsPollMsg
		if conns, err := m.client.GetConnections(); err == nil {
			msg.conns = conns.Connections
			msg.totalUp = conns.UploadTotal
			msg.totalDown = conns.DownloadTotal
		}
		if live, err := m.client.GetConfigs(); err == nil {
			msg.liveCfg = live
		}
		return msg
	})
}

func detectSysProxy() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
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

func (m model) loadStateCmd() tea.Cmd {
	return func() tea.Msg {
		ver, err := m.client.Version()
		if err != nil {
			return stateLoadedMsg{err: err}
		}
		liveCfg, err := m.client.GetConfigs()
		if err != nil {
			return stateLoadedMsg{err: err}
		}
		proxies, err := m.client.GetProxies()
		if err != nil {
			return stateLoadedMsg{err: err}
		}
		var conns []api.ConnectionItem
		var totalUp, totalDown int64
		if summary, err := m.client.GetConnections(); err == nil {
			conns = summary.Connections
			totalUp = summary.UploadTotal
			totalDown = summary.DownloadTotal
		}
		return stateLoadedMsg{
			version:    ver.Version,
			mode:       liveCfg.Mode,
			allowLan:   liveCfg.AllowLan,
			tunOn:      liveCfg.Tun.Enable,
			tunStack:   liveCfg.Tun.Stack,
			proxies:    proxies,
			sysProxyOn: detectSysProxy(),
			conns:      conns,
			totalUp:    totalUp,
			totalDown:  totalDown,
		}
	}
}

func (m model) toggleProxyDaemonCmd() tea.Cmd {
	wantStart := !m.online
	return func() tea.Msg {
		if m.mgr == nil {
			return actionResultMsg{err: fmt.Errorf("process manager unavailable")}
		}
		if wantStart {
			pid, err := m.mgr.Start()
			if err != nil {
				return actionResultMsg{err: err}
			}
			return actionResultMsg{
				status:    fmt.Sprintf("✓ Proxy started (pid %d)", pid),
				kind:      toastSuccess,
				reload:    true,
				refreshIP: true,
			}
		}
		if err := m.mgr.Stop(); err != nil {
			return actionResultMsg{err: err}
		}
		off := false
		return actionResultMsg{
			status:     "✓ Proxy stopped & macOS system proxy disabled",
			kind:       toastWarn,
			reload:     true,
			sysProxyOn: &off,
		}
	}
}

func (m model) powerOffAndQuitCmd() tea.Cmd {
	return func() tea.Msg {
		if m.mgr != nil {
			_ = m.mgr.PowerOffAll()
		}
		return powerOffDoneMsg{}
	}
}

func (m model) setModeCmd(target string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.SetMode(target); err != nil {
			// If daemon is offline, still persist the preference to config.yaml.
			return actionResultMsg{
				status: fmt.Sprintf("✓ Saved mode → %s (daemon offline; press 'p' to start)", strings.ToUpper(target)),
				kind:   toastInfo,
			}
		}
		return actionResultMsg{
			status:    fmt.Sprintf("✓ Routing mode → %s", strings.ToUpper(target)),
			kind:      toastSuccess,
			reload:    true,
			refreshIP: true,
		}
	}
}

func (m model) cycleModeCmd() tea.Cmd {
	next := "rule"
	switch strings.ToLower(m.mode) {
	case "rule":
		next = "global"
	case "global":
		next = "direct"
	default:
		next = "rule"
	}
	return m.setModeCmd(next)
}

func (m model) toggleTunCmd() tea.Cmd {
	wantOn := !m.tunOn
	return func() tea.Msg {
		if !wantOn {
			if err := m.client.SetTun(false); err != nil {
				return actionResultMsg{err: err}
			}
			if m.mgr != nil && m.cfg != nil {
				m.cfg.TunEnabled = false
				_ = config.Save(m.mgr.Paths, m.cfg)
			}
			return actionResultMsg{status: "✓ TUN mode OFF", kind: toastWarn, reload: true}
		}

		// Try hot-enabling TUN on the currently running daemon first.
		if m.online {
			_ = m.client.SetTun(true)
			if live, err := m.client.GetConfigs(); err == nil && live.Tun.Enable {
				if m.mgr != nil && m.cfg != nil {
					m.cfg.TunEnabled = true
					_ = config.Save(m.mgr.Paths, m.cfg)
				}
				return actionResultMsg{status: "✓ TUN mode ON (full-system capture)", kind: toastSuccess, reload: true}
			}
		}

		// Current daemon is offline or lacks root privileges — elevate via native macOS authorization dialog.
		if m.mgr == nil {
			return actionResultMsg{err: fmt.Errorf("process manager unavailable")}
		}
		if _, err := m.mgr.StartTunElevated(); err != nil {
			return actionResultMsg{err: err, reload: true}
		}
		return actionResultMsg{
			status:    "✓ TUN mode ON (authorized & full-system capture active)",
			kind:      toastSuccess,
			reload:    true,
			refreshIP: true,
		}
	}
}

func (m model) toggleAllowLanCmd() tea.Cmd {
	wantOn := !m.allowLan
	return func() tea.Msg {
		if err := m.client.SetAllowLan(wantOn); err != nil {
			return actionResultMsg{
				status: fmt.Sprintf("✓ Saved Allow LAN = %v (daemon offline; press 'p' to start)", wantOn),
				kind:   toastInfo,
			}
		}
		if wantOn {
			return actionResultMsg{status: fmt.Sprintf("✓ Allow LAN ON (port %d)", m.cfg.MixedPort), kind: toastSuccess, reload: true}
		}
		return actionResultMsg{status: "✓ Allow LAN OFF", kind: toastInfo, reload: true}
	}
}

func (m model) updateSubCmd() tea.Cmd {
	return func() tea.Msg {
		if m.mgr == nil {
			return actionResultMsg{err: fmt.Errorf("config manager unavailable")}
		}
		name := m.cfg.ActiveProfile
		if name == "" {
			return actionResultMsg{err: fmt.Errorf("no active subscription profile")}
		}
		meta, ok := m.cfg.Profiles[name]
		if !ok || meta.URL == "" {
			return actionResultMsg{err: fmt.Errorf("profile %q has no subscription URL", name)}
		}
		newMeta, err := config.FetchProfile(m.mgr.Paths, name, meta.URL)
		if err != nil {
			return actionResultMsg{err: err}
		}
		m.cfg.Profiles[name] = newMeta
		_ = config.Save(m.mgr.Paths, m.cfg)

		m.cfg.TunEnabled = m.tunOn
		if err := config.BuildRuntimeConfig(m.mgr.Paths, m.cfg); err != nil {
			return actionResultMsg{err: err}
		}
		if err := m.client.ReloadConfig(m.mgr.Paths.RuntimeFile); err != nil {
			return actionResultMsg{err: fmt.Errorf("profile saved, reload failed: %w", err)}
		}
		msg := fmt.Sprintf("✓ Updated & reloaded subscription %q", name)
		if q := newMeta.QuotaSummary(); q != "" {
			msg += " (" + q + ")"
		}
		return actionResultMsg{
			status:    msg,
			kind:      toastSuccess,
			reload:    true,
			refreshIP: true,
		}
	}
}

func (m model) closeAllConnsCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.client.CloseAllConnections(); err != nil {
			return actionResultMsg{err: err}
		}
		_ = m.client.FlushFakeIP()
		return actionResultMsg{
			status: "✓ Closed all active connections & flushed Fake-IP DNS cache",
			kind:   toastSuccess,
			reload: true,
		}
	}
}

func (m model) fetchExitIPCmd() tea.Cmd {
	port := m.cfg.MixedPort
	return func() tea.Msg {
		info, err := api.FetchExitIP(port)
		return exitIPLoadedMsg{info: info, err: err}
	}
}

func (m model) toggleSysProxyCmd() tea.Cmd {
	wantOn := !m.sysProxyOn
	port := m.cfg.MixedPort
	return func() tea.Msg {
		services, err := sysproxy.ResolveServices("")
		if err != nil {
			return actionResultMsg{err: err}
		}
		if wantOn {
			if err := sysproxy.Enable("127.0.0.1", port, services); err != nil {
				return actionResultMsg{err: err}
			}
			on := true
			return actionResultMsg{
				status:     fmt.Sprintf("✓ System proxy ON [%s]", strings.Join(services, ", ")),
				kind:       toastSuccess,
				sysProxyOn: &on,
			}
		}
		if err := sysproxy.Disable(services); err != nil {
			return actionResultMsg{err: err}
		}
		off := false
		return actionResultMsg{
			status:     fmt.Sprintf("✓ System proxy OFF [%s]", strings.Join(services, ", ")),
			kind:       toastWarn,
			sysProxyOn: &off,
		}
	}
}

func (m model) startTrafficCmd() tea.Cmd {
	return func() tea.Msg {
		ch, err := m.client.StreamTraffic(m.ctx)
		if err != nil {
			return nil
		}
		return trafficStartedMsg{ch: ch}
	}
}

func waitTrafficCmd(ch <-chan api.TrafficSample) tea.Cmd {
	return func() tea.Msg {
		sample, ok := <-ch
		if !ok {
			return nil
		}
		return trafficSampleMsg{sample: sample, ch: ch}
	}
}

func (m model) selectNodeCmd(group, node string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.SelectProxy(group, node); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{
			status:    fmt.Sprintf("✓ Switched %s → %s", group, node),
			kind:      toastSuccess,
			reload:    true,
			refreshIP: true,
		}
	}
}

func (m model) testNodesCmd(nodes []string, label string, single, autoSort bool) tea.Cmd {
	return func() tea.Msg {
		results := make(map[string]int, len(nodes))
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)

		for _, n := range nodes {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				d, err := m.client.TestDelay(name, "http://www.gstatic.com/generate_204", 4000)
				mu.Lock()
				if err != nil {
					results[name] = -1
				} else {
					results[name] = d
				}
				mu.Unlock()
			}(n)
		}
		wg.Wait()
		return delayResultMsg{
			results:  results,
			label:    label,
			single:   single,
			autoSort: autoSort,
		}
	}
}

func (m *model) setFocus(p pane) {
	m.focus = p
	*m.groupsFocused = (p == paneGroups)
	*m.nodesFocused = (p == paneNodes)
}

func (m *model) syncSizes() {
	_, _, leftBoxW, rightBoxW, bodyH := m.layout()
	// Proxies tab has a 1-row action bar + 1 blank breathing row above the two panes.
	paneInnerH := bodyH - 4
	if paneInnerH < 5 {
		paneInnerH = 5
	}
	m.groupList.SetSize(leftBoxW-4, paneInnerH)
	m.nodeList.SetSize(rightBoxW-4, paneInnerH)
}

// Vertical & horizontal budget with safety margin so terminal emulators NEVER auto-wrap a line:
//   Row 0: 1-line Nav Header
//   Row 1: 1-line subtle horizontal divider rule
//   Row 2..: Main workspace (bodyH lines)
//   Last Row: 1-line Smart Footer
// Total height = bodyH + 3 <= h - 1; Total width = w <= m.width - 2.
func (m model) layout() (w, h, leftBoxW, rightBoxW, bodyH int) {
	w = m.width - 2
	if w < 64 {
		w = 78
	}
	h = m.height - 1
	if h < 17 {
		h = 23
	}
	bodyH = h - 3
	if bodyH < 12 {
		bodyH = 12
	}
	usable := w - 5
	leftBoxW = usable * 32 / 100
	if leftBoxW < 24 {
		leftBoxW = 24
	}
	if leftBoxW > 42 {
		leftBoxW = 42
	}
	rightBoxW = usable - leftBoxW
	if rightBoxW < 30 {
		rightBoxW = 30
	}
	return
}

func (m model) selectedGroup() (api.ProxyInfo, bool) {
	item, ok := m.groupList.SelectedItem().(groupItem)
	if !ok {
		return api.ProxyInfo{}, false
	}
	p, exists := m.proxies[item.name]
	return p, exists
}

func (m model) primarySelectorGroup() (api.ProxyInfo, bool) {
	if m.activeTab == tabDashboard {
		for _, item := range m.groupList.Items() {
			if g, ok := item.(groupItem); ok && g.kind == "Selector" {
				if strings.Contains(g.name, "节点选择") || strings.EqualFold(g.name, "PROXY") || strings.EqualFold(g.name, "Proxy") {
					if p, exists := m.proxies[g.name]; exists {
						return p, true
					}
				}
			}
		}
	}
	return m.selectedGroup()
}

func (m model) resolveActiveExit() (groupName, chain string, delay int) {
	if !m.online || len(m.proxies) == 0 {
		return "", "(offline)", 0
	}
	mode := strings.ToLower(m.mode)
	if mode == "direct" {
		return "DIRECT", "DIRECT (bypassing proxy)", 0
	}
	if mode == "global" {
		if g, ok := m.proxies["GLOBAL"]; ok && g.Now != "" {
			return "GLOBAL", m.traceChain("GLOBAL", g.Now), m.finalDelay(g.Now)
		}
	}
	if g, ok := m.primarySelectorGroup(); ok && g.Now != "" {
		return g.Name, m.traceChain(g.Name, g.Now), m.finalDelay(g.Now)
	}
	return "", "(none)", 0
}

func (m model) traceChain(startGroup, first string) string {
	parts := []string{cleanDisplay(startGroup)}
	cur := first
	seen := map[string]bool{startGroup: true}
	for cur != "" && !seen[cur] && len(parts) < 4 {
		seen[cur] = true
		parts = append(parts, cleanDisplay(cur))
		p, ok := m.proxies[cur]
		if !ok || p.Now == "" {
			break
		}
		cur = p.Now
	}
	return strings.Join(parts, " -> ")
}

func (m model) finalDelay(name string) int {
	cur := name
	seen := map[string]bool{}
	for cur != "" && !seen[cur] {
		seen[cur] = true
		p, ok := m.proxies[cur]
		if !ok || p.Now == "" {
			return m.delays[cur]
		}
		cur = p.Now
	}
	return m.delays[cur]
}

func delayRank(ms int) int {
	if ms > 0 {
		return ms
	}
	if ms == 0 {
		return 1_000_000
	}
	return 2_000_000
}

func (m *model) rebuildNodeList(preserveSelection bool) {
	prevName := ""
	if preserveSelection {
		if sel, ok := m.nodeList.SelectedItem().(nodeItem); ok {
			prevName = sel.name
		}
	}

	g, ok := m.selectedGroup()
	if !ok {
		m.nodeList.Title = "Nodes"
		m.nodeList.SetItems(nil)
		return
	}

	badge := "Auto"
	if g.Type == "Selector" {
		badge = "Selectable"
	}
	sortTag := ""
	if m.sortByFast {
		sortTag = " • Speed"
	}
	m.nodeList.Title = fmt.Sprintf("%s  [%s • %s%s]", cleanDisplay(g.Name), g.Type, badge, sortTag)

	names := make([]string, len(g.All))
	copy(names, g.All)
	if m.sortByFast {
		sort.SliceStable(names, func(i, j int) bool {
			return delayRank(m.delays[names[i]]) < delayRank(m.delays[names[j]])
		})
	}

	items := make([]list.Item, 0, len(names))
	targetIdx := 0
	for i, name := range names {
		items = append(items, nodeItem{
			name:   name,
			active: name == g.Now,
			delay:  m.delays[name],
		})
		if preserveSelection && name == prevName {
			targetIdx = i
		}
	}
	m.nodeList.SetItems(items)
	if preserveSelection && targetIdx < len(items) {
		m.nodeList.Select(targetIdx)
	} else {
		m.nodeList.ResetSelected()
	}
}

func (m model) isFiltering() bool {
	return m.activeTab == tabProxies && (m.groupList.FilterState() == list.Filtering || m.nodeList.FilterState() == list.Filtering)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncSizes()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case toastExpireMsg:
		if msg.seq == m.toastSeq && m.toastType != toastBusy {
			m.toastType = toastNone
			m.toastText = ""
		}
		return m, nil

	case powerOffDoneMsg:
		m.cancel()
		return m, tea.Quit

	case stateLoadedMsg:
		m.busy = false
		if msg.err != nil {
			m.online = false
			return m, m.setToast(toastError, fmt.Sprintf("✗ Offline: %v", msg.err))
		}
		m.online = true
		m.version = msg.version
		m.mode = msg.mode
		m.allowLan = msg.allowLan
		m.tunOn = msg.tunOn
		m.tunStack = msg.tunStack
		m.sysProxyOn = msg.sysProxyOn
		m.conns = msg.conns
		m.totalUp = msg.totalUp
		m.totalDown = msg.totalDown
		m.proxies = msg.proxies
		if m.initialized {
			m.savePrefs()
		}

		prevGroup := ""
		if sel, ok := m.groupList.SelectedItem().(groupItem); ok {
			prevGroup = sel.name
		}

		var selectors, autos []string
		for name, p := range msg.proxies {
			if p.Type == "Selector" {
				selectors = append(selectors, name)
			} else if p.Type == "URLTest" || p.Type == "Fallback" {
				autos = append(autos, name)
			}
			if len(p.History) > 0 {
				if _, exists := m.delays[name]; !exists {
					m.delays[name] = p.History[len(p.History)-1].Delay
				}
			}
		}
		sort.Strings(selectors)
		sort.Strings(autos)

		var ordered []string
		hasGlobal := false
		for _, s := range selectors {
			if s == "GLOBAL" {
				hasGlobal = true
				continue
			}
			ordered = append(ordered, s)
		}
		ordered = append(ordered, autos...)
		if hasGlobal {
			ordered = append(ordered, "GLOBAL")
		}

		items := make([]list.Item, 0, len(ordered))
		groupIdx := 0
		wantGroup := prevGroup
		if !m.initialized && m.cfg.LastGroup != "" {
			wantGroup = m.cfg.LastGroup
		}
		for i, name := range ordered {
			p := msg.proxies[name]
			items = append(items, groupItem{
				name:  name,
				kind:  p.Type,
				now:   p.Now,
				count: len(p.All),
			})
			if wantGroup != "" && name == wantGroup {
				groupIdx = i
			}
		}
		firstLoad := !m.initialized
		m.initialized = true
		m.groupList.SetItems(items)
		if groupIdx < len(items) {
			m.groupList.Select(groupIdx)
		}
		m.rebuildNodeList(true)
		if firstLoad {
			m.toastType = toastNone
			m.toastText = ""
			if m.cfg.AutoTestOnOpen {
				if g, ok := m.selectedGroup(); ok && len(g.All) > 0 {
					m.busy = true
					m.setToast(toastBusy, fmt.Sprintf("Auto-testing %d nodes in %s...", len(g.All), g.Name))
					return m, m.testNodesCmd(g.All, g.Name, false, m.sortByFast)
				}
			}
		}
		return m, nil

	case exitIPLoadedMsg:
		m.checkingIP = false
		if msg.err != nil {
			m.exitIPErr = msg.err.Error()
			return m, m.setToast(toastWarn, "⚠ Exit IP lookup failed")
		}
		m.exitIPErr = ""
		m.exitIP = msg.info
		if m.toastType == toastBusy {
			return m, m.setToast(toastSuccess, "✓ Exit IP updated")
		}
		return m, nil

	case connsPollMsg:
		if msg.conns != nil {
			m.conns = msg.conns
			m.totalUp = msg.totalUp
			m.totalDown = msg.totalDown
		}
		if msg.liveCfg != nil {
			m.online = true
			m.mode = msg.liveCfg.Mode
			m.allowLan = msg.liveCfg.AllowLan
			m.tunOn = msg.liveCfg.Tun.Enable
			m.tunStack = msg.liveCfg.Tun.Stack
		}
		return m, m.pollConnsTickCmd()

	case trafficStartedMsg:
		return m, waitTrafficCmd(msg.ch)

	case trafficSampleMsg:
		m.up = msg.sample.Up
		m.down = msg.sample.Down
		if msg.sample.Down > m.peakDown {
			m.peakDown = msg.sample.Down
		}
		m.totalUp += msg.sample.Up
		m.totalDown += msg.sample.Down
		m.downHist = append(m.downHist, msg.sample.Down)
		if len(m.downHist) > 32 {
			m.downHist = m.downHist[len(m.downHist)-32:]
		}
		return m, waitTrafficCmd(msg.ch)

	case actionResultMsg:
		m.busy = false
		if msg.err != nil {
			return m, m.setToast(toastError, "✗ "+msg.err.Error())
		}
		var cmds []tea.Cmd
		if cmd := m.setToast(msg.kind, msg.status); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if msg.sysProxyOn != nil {
			m.sysProxyOn = *msg.sysProxyOn
		}
		if msg.reload {
			cmds = append(cmds, tea.ClearScreen, m.loadStateCmd())
		}
		if msg.refreshIP {
			m.checkingIP = true
			cmds = append(cmds, m.fetchExitIPCmd())
		}
		return m, tea.Batch(cmds...)

	case delayResultMsg:
		m.busy = false
		bestName := ""
		bestMs := 1_000_000
		okCount := 0
		for k, v := range msg.results {
			m.delays[k] = v
			if v > 0 {
				okCount++
				if v < bestMs {
					bestMs = v
					bestName = k
				}
			}
		}
		if msg.autoSort {
			m.sortByFast = true
			m.savePrefs()
		}
		m.rebuildNodeList(!msg.autoSort)
		if msg.single {
			d := msg.results[msg.label]
			if d > 0 {
				return m, m.setToast(toastSuccess, fmt.Sprintf("✓ %s: %dms", msg.label, d))
			}
			return m, m.setToast(toastError, fmt.Sprintf("✗ %s: timeout", msg.label))
		}
		if bestName != "" {
			return m, m.setToast(toastSuccess, fmt.Sprintf("✓ %d/%d alive (sorted) • Fastest: %s (%dms)", okCount, len(msg.results), bestName, bestMs))
		}
		return m, m.setToast(toastWarn, fmt.Sprintf("⚠ All %d nodes timed out", len(msg.results)))

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		if m.isFiltering() {
			if msg.String() == "ctrl+c" {
				m.cancel()
				return m, tea.Quit
			}
			return m.updateActiveList(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "Q":
			m.busy = true
			m.setToast(toastBusy, "Powering off daemon, system proxy & menu bar...")
			return m, m.powerOffAndQuitCmd()

		case "1":
			m.activeTab = tabDashboard
			m.savePrefs()
			return m, tea.ClearScreen

		case "2":
			m.activeTab = tabProxies
			m.savePrefs()
			return m, tea.ClearScreen

		case "3":
			m.activeTab = tabSettings
			return m, tea.ClearScreen

		case "p":
			m.busy = true
			label := "Starting proxy daemon..."
			if m.online {
				label = "Stopping proxy daemon..."
			}
			m.setToast(toastBusy, label)
			return m, m.toggleProxyDaemonCmd()

		case "u":
			m.busy = true
			m.setToast(toastBusy, "Toggling TUN mode...")
			return m, m.toggleTunCmd()

		case "a":
			m.busy = true
			m.setToast(toastBusy, "Toggling Allow LAN...")
			return m, m.toggleAllowLanCmd()

		case "s":
			m.busy = true
			m.setToast(toastBusy, "Toggling macOS system proxy...")
			return m, m.toggleSysProxyCmd()

		case "m":
			m.busy = true
			m.setToast(toastBusy, "Switching routing mode...")
			return m, m.cycleModeCmd()

		case "i":
			m.checkingIP = true
			m.setToast(toastBusy, "Checking exit IP...")
			return m, m.fetchExitIPCmd()

		case "r":
			m.busy = true
			m.checkingIP = true
			m.setToast(toastBusy, "Refreshing...")
			return m, tea.Batch(m.loadStateCmd(), m.fetchExitIPCmd())

		case "x":
			m.busy = true
			m.setToast(toastBusy, "Closing active connections & flushing DNS...")
			return m, m.closeAllConnsCmd()
		}

		if m.activeTab == tabProxies {
			switch msg.String() {
			case "tab", "left", "right", "h", "l":
				if m.focus == paneGroups {
					m.setFocus(paneNodes)
				} else {
					m.setFocus(paneGroups)
				}
				return m, nil

			case "enter":
				if m.focus == paneGroups {
					m.setFocus(paneNodes)
					return m, nil
				}
				return m.activateSelectedNode()

			case "t":
				if node, ok := m.nodeList.SelectedItem().(nodeItem); ok {
					m.busy = true
					m.setToast(toastBusy, fmt.Sprintf("Testing %s...", node.name))
					return m, m.testNodesCmd([]string{node.name}, node.name, true, false)
				}
				return m, nil

			case "T":
				if g, ok := m.selectedGroup(); ok && len(g.All) > 0 {
					m.busy = true
					m.setToast(toastBusy, fmt.Sprintf("Testing %d nodes in %s...", len(g.All), g.Name))
					return m, m.testNodesCmd(g.All, g.Name, false, true)
				}
				return m, nil

			case "o":
				m.sortByFast = !m.sortByFast
				m.savePrefs()
				m.rebuildNodeList(true)
				if m.sortByFast {
					return m, m.setToast(toastInfo, "↕ Sorted by speed (saved as default)")
				}
				return m, m.setToast(toastInfo, "↕ Restored default order (saved)")
			}
			return m.updateActiveList(msg)
		}
	}

	return m, nil
}

func (m model) activateSelectedNode() (tea.Model, tea.Cmd) {
	g, ok := m.selectedGroup()
	if !ok {
		return m, nil
	}
	node, ok := m.nodeList.SelectedItem().(nodeItem)
	if !ok {
		return m, nil
	}
	if g.Type != "Selector" {
		m.busy = true
		m.setToast(toastBusy, fmt.Sprintf("Auto group; testing %s...", node.name))
		return m, m.testNodesCmd([]string{node.name}, node.name, true, false)
	}
	g.Now = node.name
	m.proxies[g.Name] = g
	m.rebuildNodeList(true)

	m.busy = true
	m.setToast(toastBusy, fmt.Sprintf("Switching %s → %s...", g.Name, node.name))
	return m, m.selectNodeCmd(g.Name, node.name)
}

func (m model) updateActiveList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.focus == paneGroups {
		prevIdx := m.groupList.Index()
		m.groupList, cmd = m.groupList.Update(msg)
		if m.groupList.Index() != prevIdx {
			m.rebuildNodeList(false)
		}
		return m, cmd
	}
	m.nodeList, cmd = m.nodeList.Update(msg)
	return m, cmd
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	_, _, leftBoxW, _, _ := m.layout()
	leftOuterW := leftBoxW + 2

	if m.activeTab == tabProxies && (msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown) {
		key := tea.KeyMsg{Type: tea.KeyDown}
		if msg.Button == tea.MouseButtonWheelUp {
			key = tea.KeyMsg{Type: tea.KeyUp}
		}
		if msg.X < leftOuterW {
			m.setFocus(paneGroups)
		} else {
			m.setFocus(paneNodes)
		}
		return m.updateActiveList(key)
	}

	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	// Global top nav tabs & Power Off button
	switch {
	case zone.Get("tab:dashboard").InBounds(msg):
		m.activeTab = tabDashboard
		m.savePrefs()
		return m, tea.ClearScreen
	case zone.Get("tab:proxies").InBounds(msg):
		m.activeTab = tabProxies
		m.savePrefs()
		return m, tea.ClearScreen
	case zone.Get("tab:settings").InBounds(msg):
		m.activeTab = tabSettings
		return m, tea.ClearScreen
	case zone.Get("btn:poweroff").InBounds(msg):
		m.busy = true
		m.setToast(toastBusy, "Powering off daemon, system proxy & menu bar...")
		return m, m.powerOffAndQuitCmd()
	}

	// Settings tab controls
	if m.activeTab == tabSettings {
		switch {
		case zone.Get("set:sort:speed").InBounds(msg):
			m.sortByFast = true
			m.savePrefs()
			m.rebuildNodeList(true)
			return m, m.setToast(toastSuccess, "✓ Default node sort saved: Speed ↑")

		case zone.Get("set:sort:default").InBounds(msg):
			m.sortByFast = false
			m.savePrefs()
			m.rebuildNodeList(true)
			return m, m.setToast(toastSuccess, "✓ Default node sort saved: Original")

		case zone.Get("set:start:dashboard").InBounds(msg):
			m.cfg.StartupTab = "dashboard"
			m.savePrefs()
			return m, m.setToast(toastSuccess, "✓ Startup tab saved: Dashboard")

		case zone.Get("set:start:proxies").InBounds(msg):
			m.cfg.StartupTab = "proxies"
			m.savePrefs()
			return m, m.setToast(toastSuccess, "✓ Startup tab saved: Proxies")

		case zone.Get("set:start:last").InBounds(msg):
			m.cfg.StartupTab = "last"
			m.savePrefs()
			return m, m.setToast(toastSuccess, "✓ Startup tab saved: Last Used")

		case zone.Get("set:autotest").InBounds(msg):
			m.cfg.AutoTestOnOpen = !m.cfg.AutoTestOnOpen
			m.savePrefs()
			if m.cfg.AutoTestOnOpen {
				return m, m.setToast(toastSuccess, "✓ Auto-test on open: ON")
			}
			return m, m.setToast(toastInfo, "✓ Auto-test on open: OFF")

		case zone.Get("set:autostart").InBounds(msg):
			if autostart.IsEnabled() {
				if err := autostart.Disable(); err != nil {
					return m, m.setToast(toastError, "✗ "+err.Error())
				}
				return m, m.setToast(toastInfo, "✓ Launch at Login: OFF")
			}
			if err := autostart.Enable(); err != nil {
				return m, m.setToast(toastError, "✗ "+err.Error())
			}
			return m, m.setToast(toastSuccess, "✓ Launch at Login: ON")

		case zone.Get("set:mode:rule").InBounds(msg):
			m.mode = "rule"
			m.savePrefs()
			m.busy = true
			m.setToast(toastBusy, "Switching mode → RULE...")
			return m, m.setModeCmd("rule")

		case zone.Get("set:mode:global").InBounds(msg):
			m.mode = "global"
			m.savePrefs()
			m.busy = true
			m.setToast(toastBusy, "Switching mode → GLOBAL...")
			return m, m.setModeCmd("global")

		case zone.Get("set:mode:direct").InBounds(msg):
			m.mode = "direct"
			m.savePrefs()
			m.busy = true
			m.setToast(toastBusy, "Switching mode → DIRECT...")
			return m, m.setModeCmd("direct")

		case zone.Get("set:tun").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Toggling TUN mode...")
			return m, m.toggleTunCmd()

		case zone.Get("set:sysproxy").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Toggling macOS system proxy...")
			return m, m.toggleSysProxyCmd()

		case zone.Get("set:lan").InBounds(msg):
			cmd := m.toggleAllowLanCmd()
			m.allowLan = !m.allowLan
			m.savePrefs()
			m.busy = true
			m.setToast(toastBusy, "Toggling Allow LAN...")
			return m, cmd
		}
		return m, nil
	}

	// Dashboard inline controls
	if m.activeTab == tabDashboard {
		switch {
		case zone.Get("ctl:proxy").InBounds(msg):
			m.busy = true
			label := "Starting proxy daemon..."
			if m.online {
				label = "Stopping proxy daemon..."
			}
			m.setToast(toastBusy, label)
			return m, m.toggleProxyDaemonCmd()

		case zone.Get("ctl:mode:rule").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Switching mode → RULE...")
			return m, m.setModeCmd("rule")

		case zone.Get("ctl:mode:global").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Switching mode → GLOBAL...")
			return m, m.setModeCmd("global")

		case zone.Get("ctl:mode:direct").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Switching mode → DIRECT...")
			return m, m.setModeCmd("direct")

		case zone.Get("ctl:tun").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Toggling TUN mode...")
			return m, m.toggleTunCmd()

		case zone.Get("ctl:sysproxy").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Toggling macOS system proxy...")
			return m, m.toggleSysProxyCmd()

		case zone.Get("ctl:lan").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Toggling Allow LAN...")
			return m, m.toggleAllowLanCmd()

		case zone.Get("ctl:updatesub").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Updating subscription...")
			return m, m.updateSubCmd()

		case zone.Get("ctl:checkip").InBounds(msg):
			m.checkingIP = true
			m.setToast(toastBusy, "Checking exit IP...")
			return m, m.fetchExitIPCmd()

		case zone.Get("ctl:closeconns").InBounds(msg):
			m.busy = true
			m.setToast(toastBusy, "Closing active connections & flushing DNS...")
			return m, m.closeAllConnsCmd()
		}

		for i, item := range m.groupList.Items() {
			if zone.Get(fmt.Sprintf("dashgrp:%d", i)).InBounds(msg) {
				m.groupList.Select(i)
				m.rebuildNodeList(false)
				m.activeTab = tabProxies
				m.setFocus(paneNodes)
				if g, ok := item.(groupItem); ok {
					return m, m.setToast(toastInfo, fmt.Sprintf("Opened %s (%d nodes)", g.name, g.count))
				}
				return m, nil
			}
		}
		return m, nil
	}

	// Proxies tab toolbar & lists
	switch {
	case zone.Get("btn:sort").InBounds(msg):
		m.sortByFast = !m.sortByFast
		m.savePrefs()
		m.rebuildNodeList(true)
		if m.sortByFast {
			return m, m.setToast(toastInfo, "↕ Sorted by speed (saved as default)")
		}
		return m, m.setToast(toastInfo, "↕ Restored default order (saved)")

	case zone.Get("btn:testnode").InBounds(msg):
		if node, ok := m.nodeList.SelectedItem().(nodeItem); ok {
			m.busy = true
			m.setToast(toastBusy, fmt.Sprintf("Testing %s...", node.name))
			return m, m.testNodesCmd([]string{node.name}, node.name, true, false)
		}
		return m, nil

	case zone.Get("btn:testgroup").InBounds(msg):
		if g, ok := m.selectedGroup(); ok && len(g.All) > 0 {
			m.busy = true
			m.setToast(toastBusy, fmt.Sprintf("Testing %d nodes in %s...", len(g.All), g.Name))
			return m, m.testNodesCmd(g.All, g.Name, false, true)
		}
		return m, nil

	case zone.Get("btn:search").InBounds(msg):
		m.setFocus(paneNodes)
		cmd := m.setToast(toastInfo, "Type to filter nodes, Enter to apply, Esc to clear")
		nm, lcmd := m.updateActiveList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		return nm, tea.Batch(cmd, lcmd)

	case zone.Get("btn:prev").InBounds(msg):
		m.setFocus(paneNodes)
		m.nodeList.Paginator.PrevPage()
		return m, nil

	case zone.Get("btn:next").InBounds(msg):
		m.setFocus(paneNodes)
		m.nodeList.Paginator.NextPage()
		return m, nil
	}

	for i := range m.groupList.VisibleItems() {
		if zone.Get(fmt.Sprintf("group:%d", i)).InBounds(msg) {
			m.setFocus(paneGroups)
			m.groupList.Select(i)
			m.savePrefs()
			m.rebuildNodeList(false)
			return m, nil
		}
	}

	for i := range m.nodeList.VisibleItems() {
		if zone.Get(fmt.Sprintf("node:%d", i)).InBounds(msg) {
			m.setFocus(paneNodes)
			m.nodeList.Select(i)
			return m.activateSelectedNode()
		}
	}

	if msg.Y >= 2 {
		if msg.X < leftOuterW {
			m.setFocus(paneGroups)
		} else {
			m.setFocus(paneNodes)
		}
	}
	return m, nil
}

func (m model) View() string {
	w, _, leftBoxW, rightBoxW, bodyH := m.layout()

	// Row 0: Single-line clean navigation bar (zero duplication)
	dashTab := tabInactiveStyle.Render("1 Dashboard")
	proxTab := tabInactiveStyle.Render("2 Proxies")
	settTab := tabInactiveStyle.Render("3 Settings")
	switch m.activeTab {
	case tabDashboard:
		dashTab = tabActiveStyle.Render("1 Dashboard")
	case tabProxies:
		proxTab = tabActiveStyle.Render("2 Proxies")
	case tabSettings:
		settTab = tabActiveStyle.Render("3 Settings")
	}
	leftNav := fmt.Sprintf(
		"%s   %s  %s  %s",
		badgeStyle.Render("MANTA"),
		zone.Mark("tab:dashboard", dashTab),
		zone.Mark("tab:proxies", proxTab),
		zone.Mark("tab:settings", settTab),
	)

	connBadge := pillOfflineStyle.Render("● OFFLINE")
	if m.online {
		connBadge = pillOnlineStyle.Render("● ONLINE")
	}
	powerBtn := zone.Mark("btn:poweroff", pillOfflineStyle.Render("Power Off"))
	rightNav := fmt.Sprintf(
		"%s  %s  %s  %s",
		connBadge,
		speedUpStyle.Render("↑ "+formatSpeed(m.up)),
		speedDownStyle.Render("↓ "+formatSpeed(m.down)),
		powerBtn,
	)
	header := joinLeftRight(leftNav, rightNav, w)
	divider := headerRuleStyle.Render(strings.Repeat("─", w))

	// Main workspace
	var body string
	switch m.activeTab {
	case tabDashboard:
		body = m.renderDashboard(w, bodyH)
	case tabProxies:
		body = m.renderProxies(w, leftBoxW, rightBoxW, bodyH)
	case tabSettings:
		body = m.renderSettings(w, bodyH)
	}

	// Single smart footer row: transient colored Toast OR contextual key hints
	footer := m.renderFooter(w)

	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, header, divider, body, footer))
}

func (m model) renderFooter(w int) string {
	if m.toastType != toastNone && m.toastText != "" {
		msg := m.toastText
		if m.toastType == toastBusy {
			msg = m.spin.View() + " " + msg
		}
		msg = ansi.Truncate(msg, w-2, "…")
		switch m.toastType {
		case toastBusy:
			return toastBusyStyle.Width(w).Render(msg)
		case toastSuccess:
			return toastSuccessStyle.Width(w).Render(msg)
		case toastWarn:
			return toastWarnStyle.Width(w).Render(msg)
		case toastError:
			return toastErrorStyle.Width(w).Render(msg)
		default:
			return toastInfoStyle.Width(w).Render(msg)
		}
	}

	var hints string
	switch m.activeTab {
	case tabDashboard:
		hints = "1/2/3: Tabs  •  p: Daemon  •  u: TUN  •  s: SysProxy  •  m: Mode  •  i: Check IP  •  q: Close UI  •  Q: Power Off All"
	case tabProxies:
		hints = "1/2/3: Tabs  •  Click/Enter: Switch Node  •  /: Search  •  o: Sort Speed  •  t/T: Test  •  q: Close UI  •  Q: Power Off All"
	case tabSettings:
		hints = "1/2/3: Tabs  •  Click any setting to toggle & auto-save  •  q: Close UI  •  Q: Power Off All"
	}
	return dimStyle.Render(ansi.Truncate(hints, w, "…"))
}

func (m model) renderSettings(totalW, bodyH int) string {
	halfW := (totalW - 5) / 2
	if halfW < 30 {
		halfW = 30
	}
	rightHalfW := (totalW - 5) - halfW
	cardInnerH := bodyH - 2
	if cardInnerH < 8 {
		cardInnerH = 8
	}

	seg := func(id, label string, active bool) string {
		st := segInactiveStyle
		if active {
			st = segActiveStyle
		}
		return zone.Mark(id, st.Render(label))
	}

	// Left Card: UI & Behavior Preferences (compact, single-line rows, zero wrap)
	lw := halfW - 2
	sortSeg := seg("set:sort:speed", "Speed ↑", m.sortByFast) + " " +
		seg("set:sort:default", "Original", !m.sortByFast)

	startTab := strings.ToLower(m.cfg.StartupTab)
	if startTab == "" {
		startTab = "dashboard"
	}
	startSeg := seg("set:start:dashboard", "Dashboard", startTab == "dashboard") + " " +
		seg("set:start:proxies", "Proxies", startTab == "proxies") + " " +
		seg("set:start:last", "Last", startTab == "last")

	autoTestSwitch := zone.Mark("set:autotest", switchOffStyle.Render("○ OFF"))
	if m.cfg.AutoTestOnOpen {
		autoTestSwitch = zone.Mark("set:autotest", switchOnStyle.Render("● ON"))
	}

	autostartSwitch := zone.Mark("set:autostart", switchOffStyle.Render("○ OFF"))
	if autostart.IsEnabled() {
		autostartSwitch = zone.Mark("set:autostart", switchOnStyle.Render("● ON"))
	}

	lastGrp := m.cfg.LastGroup
	if lastGrp == "" {
		lastGrp = "(auto)"
	}
	profile := m.cfg.ActiveProfile
	if profile == "" {
		profile = "(default)"
	}

	leftLines := []string{
		joinLeftRight(cardTitleStyle.Render("Preferences"), dimStyle.Render("auto-saved"), lw),
		dimStyle.Render(strings.Repeat("─", lw)),
		kvLineRaw("Node Sort", sortSeg),
		kvLineRaw("Startup Tab", startSeg),
		kvLineRaw("Auto-Test", autoTestSwitch),
		kvLineRaw("Login Start", autostartSwitch),
		kvLine("Last Group", cleanDisplay(lastGrp), lw),
		kvLine("Custom Rules", fmt.Sprintf("%d active (`manta rule ls`)", len(m.cfg.CustomRules)), lw),
	}
	if len(leftLines) > cardInnerH {
		leftLines = leftLines[:cardInnerH]
	}
	leftCard := cardStyle.Width(halfW).Height(cardInnerH).MaxHeight(cardInnerH + 2).Render(strings.Join(leftLines, "\n"))

	// Right Card: Core & Network Settings (compact, single-line rows, zero wrap)
	rw := rightHalfW - 2
	curMode := strings.ToLower(m.mode)
	if curMode == "" {
		curMode = strings.ToLower(m.cfg.Mode)
	}
	modeSeg := seg("set:mode:rule", "RULE", curMode == "rule") + " " +
		seg("set:mode:global", "GLOBAL", curMode == "global") + " " +
		seg("set:mode:direct", "DIRECT", curMode == "direct")

	tunSwitch := zone.Mark("set:tun", switchOffStyle.Render("○ OFF"))
	if m.tunOn {
		tunSwitch = zone.Mark("set:tun", switchOnStyle.Render("● ON"))
	}

	sysSwitch := dimStyle.Render("macOS only")
	if runtime.GOOS == "darwin" {
		if m.sysProxyOn {
			sysSwitch = zone.Mark("set:sysproxy", switchOnStyle.Render("● ON"))
		} else {
			sysSwitch = zone.Mark("set:sysproxy", switchOffStyle.Render("○ OFF"))
		}
	}

	lanSwitch := zone.Mark("set:lan", switchOffStyle.Render("○ OFF"))
	if m.allowLan {
		lanSwitch = zone.Mark("set:lan", switchOnStyle.Render("● ON"))
	}

	cfgPath := "~/.config/manta/config.yaml"
	if m.mgr != nil && m.mgr.Paths.ConfigFile != "" {
		cfgPath = m.mgr.Paths.ConfigFile
	}

	rightLines := []string{
		joinLeftRight(cardTitleStyle.Render("Core & Network"), dimStyle.Render("mihomo "+m.version), rw),
		dimStyle.Render(strings.Repeat("─", rw)),
		kvLineRaw("Routing Mode", modeSeg),
		kvLineRaw("TUN Capture", tunSwitch),
		kvLineRaw("macOS Proxy", sysSwitch),
		kvLineRaw("Allow LAN", lanSwitch),
		kvLine("Mixed Port", fmt.Sprintf("127.0.0.1:%d (HTTP/SOCKS5)", m.cfg.MixedPort), rw),
		kvLine("Controller", "http://"+m.cfg.Controller, rw),
		kvLine("Config File", cfgPath, rw),
	}
	if len(rightLines) > cardInnerH {
		rightLines = rightLines[:cardInnerH]
	}
	rightCard := cardStyle.Width(rightHalfW).Height(cardInnerH).MaxHeight(cardInnerH + 2).Render(strings.Join(rightLines, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, leftCard, " ", rightCard)
}

func (m model) renderProxies(w, leftBoxW, rightBoxW, bodyH int) string {
	sortLabel := "Sort: Default"
	sortStyle := btnStyle
	if m.sortByFast {
		sortLabel = "Sort: Speed ↑"
		sortStyle = btnAccentStyle
	}
	leftActions := strings.Join([]string{
		zone.Mark("btn:sort", sortStyle.Render(sortLabel)),
		zone.Mark("btn:testnode", btnStyle.Render("Test Node")),
		zone.Mark("btn:testgroup", btnStyle.Render("Test Group")),
		zone.Mark("btn:search", btnStyle.Render("Search /")),
	}, "  ")

	rightPager := ""
	if m.nodeList.Paginator.TotalPages > 1 {
		pageLabel := fmt.Sprintf(" %d / %d ", m.nodeList.Paginator.Page+1, m.nodeList.Paginator.TotalPages)
		rightPager = zone.Mark("btn:prev", btnStyle.Render("◀")) +
			dimStyle.Render(pageLabel) +
			zone.Mark("btn:next", btnStyle.Render("▶"))
	}
	actionRow := joinLeftRight(leftActions, rightPager, w)

	paneInnerH := bodyH - 4
	if paneInnerH < 5 {
		paneInnerH = 5
	}
	leftStyle := paneBorderInactive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	rightStyle := paneBorderInactive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	if m.focus == paneGroups {
		leftStyle = paneBorderActive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	} else {
		rightStyle = paneBorderActive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	}
	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftStyle.Render(m.groupList.View()), " ", rightStyle.Render(m.nodeList.View()))
	return lipgloss.JoinVertical(lipgloss.Left, actionRow, "", panes)
}

func (m model) renderDashboard(totalW, bodyH int) string {
	halfW := (totalW - 5) / 2
	if halfW < 30 {
		halfW = 30
	}
	rightHalfW := (totalW - 5) - halfW

	// Top cards: 8 inner lines (+2 border = 10); Bottom cards: remaining height.
	topInnerH := 8
	botInnerH := bodyH - (topInnerH + 2) - 2
	if botInnerH < 5 {
		botInnerH = 5
	}

	// ── Top-Left Card: Controls & Core (Status == Interactive Switch, zero duplication)
	tlW := halfW - 4
	daemonSwitch := zone.Mark("ctl:proxy", switchOffStyle.Render("○ STOPPED"))
	if m.online {
		daemonSwitch = zone.Mark("ctl:proxy", switchOnStyle.Render("● RUNNING"))
	}

	curMode := strings.ToLower(m.mode)
	seg := func(id, label string, active bool) string {
		st := segInactiveStyle
		if active {
			st = segActiveStyle
		}
		return zone.Mark(id, st.Render(label))
	}
	modeSeg := seg("ctl:mode:rule", "RULE", curMode == "rule") + " " +
		seg("ctl:mode:global", "GLOBAL", curMode == "global") + " " +
		seg("ctl:mode:direct", "DIRECT", curMode == "direct")

	tunSwitch := zone.Mark("ctl:tun", switchOffStyle.Render("○ OFF"))
	if m.tunOn {
		stack := m.tunStack
		if stack == "" {
			stack = "mixed"
		}
		tunSwitch = zone.Mark("ctl:tun", switchOnStyle.Render("● ON • "+stack))
	}

	sysSwitch := dimStyle.Render("macOS only")
	if runtime.GOOS == "darwin" {
		if m.sysProxyOn {
			sysSwitch = zone.Mark("ctl:sysproxy", switchOnStyle.Render(fmt.Sprintf("● ON • :%d", m.cfg.MixedPort)))
		} else {
			sysSwitch = zone.Mark("ctl:sysproxy", switchOffStyle.Render("○ OFF"))
		}
	}

	lanSwitch := zone.Mark("ctl:lan", switchOffStyle.Render("○ OFF"))
	if m.allowLan {
		lanSwitch = zone.Mark("ctl:lan", switchOnStyle.Render("● ON"))
	}

	profile := m.cfg.ActiveProfile
	if profile == "" {
		profile = "(default)"
	}
	quotaTag := ""
	if meta, ok := m.cfg.Profiles[m.cfg.ActiveProfile]; ok {
		if q := meta.QuotaSummary(); q != "" {
			quotaTag = " " + dimStyle.Render("("+q+")")
		}
	}
	profileRow := kvValStyle.Render(profile) + quotaTag + " " + zone.Mark("ctl:updatesub", miniBtnStyle.Render("Update"))

	c1Lines := []string{
		joinLeftRight(cardTitleStyle.Render("Controls & Core"), dimStyle.Render("mihomo "+m.version), tlW),
		dimStyle.Render(strings.Repeat("─", tlW)),
		kvLineRaw("Daemon", daemonSwitch),
		kvLineRaw("Mode", modeSeg),
		kvLineRaw("TUN Capture", tunSwitch),
		kvLineRaw("SysProxy", sysSwitch),
		kvLineRaw("Allow LAN", lanSwitch),
		kvLineRaw("Profile", profileRow),
	}
	card1 := cardStyle.Width(halfW).Height(topInnerH).MaxHeight(topInnerH + 2).Render(strings.Join(c1Lines, "\n"))

	// ── Top-Right Card: Network & Exit (Exit IP + Geo + Main Route + Traffic)
	trW := rightHalfW - 4
	_, chain, exitDelay := m.resolveActiveExit()
	ipVal := dimStyle.Render("Checking...")
	locVal := dimStyle.Render("-")
	ispVal := dimStyle.Render("-")
	if m.exitIP != nil {
		ipVal = kvValStyle.Render(m.exitIP.IP)
		locParts := []string{}
		if m.exitIP.Country != "" {
			locParts = append(locParts, m.exitIP.Country)
		}
		if m.exitIP.City != "" {
			locParts = append(locParts, m.exitIP.City)
		}
		locVal = kvValStyle.Render(strings.Join(locParts, ", "))
		ispVal = rowTextStyle.Render(m.exitIP.ISP)
	} else if !m.checkingIP && m.exitIPErr != "" {
		ipVal = delayBadStyle.Render("Lookup failed")
		locVal = dimStyle.Render(m.exitIPErr)
	}

	routeVal := chain
	if exitDelay > 0 {
		routeVal += " (" + strings.TrimSpace(formatDelayPlain(exitDelay)) + ")"
	}
	totalsVal := fmt.Sprintf(
		"%s  %s  %s",
		speedUpStyle.Render("↑"+formatBytes(m.totalUp)),
		speedDownStyle.Render("↓"+formatBytes(m.totalDown)),
		dimStyle.Render(fmt.Sprintf("• 127.0.0.1:%d", m.cfg.MixedPort)),
	)
	spark := renderSparkline(m.downHist, trW-28)
	peakLabel := ""
	if m.peakDown > 0 {
		peakLabel = " " + dimStyle.Render("peak "+formatSpeed(m.peakDown))
	}
	checkIPBtn := zone.Mark("ctl:checkip", miniBtnStyle.Render("Check IP"))

	c2Lines := []string{
		joinLeftRight(cardTitleStyle.Render("Network & Exit"), checkIPBtn, trW),
		dimStyle.Render(strings.Repeat("─", trW)),
		kvLine("Exit IP", ipVal, trW),
		kvLine("Location", locVal, trW),
		kvLine("ISP", ispVal, trW),
		kvLine("Main Route", routeVal, trW),
		kvLine("Session", totalsVal, trW),
		kvLine("Speed Graph", speedDownStyle.Render(spark)+peakLabel, trW),
	}
	card2 := cardStyle.Width(rightHalfW).Height(topInnerH).MaxHeight(topInnerH + 2).Render(strings.Join(c2Lines, "\n"))

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, card1, " ", card2)

	// ── Bottom-Left Card: Clickable Proxy Groups Overview
	blInnerW := halfW - 4
	blCap := botInnerH - 2
	if blCap < 1 {
		blCap = 1
	}
	blLines := []string{
		joinLeftRight(cardTitleStyle.Render("Proxy Groups"), dimStyle.Render(fmt.Sprintf("%d groups • click to switch", len(m.groupList.Items()))), blInnerW),
		dimStyle.Render(strings.Repeat("─", blInnerW)),
	}
	for i, item := range m.groupList.Items() {
		if i >= blCap {
			break
		}
		g, ok := item.(groupItem)
		if !ok {
			continue
		}
		delayStr := formatDelayStyled(m.finalDelay(g.now))
		nowMax := blInnerW - 22 - lipgloss.Width(delayStr)
		if nowMax < 8 {
			nowMax = 8
		}
		leftPart := rowTextStyle.Render(ansi.Truncate(cleanDisplay(g.name), 16, "…")) + dimStyle.Render(" -> ") + exitLabelStyle.Render(ansi.Truncate(cleanDisplay(g.now), nowMax, "…"))
		blLines = append(blLines, zone.Mark(fmt.Sprintf("dashgrp:%d", i), joinLeftRight(leftPart, delayStr, blInnerW)))
	}
	card3 := cardStyle.Width(halfW).Height(botInnerH).MaxHeight(botInnerH + 2).Render(strings.Join(blLines, "\n"))

	// ── Bottom-Right Card: Live Active Connections
	brInnerW := rightHalfW - 4
	brCap := botInnerH - 2
	if brCap < 1 {
		brCap = 1
	}
	closeConnsBtn := zone.Mark("ctl:closeconns", miniBtnStyle.Render("Close All"))
	brHeaderRight := dimStyle.Render(fmt.Sprintf("%d active ", len(m.conns))) + closeConnsBtn
	brLines := []string{
		joinLeftRight(cardTitleStyle.Render("Live Connections"), brHeaderRight, brInnerW),
		dimStyle.Render(strings.Repeat("─", brInnerW)),
	}
	if len(m.conns) == 0 {
		brLines = append(brLines, dimStyle.Render("(no active connections)"))
	} else {
		sorted := make([]api.ConnectionItem, len(m.conns))
		copy(sorted, m.conns)
		sort.SliceStable(sorted, func(i, j int) bool {
			return (sorted[i].Download + sorted[i].Upload) > (sorted[j].Download + sorted[j].Upload)
		})
		for i, c := range sorted {
			if i >= brCap {
				break
			}
			traffic := formatBytes(c.Download + c.Upload)
			out := cleanDisplay(c.Outbound())
			outStyled := exitLabelStyle.Render(ansi.Truncate(out, 16, "…"))
			if strings.EqualFold(out, "DIRECT") {
				outStyled = activeMarkStyle.Render("DIRECT")
			} else if strings.EqualFold(out, "REJECT") {
				outStyled = delayBadStyle.Render("REJECT")
			}
			rightPart := outStyled + " " + dimStyle.Render(fmt.Sprintf("%7s", traffic))
			targetMax := brInnerW - lipgloss.Width(rightPart) - 2
			if targetMax < 10 {
				targetMax = 10
			}
			leftPart := rowTextStyle.Render(ansi.Truncate(c.Target(), targetMax, "…"))
			brLines = append(brLines, joinLeftRight(leftPart, rightPart, brInnerW))
		}
	}
	card4 := cardStyle.Width(rightHalfW).Height(botInnerH).MaxHeight(botInnerH + 2).Render(strings.Join(brLines, "\n"))

	botRow := lipgloss.JoinHorizontal(lipgloss.Top, card3, " ", card4)
	return lipgloss.JoinVertical(lipgloss.Left, topRow, botRow)
}

// cleanDisplay normalizes strings for deterministic terminal cell-width rendering:
//   1. Converts 2-rune Regional Indicator flag emojis (🇯🇵, 🇸🇬, 🇺🇸, 🇹🇼, etc.) into crisp
//      ASCII country tags ([JP], [SG], [US], [TW]) so terminals that allocate 4 cells per
//      flag pair never overflow lipgloss's 2-cell calculation or render tofu boxes (☒).
//   2. Strips Variation Selectors (U+FE0F / U+FE0E) and zero-width joiners (U+200D).
func cleanDisplay(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\uFE0F' || r == '\uFE0E' || r == '\u200D' {
			continue
		}
		if r >= 0x1F1E6 && r <= 0x1F1FF && i+1 < len(runes) {
			r2 := runes[i+1]
			if r2 >= 0x1F1E6 && r2 <= 0x1F1FF {
				c1 := byte('A' + (r - 0x1F1E6))
				c2 := byte('A' + (r2 - 0x1F1E6))
				b.WriteByte('[')
				b.WriteByte(c1)
				b.WriteByte(c2)
				b.WriteByte(']')
				i++
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func kvLine(k, v string, maxW int) string {
	label := kvKeyStyle.Render(fmt.Sprintf("%-13s", k+":"))
	avail := maxW - lipgloss.Width(label) - 1
	if avail < 6 {
		avail = 6
	}
	return label + " " + ansi.Truncate(v, avail, "…")
}

// kvLineRaw renders a key-value row without truncating v (preserves bubblezone markers).
func kvLineRaw(k, v string) string {
	label := kvKeyStyle.Render(fmt.Sprintf("%-13s", k+":"))
	return label + " " + v
}

func renderSparkline(samples []int64, width int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	if width < 8 {
		width = 8
	}
	if width > 44 {
		width = 44
	}
	if len(samples) == 0 {
		return strings.Repeat("▁", width)
	}
	var maxV int64 = 1
	for _, s := range samples {
		if s > maxV {
			maxV = s
		}
	}
	start := 0
	if len(samples) > width {
		start = len(samples) - width
	}
	var b strings.Builder
	for i := 0; i < width-len(samples[start:]); i++ {
		b.WriteRune('▁')
	}
	for _, s := range samples[start:] {
		idx := int(s * int64(len(bars)-1) / maxV)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		b.WriteRune(bars[idx])
	}
	return b.String()
}

func joinLeftRight(left, right string, totalW int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := totalW - lw - rw
	if gap < 1 {
		avail := totalW - rw - 1
		if avail < 0 {
			avail = 0
		}
		left = ansi.Truncate(left, avail, "…")
		lw = lipgloss.Width(left)
		gap = totalW - lw - rw
		if gap < 0 {
			gap = 0
		}
	}
	return left + strings.Repeat(" ", gap) + right
}

func formatSpeed(bps int64) string {
	return formatBytes(bps) + "/s"
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatDelayStyled(ms int) string {
	switch {
	case ms < 0:
		return delayBadStyle.Render("▫▫▫ timeout")
	case ms == 0:
		return dimStyle.Render("          -")
	case ms < 150:
		return delayGoodStyle.Render(fmt.Sprintf("▪▪▪ %5dms", ms))
	case ms < 400:
		return delayMedStyle.Render(fmt.Sprintf("▪▪▫ %5dms", ms))
	default:
		return delayBadStyle.Render(fmt.Sprintf("▪▫▫ %5dms", ms))
	}
}

func formatDelayPlain(ms int) string {
	switch {
	case ms < 0:
		return "▫▫▫ timeout"
	case ms == 0:
		return "          -"
	case ms < 150:
		return fmt.Sprintf("▪▪▪ %5dms", ms)
	case ms < 400:
		return fmt.Sprintf("▪▪▫ %5dms", ms)
	default:
		return fmt.Sprintf("▪▫▫ %5dms", ms)
	}
}
