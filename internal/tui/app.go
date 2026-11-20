package tui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"manta/internal/api"
	"manta/internal/config"
)

type pane int

const (
	paneGroups pane = iota
	paneNodes
)

// Messages
type stateLoadedMsg struct {
	version string
	mode    string
	proxies map[string]api.ProxyInfo
	err     error
}

type trafficStartedMsg struct {
	ch <-chan api.TrafficSample
}

type trafficSampleMsg struct {
	sample api.TrafficSample
	ch     <-chan api.TrafficSample
}

type actionResultMsg struct {
	status string
	err    error
	reload bool
}

type delayResultMsg struct {
	results map[string]int // -1 indicates timeout/error
	status  string
}

// Adaptive styles for both light (white) and dark terminals.
var (
	badgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#0EA5E9"}).
			Padding(0, 1)

	headerMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#334155", Dark: "#CBD5E1"})

	speedUpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"}).
			Bold(true)

	speedDownStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#38BDF8"}).
			Bold(true)

	btnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1E293B", Dark: "#E2E8F0"}).
			Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#334155"}).
			Padding(0, 1)

	btnAccentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#0EA5E9"}).
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
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#0EA5E9"})

	selectedRowInactiveStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"}).
					Background(lipgloss.AdaptiveColor{Light: "#E2E8F0", Dark: "#334155"})

	activeMarkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"})

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"})

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"}).
			Bold(true)

	delayGoodStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#059669", Dark: "#34D399"})

	delayMedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#FBBF24"})

	delayBadStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#F87171"})
)

// List item implementations for bubbles/list.
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

// Compact single-line delegate for groups (with bubblezone mouse marks).
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
	fmt.Fprint(w, zone.Mark("group:"+g.name, line))
}

// Compact single-line delegate for nodes (with right-aligned latency & bubblezone mouse marks).
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
	fmt.Fprint(w, zone.Mark("node:"+n.name, line))
}

type model struct {
	cfg    *config.AppConfig
	client *api.Client
	ctx    context.Context
	cancel context.CancelFunc

	width  int
	height int

	version string
	mode    string
	up      int64
	down    int64

	proxies map[string]api.ProxyInfo
	delays  map[string]int

	focus         pane
	groupsFocused *bool
	nodesFocused  *bool
	groupList     list.Model
	nodeList      list.Model
	spin          spinner.Model
	busy          bool

	statusLine string
	errLine    string
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
	l.Styles.TitleBar = lipgloss.NewStyle().Padding(0, 0, 0, 0)
	l.Styles.StatusBar = dimStyle.Padding(0, 0, 0, 0)
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
		statusLine:    "Loading mihomo state...",
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadStateCmd(), m.startTrafficCmd(), m.spin.Tick)
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
			version: ver.Version,
			mode:    liveCfg.Mode,
			proxies: proxies,
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
			status: fmt.Sprintf("Switched %s → %s", group, node),
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
			status: fmt.Sprintf("Mode switched to %s", next),
			reload: true,
		}
	}
}

func (m model) testNodesCmd(nodes []string, label string) tea.Cmd {
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
			results: results,
			status:  fmt.Sprintf("Latency test complete (%s)", label),
		}
	}
}

func (m *model) setFocus(p pane) {
	m.focus = p
	*m.groupsFocused = (p == paneGroups)
	*m.nodesFocused = (p == paneNodes)
}

func (m *model) syncSizes() {
	w, h, leftBoxW, rightBoxW, paneInnerH := m.layout()
	_ = w
	_ = h
	m.groupList.SetSize(leftBoxW-2, paneInnerH)
	m.nodeList.SetSize(rightBoxW-2, paneInnerH)
}

func (m model) layout() (w, h, leftBoxW, rightBoxW, paneInnerH int) {
	w = m.width
	if w < 64 {
		w = 80
	}
	h = m.height
	if h < 14 {
		h = 24
	}
	paneInnerH = h - 6
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

	m.nodeList.Title = fmt.Sprintf("%s [%s]", g.Name, g.Type)
	items := make([]list.Item, 0, len(g.All))
	targetIdx := 0
	for i, name := range g.All {
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
			m.errLine = fmt.Sprintf("Controller error: %v (is `manta up` running?)", msg.err)
			return m, nil
		}
		m.errLine = ""
		m.version = msg.version
		m.mode = msg.mode
		m.proxies = msg.proxies

		prevGroup := ""
		if sel, ok := m.groupList.SelectedItem().(groupItem); ok {
			prevGroup = sel.name
		}

		var names []string
		for name, p := range msg.proxies {
			if p.Type == "Selector" || p.Type == "URLTest" || p.Type == "Fallback" {
				names = append(names, name)
			}
			if len(p.History) > 0 {
				if _, exists := m.delays[name]; !exists {
					m.delays[name] = p.History[len(p.History)-1].Delay
				}
			}
		}
		sort.Strings(names)

		items := make([]list.Item, 0, len(names))
		groupIdx := 0
		for i, name := range names {
			p := msg.proxies[name]
			items = append(items, groupItem{
				name:  name,
				kind:  p.Type,
				now:   p.Now,
				count: len(p.All),
			})
			if name == prevGroup {
				groupIdx = i
			}
		}
		m.groupList.SetItems(items)
		if groupIdx < len(items) {
			m.groupList.Select(groupIdx)
		}
		m.rebuildNodeList(true)
		if m.statusLine == "Loading mihomo state..." {
			m.statusLine = fmt.Sprintf("Ready • %d groups loaded", len(names))
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
			m.errLine = msg.err.Error()
			return m, nil
		}
		m.errLine = ""
		m.statusLine = msg.status
		if msg.reload {
			return m, m.loadStateCmd()
		}
		return m, nil

	case delayResultMsg:
		m.busy = false
		for k, v := range msg.results {
			m.delays[k] = v
		}
		m.rebuildNodeList(true)
		m.statusLine = msg.status
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		// While typing in bubbles/list's `/` filter input, forward keys directly to the active list.
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
			g, ok := m.selectedGroup()
			if !ok {
				return m, nil
			}
			node, ok := m.nodeList.SelectedItem().(nodeItem)
			if !ok {
				return m, nil
			}
			if g.Type != "Selector" {
				m.statusLine = fmt.Sprintf("Group %s (%s) is managed automatically", g.Name, g.Type)
				return m, nil
			}
			m.busy = true
			m.statusLine = fmt.Sprintf("Switching %s → %s...", g.Name, node.name)
			return m, m.selectNodeCmd(g.Name, node.name)

		case "t":
			if node, ok := m.nodeList.SelectedItem().(nodeItem); ok {
				m.busy = true
				m.statusLine = fmt.Sprintf("Testing %s...", node.name)
				return m, m.testNodesCmd([]string{node.name}, node.name)
			}
			return m, nil

		case "T":
			if g, ok := m.selectedGroup(); ok && len(g.All) > 0 {
				m.busy = true
				m.statusLine = fmt.Sprintf("Testing %d nodes in %s...", len(g.All), g.Name)
				return m, m.testNodesCmd(g.All, g.Name)
			}
			return m, nil

		case "m":
			m.busy = true
			m.statusLine = "Switching mode..."
			return m, m.cycleModeCmd()

		case "r":
			m.busy = true
			m.statusLine = "Refreshing..."
			return m, m.loadStateCmd()
		}

		return m.updateActiveList(msg)
	}

	return m, nil
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
	// Wheel scroll uses pane zones.
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		key := tea.KeyMsg{Type: tea.KeyDown}
		if msg.Button == tea.MouseButtonWheelUp {
			key = tea.KeyMsg{Type: tea.KeyUp}
		}
		if zone.Get("pane:groups").InBounds(msg) {
			m.setFocus(paneGroups)
			return m.updateActiveList(key)
		}
		if zone.Get("pane:nodes").InBounds(msg) {
			m.setFocus(paneNodes)
			return m.updateActiveList(key)
		}
		return m, nil
	}

	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	// Top action buttons via bubblezone.
	switch {
	case zone.Get("btn:mode").InBounds(msg):
		m.busy = true
		m.statusLine = "Switching mode..."
		return m, m.cycleModeCmd()
	case zone.Get("btn:test").InBounds(msg):
		if g, ok := m.selectedGroup(); ok && len(g.All) > 0 {
			m.busy = true
			m.statusLine = fmt.Sprintf("Testing %d nodes in %s...", len(g.All), g.Name)
			return m, m.testNodesCmd(g.All, g.Name)
		}
		return m, nil
	case zone.Get("btn:refresh").InBounds(msg):
		m.busy = true
		m.statusLine = "Refreshing..."
		return m, m.loadStateCmd()
	case zone.Get("btn:quit").InBounds(msg):
		m.cancel()
		return m, tea.Quit
	}

	// Click on a group item.
	for i, item := range m.groupList.VisibleItems() {
		if g, ok := item.(groupItem); ok && zone.Get("group:"+g.name).InBounds(msg) {
			m.setFocus(paneGroups)
			m.groupList.Select(i)
			m.rebuildNodeList(false)
			return m, nil
		}
	}

	// Click on a node item: focus and immediately switch if Selector.
	for i, item := range m.nodeList.VisibleItems() {
		if n, ok := item.(nodeItem); ok && zone.Get("node:"+n.name).InBounds(msg) {
			m.setFocus(paneNodes)
			m.nodeList.Select(i)
			if g, ok := m.selectedGroup(); ok {
				if g.Type != "Selector" {
					m.statusLine = fmt.Sprintf("Group %s (%s) is managed automatically", g.Name, g.Type)
					return m, nil
				}
				m.busy = true
				m.statusLine = fmt.Sprintf("Switching %s → %s...", g.Name, n.name)
				return m, m.selectNodeCmd(g.Name, n.name)
			}
			return m, nil
		}
	}

	// Focus pane if clicked on empty area inside pane.
	if zone.Get("pane:groups").InBounds(msg) {
		m.setFocus(paneGroups)
	} else if zone.Get("pane:nodes").InBounds(msg) {
		m.setFocus(paneNodes)
	}
	return m, nil
}

func (m model) View() string {
	w, _, leftBoxW, rightBoxW, paneInnerH := m.layout()

	// Row 0: Header
	profile := m.cfg.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	leftHeader := fmt.Sprintf(
		"%s  %s",
		badgeStyle.Render("MANTA"),
		headerMetaStyle.Render(fmt.Sprintf("mihomo %s  •  %s  •  127.0.0.1:%d", m.version, profile, m.cfg.MixedPort)),
	)
	rightHeader := fmt.Sprintf("%s  %s", speedUpStyle.Render("↑ "+formatSpeed(m.up)), speedDownStyle.Render("↓ "+formatSpeed(m.down)))
	header := joinLeftRight(leftHeader, rightHeader, w)

	// Row 1: Action buttons (marked with bubblezone)
	mode := m.mode
	if mode == "" {
		mode = m.cfg.Mode
	}
	actionBar := strings.Join([]string{
		zone.Mark("btn:mode", btnAccentStyle.Render("Mode: "+strings.ToUpper(mode))),
		zone.Mark("btn:test", btnStyle.Render("Test Group")),
		zone.Mark("btn:refresh", btnStyle.Render("Refresh")),
		zone.Mark("btn:quit", btnStyle.Render("Quit")),
	}, " ")

	// Row 2..: Two-pane bubbles/list body
	leftStyle := paneBorderInactive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	rightStyle := paneBorderInactive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	if m.focus == paneGroups {
		leftStyle = paneBorderActive.Width(leftBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	} else {
		rightStyle = paneBorderActive.Width(rightBoxW).Height(paneInnerH).MaxHeight(paneInnerH + 2)
	}

	leftPane := zone.Mark("pane:groups", leftStyle.Render(m.groupList.View()))
	rightPane := zone.Mark("pane:nodes", rightStyle.Render(m.nodeList.View()))
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, " ", rightPane)

	// Footer
	statusText := m.statusLine
	if m.busy {
		statusText = m.spin.View() + " " + statusText
	}
	status := dimStyle.Render(ansi.Truncate(statusText, w, "…"))
	if m.errLine != "" {
		status = errStyle.Render(ansi.Truncate(m.errLine, w, "…"))
	}
	help := dimStyle.Render(ansi.Truncate("Click/Scroll: select  •  /: search  •  Tab: pane  •  Enter: switch  •  t/T: test  •  m: mode  •  r: refresh  •  q: quit", w, "…"))

	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, header, actionBar, body, status, help))
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
