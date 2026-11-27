package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/smkod/tts-cli/internal/geminitts"
	"github.com/spf13/cobra"
)

func (a *app) keys() *cobra.Command {
	parent := &cobra.Command{
		Use:     "keys",
		Aliases: []string{"k"},
		Short:   "Manage Gemini API keys and free/paid tiers",
	}

	var tier, name string
	add := command("add <api-key>", "Save a Gemini API key under the free or paid tier", []string{"a"}, exactly(1), func(cmd *cobra.Command, args []string) error {
		path, _, err := a.path(cmd)
		if err != nil {
			return err
		}
		configDir := filepath.Dir(path)
		entry, err := geminitts.AddKey(configDir, name, tier, args[0])
		if err != nil {
			return err
		}
		masked := geminitts.MaskKey(entry.Key)
		a.result = map[string]any{
			"name":       entry.Name,
			"tier":       entry.Tier,
			"masked_key": masked,
			"file":       geminitts.KeysPath(configDir),
		}
		a.human = fmt.Sprintf("Saved Gemini %s key %s (%s).", entry.Tier, entry.Name, masked)
		return nil
	})
	add.Flags().StringVar(&tier, "tier", geminitts.TierFree, "Key tier (free or paid)")
	add.Flags().StringVar(&name, "name", "", "Optional label for this key")

	list := command("list", "List configured Gemini API keys and cooldown status", []string{"ls"}, exactly(0), func(cmd *cobra.Command, _ []string) error {
		configDir, cacheDir := a.keyDirs(cmd)
		keys, err := geminitts.LoadKeys(configDir)
		if err != nil {
			return err
		}
		pool := geminitts.NewKeyPool()
		statuses := pool.Statuses(keys, geminitts.StatePath(configDir, cacheDir))
		a.result = map[string]any{"keys": statuses, "count": len(statuses)}
		if len(statuses) == 0 {
			a.human = "No Gemini API keys configured. Run ttsi keys add --tier free <KEY> or set GEMINI_API_KEY."
			return nil
		}
		var lines []string
		for i, st := range statuses {
			statusText := "available"
			if !st.Available && st.CooldownUntil != nil {
				rem := time.Until(*st.CooldownUntil).Round(time.Second)
				if rem < 0 {
					rem = 0
				}
				statusText = fmt.Sprintf("cooldown (%s remaining)", rem)
			}
			lines = append(lines, fmt.Sprintf("%d. %s\t%s\t%s\t%s\t%s", i+1, st.Name, st.Tier, st.MaskedKey, st.Source, statusText))
		}
		a.human = strings.Join(lines, "\n")
		return nil
	})

	remove := command("remove <name-or-index>", "Remove a saved Gemini API key", []string{"rm"}, exactly(1), func(cmd *cobra.Command, args []string) error {
		configDir, _ := a.keyDirs(cmd)
		removed, err := geminitts.RemoveKey(configDir, args[0])
		if err != nil {
			return err
		}
		masked := geminitts.MaskKey(removed.Key)
		a.result = map[string]any{"removed": removed.Name, "tier": removed.Tier, "masked_key": masked}
		a.human = fmt.Sprintf("Removed Gemini %s key %s (%s).", removed.Tier, removed.Name, masked)
		return nil
	})

	reset := command("reset", "Clear persisted key cooldowns so all keys are retried", nil, exactly(0), func(cmd *cobra.Command, _ []string) error {
		configDir, cacheDir := a.keyDirs(cmd)
		pool := geminitts.NewKeyPool()
		if err := pool.ResetCooldowns(geminitts.StatePath(configDir, cacheDir)); err != nil {
			return core.Fail("FILE_IO", "Cannot reset Gemini key cooldown state.", false)
		}
		a.result = map[string]any{"reset": true}
		a.human = "Cleared Gemini key cooldown state."
		return nil
	})

	var updateName, updateTier string
	update := command("update <name-or-index>", "Rename or change the tier of a saved Gemini API key", []string{"up"}, exactly(1), func(cmd *cobra.Command, args []string) error {
		configDir, _ := a.keyDirs(cmd)
		updated, err := geminitts.UpdateKey(configDir, args[0], updateName, updateTier)
		if err != nil {
			return err
		}
		masked := geminitts.MaskKey(updated.Key)
		a.result = map[string]any{"name": updated.Name, "tier": updated.Tier, "masked_key": masked}
		a.human = fmt.Sprintf("Updated Gemini %s key %s (%s).", updated.Tier, updated.Name, masked)
		return nil
	})
	update.Flags().StringVar(&updateName, "name", "", "New label for this key")
	update.Flags().StringVar(&updateTier, "tier", "", "New key tier (free or paid)")

	parent.AddCommand(add, list, update, remove, reset)
	return parent
}

func (a *app) keyDirs(cmd *cobra.Command) (string, string) {
	path, _, err := a.path(cmd)
	if err != nil {
		return "", ""
	}
	configDir := filepath.Dir(path)
	c, err := core.LoadConfig(path, false, false)
	if err != nil {
		return configDir, ""
	}
	s, err := c.Resolve(nil, nil)
	if err != nil {
		return configDir, ""
	}
	return configDir, s.CacheDir
}
