package geminitts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/smkod/tts-cli/internal/core"
)

const (
	TierFree = core.KeyTierFree
	TierPaid = core.KeyTierPaid
)

// KeyEntry represents a configured Gemini API key and its billing tier.
type KeyEntry struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Key    string `json:"key"`
	Source string `json:"source,omitempty"`
}

type storedKeysFile struct {
	Keys []KeyEntry `json:"keys"`
}

// KeysPath returns the path to the Gemini API keys file for a config directory.
func KeysPath(configDir string) string {
	if p := strings.TrimSpace(os.Getenv("TTS_GEMINI_KEYS_FILE")); p != "" {
		return p
	}
	if configDir == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			configDir = filepath.Join(dir, "tts")
		}
	}
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "gemini-keys.json")
}

// KeyID returns a non-reversible identifier for a key, safe for state files and logs.
func KeyID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// MaskKey returns a redacted representation of an API key for display.
func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// LoadKeys loads Gemini API keys from the keys file and environment variables.
// Keys are deduplicated by secret value while preserving priority order.
func LoadKeys(configDir string) ([]KeyEntry, error) {
	var all []KeyEntry
	seen := map[string]bool{}
	appendUnique := func(e KeyEntry) {
		e.Key = strings.TrimSpace(e.Key)
		if e.Key == "" || seen[e.Key] {
			return
		}
		seen[e.Key] = true
		if e.Tier != TierPaid {
			e.Tier = TierFree
		}
		all = append(all, e)
	}

	if path := KeysPath(configDir); path != "" {
		fileKeys, err := readKeysFile(path)
		if err != nil {
			return nil, err
		}
		for _, k := range fileKeys {
			k.Source = "file"
			appendUnique(k)
		}
	}

	for _, spec := range []struct {
		env  string
		tier string
		pfx  string
	}{
		{"TTS_GEMINI_FREE_KEYS", TierFree, "env-free"},
		{"TTS_GEMINI_PAID_KEYS", TierPaid, "env-paid"},
		{"GEMINI_API_KEYS", TierFree, "env"},
		{"GEMINI_API_KEY", TierFree, "env"},
	} {
		raw := strings.TrimSpace(os.Getenv(spec.env))
		if raw == "" {
			continue
		}
		parts := strings.Split(raw, ",")
		for i, part := range parts {
			k := strings.TrimSpace(part)
			if k == "" {
				continue
			}
			name := spec.pfx
			if len(parts) > 1 {
				name = fmt.Sprintf("%s-%d", spec.pfx, i+1)
			}
			appendUnique(KeyEntry{Name: name, Tier: spec.tier, Key: k, Source: "env"})
		}
	}

	return all, nil
}

// AddKey saves a Gemini API key to the keys file under the specified tier.
func AddKey(configDir, name, tier, apiKey string) (KeyEntry, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "API key must not be empty.", false)
	}
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier != TierFree && tier != TierPaid {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Key tier must be free or paid.", false)
	}
	path := KeysPath(configDir)
	if path == "" {
		return KeyEntry{}, core.Fail("FILE_IO", "Cannot determine the keys file path.", false)
	}
	existing, err := readKeysFile(path)
	if err != nil {
		return KeyEntry{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = nextKeyName(existing, tier)
	}
	for _, k := range existing {
		if k.Key == apiKey {
			return KeyEntry{}, core.Fail("INVALID_INPUT", "This API key is already saved.", false)
		}
		if strings.EqualFold(k.Name, name) {
			return KeyEntry{}, core.Fail("INVALID_INPUT", "A key with that name already exists.", false)
		}
	}
	entry := KeyEntry{Name: name, Tier: tier, Key: apiKey}
	existing = append(existing, entry)
	if err := writePrivateJSON(path, storedKeysFile{Keys: existing}); err != nil {
		return KeyEntry{}, err
	}
	entry.Source = "file"
	return entry, nil
}

// RemoveKey removes a saved Gemini API key by name or 1-based index.
func RemoveKey(configDir, target string) (KeyEntry, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Specify a key name or index to remove.", false)
	}
	path := KeysPath(configDir)
	if path == "" {
		return KeyEntry{}, core.Fail("FILE_IO", "Cannot determine the keys file path.", false)
	}
	existing, err := readKeysFile(path)
	if err != nil {
		return KeyEntry{}, err
	}
	idx := findKeyIndex(existing, target)
	if idx == -1 {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Key not found.", false)
	}
	removed := existing[idx]
	updated := append(existing[:idx], existing[idx+1:]...)
	if err := writePrivateJSON(path, storedKeysFile{Keys: updated}); err != nil {
		return KeyEntry{}, err
	}
	removed.Source = "file"
	return removed, nil
}

// UpdateKey renames and/or changes the free/paid tier of a saved Gemini API key.
func UpdateKey(configDir, target, newName, newTier string) (KeyEntry, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Specify a key name or index to update.", false)
	}
	newName = strings.TrimSpace(newName)
	newTier = strings.ToLower(strings.TrimSpace(newTier))
	if newName == "" && newTier == "" {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Specify a new name or tier.", false)
	}
	if newTier != "" && newTier != TierFree && newTier != TierPaid {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Key tier must be free or paid.", false)
	}
	path := KeysPath(configDir)
	if path == "" {
		return KeyEntry{}, core.Fail("FILE_IO", "Cannot determine the keys file path.", false)
	}
	existing, err := readKeysFile(path)
	if err != nil {
		return KeyEntry{}, err
	}
	idx := findKeyIndex(existing, target)
	if idx == -1 {
		return KeyEntry{}, core.Fail("INVALID_INPUT", "Key not found.", false)
	}
	if newName != "" {
		for i, k := range existing {
			if i != idx && strings.EqualFold(k.Name, newName) {
				return KeyEntry{}, core.Fail("INVALID_INPUT", "A key with that name already exists.", false)
			}
		}
		existing[idx].Name = newName
	}
	if newTier != "" {
		existing[idx].Tier = newTier
	}
	if err := writePrivateJSON(path, storedKeysFile{Keys: existing}); err != nil {
		return KeyEntry{}, err
	}
	updated := existing[idx]
	updated.Source = "file"
	return updated, nil
}

func findKeyIndex(existing []KeyEntry, target string) int {
	for i, k := range existing {
		if strings.EqualFold(k.Name, target) {
			return i
		}
	}
	if n, err := strconv.Atoi(target); err == nil && n >= 1 && n <= len(existing) {
		return n - 1
	}
	return -1
}

func nextKeyName(existing []KeyEntry, tier string) string {
	used := map[string]bool{}
	for _, k := range existing {
		used[strings.ToLower(k.Name)] = true
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d", tier, i)
		if !used[candidate] {
			return candidate
		}
	}
}

func readKeysFile(path string) ([]KeyEntry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, core.Fail("FILE_IO", "Cannot read Gemini keys file.", false)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	var doc storedKeysFile
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, core.Fail("INVALID_CONFIG", "Gemini keys file is not valid JSON.", false)
	}
	return doc.Keys, nil
}

func writePrivateJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return core.Fail("FILE_IO", "Cannot create configuration directory.", false)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return core.Fail("FILE_IO", "Cannot encode JSON data.", false)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tts-tmp-*")
	if err != nil {
		return core.Fail("FILE_IO", "Cannot write private file.", false)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return core.Fail("FILE_IO", "Cannot set private file permissions.", false)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return core.Fail("FILE_IO", "Cannot write private file.", false)
	}
	if err := tmp.Close(); err != nil {
		return core.Fail("FILE_IO", "Cannot close private file.", false)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return core.Fail("FILE_IO", "Cannot save private file.", false)
	}
	return nil
}
