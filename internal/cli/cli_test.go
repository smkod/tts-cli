package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/smkod/tts-cli/internal/core"
)

type mockBackend struct {
	mu       sync.Mutex
	requests []core.Request
	lists    int
}

func (m *mockBackend) Synthesize(_ context.Context, r core.Request) ([]byte, error) {
	m.mu.Lock()
	m.requests = append(m.requests, r)
	m.mu.Unlock()
	if r.Text == "private rejection" {
		return nil, core.Fail("PROVIDER_REJECTED", "Google rejected the request.", false)
	}
	b := make([]byte, 417)
	copy(b, []byte{0xff, 0xfb, 0x90, 0x00})
	return b, nil
}
func (m *mockBackend) ListVoices(_ context.Context, lang string) ([]core.Voice, error) {
	m.mu.Lock()
	m.lists++
	m.mu.Unlock()
	return core.LocalVoices(lang), nil
}
func (m *mockBackend) Close() error { return nil }
func workspace(t *testing.T) string {
	for _, k := range append([]string{"CONFIG"}, core.Keys...) {
		key := "TTS_" + strings.ToUpper(k)
		if old, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { _ = os.Setenv(key, old) })
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	b := []byte("output_dir: " + jsonString(filepath.Join(dir, "output")) + "\ncache_dir: " + jsonString(filepath.Join(dir, "cache")) + "\n")
	if err := os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TTS_CONFIG", cfg)
	return dir
}
func jsonString(v string) string { b, _ := json.Marshal(v); return string(b) }

func runTest(m *mockBackend, args ...string) (int, string, string) {
	var out, diag bytes.Buffer
	code := Run(context.Background(), args, &out, &diag, m)
	return code, out.String(), diag.String()
}

func TestCommandAliases(t *testing.T) {
	workspace(t)
	m := &mockBackend{}
	for _, tc := range []struct {
		name        string
		long, short string
	}{
		{"generate", "generate Hello --dry-run", "g Hello --dry-run"},
		{"voices", "voices list --lang it-IT", "v ls --lang it-IT"},
		{"providers", "providers list", "p ls"},
		{"capabilities", "capabilities", "cap"},
		{"config", "config show", "cfg sh"},
		{"doctor", "doctor", "d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, out, _ := runTest(m, append(strings.Fields(tc.long), "--json")...)
			ca, alias, _ := runTest(m, append(strings.Fields(tc.short), "--json")...)
			if c != 0 || c != ca || alias != out || !json.Valid([]byte(out)) {
				t.Fatalf("commands differ: %d %d", c, ca)
			}
		})
	}
	if len(m.requests) > 0 || m.lists > 0 {
		t.Fatal("an offline command called the backend")
	}
}
func TestGenerateRoutingAndJSONFailures(t *testing.T) {
	workspace(t)
	m := &mockBackend{}
	for _, tc := range []struct {
		name      string
		args      []string
		code      int
		errorCode string
	}{
		{"implicit", []string{"hello world", "--dry-run"}, 0, ""},
		{"implicit after global flag", []string{"--json", "hello world", "--dry-run"}, 0, ""},
		{"reserved name", []string{"g", "--dry-run", "--", "cap"}, 0, ""},
		{"text starts with dash", []string{"g", "--dry-run", "--", "-hola"}, 0, ""},
		{"invalid pitch", []string{"g", "private input", "--pitch=2"}, 2, "INVALID_USAGE"},
		{"missing argument", []string{"g"}, 2, "INVALID_USAGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, diag := runTest(m, append([]string{"--json"}, tc.args...)...)
			var r response
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatalf("invalid JSON: %q, %v", out, err)
			}
			if code != tc.code || r.Version != 1 || r.Success != (tc.code == 0) || diag != "" {
				t.Fatalf("unexpected output: %d %s %q", code, out, diag)
			}
			if tc.errorCode != "" && (r.Error == nil || r.Error.Code != tc.errorCode) {
				t.Fatalf("unexpected error: %s", out)
			}
			if strings.Contains(out, "private input") {
				t.Fatal("CLI leaked the text")
			}
		})
	}
}
func TestJSONConfigEditAndOnlineDiscovery(t *testing.T) {
	workspace(t)
	m := &mockBackend{}
	code, out, _ := runTest(m, "cfg", "s", "language=it-IT", "voice=kore", "jobs=2", "--json")
	if code != 0 || !strings.Contains(out, "it-IT-Chirp3-HD-Kore") {
		t.Fatalf("set: %d %s", code, out)
	}
	code, out, _ = runTest(m, "cfg", "u", "language", "--json")
	if code != 0 || !strings.Contains(out, "en-US-Chirp3-HD-Kore") {
		t.Fatalf("unset: %d %s", code, out)
	}
	code, out, _ = runTest(m, "v", "ls", "--online", "--lang", "it-IT", "--json")
	if code != 0 || !strings.Contains(out, `"source":"google"`) || m.lists != 1 {
		t.Fatalf("online voice list: %d %s", code, out)
	}
}

func TestBatchPartialFailureAndCache(t *testing.T) {
	dir := workspace(t)
	m := &mockBackend{}
	manifestPath := filepath.Join(dir, "items.json")
	input := `{"items":[{"id":"z","text":" Hi "},{"id":"invalid","text":5},{"id":"a","text":"Come stai?","language":"it-IT"},{"id":"failed","text":"private rejection"},{"id":"duplicate","text":"first"},{"id":"duplicate","text":"second"}]}`
	if err := os.WriteFile(manifestPath, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	parse := func(out string) manifest {
		t.Helper()
		var env struct {
			Success bool
			Data    manifest
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil || env.Success {
			t.Fatalf("unexpected result %q (%v)", out, err)
		}
		return env.Data
	}
	code, out, diag := runTest(m, "b", manifestPath)
	r := parse(out)
	if code != 3 || r.Total != 6 || r.Succeeded != 2 || r.Failed != 4 || !strings.Contains(diag, "2 succeeded") || len(m.requests) != 3 {
		t.Fatalf("first batch: %d %s %s (%d requests)", code, out, diag, len(m.requests))
	}
	if r.Items[0].ID != "z" || r.Items[1].ID != "invalid" || r.Items[2].ID != "a" || r.Items[2].Data == nil || r.Items[2].Data.Language != "it-IT" || strings.Contains(out, "private rejection") {
		t.Fatalf("wrong order or content: %s", out)
	}
	if !filepath.IsAbs(r.Items[0].Data.File) || r.Items[4].Error.Code != "DUPLICATE_ID" || r.Items[5].Error.Code != "DUPLICATE_ID" {
		t.Fatalf("wrong file or ID handling: %s", out)
	}
	code, out, diag = runTest(m, "batch", manifestPath, "--json")
	r = parse(out)
	if code != 3 || !r.Items[0].Data.Cached || !r.Items[2].Data.Cached || diag != "" || len(m.requests) != 4 {
		t.Fatalf("repeat batch: %d %s %q", code, out, diag)
	}
}
