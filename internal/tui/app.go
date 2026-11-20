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

type tabView int

const (
	tabDashboard tabView = iota
	tabProxies
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
	status       string
	kind         toastKind
	err          error
	reload       bool
	refreshIP    bool
	sysProxyOn   *bool
}

type delayResultMsg struct {
	results  map[string]int // -1 indicates timeout/error
	label    string
	single   bool
	autoSort bool
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

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#0EA5E9"}).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"}).
				Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#1E293B"}).
				Padding(0, 1)

	headerMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"})

	exitLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0369A1", Dark: "#38BDF8"})

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "#CBD5E1", Dark: "#475569"}).
			Padding(0, 1)

	cardTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"})

	kvKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"})

	kvValStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"})

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

	activeTab tabView

	online     bool
	version    string
	mode       string
	tunOn      bool
	tunStack   string
	sysProxyOn bool
	sortByFast bool

	up        int64
	down      int64
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

// Run launches the interactive manta terminal dashboard.
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
		activeTab:     tabDashboard,
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
		toastText:     "Connecting to mihomo & checking exit IP...",
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
	return tea.Batch(m.loadStateCmd(), m.fetchExitIPCmd(), m.startTrafficCmd(), m.pollConnsTickCmd(), m.spin.Tick)
}

func (m model) pollConnsTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		conns, err := m.client.GetConnections()
		if err != nil {
			return connsPollMsg{}
		}
		return connsPollMsg{
			conns:     conns.Connections,
			totalUp:   conns.UploadTotal,
			totalDown: conns.DownloadTotal,
		}
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
			status:    fmt.Sprintf("✓ Switched %s → %s (refreshing exit IP...)", group, node),
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
	return func() tea.Msg {
		if err := m.client.SetMode(next); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{
			status:    fmt.Sprintf("✓ Routing mode switched to %s", strings.ToUpper(next)),
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
	_, _, leftBoxW, rightBoxW, paneInnerH := m.layout()
	m.groupList.SetSize(leftBoxW-2, paneInnerH)
	m.nodeList.SetSize(rightBoxW-2, paneInnerH)
}

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

func (m model) primarySelectorGroup() (api.ProxyInfo, bool) {
	// Prefer the main proxy selector group (e.g. 🚀节点选择 / PROXY / Proxy) when on Dashboard.
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
		m.tunOn = msg.tunOn
		m.tunStack = msg.tunStack
		m.sysProxyOn = msg.sysProxyOn
		m.conns = msg.conns
		m.totalUp = msg.totalUp
		m.totalDown = msg.totalDown
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

	case exitIPLoadedMsg:
		m.checkingIP = false
		if msg.err != nil {
			m.exitIPErr = msg.err.Error()
			m.setToast(toastWarn, "⚠ Exit IP lookup failed: "+msg.err.Error())
			return m, nil
		}
		m.exitIPErr = ""
		m.exitIP = msg.info
		loc := strings.TrimSpace(strings.Join([]string{msg.info.Country, msg.info.City}, ", "))
		m.setToast(toastSuccess, fmt.Sprintf("✓ Exit IP: %s (%s • %s)", msg.info.IP, loc, msg.info.ISP))
		return m, nil

	case connsPollMsg:
		if msg.conns != nil {
			m.conns = msg.conns
			m.totalUp = msg.totalUp
			m.totalDown = msg.totalDown
		}
		return m, m.pollConnsTickCmd()

	case trafficStartedMsg:
		return m, waitTrafficCmd(msg.ch)

	case trafficSampleMsg:
		m.up = msg.sample.Up
		m.down = msg.sample.Down
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
			m.setToast(toastError, "✗ "+msg.err.Error())
			return m, nil
		}
		m.setToast(msg.kind, msg.status)
		if msg.sysProxyOn != nil {
			m.sysProxyOn = *msg.sysProxyOn
		}
		var cmds []tea.Cmd
		if msg.reload {
			cmds = append(cmds, m.loadStateCmd())
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
			m.setToast(toastSuccess, fmt.Sprintf("✓ Tested %s (%d/%d alive, sorted) • Fastest: %s (%dms)", msg.label, okCount, len(msg.results), bestName, bestMs))
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

		case "1":
			m.activeTab = tabDashboard
			m.setToast(toastInfo, "Viewing Dashboard")
			return m, nil

		case "2":
			m.activeTab = tabProxies
			m.setToast(toastInfo, "Viewing Proxies")
			return m, nil

		case "i":
			m.checkingIP = true
			m.setToast(toastBusy, "Checking exit IP & geolocation...")
			return m, m.fetchExitIPCmd()

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
			m.checkingIP = true
			m.setToast(toastBusy, "Refreshing state & exit IP...")
			return m, tea.Batch(m.loadStateCmd(), m.fetchExitIPCmd())
		}

		// Proxies-tab specific keys
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
				m.rebuildNodeList(true)
				if m.sortByFast {
					m.setToast(toastInfo, "↕ Sorted nodes by speed (fastest first)")
				} else {
					m.setToast(toastInfo, "↕ Restored default node order")
				}
				return m, nil
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
		m.setToast(toastBusy, fmt.Sprintf("%s is auto-managed; testing %s...", g.Name, node.name))
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

	// Global clickable tabs & toolbar buttons
	switch {
	case zone.Get("tab:dashboard").InBounds(msg):
		m.activeTab = tabDashboard
		m.setToast(toastInfo, "Viewing Dashboard")
		return m, nil

	case zone.Get("tab:proxies").InBounds(msg):
		m.activeTab = tabProxies
		m.setToast(toastInfo, "Viewing Proxies")
		return m, nil

	case zone.Get("btn:mode").InBounds(msg):
		m.busy = true
		m.setToast(toastBusy, "Switching routing mode...")
		return m, m.cycleModeCmd()

	case zone.Get("btn:sysproxy").InBounds(msg):
		m.busy = true
		m.setToast(toastBusy, "Toggling macOS system proxy...")
		return m, m.toggleSysProxyCmd()

	case zone.Get("btn:checkip").InBounds(msg):
		m.checkingIP = true
		m.setToast(toastBusy, "Checking exit IP & geolocation...")
		return m, m.fetchExitIPCmd()

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
		m.activeTab = tabProxies
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
		m.checkingIP = true
		m.setToast(toastBusy, "Refreshing state & exit IP...")
		return m, tea.Batch(m.loadStateCmd(), m.fetchExitIPCmd())

	case zone.Get("btn:quit").InBounds(msg):
		m.cancel()
		return m, tea.Quit
	}

	if m.activeTab != tabProxies {
		// Dashboard: clicking a group in the Groups Overview jumps directly to that group in Proxies tab.
		for i, item := range m.groupList.Items() {
			if zone.Get(fmt.Sprintf("dashgrp:%d", i)).InBounds(msg) {
				m.groupList.Select(i)
				m.rebuildNodeList(false)
				m.activeTab = tabProxies
				m.setFocus(paneNodes)
				if g, ok := item.(groupItem); ok {
					m.setToast(toastInfo, fmt.Sprintf("Jumped to %s (%d nodes)", g.name, g.count))
				}
				return m, nil
			}
		}
		return m, nil
	}

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

	// Row 0: Header with tabs, online badge, and live speed
	connBadge := pillOfflineStyle.Render("● OFFLINE")
	if m.online {
		connBadge = pillOnlineStyle.Render("● ONLINE")
	}
	dashTab := tabInactiveStyle.Render("1 Dashboard")
	proxTab := tabInactiveStyle.Render("2 Proxies")
	if m.activeTab == tabDashboard {
		dashTab = tabActiveStyle.Render("1 Dashboard")
	} else {
		proxTab = tabActiveStyle.Render("2 Proxies")
	}
	leftHeader := fmt.Sprintf(
		"%s %s  %s %s",
		badgeStyle.Render("MANTA"),
		connBadge,
		zone.Mark("tab:dashboard", dashTab),
		zone.Mark("tab:proxies", proxTab),
	)
	rightHeader := fmt.Sprintf("%s  %s", speedUpStyle.Render("↑ "+formatSpeed(m.up)), speedDownStyle.Render("↓ "+formatSpeed(m.down)))
	header := joinLeftRight(leftHeader, rightHeader, w)

	// Row 1: Quick exit & capture summary
	_, chain, exitDelay := m.resolveActiveExit()
	delayTag := ""
	if exitDelay > 0 {
		delayTag = " (" + strings.TrimSpace(formatDelayPlain(exitDelay)) + ")"
	}
	ipBrief := ""
	if m.exitIP != nil {
		ipBrief = fmt.Sprintf(" • %s (%s)", m.exitIP.IP, m.exitIP.CountryCode)
	}
	exitSummary := ansi.Truncate(
		fmt.Sprintf("%s %s%s", exitLabelStyle.Render("Exit:"), rowTextStyle.Render(chain+delayTag), dimStyle.Render(ipBrief)),
		w, "…",
	)

	// Row 2: Action toolbar
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
	buttons := []string{
		zone.Mark("btn:mode", btnAccentStyle.Render("Mode: "+strings.ToUpper(mode))),
	}
	if runtime.GOOS == "darwin" {
		buttons = append(buttons, zone.Mark("btn:sysproxy", sysStyle.Render(sysLabel)))
	}
	if m.activeTab == tabDashboard {
		buttons = append(buttons,
			zone.Mark("btn:checkip", btnStyle.Render("Check Exit IP")),
		)
	} else {
		sortLabel := "Sort: Default"
		sortStyle := btnStyle
		if m.sortByFast {
			sortLabel = "Sort: Speed ↑"
			sortStyle = btnAccentStyle
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
	}
	buttons = append(buttons,
		zone.Mark("btn:refresh", btnStyle.Render("Refresh")),
		zone.Mark("btn:quit", btnStyle.Render("Quit")),
	)
	actionBar := strings.Join(buttons, " ")

	// Row 3..: Main content (Dashboard OR Proxies)
	var body string
	if m.activeTab == tabDashboard {
		body = m.renderDashboard(w, paneInnerH)
	} else {
		leftStyle := paneBorderInactive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
		rightStyle := paneBorderInactive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
		if m.focus == paneGroups {
			leftStyle = paneBorderActive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
		} else {
			rightStyle = paneBorderActive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, leftStyle.Render(m.groupList.View()), " ", rightStyle.Render(m.nodeList.View()))
	}

	// Footer: Toast bar
	ts := m.toastTime.Format("15:04:05")
	toastMsg := fmt.Sprintf("[%s] %s", ts, m.toastText)
	if m.busy || m.checkingIP {
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

	help := dimStyle.Render(ansi.Truncate("1/2: Dashboard/Proxies  •  i: check IP  •  s: sysproxy  •  m: mode  •  o: sort speed  •  /: search  •  r: refresh  •  q: quit", w, "…"))

	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, header, exitSummary, actionBar, body, toastRendered, help))
}

func (m model) renderDashboard(totalW, paneInnerH int) string {
	// Compact 2x2 grid:
	//   Top-Left:  System, Capture Mode & Traffic totals
	//   Top-Right: Exit IP, Geolocation & Speed Sparkline
	//   Bot-Left:  Proxy Groups Overview (clickable -> jumps to Proxies tab)
	//   Bot-Right: Live Active Connections (auto-refreshed every 2s)
	halfW := (totalW - 5) / 2
	if halfW < 28 {
		halfW = 28
	}
	rightHalfW := (totalW - 5) - halfW

	topCardInnerH := 8
	botCardInnerH := paneInnerH - topCardInnerH - 2
	if botCardInnerH < 5 {
		botCardInnerH = 5
	}

	// Top-Left Card: System, Capture Mode & Traffic
	tunStatus := dimStyle.Render("○ OFF (`sudo manta up --tun`)")
	if m.tunOn {
		stack := m.tunStack
		if stack == "" {
			stack = "mixed"
		}
		tunStatus = activeMarkStyle.Render("● ON (utun • " + stack + ")")
	}
	sysStatus := dimStyle.Render("○ OFF")
	if m.sysProxyOn {
		sysStatus = activeMarkStyle.Render(fmt.Sprintf("● ON (127.0.0.1:%d)", m.cfg.MixedPort))
	}
	profile := m.cfg.ActiveProfile
	if profile == "" {
		profile = "(default)"
	}
	trafficSummary := fmt.Sprintf(
		"%s %s  %s %s",
		speedUpStyle.Render("↑"+formatBytes(m.totalUp)),
		dimStyle.Render("|"),
		speedDownStyle.Render("↓"+formatBytes(m.totalDown)),
		dimStyle.Render(fmt.Sprintf("(%d conns)", len(m.conns))),
	)
	c1Lines := []string{
		cardTitleStyle.Render("System & Capture Mode"),
		kvLine("Core", fmt.Sprintf("mihomo %s (%s)", m.version, profile), halfW-2),
		kvLine("Routing Mode", strings.ToUpper(m.mode), halfW-2),
		kvLine("TUN Mode", tunStatus, halfW-2),
		kvLine("SysProxy", sysStatus, halfW-2),
		kvLine("Local Inbound", fmt.Sprintf("127.0.0.1:%d (HTTP/SOCKS5)", m.cfg.MixedPort), halfW-2),
		kvLine("Total Traffic", trafficSummary, halfW-2),
	}
	card1 := cardStyle.Width(halfW).Height(topCardInnerH).MaxHeight(topCardInnerH + 2).Render(strings.Join(c1Lines, "\n"))

	// Top-Right Card: Exit IP, Geolocation & Speed Sparkline
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
		if m.exitIP.Region != "" && m.exitIP.Region != m.exitIP.City {
			locParts = append(locParts, m.exitIP.Region)
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
	spark := renderSparkline(m.downHist, rightHalfW-18)
	c2Lines := []string{
		cardTitleStyle.Render("Exit IP & Geolocation"),
		kvLine("Exit IP", ipVal, rightHalfW-2),
		kvLine("Location", locVal, rightHalfW-2),
		kvLine("ISP", ispVal, rightHalfW-2),
		kvLine("Active Chain", chain, rightHalfW-2),
		kvLine("Exit Latency", formatDelayStyled(exitDelay), rightHalfW-2),
		kvLine("Down Speed", speedDownStyle.Render(spark), rightHalfW-2),
	}
	card2 := cardStyle.Width(rightHalfW).Height(topCardInnerH).MaxHeight(topCardInnerH + 2).Render(strings.Join(c2Lines, "\n"))

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, card1, " ", card2)

	// Bottom-Left Card: Clickable Proxy Groups Overview
	blInnerW := halfW - 2
	blCap := botCardInnerH - 2
	if blCap < 1 {
		blCap = 1
	}
	blLines := []string{
		joinLeftRight(cardTitleStyle.Render("Proxy Groups (click to open)"), dimStyle.Render(fmt.Sprintf("%d groups", len(m.groupList.Items()))), blInnerW),
		dimStyle.Render(strings.Repeat("─", blInnerW)),
	}
	allGroups := m.groupList.Items()
	for i, item := range allGroups {
		if i >= blCap {
			break
		}
		g, ok := item.(groupItem)
		if !ok {
			continue
		}
		d := m.finalDelay(g.now)
		delayStr := formatDelayStyled(d)
		nowMax := blInnerW - 22 - lipgloss.Width(delayStr)
		if nowMax < 8 {
			nowMax = 8
		}
		leftPart := rowTextStyle.Render(ansi.Truncate(g.name, 16, "…")) + dimStyle.Render(" → ") + exitLabelStyle.Render(ansi.Truncate(g.now, nowMax, "…"))
		row := joinLeftRight(leftPart, delayStr, blInnerW)
		blLines = append(blLines, zone.Mark(fmt.Sprintf("dashgrp:%d", i), row))
	}
	card3 := cardStyle.Width(halfW).Height(botCardInnerH).MaxHeight(botCardInnerH + 2).Render(strings.Join(blLines, "\n"))

	// Bottom-Right Card: Live Active Connections
	brInnerW := rightHalfW - 2
	brCap := botCardInnerH - 2
	if brCap < 1 {
		brCap = 1
	}
	brLines := []string{
		joinLeftRight(cardTitleStyle.Render("Live Connections"), dimStyle.Render(fmt.Sprintf("%d active", len(m.conns))), brInnerW),
		dimStyle.Render(strings.Repeat("─", brInnerW)),
	}
	if len(m.conns) == 0 {
		brLines = append(brLines, dimStyle.Render("(no active connections)"))
	} else {
		// Sort by total bytes descending so highest-traffic connections appear on top.
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
			out := c.Outbound()
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
	card4 := cardStyle.Width(rightHalfW).Height(botCardInnerH).MaxHeight(botCardInnerH + 2).Render(strings.Join(brLines, "\n"))

	botRow := lipgloss.JoinHorizontal(lipgloss.Top, card3, " ", card4)

	return lipgloss.JoinVertical(lipgloss.Left, topRow, botRow)
}

func kvLine(k, v string, maxW int) string {
	label := kvKeyStyle.Render(fmt.Sprintf("%-14s", k+":"))
	avail := maxW - lipgloss.Width(label) - 1
	if avail < 6 {
		avail = 6
	}
	return label + " " + ansi.Truncate(v, avail, "…")
}

func renderSparkline(samples []int64, width int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	if width < 8 {
		width = 8
	}
	if width > 48 {
		width = 48
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
