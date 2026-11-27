package cli

import (
	"context"
	"io"
	"maps"
	"path/filepath"
	"sync"

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
		configPath, explicit, err := a.path(cmd)
		if err != nil {
			return err
		}
		config, err := core.LoadConfig(configPath, explicit, false)
		if err != nil {
			return err
		}
		settings, err := config.Resolve(nil, nil)
		if err != nil {
			return err
		}
		var configMu sync.RWMutex
		// Provider warnings must never write into the active terminal renderer.
		engine := &core.Engine{Provider: a.backend, Warn: func(string) {}}
		generate := func(
			ctx context.Context, overrides map[string]string, text string, options core.Options,
		) (core.Generation, error) {
			configMu.RLock()
			current := config
			configMu.RUnlock()
			settings, err := current.ResolveInteractive(overrides)
			if err != nil {
				return core.Generation{}, err
			}
			return engine.Generate(ctx, settings, text, options)
		}
		loadConfig := func(ctx context.Context) (tui.ConfigSnapshot, error) {
			if err := ctx.Err(); err != nil {
				return tui.ConfigSnapshot{}, err
			}
			loaded, err := core.LoadConfig(configPath, explicit, false)
			if err != nil {
				return tui.ConfigSnapshot{}, err
			}
			resolved, err := loaded.Resolve(nil, nil)
			if err != nil {
				return tui.ConfigSnapshot{}, err
			}
			configMu.Lock()
			config = loaded
			configMu.Unlock()
			return configSnapshot(loaded, resolved, explicit), nil
		}
		saveConfig := func(ctx context.Context, set map[string]string, unset []string) (tui.ConfigSnapshot, error) {
			if err := ctx.Err(); err != nil {
				return tui.ConfigSnapshot{}, err
			}
			updated, resolved, err := core.EditConfig(configPath, explicit, set, unset)
			if err != nil {
				return tui.ConfigSnapshot{}, err
			}
			configMu.Lock()
			config = updated
			configMu.Unlock()
			return configSnapshot(updated, resolved, explicit), nil
		}
		run := a.runTUI
		if run == nil {
			run = tui.Run
		}
		configDir := filepath.Dir(config.Path)
		options := tui.Options{
			Settings: settings, Generate: generate,
			LoadUsage:  usageLoader(configDir),
			LoadConfig: loadConfig,
			SaveConfig: saveConfig,
			AddKey: func(ctx context.Context, name, tier, apiKey string) ([]tui.ProviderKey, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if _, err := geminitts.AddKey(configDir, name, tier, apiKey); err != nil {
					return nil, err
				}
				return loadProviderKeys(configDir)
			},
			UpdateKey: func(ctx context.Context, targetName, newName, newTier string) ([]tui.ProviderKey, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if _, err := geminitts.UpdateKey(configDir, targetName, newName, newTier); err != nil {
					return nil, err
				}
				return loadProviderKeys(configDir)
			},
			RemoveKey: func(ctx context.Context, targetName string) ([]tui.ProviderKey, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if _, err := geminitts.RemoveKey(configDir, targetName); err != nil {
					return nil, err
				}
				return loadProviderKeys(configDir)
			},
		}
		return run(cmd.Context(), options, a.stdin, a.stdout)
	}
	return command("tui", "Open the interactive Generate terminal interface", nil, exactly(0), runCommand)
}

func loadProviderKeys(configDir string) ([]tui.ProviderKey, error) {
	keys, err := geminitts.LoadKeys(configDir)
	if err != nil {
		return nil, err
	}
	out := make([]tui.ProviderKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, tui.ProviderKey{
			Name:      k.Name,
			Tier:      k.Tier,
			MaskedKey: geminitts.MaskKey(k.Key),
			Source:    k.Source,
		})
	}
	return out, nil
}

func configSnapshot(c core.Config, s core.Settings, explicit bool) tui.ConfigSnapshot {
	stored := map[string]string{}
	if c.Stored != nil {
		stored = maps.Clone(c.Stored)
	}
	keys, _ := loadProviderKeys(filepath.Dir(c.Path))
	return tui.ConfigSnapshot{
		Path:      c.Path,
		Explicit:  explicit,
		Stored:    stored,
		Env:       c.EnvValues(),
		Defaults:  c.DefaultValues(),
		Effective: s,
		Keys:      keys,
	}
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
