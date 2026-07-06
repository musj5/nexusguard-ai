// Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package tui implements the fighter-jet-cockpit terminal UI dashboard.
// Features real-time stats, interactive menus, and a cyberpunk aesthetic.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/smilespoon/nexusguard-ai/pkg/budget"
	"github.com/smilespoon/nexusguard-ai/pkg/cache"
	"github.com/smilespoon/nexusguard-ai/pkg/mask"
	"github.com/smilespoon/nexusguard-ai/pkg/proxy"
)

// Theme colors - Cyberpunk/Dark Mode
type Theme struct {
	Primary     lipgloss.Color // Cyan
	Secondary   lipgloss.Color // Magenta
	Success     lipgloss.Color // Neon Green
	Warning     lipgloss.Color // Yellow
	Danger      lipgloss.Color // Red
	Text        lipgloss.Color // White
	TextDim     lipgloss.Color // Gray
	Background  lipgloss.Color // Dark
	Border      lipgloss.Color // Subtle
	Highlight   lipgloss.Color // Bright Cyan
}

var CyberTheme = Theme{
	Primary:    lipgloss.Color("#00FFFF"),     // Cyan
	Secondary:  lipgloss.Color("#FF00FF"),     // Magenta
	Success:    lipgloss.Color("#39FF14"),     // Neon Green
	Warning:    lipgloss.Color("#FFD700"),     // Gold
	Danger:     lipgloss.Color("#FF3366"),     // Red
	Text:       lipgloss.Color("#F0F0F0"),     // White
	TextDim:    lipgloss.Color("#666666"),     // Gray
	Background: lipgloss.Color("#0D0D0D"),     // Near Black
	Border:     lipgloss.Color("#333333"),     // Dark Gray
	Highlight:  lipgloss.Color("#00E5E5"),     // Bright Cyan
}

// Messages
type tickMsg time.Time
type statsMsg struct {
	totalReqs    int64
	savedCost    float64
	maskedItems  int64
	activeProv   string
	cacheHits    int64
	budgetStatus string
	latency      time.Duration
}

// Model represents the TUI state
type Model struct {
	proxy        *proxy.Server
	cache        *cache.Manager
	budget       *budget.Tracker
	masker       *mask.Masker
	version      string

	// Theme
	theme Theme

	// Layout dimensions
	width        int
	height       int

	// Sections
	headerHeight int
	footerHeight int

	// Menu
	menuItems    []MenuItem
	selectedIdx  int
	focusedPane  int

	// Data
	stats        statsMsg
	spinner      spinner.Model
	showHelp     bool

	// Animations
	tick         int
}

// MenuItem represents an interactive menu option
type MenuItem struct {
	Label    string
	Key      string
	Enabled  bool
	Toggle   bool
}

// New creates a new TUI model
func New(
	p *proxy.Server,
	c *cache.Manager,
	b *budget.Tracker,
	m *mask.Masker,
	version string,
) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot

	return Model{
		proxy:   p,
		cache:   c,
		budget:  b,
		masker:  m,
		version: version,
		theme:   CyberTheme,
		spinner: s,
		menuItems: []MenuItem{
			{Label: "Development Mode", Key: "dev", Enabled: false, Toggle: true},
			{Label: "Enterprise Mode", Key: "enterprise", Enabled: false, Toggle: true},
			{Label: "Custom Mode", Key: "custom", Enabled: false, Toggle: true},
			{Label: "Cache System", Key: "cache", Enabled: true, Toggle: true},
			{Label: "PII Masking", Key: "mask", Enabled: true, Toggle: true},
			{Label: "Budget Defender", Key: "budget", Enabled: true, Toggle: true},
			{Label: "Auto-Fallback", Key: "fallback", Enabled: true, Toggle: true},
			{Label: "Hard Stop", Key: "hardstop", Enabled: true, Toggle: true},
		},
		headerHeight: 3,
		footerHeight: 2,
	}
}

// Init initializes the TUI
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
	)
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
		case "down", "j":
			if m.selectedIdx < len(m.menuItems)-1 {
				m.selectedIdx++
			}
		case "enter", " ":
			m.toggleMenuItem(m.selectedIdx)
		case "h":
			m.showHelp = !m.showHelp
		case "c":
			m.proxy.GetCache().Clear()
		case "r":
			m.proxy.GetBudget().ResetDaily()
		case "x":
			m.proxy.GetMasker().Reset()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.updateStats()
		m.tick++
		return m, tickCmd()

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

// View renders the TUI
func (m Model) View() string {
	if m.showHelp {
		return m.renderHelp()
	}

	var sections []string

	// Header
	sections = append(sections, m.renderHeader())

	// Main content - split into left and right
	mainContent := m.renderMainContent()
	sections = append(sections, mainContent)

	// Footer
	sections = append(sections, m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// Header with title and branding
func (m Model) renderHeader() string {
	t := m.theme

	// Logo and title
	titleStyle := lipgloss.NewStyle().
		Foreground(t.Primary).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Primary)

	versionStyle := lipgloss.NewStyle().
		Foreground(t.TextDim).
		Padding(0, 1)

	statusStyle := lipgloss.NewStyle().
		Foreground(t.Success).
		Padding(0, 1)

	title := titleStyle.Render(" ⚡ NEXUSGUARD AI ")
	version := versionStyle.Render(fmt.Sprintf("v%s", m.version))
	status := statusStyle.Render("● LIVE")

	header := lipgloss.JoinHorizontal(lipgloss.Center, title, version, status)

	// Decorative line
	lineWidth := m.width - 4
	if lineWidth < 20 {
		lineWidth = 20
	}
	line := lipgloss.NewStyle().
		Foreground(t.Border).
		Render(strings.Repeat("━", lineWidth))

	return lipgloss.JoinVertical(lipgloss.Center, header, line)
}

// Main content area with stats and menu
func (m Model) renderMainContent() string {
	

	// Left panel - Stats cards
	statsPanel := m.renderStatsPanel()

	// Right panel - Menu and controls
	menuPanel := m.renderMenuPanel()

	// Combine panels
	availableHeight := m.height - m.headerHeight - m.footerHeight - 4
	_ = availableHeight

	content := lipgloss.JoinHorizontal(lipgloss.Top, statsPanel, "  ", menuPanel)

	return lipgloss.NewStyle().
		Padding(1, 2).
		Render(content)
}

// Stats panel with metric cards
func (m Model) renderStatsPanel() string {
	t := m.theme
	width := 50

	// Money Saved Card
	savedCard := m.renderCard(
		"MONEY SAVED",
		fmt.Sprintf("$%.4f", m.stats.savedCost),
		t.Success,
		width,
	)

	// Total Requests Card
	reqsCard := m.renderCard(
		"TOTAL REQUESTS",
		fmt.Sprintf("%d", m.stats.totalReqs),
		t.Primary,
		width,
	)

	// Masked PII Card
	piiCard := m.renderCard(
		"PII ITEMS MASKED",
		fmt.Sprintf("%d", m.stats.maskedItems),
		t.Secondary,
		width,
	)

	// Cache Hits Card
	cacheCard := m.renderCard(
		"CACHE HITS",
		fmt.Sprintf("%d", m.stats.cacheHits),
		t.Warning,
		width,
	)

	// Active Provider Card
	provCard := m.renderCard(
		"ACTIVE PROVIDER",
		m.stats.activeProv,
		t.Highlight,
		width,
	)

	// Latency Card
	latCard := m.renderCard(
		"AVG LATENCY",
		m.stats.latency.String(),
		t.Text,
		width,
	)

	// Budget Status
	budgetColor := t.Success
	if m.stats.budgetStatus == "WARNING" {
		budgetColor = t.Warning
	} else if m.stats.budgetStatus == "EXCEEDED" || m.stats.budgetStatus == "BLOCKED" {
		budgetColor = t.Danger
	}
	budgetCard := m.renderCard(
		"BUDGET STATUS",
		m.stats.budgetStatus,
		budgetColor,
		width,
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		savedCard,
		"",
		reqsCard,
		"",
		piiCard,
		"",
		cacheCard,
		"",
		provCard,
		"",
		latCard,
		"",
		budgetCard,
	)
}

// Individual stat card
func (m Model) renderCard(label, value string, color lipgloss.Color, width int) string {
	t := m.theme

	labelStyle := lipgloss.NewStyle().
		Foreground(t.TextDim).
		Bold(true).
		Padding(0, 1)

	valueStyle := lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Padding(0, 1).
		Width(width)

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1).
		Width(width)

	content := lipgloss.JoinVertical(lipgloss.Left,
		labelStyle.Render(label),
		valueStyle.Render(value),
	)

	return cardStyle.Render(content)
}

// Menu panel with toggle controls
func (m Model) renderMenuPanel() string {
	t := m.theme
	width := 40

	titleStyle := lipgloss.NewStyle().
		Foreground(t.Primary).
		Bold(true).
		Padding(0, 1).
		Width(width)

	menuTitle := titleStyle.Render("⚙️  SYSTEM CONTROLS")

	var items []string
	for i, item := range m.menuItems {
		items = append(items, m.renderMenuItem(i, item, width))
	}

	// Add keyboard shortcuts info
	helpStyle := lipgloss.NewStyle().
		Foreground(t.TextDim).
		Padding(1, 1).
		Width(width)

	helpText := "[↑↓] Navigate  [Enter] Toggle\n[h] Help  [q] Quit"
	help := helpStyle.Render(helpText)

	allItems := append([]string{menuTitle, ""}, items...)
	allItems = append(allItems, "", help)

	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border).
		Padding(1, 1).
		Width(width + 4)

	return panelStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left, allItems...),
	)
}

// Individual menu item
func (m Model) renderMenuItem(idx int, item MenuItem, width int) string {
	t := m.theme

	selected := idx == m.selectedIdx

	labelStyle := lipgloss.NewStyle().Width(width - 10)
	if selected {
		labelStyle = labelStyle.Foreground(t.Highlight).Bold(true)
	} else {
		labelStyle = labelStyle.Foreground(t.Text)
	}

	var toggle string
	if item.Toggle {
		if item.Enabled {
			toggle = lipgloss.NewStyle().
				Foreground(t.Success).
				Render("● ON ")
		} else {
			toggle = lipgloss.NewStyle().
				Foreground(t.Danger).
				Render("● OFF")
		}
	}

	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().
			Foreground(t.Primary).
			Render("▶ ")
	}

	content := lipgloss.JoinHorizontal(lipgloss.Left,
		cursor,
		labelStyle.Render(item.Label),
		" ",
		toggle,
	)

	if selected {
		bgStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#1a1a1a")).
			Padding(0, 1).
			Width(width + 4)
		content = bgStyle.Render(content)
	}

	return content
}

// Footer with status bar
func (m Model) renderFooter() string {
	t := m.theme

	// Author credit
	authorStyle := lipgloss.NewStyle().
		Foreground(t.TextDim).
		Italic(true).
		Padding(0, 1)

	author := authorStyle.Render("Crafted by Mustafa Al-Aqrawi (Smile Spoon)")

	// Status indicators
	indicators := []string{
		m.indicator("Proxy", true, t.Success),
		m.indicator("Cache", m.proxy.GetCache() != nil, t.Success),
		m.indicator("Mask", m.proxy.GetMasker() != nil, t.Success),
		m.indicator("Budget", m.proxy.GetBudget() != nil, t.Success),
	}

	statusLine := lipgloss.JoinHorizontal(lipgloss.Left, indicators...)

	return lipgloss.JoinHorizontal(lipgloss.Left, statusLine, author)
}

// Status indicator dot
func (m Model) indicator(label string, active bool, color lipgloss.Color) string {
	t := m.theme
	dot := "●"
	if !active {
		color = t.TextDim
		dot = "○"
	}

	style := lipgloss.NewStyle().
		Foreground(color).
		Padding(0, 1)

	return style.Render(fmt.Sprintf("%s %s", dot, label))
}

// Help screen
func (m Model) renderHelp() string {
	t := m.theme

	titleStyle := lipgloss.NewStyle().
		Foreground(t.Primary).
		Bold(true).
		Padding(1, 2)

	contentStyle := lipgloss.NewStyle().
		Foreground(t.Text).
		Padding(1, 2).
		Width(70)

	keyStyle := lipgloss.NewStyle().
		Foreground(t.Highlight).
		Bold(true)

	title := titleStyle.Render("🎮 NexusGuard AI - Controls")

	helpText := fmt.Sprintf(`%s - Navigation
  %s - Move up
  %s - Move down
  %s - Toggle item on/off

%s - Actions
  %s - Show/hide this help
  %s - Clear cache
  %s - Reset daily budget
  %s - Reset PII mappings
  %s - Quit application

%s - Features
  Development Mode  - Enhanced logging & debugging
  Enterprise Mode   - Strict compliance & audit
  Custom Mode       - User-defined rules
  Cache System      - Semantic response caching
  PII Masking       - Privacy protection
  Budget Defender   - Cost limit enforcement
  Auto-Fallback     - Provider failover
  Hard Stop         - Block on budget exceeded`,
		keyStyle.Render("Navigation"),
		keyStyle.Render("↑ / k"),
		keyStyle.Render("↓ / j"),
		keyStyle.Render("Enter / Space"),
		keyStyle.Render("Actions"),
		keyStyle.Render("h"),
		keyStyle.Render("c"),
		keyStyle.Render("r"),
		keyStyle.Render("x"),
		keyStyle.Render("q / Ctrl+C"),
		keyStyle.Render("Features"),
	)

	content := contentStyle.Render(helpText)

	return lipgloss.JoinVertical(lipgloss.Center, title, "", content, "", keyStyle.Render("Press any key to return..."))
}

// Toggle a menu item
func (m *Model) toggleMenuItem(idx int) {
	if idx < 0 || idx >= len(m.menuItems) {
		return
	}

	item := &m.menuItems[idx]
	if !item.Toggle {
		return
	}

	item.Enabled = !item.Enabled

	// Apply changes to subsystems
	switch item.Key {
	case "cache":
		// Toggle cache via config
	case "mask":
		// Toggle masking
	case "budget":
		// Toggle budget tracking
	case "hardstop":
		// Toggle hard stop
	}
}

// Update stats from proxy
func (m *Model) updateStats() {
	proxyStats := m.proxy.GetStats()
	cacheStats := m.proxy.GetCache().Stats()
	budgetStats := m.proxy.GetBudget().GetStats()
	maskStats := m.proxy.GetMasker().Stats()

	m.stats = statsMsg{
		totalReqs:    proxyStats.TotalRequests,
		savedCost:    cacheStats.SavedCost,
		maskedItems:  maskStats.TotalMasked,
		activeProv:   proxyStats.ActiveProvider,
		cacheHits:    cacheStats.Hits,
		budgetStatus: budgetStats.Status.String(),
		latency:      proxyStats.AvgLatency,
	}
}

// Tick command for animation
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Run starts the TUI
func Run(ctx context.Context, model Model) error {
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Handle context cancellation
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, err := p.Run()
	return err
}
