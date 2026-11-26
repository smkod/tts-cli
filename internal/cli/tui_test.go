package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/smkod/tts-cli/internal/geminitts"
	"github.com/smkod/tts-cli/internal/tui"
)

func TestTUICommandGuards(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		code int
	}{
		{name: "help", args: []string{"tui", "--help"}, want: "interactive Generate", code: 0},
		{name: "json", args: []string{"tui", "--json"}, want: "INVALID_USAGE", code: 2},
		{name: "redirected output", args: []string{"tui"}, want: "interactive stdin and stdout", code: 2},
		{name: "no arguments", want: "Usage:", code: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace(t)
			code, out, diagnostic := runTest(&mockBackend{}, tc.args...)
			if code != tc.code || !strings.Contains(out+diagnostic, tc.want) {
				t.Fatalf("got %d, %q, %q", code, out, diagnostic)
			}
		})
	}
}

func TestTUIInjectedRunnerUsesCoreConfigurationAndGeneration(t *testing.T) {
	workspace(t)
	t.Setenv("TTS_LANGUAGE", "it-IT")
	backend := &mockBackend{}
	var out, diagnostic bytes.Buffer
	called := false
	a := &app{
		backend: backend, stdin: strings.NewReader(""), stdout: &out, stderr: &diagnostic,
		terminal: func(any) bool { return true },
		runTUI: func(ctx context.Context, options tui.Options, input io.Reader, output io.Writer) error {
			called = true
			if options.Settings.Language != "it-IT" || input == nil || output != &out {
				t.Fatalf("incorrect TUI initialization: %+v", options.Settings)
			}
			overrides := map[string]string{"language": "en-US", "voice": "kore", "format": "wav", "style": ""}
			generation, err := options.Generate(ctx, overrides, "Hello", core.Options{DryRun: true})
			if err != nil || generation.Language != "en-US" || generation.Format != "wav" {
				t.Fatalf("generation did not use core resolver: %+v, %v", generation, err)
			}
			for _, tc := range []struct {
				name      string
				text      string
				overrides map[string]string
				code      string
			}{
				{name: "blank text", text: " ", code: "INVALID_TEXT"},
				{name: "invalid format", text: "Hello", overrides: map[string]string{"format": "bad"}, code: "INVALID_CONFIG"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := options.Generate(ctx, tc.overrides, tc.text, core.Options{DryRun: true})
					if err == nil || core.Public(err).Code != tc.code {
						t.Fatalf("got %v, want %s", err, tc.code)
					}
				})
			}
			return nil
		},
	}
	code := a.run(context.Background(), []string{"tui"})
	if code != 0 || !called || len(backend.requests) != 0 || out.Len()+diagnostic.Len() != 0 {
		t.Fatalf("TUI run: code=%d called=%v requests=%d output=%q %q",
			code, called, len(backend.requests), out.String(), diagnostic.String())
	}
}

func TestTUINameIsReservedWithoutChangingGenerationShorthand(t *testing.T) {
	workspace(t)
	for _, args := range [][]string{
		{"Hello", "--dry-run", "--json"},
		{"generate", "tui", "--dry-run", "--json"},
		{"generate", "--dry-run", "--json", "--", "tui"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, out, diagnostic := runTest(&mockBackend{}, args...)
			if code != 0 || !strings.Contains(out, `"dry_run":true`) {
				t.Fatalf("shorthand regression: %d %s %s", code, out, diagnostic)
			}
		})
	}
}

func TestTUIConfigFlagOverridesEnvironmentPath(t *testing.T) {
	dir := workspace(t)
	path := filepath.Join(dir, "alternate.yaml")
	if err := os.WriteFile(path, []byte("language: fr-FR\nformat: wav\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	called := false
	a := &app{
		backend: &mockBackend{}, stdin: strings.NewReader(""), stdout: &out, stderr: &out,
		terminal: func(any) bool { return true },
		runTUI: func(_ context.Context, options tui.Options, _ io.Reader, _ io.Writer) error {
			called = true
			if options.Settings.Language != "fr-FR" || options.Settings.Format != "wav" || options.Settings.ConfigDir != dir {
				t.Fatalf("wrong config: %+v", options.Settings)
			}
			return nil
		},
	}
	if code := a.run(context.Background(), []string{"--config", path, "tui"}); code != 0 || !called {
		t.Fatalf("config flag: code=%d called=%v output=%s", code, called, out.String())
	}
}

func TestTUIRejectsEitherNonterminalBeforeLoadingConfiguration(t *testing.T) {
	for _, inputTerminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "stdin", true: "stdout"}[inputTerminal], func(t *testing.T) {
			workspace(t)
			t.Setenv("TTS_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
			input := strings.NewReader("")
			var out bytes.Buffer
			a := &app{
				backend: &mockBackend{}, stdin: input, stdout: &out, stderr: &out,
				terminal: func(stream any) bool { return (stream == input) == inputTerminal },
				runTUI: func(context.Context, tui.Options, io.Reader, io.Writer) error {
					t.Fatal("nonterminal opened TUI")
					return nil
				},
			}
			code := a.run(context.Background(), []string{"tui"})
			if code != 2 || !strings.Contains(out.String(), "interactive stdin and stdout") {
				t.Fatalf("wrong error: %d %s", code, out.String())
			}
		})
	}
}

func TestUsageLoaderReturnsOnlySafeConfiguredMetadata(t *testing.T) {
	dir := workspace(t)
	for _, name := range []string{
		"TTS_GEMINI_KEYS_FILE", "TTS_GEMINI_FREE_KEYS", "TTS_GEMINI_PAID_KEYS", "GEMINI_API_KEYS", "GEMINI_API_KEY",
	} {
		t.Setenv(name, "")
	}
	secret := "private-test-api-secret-123456789"
	if _, err := geminitts.AddKey(dir, "Work", geminitts.TierPaid, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := geminitts.AddKey(dir, "Unused", geminitts.TierFree, "other-private-test-secret-abcdefgh"); err != nil {
		t.Fatal(err)
	}
	usage := core.Usage{CredentialID: geminitts.KeyID(secret)}
	record := core.EstimateUsage(core.ProviderGemini, core.DefaultGeminiModel, usage, time.Now())
	if err := core.SaveUsage(dir, record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := usageLoader(dir)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Configurations) != 3 || snapshot.Configurations[1].ID != geminitts.KeyID(secret) ||
		snapshot.Configurations[1].Name != "Work" || snapshot.Configurations[2].Name != "Unused" ||
		snapshot.Report.Configurations[core.ProviderGemini][geminitts.KeyID(secret)].UnknownRequests != 1 {
		t.Fatalf("wrong snapshot: %+v", snapshot)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), geminitts.MaskKey(secret)) {
		t.Fatalf("snapshot exposes secret: %s", data)
	}
	entries, err := os.ReadDir(core.UsageDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(core.UsageDir(dir), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) || strings.Contains(string(data), "Work") {
			t.Fatalf("record exposes credential metadata: %s", data)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := usageLoader(dir)(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestUsageLoaderErrorsAreLocalAndDoNotPreventGenerate(t *testing.T) {
	dir := workspace(t)
	if err := os.MkdirAll(core.UsageDir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(core.UsageDir(dir), "broken.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	called := false
	a := &app{
		backend: &mockBackend{}, stdin: strings.NewReader(""), stdout: &out, stderr: &out,
		terminal: func(any) bool { return true },
		runTUI: func(ctx context.Context, options tui.Options, _ io.Reader, _ io.Writer) error {
			called = true
			if _, err := options.LoadUsage(ctx); err == nil || core.Public(err).Code != "FILE_IO" {
				t.Fatalf("expected local usage error: %v", err)
			}
			if _, err := options.Generate(ctx, nil, "Hello", core.Options{DryRun: true}); err != nil {
				t.Fatalf("usage error broke generation: %v", err)
			}
			return nil
		}}
	if code := a.run(context.Background(), []string{"tui"}); code != 0 || !called {
		t.Fatalf("usage loader killed form: %d %v %s", code, called, out.String())
	}
}
