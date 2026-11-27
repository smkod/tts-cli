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

// ProviderKey holds non-secret metadata for a configured provider API key.
type ProviderKey struct {
	Name      string
	Tier      string
	MaskedKey string
	Source    string
}

// ConfigSnapshot holds stored YAML settings, environment overrides, defaults, and resolved effective settings.
type ConfigSnapshot struct {
	Path      string
	Explicit  bool
	Stored    map[string]string
	Env       map[string]string
	Defaults  map[string]string
	Effective core.Settings
	Keys      []ProviderKey
}

type LoadConfigFunc func(context.Context) (ConfigSnapshot, error)
type SaveConfigFunc func(context.Context, map[string]string, []string) (ConfigSnapshot, error)
type AddKeyFunc func(ctx context.Context, name, tier, apiKey string) ([]ProviderKey, error)
type UpdateKeyFunc func(ctx context.Context, targetName, newName, newTier string) ([]ProviderKey, error)
type RemoveKeyFunc func(ctx context.Context, targetName string) ([]ProviderKey, error)

type configLoadedMsg struct {
	id       uint64
	snapshot ConfigSnapshot
	err      error
}

type configSavedMsg struct {
	id       uint64
	snapshot ConfigSnapshot
	keysOnly bool
	keys     []ProviderKey
	hasKeys  bool
	err      error
}

type keysUpdatedMsg struct {
	id     uint64
	keys   []ProviderKey
	status string
	err    error
}

type configKeyItem struct {
	initialName string
	initialTier string
	tier        string
	maskedKey   string
	source      string
	nameInput   textinput.Model
}

func (k configKeyItem) dirty() bool {
	if k.source != "file" {
		return false
	}
	return strings.TrimSpace(k.nameInput.Value()) != k.initialName || k.tier != k.initialTier
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

func (m *model) applyProviderKeys(keys []ProviderKey) {
	items := make([]configKeyItem, 0, len(keys))
	for _, k := range keys {
		tier := strings.ToLower(strings.TrimSpace(k.Tier))
		if tier != core.KeyTierPaid {
			tier = core.KeyTierFree
		}
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = "Key name"
		input.CharLimit = 0
		input.SetValue(k.Name)
		items = append(items, configKeyItem{
			initialName: k.Name,
			initialTier: tier,
			tier:        tier,
			maskedKey:   k.MaskedKey,
			source:      k.Source,
			nameInput:   input,
		})
	}
	m.cfgKeys = items
	m.sizeConfig()
}

func (m *model) applyConfigSnapshot(snapshot ConfigSnapshot) {
	m.cfgSnapshot = snapshot
	if len(m.cfgFields) == 0 {
		m.cfgFields = newConfigFields(snapshot)
	} else {
		applySnapshotToFields(m.cfgFields, snapshot)
	}
	m.applyProviderKeys(snapshot.Keys)
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
	keyNameWidth := max(8, min(22, width-42))
	for i := range m.cfgKeys {
		m.cfgKeys[i].nameInput.Width = keyNameWidth
	}
	formInputWidth := max(8, width-24)
	m.cfgNewKeyName.Width = formInputWidth
	m.cfgNewKeySecret.Width = formInputWidth
}

func (m *model) blurConfigFields() {
	for i := range m.cfgFields {
		m.cfgFields[i].input.Blur()
	}
	for i := range m.cfgKeys {
		m.cfgKeys[i].nameInput.Blur()
	}
	m.cfgNewKeyName.Blur()
	m.cfgNewKeySecret.Blur()
}

func (m model) cfgKeyIndex() int {
	start := len(m.cfgFields)
	if m.cfgFocus >= start && m.cfgFocus < start+len(m.cfgKeys) {
		return m.cfgFocus - start
	}
	return -1
}

func (m model) cfgAddFormStart() int {
	return len(m.cfgFields) + len(m.cfgKeys)
}

func (m model) cfgAddButtonFocus() int {
	return m.cfgAddFormStart()
}

func (m model) cfgAddSubmitFocus() int {
	return m.cfgAddFormStart() + 3
}

func (m model) cfgAddCancelFocus() int {
	return m.cfgAddFormStart() + 4
}

func (m model) cfgSaveFocus() int {
	if m.cfgAddingKey {
		return m.cfgAddFormStart() + 5
	}
	return m.cfgAddFormStart() + 1
}

func (m model) cfgReloadFocus() int {
	return m.cfgSaveFocus() + 1
}

func (m model) cfgTotalFocus() int {
	return m.cfgReloadFocus() + 1
}

func (m *model) setConfigFocus(next int) tea.Cmd {
	m.blurConfigFields()
	total := m.cfgTotalFocus()
	m.cfgFocus = (next + total) % total
	if m.cfgFocus < len(m.cfgFields) {
		return m.cfgFields[m.cfgFocus].input.Focus()
	}
	if idx := m.cfgKeyIndex(); idx >= 0 {
		if m.cfgKeys[idx].source == "file" {
			return m.cfgKeys[idx].nameInput.Focus()
		}
		return nil
	}
	if m.cfgAddingKey {
		switch m.cfgFocus {
		case m.cfgAddFormStart():
			return m.cfgNewKeyName.Focus()
		case m.cfgAddFormStart() + 2:
			return m.cfgNewKeySecret.Focus()
		}
	}
	return nil
}

func (m model) configDirty() bool {
	for _, f := range m.cfgFields {
		if f.pendingUnset || f.input.Value() != f.initial {
			return true
		}
	}
	for _, k := range m.cfgKeys {
		if k.dirty() {
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
	if idx := m.cfgKeyIndex(); idx >= 0 {
		if m.cfgKeys[idx].source != "file" {
			if !m.busy {
				m.status = "Environment keys are read-only; edit via environment variables."
			}
			return true
		}
		m.cfgKeys[idx].tier = cycleChoice(m.cfgKeys[idx].tier, direction, []string{core.KeyTierFree, core.KeyTierPaid})
		return true
	}
	if m.cfgAddingKey && m.cfgFocus == m.cfgAddFormStart()+1 {
		m.cfgNewKeyTier = cycleChoice(m.cfgNewKeyTier, direction, []string{core.KeyTierFree, core.KeyTierPaid})
		return true
	}
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
	if m.cfgSaving || m.cfgKeyBusy {
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
	type keyEdit struct {
		target, name, tier string
	}
	var keyEdits []keyEdit
	for _, k := range m.cfgKeys {
		if !k.dirty() {
			continue
		}
		newName := strings.TrimSpace(k.nameInput.Value())
		if newName == "" {
			m.status = "Error: provider key name must not be empty."
			return m, nil
		}
		keyEdits = append(keyEdits, keyEdit{
			target: k.initialName,
			name:   newName,
			tier:   k.tier,
		})
	}
	hasConfigChanges := len(set) > 0 || len(unset) > 0
	if !hasConfigChanges && len(keyEdits) == 0 {
		if !m.busy {
			m.status = "No configuration changes to save."
		}
		return m, nil
	}
	if hasConfigChanges && m.saveConfigFn == nil {
		m.status = "Error: configuration saving is not available"
		return m, nil
	}
	if len(keyEdits) > 0 && m.updateKeyFn == nil {
		m.status = "Error: provider key management is not available"
		return m, nil
	}
	m.cfgSaving = true
	m.cfgSaveID++
	m.cfgError = ""
	id, save, updateKey, ctx := m.cfgSaveID, m.saveConfigFn, m.updateKeyFn, m.ctx
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
		var snapshot ConfigSnapshot
		var err error
		if hasConfigChanges {
			snapshot, err = save(ctx, set, unset)
			if err != nil {
				result <- configSavedMsg{id: id, err: err}
				return
			}
		}
		var keys []ProviderKey
		hasKeys := false
		for _, edit := range keyEdits {
			keys, err = updateKey(ctx, edit.target, edit.name, edit.tier)
			if err != nil {
				result <- configSavedMsg{id: id, snapshot: snapshot, keysOnly: !hasConfigChanges, err: err}
				return
			}
			hasKeys = true
		}
		if hasConfigChanges && hasKeys {
			snapshot.Keys = keys
		}
		result <- configSavedMsg{id: id, snapshot: snapshot, keysOnly: !hasConfigChanges, keys: keys, hasKeys: hasKeys}
	}()
	work := func() tea.Msg { return <-result }
	return m, tea.Batch(work, m.spinner.Tick)
}

func (m *model) clearNewKeyInputs() {
	m.cfgNewKeyName.SetValue("")
	m.cfgNewKeyTier = core.KeyTierFree
	m.cfgNewKeySecret.SetValue("")
}

func (m model) startAddKey() (tea.Model, tea.Cmd) {
	m.cfgAddingKey = true
	m.clearNewKeyInputs()
	focusCmd := m.setConfigFocus(m.cfgAddFormStart())
	if !m.busy {
		m.status = "Enter optional name, choose free/paid (←/→), and paste your Gemini API key."
	}
	return m, focusCmd
}

func (m *model) cancelAddKey() tea.Cmd {
	m.cfgAddingKey = false
	m.clearNewKeyInputs()
	if !m.busy {
		m.status = "Cancelled adding provider key."
	}
	return m.setConfigFocus(m.cfgAddButtonFocus())
}

func (m model) handleConfigEnter() (tea.Model, tea.Cmd) {
	if m.cfgFocus == m.cfgSaveFocus() {
		return m.saveConfig()
	}
	if m.cfgFocus == m.cfgReloadFocus() {
		return m, m.refreshConfig()
	}
	if !m.cfgAddingKey && m.cfgFocus == m.cfgAddButtonFocus() {
		return m.startAddKey()
	}
	if m.cfgAddingKey {
		if m.cfgFocus == m.cfgAddSubmitFocus() {
			return m.submitAddKey()
		}
		if m.cfgFocus == m.cfgAddCancelFocus() {
			return m, m.cancelAddKey()
		}
		if m.cfgFocus >= m.cfgAddFormStart() && m.cfgFocus < m.cfgAddSubmitFocus() {
			if strings.TrimSpace(m.cfgNewKeySecret.Value()) != "" {
				return m.submitAddKey()
			}
			return m, m.setConfigFocus(m.cfgAddFormStart() + 2)
		}
	}
	if idx := m.cfgKeyIndex(); idx >= 0 && m.cfgKeys[idx].dirty() {
		return m.submitUpdateKey(idx)
	}
	return m, nil
}

func (m model) runKeyOp(status string, fn func(context.Context) ([]ProviderKey, string, error)) (tea.Model, tea.Cmd) {
	if m.cfgKeyBusy || m.cfgSaving {
		return m, nil
	}
	m.cfgKeyBusy = true
	m.cfgKeyOpID++
	m.cfgError = ""
	id, ctx := m.cfgKeyOpID, m.ctx
	workers := m.workers
	if workers != nil {
		workers.Add(1)
	}
	if !m.busy && status != "" {
		m.status = status
	}
	result := make(chan keysUpdatedMsg, 1)
	go func() {
		if workers != nil {
			defer workers.Done()
		}
		keys, doneStatus, err := fn(ctx)
		result <- keysUpdatedMsg{id: id, keys: keys, status: doneStatus, err: err}
	}()
	work := func() tea.Msg { return <-result }
	return m, tea.Batch(work, m.spinner.Tick)
}

func (m model) submitAddKey() (tea.Model, tea.Cmd) {
	if m.addKeyFn == nil {
		m.status = "Error: provider key management is not available"
		return m, nil
	}
	apiKey := strings.TrimSpace(m.cfgNewKeySecret.Value())
	if apiKey == "" {
		m.status = "Error: API key must not be empty."
		return m, m.setConfigFocus(m.cfgAddFormStart() + 2)
	}
	name := strings.TrimSpace(m.cfgNewKeyName.Value())
	tier := m.cfgNewKeyTier
	if tier != core.KeyTierPaid {
		tier = core.KeyTierFree
	}
	addFn := m.addKeyFn
	return m.runKeyOp("Saving provider key…", func(ctx context.Context) ([]ProviderKey, string, error) {
		keys, err := addFn(ctx, name, tier, apiKey)
		if err != nil {
			return nil, "", err
		}
		return keys, fmt.Sprintf("Saved %s Gemini key.", tier), nil
	})
}

func (m model) submitUpdateKey(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.cfgKeys) {
		return m, nil
	}
	k := m.cfgKeys[idx]
	if k.source != "file" {
		m.status = "Environment keys are read-only; edit via environment variables."
		return m, nil
	}
	if m.updateKeyFn == nil {
		m.status = "Error: provider key management is not available"
		return m, nil
	}
	newName := strings.TrimSpace(k.nameInput.Value())
	if newName == "" {
		m.status = "Error: provider key name must not be empty."
		return m, nil
	}
	target, newTier := k.initialName, k.tier
	updateFn := m.updateKeyFn
	return m.runKeyOp("Updating provider key…", func(ctx context.Context) ([]ProviderKey, string, error) {
		keys, err := updateFn(ctx, target, newName, newTier)
		if err != nil {
			return nil, "", err
		}
		return keys, fmt.Sprintf("Updated key %s (%s).", newName, newTier), nil
	})
}

func (m model) removeKey(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.cfgKeys) {
		return m, nil
	}
	k := m.cfgKeys[idx]
	if k.source != "file" {
		m.status = "Environment keys are read-only; edit via environment variables."
		return m, nil
	}
	if m.removeKeyFn == nil {
		m.status = "Error: provider key management is not available"
		return m, nil
	}
	target := k.initialName
	removeFn := m.removeKeyFn
	return m.runKeyOp("Removing provider key…", func(ctx context.Context) ([]ProviderKey, string, error) {
		keys, err := removeFn(ctx, target)
		if err != nil {
			return nil, "", err
		}
		return keys, fmt.Sprintf("Removed key %s.", target), nil
	})
}

func (m model) configView() []string {
	lines, _ := m.configLayoutLines(0, 0)
	return lines
}

func (m model) configLayoutLines(originX, originY int) ([]string, []mouseRegion) {
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "250"})
	warn := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "221"})
	label := lipgloss.NewStyle().Bold(true)
	width := m.contentWidth()
	layout := m.configLayout()
	var regions []mouseRegion

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

	freeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "78"})
	paidStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "94", Dark: "221"})
	tierBadge := func(tier string) string {
		if tier == core.KeyTierPaid {
			return paidStyle.Render("paid")
		}
		return freeStyle.Render("free")
	}

	keyRow := func(i int) string {
		k := m.cfgKeys[i]
		focused := m.cfgFocus == len(m.cfgFields)+i
		tb := tierBadge(k.tier)
		src := muted.Render("file")
		if k.source != "file" {
			src = warn.Render("env")
		}
		if focused {
			if k.source == "file" {
				row := accent.Render("› ") + k.nameInput.View() + "  " + tb + "  " + muted.Render(safe(k.maskedKey)) + "  " + src
				if k.dirty() {
					row += "  " + accent.Render("edited*")
				}
				return row
			}
			return accent.Render("› "+safe(k.initialName)) + "  " + tb + "  " + muted.Render(safe(k.maskedKey)) + "  " + warn.Render("env (read-only)")
		}
		name := safe(k.nameInput.Value())
		if name == "" {
			name = safe(k.initialName)
		}
		row := "  " + fmt.Sprintf("%-14s", name) + " " + tb + "  " + muted.Render(safe(k.maskedKey)) + "  " + src
		if k.dirty() {
			row += "  " + accent.Render("edited*")
		}
		return row
	}

	addKeyRows := func() []string {
		if !m.cfgAddingKey {
			if m.cfgFocus == m.cfgAddButtonFocus() {
				return []string{accent.Render("› + Add Gemini key") + muted.Render("  ·  Enter")}
			}
			return []string{"  " + muted.Render("+ Add Gemini key")}
		}
		formStart := m.cfgAddFormStart()
		formField := func(idx int, fieldLabel string, content string) string {
			name := fmt.Sprintf("%-12s", fieldLabel)
			if m.cfgFocus == idx {
				return accent.Render("› "+name) + " " + content
			}
			return "  " + muted.Render(name) + " " + content
		}
		nameVal := m.cfgNewKeyName.View()
		if m.cfgFocus != formStart {
			if v := safe(m.cfgNewKeyName.Value()); v != "" {
				nameVal = v
			} else {
				nameVal = muted.Render("Optional")
			}
		}
		secretVal := m.cfgNewKeySecret.View()
		if m.cfgFocus != formStart+2 {
			if strings.TrimSpace(m.cfgNewKeySecret.Value()) != "" {
				secretVal = strings.Repeat("*", min(12, len(m.cfgNewKeySecret.Value())))
			} else {
				secretVal = muted.Render("—")
			}
		}
		connVal := tierBadge(m.cfgNewKeyTier) + muted.Render("  (←/→ free/paid)")
		addBtn := renderAction("Add key", "Enter", m.cfgFocus == m.cfgAddSubmitFocus(), true)
		if m.cfgKeyBusy {
			addBtn = accent.Render(m.spinner.View() + " Saving…")
		}
		cancelBtn := renderAction("Cancel", "Esc", m.cfgFocus == m.cfgAddCancelFocus(), false)
		return []string{
			formField(formStart, "Key name", nameVal),
			formField(formStart+1, "Connection", connVal),
			formField(formStart+2, "API key", secretVal),
			"  " + addBtn + "  " + cancelBtn,
		}
	}

	providersCard := func(cardY, maxKeyRows int) (string, int) {
		heading := label.Render("Providers & Keys")
		if width >= 66 {
			heading += muted.Render("  ·  ←/→ free/paid · Enter save/add · Ctrl+D remove")
		}
		lines := []string{heading}
		if len(m.cfgKeys) == 0 {
			if !m.cfgAddingKey {
				lines = append(lines, muted.Render("  No Gemini API keys configured."))
			}
		} else {
			win := len(m.cfgKeys)
			if maxKeyRows > 0 && win > maxKeyRows {
				win = maxKeyRows
			}
			start := 0
			if idx := m.cfgKeyIndex(); idx >= 0 {
				start = max(0, idx-win/2)
			}
			if start+win > len(m.cfgKeys) {
				start = max(0, len(m.cfgKeys)-win)
			}
			for i := start; i < min(len(m.cfgKeys), start+win); i++ {
				rowY := cardY + 1 + len(lines)
				// Tier badge sits after the name column; put its region before the full-row focus region.
				tierX := originX + 2 + 2 + 15
				if m.cfgFocus == len(m.cfgFields)+i && m.cfgKeys[i].source == "file" {
					tierX = originX + 2 + 2 + m.cfgKeys[i].nameInput.Width + 2
				}
				regions = append(regions,
					mouseRegion{x: tierX, y: rowY, w: 6, h: 1, action: mouseCfgKeyTier, index: i},
					mouseRegion{x: originX, y: rowY, w: width, h: 1, action: mouseCfgFocus, index: len(m.cfgFields) + i},
				)
				lines = append(lines, keyRow(i))
			}
		}
		addStartY := cardY + 1 + len(lines)
		if !m.cfgAddingKey {
			regions = append(regions, mouseRegion{x: originX, y: addStartY, w: width, h: 1, action: mouseCfgAddOpen})
		} else {
			addBtn := renderAction("Add key", "Enter", m.cfgFocus == m.cfgAddSubmitFocus(), true)
			cancelBtn := renderAction("Cancel", "Esc", m.cfgFocus == m.cfgAddCancelFocus(), false)
			regions = append(regions,
				mouseRegion{x: originX, y: addStartY, w: width, h: 1, action: mouseCfgFocus, index: m.cfgAddFormStart()},
				mouseRegion{x: originX, y: addStartY + 1, w: width, h: 1, action: mouseCfgAddTier},
				mouseRegion{x: originX, y: addStartY + 2, w: width, h: 1, action: mouseCfgFocus, index: m.cfgAddFormStart() + 2},
				mouseRegion{x: originX + 4, y: addStartY + 3, w: lipgloss.Width(addBtn), h: 1, action: mouseCfgAddSubmit},
				mouseRegion{x: originX + 4 + lipgloss.Width(addBtn) + 2, y: addStartY + 3, w: lipgloss.Width(cancelBtn), h: 1, action: mouseCfgAddCancel},
			)
		}
		lines = append(lines, addKeyRows()...)
		focused := m.cfgFocus >= len(m.cfgFields) && m.cfgFocus < m.cfgSaveFocus()
		return renderCard(width, focused, lines...), len(lines) + 2
	}

	saveLabel := renderAction("Save", "Ctrl+S", m.cfgFocus == m.cfgSaveFocus(), true)
	if m.cfgSaving {
		saveLabel = accent.Render(m.spinner.View() + " Saving…")
	}
	reloadLabel := renderAction("Reload", "Ctrl+R", m.cfgFocus == m.cfgReloadFocus(), false)
	if m.cfgLoading {
		reloadLabel = accent.Render(m.spinner.View() + " Loading…")
	}
	actions := saveLabel + "  " + reloadLabel

	title := accent.Render("Configuration")
	if layout == configTiny {
		active := actions
		switch {
		case m.cfgFocus < len(m.cfgFields):
			active = fieldRow(m.cfgFocus)
		case m.cfgKeyIndex() >= 0:
			active = keyRow(m.cfgKeyIndex())
		case m.cfgFocus >= m.cfgAddFormStart() && m.cfgFocus < m.cfgSaveFocus():
			rows := addKeyRows()
			if len(rows) == 1 {
				active = rows[0]
			} else {
				idx := min(len(rows)-1, m.cfgFocus-m.cfgAddFormStart())
				active = rows[idx]
			}
		default:
			regions = append(regions,
				mouseRegion{x: originX, y: originY + 1, w: lipgloss.Width(saveLabel), h: 1, action: mouseCfgSave},
				mouseRegion{x: originX + lipgloss.Width(saveLabel) + 2, y: originY + 1, w: lipgloss.Width(reloadLabel), h: 1, action: mouseCfgReload},
			)
		}
		return []string{title, active}, regions
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
	curY := originY + 3
	switch layout {
	case configCompact:
		if m.cfgFocus >= len(m.cfgFields) && m.cfgFocus < m.cfgSaveFocus() {
			maxKeys := max(1, m.height-14)
			card, cardH := providersCard(curY, maxKeys)
			content = append(content, card)
			curY += cardH
		} else {
			window := max(3, min(len(m.cfgFields), m.height-11))
			focused := min(m.cfgFocus, len(m.cfgFields)-1)
			start := max(0, focused-window/2)
			if start+window > len(m.cfgFields) {
				start = max(0, len(m.cfgFields)-window)
			}
			lines := []string{label.Render("Settings") + muted.Render("  ·  ←/→ choices · Ctrl+U unset")}
			for i := start; i < min(len(m.cfgFields), start+window); i++ {
				regions = append(regions, mouseRegion{x: originX, y: curY + 1 + len(lines), w: width, h: 1, action: mouseCfgFocus, index: i})
				lines = append(lines, fieldRow(i))
			}
			content = append(content, renderCard(width, m.cfgFocus < len(m.cfgFields), lines...))
			curY += len(lines) + 2
		}
	default:
		group := func(x, y, w, start, end int, heading string) string {
			lines := []string{heading}
			for i := start; i < end; i++ {
				regions = append(regions, mouseRegion{x: x, y: y + 2 + (i - start), w: w, h: 1, action: mouseCfgFocus, index: i})
				lines = append(lines, fieldRow(i))
			}
			return renderCard(w, m.cfgFocus >= start && m.cfgFocus < end, lines...)
		}
		voice := label.Render("Defaults & Voice") + muted.Render("  ·  ←/→ choices")
		audio := label.Render("Audio & Storage") + muted.Render("  ·  ←/→ choices")
		if layout == configColumns {
			left := (width - 2) / 2
			content = append(content, lipgloss.JoinHorizontal(lipgloss.Top,
				group(originX, curY, left, 0, 6, voice), "  ", group(originX+left+2, curY, width-left-2, 6, len(m.cfgFields), audio)))
			curY += 9 // 6 fields + heading + 2 borders
			maxKeys := max(1, m.height-20)
			if m.cfgAddingKey {
				maxKeys = max(1, m.height-23)
			}
			card, cardH := providersCard(curY, maxKeys)
			content = append(content, card)
			curY += cardH
		} else {
			inProviders := m.cfgFocus >= len(m.cfgFields) && m.cfgFocus < m.cfgSaveFocus()
			if m.height >= 31 {
				content = append(content, group(originX, curY, width, 0, 6, voice), group(originX, curY+9, width, 6, len(m.cfgFields), audio))
				curY += 9 + 8
				maxKeys := max(1, m.height-28)
				card, cardH := providersCard(curY, maxKeys)
				content = append(content, card)
				curY += cardH
			} else if inProviders {
				maxKeys := max(1, m.height-14)
				card, cardH := providersCard(curY, maxKeys)
				content = append(content, card)
				curY += cardH
			} else {
				content = append(content, group(originX, curY, width, 0, 6, voice), group(originX, curY+9, width, 6, len(m.cfgFields), audio))
				curY += 9 + 8
			}
		}
	}
	actionsY := curY + 1
	regions = append(regions,
		mouseRegion{x: originX, y: actionsY, w: lipgloss.Width(saveLabel), h: 1, action: mouseCfgSave},
		mouseRegion{x: originX + lipgloss.Width(saveLabel) + 2, y: actionsY, w: lipgloss.Width(reloadLabel), h: 1, action: mouseCfgReload},
	)
	return append(content, "", actions), regions
}
