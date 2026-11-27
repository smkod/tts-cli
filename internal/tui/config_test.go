package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/smkod/tts-cli/internal/core"
)

func TestConfigNavigationAndHiddenGenerateIsolation(t *testing.T) {
	var loads atomic.Int32
	m := newModel(context.Background(), Options{
		Generate: func(context.Context, map[string]string, string, core.Options) (core.Generation, error) {
			t.Error("hidden generate form submitted from config")
			return core.Generation{}, nil
		},
		LoadConfig: func(context.Context) (ConfigSnapshot, error) {
			loads.Add(1)
			return ConfigSnapshot{
				Path:     "/tmp/tts/config.yaml",
				Stored:   map[string]string{"format": core.FormatWAV},
				Env:      map[string]string{},
				Defaults: map[string]string{"provider": core.ProviderGoogle, "format": core.FormatMP3, "bitrate": "96", "voice": "random", "key_tier": core.KeyTierAuto, "output_dir": ".", "jobs": "4"},
				Effective: core.Settings{
					Provider: core.ProviderGoogle, Format: core.FormatWAV, Bitrate: 96, Voice: "random", KeyTier: core.KeyTierAuto, Language: "en-US", OutputDir: ".", Jobs: 4,
				},
			}, nil
		},
	})
	m.text.SetValue("Keep this script")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF3})
	m = next.(model)
	if m.screen != "config" || !m.cfgLoading || cmd == nil {
		t.Fatal("F3 did not enter config and start load")
	}
	if m.text.Focused() {
		t.Fatal("generate editor remained focused on config screen")
	}
	// Resolve the batched commands (focus blink + loadConfig) and apply loaded message.
	next, _ = m.Update(configLoadedMsg{
		id: m.cfgLoadID,
		snapshot: ConfigSnapshot{
			Path:     "/tmp/tts/config.yaml",
			Stored:   map[string]string{"format": core.FormatWAV},
			Env:      map[string]string{},
			Defaults: map[string]string{"provider": core.ProviderGoogle, "format": core.FormatMP3, "bitrate": "96", "voice": "random", "key_tier": core.KeyTierAuto, "output_dir": ".", "jobs": "4"},
			Effective: core.Settings{
				Provider: core.ProviderGoogle, Format: core.FormatWAV, Bitrate: 96, Voice: "random", KeyTier: core.KeyTierAuto, Language: "en-US", OutputDir: ".", Jobs: 4,
			},
		},
	})
	m = next.(model)
	if m.cfgLoading || !m.cfgLoaded || m.cfgFields[6].input.Value() != core.FormatWAV {
		t.Fatalf("config snapshot not applied: loading=%v loaded=%v format=%q", m.cfgLoading, m.cfgLoaded, m.cfgFields[6].input.Value())
	}

	// Hidden generate shortcuts must not trigger generation while on config.
	for _, kt := range []tea.KeyType{tea.KeyCtrlG, tea.KeyCtrlP} {
		next, gCmd := m.Update(tea.KeyMsg{Type: kt})
		m = next.(model)
		if m.busy || gCmd != nil {
			t.Fatalf("key %v triggered generate from config", kt)
		}
	}

	// Tab / Down / Up / Shift+Tab wrap around all config fields + Save + Reload.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.cfgFocus != 1 {
		t.Fatalf("down focus = %d, want 1", m.cfgFocus)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.cfgFocus != 0 {
		t.Fatalf("up focus = %d, want 0", m.cfgFocus)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(model)
	if m.cfgFocus != m.cfgReloadFocus() {
		t.Fatalf("wrap shift+tab focus = %d, want %d", m.cfgFocus, m.cfgReloadFocus())
	}

	// Make a draft edit, switch to Generate and back: unsaved draft must survive F3.
	m.setConfigFocus(0)
	m.cycleConfig("right") // google -> gemini
	if !m.configDirty() {
		t.Fatal("expected dirty config after cycling provider")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	m = next.(model)
	if m.screen != "generate" || m.text.Value() != "Keep this script" {
		t.Fatal("F1 lost generate screen or text")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyF3})
	m = next.(model)
	if m.cfgLoading || m.cfgFields[0].input.Value() != core.ProviderGemini {
		t.Fatal("F3 clobbered unsaved config draft")
	}
	// Explicit Ctrl+R reloads even when dirty.
	next, reloadCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(model)
	if !m.cfgLoading || reloadCmd == nil {
		t.Fatal("Ctrl+R did not trigger config reload")
	}
}

func TestConfigCyclingAndUnset(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*model)
		focusKey  string
		direction string
		check     func(*testing.T, string)
	}{
		{
			name:      "provider cycles google to gemini",
			focusKey:  "provider",
			direction: "right",
			check:     func(t *testing.T, got string) { wantEq(t, got, core.ProviderGemini) },
		},
		{
			name:      "model cycles unset to default gemini model",
			focusKey:  "model",
			direction: "right",
			check:     func(t *testing.T, got string) { wantEq(t, got, core.DefaultGeminiModel) },
		},
		{
			name:      "key tier cycles auto to paid backwards",
			focusKey:  "key_tier",
			direction: "left",
			check:     func(t *testing.T, got string) { wantEq(t, got, core.KeyTierPaid) },
		},
		{
			name: "language includes auto when provider is gemini",
			setup: func(m *model) {
				m.cfgFields[0].input.SetValue(core.ProviderGemini)
			},
			focusKey:  "language",
			direction: "right",
			check:     func(t *testing.T, got string) { wantEq(t, got, core.LanguageAuto) },
		},
		{
			name: "voice uses Gemini alias when provider is gemini",
			setup: func(m *model) {
				m.cfgFields[0].input.SetValue(core.ProviderGemini)
				m.cfgFields[3].input.SetValue("en-US")
				m.cfgFields[4].input.SetValue("random")
			},
			focusKey:  "voice",
			direction: "right",
			check: func(t *testing.T, got string) {
				if got == "random" || strings.Contains(got, "Chirp3") {
					t.Fatalf("expected Gemini alias, got %q", got)
				}
			},
		},
		{
			name:      "format cycles mp3 to wav",
			focusKey:  "format",
			direction: "right",
			check:     func(t *testing.T, got string) { wantEq(t, got, core.FormatWAV) },
		},
		{
			name:      "bitrate cycles 96 to 112",
			focusKey:  "bitrate",
			direction: "right",
			check:     func(t *testing.T, got string) { wantEq(t, got, "112") },
		},
		{
			name:      "jobs cycles 4 to 5",
			focusKey:  "jobs",
			direction: "right",
			check:     func(t *testing.T, got string) { wantEq(t, got, "5") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(context.Background(), Options{})
			m.screen = "config"
			if tt.setup != nil {
				tt.setup(&m)
			}
			idx := -1
			for i, f := range m.cfgFields {
				if f.key == tt.focusKey {
					idx = i
					break
				}
			}
			if idx == -1 {
				t.Fatalf("missing config field %q", tt.focusKey)
			}
			m.setConfigFocus(idx)
			if !m.cycleConfig(tt.direction) {
				t.Fatalf("cycleConfig(%q) returned false", tt.direction)
			}
			tt.check(t, m.cfgFields[idx].input.Value())
		})
	}

	// Ctrl+U marks pending unset and resets value; cycling clears pending unset.
	m := newModel(context.Background(), Options{})
	m.screen = "config"
	m.applyConfigSnapshot(ConfigSnapshot{
		Stored:   map[string]string{"format": core.FormatWAV, "style": "warm"},
		Defaults: map[string]string{"format": core.FormatMP3},
	})
	m.setConfigFocus(6) // format
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(model)
	if !m.cfgFields[6].pendingUnset || m.cfgFields[6].input.Value() != core.FormatMP3 {
		t.Fatalf("unset did not mark pendingUnset or restore default preview: %+v", m.cfgFields[6])
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.cfgFields[6].pendingUnset {
		t.Fatal("cycling should clear pendingUnset")
	}
}

func TestConfigSaveLoadStaleDropAndGenerateSync(t *testing.T) {
	c := core.Config{Stored: map[string]string{
		"provider": "google", "key_tier": "auto", "voice": "kore", "format": "mp3",
		"bitrate": "96", "jobs": "4", "output_dir": t.TempDir(), "cache_dir": t.TempDir(),
	}}
	initial, err := c.Resolve(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	type saveCall struct {
		set   map[string]string
		unset []string
	}
	savedCh := make(chan saveCall, 1)
	m := newModel(context.Background(), Options{
		Settings: initial,
		SaveConfig: func(_ context.Context, set map[string]string, unset []string) (ConfigSnapshot, error) {
			savedCh <- saveCall{set: set, unset: unset}
			return ConfigSnapshot{
				Path:     "/tmp/config.yaml",
				Stored:   map[string]string{"provider": core.ProviderGemini, "format": core.FormatWAV},
				Env:      map[string]string{},
				Defaults: map[string]string{"provider": core.ProviderGoogle, "format": core.FormatMP3, "bitrate": "96", "voice": "random", "key_tier": core.KeyTierAuto, "output_dir": ".", "jobs": "4"},
				Effective: core.Settings{
					Provider: core.ProviderGemini, Model: core.DefaultGeminiModel,
					Language: "en-US", Voice: "kore", Format: core.FormatWAV,
					Bitrate: 96, KeyTier: core.KeyTierAuto, Jobs: 4,
				},
			}, nil
		},
	})

	// No-op save does not invoke SaveConfig.
	m.screen = "config"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if cmd != nil || !strings.Contains(m.status, "No configuration changes") {
		t.Fatalf("unexpected no-op save: cmd=%v status=%q", cmd, m.status)
	}

	// User customizes voice in Generate tab; saving Config must preserve that manual Generate override,
	// while updating untouched Generate fields (provider, model, format).
	m.fields[3].input.SetValue("puck")
	m.cfgFields[0].input.SetValue(core.ProviderGemini) // provider changed
	m.cfgFields[6].input.SetValue(core.FormatWAV)      // format changed
	m.cfgFields[5].input.SetValue("")                  // style unchanged empty
	m.setConfigFocus(3)                                // language
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU}) // mark language to unset
	m = next.(model)

	// Stale load result should be ignored.
	m.cfgLoadID = 5
	next, _ = m.Update(configLoadedMsg{id: 4, err: errors.New("stale load")})
	m = next.(model)
	if m.cfgError != "" {
		t.Fatal("stale config load error leaked")
	}

	// Trigger save via Enter on Save button.
	m.setConfigFocus(m.cfgSaveFocus())
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !m.cfgSaving {
		t.Fatal("Enter on Save did not start save")
	}
	req := <-savedCh
	if req.set["provider"] != core.ProviderGemini || req.set["format"] != core.FormatWAV || len(req.set) != 2 {
		t.Fatalf("unexpected set payload: %+v", req.set)
	}
	if len(req.unset) != 1 || req.unset[0] != "language" {
		t.Fatalf("unexpected unset payload: %+v", req.unset)
	}

	// Stale save result is ignored.
	next, _ = m.Update(configSavedMsg{id: m.cfgSaveID - 1, err: errors.New("stale save")})
	m = next.(model)
	if !m.cfgSaving || m.cfgError != "" {
		t.Fatal("stale config save result leaked")
	}

	// Deliver save error first to verify draft preservation, then succeed.
	next, _ = m.Update(configSavedMsg{id: m.cfgSaveID, err: errors.New("bad jobs")})
	m = next.(model)
	if m.cfgSaving || !strings.Contains(m.status, "bad jobs") || !m.configDirty() {
		t.Fatalf("save error did not preserve draft: saving=%v status=%q dirty=%v", m.cfgSaving, m.status, m.configDirty())
	}

	m.cfgSaving = true
	next, _ = m.Update(configSavedMsg{
		id: m.cfgSaveID,
		snapshot: ConfigSnapshot{
			Path:     "/tmp/config.yaml",
			Stored:   map[string]string{"provider": core.ProviderGemini, "format": core.FormatWAV},
			Env:      map[string]string{},
			Defaults: map[string]string{"provider": core.ProviderGoogle, "format": core.FormatMP3, "bitrate": "96", "voice": "random", "key_tier": core.KeyTierAuto, "output_dir": ".", "jobs": "4"},
			Effective: core.Settings{
				Provider: core.ProviderGemini, Model: core.DefaultGeminiModel,
				Language: "en-US", Voice: "kore", Format: core.FormatWAV,
				Bitrate: 96, KeyTier: core.KeyTierAuto, Jobs: 4,
			},
		},
	})
	m = next.(model)
	if m.cfgSaving || m.configDirty() || !strings.Contains(m.status, "Saved configuration") {
		t.Fatalf("save completion failed: saving=%v dirty=%v status=%q", m.cfgSaving, m.configDirty(), m.status)
	}
	if m.fields[0].input.Value() != core.ProviderGemini || m.fields[6].input.Value() != core.FormatWAV {
		t.Fatalf("untouched Generate fields not synced: provider=%q format=%q", m.fields[0].input.Value(), m.fields[6].input.Value())
	}
	if m.fields[3].input.Value() != "puck" {
		t.Fatalf("edited Generate voice was overwritten: %q", m.fields[3].input.Value())
	}
	if m.fields[2].input.Value() != m.fields[2].initial {
		t.Fatal("untouched Generate language provenance was broken")
	}
}

func TestConfigLayoutsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		size   tea.WindowSizeMsg
		layout configLayout
		wants  []string
	}{
		{"columns", tea.WindowSizeMsg{Width: 110, Height: 24}, configColumns, []string{"Configuration", "Defaults & Voice", "Audio & Storage", "Save", "Reload", "env:wav"}},
		{"stacked", tea.WindowSizeMsg{Width: 70, Height: 28}, configStacked, []string{"Configuration", "Defaults & Voice", "Audio & Storage", "Save", "Reload"}},
		{"compact", tea.WindowSizeMsg{Width: 50, Height: 18}, configCompact, []string{"Configuration", "Settings", "Save", "Reload"}},
		{"tiny", tea.WindowSizeMsg{Width: 40, Height: 6}, configTiny, []string{"Configuration", "Provider"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), Options{})
			m.applyConfigSnapshot(ConfigSnapshot{
				Path:     "/tmp/safe\x1b[31m/config.yaml",
				Stored:   map[string]string{"provider": core.ProviderGoogle},
				Env:      map[string]string{"format": core.FormatWAV},
				Defaults: map[string]string{"format": core.FormatMP3, "bitrate": "96", "voice": "random", "key_tier": core.KeyTierAuto, "output_dir": ".", "jobs": "4"},
			})
			m.screen = "config"
			next, _ := m.Update(tc.size)
			m = next.(model)
			if got := m.configLayout(); got != tc.layout {
				t.Fatalf("layout = %v, want %v", got, tc.layout)
			}
			raw := m.View()
			if strings.Contains(raw, "\x1b[31m") {
				t.Fatal("unsafe ANSI sequence leaked from config path")
			}
			view := ansi.Strip(raw)
			if lipgloss.Width(raw) > tc.size.Width || lipgloss.Height(raw) > tc.size.Height {
				t.Fatalf("bounds exceeded %+v: got %dx%d", tc.size, lipgloss.Width(raw), lipgloss.Height(raw))
			}
			for _, want := range tc.wants {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in:\n%s", want, view)
				}
			}
		})
	}
}

func TestConfigWorkerJoinsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var workers sync.WaitGroup
	m := newModel(ctx, Options{
		LoadConfig: func(ctx context.Context) (ConfigSnapshot, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return ConfigSnapshot{}, ctx.Err()
		},
	})
	m.workers = &workers
	cmd := m.refreshConfig()
	<-started
	cancel()
	<-cancelled
	joined := make(chan struct{})
	go func() { workers.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("config callback escaped shutdown join")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("config callback did not join")
	}
	if cmd == nil {
		t.Fatal("expected batch cmd from refreshConfig")
	}
}

func TestConfigProviderKeysWorkflow(t *testing.T) {
	type addReq struct {
		name, tier, key string
	}
	type updateReq struct {
		target, name, tier string
	}
	addCh := make(chan addReq, 1)
	updateCh := make(chan updateReq, 1)
	removeCh := make(chan string, 1)

	m := newModel(context.Background(), Options{
		AddKey: func(_ context.Context, name, tier, apiKey string) ([]ProviderKey, error) {
			addCh <- addReq{name: name, tier: tier, key: apiKey}
			return []ProviderKey{
				{Name: "work-paid", Tier: core.KeyTierPaid, MaskedKey: "AIza...1111", Source: "file"},
				{Name: "env", Tier: core.KeyTierFree, MaskedKey: "AIza...2222", Source: "env"},
				{Name: name, Tier: tier, MaskedKey: "AIza...3333", Source: "file"},
			}, nil
		},
		UpdateKey: func(_ context.Context, target, name, tier string) ([]ProviderKey, error) {
			updateCh <- updateReq{target: target, name: name, tier: tier}
			return []ProviderKey{
				{Name: name, Tier: tier, MaskedKey: "AIza...1111", Source: "file"},
				{Name: "env", Tier: core.KeyTierFree, MaskedKey: "AIza...2222", Source: "env"},
			}, nil
		},
		RemoveKey: func(_ context.Context, target string) ([]ProviderKey, error) {
			removeCh <- target
			return []ProviderKey{
				{Name: "env", Tier: core.KeyTierFree, MaskedKey: "AIza...2222", Source: "env"},
			}, nil
		},
	})
	m.screen = "config"
	m.applyConfigSnapshot(ConfigSnapshot{
		Path: "/tmp/config.yaml",
		Keys: []ProviderKey{
			{Name: "personal", Tier: core.KeyTierFree, MaskedKey: "AIza...1111", Source: "file"},
			{Name: "env", Tier: core.KeyTierFree, MaskedKey: "AIza...2222", Source: "env"},
		},
	})

	// Rename first key and toggle tier to paid, then save with Enter.
	m.setConfigFocus(len(m.cfgFields))
	m.cfgKeys[0].nameInput.SetValue("work-paid")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.cfgKeys[0].tier != core.KeyTierPaid || !m.configDirty() {
		t.Fatalf("expected paid tier and dirty key: %+v", m.cfgKeys[0])
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !m.cfgKeyBusy {
		t.Fatal("Enter on dirty key did not start UpdateKey")
	}
	ureq := <-updateCh
	if ureq.target != "personal" || ureq.name != "work-paid" || ureq.tier != core.KeyTierPaid {
		t.Fatalf("unexpected update request: %+v", ureq)
	}
	next, _ = m.Update(keysUpdatedMsg{
		id:     m.cfgKeyOpID,
		status: "Updated key work-paid (paid).",
		keys: []ProviderKey{
			{Name: "work-paid", Tier: core.KeyTierPaid, MaskedKey: "AIza...1111", Source: "file"},
			{Name: "env", Tier: core.KeyTierFree, MaskedKey: "AIza...2222", Source: "env"},
		},
	})
	m = next.(model)
	if m.cfgKeyBusy || m.configDirty() || m.cfgKeys[0].initialName != "work-paid" {
		t.Fatalf("update completion failed: busy=%v dirty=%v key=%+v", m.cfgKeyBusy, m.configDirty(), m.cfgKeys[0])
	}

	// Env key is read-only.
	m.setConfigFocus(len(m.cfgFields) + 1)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.cfgKeys[1].tier != core.KeyTierFree || !strings.Contains(m.status, "read-only") {
		t.Fatalf("env key should remain read-only: tier=%q status=%q", m.cfgKeys[1].tier, m.status)
	}

	// Open "+ Add Gemini key", fill fields, toggle paid, submit, and verify secret is cleared.
	m.setConfigFocus(m.cfgAddButtonFocus())
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if !m.cfgAddingKey {
		t.Fatal("Enter on + Add Gemini key did not open form")
	}
	m.cfgNewKeyName.SetValue("backup")
	m.setConfigFocus(m.cfgAddFormStart() + 1)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(model)
	if m.cfgNewKeyTier != core.KeyTierPaid {
		t.Fatalf("expected new key tier paid, got %q", m.cfgNewKeyTier)
	}
	m.cfgNewKeySecret.SetValue("AIza-new-secret-3333")
	m.setConfigFocus(m.cfgAddSubmitFocus())
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	areq := <-addCh
	if areq.name != "backup" || areq.tier != core.KeyTierPaid || areq.key != "AIza-new-secret-3333" {
		t.Fatalf("unexpected add request: %+v", areq)
	}
	next, _ = m.Update(keysUpdatedMsg{
		id:     m.cfgKeyOpID,
		status: "Saved paid Gemini key.",
		keys: []ProviderKey{
			{Name: "work-paid", Tier: core.KeyTierPaid, MaskedKey: "AIza...1111", Source: "file"},
			{Name: "env", Tier: core.KeyTierFree, MaskedKey: "AIza...2222", Source: "env"},
			{Name: "backup", Tier: core.KeyTierPaid, MaskedKey: "AIza...3333", Source: "file"},
		},
	})
	m = next.(model)
	if m.cfgAddingKey || m.cfgNewKeySecret.Value() != "" || len(m.cfgKeys) != 3 {
		t.Fatalf("add completion failed: adding=%v secret=%q count=%d", m.cfgAddingKey, m.cfgNewKeySecret.Value(), len(m.cfgKeys))
	}

	// Esc cancels add form.
	m.setConfigFocus(m.cfgAddButtonFocus())
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	m.cfgNewKeySecret.SetValue("secret-to-clear")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.cfgAddingKey || m.cfgNewKeySecret.Value() != "" {
		t.Fatal("Esc did not cancel add key form and clear secret")
	}

	// Ctrl+D removes focused file key.
	m.setConfigFocus(len(m.cfgFields))
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(model)
	if got := <-removeCh; got != "work-paid" {
		t.Fatalf("removed = %q, want work-paid", got)
	}
}

func wantEq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
