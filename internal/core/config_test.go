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
		Stored:   map[string]string{"language": "fr-FR", "jobs": "8"},
		env:      map[string]string{"language": "de-DE"},
		defaults: map[string]string{"provider": "google", "voice": "kore", "output_dir": t.TempDir(), "cache_dir": t.TempDir(), "jobs": "4"},
	}
	for _, tc := range []struct {
		name        string
		flags, item map[string]string
		lang        string
	}{
		{"flag wins", map[string]string{"language": "it-IT"}, map[string]string{"language": "en-US"}, "it-IT"},
		{"item wins", nil, map[string]string{"language": "en-US"}, "en-US"},
		{"environment wins", nil, nil, "de-DE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := c.Resolve(tc.flags, tc.item)
			if err != nil || s.Language != tc.lang || s.Jobs != 8 {
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
