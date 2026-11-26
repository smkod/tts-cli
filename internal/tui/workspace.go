package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

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
		for _, voice := range core.LocalVoices(m.fields[2].input.Value()) {
			name := voice.Name
			if m.fields[0].input.Value() == core.ProviderGemini {
				name = core.GeminiVoiceName(name)
			}
			choices = append(choices, name)
		}
		if len(choices) == 0 {
			for _, voice := range core.LocalVoices("en-US") {
				choices = append(choices, core.GeminiVoiceName(voice.Name))
			}
		}
	default:
		return false
	}
	if f.key == "voice" {
		choices = append([]string{"random"}, choices...)
	}
	index := 0
	for i, v := range choices {
		if strings.EqualFold(v, f.input.Value()) {
			index = i
			break
		}
	}
	delta := 1
	if direction == "left" {
		delta = -1
	}
	f.input.SetValue(choices[(index+delta+len(choices))%len(choices)])
	return true
}
func summary(s core.CostSummary) string {
	amount := "total"
	if s.UnknownRequests > 0 {
		amount = "known subtotal"
	}
	return fmt.Sprintf("USD %.6f %s · %d requests · %d unknown", s.EstimatedUSD, amount, s.Requests, s.UnknownRequests)
}
func (m model) usageLines() []string {
	lines := []string{
		"Usage & estimated costs",
		"Local gross paid-list estimates — not bills.",
		"Free tiers, credits, discounts and taxes excluded.",
		"",
		"Combined · " + summary(m.usage.Report.CostSummary),
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
	for _, p := range names {
		lines = append(lines, "", safe(p)+" · "+summary(m.usage.Report.Providers[p]))
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
			lines = append(
				lines,
				"  "+label+" ["+safe(c.ID)+"] · "+safe(c.Tier),
				"    "+summary(m.usage.Report.Configurations[p][c.ID]),
			)
		}
		ids := make([]string, 0)
		for id := range m.usage.Report.Configurations[p] {
			if !seen[id] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			label := "Unidentified / removed key [" + safe(id) + "]"
			if id == "" || id == "unattributed" {
				label = "Legacy / unattributed"
			}
			lines = append(lines, "  "+label, "    "+summary(m.usage.Report.Configurations[p][id]))
		}
		if p == core.ProviderGoogle {
			lines = append(lines, "  Google ADC/default credentials: no per-key attribution.")
		}
	}
	if len(names) == 0 {
		lines = append(lines, "No local usage recorded yet.")
	}
	lines = append(lines, "", "↑/↓ or PgUp/PgDown: scroll · R: refresh local records")
	return lines
}
func (m model) View() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
	width := max(1, m.contentWidth()-2)
	header := accent.Render("ttsi") + muted.Render(" / Text-to-Speech Interface")
	content := []string{}
	if m.screen == "usage" {
		rows := m.usageLines()
		available := max(1, m.height-6)
		offset := min(m.usageOffset, max(0, len(rows)-available))
		content = append(content, rows[offset:min(len(rows), offset+available)]...)
	} else {
		content = append(content, accent.Render("Generate audio"))
		fields := []string{muted.Render("SETTINGS · Tab to edit · ←/→ choices")}
		if m.height < 30 {
			fields = append(
				fields,
				safe(m.fields[0].input.Value())+" · "+safe(m.fields[3].input.Value())+" · "+safe(m.fields[6].input.Value()),
			)
		}
		for i, f := range m.fields {
			if m.height < 30 && m.focus != i+1 {
				continue
			}
			label := f.label
			if m.focus == i+1 {
				label = accent.Render("› " + label)
			}
			fields = append(fields, label+": "+f.input.View())
		}
		editor := fmt.Sprintf(
			"TEXT · %d characters / %d bytes",
			utf8.RuneCountInString(m.text.Value()),
			len(m.text.Value()),
		) + "\n" + m.text.View()
		content = append(
			content,
			border.Width(max(1, width-2)).Render(strings.Join(fields, "\n")),
			border.Width(max(1, width-2)).Render(editor),
		)
		generate, preview := "[ Generate  Ctrl+G ]", "[ Preview  Ctrl+P ]"
		if m.focus == len(m.fields)+1 {
			generate = accent.Render(generate)
		}
		if m.focus == len(m.fields)+2 {
			preview = accent.Render(preview)
		}
		if m.busy {
			generate = m.spinner.View() + " Working…"
		}
		content = append(content, generate+"  "+preview, muted.Render("Preview request (no API call)"))
	}
	if m.height < 14 && m.screen != "usage" {
		active := m.text.View()
		if m.focus > 0 && m.focus <= len(m.fields) {
			f := m.fields[m.focus-1]
			active = f.label + ": " + f.input.View()
		}
		if m.focus > len(m.fields) {
			active = "Generate Ctrl+G · Preview Ctrl+P"
		}
		content = []string{accent.Render("Generate audio"), active}
	}
	body := strings.Join(content, "\n")
	if m.width >= 76 {
		generate, usage := "  Generate  F1", "  Usage     F2"
		if m.screen == "usage" {
			usage = accent.Render("› Usage     F2")
		} else {
			generate = accent.Render("› Generate  F1")
		}
		sidebar := lipgloss.NewStyle().Width(20).Render(muted.Render("WORKSPACE") + "\n\n" + generate + "\n" + usage)
		body = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " │ ", body)
	} else {
		header += " · F1 Generate / F2 Usage"
	}
	status := safe(m.status)
	footer := muted.Render("F1/F2: workspace · Tab: focus · Ctrl+G: generate" +
		" · Ctrl+P: preview · Esc: cancel · Ctrl+Q: quit")
	if m.screen == "usage" {
		footer = muted.Render("F1: Generate · R/Ctrl+R: refresh · ↑/↓: scroll · Ctrl+Q: quit")
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
