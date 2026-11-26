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

func TestWorkspaceNavigationAndUsageRefresh(t *testing.T) {
	var loads atomic.Int32
	m := newModel(context.Background(), Options{LoadUsage: func(context.Context) (UsageSnapshot, error) {
		loads.Add(1)
		return UsageSnapshot{}, nil
	}})
	m.text.SetValue("Preserved 😀")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = next.(model)
	if m.screen != "usage" || !m.usageLoading || cmd == nil {
		t.Fatal("usage entry did not refresh")
	}
	first := m.usageID
	next, refresh := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = next.(model)
	if m.usageID <= first {
		t.Fatal("refresh ID did not increase")
	}
	next, _ = m.Update(usageMsg{id: first, err: errors.New("stale")})
	m = next.(model)
	if m.usageError != "" {
		t.Fatal("stale load overwrote state")
	}
	next, _ = m.Update(refresh())
	m = next.(model)
	if m.usageLoading || loads.Load() < 1 {
		t.Fatal("load completion not applied")
	}
	next, _ = m.Update(usageMsg{id: m.usageID, err: errors.New("local read failed")})
	m = next.(model)
	if !strings.Contains(m.View(), "local read failed") {
		t.Fatal("usage error not visible")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	m = next.(model)
	if m.screen != "generate" || m.text.Value() != "Preserved 😀" {
		t.Fatal("navigation lost text")
	}
}
func TestPreviewIsDryRun(t *testing.T) {
	opts := make(chan core.Options, 1)
	m := newModel(context.Background(), Options{Generate: func(
		_ context.Context,
		_ map[string]string,
		_ string,
		o core.Options,
	) (core.Generation, error) {
		opts <- o
		return core.Generation{DryRun: o.DryRun}, nil
	}})
	m.text.SetValue("Preview")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = next.(model)
	if !m.busy || !(<-opts).DryRun {
		t.Fatal("preview was not dry run")
	}
}
func TestUsageRowsAndSafeMetadata(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.usage = UsageSnapshot{Report: core.UsageReport{
		CostSummary: core.CostSummary{Requests: 2, UnknownRequests: 1, EstimatedUSD: 0.2},
		Providers:   map[string]core.CostSummary{"google": {}, "gemini": {}},
		Configurations: map[string]map[string]core.CostSummary{
			"gemini": {"removed-id": {},
				"unattributed": {UnknownRequests: 1}},
		},
	}, Configurations: []Configuration{{Provider: "gemini", ID: "safe-id", Name: "Friendly\x1b[31m\nname", Tier: "free"}}}
	rows := ansi.Strip(strings.Join(m.usageLines(), "\n"))
	for _, want := range []string{
		"Unassigned tier: 2",
		"Friendlyname",
		"safe-id",
		"free",
		"removed-id",
		"Legacy / unattributed",
		"Google ADC",
		"not bills",
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(strings.Join(m.usageLines(), "\n"), "\x1b[31m") {
		t.Fatal("credential metadata injected an escape sequence")
	}
}

func TestUsageCostLayout(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		want  []string
	}{
		{"wide table", 110, []string{"KNOWN USD", "UNPRICED", "0.001015     1         1"}},
		{"narrow stacked", 38, []string{"PROVIDERS & CREDENTIALS · USD", "0.001015 · 1 request · 1 unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), Options{})
			m.width, m.height, m.screen = tc.width, 25, "usage"
			cost := core.CostSummary{EstimatedUSD: 0.001015, Requests: 1, UnknownRequests: 1}
			m.usage = UsageSnapshot{
				Report: core.UsageReport{
					CostSummary: core.CostSummary{Requests: 3},
					Providers: map[string]core.CostSummary{
						core.ProviderGemini: cost, core.ProviderGoogle: {EstimatedUSD: 0.04, Requests: 1},
					},
					Configurations: map[string]map[string]core.CostSummary{
						core.ProviderGemini: {"free-id": cost, "unattributed": {EstimatedUSD: 2, Requests: 1}},
					},
				},
				Configurations: []Configuration{{Provider: core.ProviderGemini, ID: "free-id", Tier: core.KeyTierFree}},
			}
			view := ansi.Strip(m.View())
			for _, fragment := range append(tc.want, "PAID ESTIMATE  USD 0.040000",
				"FREE-TIER VALUE  USD 0.001015", "Known subtotal", "Unassigned tier: 1", "Gemini") {
				if !strings.Contains(view, fragment) {
					t.Errorf("missing %q in:\n%s", fragment, view)
				}
			}
		})
	}
}
func TestWorkspaceBoundsAndScrolling(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 100, Height: 40},
		{Width: 76, Height: 24},
		{Width: 35, Height: 16},
		{Width: 12, Height: 6},
		{Width: 1, Height: 1},
	} {
		m := newModel(context.Background(), Options{})
		m.text.SetValue("Visible 😀")
		next, _ := m.Update(size)
		m = next.(model)
		for _, screen := range []string{"generate", "usage"} {
			m.screen = screen
			view := m.View()
			if lipgloss.Width(view) > size.Width || lipgloss.Height(view) > size.Height {
				t.Fatalf("%s exceeds %+v", screen, size)
			}
		}
	}
	m := newModel(context.Background(), Options{})
	m.screen = "usage"
	m.height = 6
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(model)
	if m.usageOffset != 1 {
		t.Fatal("usage did not scroll")
	}
}

func TestUsageCannotSubmitHiddenForm(t *testing.T) {
	for _, keyType := range []tea.KeyType{tea.KeyEnter, tea.KeyCtrlG, tea.KeyCtrlP, tea.KeyTab, tea.KeyShiftTab} {
		t.Run(tea.KeyMsg{Type: keyType}.String(), func(t *testing.T) {
			m := newModel(context.Background(), Options{Generate: func(
				context.Context,
				map[string]string,
				string,
				core.Options,
			) (core.Generation, error) {
				t.Error("hidden form submitted")
				return core.Generation{}, nil
			}})
			m.text.SetValue("Must not submit")
			m.focus = len(m.fields) + 1
			m.screen = "usage"
			next, cmd := m.Update(tea.KeyMsg{Type: keyType})
			m = next.(model)
			if m.busy || cmd != nil || m.focus != len(m.fields)+1 {
				t.Fatal("usage dispatched hidden form action")
			}
		})
	}
}

func TestProviderAwareVoiceCycleAndBoundedScroll(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.fields[0].input.SetValue(core.ProviderGemini)
	m.fields[2].input.SetValue("en-US")
	m.fields[3].input.SetValue("random")
	m.focus = 4
	if !m.cycle("right") || strings.Contains(m.fields[3].input.Value(), "Chirp3") {
		t.Fatal("Gemini received Google full ID")
	}
	m.screen = "usage"
	m.height = 6
	for i := 0; i < 100; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(model)
	}
	if m.usageOffset != m.maxUsageOffset() {
		t.Fatal("unbounded scrolling")
	}
	before := m.usageOffset
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if before > 0 && m.usageOffset != before-1 {
		t.Fatal("scroll back from bottom failed")
	}
}

func TestUsageWorkerJoinsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var workers sync.WaitGroup
	m := newModel(ctx, Options{LoadUsage: func(ctx context.Context) (UsageSnapshot, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return UsageSnapshot{}, ctx.Err()
	}})
	// Run supplies this same tracker to both synthesis and local usage work.
	m.workers = &workers
	cmd := m.refreshUsage()
	<-started
	cancel()
	<-cancelled
	joined := make(chan struct{})
	go func() { workers.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("usage callback escaped shutdown join")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("usage callback did not join")
	}
	if result := cmd().(usageMsg); !errors.Is(result.err, context.Canceled) {
		t.Fatal("cancelled loader result lost")
	}
}
