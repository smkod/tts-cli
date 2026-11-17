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
			p = core.Fail("INVALID_USAGE", "Invalid command, arguments or flags. See tts --help.", false)
		}
		if a.code == 0 {
			a.code = exitFor(p)
		}
	}
	if a.json || a.alwaysJSON || (err != nil && earlyJSON) {
		if encodeErr := json.NewEncoder(stdout).Encode(response{1, err == nil && a.code == 0, a.result, p}); encodeErr != nil {
			fmt.Fprintln(stderr, "tts: Cannot write JSON output.")
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
	fmt.Fprintln(a.stderr, "tts: "+msg)
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
	root := &cobra.Command{Use: "tts", Short: "Generate Chirp 3 HD speech for people and scripts.",
		SilenceUsage: true, SilenceErrors: true, DisableSuggestions: true,
		Example: "  tts \"Hello there\"\n  tts g \"Come stai?\" --lang it-IT --json\n  tts cfg s language=it-IT voice=kore",
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "Emit versioned JSON")
	root.PersistentFlags().StringVar(&a.configFlag, "config", "", "Select a YAML configuration file")
	root.AddCommand(a.generate(), a.batch(), a.voices(), a.providers(), a.config(), a.capabilities(), a.doctor(), a.login())
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
	lang, voice, provider, cache, out, outDir string
	noCache, dryRun                           bool
	jobs                                      int
}

func bindSynth(cmd *cobra.Command, f *synthFlags, batch bool) {
	flags := cmd.Flags()
	flags.StringVar(&f.lang, "lang", "", "Select a voice language")
	flags.StringVar(&f.voice, "voice", "", "Select a voice alias or full Chirp 3 HD ID")
	flags.StringVar(&f.provider, "provider", "", "Select a synthesis provider")
	flags.StringVar(&f.cache, "cache-dir", "", "Override the cache directory")
	flags.BoolVar(&f.noCache, "no-cache", false, "Skip cache reads and writes")
	flags.BoolVar(&f.dryRun, "dry-run", false, "Inspect the request without network or file writes")
	if batch {
		flags.StringVar(&f.outDir, "output-dir", "", "Override the output directory")
		flags.IntVar(&f.jobs, "jobs", 4, "Set batch concurrency (1–16)")
	} else {
		flags.StringVarP(&f.out, "output", "o", "", "Write to this MP3 path")
	}
}
func (f *synthFlags) values(cmd *cobra.Command) map[string]string {
	out := map[string]string{}
	values := [][3]string{{"lang", "language", f.lang}, {"voice", "voice", f.voice}, {"provider", "provider", f.provider},
		{"cache-dir", "cache_dir", f.cache}, {"output-dir", "output_dir", f.outDir}, {"jobs", "jobs", fmt.Sprint(f.jobs)}}
	for _, v := range values {
		if cmd.Flags().Changed(v[0]) {
			out[v[1]] = v[2]
		}
	}
	return out
}

func (a *app) generate() *cobra.Command {
	f := &synthFlags{}
	cmd := command("generate <text>", "Generate one MP3", []string{"g"}, exactly(1), func(cmd *cobra.Command, args []string) error {
		if e := core.ValidateText(args[0]); e != nil {
			return e
		}
		if cmd.Flags().Changed("output") && strings.TrimSpace(f.out) == "" {
			return core.Fail("INVALID_INPUT", "Output path must not be blank.", false)
		}
		s, e := a.settings(cmd, f.values(cmd))
		if e != nil {
			return e
		}
		eng := &core.Engine{Provider: a.backend, Warn: a.warn}
		g, e := eng.Generate(cmd.Context(), s, args[0], core.Options{Output: f.out, DryRun: f.dryRun, NoCache: f.noCache})
		if e != nil {
			return e
		}
		a.result, a.human = g, core.FormatFile(g)
		return nil
	})
	bindSynth(cmd, f, false)
	return cmd
}

func (a *app) voices() *cobra.Command {
	parent := &cobra.Command{Use: "voices", Short: "Explore Chirp 3 HD voices", Aliases: []string{"v"}}
	var lang string
	var online bool
	list := command("list", "List compatible voices", []string{"ls"}, exactly(0), func(cmd *cobra.Command, _ []string) error {
		voices := core.LocalVoices(lang)
		payload := map[string]any{"source": "local", "catalog_date": core.CatalogDate}
		if online {
			var err error
			voices, err = a.backend.ListVoices(cmd.Context(), lang)
			if err != nil {
				a.code = 1
				return err
			}
			payload = map[string]any{"source": "google"}
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
	list.Flags().BoolVar(&online, "online", false, "Query Google rather than the bundled catalog")
	parent.AddCommand(list)
	return parent
}

var providerIDs = []string{"google"}

func (a *app) providers() *cobra.Command {
	p := &cobra.Command{Use: "providers", Aliases: []string{"p"}, Short: "Explore supported providers"}
	p.AddCommand(command("list", "List implemented providers", []string{"ls"}, exactly(0), func(_ *cobra.Command, _ []string) error {
		a.result, a.human = map[string]any{"providers": providerIDs}, "google (Chirp 3 HD)"
		return nil
	}))
	return p
}

func (a *app) capabilities() *cobra.Command {
	return command("capabilities", "Show supported operations and limits", []string{"cap"}, exactly(0), func(_ *cobra.Command, _ []string) error {
		a.result = map[string]any{
			"providers": providerIDs, "default_provider": "google", "voice_family": "Chirp3-HD", "languages": core.Languages(),
			"voices_command": "tts voices list --json", "catalog_source": "local", "catalog_date": core.CatalogDate,
			"input_formats": []string{"text"}, "output_formats": []string{"mp3"}, "max_text_bytes": core.MaxTextBytes,
			"synthesis_parameters":   []string{"provider", "language", "voice"},
			"unsupported_parameters": []string{"ssml", "speaking_rate", "pitch", "custom_pronunciation", "pause_control"},
			"batch":                  true, "cache": true, "dry_run": true,
		}
		a.human = "Google Chirp 3 HD · plain text to MP3 · 5,000 UTF-8 bytes per item\nUse tts voices list to discover languages and voices."
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
	a.human = fmt.Sprintf("%s: %s\nProvider: %s\nLanguage: %s\nVoice: %s\nOutput directory: %s\nCache directory: %s\nJobs: %d", heading, c.Path, s.Provider, s.Language, s.Voice, s.OutputDir, s.CacheDir, s.Jobs)
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
		if online && err == nil {
			if _, e := a.backend.ListVoices(cmd.Context(), ""); e != nil {
				if p == nil {
					p = core.Public(e)
				}
				checks = append(checks, check{"google_api", "failed", core.Public(e).Message})
			} else {
				checks = append(checks, check{"google_api", "ok", "ListVoices succeeded; synthesis permission and quota were not tested."})
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
