package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/smkod/tts-cli/internal/core"
)

func key(m model, value string) model {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
	switch value {
	case "tab":
		msg.Type = tea.KeyTab
	case "ctrl+g":
		msg.Type = tea.KeyCtrlG
	case "esc":
		msg.Type = tea.KeyEsc
	case "ctrl+q":
		msg.Type = tea.KeyCtrlQ
	}
	next, _ := m.Update(msg)
	return next.(model)
}
func TestNavigationAndResize(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = key(m, "tab")
	if m.focus != 1 || !m.fields[0].input.Focused() {
		t.Fatal("provider not focused")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 30}, {Width: 20, Height: 10}, {Width: 1, Height: 1}} {
		next, _ := m.Update(size)
		m = next.(model)
		if lines := strings.Count(m.View(), "\n") + 1; lines > size.Height {
			t.Fatalf("height %d > %d", lines, size.Height)
		}
	}
}
func TestGenerationState(t *testing.T) {
	m := newModel(context.Background(), Options{Generate: func(
		context.Context,
		map[string]string,
		string,
		core.Options,
	) (core.Generation, error) {
		return core.Generation{}, nil
	}})
	m.text.SetValue("  Original text\n")
	next, cmd := m.submit()
	m = next.(model)
	if !m.busy || cmd == nil {
		t.Fatal("generation did not start")
	}
	_, duplicate := m.submit()
	if duplicate != nil {
		t.Fatal("duplicate submission")
	}
	next, _ = m.Update(resultMsg{id: m.job, err: errors.New("test failure")})
	m = next.(model)
	if m.busy || !strings.Contains(m.status, "test failure") || m.text.Value() != "  Original text\n" {
		t.Fatal("failed generation lost state")
	}
	next, _ = m.submit()
	m = next.(model)
	m = key(m, "esc")
	next, _ = m.Update(resultMsg{id: m.job, err: context.Canceled})
	m = next.(model)
	if !strings.Contains(m.status, "Cancelled") {
		t.Fatal(m.status)
	}
	next, _ = m.submit()
	m = next.(model)
	next, _ = m.Update(resultMsg{id: m.job, result: core.Generation{File: "audio.wav", Cached: true}})
	m = next.(model)
	if !strings.Contains(m.status, "Cached") || !strings.Contains(m.status, "audio.wav") {
		t.Fatal(m.status)
	}
}
func TestEmptyTextDoesNotGenerate(t *testing.T) {
	m := newModel(context.Background(), Options{})
	next, cmd := m.submit()
	if cmd != nil || next.(model).busy {
		t.Fatal("blank input submitted")
	}
}

func TestSnapshotAndQuitCancellation(t *testing.T) {
	received := make(chan string, 1)
	cancelled := make(chan struct{})
	m := newModel(context.Background(), Options{Generate: func(
		ctx context.Context,
		values map[string]string,
		text string,
		opts core.Options,
	) (core.Generation, error) {
		received <- text + "|" + values["provider"] + "|" + opts.Output
		<-ctx.Done()
		close(cancelled)
		return core.Generation{}, ctx.Err()
	}})
	m.text.SetValue(" Original\n")
	m.fields[0].input.SetValue("google")
	m.fields[5].input.SetValue("test.wav")
	next, _ := m.submit()
	m = next.(model)
	m.text.SetValue("changed")
	m.fields[0].input.SetValue("gemini")
	if got := <-received; got != " Original\n|google|test.wav" {
		t.Fatalf("snapshot = %q", got)
	}
	m = key(m, "ctrl+q")
	if !m.quitting || !m.busy {
		t.Fatal("quit did not wait for result")
	}
	<-cancelled
	next, cmd := m.Update(resultMsg{id: m.job, err: context.Canceled})
	if cmd == nil || next.(model).busy {
		t.Fatal("cancelled job did not finish quit")
	}
}

func TestConfigOverridesPreserveLanguageProvenance(t *testing.T) {
	c := core.Config{Stored: map[string]string{
		"provider": "google", "key_tier": "auto", "voice": "kore", "format": "wav",
		"bitrate": "128", "jobs": "1", "output_dir": t.TempDir(), "cache_dir": t.TempDir(),
	}}
	initial, err := c.Resolve(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan map[string]string, 1)
	m := newModel(context.Background(), Options{Settings: initial, Generate: func(
		_ context.Context,
		values map[string]string,
		_ string,
		_ core.Options,
	) (core.Generation, error) {
		captured <- values
		return core.Generation{}, nil
	}})
	m.text.SetValue("Test")
	m.submit()
	values := <-captured
	if _, ok := values["language"]; ok {
		t.Fatal("unchanged language made explicit")
	}
	if _, ok := values["voice"]; ok {
		t.Fatal("unchanged resolved voice made explicit")
	}
	// The CLI interprets empty style as explicit clearing through ResolveInteractive.
	resolved, err := c.ResolveInteractive(values)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.LanguagePinned != initial.LanguagePinned {
		t.Fatal("language provenance changed")
	}
}
func TestPublicCancellationAndTinyEditor(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.text.SetValue("VISIBLE")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	m = next.(model)
	if !strings.Contains(m.View(), "VISIBLE") {
		t.Fatal("focused editor hidden")
	}
	next, _ = m.Update(resultMsg{id: m.job, err: core.Fail("CANCELED", "cancelled", false)})
	m = next.(model)
	if !strings.Contains(m.status, "Cancelled") {
		t.Fatal(m.status)
	}
}
