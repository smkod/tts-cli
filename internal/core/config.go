package core

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	FormatWAV = "wav"
	FormatMP3 = "mp3"
)

type Settings struct {
	Bitrate   int    `json:"bitrate" yaml:"bitrate"`
	Format    string `json:"format" yaml:"format"`
	Provider  string `json:"provider" yaml:"provider"`
	Language  string `json:"language" yaml:"language"`
	Voice     string `json:"voice" yaml:"voice"`
	OutputDir string `json:"output_dir" yaml:"output_dir"`
	CacheDir  string `json:"cache_dir" yaml:"cache_dir"`
	Jobs      int    `json:"jobs" yaml:"jobs"`
}

var Keys = []string{"provider", "language", "voice", "format", "output_dir", "cache_dir", "jobs", "bitrate"}

type Config struct {
	Path     string
	Stored   map[string]string
	defaults map[string]string
	env      map[string]string
}

func ConfigPath(flag string) (string, bool, error) {
	if flag != "" {
		p, e := filepath.Abs(flag)
		return p, true, e
	}
	if path, ok := os.LookupEnv("TTS_CONFIG"); ok {
		if path == "" {
			return "", true, Fail("INVALID_CONFIG", "TTS_CONFIG must not be empty.", false)
		}
		p, e := filepath.Abs(path)
		return p, true, e
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(dir, "tts", "config.yaml"), false, nil
}

func LoadConfig(path string, explicit, allowCreate bool) (Config, error) {
	c := Config{Path: path, Stored: map[string]string{}, env: map[string]string{}, defaults: map[string]string{
		"bitrate": "96", "format": FormatMP3, "provider": "google", "voice": "random", "output_dir": ".", "jobs": "4",
	}}
	if d, e := os.UserCacheDir(); e == nil {
		c.defaults["cache_dir"] = filepath.Join(d, "tts")
	}
	for _, k := range Keys {
		if v, ok := os.LookupEnv("TTS_" + strings.ToUpper(k)); ok {
			c.env[k] = v
		}
	}
	const maxConfig = 64 << 10
	b, err := regularData(path, maxConfig)
	if errors.Is(err, os.ErrNotExist) && (!explicit || allowCreate) {
		return c, nil
	}
	if err != nil {
		return c, Fail("INVALID_CONFIG", "Configuration must be a readable regular file of at most 64 KiB.", false)
	}
	d := yaml.NewDecoder(strings.NewReader(string(b)))
	var n yaml.Node
	if err = d.Decode(&n); errors.Is(err, io.EOF) {
		return c, nil
	}
	if err != nil || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode {
		return c, Fail("INVALID_CONFIG", "Configuration must be a YAML mapping.", false)
	}
	seen := map[string]bool{}
	for i := 0; i < len(n.Content[0].Content); i += 2 {
		k, v := n.Content[0].Content[i], n.Content[0].Content[i+1]
		if k.Tag != "!!str" || !validKey(k.Value) || seen[k.Value] || v.Kind != yaml.ScalarNode || (v.Tag != "!!str" && !((k.Value == "jobs" || k.Value == "bitrate") && v.Tag == "!!int")) {
			return c, Fail("INVALID_CONFIG", "Configuration contains an unknown, duplicate or invalid setting.", false)
		}
		seen[k.Value] = true
		c.Stored[k.Value] = v.Value
	}
	if !errors.Is(d.Decode(&n), io.EOF) {
		return c, Fail("INVALID_CONFIG", "Configuration must contain one YAML document.", false)
	}
	return c, nil
}

func validKey(key string) bool {
	for _, k := range Keys {
		if k == key {
			return true
		}
	}
	return false
}
func setting(k string, layers ...map[string]string) string {
	for _, m := range layers {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return ""
}

func (c Config) Resolve(flags, item map[string]string) (Settings, error) {
	return c.resolve(flags, item, c.env, c.Stored, c.defaults)
}
func (c Config) resolve(layers ...map[string]string) (Settings, error) {
	get := func(k string) string { return setting(k, layers...) }
	s := Settings{Format: strings.ToLower(get("format")), Provider: get("provider"), Language: get("language"), Voice: get("voice"), OutputDir: get("output_dir"), CacheDir: get("cache_dir")}
	if s.Format != FormatWAV && s.Format != FormatMP3 {
		return s, Fail("INVALID_CONFIG", "Format must be wav or mp3.", false)
	}
	if s.Provider != "google" {
		return s, Fail("INVALID_CONFIG", "The only supported provider is google.", false)
	}
	for _, k := range []string{"language", "voice"} {
		for _, m := range layers {
			if v, ok := m[k]; ok {
				if strings.TrimSpace(v) == "" {
					return s, Fail("INVALID_CONFIG", "Language and voice must not be blank.", false)
				}
				break
			}
		}
	}
	jobs, err := strconv.Atoi(get("jobs"))
	if err != nil || jobs < 1 || jobs > 16 {
		return s, Fail("INVALID_CONFIG", "Jobs must be an integer between 1 and 16.", false)
	}
	s.Jobs = jobs
	s.Bitrate, err = strconv.Atoi(get("bitrate"))
	if err != nil || !ValidBitrate(s.Bitrate) {
		return s, Fail("INVALID_CONFIG", "Bitrate must be a supported MP3 bitrate in kbps (8-320).", false)
	}
	if strings.TrimSpace(s.OutputDir) == "" || strings.TrimSpace(s.CacheDir) == "" {
		return s, Fail("INVALID_CONFIG", "Output and cache directories must not be blank.", false)
	}
	if s.OutputDir, err = filepath.Abs(s.OutputDir); err != nil {
		return s, Fail("INVALID_CONFIG", "Invalid output directory.", false)
	}
	if s.CacheDir, err = filepath.Abs(s.CacheDir); err != nil {
		return s, Fail("INVALID_CONFIG", "Invalid cache directory.", false)
	}
	if strings.EqualFold(s.Voice, "random") {
		voices := randomVoiceCandidates(s.Language)
		if len(voices) == 0 {
			return s, invalidVoice()
		}
		s.Language, s.Voice = voices[0].Language, "random"
		return s, nil
	}
	s.Language, s.Voice, err = ResolveVoice(s.Language, s.Voice)
	return s, err
}

// EditConfig validates both the stored defaults and the environment-overridden result.
func EditConfig(path string, explicit bool, set map[string]string, unset []string) (Config, Settings, error) {
	var c Config
	var s Settings
	fail := Fail("FILE_IO", "Cannot safely update the configuration file.", false)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return c, s, fail
	}
	lock := path + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return c, s, Fail("CONFIG_BUSY", "Configuration is locked by another writer.", true)
	}
	if err != nil {
		return c, s, fail
	}
	defer os.Remove(lock)
	if f.Close() != nil {
		return c, s, fail
	}
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) || info != nil && !info.Mode().IsRegular() {
		return c, s, fail
	}
	c, err = LoadConfig(path, explicit, true)
	if err != nil {
		return c, s, err
	}
	for k, v := range set {
		if !validKey(k) {
			return c, s, Fail("INVALID_CONFIG", "Unknown configuration key.", false)
		}
		c.Stored[k] = v
	}
	for _, k := range unset {
		if !validKey(k) {
			return c, s, Fail("INVALID_CONFIG", "Unknown configuration key.", false)
		}
		delete(c.Stored, k)
	}
	if _, err = c.resolve(c.Stored, c.defaults); err != nil {
		return c, s, err
	}
	if s, err = c.Resolve(nil, nil); err != nil {
		return c, s, err
	}
	data, err := yaml.Marshal(c.Stored)
	if err != nil || len(data) > 64<<10 {
		return c, s, fail
	}
	if err = publish(path, data, info != nil); err != nil {
		return c, s, fail
	}
	return c, s, nil
}
