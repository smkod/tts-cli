package cli

import (
	_ "embed"

	"github.com/spf13/cobra"
)

//go:embed agent-guide.md
var agentGuideText string

func (a *app) agentGuide() *cobra.Command {
	return command("agent-guide", "Show self-contained instructions for AI agents", nil, exactly(0),
		func(_ *cobra.Command, _ []string) error {
			a.result = map[string]any{"guide_version": 1, "format": "markdown", "instructions": agentGuideText}
			a.human = agentGuideText
			return nil
		})
}
