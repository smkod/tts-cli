package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationPrecedence(t *testing.T) {
	c := Config{
		Stored:   map[string]string{"language": "fr-FR", "format": FormatWAV, "jobs": "8"},
		env:      map[string]string{"language": "de-DE", "format": FormatMP3},
		defaults: map[string]string{"bitrate": "96", "format": FormatMP3, "provider": "google", "key_tier": KeyTierAuto, "voice": "kore", "output_dir": t.TempDir(), "cache_dir": t.TempDir(), "jobs": "4"},
	}
	for _, tc := range []struct {
		name         string
		flags, item  map[string]string
		lang, format string
	}{
		{"flag wins", map[string]string{"language": "it-IT", "format": FormatMP3}, map[string]string{"language": "en-US", "format": FormatWAV}, "it-IT", FormatMP3},
		{"item wins", nil, map[string]string{"language": "en-US", "format": FormatWAV}, "en-US", FormatWAV},
		{"environment wins", nil, nil, "de-DE", FormatMP3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := c.Resolve(tc.flags, tc.item)
			if err != nil || s.Language != tc.lang || s.Format != tc.format || s.Jobs != 8 {
				t.Fatalf("got %+v, %v", s, err)
			}
		})
	}
}
func TestConfigEditRejectsInvalidTransaction(t *testing.T) {
	for _, k := range Keys {
		key := "TTS_" + strings.ToUpper(k)
		if old, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { _ = os.Setenv(key, old) })
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := filepath.Join(t.TempDir(), "nested", "config.yaml")
	_, s, err := EditConfig(p, true, map[string]string{"language": "it-IT", "voice": "kore"}, nil)
	if err != nil || s.Language != "it-IT" {
		t.Fatalf("set: %+v, %v", s, err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EditConfig(p, true, map[string]string{"jobs": "17", "language": "en-US"}, nil); err == nil {
		t.Fatal("invalid transaction succeeded")
	}
	after, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("file changed: %v", err)
	}
	if err := os.WriteFile(p+".lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EditConfig(p, true, nil, []string{"language"}); err == nil || Public(err).Code != "CONFIG_BUSY" {
		t.Fatalf("concurrent config writer was not rejected: %v", err)
	}
	if err := os.Remove(p + ".lock"); err != nil {
		t.Fatal(err)
	}
	_, s, err = EditConfig(p, true, nil, []string{"language"})
	if err != nil || s.Language != "en-US" {
		t.Fatalf("unset: %+v, %v", s, err)
	}
}

func TestConfigRejectsUnknownAndDuplicateKeys(t *testing.T) {
	for _, input := range []string{"google_credentials: secret", "jobs: 4\njobs: 5", "---\njobs: 4\n---\njobs: 5", "jobs: [4]"} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(p, true, false); err == nil {
			t.Fatalf("invalid YAML accepted: %s", input)
		}
	}
}

func TestGeminiStyleAndAutoLanguageConfig(t *testing.T) {
	c := Config{
		Stored:   map[string]string{"provider": ProviderGemini},
		env:      map[string]string{},
		defaults: map[string]string{"bitrate": "96", "format": FormatMP3, "provider": ProviderGoogle, "key_tier": KeyTierAuto, "voice": "random", "output_dir": t.TempDir(), "cache_dir": t.TempDir(), "jobs": "4"},
	}

	s, err := c.Resolve(nil, nil)
	if err != nil || s.LanguagePinned {
		t.Fatalf("expected unpinned default language, got %+v %v", s, err)
	}

	s, err = c.Resolve(map[string]string{"language": "it-IT", "style": "warm"}, nil)
	if err != nil || !s.LanguagePinned || s.Language != "it-IT" || s.Style != "warm" {
		t.Fatalf("expected pinned language and style, got %+v %v", s, err)
	}

	s, err = c.Resolve(map[string]string{"language": "auto"}, nil)
	if err != nil || s.Language != LanguageAuto || s.LanguagePinned {
		t.Fatalf("expected auto language without pinning, got %+v %v", s, err)
	}

	if _, err := c.Resolve(map[string]string{"provider": ProviderGoogle, "style": "warm"}, nil); err == nil {
		t.Fatal("expected style to be rejected for google provider")
	}
	if _, err := c.Resolve(map[string]string{"provider": ProviderGoogle, "language": "auto"}, nil); err == nil {
		t.Fatal("expected language=auto to be rejected for google provider")
	}
}

func TestInteractiveStyleClearingDoesNotMutateConfiguration(t *testing.T) {
	c := Config{
		Stored:   map[string]string{"style": "warm"},
		env:      map[string]string{"style": "whispered"},
		defaults: map[string]string{"provider": ProviderGemini, "model": DefaultGeminiModel, "key_tier": KeyTierAuto, "language": "en-US", "voice": "kore", "format": FormatWAV, "bitrate": "96", "jobs": "4", "output_dir": t.TempDir(), "cache_dir": t.TempDir()},
	}
	for _, tc := range []struct{ name, style, provider string }{
		{name: "clear Gemini", style: "", provider: ProviderGemini},
		{name: "clear for Google", style: "  ", provider: ProviderGoogle},
		{name: "replace style", style: "cheerful", provider: ProviderGemini},
	} {
		t.Run(tc.name, func(t *testing.T) {
			overrides := map[string]string{"style": tc.style, "provider": tc.provider}
			s, err := c.ResolveInteractive(overrides)
			if err != nil || s.Style != strings.TrimSpace(tc.style) {
				t.Fatalf("got %+v, %v", s, err)
			}
			if overrides["style"] != tc.style || c.Stored["style"] != "warm" || c.env["style"] != "whispered" {
				t.Fatal("resolver mutated input layers")
			}
		})
	}
	if _, err := c.Resolve(map[string]string{"style": ""}, nil); err == nil {
		t.Fatal("ordinary explicit blank style must remain invalid")
	}
}
