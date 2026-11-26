package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/smkod/tts-cli/internal/geminitts"
	"github.com/spf13/cobra"
)

type response struct {
	Version int           `json:"schema_version"`
	Success bool          `json:"success"`
	Data    any           `json:"data,omitempty"`
	Error   *core.Problem `json:"error,omitempty"`
}

type app struct {
	backend          core.Backend
	stdout, stderr   io.Writer
	json, alwaysJSON bool
	configFlag       string
	result           any
	human            string
	code             int
	errMu            sync.Mutex
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer, backend core.Backend) int {
	a := &app{backend: backend, stdout: stdout, stderr: stderr}
	defer backend.Close()
	root := a.root()
	args, defaultJSON := route(root, args)
	a.alwaysJSON = defaultJSON
	earlyJSON := wantsJSON(args)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	_, err := root.ExecuteContextC(ctx)
	if err == nil && a.result == nil {
		return 0
	} // Help and completion are not data commands.
	var p *core.Problem
	if err != nil {
		p = core.Public(err)
		if p.Code == "INTERNAL" {
			p = core.Fail("INVALID_USAGE", "Invalid command, arguments or flags. See ttsi --help.", false)
		}
		if a.code == 0 {
			a.code = exitFor(p)
		}
	}
	if a.json || a.alwaysJSON || (err != nil && earlyJSON) {
		if encodeErr := json.NewEncoder(stdout).Encode(response{1, err == nil && a.code == 0, a.result, p}); encodeErr != nil {
			fmt.Fprintln(stderr, "ttsi: Cannot write JSON output.")
			return 1
		}
	} else if a.human != "" {
		if _, writeErr := fmt.Fprintln(stdout, a.human); writeErr != nil {
			return 1
		}
	}
	if a.alwaysJSON && !a.json && a.human != "" {
		a.warn(a.human)
	}
	if p != nil && !a.json && !a.alwaysJSON && !earlyJSON {
		a.warn(p.Message)
	}
	return a.code
}

func exitFor(p *core.Problem) int {
	if p == nil {
		return 0
	}
	switch p.Code {
	case "INVALID_USAGE", "INVALID_CONFIG", "INVALID_INPUT", "INVALID_TEXT", "INVALID_VOICE", "INVALID_BATCH":
		return 2
	default:
		return 1
	}
}
func (a *app) warn(msg string) {
	a.errMu.Lock()
	defer a.errMu.Unlock()
	fmt.Fprintln(a.stderr, "ttsi: "+msg)
}

func wantsJSON(args []string) bool {
	result := false
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			break
		}
		if v == "--json" || v == "--json=true" || v == "--json=1" || v == "--json=t" {
			result = true
		}
		if v == "--json=false" || v == "--json=0" || v == "--json=f" {
			result = false
		}
		if v == "--config" {
			i++
		}
	}
	return result
}

func route(root *cobra.Command, args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if tok == "--config" {
			i++
			continue
		}
		if tok == "--json" || strings.HasPrefix(tok, "--json=") || strings.HasPrefix(tok, "--config=") {
			continue
		}
		if tok == "" || tok == "--" || !strings.HasPrefix(tok, "-") {
			if tok == "help" || tok == "completion" || tok == "__complete" || tok == "__completeNoDesc" {
				return args, false
			}
			for _, cmd := range root.Commands() {
				if cmd.Name() == tok || cmd.HasAlias(tok) {
					return args, cmd.Name() == "batch"
				}
			}
			return append([]string{"generate"}, args...), false
		}
		return args, false
	}
	return args, false
}

func command(use, short string, aliases []string, args cobra.PositionalArgs, run func(*cobra.Command, []string) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Aliases: aliases, Args: args, RunE: run}
}
func exactly(n int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return core.Fail("INVALID_USAGE", fmt.Sprintf("Expected %d positional argument(s). See command help.", n), false)
		}
		return nil
	}
}
func requiresArgs(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return core.Fail("INVALID_USAGE", "Provide at least one setting.", false)
	}
	return nil
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{Use: "ttsi", Short: "Generate Chirp 3 HD speech for people and scripts.",
		SilenceUsage: true, SilenceErrors: true, DisableSuggestions: true,
		Example: "  ttsi \"Hello there\"\n  ttsi g \"Come stai?\" --lang it-IT --json\n  ttsi voices list --lang it-IT",
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "Emit versioned JSON")
	root.PersistentFlags().StringVar(&a.configFlag, "config", "", "Select a YAML configuration file")
	root.AddCommand(a.generate(), a.batch(), a.voices(), a.modifiers(), a.providers(), a.config(), a.capabilities(), a.doctor(), a.login(), a.keys(), a.agentGuide(), a.usage())
	return root
}

func (a *app) load(cmd *cobra.Command, create bool) (core.Config, error) {
	path, explicit, err := a.path(cmd)
	if err != nil {
		return core.Config{}, err
	}
	return core.LoadConfig(path, explicit, create)
}
func (a *app) path(cmd *cobra.Command) (string, bool, error) {
	if cmd.Flags().Changed("config") && a.configFlag == "" {
		return "", true, core.Fail("INVALID_CONFIG", "Config path must not be empty.", false)
	}
	path, explicit, err := core.ConfigPath(a.configFlag)
	if err != nil && core.Public(err).Code == "INTERNAL" {
		err = core.Fail("INVALID_CONFIG", "Cannot determine the configuration path.", false)
	}
	return path, explicit, err
}
func (a *app) settings(cmd *cobra.Command, flags map[string]string) (core.Settings, error) {
	c, e := a.load(cmd, false)
	if e != nil {
		return core.Settings{}, e
	}
	return c.Resolve(flags, nil)
}

type synthFlags struct {
	lang, voice, provider, model, keyTier, style, script, cache, out, outDir, format string
	noCache, dryRun, showCost                                                        bool
	jobs                                                                             int
	bitrate                                                                          int
}

func bindSynth(cmd *cobra.Command, f *synthFlags, batch bool) {
	flags := cmd.Flags()
	flags.StringVar(&f.lang, "lang", "", "Select a voice language (or auto for Gemini 3.8 TTS)")
	flags.StringVar(&f.voice, "voice", "", "Select a voice alias, full Chirp 3 HD ID, or random (default if not configured)")
	flags.StringVar(&f.provider, "provider", "", "Select a synthesis provider (google or gemini)")
	flags.StringVar(&f.model, "model", "", "Select a Gemini 3.8 TTS model (gemini-3.8-flash-tts or gemini-3.8-flash-lite-tts)")
	flags.StringVar(&f.keyTier, "key-tier", "", "Select Gemini key tier (auto, free, or paid)")
	flags.StringVar(&f.style, "style", "", "Set sustained delivery style for Gemini 3.8 TTS (e.g. \"whispered, nervous\")")
	flags.IntVar(&f.bitrate, "bitrate", 96, "Set MP3 bitrate in kbps (8-320; default 96)")
	flags.StringVar(&f.format, "format", "", "Select the output format (wav or mp3)")
	flags.StringVar(&f.cache, "cache-dir", "", "Override the cache directory")
	flags.BoolVar(&f.showCost, "show-cost", false, "Show gross estimated usage cost after generation")
	flags.BoolVar(&f.noCache, "no-cache", false, "Skip cache reads and writes")
	flags.BoolVar(&f.dryRun, "dry-run", false, "Inspect the request without network or file writes")
	if batch {
		flags.StringVar(&f.outDir, "output-dir", "", "Override the output directory")
		flags.IntVar(&f.jobs, "jobs", 4, "Set batch concurrency (1–16)")
	} else {
		flags.StringVarP(&f.out, "output", "o", "", "Write to this audio file path")
		flags.StringVar(&f.script, "script", "", "Read multi-turn or multi-speaker dialogue from a JSON script file")
	}
}
func (f *synthFlags) values(cmd *cobra.Command) map[string]string {
	out := map[string]string{}
	values := [][3]string{
		{"bitrate", "bitrate", fmt.Sprint(f.bitrate)}, {"format", "format", f.format},
		{"lang", "language", f.lang}, {"voice", "voice", f.voice}, {"provider", "provider", f.provider},
		{"model", "model", f.model}, {"key-tier", "key_tier", f.keyTier}, {"style", "style", f.style},
		{"cache-dir", "cache_dir", f.cache}, {"output-dir", "output_dir", f.outDir}, {"jobs", "jobs", fmt.Sprint(f.jobs)},
	}
	for _, v := range values {
		if cmd.Flags().Changed(v[0]) {
			out[v[1]] = v[2]
		}
	}
	return out
}

func (a *app) generate() *cobra.Command {
	f := &synthFlags{}
	cmd := command("generate [<text>]", "Generate one audio file", []string{"g"}, cobra.RangeArgs(0, 1), func(cmd *cobra.Command, args []string) error {
		hasScript := cmd.Flags().Changed("script")
		if hasScript {
			if strings.TrimSpace(f.script) == "" {
				return core.Fail("INVALID_INPUT", "Script path must not be blank.", false)
			}
			if len(args) > 0 {
				return core.Fail("INVALID_USAGE", "Cannot combine positional text with --script.", false)
			}
		} else {
			if len(args) != 1 {
				return core.Fail("INVALID_USAGE", "Expected 1 positional argument(s). See command help.", false)
			}
			if e := core.ValidateText(args[0]); e != nil {
				return e
			}
		}
		if cmd.Flags().Changed("output") && strings.TrimSpace(f.out) == "" {
			return core.Fail("INVALID_INPUT", "Output path must not be blank.", false)
		}
		var spec scriptSpec
		if hasScript {
			var err error
			spec, err = loadScript(f.script)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("voice") && len(spec.Speakers) > 0 {
				return core.Fail("INVALID_USAGE", "Cannot combine --voice with a multi-speaker script; assign voices inside speakers.", false)
			}
		}
		s, e := a.settings(cmd, f.values(cmd))
		if e != nil {
			return e
		}
		eng := &core.Engine{Provider: a.backend, Warn: a.warn}
		var text string
		if len(args) == 1 {
			text = args[0]
		}
		g, e := eng.Generate(cmd.Context(), s, text, core.Options{
			ShowCost: f.showCost,
			Output:   f.out,
			DryRun:   f.dryRun,
			NoCache:  f.noCache,
			Turns:    spec.Turns,
			Speakers: spec.Speakers,
		})
		if e != nil {
			return e
		}
		a.result, a.human = g, core.FormatFile(g)
		if g.Cost != nil && !a.json {
			a.warn(g.Cost.String())
		}
		return nil
	})
	bindSynth(cmd, f, false)
	return cmd
}

func (a *app) voices() *cobra.Command {
	parent := &cobra.Command{Use: "voices", Short: "Explore and design Chirp 3 HD and Gemini 3.8 TTS voices", Aliases: []string{"v"}}
	var lang, provider string
	var online bool
	list := command("list", "List compatible voices", []string{"ls"}, exactly(0), func(cmd *cobra.Command, _ []string) error {
		voices := core.LocalVoices(lang)
		payload := map[string]any{"source": "local", "catalog_date": core.CatalogDate}
		if online {
			var err error
			if provider != "" {
				if pvl, ok := a.backend.(ProviderVoiceLister); ok {
					voices, err = pvl.ListVoicesForProvider(cmd.Context(), provider, lang)
				} else {
					voices, err = a.backend.ListVoices(cmd.Context(), lang)
				}
			} else {
				voices, err = a.backend.ListVoices(cmd.Context(), lang)
			}
			if err != nil {
				a.code = 1
				return err
			}
			src := core.ProviderGoogle
			if provider != "" {
				src = provider
			}
			payload = map[string]any{"source": src}
		}
		payload["voices"] = voices
		a.result = payload
		var out strings.Builder
		fmt.Fprintf(&out, "%d voices (%s)", len(voices), payload["source"])
		for _, v := range voices {
			fmt.Fprintf(&out, "\n%s\t%s", v.Name, v.Alias)
		}
		a.human = out.String()
		return nil
	})
	list.Flags().StringVar(&lang, "lang", "", "Filter by exact locale")
	list.Flags().StringVar(&provider, "provider", "", "Select provider when querying online (google or gemini)")
	list.Flags().BoolVar(&online, "online", false, "Query the provider API rather than the bundled catalog")

	var dName, dLang, dGender, dModel, dKeyTier, dOut, dFormat string
	var dBitrate int
	var dDryRun bool
	design := command("design <description>", "Design a custom Gemini 3.8 TTS voice from a text description", nil, exactly(1), func(cmd *cobra.Command, args []string) error {
		prompt := strings.TrimSpace(args[0])
		if err := core.ValidateText(prompt); err != nil {
			return err
		}
		if cmd.Flags().Changed("name") && strings.TrimSpace(dName) == "" {
			return core.Fail("INVALID_INPUT", "Display name must not be blank.", false)
		}
		var gender string
		if cmd.Flags().Changed("gender") {
			g := strings.ToLower(strings.TrimSpace(dGender))
			if g != "male" && g != "female" && g != "neutral" {
				return core.Fail("INVALID_INPUT", "Gender must be male, female, or neutral.", false)
			}
			gender = g
		}
		var langCode string
		if cmd.Flags().Changed("lang") {
			norm, ok := core.ValidLocale(dLang)
			if !ok {
				return core.Fail("INVALID_INPUT", "Language must be a valid BCP-47 locale (e.g. en-US).", false)
			}
			langCode = norm
		}
		if cmd.Flags().Changed("output") && strings.TrimSpace(dOut) == "" {
			return core.Fail("INVALID_INPUT", "Output path must not be blank.", false)
		}
		overrides := map[string]string{"provider": core.ProviderGemini}
		if cmd.Flags().Changed("model") {
			overrides["model"] = dModel
		}
		if cmd.Flags().Changed("key-tier") {
			overrides["key_tier"] = dKeyTier
		}
		if cmd.Flags().Changed("format") {
			overrides["format"] = dFormat
		}
		if cmd.Flags().Changed("bitrate") {
			overrides["bitrate"] = fmt.Sprint(dBitrate)
		}
		s, err := a.settings(cmd, overrides)
		if err != nil {
			return err
		}
		var outPath string
		if dOut != "" {
			ext := "." + s.Format
			absOut, absErr := filepath.Abs(dOut)
			if absErr != nil || !strings.EqualFold(filepath.Ext(absOut), ext) {
				return core.Fail("INVALID_INPUT", "Output path must end in "+ext+" to match the selected format.", false)
			}
			if err := core.CheckOutput(absOut); err != nil {
				return err
			}
			outPath = absOut
		}
		if dDryRun {
			preview := map[string]any{
				"dry_run":      true,
				"model":        s.Model,
				"display_name": strings.TrimSpace(dName),
				"gender":       gender,
				"language":     langCode,
				"format":       s.Format,
			}
			if outPath != "" {
				preview["sample_file"] = outPath
			}
			a.result = preview
			a.human = fmt.Sprintf("Dry run: voice design on %s", s.Model)
			return nil
		}
		vd, ok := a.backend.(VoiceDesigner)
		if !ok {
			return core.Fail("INVALID_CONFIG", "The configured backend does not support Gemini voice design.", false)
		}
		res, err := vd.DesignVoice(cmd.Context(), core.VoiceDesignRequest{
			Prompt:       prompt,
			DisplayName:  strings.TrimSpace(dName),
			Gender:       gender,
			LanguageCode: langCode,
			Model:        s.Model,
			KeyTier:      s.KeyTier,
			Format:       s.Format,
			Bitrate:      s.Bitrate,
			ConfigDir:    s.ConfigDir,
			CacheDir:     s.CacheDir,
			Warn:         a.warn,
		})
		if err != nil {
			return err
		}
		if outPath != "" && len(res.SampleAudio) > 0 {
			if !core.ValidAudio(res.SampleAudio, s.Format) {
				return core.Fail("INVALID_AUDIO", "Gemini returned invalid or incomplete sample audio.", false)
			}
			if err := core.WriteOutput(outPath, res.SampleAudio); err != nil {
				return err
			}
			res.SampleFile = outPath
		}
		a.result = res
		if res.SampleFile != "" {
			a.human = fmt.Sprintf("%s (sample: %s)", res.ID, res.SampleFile)
		} else {
			a.human = res.ID
		}
		return nil
	})
	design.Flags().StringVar(&dName, "name", "", "Optional display name for the designed voice")
	design.Flags().StringVar(&dLang, "lang", "", "Optional BCP-47 locale hint (e.g. en-US)")
	design.Flags().StringVar(&dGender, "gender", "", "Optional voice gender (male, female, or neutral)")
	design.Flags().StringVar(&dModel, "model", "", "Select a Gemini 3.8 TTS model")
	design.Flags().StringVar(&dKeyTier, "key-tier", "", "Select Gemini key tier (auto, free, or paid)")
	design.Flags().StringVarP(&dOut, "output", "o", "", "Save preview sample audio to this path")
	design.Flags().StringVar(&dFormat, "format", "", "Preview audio format (wav or mp3)")
	design.Flags().IntVar(&dBitrate, "bitrate", 96, "Preview MP3 bitrate in kbps (8-320)")
	design.Flags().BoolVar(&dDryRun, "dry-run", false, "Validate inputs without calling the Gemini API")

	parent.AddCommand(list, design)
	return parent
}

func (a *app) modifiers() *cobra.Command {
	parent := &cobra.Command{Use: "modifiers", Short: "List supported inline text modifiers and style controls", Aliases: []string{"m"}}
	var provider string
	list := command("list", "List supported inline modifier tags", []string{"ls"}, exactly(0), func(_ *cobra.Command, _ []string) error {
		p := strings.ToLower(strings.TrimSpace(provider))
		if p != "" && p != core.ProviderGoogle && p != core.ProviderGemini {
			return core.Fail("INVALID_INPUT", "Provider must be google or gemini.", false)
		}
		tags := core.Modifiers(p)
		a.result = map[string]any{
			"tags":                   tags,
			"gemini_style_supported": p == "" || p == core.ProviderGemini,
			"gemini_style_examples":  []string{"warm and neutral", "whispered, nervous", "excited, fast pace", "sarcastic", "commanding, dramatic pauses"},
		}
		var out strings.Builder
		fmt.Fprintf(&out, "%d inline modifier tags", len(tags))
		for _, t := range tags {
			fmt.Fprintf(&out, "\n%s\t[%s · %s]\t%s", t.Tag, t.Provider, t.Category, t.Description)
		}
		if p == "" || p == core.ProviderGemini {
			out.WriteString("\n\nGemini 3.8 sustained delivery: use --style \"<natural language direction>\" or script turns.")
		}
		a.human = out.String()
		return nil
	})
	list.Flags().StringVar(&provider, "provider", "", "Filter by provider (google or gemini)")
	parent.AddCommand(list)
	return parent
}

var providerIDs = []string{core.ProviderGoogle, core.ProviderGemini}

func (a *app) providers() *cobra.Command {
	p := &cobra.Command{Use: "providers", Aliases: []string{"p"}, Short: "Explore supported providers"}
	p.AddCommand(command("list", "List implemented providers", []string{"ls"}, exactly(0), func(_ *cobra.Command, _ []string) error {
		a.result, a.human = map[string]any{"providers": providerIDs}, "google (Chirp 3 HD)\ngemini (Gemini 3.8 TTS: gemini-3.8-flash-tts, gemini-3.8-flash-lite-tts)"
		return nil
	}))
	return p
}

func (a *app) capabilities() *cobra.Command {
	return command("capabilities", "Show supported operations and limits", []string{"cap"}, exactly(0), func(_ *cobra.Command, _ []string) error {
		geminiTags := make([]string, 0, len(core.GeminiInlineTags()))
		for _, t := range core.GeminiInlineTags() {
			geminiTags = append(geminiTags, t.Tag)
		}
		googleTags := make([]string, 0, len(core.GoogleMarkupTags()))
		for _, t := range core.GoogleMarkupTags() {
			googleTags = append(googleTags, t.Tag)
		}
		a.result = map[string]any{
			"providers": providerIDs, "default_provider": core.ProviderGoogle, "voice_family": "Chirp3-HD",
			"gemini_models":  []string{core.DefaultGeminiModel, core.GeminiFlashLiteTTSModel},
			"key_tiers":      []string{core.KeyTierAuto, core.KeyTierFree, core.KeyTierPaid},
			"languages":      core.Languages(),
			"voices_command": "ttsi voices list --json", "modifiers_command": "ttsi modifiers list --json",
			"catalog_source": "local", "catalog_date": core.CatalogDate,
			"input_formats": []string{"text", "script"}, "output_formats": []string{core.FormatWAV, core.FormatMP3}, "default_output_format": core.FormatMP3, "max_text_bytes": core.MaxTextBytes,
			"synthesis_parameters":   []string{"provider", "model", "key_tier", "language", "voice", "style", "format", "bitrate", "script", "turns", "speakers"},
			"unsupported_parameters": []string{"ssml", "speaking_rate", "pitch", "custom_pronunciation", "temperature", "seed", "system_instruction"},
			"gemini_capabilities": map[string]any{
				"style_direction": true,
				"inline_tags":     geminiTags,
				"multi_turn":      true,
				"multi_speaker":   map[string]any{"supported": true, "max_speakers": 2},
				"language_auto":   true,
				"voice_design":    true,
			},
			"google_capabilities": map[string]any{
				"markup_tags": googleTags,
			},
			"batch": true, "cache": true, "dry_run": true,
		}
		a.human = "Google Chirp 3 HD & Gemini 3.8 TTS · plain text or script to WAV/MP3 (default: MP3 at 96 kbps via FFmpeg) · 5,000 UTF-8 bytes per item\nUse ttsi voices list and ttsi modifiers list to discover voices, styles, and inline tags."
		return nil
	})
}

func (a *app) config() *cobra.Command {
	p := &cobra.Command{Use: "config", Aliases: []string{"cfg"}, Short: "Inspect or edit the shared YAML configuration"}
	show := command("show", "Show stored and effective settings", []string{"sh"}, exactly(0), func(cmd *cobra.Command, _ []string) error {
		c, e := a.load(cmd, false)
		if e != nil {
			return e
		}
		s, e := c.Resolve(nil, nil)
		if e != nil {
			return e
		}
		a.configResult(c, s, "Configuration")
		return nil
	})
	set := command("set <key=value>...", "Save settings together", []string{"s"}, requiresArgs, func(cmd *cobra.Command, args []string) error {
		values := map[string]string{}
		for _, pair := range args {
			k, v, ok := strings.Cut(pair, "=")
			_, duplicate := values[k]
			if !ok || k == "" || duplicate {
				return core.Fail("INVALID_USAGE", "Use distinct key=value arguments.", false)
			}
			values[k] = v
		}
		return a.edit(cmd, values, nil)
	})
	unset := command("unset <key>...", "Reset saved settings to their defaults or environment overrides", []string{"u"}, requiresArgs, func(cmd *cobra.Command, args []string) error {
		return a.edit(cmd, nil, args)
	})
	p.AddCommand(show, set, unset)
	return p
}
func (a *app) edit(cmd *cobra.Command, set map[string]string, unset []string) error {
	path, explicit, e := a.path(cmd)
	if e != nil {
		return e
	}
	c, s, e := core.EditConfig(path, explicit, set, unset)
	if e != nil {
		return e
	}
	a.configResult(c, s, "Updated configuration")
	return nil
}
func (a *app) configResult(c core.Config, s core.Settings, heading string) {
	a.result = map[string]any{"file": c.Path, "stored": c.Stored, "effective": s}
	var extra strings.Builder
	if s.Model != "" {
		fmt.Fprintf(&extra, "\nModel: %s", s.Model)
	}
	if s.Style != "" {
		fmt.Fprintf(&extra, "\nStyle: %s", s.Style)
	}
	a.human = fmt.Sprintf("%s: %s\nProvider: %s%s\nLanguage: %s\nVoice: %s\nFormat: %s\nOutput directory: %s\nCache directory: %s\nJobs: %d", heading, c.Path, s.Provider, extra.String(), s.Language, s.Voice, s.Format, s.OutputDir, s.CacheDir, s.Jobs)
}

type check struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (a *app) doctor() *cobra.Command {
	var online bool
	cmd := command("doctor", "Check local setup and optionally Google API access", []string{"d"}, exactly(0), func(cmd *cobra.Command, _ []string) error {
		checks := []check{}
		payload := map[string]any{"checks": checks, "online": online}
		var p *core.Problem
		c, err := a.load(cmd, false)
		var s core.Settings
		if err == nil {
			s, err = c.Resolve(nil, nil)
		}
		if err != nil {
			p = core.Public(err)
			checks = append(checks, check{"config", "failed", p.Message})
		} else {
			checks = append(checks, check{"config", "ok", "Configuration is valid."})
			for _, target := range [][2]string{{"config_directory", filepath.Dir(c.Path)}, {"output_directory", s.OutputDir}, {"cache_directory", s.CacheDir}} {
				if e := core.ProbeDirectory(target[1]); e != nil {
					p = core.Fail("FILE_IO", "A required directory is not writable.", false)
					checks = append(checks, check{target[0], "failed", p.Message})
				} else {
					checks = append(checks, check{target[0], "ok", "Directory or nearest existing ancestor is writable."})
				}
			}
		}
		checks = append(checks, adcHint())
		checks = append(checks, geminiKeysHint(s.ConfigDir))
		if online && err == nil {
			var probeErr error
			if s.Provider == core.ProviderGemini {
				if pvl, ok := a.backend.(ProviderVoiceLister); ok {
					_, probeErr = pvl.ListVoicesForProvider(cmd.Context(), core.ProviderGemini, "")
				} else {
					_, probeErr = a.backend.ListVoices(cmd.Context(), "")
				}
			} else {
				_, probeErr = a.backend.ListVoices(cmd.Context(), "")
			}
			if probeErr != nil {
				if p == nil {
					p = core.Public(probeErr)
				}
				checks = append(checks, check{s.Provider + "_api", "failed", core.Public(probeErr).Message})
			} else {
				checks = append(checks, check{s.Provider + "_api", "ok", "ListVoices succeeded; synthesis permission and quota were not tested."})
			}
		} else if online {
			checks = append(checks, check{"google_api", "skipped", "Correct the local configuration before probing Google."})
		} else {
			checks = append(checks, check{"google_api", "not_checked", "Use --online to check API access without synthesizing audio."})
		}
		payload["checks"] = checks
		a.result = payload
		var lines []string
		for _, c := range checks {
			lines = append(lines, fmt.Sprintf("%s: %s — %s", c.Check, c.Status, c.Message))
		}
		a.human = strings.Join(lines, "\n")
		if p != nil {
			return p
		}
		return nil
	})
	cmd.Flags().BoolVar(&online, "online", false, "Probe Google using ListVoices (no audio synthesis)")
	return cmd
}

func geminiKeysHint(configDir string) check {
	keys, err := geminitts.LoadKeys(configDir)
	if err != nil {
		return check{"gemini_keys", "warning", core.Public(err).Message}
	}
	if len(keys) == 0 {
		return check{"gemini_keys", "not_configured", "No Gemini API keys configured. Use ttsi keys add or GEMINI_API_KEY when using --provider gemini."}
	}
	var freeCount, paidCount int
	for _, k := range keys {
		if k.Tier == geminitts.TierPaid {
			paidCount++
		} else {
			freeCount++
		}
	}
	return check{"gemini_keys", "present", fmt.Sprintf("Configured %d Gemini API key(s) (%d free, %d paid).", len(keys), freeCount, paidCount)}
}

func adcHint() check {
	if path, ok := os.LookupEnv("GOOGLE_APPLICATION_CREDENTIALS"); ok && path != "" {
		if readableFile(path) {
			return check{"adc", "present", "The ADC environment variable points to a readable file; validity is not checked offline."}
		}
		return check{"adc", "warning", "The ADC environment variable does not point to a readable file."}
	}
	var path string
	if dir := os.Getenv("CLOUDSDK_CONFIG"); dir != "" {
		path = filepath.Join(dir, "application_default_credentials.json")
	} else if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			path = filepath.Join(dir, "gcloud", "application_default_credentials.json")
		}
	} else if home, e := os.UserHomeDir(); e == nil {
		path = filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	}
	if path != "" && readableFile(path) {
		return check{"adc", "present", "A local gcloud ADC file is readable; validity is not checked offline."}
	}
	return check{"adc", "not_verified", "No local ADC file found. Workload identity may still work; use --online to verify access."}
}
func readableFile(path string) bool {
	i, e := os.Stat(path)
	if e != nil || !i.Mode().IsRegular() {
		return false
	}
	f, e := os.Open(path)
	if e != nil {
		return false
	}
	return f.Close() == nil
}
