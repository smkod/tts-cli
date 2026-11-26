package core

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEstimateUsage(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model string
		usage                 Usage
		date                  string
		want                  float64
		known                 bool
	}{
		{"chirp unicode", ProviderGoogle, "", Usage{Characters: 3}, "2026-11-26", 0.00009, true},
		{"flash", ProviderGemini, DefaultGeminiModel, Usage{InputTokens: int64ptr(100), OutputTokens: int64ptr(250)}, "2026-11-26", 0.0023, true},
		{"lite", ProviderGemini, GeminiFlashLiteTTSModel, Usage{InputTokens: int64ptr(100), OutputTokens: int64ptr(250)}, "2026-11-26", 0.00155, true},
		{"future rates", ProviderGemini, DefaultGeminiModel, Usage{InputTokens: int64ptr(100), OutputTokens: int64ptr(250)}, "2027-01-01", 0.0046, true},
		{"missing metadata", ProviderGemini, DefaultGeminiModel, Usage{}, "2026-11-26", 0, false},
		{"unknown model", ProviderGemini, "other", Usage{InputTokens: int64ptr(1), OutputTokens: int64ptr(1)}, "2026-11-26", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, _ := time.Parse("2006-01-02", tc.date)
			got := EstimateUsage(tc.provider, tc.model, tc.usage, at)
			if (got.EstimatedUSD != nil) != tc.known {
				t.Fatalf("known: %+v", got)
			}
			if tc.known && math.Abs(*got.EstimatedUSD-tc.want) > 1e-10 {
				t.Fatalf("cost %v want %v", *got.EstimatedUSD, tc.want)
			}
		})
	}
}
func int64ptr(n int64) *int64 { return &n }

func TestUsageRecordsConcurrent(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			if err := SaveUsage(dir, EstimateUsage(ProviderGoogle, "", Usage{Characters: 2}, time.Now())); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := ReadUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != 30 || got.Characters != 60 {
		t.Fatalf("%+v", got)
	}
	if err := os.WriteFile(filepath.Join(UsageDir(dir), "broken.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUsage(dir); err == nil {
		t.Fatal("corruption must not silently produce totals")
	}
}

func TestUsageCacheDryRunAndPersistenceFailure(t *testing.T) {
	s := testSettings(t)
	s.ConfigDir = t.TempDir()
	e := &Engine{Provider: fakeProvider(func(context.Context, Request) ([]byte, error) { return frame(1), nil })}
	for i := range 2 {
		g, err := e.Generate(context.Background(), s, "hé 日", Options{ShowCost: true})
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if i == 1 {
			want = 0
		}
		if g.Cost == nil || g.Cost.Requests != want {
			t.Fatalf("%+v", g)
		}
	}
	if _, err := e.Generate(context.Background(), s, "dry", Options{DryRun: true, ShowCost: true}); err != nil {
		t.Fatal(err)
	}
	total, err := ReadUsage(s.ConfigDir)
	if err != nil || total.Requests != 1 || total.Characters != 4 {
		t.Fatalf("%+v %v", total, err)
	}
	if err := os.WriteFile(filepath.Join(s.ConfigDir, "blocked"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.ConfigDir = filepath.Join(s.ConfigDir, "blocked")
	warnings := 0
	e.Warn = func(string) { warnings++ }
	if _, err := e.Generate(context.Background(), s, "no storage", Options{NoCache: true}); err != nil {
		t.Fatal(err)
	}
	if warnings != 1 {
		t.Fatalf("warnings %d", warnings)
	}
}

func TestUsageRecordedBeforeOutputFailure(t *testing.T) {
	s := testSettings(t)
	s.ConfigDir = t.TempDir()
	out := filepath.Join(s.OutputDir, "test.mp3")
	e := &Engine{Provider: fakeProvider(func(_ context.Context, r Request) ([]byte, error) {
		if err := os.Mkdir(out, 0700); err != nil {
			return nil, err
		}
		return frame(1), nil
	})}
	if _, err := e.Generate(context.Background(), s, "hello", Options{Output: out, NoCache: true}); err == nil {
		t.Fatal("expected output error")
	}
	totals, err := ReadUsage(s.ConfigDir)
	if err != nil || totals.Requests != 1 {
		t.Fatalf("%+v %v", totals, err)
	}
}

func TestAttributedUsageRecordedBeforeOutputFailure(t *testing.T) {
	s := testSettings(t)
	s.Provider = ProviderGemini
	s.Model = DefaultGeminiModel
	s.ConfigDir = t.TempDir()
	out := filepath.Join(s.OutputDir, "attributed.mp3")
	e := &Engine{Provider: fakeProvider(func(_ context.Context, r Request) ([]byte, error) {
		r.ReportUsage(Usage{CredentialID: "0123456789abcdef", InputTokens: int64ptr(10), OutputTokens: int64ptr(20)})
		if err := os.Mkdir(out, 0700); err != nil {
			return nil, err
		}
		return frame(1), nil
	})}
	if _, err := e.Generate(context.Background(), s, "hello", Options{Output: out, NoCache: true}); err == nil {
		t.Fatal("expected output error")
	}
	totals, err := ReadUsage(s.ConfigDir)
	if err != nil || totals.Requests != 1 || totals.Configurations[ProviderGemini]["0123456789abcdef"].Requests != 1 {
		t.Fatalf("lost attributed record: %+v %v", totals, err)
	}
}

func TestUsageConfigurationsPreserveLegacyAndUnknownTotals(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	for _, u := range []Usage{
		{Characters: 2},
		{CredentialID: "0123456789abcdef", Characters: 3, InputTokens: int64ptr(100), OutputTokens: int64ptr(250)},
		{CredentialID: "0123456789abcdef", Characters: 4},
		{CredentialID: "fedcba9876543210", Characters: 5, InputTokens: int64ptr(100), OutputTokens: int64ptr(250)},
	} {
		r := EstimateUsage(ProviderGemini, DefaultGeminiModel, u, at)
		if r.CredentialID != u.CredentialID {
			t.Fatal("credential attribution lost")
		}
		if err := SaveUsage(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	configurations := got.Configurations[ProviderGemini]
	if got.Requests != 4 || got.UnknownRequests != 2 || got.Providers[ProviderGemini].Requests != 4 ||
		configurations["unattributed"].Requests != 1 || configurations["0123456789abcdef"].Requests != 2 ||
		configurations["0123456789abcdef"].UnknownRequests != 1 {
		t.Fatalf("wrong grouping: %+v", got)
	}
	var sum float64
	for _, configuration := range configurations {
		sum += configuration.EstimatedUSD
	}
	if math.Abs(sum-got.EstimatedUSD) > 1e-10 {
		t.Fatalf("aggregate mismatch: %v %v", sum, got.EstimatedUSD)
	}
}
