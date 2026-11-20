package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

type buttonHitbox struct {
	id    string
	x0    int
	x1    int // exclusive
	label string
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
	groups  []string
	delays  map[string]int

	focus      pane
	groupIdx   int
	nodeIdx    int
	statusLine string
	errLine    string
}

// Styles
var (
	badgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(lipgloss.Color("#38BDF8")).
			Padding(0, 1)

	headerMetaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CBD5E1"))

	speedUpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#34D399")).
			Bold(true)

	speedDownStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#38BDF8")).
			Bold(true)

	btnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(lipgloss.Color("#334155")).
			Padding(0, 1)

	paneBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#38BDF8")).
				Padding(0, 1)

	paneBorderInactive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#475569")).
				Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F8FAFC"))

	selectedRowStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#0F172A")).
				Background(lipgloss.Color("#38BDF8"))

	activeMarkStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#34D399"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F87171")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))
)

// Run launches the interactive manta terminal dashboard with keyboard and mouse support.
func Run(cfg *config.AppConfig, client *api.Client) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := model{
		cfg:        cfg,
		client:     client,
		ctx:        ctx,
		cancel:     cancel,
		proxies:    make(map[string]api.ProxyInfo),
		delays:     make(map[string]int),
		statusLine: "Loading mihomo state...",
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadStateCmd(), m.startTrafficCmd())
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
			status: fmt.Sprintf("Switched %s -> %s", group, node),
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

func (m model) currentGroup() (api.ProxyInfo, bool) {
	if len(m.groups) == 0 || m.groupIdx < 0 || m.groupIdx >= len(m.groups) {
		return api.ProxyInfo{}, false
	}
	p, ok := m.proxies[m.groups[m.groupIdx]]
	return p, ok
}

func (m model) layout() (w, h, leftWidth, rightWidth, bodyHeight int) {
	w = m.width
	if w < 60 {
		w = 80
	}
	h = m.height
	if h < 16 {
		h = 24
	}
	bodyHeight = h - 6
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	leftWidth = w / 3
	if leftWidth < 24 {
		leftWidth = 24
	}
	rightWidth = w - leftWidth - 6
	if rightWidth < 30 {
		rightWidth = 30
	}
	return
}

func (m model) actionButtons() (string, []buttonHitbox) {
	mode := m.mode
	if mode == "" {
		mode = m.cfg.Mode
	}
	specs := []struct {
		id    string
		label string
	}{
		{"mode", fmt.Sprintf("Mode: %s", mode)},
		{"test", "Test Group"},
		{"refresh", "Refresh"},
		{"quit", "Quit"},
	}

	var parts []string
	var boxes []buttonHitbox
	x := 0
	for i, s := range specs {
		if i > 0 {
			parts = append(parts, " ")
			x++
		}
		rendered := btnStyle.Render(s.label)
		bw := lipgloss.Width(rendered)
		boxes = append(boxes, buttonHitbox{id: s.id, x0: x, x1: x + bw, label: s.label})
		parts = append(parts, rendered)
		x += bw
	}
	return strings.Join(parts, ""), boxes
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case stateLoadedMsg:
		if msg.err != nil {
			m.errLine = fmt.Sprintf("Controller error: %v (is `manta up` running?)", msg.err)
			return m, nil
		}
		m.errLine = ""
		m.version = msg.version
		m.mode = msg.mode
		m.proxies = msg.proxies

		var groups []string
		for name, p := range msg.proxies {
			if p.Type == "Selector" || p.Type == "URLTest" || p.Type == "Fallback" {
				groups = append(groups, name)
			}
			if len(p.History) > 0 {
				if _, exists := m.delays[name]; !exists {
					m.delays[name] = p.History[len(p.History)-1].Delay
				}
			}
		}
		sort.Strings(groups)
		m.groups = groups
		if m.groupIdx >= len(m.groups) {
			m.groupIdx = 0
		}
		if g, ok := m.currentGroup(); ok && m.nodeIdx >= len(g.All) {
			m.nodeIdx = 0
		}
		if m.statusLine == "Loading mihomo state..." {
			m.statusLine = fmt.Sprintf("Loaded %d groups (click or use arrow keys)", len(m.groups))
		}
		return m, nil

	case trafficStartedMsg:
		return m, waitTrafficCmd(msg.ch)

	case trafficSampleMsg:
		m.up = msg.sample.Up
		m.down = msg.sample.Down
		return m, waitTrafficCmd(msg.ch)

	case actionResultMsg:
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
		for k, v := range msg.results {
			m.delays[k] = v
		}
		m.statusLine = msg.status
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "tab", "left", "right", "h", "l":
			if m.focus == paneGroups {
				m.focus = paneNodes
			} else {
				m.focus = paneGroups
			}
			return m, nil

		case "up", "k":
			if m.focus == paneGroups {
				if m.groupIdx > 0 {
					m.groupIdx--
					m.nodeIdx = 0
				}
			} else {
				if m.nodeIdx > 0 {
					m.nodeIdx--
				}
			}
			return m, nil

		case "down", "j":
			if m.focus == paneGroups {
				if m.groupIdx < len(m.groups)-1 {
					m.groupIdx++
					m.nodeIdx = 0
				}
			} else if g, ok := m.currentGroup(); ok {
				if m.nodeIdx < len(g.All)-1 {
					m.nodeIdx++
				}
			}
			return m, nil

		case "enter":
			g, ok := m.currentGroup()
			if !ok || len(g.All) == 0 {
				return m, nil
			}
			if m.focus == paneGroups {
				m.focus = paneNodes
				return m, nil
			}
			if g.Type != "Selector" {
				m.statusLine = fmt.Sprintf("Group %s (%s) is managed automatically", g.Name, g.Type)
				return m, nil
			}
			target := g.All[m.nodeIdx]
			m.statusLine = fmt.Sprintf("Switching %s -> %s...", g.Name, target)
			return m, m.selectNodeCmd(g.Name, target)

		case "t":
			if g, ok := m.currentGroup(); ok && len(g.All) > 0 && m.nodeIdx < len(g.All) {
				target := g.All[m.nodeIdx]
				m.statusLine = fmt.Sprintf("Testing %s...", target)
				return m, m.testNodesCmd([]string{target}, target)
			}
			return m, nil

		case "T":
			if g, ok := m.currentGroup(); ok && len(g.All) > 0 {
				m.statusLine = fmt.Sprintf("Testing %d nodes in %s...", len(g.All), g.Name)
				return m, m.testNodesCmd(g.All, g.Name)
			}
			return m, nil

		case "m":
			m.statusLine = "Switching mode..."
			return m, m.cycleModeCmd()

		case "r":
			m.statusLine = "Refreshing..."
			return m, m.loadStateCmd()
		}
	}

	return m, nil
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	_, _, leftWidth, _, bodyHeight := m.layout()
	// Outer width of left pane is leftWidth + 2 (left/right border).
	leftOuterWidth := leftWidth + 2

	// Handle scroll wheel anywhere over left or right pane.
	if msg.Button == tea.MouseButtonWheelUp {
		if msg.X < leftOuterWidth {
			m.focus = paneGroups
			if m.groupIdx > 0 {
				m.groupIdx--
				m.nodeIdx = 0
			}
		} else {
			m.focus = paneNodes
			if m.nodeIdx > 0 {
				m.nodeIdx--
			}
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		if msg.X < leftOuterWidth {
			m.focus = paneGroups
			if m.groupIdx < len(m.groups)-1 {
				m.groupIdx++
				m.nodeIdx = 0
			}
		} else if g, ok := m.currentGroup(); ok {
			m.focus = paneNodes
			if m.nodeIdx < len(g.All)-1 {
				m.nodeIdx++
			}
		}
		return m, nil
	}

	// Only act on left button press.
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	// Row 1: top clickable action buttons.
	if msg.Y == 1 {
		_, boxes := m.actionButtons()
		for _, b := range boxes {
			if msg.X >= b.x0 && msg.X < b.x1 {
				switch b.id {
				case "mode":
					m.statusLine = "Switching mode..."
					return m, m.cycleModeCmd()
				case "test":
					if g, ok := m.currentGroup(); ok && len(g.All) > 0 {
						m.statusLine = fmt.Sprintf("Testing %d nodes in %s...", len(g.All), g.Name)
						return m, m.testNodesCmd(g.All, g.Name)
					}
				case "refresh":
					m.statusLine = "Refreshing..."
					return m, m.loadStateCmd()
				case "quit":
					m.cancel()
					return m, tea.Quit
				}
			}
		}
		return m, nil
	}

	// Panes start at Y=2 (top border), title at Y=3, list items start at Y=4.
	itemStartY := 4
	maxRows := bodyHeight - 2
	if msg.Y < itemStartY || msg.Y >= itemStartY+maxRows-1 {
		// Clicking inside pane header/border still focuses that pane.
		if msg.Y >= 2 && msg.Y < 2+bodyHeight+2 {
			if msg.X < leftOuterWidth {
				m.focus = paneGroups
			} else {
				m.focus = paneNodes
			}
		}
		return m, nil
	}

	clickedOffset := msg.Y - itemStartY
	if msg.X < leftOuterWidth {
		m.focus = paneGroups
		start, end := visibleWindow(m.groupIdx, len(m.groups), maxRows-1)
		idx := start + clickedOffset
		if idx >= start && idx < end {
			m.groupIdx = idx
			m.nodeIdx = 0
		}
		return m, nil
	}

	// Right pane: click a node to focus and immediately select it (for Selector groups).
	m.focus = paneNodes
	g, ok := m.currentGroup()
	if !ok || len(g.All) == 0 {
		return m, nil
	}
	start, end := visibleWindow(m.nodeIdx, len(g.All), maxRows-1)
	idx := start + clickedOffset
	if idx >= start && idx < end {
		m.nodeIdx = idx
		if g.Type != "Selector" {
			m.statusLine = fmt.Sprintf("Group %s (%s) is managed automatically", g.Name, g.Type)
			return m, nil
		}
		target := g.All[idx]
		m.statusLine = fmt.Sprintf("Switching %s -> %s...", g.Name, target)
		return m, m.selectNodeCmd(g.Name, target)
	}
	return m, nil
}

func (m model) View() string {
	_, _, leftWidth, rightWidth, bodyHeight := m.layout()

	// Row 0: Header
	profile := m.cfg.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	header := fmt.Sprintf(
		"%s  %s  %s %s",
		badgeStyle.Render("MANTA"),
		headerMetaStyle.Render(fmt.Sprintf("mihomo %s  |  profile:%s  |  127.0.0.1:%d", m.version, profile, m.cfg.MixedPort)),
		speedUpStyle.Render("↑ "+formatSpeed(m.up)),
		speedDownStyle.Render("↓ "+formatSpeed(m.down)),
	)

	// Row 1: Clickable action bar
	actionBar, _ := m.actionButtons()

	// Row 2..: Two-pane body
	leftContent := m.renderGroups(leftWidth-4, bodyHeight-2)
	rightContent := m.renderNodes(rightWidth-4, bodyHeight-2)

	leftStyle := paneBorderInactive.Width(leftWidth).Height(bodyHeight)
	rightStyle := paneBorderInactive.Width(rightWidth).Height(bodyHeight)
	if m.focus == paneGroups {
		leftStyle = paneBorderActive.Width(leftWidth).Height(bodyHeight)
	} else {
		rightStyle = paneBorderActive.Width(rightWidth).Height(bodyHeight)
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftStyle.Render(leftContent), " ", rightStyle.Render(rightContent))

	// Footer
	status := dimStyle.Render(m.statusLine)
	if m.errLine != "" {
		status = errStyle.Render(m.errLine)
	}
	help := helpStyle.Render("Click/Scroll: select  •  ↑/↓: move  •  Tab: pane  •  Enter: switch  •  t/T: test  •  m: mode  •  r: refresh  •  q: quit")

	return lipgloss.JoinVertical(lipgloss.Left, header, actionBar, body, status, help)
}

func (m model) renderGroups(maxW, maxRows int) string {
	var lines []string
	lines = append(lines, titleStyle.Render("Proxy Groups"))
	if len(m.groups) == 0 {
		lines = append(lines, dimStyle.Render("(no groups)"))
		return strings.Join(lines, "\n")
	}

	start, end := visibleWindow(m.groupIdx, len(m.groups), maxRows-1)
	for i := start; i < end; i++ {
		name := m.groups[i]
		p := m.proxies[name]
		label := truncate(fmt.Sprintf("%s (%s)", name, p.Now), maxW)
		if i == m.groupIdx {
			lines = append(lines, selectedRowStyle.Width(maxW).Render(label))
		} else {
			lines = append(lines, label)
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderNodes(maxW, maxRows int) string {
	g, ok := m.currentGroup()
	if !ok {
		return titleStyle.Render("Nodes") + "\n" + dimStyle.Render("(select a group)")
	}

	var lines []string
	lines = append(lines, titleStyle.Render(fmt.Sprintf("Nodes — %s [%s]", g.Name, g.Type)))
	if len(g.All) == 0 {
		lines = append(lines, dimStyle.Render("(empty)"))
		return strings.Join(lines, "\n")
	}

	start, end := visibleWindow(m.nodeIdx, len(g.All), maxRows-1)
	for i := start; i < end; i++ {
		node := g.All[i]
		mark := "  "
		if node == g.Now {
			mark = activeMarkStyle.Render("● ")
		}
		delayBadge := formatDelay(m.delays[node])
		nameWidth := maxW - 12
		if nameWidth < 10 {
			nameWidth = 10
		}
		row := fmt.Sprintf("%s%-*s %s", mark, nameWidth, truncate(node, nameWidth), delayBadge)
		if i == m.nodeIdx && m.focus == paneNodes {
			plain := fmt.Sprintf("  %-*s %s", nameWidth, truncate(node, nameWidth), formatDelayPlain(m.delays[node]))
			if node == g.Now {
				plain = "●" + plain[1:]
			}
			lines = append(lines, selectedRowStyle.Width(maxW).Render(plain))
		} else {
			lines = append(lines, row)
		}
	}
	return strings.Join(lines, "\n")
}

func visibleWindow(cursor, total, capacity int) (int, int) {
	if capacity <= 0 {
		capacity = 1
	}
	if total <= capacity {
		return 0, total
	}
	start := cursor - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > total {
		start = total - capacity
	}
	return start, start + capacity
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
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

func formatDelay(ms int) string {
	switch {
	case ms < 0:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Render("timeout")
	case ms == 0:
		return dimStyle.Render("   -   ")
	case ms < 150:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Render(fmt.Sprintf("%4dms", ms))
	case ms < 400:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")).Render(fmt.Sprintf("%4dms", ms))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Render(fmt.Sprintf("%4dms", ms))
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
