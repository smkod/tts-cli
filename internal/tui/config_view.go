package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/smkod/tts-cli/internal/core"
)

// ConfigSnapshot holds stored YAML settings, environment overrides, defaults, and resolved effective settings.
type ConfigSnapshot struct {
	Path      string
	Explicit  bool
	Stored    map[string]string
	Env       map[string]string
	Defaults  map[string]string
	Effective core.Settings
}

type LoadConfigFunc func(context.Context) (ConfigSnapshot, error)
type SaveConfigFunc func(context.Context, map[string]string, []string) (ConfigSnapshot, error)

type configLoadedMsg struct {
	id       uint64
	snapshot ConfigSnapshot
	err      error
}

type configSavedMsg struct {
	id       uint64
	snapshot ConfigSnapshot
	err      error
}

type configField struct {
	key, label, initial string
	storedValue         string
	hasStored           bool
	defaultValue        string
	hasDefault          bool
	envValue            string
	hasEnv              bool
	pendingUnset        bool
	input               textinput.Model
}

type configLayout int

const (
	configTiny configLayout = iota
	configCompact
	configStacked
	configColumns
)

var configFieldSpecs = []struct {
	key, label, placeholder string
}{
	{"provider", "Provider", core.ProviderGoogle},
	{"model", "Model", "Default (" + core.DefaultGeminiModel + ")"},
	{"key_tier", "Key tier", core.KeyTierAuto},
	{"language", "Language", "Default (en-US)"},
	{"voice", "Voice", "random"},
	{"style", "Style", "Optional (Gemini only)"},
	{"format", "Format", core.FormatMP3},
	{"bitrate", "MP3 bitrate", "96"},
	{"output_dir", "Output dir", "."},
	{"cache_dir", "Cache dir", "User cache dir"},
	{"jobs", "Jobs", "4"},
}

func defaultConfigSnapshot(s core.Settings) ConfigSnapshot {
	defaults := map[string]string{
		"provider":   core.ProviderGoogle,
		"key_tier":   core.KeyTierAuto,
		"voice":      "random",
		"format":     core.FormatMP3,
		"bitrate":    "96",
		"output_dir": ".",
		"jobs":       "4",
	}
	if s.Provider != "" {
		defaults["provider"] = s.Provider
	}
	if s.KeyTier != "" {
		defaults["key_tier"] = s.KeyTier
	}
	if s.Voice != "" {
		defaults["voice"] = s.Voice
	}
	if s.Format != "" {
		defaults["format"] = s.Format
	}
	if s.Bitrate > 0 {
		defaults["bitrate"] = strconv.Itoa(s.Bitrate)
	}
	if s.OutputDir != "" {
		defaults["output_dir"] = s.OutputDir
	}
	if s.CacheDir != "" {
		defaults["cache_dir"] = s.CacheDir
	}
	if s.Jobs > 0 {
		defaults["jobs"] = strconv.Itoa(s.Jobs)
	}
	stored := map[string]string{}
	if s.Model != "" {
		stored["model"] = s.Model
	}
	if s.LanguagePinned || s.Language == core.LanguageAuto {
		stored["language"] = s.Language
	}
	if s.Style != "" {
		stored["style"] = s.Style
	}
	return ConfigSnapshot{
		Stored:    stored,
		Env:       map[string]string{},
		Defaults:  defaults,
		Effective: s,
	}
}

func newConfigFields(snapshot ConfigSnapshot) []configField {
	fields := make([]configField, 0, len(configFieldSpecs))
	for _, spec := range configFieldSpecs {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = spec.placeholder
		input.CharLimit = 0
		fields = append(fields, configField{key: spec.key, label: spec.label, input: input})
	}
	applySnapshotToFields(fields, snapshot)
	return fields
}

func applySnapshotToFields(fields []configField, snapshot ConfigSnapshot) {
	for i := range fields {
		f := &fields[i]
		f.pendingUnset = false
		f.storedValue, f.hasStored = snapshot.Stored[f.key]
		f.defaultValue, f.hasDefault = snapshot.Defaults[f.key]
		f.envValue, f.hasEnv = snapshot.Env[f.key]
		val := ""
		switch {
		case f.hasStored:
			val = f.storedValue
		case f.hasDefault:
			val = f.defaultValue
		}
		f.initial = val
		f.input.SetValue(val)
	}
}

func (m *model) applyConfigSnapshot(snapshot ConfigSnapshot) {
	m.cfgSnapshot = snapshot
	if len(m.cfgFields) == 0 {
		m.cfgFields = newConfigFields(snapshot)
	} else {
		applySnapshotToFields(m.cfgFields, snapshot)
	}
	m.sizeConfig()
}

func (m *model) applySettingsToGenerate(s core.Settings) {
	updates := map[string]string{
		"provider": s.Provider,
		"model":    s.Model,
		"language": s.Language,
		"voice":    s.Voice,
		"style":    s.Style,
		"format":   s.Format,
		"bitrate":  strconv.Itoa(s.Bitrate),
		"key_tier": s.KeyTier,
	}
	for i := range m.fields {
		f := &m.fields[i]
		nextVal, ok := updates[f.key]
		if !ok {
			continue
		}
		if f.input.Value() == f.initial {
			f.input.SetValue(nextVal)
		}
		f.initial = nextVal
	}
}

func (m model) configLayout() configLayout {
	switch {
	case m.height >= 24 && m.contentWidth() >= 74:
		return configColumns
	case m.height >= 27:
		return configStacked
	case m.height >= 16:
		return configCompact
	default:
		return configTiny
	}
}

func (m *model) sizeConfig() {
	layout := m.configLayout()
	width := m.contentWidth()
	fieldWidth := width
	if layout == configColumns {
		fieldWidth = (width - 2) / 2
	}
	// Reserve space for marker (2), label (12), separator (1), card padding/borders (4), and badge.
	inputWidth := max(4, fieldWidth-29)
	for i := range m.cfgFields {
		m.cfgFields[i].input.Width = inputWidth
	}
}

func (m *model) blurConfigFields() {
	for i := range m.cfgFields {
		m.cfgFields[i].input.Blur()
	}
}

func (m *model) setConfigFocus(next int) tea.Cmd {
	m.blurConfigFields()
	total := len(m.cfgFields) + 2
	m.cfgFocus = (next + total) % total
	if m.cfgFocus < len(m.cfgFields) {
		return m.cfgFields[m.cfgFocus].input.Focus()
	}
	return nil
}

func (m model) configDirty() bool {
	for _, f := range m.cfgFields {
		if f.pendingUnset || f.input.Value() != f.initial {
			return true
		}
	}
	return false
}

func (m model) configFieldValue(key string) string {
	for _, f := range m.cfgFields {
		if f.key == key {
			return f.input.Value()
		}
	}
	return ""
}

func (m *model) cycleConfig(direction string) bool {
	if m.cfgFocus < 0 || m.cfgFocus >= len(m.cfgFields) {
		return false
	}
	f := &m.cfgFields[m.cfgFocus]
	provider := strings.ToLower(strings.TrimSpace(m.configFieldValue("provider")))
	var choices []string
	switch f.key {
	case "provider":
		choices = []string{core.ProviderGoogle, core.ProviderGemini}
	case "model":
		choices = []string{"", core.DefaultGeminiModel, core.GeminiFlashLiteTTSModel}
	case "key_tier":
		choices = []string{core.KeyTierAuto, core.KeyTierFree, core.KeyTierPaid}
	case "language":
		choices = append(choices, "")
		if provider == core.ProviderGemini {
			choices = append(choices, core.LanguageAuto)
		}
		choices = append(choices, core.Languages()...)
	case "voice":
		choices = voiceChoices(provider, m.configFieldValue("language"))
	case "format":
		choices = []string{core.FormatMP3, core.FormatWAV}
	case "bitrate":
		choices = []string{"8", "16", "24", "32", "40", "48", "56", "64", "80", "96", "112", "128", "144", "160", "192", "224", "256", "320"}
	case "jobs":
		for i := 1; i <= 16; i++ {
			choices = append(choices, strconv.Itoa(i))
		}
	default:
		return false
	}
	f.input.SetValue(cycleChoice(f.input.Value(), direction, choices))
	f.pendingUnset = false
	return true
}

func (m *model) unsetFocusedConfigField() {
	if m.cfgFocus < 0 || m.cfgFocus >= len(m.cfgFields) {
		return
	}
	f := &m.cfgFields[m.cfgFocus]
	f.pendingUnset = true
	if f.hasDefault {
		f.input.SetValue(f.defaultValue)
	} else {
		f.input.SetValue("")
	}
	if !m.busy {
		m.status = fmt.Sprintf("Marked %s to unset · Ctrl+S to save", f.key)
	}
}

func (m *model) refreshConfig() tea.Cmd {
	if m.loadConfig == nil {
		return nil
	}
	m.cfgLoadID++
	m.cfgLoading = true
	m.cfgError = ""
	id, load, ctx := m.cfgLoadID, m.loadConfig, m.ctx
	workers := m.workers
	if workers != nil {
		workers.Add(1)
	}
	result := make(chan configLoadedMsg, 1)
	go func() {
		if workers != nil {
			defer workers.Done()
		}
		snapshot, err := load(ctx)
		result <- configLoadedMsg{id: id, snapshot: snapshot, err: err}
	}()
	work := func() tea.Msg { return <-result }
	return tea.Batch(work, m.spinner.Tick)
}

func (m model) saveConfig() (tea.Model, tea.Cmd) {
	if m.cfgSaving {
		return m, nil
	}
	if m.saveConfigFn == nil {
		m.status = "Error: configuration saving is not available"
		return m, nil
	}
	set := map[string]string{}
	var unset []string
	for _, f := range m.cfgFields {
		val := f.input.Value()
		if f.pendingUnset {
			unset = append(unset, f.key)
			continue
		}
		if val == f.initial {
			continue
		}
		if strings.TrimSpace(val) == "" && (f.key == "model" || f.key == "language" || f.key == "style") {
			unset = append(unset, f.key)
			continue
		}
		set[f.key] = val
	}
	if len(set) == 0 && len(unset) == 0 {
		if !m.busy {
			m.status = "No configuration changes to save."
		}
		return m, nil
	}
	m.cfgSaving = true
	m.cfgSaveID++
	m.cfgError = ""
	id, save, ctx := m.cfgSaveID, m.saveConfigFn, m.ctx
	workers := m.workers
	if workers != nil {
		workers.Add(1)
	}
	if !m.busy {
		m.status = "Saving configuration…"
	}
	result := make(chan configSavedMsg, 1)
	go func() {
		if workers != nil {
			defer workers.Done()
		}
		snapshot, err := save(ctx, set, unset)
		result <- configSavedMsg{id: id, snapshot: snapshot, err: err}
	}()
	work := func() tea.Msg { return <-result }
	return m, tea.Batch(work, m.spinner.Tick)
}

func (m model) configView() []string {
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "250"})
	warn := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "221"})
	label := lipgloss.NewStyle().Bold(true)
	width := m.contentWidth()
	layout := m.configLayout()

	badge := func(f configField) string {
		switch {
		case f.pendingUnset:
			return warn.Render("unset*")
		case f.input.Value() != f.initial:
			return accent.Render("edited*")
		case f.hasEnv:
			if f.envValue != "" && f.envValue != f.input.Value() {
				return warn.Render("env:" + safe(f.envValue))
			}
			return warn.Render("env")
		case f.hasStored:
			return muted.Render("yaml")
		case f.hasDefault:
			return muted.Render("default")
		default:
			return muted.Render("unset")
		}
	}

	fieldRow := func(i int) string {
		f := m.cfgFields[i]
		name := fmt.Sprintf("%-12s", f.label)
		b := badge(f)
		if m.cfgFocus == i {
			row := accent.Render("› "+name) + " " + f.input.View()
			if b != "" && lipgloss.Width(row)+2+lipgloss.Width(b) <= max(1, width-4) {
				row += "  " + b
			}
			return row
		}
		value := safe(f.input.Value())
		if f.pendingUnset {
			value = muted.Render("(unset)")
		} else if value == "" {
			value = muted.Render("—")
		}
		row := "  " + muted.Render(name) + " " + value
		if b != "" {
			row += "  " + b
		}
		return row
	}

	saveLabel := renderAction("Save", "Ctrl+S", m.cfgFocus == len(m.cfgFields), true)
	if m.cfgSaving {
		saveLabel = accent.Render(m.spinner.View() + " Saving…")
	}
	reloadLabel := renderAction("Reload", "Ctrl+R", m.cfgFocus == len(m.cfgFields)+1, false)
	if m.cfgLoading {
		reloadLabel = accent.Render(m.spinner.View() + " Loading…")
	}
	actions := saveLabel + "  " + reloadLabel
	if width >= 62 {
		actions += muted.Render("  ·  Ctrl+U: unset field")
	}

	title := accent.Render("Configuration")
	if layout == configTiny {
		active := actions
		if m.cfgFocus < len(m.cfgFields) {
			active = fieldRow(m.cfgFocus)
		}
		return []string{title, active}
	}

	subtitleParts := []string{}
	if p := safe(m.cfgSnapshot.Path); p != "" {
		subtitleParts = append(subtitleParts, p)
	} else {
		subtitleParts = append(subtitleParts, "Shared YAML defaults")
	}
	if len(m.cfgSnapshot.Env) > 0 {
		envNames := make([]string, 0, len(m.cfgSnapshot.Env))
		for k := range m.cfgSnapshot.Env {
			envNames = append(envNames, "TTS_"+strings.ToUpper(k))
		}
		sort.Strings(envNames)
		subtitleParts = append(subtitleParts, "env: "+strings.Join(envNames, ", "))
	}
	if m.configDirty() {
		subtitleParts = append(subtitleParts, "unsaved")
	}
	subtitle := muted.Render(strings.Join(subtitleParts, " · "))
	if m.cfgError != "" {
		subtitle = warn.Render(safe(m.cfgError))
	}

	content := []string{title, subtitle, ""}
	switch layout {
	case configCompact:
		window := max(3, min(len(m.cfgFields), m.height-11))
		focused := min(m.cfgFocus, len(m.cfgFields)-1)
		start := max(0, focused-window/2)
		if start+window > len(m.cfgFields) {
			start = max(0, len(m.cfgFields)-window)
		}
		lines := []string{label.Render("Settings") + muted.Render("  ·  ←/→ choices · Ctrl+U unset")}
		for i := start; i < min(len(m.cfgFields), start+window); i++ {
			lines = append(lines, fieldRow(i))
		}
		content = append(content, renderCard(width, m.cfgFocus < len(m.cfgFields), lines...))
	default:
		group := func(w, start, end int, heading string) string {
			lines := []string{heading}
			for i := start; i < end; i++ {
				lines = append(lines, fieldRow(i))
			}
			return renderCard(w, m.cfgFocus >= start && m.cfgFocus < end, lines...)
		}
		voice := label.Render("Defaults & Voice") + muted.Render("  ·  ←/→ choices")
		audio := label.Render("Audio & Storage") + muted.Render("  ·  ←/→ choices")
		if layout == configColumns {
			left := (width - 2) / 2
			content = append(content, lipgloss.JoinHorizontal(lipgloss.Top,
				group(left, 0, 6, voice), "  ", group(width-left-2, 6, len(m.cfgFields), audio)))
		} else {
			content = append(content, group(width, 0, 6, voice), group(width, 6, len(m.cfgFields), audio))
		}
	}
	return append(content, "", actions)
}
