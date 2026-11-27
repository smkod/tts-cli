package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type generateLayout int

const (
	generateTiny generateLayout = iota
	generateCompact
	generateStacked
	generateColumns
)

func (m model) generateLayout() generateLayout {
	switch {
	case m.height >= 24 && m.contentWidth() >= 74:
		return generateColumns
	case m.height >= 30:
		return generateStacked
	case m.height >= 19:
		return generateCompact
	default:
		return generateTiny
	}
}

func (m *model) sizeGenerate() {
	layout := m.generateLayout()
	// Rows reserved for workspace chrome, cards, divider, and actions, excluding script lines.
	overhead := [...]int{4, 17, 29, 22}[layout]
	width := m.contentWidth()
	m.text.SetWidth(max(1, width-4))
	m.text.SetHeight(max(1, min(12, m.height-overhead)))
	fieldWidth := width
	if layout == generateColumns {
		fieldWidth = (width - 2) / 2
	}
	for i := range m.fields {
		m.fields[i].input.Width = max(1, fieldWidth-18)
	}
}

func renderCard(w int, focused bool, lines ...string) string {
	color := lipgloss.AdaptiveColor{Light: "246", Dark: "241"}
	if focused {
		color = lipgloss.AdaptiveColor{Light: "61", Dark: "141"}
	}
	inner := max(1, w-4)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], inner, "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).
		Padding(0, 1).Width(inner + 2).Render(strings.Join(lines, "\n"))
}

func renderAction(name, shortcut string, focused, primary bool) string {
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "250"})
	style := lipgloss.NewStyle().Bold(true)
	if primary {
		style = accent
	}
	marker := " "
	if focused {
		marker = "›"
		style = style.Underline(true)
	}
	return marker + style.Render(name) + " " + muted.Render(shortcut)
}

func (m model) generateView() []string {
	lines, _ := m.generateLayoutLines(0, 0)
	return lines
}

func (m model) generateLayoutLines(originX, originY int) ([]string, []mouseRegion) {
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "250"})
	label := lipgloss.NewStyle().Bold(true)
	width := m.contentWidth()
	layout := m.generateLayout()
	var regions []mouseRegion
	fieldRow := func(i int) string {
		f := m.fields[i]
		name := fmt.Sprintf("%-12s", f.label)
		if m.focus == i+1 {
			return accent.Render("› "+name) + " " + f.input.View()
		}
		value := safe(f.input.Value())
		if value == "" {
			value = muted.Render("—")
		}
		return "  " + muted.Render(name) + " " + value
	}
	generate := renderAction("Generate", "Ctrl+G", m.focus == len(m.fields)+1, true)
	if m.busy {
		generate = accent.Render(m.spinner.View() + " Generating…")
	}
	preview := renderAction("Preview", "Ctrl+P", m.focus == len(m.fields)+2, false)
	actions := generate + "  " + preview
	title := accent.Render("Generate audio")
	if layout == generateTiny {
		active := m.text.View()
		if m.focus > 0 && m.focus <= len(m.fields) {
			active = fieldRow(m.focus - 1)
		} else if m.focus > len(m.fields) {
			active = actions
			regions = append(regions,
				mouseRegion{x: originX, y: originY + 1, w: lipgloss.Width(generate), h: 1, action: mouseGenSubmit},
				mouseRegion{x: originX + lipgloss.Width(generate) + 2, y: originY + 1, w: lipgloss.Width(preview), h: 1, action: mouseGenPreview},
			)
		}
		return []string{title, active}, regions
	}

	text := m.text.Value()
	counter := fmt.Sprintf("%d characters · %d bytes", utf8.RuneCountInString(text), len(text))
	editor := append([]string{label.Render("Script") + muted.Render("  ·  "+counter)}, strings.Split(m.text.View(), "\n")...)
	scriptCard := renderCard(width, m.focus == 0, editor...)
	scriptY := originY + 3
	scriptH := len(editor) + 2
	regions = append(regions, mouseRegion{x: originX, y: scriptY, w: width, h: scriptH, action: mouseGenFocus, index: 0})
	content := []string{title, muted.Render("Write a script, tune the voice, and turn it into audio."), "", scriptCard, ""}
	settingsY := scriptY + scriptH + 1
	if layout == generateCompact {
		settings := []string{label.Render("Settings") + muted.Render("  ·  Tab to edit")}
		settings = append(settings, safe(m.fields[0].input.Value())+" · "+safe(m.fields[3].input.Value())+" · "+safe(m.fields[6].input.Value()))
		if m.focus > 0 && m.focus <= len(m.fields) {
			settings = append(settings, fieldRow(m.focus-1))
		}
		card := renderCard(width, m.focus > 0 && m.focus <= len(m.fields), settings...)
		regions = append(regions, mouseRegion{x: originX, y: settingsY, w: width, h: len(settings) + 2, action: mouseGenFocus, index: 1})
		content = append(content, card)
		settingsY += len(settings) + 2
	} else {
		group := func(x, y, w, start, end, minFields int, title string) string {
			lines := []string{title}
			for i := start; i < end; i++ {
				lines = append(lines, fieldRow(i))
				regions = append(regions, mouseRegion{x: x, y: y + 2 + (i - start), w: w, h: 1, action: mouseGenFocus, index: i + 1})
			}
			for i := end - start; i < minFields; i++ {
				lines = append(lines, "")
			}
			return renderCard(w, m.focus > start && m.focus <= end, lines...)
		}
		voice := label.Render("Voice") + muted.Render("  ·  ←/→ choices")
		output := label.Render("Output") + muted.Render("  ·  ←/→ format")
		if layout == generateColumns {
			left := (width - 2) / 2
			right := width - left - 2
			content = append(content, lipgloss.JoinHorizontal(lipgloss.Top,
				group(originX, settingsY, left, 0, 5, 5, voice), "  ", group(originX+left+2, settingsY, right, 5, len(m.fields), 5, output)))
			settingsY += 8
		} else {
			content = append(content, group(originX, settingsY, width, 0, 5, 0, voice), group(originX, settingsY+8, width, 5, len(m.fields), 0, output))
			settingsY += 8 + 7
		}
	}
	actionsY := settingsY + 1
	regions = append(regions,
		mouseRegion{x: originX, y: actionsY, w: lipgloss.Width(generate), h: 1, action: mouseGenSubmit},
		mouseRegion{x: originX + lipgloss.Width(generate) + 2, y: actionsY, w: lipgloss.Width(preview), h: 1, action: mouseGenPreview},
	)
	return append(content, "", actions), regions
}
