package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKeysAddListRemoveReset(t *testing.T) {
	workspace(t)
	for _, k := range []string{"TTS_GEMINI_KEYS_FILE", "TTS_GEMINI_FREE_KEYS", "TTS_GEMINI_PAID_KEYS", "GEMINI_API_KEYS", "GEMINI_API_KEY"} {
		t.Setenv(k, "")
	}
	m := &mockBackend{}

	c, out, _ := runTest(m, "keys", "add", "AIza-free-secret-1111", "--tier", "free", "--name", "my-free", "--json")
	if c != 0 {
		t.Fatalf("keys add free failed (%d): %s", c, out)
	}
	if strings.Contains(out, "AIza-free-secret-1111") {
		t.Fatalf("keys add leaked full secret in JSON output: %s", out)
	}

	c, out, _ = runTest(m, "keys", "add", "AIza-paid-secret-9999", "--tier", "paid", "--json")
	if c != 0 {
		t.Fatalf("keys add paid failed (%d): %s", c, out)
	}

	c, out, _ = runTest(m, "keys", "list", "--json")
	if c != 0 {
		t.Fatalf("keys list failed (%d): %s", c, out)
	}
	var res struct {
		Success bool `json:"success"`
		Data    struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.Success || res.Data.Count != 2 {
		t.Fatalf("unexpected keys list output: %s", out)
	}

	c, out, _ = runTest(m, "keys", "remove", "my-free", "--json")
	if c != 0 {
		t.Fatalf("keys remove failed (%d): %s", c, out)
	}

	c, out, _ = runTest(m, "keys", "reset", "--json")
	if c != 0 {
		t.Fatalf("keys reset failed (%d): %s", c, out)
	}
}

func TestGeminiDryRunFlags(t *testing.T) {
	workspace(t)
	m := &mockBackend{}
	c, out, _ := runTest(m, "generate", "Hello Gemini", "--provider", "gemini", "--model", "gemini-3.8-flash-lite-tts", "--key-tier", "free", "--voice", "kore", "--dry-run", "--json")
	if c != 0 {
		t.Fatalf("gemini dry-run failed (%d): %s", c, out)
	}
	if !strings.Contains(out, `"provider":"gemini"`) || !strings.Contains(out, `"model":"gemini-3.8-flash-lite-tts"`) {
		t.Fatalf("unexpected dry-run JSON: %s", out)
	}
}
