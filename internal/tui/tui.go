// Package tui provides the interactive generation screen.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/smkod/tts-cli/internal/core"
)

type GenerateFunc func(context.Context, map[string]string, string, core.Options) (core.Generation, error)
type Options struct {
	Settings   core.Settings
	Generate   GenerateFunc
	LoadUsage  func(context.Context) (UsageSnapshot, error)
	LoadConfig LoadConfigFunc
	SaveConfig SaveConfigFunc
}

// Run owns the program and joins cancelled work before returning to its caller.
func Run(ctx context.Context, options Options, input io.Reader, output io.Writer) error {
	if options.Generate == nil {
		return errors.New("generation callback is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	generate := options.Generate
	options.Generate = func(
		ctx context.Context,
		values map[string]string,
		text string,
		opts core.Options,
	) (core.Generation, error) {
		defer workers.Done()
		return generate(ctx, values, text, opts)
	}
	m := newModel(runCtx, options)
	m.workers = &workers
	_, err := tea.NewProgram(
		m,
		tea.WithContext(runCtx),
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithAltScreen(),
	).Run()
	cancel()
	workers.Wait()
	if err != nil {
		return fmt.Errorf("run generation screen: %w", err)
	}
	return nil
}

type field struct {
	key, label, initial string
	input               textinput.Model
}
type resultMsg struct {
	id     uint64
	result core.Generation
	err    error
}
type model struct {
	ctx                  context.Context
	generate             GenerateFunc
	text                 textarea.Model
	fields               []field
	focus, width, height int
	spinner              spinner.Model
	busy, quitting       bool
	cancel               context.CancelFunc
	job                  uint64
	status               string
	workers              *sync.WaitGroup
	screen               string
	loadUsage            func(context.Context) (UsageSnapshot, error)
	usage                UsageSnapshot
	usageID              uint64
	usageLoading         bool
	usageError           string
	usageOffset          int
	loadConfig           LoadConfigFunc
	saveConfigFn         SaveConfigFunc
	cfgSnapshot          ConfigSnapshot
	cfgFields            []configField
	cfgFocus             int
	cfgLoadID, cfgSaveID uint64
	cfgLoaded            bool
	cfgLoading           bool
	cfgSaving            bool
	cfgError             string
}

func newModel(ctx context.Context, options Options) model {
	text := textarea.New()
	text.Placeholder = "Write or paste the text to speak…"
	text.CharLimit = 0
	text.SetHeight(5)
	text.ShowLineNumbers = false
	text.Focus()
	s := options.Settings
	values := []struct{ key, label, value string }{
		{"provider", "Provider", s.Provider}, {"model", "Model", s.Model},
		{"language", "Language", s.Language}, {"voice", "Voice", s.Voice},
		{"style", "Style", s.Style}, {"output", "Output file", ""},
		{"format", "Format", s.Format}, {"bitrate", "MP3 bitrate", strconv.Itoa(s.Bitrate)},
		{"key_tier", "Key tier", s.KeyTier},
	}
	fields := make([]field, 0, len(values))
	for _, v := range values {
		input := textinput.New()
		input.Prompt = ""
		if v.key == "output" {
			input.Placeholder = "Optional"
		}
		input.SetValue(v.value)
		input.CharLimit = 0
		fields = append(fields, field{key: v.key, label: v.label, initial: v.value, input: input})
	}
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	snapshot := defaultConfigSnapshot(s)
	m := model{ctx: ctx, generate: options.Generate, text: text, fields: fields,
		screen: "generate", loadUsage: options.LoadUsage,
		loadConfig: options.LoadConfig, saveConfigFn: options.SaveConfig,
		cfgSnapshot: snapshot, cfgFields: newConfigFields(snapshot),
		spinner: spin,
		width:   80, height: 30,
		status: "Ready · generation starts only when you ask."}
	m.sizeGenerate()
	m.sizeConfig()
	return m
}

func (m model) Init() tea.Cmd { return textarea.Blink }
func (m *model) blurGenerateFields() {
	m.text.Blur()
	for i := range m.fields {
		m.fields[i].input.Blur()
	}
}

func (m *model) setFocus(next int) tea.Cmd {
	m.blurGenerateFields()
	m.focus = (next + len(m.fields) + 3) % (len(m.fields) + 3)
	if m.focus == 0 {
		return m.text.Focus()
	}
	if m.focus <= len(m.fields) {
		return m.fields[m.focus-1].input.Focus()
	}
	return nil
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case usageMsg:
		if msg.id != m.usageID {
			return m, nil
		}
		m.usageLoading = false
		m.usageError = ""
		if msg.err != nil {
			m.usageError = msg.err.Error()
		} else {
			m.usage = msg.snapshot
			m.usageOffset = min(m.usageOffset, m.maxUsageOffset())
		}
		return m, nil
	case configLoadedMsg:
		if msg.id != m.cfgLoadID {
			return m, nil
		}
		m.cfgLoading = false
		if msg.err != nil {
			m.cfgError = msg.err.Error()
			if !m.busy {
				m.status = "Config load error: " + msg.err.Error()
			}
		} else {
			m.applyConfigSnapshot(msg.snapshot)
			m.cfgLoaded = true
			m.cfgError = ""
			if !m.busy && m.screen == "config" {
				m.status = "Loaded configuration · " + safe(msg.snapshot.Path)
			}
		}
		return m, nil
	case configSavedMsg:
		if msg.id != m.cfgSaveID {
			return m, nil
		}
		m.cfgSaving = false
		if msg.err != nil {
			m.cfgError = msg.err.Error()
			if !m.busy {
				m.status = "Error: " + msg.err.Error()
			}
		} else {
			m.applyConfigSnapshot(msg.snapshot)
			m.applySettingsToGenerate(msg.snapshot.Effective)
			m.cfgLoaded = true
			m.cfgError = ""
			if !m.busy {
				m.status = "Saved configuration · " + safe(msg.snapshot.Path)
			}
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.usageOffset = min(m.usageOffset, m.maxUsageOffset())
		m.sizeGenerate()
		m.sizeConfig()
		return m, nil
	case resultMsg:
		if msg.id != m.job {
			return m, nil
		}
		m.busy = false
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		if m.quitting {
			return m, tea.Quit
		}
		switch {
		case errors.Is(msg.err, context.Canceled) || (msg.err != nil && core.Public(msg.err).Code == "CANCELED"):
			m.status = "Cancelled. Your text and settings are preserved."
		case msg.err != nil:
			m.status = "Error: " + msg.err.Error() + " · Edit settings and try again."
		default:
			source := "Generated"
			if msg.result.Cached {
				source = "Cached"
			}
			if msg.result.DryRun {
				source = "Preview (no API call)"
			}
			m.status = fmt.Sprintf("%s · %s · %s / %s / %s · %d characters",
				source,
				msg.result.File,
				msg.result.Provider,
				msg.result.Voice,
				msg.result.Language,
				msg.result.Characters,
			)
		}
		return m, m.refreshUsage()
	case tea.KeyMsg:
		if m.screen == "usage" {
			switch msg.String() {
			case "r":
				return m, m.refreshUsage()
			case "down", "pgdown":
				m.usageOffset = min(m.usageOffset+1, m.maxUsageOffset())
				return m, nil
			case "up", "pgup":
				m.usageOffset = max(0, m.usageOffset-1)
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			if m.busy {
				m.quitting = true
				m.cancel()
				m.status = "Cancelling before exit…"
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.busy {
				m.cancel()
				m.status = "Cancelling…"
			}
			return m, nil
		case "f1":
			m.screen = "generate"
			m.blurConfigFields()
			return m, m.setFocus(m.focus)
		case "f2":
			m.screen = "usage"
			m.blurGenerateFields()
			m.blurConfigFields()
			return m, m.refreshUsage()
		case "f3":
			m.screen = "config"
			m.blurGenerateFields()
			focusCmd := m.setConfigFocus(m.cfgFocus)
			if !m.cfgLoaded || !m.configDirty() {
				return m, tea.Batch(focusCmd, m.refreshConfig())
			}
			return m, focusCmd
		case "ctrl+r":
			if m.screen == "config" {
				return m, m.refreshConfig()
			}
			return m, m.refreshUsage()
		case "ctrl+s":
			if m.screen != "config" {
				return m, nil
			}
			return m.saveConfig()
		case "ctrl+u":
			if m.screen != "config" {
				return m, nil
			}
			m.unsetFocusedConfigField()
			return m, nil
		case "ctrl+p":
			if m.screen != "generate" {
				return m, nil
			}
			return m.submitPreview(true)
		case "tab":
			if m.screen == "config" {
				return m, m.setConfigFocus(m.cfgFocus + 1)
			}
			if m.screen != "generate" {
				return m, nil
			}
			return m, m.setFocus(m.focus + 1)
		case "shift+tab":
			if m.screen == "config" {
				return m, m.setConfigFocus(m.cfgFocus - 1)
			}
			if m.screen != "generate" {
				return m, nil
			}
			return m, m.setFocus(m.focus - 1)
		case "down":
			if m.screen == "config" {
				return m, m.setConfigFocus(m.cfgFocus + 1)
			}
		case "up":
			if m.screen == "config" {
				return m, m.setConfigFocus(m.cfgFocus - 1)
			}
		case "ctrl+g":
			if m.screen != "generate" {
				return m, nil
			}
			return m.submit()
		case "left", "right":
			if m.screen == "generate" && m.cycle(msg.String()) {
				return m, nil
			}
			if m.screen == "config" && m.cycleConfig(msg.String()) {
				return m, nil
			}
		case "enter":
			if m.screen == "config" {
				if m.cfgFocus == len(m.cfgFields) {
					return m.saveConfig()
				}
				if m.cfgFocus == len(m.cfgFields)+1 {
					return m, m.refreshConfig()
				}
				return m, nil
			}
			if m.screen != "generate" {
				return m, nil
			}
			if m.focus == len(m.fields)+2 {
				return m.submitPreview(true)
			}
			if m.focus == len(m.fields)+1 {
				return m.submit()
			}
		}
	}
	var cmd tea.Cmd
	if m.screen == "usage" {
		if m.busy || m.cfgLoading || m.cfgSaving {
			m.spinner, cmd = m.spinner.Update(msg)
		}
		return m, cmd
	}
	if m.screen == "config" {
		if m.cfgFocus >= 0 && m.cfgFocus < len(m.cfgFields) {
			prev := m.cfgFields[m.cfgFocus].input.Value()
			m.cfgFields[m.cfgFocus].input, cmd = m.cfgFields[m.cfgFocus].input.Update(msg)
			if m.cfgFields[m.cfgFocus].input.Value() != prev {
				m.cfgFields[m.cfgFocus].pendingUnset = false
			}
		}
		if m.busy || m.cfgLoading || m.cfgSaving {
			var tick tea.Cmd
			m.spinner, tick = m.spinner.Update(msg)
			cmd = tea.Batch(cmd, tick)
		}
		return m, cmd
	}
	if m.focus == 0 {
		m.text, cmd = m.text.Update(msg)
	} else if m.focus <= len(m.fields) {
		m.fields[m.focus-1].input, cmd = m.fields[m.focus-1].input.Update(msg)
	}
	if m.busy || m.cfgLoading || m.cfgSaving {
		var tick tea.Cmd
		m.spinner, tick = m.spinner.Update(msg)
		cmd = tea.Batch(cmd, tick)
	}
	return m, cmd
}
func (m model) submit() (tea.Model, tea.Cmd) { return m.submitPreview(false) }
func (m model) submitPreview(preview bool) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if err := core.ValidateText(m.text.Value()); err != nil {
		m.status = "Error: " + err.Error()
		return m, nil
	}
	if m.generate == nil {
		m.status = "Error: generation callback is required"
		return m, nil
	}
	values := make(map[string]string)
	opts := core.Options{DryRun: preview, ShowCost: !preview}
	for _, f := range m.fields {
		// Preserve config provenance: resolved locale/voice are not explicit user overrides.
		if (f.key == "language" || f.key == "voice") && f.input.Value() == f.initial {
			continue
		}
		if f.key == "output" {
			opts.Output = f.input.Value()
		} else {
			values[f.key] = f.input.Value()
		}
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.busy = true
	m.job++
	id, generate, text := m.job, m.generate, m.text.Value()
	if m.workers != nil {
		m.workers.Add(1)
	}
	m.status = "Generating… Esc cancels this request."
	// Start before returning the command so program shutdown cannot discard a
	// scheduled command while leaving the joined worker count outstanding.
	result := make(chan resultMsg, 1)
	go func() {
		generation, err := generate(ctx, values, text, opts)
		result <- resultMsg{id: id, result: generation, err: err}
	}()
	work := func() tea.Msg { return <-result }
	return m, tea.Batch(work, m.spinner.Tick)
}
