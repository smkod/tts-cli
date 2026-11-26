package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/smkod/tts-cli/internal/core"
)

// Configuration contains public identifiers only, never credential material.
type Configuration struct{ Provider, ID, Name, Tier string }
type UsageSnapshot struct {
	Report         core.UsageReport
	Configurations []Configuration
}
type usageMsg struct {
	id       uint64
	snapshot UsageSnapshot
	err      error
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
func (m *model) refreshUsage() tea.Cmd {
	if m.loadUsage == nil {
		return nil
	}
	m.usageID++
	m.usageLoading = true
	id, load, ctx := m.usageID, m.loadUsage, m.ctx
	workers := m.workers
	if workers != nil {
		workers.Add(1)
	}
	result := make(chan usageMsg, 1)
	go func() {
		if workers != nil {
			defer workers.Done()
		}
		snapshot, err := load(ctx)
		result <- usageMsg{id: id, snapshot: snapshot, err: err}
	}()
	return func() tea.Msg { return <-result }
}
func (m model) contentWidth() int {
	if m.width >= 76 {
		return m.width - 23
	}
	return m.width
}
func cycleChoice(current, direction string, choices []string) string {
	if len(choices) == 0 {
		return current
	}
	index := 0
	for i, v := range choices {
		if strings.EqualFold(v, current) {
			index = i
			break
		}
	}
	delta := 1
	if direction == "left" {
		delta = -1
	}
	return choices[(index+delta+len(choices))%len(choices)]
}

func voiceChoices(provider, language string) []string {
	var choices []string
	for _, voice := range core.LocalVoices(language) {
		name := voice.Name
		if provider == core.ProviderGemini {
			name = core.GeminiVoiceName(name)
		}
		choices = append(choices, name)
	}
	if len(choices) == 0 {
		for _, voice := range core.LocalVoices("en-US") {
			name := voice.Name
			if provider == core.ProviderGemini {
				name = core.GeminiVoiceName(name)
			}
			choices = append(choices, name)
		}
	}
	return append([]string{"random"}, choices...)
}

func (m *model) cycle(direction string) bool {
	if m.focus < 1 || m.focus > len(m.fields) {
		return false
	}
	f := &m.fields[m.focus-1]
	choices := []string{}
	switch f.key {
	case "provider":
		choices = []string{core.ProviderGoogle, core.ProviderGemini}
	case "format":
		choices = []string{core.FormatWAV, core.FormatMP3}
	case "voice":
		choices = voiceChoices(m.fields[0].input.Value(), m.fields[2].input.Value())
	default:
		return false
	}
	f.input.SetValue(cycleChoice(f.input.Value(), direction, choices))
	return true
}
func (u UsageSnapshot) tierEstimates() (paid, free core.CostSummary, unassigned int) {
	// Google Cloud usage is estimated at paid list rates, not verified billing.
	paid = u.Report.Providers[core.ProviderGoogle]
	seen := make(map[string]bool)
	for _, c := range u.Configurations {
		if c.Provider != core.ProviderGemini || c.ID == "" || c.ID == "unattributed" || seen[c.ID] {
			continue
		}
		var target *core.CostSummary
		switch c.Tier {
		case core.KeyTierPaid:
			target = &paid
		case core.KeyTierFree:
			target = &free
		default:
			continue
		}
		seen[c.ID] = true
		s := u.Report.Configurations[c.Provider][c.ID]
		target.EstimatedUSD += s.EstimatedUSD
		target.Requests += s.Requests
		target.UnknownRequests += s.UnknownRequests
	}
	return paid, free, max(0, u.Report.Requests-paid.Requests-free.Requests)
}

func (m model) usageLines() []string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	strong := lipgloss.NewStyle().Bold(true)
	width := max(1, m.contentWidth()-2)
	wide := width >= 48
	paidStyle := strong.Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "221"})
	freeStyle := strong.Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "78"})
	requests := func(s core.CostSummary) string {
		unit := "requests"
		if s.Requests == 1 {
			unit = "request"
		}
		value := fmt.Sprintf("%d %s", s.Requests, unit)
		if s.UnknownRequests > 0 {
			value += fmt.Sprintf(" · %d unknown", s.UnknownRequests)
		}
		return value
	}
	// Use terminal cells rather than bytes so credential names align even with wide characters.
	columns := func(name, cost, count, unknown string, metrics lipgloss.Style) string {
		name = ansi.Truncate(name, width-30, "…")
		return name + strings.Repeat(" ", width-30-lipgloss.Width(name)) +
			metrics.Render(fmt.Sprintf("  %12s %5s %9s", cost, count, unknown))
	}
	estimate := func(label string, s core.CostSummary, style lipgloss.Style) (string, string) {
		detail := "  " + requests(s)
		if s.UnknownRequests > 0 {
			detail = "  Known subtotal · " + requests(s)
		}
		return style.Render(fmt.Sprintf("%s  USD %.6f", label, s.EstimatedUSD)), detail
	}
	paid, free, unassigned := m.usage.tierEstimates()
	paidLabel, paidDetail := estimate("PAID ESTIMATE", paid, paidStyle)
	freeLabel, freeDetail := estimate("FREE-TIER VALUE", free, freeStyle)
	lines := []string{
		accent.Render("Usage & estimated costs"),
		paidLabel,
		muted.Render(paidDetail),
		freeLabel,
		muted.Render(freeDetail),
		"",
		muted.Render("Local gross paid-list estimates — not bills."),
		muted.Render("Paid includes Google Cloud; free shows value at paid rates."),
	}
	if unassigned > 0 {
		lines = append(lines, muted.Render(fmt.Sprintf("Unassigned tier: %d (see breakdown below)", unassigned)))
	}
	addRow := func(name, metadata string, s core.CostSummary, provider bool) {
		cost := fmt.Sprintf("%.6f", s.EstimatedUSD)
		style := lipgloss.NewStyle()
		if provider {
			style = strong
		} else if s.Requests == 0 && s.UnknownRequests == 0 {
			style = muted
		}
		if wide {
			unknown := "—"
			if s.UnknownRequests > 0 {
				unknown = fmt.Sprint(s.UnknownRequests)
			}
			label := style.Render(name)
			if metadata != "" && lipgloss.Width(name+" "+metadata) <= width-30 {
				label += " " + metadata
				metadata = ""
			}
			lines = append(lines, columns(label, cost, fmt.Sprint(s.Requests), unknown, style))
		} else {
			lines = append(lines, style.Render(name), style.Render("  "+cost+" · "+requests(s)))
		}
		if metadata != "" {
			lines = append(lines, "    "+metadata)
		}
	}
	if m.usageLoading {
		lines = append(lines, "Refreshing local usage…")
	}
	if m.usageError != "" {
		lines = append(lines, "Usage error: "+safe(m.usageError)+" · R retries")
	}
	providers := map[string]bool{}
	for p := range m.usage.Report.Providers {
		providers[p] = true
	}
	for _, c := range m.usage.Configurations {
		providers[c.Provider] = true
	}
	names := make([]string, 0, len(providers))
	for p := range providers {
		names = append(names, p)
	}
	sort.Strings(names)
	if len(names) > 0 {
		heading := "PROVIDERS & CREDENTIALS · USD"
		if wide {
			heading = columns("PROVIDERS / KEYS", "KNOWN USD", "REQS", "UNPRICED", lipgloss.NewStyle())
		}
		lines = append(lines, "", muted.Render(heading))
	}
	for _, p := range names {
		lines = append(lines, muted.Render(strings.Repeat("─", width)))
		name := safe(p)
		switch p {
		case core.ProviderGemini:
			name = "Gemini"
		case core.ProviderGoogle:
			name = "Google Cloud"
		}
		addRow(name, "", m.usage.Report.Providers[p], true)
		seen := map[string]bool{}
		for _, c := range m.usage.Configurations {
			if c.Provider != p {
				continue
			}
			seen[c.ID] = true
			label := safe(c.Name)
			if label == "" {
				label = "Configured key"
			}
			metadata := muted.Render("[" + safe(c.ID) + "]")
			if tier := safe(c.Tier); tier != "" {
				tierStyle := muted
				switch tier {
				case core.KeyTierFree:
					tierStyle = freeStyle
				case core.KeyTierPaid:
					tierStyle = paidStyle
				}
				metadata += muted.Render(" · ") + tierStyle.Render(tier)
			}
			addRow("  "+label, metadata, m.usage.Report.Configurations[p][c.ID], false)
		}
		ids := make([]string, 0)
		for id := range m.usage.Report.Configurations[p] {
			if !seen[id] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			label, metadata := "Unidentified / removed key", muted.Render("["+safe(id)+"]")
			if id == "" || id == "unattributed" {
				label, metadata = "Legacy / unattributed", ""
			}
			addRow("  "+label, metadata, m.usage.Report.Configurations[p][id], false)
		}
		if p == core.ProviderGoogle {
			lines = append(lines, muted.Render("  Google ADC: no per-key attribution."))
		}
	}
	if len(names) == 0 {
		lines = append(lines, "No local usage recorded yet.")
	}
	return lines
}
func (m model) View() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	header := accent.Render("ttsi") + muted.Render(" / Text-to-Speech Interface")
	content := []string{}
	switch m.screen {
	case "usage":
		rows := m.usageLines()
		available := max(1, m.height-6)
		offset := min(m.usageOffset, max(0, len(rows)-available))
		content = append(content, rows[offset:min(len(rows), offset+available)]...)
	case "config":
		content = m.configView()
	default:
		content = m.generateView()
	}
	body := strings.Join(content, "\n")
	if m.width >= 76 {
		generate, usage, cfg := "  Generate  F1", "  Usage     F2", "  Config    F3"
		switch m.screen {
		case "usage":
			usage = accent.Render("› Usage     F2")
		case "config":
			cfg = accent.Render("› Config    F3")
		default:
			generate = accent.Render("› Generate  F1")
		}
		sidebar := lipgloss.NewStyle().Width(20).Render(muted.Render("WORKSPACE") + "\n\n" + generate + "\n" + usage + "\n" + cfg)
		body = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " │ ", body)
	} else {
		header += " · F1 Generate / F2 Usage / F3 Config"
	}
	status := safe(m.status)
	footer := muted.Render("F1/F2/F3: workspace · Tab: focus · Ctrl+G: generate" +
		" · Ctrl+P: preview · Esc: cancel · Ctrl+Q: quit")
	switch m.screen {
	case "usage":
		footer = muted.Render("F1: Generate · F3: Config · R: refresh · ↑/↓ PgUp/PgDn: scroll · Ctrl+Q: quit")
	case "config":
		footer = muted.Render("Tab/↑/↓: field · ←/→: cycle · Ctrl+S: save · Ctrl+U: unset · Ctrl+R: reload · Ctrl+Q: quit")
	default:
		if m.generateLayout() != generateTiny {
			footer = muted.Render("Tab: next · Shift+Tab: back · Esc: cancel · Ctrl+Q: quit")
		}
	}
	// Fit terminal cells and reserve bottom status rows, prioritizing active controls.
	lines := []string{header}
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, line)
	}
	bodyHeight := max(1, m.height-2)
	if len(lines) > bodyHeight {
		lines = lines[:bodyHeight]
	}
	if m.height > 2 {
		lines = append(lines, status, footer)
	}
	if len(lines) > max(1, m.height) {
		lines = lines[:max(1, m.height)]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, max(1, m.width), "…")
	}
	return strings.Join(lines, "\n")
}

func (m model) maxUsageOffset() int { return max(0, len(m.usageLines())-max(1, m.height-6)) }
