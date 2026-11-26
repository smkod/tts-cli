package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/spf13/cobra"
)

func (a *app) usage() *cobra.Command {
	return command("usage", "Show locally tracked cumulative synthesis usage and estimated cost", nil, exactly(0), func(cmd *cobra.Command, _ []string) error {
		path, _, err := a.path(cmd)
		if err != nil {
			return err
		}
		totals, err := core.ReadUsage(filepath.Dir(path))
		if err != nil {
			return core.Fail("FILE_IO", "Cannot read usage totals: "+err.Error(), false)
		}
		a.result = totals
		format := func(label string, s core.CostSummary) string {
			return fmt.Sprintf("%s: %d provider request(s), %d characters, %d input tokens, %d output tokens\n  %s", label, s.Requests, s.Characters, s.InputTokens, s.OutputTokens, s.String())
		}
		lines := []string{format("Total", totals.CostSummary)}
		providers := make([]string, 0, len(totals.Providers))
		for provider := range totals.Providers {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		for _, provider := range providers {
			lines = append(lines, format(provider, totals.Providers[provider]))
			ids := make([]string, 0, len(totals.Configurations[provider]))
			for id := range totals.Configurations[provider] {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				lines = append(lines, format("  configuration "+id, totals.Configurations[provider][id]))
			}
		}
		lines = append(lines, totals.Basis)
		lines = append(lines, "Local estimates, not a bill; tracking may be incomplete. "+
			"Historical and Google default-credential usage is unattributed.")
		a.human = strings.Join(lines, "\n")
		return nil
	})
}
