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
	mu         sync.Mutex
	requests   []core.Request
	designReqs []core.VoiceDesignRequest
	lists      int
}

func (m *mockBackend) DesignVoice(_ context.Context, req core.VoiceDesignRequest) (core.VoiceDesignResult, error) {
	m.mu.Lock()
	m.designReqs = append(m.designReqs, req)
	m.mu.Unlock()
	wav := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\xc0\x5d\x00\x00\x80\xbb\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00")
	return core.VoiceDesignResult{
		ID:           "voice_custom123",
		Name:         "voices/voice_custom123",
		DisplayName:  req.DisplayName,
		Model:        req.Model,
		LanguageCode: req.LanguageCode,
		Gender:       req.Gender,
		SampleAudio:  wav,
	}, nil
}

func (m *mockBackend) Synthesize(_ context.Context, r core.Request) ([]byte, error) {
	m.mu.Lock()
	m.requests = append(m.requests, r)
	m.mu.Unlock()
	if r.Text == "private rejection" {
		return nil, core.Fail("PROVIDER_REJECTED", "Google rejected the request.", false)
	}
	if r.Format == core.FormatWAV {
		return []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\xc0\x5d\x00\x00\x80\xbb\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00"), nil
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
		{"generate", "generate Hello --voice kore --dry-run", "g Hello --voice kore --dry-run"},
		{"voices", "voices list --lang it-IT", "v ls --lang it-IT"},
		{"modifiers", "modifiers list --provider gemini", "m ls --provider gemini"},
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

func TestGenerateOutputFormat(t *testing.T) {
	workspace(t)
	m := &mockBackend{}
	for _, tc := range []struct {
		name       string
		args       []string
		wantFormat string
	}{
		{"default MP3", nil, "mp3"},
		{"explicit WAV", []string{"--format", "wav"}, "wav"},
		{"mismatched extension", []string{"-o", "wrong.wav"}, ""},
		{"invalid format", []string{"--format", "flac"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, diag := runTest(m, append([]string{"hello", "--json"}, tc.args...)...)
			if tc.wantFormat == "" {
				if code == 0 {
					t.Fatalf("expected a format error: %s", out)
				}
				return
			}
			if code != 0 || !strings.Contains(out, `"format":"`+tc.wantFormat+`"`) ||
				!strings.Contains(out, "."+tc.wantFormat+`"`) {
				t.Fatalf("unexpected format: code %d, output %s, diagnostics %s", code, out, diag)
			}
		})
	}
	if len(m.requests) != 2 || m.requests[0].Format != core.FormatMP3 || m.requests[1].Format != core.FormatWAV {
		t.Fatalf("expected distinct synthesis requests for each format, got %v", m.requests)
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
func TestGenerateVoiceSelection(t *testing.T) {
	workspace(t)
	for _, tc := range []struct {
		name  string
		args  []string
		lang  string
		voice string
	}{
		{"default locale", nil, "en-US", ""},
		{"requested locale", []string{"--lang", "IT-it"}, "it-IT", ""},
		{"explicit voice", []string{"--lang", "it-IT", "--voice", "kore"}, "it-IT", "it-IT-Chirp3-HD-Kore"},
		{"unknown locale", []string{"--lang", "xx-XX"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockBackend{}
			code, out, _ := runTest(m, append([]string{"generate", "Hello", "--dry-run", "--json"}, tc.args...)...)
			var result struct {
				Data  core.Generation
				Error *core.Problem
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if len(m.requests) != 0 || m.lists != 0 {
				t.Fatal("dry run contacted the provider")
			}
			if tc.lang == "" {
				if code != 2 || result.Error == nil || result.Error.Code != "INVALID_VOICE" {
					t.Fatalf("unexpected error: %d %s", code, out)
				}
				return
			}
			if code != 0 || result.Data.Language != tc.lang || (tc.voice != "" && result.Data.Voice != tc.voice) {
				t.Fatalf("unexpected voice selection: %d %s", code, out)
			}
			for _, voice := range core.LocalVoices(tc.lang) {
				if voice.Name == result.Data.Voice {
					return
				}
			}
			t.Fatalf("voice is not in the catalog for %s: %s", tc.lang, out)
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
	code, out, diag := runTest(m, "b", manifestPath, "--voice", "kore")
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
	code, out, diag = runTest(m, "batch", manifestPath, "--voice", "kore", "--json")
	r = parse(out)
	if code != 3 || !r.Items[0].Data.Cached || !r.Items[2].Data.Cached || diag != "" || len(m.requests) != 4 {
		t.Fatalf("repeat batch: %d %s %q", code, out, diag)
	}
}

func TestGeminiModifiersStyleScriptAndVoiceDesign(t *testing.T) {
	dir := workspace(t)
	m := &mockBackend{}

	// 1. Local-first modifier validation rejects bad/mismatched tags before touching backend.
	for _, args := range [][]string{
		{"generate", "Hello <sniff>", "--provider", "gemini", "--json"},
		{"generate", "Hello [laughs]", "--provider", "gemini", "--json"},
		{"generate", "Hello <laugh>", "--provider", "google", "--json"},
		{"generate", "Hello [pause medium]", "--provider", "google", "--json"},
		{"generate", "Hello", "--provider", "google", "--style", "warm", "--json"},
		{"generate", "Hello", "--provider", "google", "--lang", "auto", "--json"},
	} {
		code, out, _ := runTest(m, args...)
		if code != 2 {
			t.Fatalf("expected exit 2 for %v, got %d: %s", args, code, out)
		}
	}
	if len(m.requests) != 0 {
		t.Fatalf("invalid local inputs contacted backend: %d", len(m.requests))
	}

	// 2. Valid Gemini style + inline tags + auto language.
	code, out, _ := runTest(m, "generate", "Wait... <short pause> who is there? <gasp>",
		"--provider", "gemini", "--voice", "kore", "--style", "whispered, nervous", "--lang", "auto", "--format", "wav", "--json")
	if code != 0 || !strings.Contains(out, `"style":"whispered, nervous"`) || !strings.Contains(out, `"detected_tags":["short pause","gasp"]`) {
		t.Fatalf("gemini styled generate failed: %d %s", code, out)
	}
	if len(m.requests) != 1 || m.requests[0].LanguagePinned || m.requests[0].Style != "whispered, nervous" {
		t.Fatalf("unexpected backend request: %+v", m.requests)
	}

	// 3. Script mode (multi-speaker dialogue).
	scriptPath := filepath.Join(dir, "dialogue.json")
	scriptJSON := `{"speakers":{"Host":"puck","Guest":"kore"},"turns":[{"speaker":"Host","style":"warm","text":"Hello! <short pause>"},{"speaker":"Guest","text":"<sigh> Hi."}]}`
	if err := os.WriteFile(scriptPath, []byte(scriptJSON), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runTest(m, "generate", "--script", scriptPath, "--provider", "gemini", "--format", "wav", "--json")
	if code != 0 || !strings.Contains(out, `"turns":2`) || !strings.Contains(out, `"speakers":["Guest","Host"]`) {
		t.Fatalf("script generate failed: %d %s", code, out)
	}
	if len(m.requests) != 2 || len(m.requests[1].Turns) != 2 || m.requests[1].Speakers["Host"] != "Puck" {
		t.Fatalf("unexpected script backend request: %+v", m.requests[1])
	}

	// Script validation conflicts.
	code, _, _ = runTest(m, "generate", "extra text", "--script", scriptPath, "--provider", "gemini", "--json")
	if code != 2 {
		t.Fatalf("expected exit 2 when combining text and --script, got %d", code)
	}
	code, _, _ = runTest(m, "generate", "--script", scriptPath, "--provider", "gemini", "--voice", "kore", "--json")
	if code != 2 {
		t.Fatalf("expected exit 2 when combining --voice and multi-speaker script, got %d", code)
	}

	// 4. Batch turns + style item alongside invalid mixed item.
	batchPath := filepath.Join(dir, "expressive-batch.json")
	batchJSON := `{"items":[{"id":"styled","provider":"gemini","voice":"kore","style":"cheerful","text":"Hi <laugh>"},{"id":"dialogue","provider":"gemini","speakers":{"A":"puck","B":"kore"},"turns":[{"speaker":"A","text":"Ping"},{"speaker":"B","text":"Pong"}]},{"id":"bad","provider":"gemini","text":"x","turns":[{"text":"y"}]}]}`
	if err := os.WriteFile(batchPath, []byte(batchJSON), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runTest(m, "batch", batchPath, "--format", "wav")
	if code != 3 || !strings.Contains(out, `"succeeded":2,"failed":1`) {
		t.Fatalf("unexpected expressive batch result: %d %s", code, out)
	}

	// 5. Voice design local validation + execution.
	code, _, _ = runTest(m, "voices", "design", "Warm narrator", "--gender", "unknown", "--json")
	if code != 2 || len(m.designReqs) != 0 {
		t.Fatalf("expected local rejection for invalid gender, got %d", code)
	}
	sampleOut := filepath.Join(dir, "sample.wav")
	code, out, _ = runTest(m, "voices", "design", "Warm narrator", "--name", "Narrator", "--lang", "en-US", "--gender", "male", "--format", "wav", "-o", sampleOut, "--json")
	if code != 0 || !strings.Contains(out, `"id":"voice_custom123"`) || len(m.designReqs) != 1 {
		t.Fatalf("voice design failed: %d %s", code, out)
	}
	if _, err := os.Stat(sampleOut); err != nil {
		t.Fatalf("expected sample audio file at %s: %v", sampleOut, err)
	}
}
