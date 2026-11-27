package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/smkod/tts-cli/internal/core"
)

func clickAt(x, y int, btn tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: btn, Action: tea.MouseActionPress}
}

func TestMouseSidebarAndWheel(t *testing.T) {
	m := newModel(context.Background(), Options{
		LoadUsage: func(context.Context) (UsageSnapshot, error) {
			return UsageSnapshot{}, nil
		},
		LoadConfig: func(context.Context) (ConfigSnapshot, error) {
			return defaultConfigSnapshot(core.Settings{}), nil
		},
	})
	m.width, m.height = 100, 30
	m.sizeGenerate()
	m.sizeConfig()

	// Click "Usage F2" in sidebar (x=5, y=4).
	next, cmd := m.Update(clickAt(5, 4, tea.MouseButtonLeft))
	m = next.(model)
	if m.screen != "usage" || cmd == nil {
		t.Fatalf("sidebar click did not switch to usage: screen=%q cmd=%v", m.screen, cmd)
	}

	// Wheel down/up on usage screen.
	m.usage = UsageSnapshot{
		Report: core.UsageReport{Providers: map[string]core.CostSummary{"google": {}, "gemini": {}}},
		Configurations: []Configuration{
			{Provider: "gemini", ID: "1", Name: "k1", Tier: "free"},
			{Provider: "gemini", ID: "2", Name: "k2", Tier: "free"},
			{Provider: "gemini", ID: "3", Name: "k3", Tier: "paid"},
		},
	}
	m.height = 12
	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m = next.(model)
	if m.usageOffset != 1 {
		t.Fatalf("wheel down usageOffset = %d, want 1", m.usageOffset)
	}
	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = next.(model)
	if m.usageOffset != 0 {
		t.Fatalf("wheel up usageOffset = %d, want 0", m.usageOffset)
	}

	// Click "Config F3" in sidebar (x=5, y=5).
	next, _ = m.Update(clickAt(5, 5, tea.MouseButtonLeft))
	m = next.(model)
	if m.screen != "config" {
		t.Fatalf("sidebar click did not switch to config: screen=%q", m.screen)
	}
}

func TestMouseGenerateFieldsAndButtons(t *testing.T) {
	dryRun := make(chan bool, 1)
	m := newModel(context.Background(), Options{
		Generate: func(_ context.Context, _ map[string]string, _ string, opts core.Options) (core.Generation, error) {
			dryRun <- opts.DryRun
			return core.Generation{DryRun: opts.DryRun}, nil
		},
	})
	m.width, m.height = 100, 30
	m.sizeGenerate()
	m.text.SetValue("Hello mouse")

	var providerRegion, previewRegion mouseRegion
	for _, r := range m.mouseRegions() {
		if r.action == mouseGenFocus && r.index == 1 {
			providerRegion = r
		}
		if r.action == mouseGenPreview {
			previewRegion = r
		}
	}
	if providerRegion.w == 0 || previewRegion.w == 0 {
		t.Fatalf("missing generate mouse regions: provider=%+v preview=%+v", providerRegion, previewRegion)
	}

	// First click focuses provider field; second click cycles google -> gemini; right-click cycles back.
	next, _ := m.Update(clickAt(providerRegion.x+1, providerRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if m.focus != 1 {
		t.Fatalf("click did not focus provider: focus=%d", m.focus)
	}
	next, _ = m.Update(clickAt(providerRegion.x+1, providerRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if m.fields[0].input.Value() != core.ProviderGemini {
		t.Fatalf("second click did not cycle provider: %q", m.fields[0].input.Value())
	}
	next, _ = m.Update(clickAt(providerRegion.x+1, providerRegion.y, tea.MouseButtonRight))
	m = next.(model)
	if m.fields[0].input.Value() != core.ProviderGoogle {
		t.Fatalf("right click did not cycle provider back: %q", m.fields[0].input.Value())
	}

	// Click Preview button.
	next, cmd := m.Update(clickAt(previewRegion.x+1, previewRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if !m.busy || cmd == nil || !<-dryRun {
		t.Fatal("click on Preview did not trigger dry-run generation")
	}
}

func TestMouseConfigFieldsKeysAndActions(t *testing.T) {
	saved := make(chan map[string]string, 1)
	m := newModel(context.Background(), Options{
		SaveConfig: func(_ context.Context, set map[string]string, _ []string) (ConfigSnapshot, error) {
			saved <- set
			return ConfigSnapshot{Stored: set}, nil
		},
	})
	m.screen = "config"
	m.width, m.height = 110, 30
	m.applyConfigSnapshot(ConfigSnapshot{
		Path:     "/tmp/config.yaml",
		Defaults: map[string]string{"provider": core.ProviderGoogle, "format": core.FormatMP3},
		Keys: []ProviderKey{
			{Name: "personal", Tier: core.KeyTierFree, MaskedKey: "AIza...1111", Source: "file"},
		},
	})

	var formatRegion, tierRegion, addOpenRegion, saveRegion mouseRegion
	for _, r := range m.mouseRegions() {
		switch r.action {
		case mouseCfgFocus:
			if r.index == 6 {
				formatRegion = r
			}
		case mouseCfgKeyTier:
			if r.index == 0 {
				tierRegion = r
			}
		case mouseCfgAddOpen:
			addOpenRegion = r
		case mouseCfgSave:
			saveRegion = r
		}
	}
	if formatRegion.w == 0 || tierRegion.w == 0 || addOpenRegion.w == 0 || saveRegion.w == 0 {
		t.Fatalf("missing config mouse regions: format=%+v tier=%+v add=%+v save=%+v", formatRegion, tierRegion, addOpenRegion, saveRegion)
	}

	// Click format field twice to focus and cycle mp3 -> wav.
	next, _ := m.Update(clickAt(formatRegion.x+1, formatRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	next, _ = m.Update(clickAt(formatRegion.x+1, formatRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if m.cfgFields[6].input.Value() != core.FormatWAV {
		t.Fatalf("format = %q, want wav", m.cfgFields[6].input.Value())
	}

	// Click tier badge to toggle free -> paid.
	next, _ = m.Update(clickAt(tierRegion.x, tierRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if m.cfgKeys[0].tier != core.KeyTierPaid {
		t.Fatalf("key tier = %q, want paid", m.cfgKeys[0].tier)
	}

	// Click "+ Add Gemini key" then click Cancel.
	next, _ = m.Update(clickAt(addOpenRegion.x+1, addOpenRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if !m.cfgAddingKey {
		t.Fatal("click did not open add key form")
	}
	var cancelRegion mouseRegion
	for _, r := range m.mouseRegions() {
		if r.action == mouseCfgAddCancel {
			cancelRegion = r
			break
		}
	}
	next, _ = m.Update(clickAt(cancelRegion.x+1, cancelRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if m.cfgAddingKey {
		t.Fatal("click on Cancel did not close add key form")
	}

	// Reset key tier to avoid UpdateKey requirement and click Save.
	m.cfgKeys[0].tier = core.KeyTierFree
	next, _ = m.Update(clickAt(saveRegion.x+1, saveRegion.y, tea.MouseButtonLeft))
	m = next.(model)
	if !m.cfgSaving {
		t.Fatal("click on Save did not start save")
	}
	got := <-saved
	if got["format"] != core.FormatWAV {
		t.Fatalf("saved format = %q, want wav", got["format"])
	}
}
