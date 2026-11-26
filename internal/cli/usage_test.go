package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/smkod/tts-cli/internal/core"
)

func TestCostOutputAndCumulativeUsage(t *testing.T) {
	workspace(t)
	m := &mockBackend{}
	for _, show := range []bool{false, true} {
		args := []string{"generate", "hé 日", "--voice", "Kore", "--format", "wav", "--no-cache", "--json"}
		if show {
			args = append(args, "--show-cost")
		}
		code, out, diag := runTest(m, args...)
		if code != 0 {
			t.Fatalf("%s %s", out, diag)
		}
		var doc struct{ Data map[string]json.RawMessage }
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if _, ok := doc.Data["cost"]; ok != show {
			t.Fatalf("show=%v: %s", show, out)
		}
	}
	code, out, diag := runTest(m, "usage", "--json")
	if code != 0 || !strings.Contains(out, `"requests":2`) || !strings.Contains(out, `"characters":8`) {
		t.Fatalf("%s %s", out, diag)
	}
}

func TestUsageIncludesProviderBreakdown(t *testing.T) {
	dir := workspace(t)
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	input, output := int64(100), int64(250)
	for _, record := range []core.CostRecord{
		core.EstimateUsage(core.ProviderGoogle, "", core.Usage{Characters: 3}, at),
		core.EstimateUsage(core.ProviderGemini, core.DefaultGeminiModel, core.Usage{
			CredentialID: "0123456789abcdef", InputTokens: &input, OutputTokens: &output,
		}, at),
		core.EstimateUsage(core.ProviderGemini, core.DefaultGeminiModel, core.Usage{}, at),
	} {
		if err := core.SaveUsage(dir, record); err != nil {
			t.Fatal(err)
		}
	}
	code, out, diag := runTest(&mockBackend{}, "usage", "--json")
	if code != 0 {
		t.Fatalf("%s %s", out, diag)
	}
	var doc struct {
		Data struct {
			Requests       int                                    `json:"requests"`
			Unknown        int                                    `json:"unknown_requests"`
			Providers      map[string]core.CostSummary            `json:"providers"`
			Configurations map[string]map[string]core.CostSummary `json:"configurations"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Data.Requests != 3 || doc.Data.Unknown != 1 || doc.Data.Providers[core.ProviderGoogle].Requests != 1 || doc.Data.Providers[core.ProviderGemini].Requests != 2 || doc.Data.Providers[core.ProviderGemini].UnknownRequests != 1 {
		t.Fatalf("unexpected report: %s", out)
	}
	if doc.Data.Configurations[core.ProviderGemini]["0123456789abcdef"].Requests != 1 ||
		doc.Data.Configurations[core.ProviderGemini]["unattributed"].UnknownRequests != 1 {
		t.Fatalf("missing configuration breakdown: %s", out)
	}
	code, out, diag = runTest(&mockBackend{}, "usage")
	if code != 0 || !strings.Contains(out, "Total: 3") || !strings.Contains(out, "gemini: 2") || !strings.Contains(out, "google: 1") || !strings.Contains(out, "1 request(s) unknown") {
		t.Fatalf("%s %s", out, diag)
	}
	if !strings.Contains(out, "configuration 0123456789abcdef") || !strings.Contains(out, "not a bill") {
		t.Fatalf("missing configuration or caveat: %s", out)
	}
}
