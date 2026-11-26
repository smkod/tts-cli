package cli

import (
	"context"
	"io"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/smkod/tts-cli/internal/core"
	"github.com/smkod/tts-cli/internal/geminitts"
	"github.com/smkod/tts-cli/internal/tui"
	"github.com/spf13/cobra"
)

type tuiRunner func(context.Context, tui.Options, io.Reader, io.Writer) error

func terminalStream(stream any) bool {
	file, ok := stream.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(file.Fd())
}

func (a *app) tui() *cobra.Command {
	runCommand := func(cmd *cobra.Command, _ []string) error {
		if a.json {
			return core.Fail("INVALID_USAGE",
				"The tui command does not support --json. Use generate --json for automation.", false)
		}
		isTerminal := a.terminal
		if isTerminal == nil {
			isTerminal = terminalStream
		}
		if !isTerminal(a.stdin) || !isTerminal(a.stdout) {
			return core.Fail("INVALID_USAGE",
				"The tui command requires interactive stdin and stdout terminals. "+
					"Open a terminal or use generate for scripts and redirected output.", false)
		}
		config, err := a.load(cmd, false)
		if err != nil {
			return err
		}
		settings, err := config.Resolve(nil, nil)
		if err != nil {
			return err
		}
		// Provider warnings must never write into the active terminal renderer.
		engine := &core.Engine{Provider: a.backend, Warn: func(string) {}}
		generate := func(
			ctx context.Context, overrides map[string]string, text string, options core.Options,
		) (core.Generation, error) {
			settings, err := config.ResolveInteractive(overrides)
			if err != nil {
				return core.Generation{}, err
			}
			return engine.Generate(ctx, settings, text, options)
		}
		run := a.runTUI
		if run == nil {
			run = tui.Run
		}
		options := tui.Options{
			Settings: settings, Generate: generate,
			LoadUsage: usageLoader(filepath.Dir(config.Path)),
		}
		return run(cmd.Context(), options, a.stdin, a.stdout)
	}
	return command("tui", "Open the interactive Generate terminal interface", nil, exactly(0), runCommand)
}

func usageLoader(configDir string) func(context.Context) (tui.UsageSnapshot, error) {
	return func(ctx context.Context) (tui.UsageSnapshot, error) {
		if err := ctx.Err(); err != nil {
			return tui.UsageSnapshot{}, err
		}
		report, err := core.ReadUsage(configDir)
		if err != nil {
			return tui.UsageSnapshot{}, core.Fail("FILE_IO", "Cannot read local usage records.", false)
		}
		keys, err := geminitts.LoadKeys(configDir)
		if err != nil {
			return tui.UsageSnapshot{}, core.Fail("FILE_IO", "Cannot read configured Gemini keys.", false)
		}
		configurations := []tui.Configuration{{
			Provider: core.ProviderGoogle, ID: "unattributed",
			Name: "Default credentials (identity unknown)",
		}}
		for _, key := range keys {
			configurations = append(configurations, tui.Configuration{
				Provider: core.ProviderGemini, ID: geminitts.KeyID(key.Key),
				Name: key.Name, Tier: key.Tier,
			})
		}
		if err := ctx.Err(); err != nil {
			return tui.UsageSnapshot{}, err
		}
		return tui.UsageSnapshot{Report: report, Configurations: configurations}, nil
	}
}
