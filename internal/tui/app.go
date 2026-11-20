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

	"manta/internal/api"
	"manta/internal/config"
	"manta/internal/sysproxy"
)

type pane int

const (
	paneGroups pane = iota
	paneNodes
)

type toastKind int

const (
	toastInfo toastKind = iota
	toastBusy
	toastSuccess
	toastWarn
	toastError
)

// Messages
type stateLoadedMsg struct {
	version    string
	mode       string
	proxies    map[string]api.ProxyInfo
	sysProxyOn bool
	err        error
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
	sysProxyOn *bool
}

type delayResultMsg struct {
	results   map[string]int // -1 indicates timeout/error
	label     string
	single    bool
	autoSort  bool
}

// Adaptive styles for both light (white) and dark terminals.
var (
	badgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#0EA5E9"}).
			Padding(0, 1)

	pillOnlineStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#065F46", Dark: "#D1FAE5"}).
			Background(lipgloss.AdaptiveColor{Light: "#D1FAE5", Dark: "#065F46"}).
			Padding(0, 1)

	pillOfflineStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#991B1B", Dark: "#FEE2E2"}).
				Background(lipgloss.AdaptiveColor{Light: "#FEE2E2", Dark: "#991B1B"}).
				Padding(0, 1)

	headerMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"})

	exitLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#38BDF8"})

	speedUpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"}).
			Bold(true)

	speedDownStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#38BDF8"}).
			Bold(true)

	btnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1E293B", Dark: "#E2E8F0"}).
			Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#334155"}).
			Padding(0, 1)

	btnAccentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#E0F2FE"}).
			Background(lipgloss.AdaptiveColor{Light: "#E0F2FE", Dark: "#0369A1"}).
			Padding(0, 1)

	btnOnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#065F46", Dark: "#D1FAE5"}).
			Background(lipgloss.AdaptiveColor{Light: "#D1FAE5", Dark: "#065F46"}).
			Padding(0, 1)

	paneBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#38BDF8"}).
				Padding(0, 1)

	paneBorderInactive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.AdaptiveColor{Light: "#CBD5E1", Dark: "#475569"}).
				Padding(0, 1)

	rowTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1E293B", Dark: "#E2E8F0"})

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#FFFFFF"}).
				Background(lipgloss.AdaptiveColor{Light: "#BAE6FD", Dark: "#0369A1"})

	selectedRowInactiveStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"}).
					Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#334155"})

	activeMarkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"})

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"})

	// High-visibility feedback toast styles
	toastInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"}).
			Background(lipgloss.AdaptiveColor{Light: "#F1F5F9", Dark: "#1E293B"}).
			Padding(0, 1)

	toastBusyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#E0F2FE"}).
			Background(lipgloss.AdaptiveColor{Light: "#E0F2FE", Dark: "#0C4A6E"}).
			Padding(0, 1)

	toastSuccessStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#065F46", Dark: "#D1FAE5"}).
				Background(lipgloss.AdaptiveColor{Light: "#D1FAE5", Dark: "#064E3B"}).
				Padding(0, 1)

	toastWarnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#92400E", Dark: "#FEF3C7"}).
			Background(lipgloss.AdaptiveColor{Light: "#FEF3C7", Dark: "#78350F"}).
			Padding(0, 1)

	toastErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#991B1B", Dark: "#FEE2E2"}).
			Background(lipgloss.AdaptiveColor{Light: "#FEE2E2", Dark: "#7F1D1D"}).
			Padding(0, 1)

	delayGoodStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"})

	delayMedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FBBF24"})

	delayBadStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"})
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

// Delegates (leaf bubblezone markers only — never nested).
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
	countStr := fmt.Sprintf("%d", g.count)
	nameMax := width - len(countStr) - 3
	if nameMax < 6 {
		nameMax = 6
	}
	name := ansi.Truncate(g.name, nameMax, "…")

	var line string
	if index == m.Index() {
		plain := joinLeftRight("▸ "+name, countStr, width)
		if d.focused != nil && *d.focused {
			line = selectedRowStyle.Render(plain)
		} else {
			line = selectedRowInactiveStyle.Render(plain)
		}
	} else {
		line = joinLeftRight(rowTextStyle.Render("  "+name), dimStyle.Render(countStr), width)
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
	nameMax := width - 2 - 2 - lipgloss.Width(delayPlain)
	if nameMax < 8 {
		nameMax = 8
	}
	name := ansi.Truncate(n.name, nameMax, "…")

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
	client *api.Client
	ctx    context.Context
	cancel context.CancelFunc

	width  int
	height int

	online     bool
	version    string
	mode       string
	sysProxyOn bool
	sortByFast bool
	up         int64
	down       int64

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
	toastTime time.Time
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

// Run launches the interactive manta terminal dashboard built with bubbles & bubblezone.
func Run(cfg *config.AppConfig, client *api.Client) error {
	zone.NewGlobal()
	defer zone.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gf, nf := true, false
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = speedDownStyle

	m := model{
		cfg:           cfg,
		client:        client,
		ctx:           ctx,
		cancel:        cancel,
		proxies:       make(map[string]api.ProxyInfo),
		delays:        make(map[string]int),
		focus:         paneGroups,
		groupsFocused: &gf,
		nodesFocused:  &nf,
		groupList:     newStyledList("Proxy Groups", groupDelegate{focused: &gf}),
		nodeList:      newStyledList("Nodes", nodeDelegate{focused: &nf}),
		spin:          sp,
		busy:          true,
		toastText:     "Connecting to mihomo controller...",
		toastType:     toastBusy,
		toastTime:     time.Now(),
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *model) setToast(kind toastKind, text string) {
	m.toastType = kind
	m.toastText = text
	m.toastTime = time.Now()
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadStateCmd(), m.startTrafficCmd(), m.spin.Tick)
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
		return stateLoadedMsg{
			version:    ver.Version,
			mode:       liveCfg.Mode,
			proxies:    proxies,
			sysProxyOn: detectSysProxy(),
		}
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
				status:     fmt.Sprintf("✓ System proxy ON [%s] → 127.0.0.1:%d", strings.Join(services, ", "), port),
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
			status: fmt.Sprintf("✓ Switched %s → %s", group, node),
			kind:   toastSuccess,
			reload: true,
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
	return func() tea.Msg {
		if err := m.client.SetMode(next); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{
			status: fmt.Sprintf("✓ Routing mode switched to %s", strings.ToUpper(next)),
			kind:   toastSuccess,
			reload: true,
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
	_, _, leftBoxW, rightBoxW, paneInnerH := m.layout()
	m.groupList.SetSize(leftBoxW-2, paneInnerH)
	m.nodeList.SetSize(rightBoxW-2, paneInnerH)
}

// Vertical budget:
//   Row 0: Header (ONLINE badge, mihomo, speed)
//   Row 1: Current Active Exit & Routing summary line
//   Row 2: Action buttons
//   Row 3..3+paneInnerH+1: Bordered panes (paneInnerH + 2 lines)
//   Row +2: High-visibility Toast feedback bar
//   Row +3: Help line
// Total = paneInnerH + 7.
func (m model) layout() (w, h, leftBoxW, rightBoxW, paneInnerH int) {
	w = m.width
	if w < 64 {
		w = 80
	}
	h = m.height
	if h < 15 {
		h = 24
	}
	paneInnerH = h - 7
	if paneInnerH < 6 {
		paneInnerH = 6
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

// resolveActiveExit traces the active exit node for the current group (or primary selector),
// following nested groups (e.g. 🚀节点选择 → ♻️自动选择 → 🇯🇵AWS日本01) to show the real exit node.
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
	if g, ok := m.selectedGroup(); ok && g.Now != "" {
		return g.Name, m.traceChain(g.Name, g.Now), m.finalDelay(g.Now)
	}
	return "", "(none)", 0
}

func (m model) traceChain(startGroup, first string) string {
	parts := []string{startGroup}
	cur := first
	seen := map[string]bool{startGroup: true}
	for cur != "" && !seen[cur] && len(parts) < 4 {
		seen[cur] = true
		parts = append(parts, cur)
		p, ok := m.proxies[cur]
		if !ok || p.Now == "" {
			break
		}
		cur = p.Now
	}
	return strings.Join(parts, " → ")
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

// delayRank orders nodes when sorting by speed:
//   positive latency (fastest first) < untested (0) < timeout (-1).
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
		sortTag = " • Sorted by Speed ↑"
	}
	m.nodeList.Title = fmt.Sprintf("%s  [%s • %s%s]", g.Name, g.Type, badge, sortTag)

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
	return m.groupList.FilterState() == list.Filtering || m.nodeList.FilterState() == list.Filtering
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

	case stateLoadedMsg:
		m.busy = false
		if msg.err != nil {
			m.online = false
			m.setToast(toastError, fmt.Sprintf("✗ Offline: %v (run `manta up`)", msg.err))
			return m, nil
		}
		m.online = true
		m.version = msg.version
		m.mode = msg.mode
		m.sysProxyOn = msg.sysProxyOn
		m.proxies = msg.proxies

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
		for i, name := range ordered {
			p := msg.proxies[name]
			items = append(items, groupItem{
				name:  name,
				kind:  p.Type,
				now:   p.Now,
				count: len(p.All),
			})
			if m.initialized && name == prevGroup {
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
			m.setToast(toastSuccess, fmt.Sprintf("✓ Connected to mihomo %s • %d groups ready", m.version, len(ordered)))
		}
		return m, nil

	case trafficStartedMsg:
		return m, waitTrafficCmd(msg.ch)

	case trafficSampleMsg:
		m.up = msg.sample.Up
		m.down = msg.sample.Down
		return m, waitTrafficCmd(msg.ch)

	case actionResultMsg:
		m.busy = false
		if msg.err != nil {
			m.setToast(toastError, "✗ "+msg.err.Error())
			return m, nil
		}
		m.setToast(msg.kind, msg.status)
		if msg.sysProxyOn != nil {
			m.sysProxyOn = *msg.sysProxyOn
		}
		if msg.reload {
			return m, m.loadStateCmd()
		}
		return m, nil

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
		}
		m.rebuildNodeList(!msg.autoSort)
		if msg.single {
			d := msg.results[msg.label]
			if d > 0 {
				m.setToast(toastSuccess, fmt.Sprintf("✓ %s: %dms", msg.label, d))
			} else {
				m.setToast(toastError, fmt.Sprintf("✗ %s: timeout", msg.label))
			}
		} else if bestName != "" {
			m.setToast(toastSuccess, fmt.Sprintf("✓ Tested %s (%d/%d alive, sorted by speed) • Fastest: %s (%dms)", msg.label, okCount, len(msg.results), bestName, bestMs))
		} else {
			m.setToast(toastWarn, fmt.Sprintf("⚠ Tested %s: all %d nodes timed out", msg.label, len(msg.results)))
		}
		return m, nil

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
			m.rebuildNodeList(true)
			if m.sortByFast {
				m.setToast(toastInfo, "↕ Sorted nodes by speed (fastest first)")
			} else {
				m.setToast(toastInfo, "↕ Restored default node order")
			}
			return m, nil

		case "s":
			m.busy = true
			m.setToast(toastBusy, "Toggling macOS system proxy...")
			return m, m.toggleSysProxyCmd()

		case "m":
			m.busy = true
			m.setToast(toastBusy, "Switching routing mode...")
			return m, m.cycleModeCmd()

		case "r":
			m.busy = true
			m.setToast(toastBusy, "Refreshing state...")
			return m, m.loadStateCmd()
		}

		return m.updateActiveList(msg)
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
		m.setToast(toastBusy, fmt.Sprintf("%s is auto-managed; testing %s...", g.Name, node.name))
		return m, m.testNodesCmd([]string{node.name}, node.name, true, false)
	}
	// Optimistic UI update so the green dot moves immediately on click.
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

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
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

	switch {
	case zone.Get("btn:mode").InBounds(msg):
		m.busy = true
		m.setToast(toastBusy, "Switching routing mode...")
		return m, m.cycleModeCmd()

	case zone.Get("btn:sysproxy").InBounds(msg):
		m.busy = true
		m.setToast(toastBusy, "Toggling macOS system proxy...")
		return m, m.toggleSysProxyCmd()

	case zone.Get("btn:sort").InBounds(msg):
		m.sortByFast = !m.sortByFast
		m.rebuildNodeList(true)
		if m.sortByFast {
			m.setToast(toastInfo, "↕ Sorted nodes by speed (fastest first)")
		} else {
			m.setToast(toastInfo, "↕ Restored default node order")
		}
		return m, nil

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
		m.setToast(toastInfo, "Type to filter nodes, Enter to apply, Esc to clear")
		return m.updateActiveList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	case zone.Get("btn:prev").InBounds(msg):
		m.setFocus(paneNodes)
		m.nodeList.Paginator.PrevPage()
		m.setToast(toastInfo, fmt.Sprintf("Page %d / %d", m.nodeList.Paginator.Page+1, m.nodeList.Paginator.TotalPages))
		return m, nil

	case zone.Get("btn:next").InBounds(msg):
		m.setFocus(paneNodes)
		m.nodeList.Paginator.NextPage()
		m.setToast(toastInfo, fmt.Sprintf("Page %d / %d", m.nodeList.Paginator.Page+1, m.nodeList.Paginator.TotalPages))
		return m, nil

	case zone.Get("btn:refresh").InBounds(msg):
		m.busy = true
		m.setToast(toastBusy, "Refreshing state...")
		return m, m.loadStateCmd()

	case zone.Get("btn:quit").InBounds(msg):
		m.cancel()
		return m, tea.Quit
	}

	// Click on group row
	for i, item := range m.groupList.VisibleItems() {
		if zone.Get(fmt.Sprintf("group:%d", i)).InBounds(msg) {
			m.setFocus(paneGroups)
			m.groupList.Select(i)
			m.rebuildNodeList(false)
			if g, ok := item.(groupItem); ok {
				m.setToast(toastInfo, fmt.Sprintf("Viewing %s (%s • active: %s)", g.name, g.kind, g.now))
			}
			return m, nil
		}
	}

	// Click on node row
	for i := range m.nodeList.VisibleItems() {
		if zone.Get(fmt.Sprintf("node:%d", i)).InBounds(msg) {
			m.setFocus(paneNodes)
			m.nodeList.Select(i)
			return m.activateSelectedNode()
		}
	}

	if msg.Y >= 3 {
		if msg.X < leftOuterW {
			m.setFocus(paneGroups)
		} else {
			m.setFocus(paneNodes)
		}
	}
	return m, nil
}

func (m model) View() string {
	w, _, leftBoxW, rightBoxW, paneInnerH := m.layout()

	// Row 0: Header with live ONLINE/OFFLINE badge & real-time speeds
	profile := m.cfg.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	connBadge := pillOfflineStyle.Render("● OFFLINE")
	if m.online {
		connBadge = pillOnlineStyle.Render("● ONLINE")
	}
	leftHeader := fmt.Sprintf(
		"%s %s  %s",
		badgeStyle.Render("MANTA"),
		connBadge,
		headerMetaStyle.Render(fmt.Sprintf("mihomo %s • %s • 127.0.0.1:%d", m.version, profile, m.cfg.MixedPort)),
	)
	rightHeader := fmt.Sprintf("%s  %s", speedUpStyle.Render("↑ "+formatSpeed(m.up)), speedDownStyle.Render("↓ "+formatSpeed(m.down)))
	header := joinLeftRight(leftHeader, rightHeader, w)

	// Row 1: Live active exit summary so current state is crystal clear at a glance
	_, chain, exitDelay := m.resolveActiveExit()
	delayTag := ""
	if exitDelay > 0 {
		delayTag = " (" + strings.TrimSpace(formatDelayPlain(exitDelay)) + ")"
	}
	captureMode := "Local Proxy Only"
	if m.sysProxyOn {
		captureMode = "macOS SysProxy ON"
	}
	exitSummary := fmt.Sprintf(
		"%s %s  %s",
		exitLabelStyle.Render("Active Exit:"),
		rowTextStyle.Render(chain+delayTag),
		dimStyle.Render("["+captureMode+"]"),
	)
	exitSummary = ansi.Truncate(exitSummary, w, "…")

	// Row 2: Clickable action toolbar
	mode := m.mode
	if mode == "" {
		mode = m.cfg.Mode
	}
	sysLabel := "SysProxy: OFF"
	sysStyle := btnStyle
	if m.sysProxyOn {
		sysLabel = "SysProxy: ON"
		sysStyle = btnOnStyle
	}
	sortLabel := "Sort: Default"
	sortStyle := btnStyle
	if m.sortByFast {
		sortLabel = "Sort: Speed ↑"
		sortStyle = btnAccentStyle
	}
	buttons := []string{
		zone.Mark("btn:mode", btnAccentStyle.Render("Mode: "+strings.ToUpper(mode))),
	}
	if runtime.GOOS == "darwin" {
		buttons = append(buttons, zone.Mark("btn:sysproxy", sysStyle.Render(sysLabel)))
	}
	buttons = append(buttons,
		zone.Mark("btn:sort", sortStyle.Render(sortLabel)),
		zone.Mark("btn:testnode", btnStyle.Render("Test Node")),
		zone.Mark("btn:testgroup", btnStyle.Render("Test Group")),
		zone.Mark("btn:search", btnStyle.Render("Search /")),
	)
	if m.nodeList.Paginator.TotalPages > 1 {
		pageLabel := fmt.Sprintf("%d/%d", m.nodeList.Paginator.Page+1, m.nodeList.Paginator.TotalPages)
		buttons = append(buttons,
			zone.Mark("btn:prev", btnStyle.Render("◀")),
			dimStyle.Render(pageLabel),
			zone.Mark("btn:next", btnStyle.Render("▶")),
		)
	}
	buttons = append(buttons,
		zone.Mark("btn:refresh", btnStyle.Render("Refresh")),
		zone.Mark("btn:quit", btnStyle.Render("Quit")),
	)
	actionBar := strings.Join(buttons, " ")

	// Row 3..: Two-pane body
	leftStyle := paneBorderInactive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	rightStyle := paneBorderInactive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	if m.focus == paneGroups {
		leftStyle = paneBorderActive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	} else {
		rightStyle = paneBorderActive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftStyle.Render(m.groupList.View()), " ", rightStyle.Render(m.nodeList.View()))

	// Footer: High-visibility Toast feedback bar with timestamp
	ts := m.toastTime.Format("15:04:05")
	toastMsg := fmt.Sprintf("[%s] %s", ts, m.toastText)
	if m.busy {
		toastMsg = fmt.Sprintf("[%s] %s %s", ts, m.spin.View(), m.toastText)
	}
	toastMsg = ansi.Truncate(toastMsg, w-2, "…")
	var toastRendered string
	switch m.toastType {
	case toastBusy:
		toastRendered = toastBusyStyle.Width(w).Render(toastMsg)
	case toastSuccess:
		toastRendered = toastSuccessStyle.Width(w).Render(toastMsg)
	case toastWarn:
		toastRendered = toastWarnStyle.Width(w).Render(toastMsg)
	case toastError:
		toastRendered = toastErrorStyle.Width(w).Render(toastMsg)
	default:
		toastRendered = toastInfoStyle.Width(w).Render(toastMsg)
	}

	help := dimStyle.Render(ansi.Truncate("Click/Scroll: select  •  o: sort speed  •  /: search  •  s: sysproxy  •  m: mode  •  t/T: test  •  r: refresh  •  q: quit", w, "…"))

	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, header, exitSummary, actionBar, body, toastRendered, help))
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
	switch {
	case bps >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", float64(bps)/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.1f KB/s", float64(bps)/(1<<10))
	default:
		return fmt.Sprintf("%d B/s", bps)
	}
}

func formatDelayStyled(ms int) string {
	switch {
	case ms < 0:
		return delayBadStyle.Render("timeout")
	case ms == 0:
		return dimStyle.Render("   -   ")
	case ms < 150:
		return delayGoodStyle.Render(fmt.Sprintf("%4dms", ms))
	case ms < 400:
		return delayMedStyle.Render(fmt.Sprintf("%4dms", ms))
	default:
		return delayBadStyle.Render(fmt.Sprintf("%4dms", ms))
	}
}

func formatDelayPlain(ms int) string {
	switch {
	case ms < 0:
		return "timeout"
	case ms == 0:
		return "   -   "
	default:
		return fmt.Sprintf("%4dms", ms)
	}
}
