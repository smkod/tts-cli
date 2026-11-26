package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Usage contains metering only, never input text, audio, or credentials.
type Usage struct {
	CredentialID string `json:"-"`
	Characters   int    `json:"characters"`
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
}

type CostRecord struct {
	CredentialID  string    `json:"credential_id,omitempty"`
	Version       int       `json:"version"`
	At            time.Time `json:"at"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model,omitempty"`
	Usage         Usage     `json:"usage"`
	EstimatedUSD  *float64  `json:"estimated_usd"`
	PricingDate   string    `json:"pricing_date"`
	PricingSource string    `json:"pricing_source"`
}

// EstimateUsage applies gross standard paid-list prices, not account-specific billing.
func EstimateUsage(provider, model string, u Usage, at time.Time) CostRecord {
	r := CostRecord{
		Version: 1, At: at.UTC(), Provider: provider, Model: model, Usage: u,
		CredentialID: u.CredentialID, PricingDate: "2026-11-26",
	}
	var cost float64
	switch provider {
	case ProviderGoogle:
		r.PricingSource = "https://cloud.google.com/text-to-speech/pricing"
		if u.Characters < 0 {
			return r
		}
		cost = float64(u.Characters) * 30 / 1e6
	case ProviderGemini:
		r.PricingSource = "https://ai.google.dev/gemini-api/docs/pricing"
		if u.InputTokens == nil || u.OutputTokens == nil || *u.InputTokens < 0 || *u.OutputTokens < 0 {
			return r
		}
		input, output := 0.5, 9.0
		switch model {
		case DefaultGeminiModel:
		case GeminiFlashLiteTTSModel:
			output = 6
		default:
			return r
		}
		if !at.Before(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
			input *= 2
			output *= 2
		}
		cost = (float64(*u.InputTokens)*input + float64(*u.OutputTokens)*output) / 1e6
	default:
		return r
	}
	r.EstimatedUSD = &cost
	return r
}

type CostSummary struct {
	Requests        int     `json:"requests"`
	Characters      int     `json:"characters"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	EstimatedUSD    float64 `json:"estimated_usd"`
	UnknownRequests int     `json:"unknown_requests"`
	Currency        string  `json:"currency"`
	Basis           string  `json:"basis"`
}

func NewCostSummary() CostSummary {
	return CostSummary{Currency: "USD", Basis: "gross paid-list estimate; excludes free tiers, credits, discounts and taxes"}
}
func (s *CostSummary) Add(r CostRecord) {
	s.Requests++
	s.Characters += r.Usage.Characters
	if r.Usage.InputTokens != nil {
		s.InputTokens += *r.Usage.InputTokens
	}
	if r.Usage.OutputTokens != nil {
		s.OutputTokens += *r.Usage.OutputTokens
	}
	if r.EstimatedUSD == nil {
		s.UnknownRequests++
	} else {
		s.EstimatedUSD += *r.EstimatedUSD
	}
}
func (s CostSummary) String() string {
	if s.UnknownRequests > 0 {
		return fmt.Sprintf("Estimated cost: USD %.6f known subtotal (%d request(s) unknown)", s.EstimatedUSD, s.UnknownRequests)
	}
	return fmt.Sprintf("Estimated cost: USD %.6f (gross paid-list)", s.EstimatedUSD)
}
func UsageDir(configDir string) string { return filepath.Join(configDir, "usage", "v1") }

// SaveUsage publishes one immutable record atomically. Independent files avoid
// read-modify-write races across processes without a lock or a mutable total.
func SaveUsage(configDir string, record CostRecord) (resultErr error) {
	if configDir == "" {
		return errors.New("usage configuration directory is unavailable")
	}
	dir := UsageDir(configDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create usage directory: %w", err)
	}
	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode usage record: %w", err)
	}
	f, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return fmt.Errorf("create usage record: %w", err)
	}
	name := f.Name()
	defer func() {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove pending usage record: %w", err))
		}
	}()
	if _, err := f.Write(b); err != nil {
		return errors.Join(fmt.Errorf("write usage record: %w", err), f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync usage record: %w", err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close usage record: %w", err)
	}
	if err := os.Rename(name, name+".json"); err != nil {
		return fmt.Errorf("publish usage record: %w", err)
	}
	return nil
}

type UsageReport struct {
	CostSummary
	Providers      map[string]CostSummary            `json:"providers"`
	Configurations map[string]map[string]CostSummary `json:"configurations"`
}

func ReadUsage(configDir string) (UsageReport, error) {
	total := UsageReport{
		CostSummary: NewCostSummary(), Providers: make(map[string]CostSummary),
		Configurations: make(map[string]map[string]CostSummary),
	}
	dir := UsageDir(configDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return total, nil
	}
	if err != nil {
		return total, fmt.Errorf("read usage directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return total, fmt.Errorf("read usage record: %w", err)
		}
		var r CostRecord
		if err := json.Unmarshal(b, &r); err != nil {
			return total, fmt.Errorf("corrupt usage record %s: %w", entry.Name(), err)
		}
		if r.Version != 1 || r.At.IsZero() || r.Provider == "" || r.Usage.Characters < 0 || (r.Usage.InputTokens != nil && *r.Usage.InputTokens < 0) || (r.Usage.OutputTokens != nil && *r.Usage.OutputTokens < 0) || (r.EstimatedUSD != nil && *r.EstimatedUSD < 0) {
			return total, fmt.Errorf("invalid usage record %s", entry.Name())
		}
		total.Add(r)
		provider, ok := total.Providers[r.Provider]
		if !ok {
			provider = NewCostSummary()
		}
		provider.Add(r)
		total.Providers[r.Provider] = provider
		if total.Configurations[r.Provider] == nil {
			total.Configurations[r.Provider] = make(map[string]CostSummary)
		}
		id := r.CredentialID
		if id == "" {
			id = "unattributed"
		}
		configuration, ok := total.Configurations[r.Provider][id]
		if !ok {
			configuration = NewCostSummary()
		}
		configuration.Add(r)
		total.Configurations[r.Provider][id] = configuration
	}
	return total, nil
}
