package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentGuide(t *testing.T) {
	t.Setenv("TTS_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	for _, asJSON := range []bool{false, true} {
		name := "markdown"
		args := []string{"agent-guide"}
		if asJSON {
			name = "json"
			args = append(args, "--json")
		}
		t.Run(name, func(t *testing.T) {
			backend := &mockBackend{}
			code, out, diag := runTest(backend, args...)
			if code != 0 || diag != "" {
				t.Fatalf("code=%d diagnostics=%s", code, diag)
			}
			guide := out
			if asJSON {
				var result struct {
					Version int  `json:"schema_version"`
					Success bool `json:"success"`
					Data    struct {
						Version      int    `json:"guide_version"`
						Format       string `json:"format"`
						Instructions string `json:"instructions"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if !result.Success || result.Version != 1 || result.Data.Version != 1 || result.Data.Format != "markdown" {
					t.Fatalf("unexpected envelope: %s", out)
				}
				guide = result.Data.Instructions
			}
			if strings.TrimSpace(guide) != strings.TrimSpace(agentGuideText) {
				t.Fatal("guide differs from bundled source")
			}
			for _, required := range []string{"capabilities --json", "--dry-run", "data.file", "error.retryable", "batch items.json"} {
				if !strings.Contains(guide, required) {
					t.Errorf("missing guidance: %s", required)
				}
			}
			if len(backend.requests) != 0 || backend.lists != 0 {
				t.Fatal("guide called the backend")
			}
		})
	}
}

func TestAgentGuideDiscoveryAndArguments(t *testing.T) {
	code, out, _ := runTest(&mockBackend{}, "--help")
	if code != 0 || !strings.Contains(out, "agent-guide") {
		t.Fatal("root help does not expose agent-guide")
	}
	code, out, _ = runTest(&mockBackend{}, "agent-guide", "unexpected", "--json")
	if code != 2 || !strings.Contains(out, "INVALID_USAGE") {
		t.Fatalf("unexpected argument handling: %d %s", code, out)
	}
}
