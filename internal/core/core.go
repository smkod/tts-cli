package core

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Problem struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (p *Problem) Error() string                 { return p.Message }
func Fail(code, msg string, retry bool) *Problem { return &Problem{code, msg, retry} }
func Public(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	if errors.Is(err, context.Canceled) {
		return Fail("CANCELED", "The operation was canceled.", false)
	}
	return Fail("INTERNAL", "The operation failed unexpectedly.", false)
}

const CatalogDate = "2026-11-17"
const MaxTextBytes = 5000

//go:embed chirp3-voices.txt
var catalog string

type Voice struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Alias    string `json:"alias"`
}

func ParseVoice(name string) (Voice, bool) {
	lang, alias, ok := strings.Cut(name, "-Chirp3-HD-")
	if !ok || lang == "" || alias == "" {
		return Voice{}, false
	}
	return Voice{name, lang, strings.ToLower(alias)}, true
}

var localVoices, voiceByName = loadVoices()

func loadVoices() ([]Voice, map[string]Voice) {
	vs := []Voice{}
	byName := map[string]Voice{}
	for _, name := range strings.Fields(catalog) {
		if v, ok := ParseVoice(name); ok {
			vs = append(vs, v)
			byName[strings.ToLower(name)] = v
		}
	}
	SortVoices(vs)
	return vs, byName
}

func SortVoices(vs []Voice) { sort.Slice(vs, func(i, j int) bool { return vs[i].Name < vs[j].Name }) }
func LocalVoices(lang string) []Voice {
	out := []Voice{}
	for _, v := range localVoices {
		if lang == "" || strings.EqualFold(lang, v.Language) {
			out = append(out, v)
		}
	}
	return out
}
func Languages() []string {
	langs := []string{}
	seen := map[string]bool{}
	for _, v := range localVoices {
		if !seen[v.Language] {
			seen[v.Language] = true
			langs = append(langs, v.Language)
		}
	}
	sort.Strings(langs)
	return langs
}

func ResolveVoice(lang, choice string) (string, string, error) {
	id := strings.ToLower(choice)
	if strings.Contains(id, "-chirp3-hd-") {
		v, ok := voiceByName[id]
		if !ok {
			// A well-formed new ID can be discovered via the live catalog before a CLI update.
			parts := strings.Split(id, "-chirp3-hd-")
			if len(parts) != 2 || !validLocale(parts[0]) || !letters(parts[1]) {
				return "", "", invalidVoice()
			}
			locale := canonicalLocale(parts[0])
			v = Voice{locale + "-Chirp3-HD-" + strings.ToUpper(parts[1][:1]) + parts[1][1:], locale, parts[1]}
		}
		if lang != "" && !strings.EqualFold(lang, v.Language) {
			return "", "", Fail("INVALID_VOICE", "The full voice ID conflicts with the selected language.", false)
		}
		return v.Language, v.Name, nil
	}
	if lang == "" {
		lang = "en-US"
	}
	v, ok := voiceByName[strings.ToLower(lang)+"-chirp3-hd-"+id]
	if !ok {
		return "", "", invalidVoice()
	}
	return v.Language, v.Name, nil
}

func letters(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
func validLocale(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 2 || len(parts) > 3 || (len(parts[0]) != 2 && len(parts[0]) != 3) {
		return false
	}
	for i, p := range parts {
		if !letters(p) || (i > 0 && len(p) != 2 && len(p) != 4) {
			return false
		}
	}
	return true
}
func canonicalLocale(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) == 2 {
			parts[i] = strings.ToUpper(parts[i])
		} else {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "-")
}
func invalidVoice() *Problem {
	return Fail("INVALID_VOICE", "Unknown Chirp 3 HD voice or locale. Check the voice list for supported aliases.", false)
}

type Request struct{ Text, Provider, Language, Voice string }
type Provider interface {
	Synthesize(context.Context, Request) ([]byte, error)
}
type VoiceLister interface {
	ListVoices(context.Context, string) ([]Voice, error)
}
type Backend interface {
	Provider
	VoiceLister
	Close() error
}
type Generation struct {
	File        string `json:"file"`
	Provider    string `json:"provider"`
	Voice       string `json:"voice"`
	Language    string `json:"language"`
	Cached      bool   `json:"cached"`
	Characters  int    `json:"characters"`
	DryRun      bool   `json:"dry_run,omitempty"`
	CacheStatus string `json:"cache_status"`
}
type Options struct {
	Output          string
	NoCache, DryRun bool
}
type pending struct {
	done   chan struct{}
	data   []byte
	cached bool
	err    error
}
type Engine struct {
	Provider Provider
	Warn     func(string)
	Wait     func(context.Context, time.Duration) error
	mu       sync.Mutex
	pending  map[string]*pending
}

func ValidateText(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || len(text) > MaxTextBytes {
		return Fail("INVALID_TEXT", "Text must be nonblank UTF-8 and no more than 5,000 bytes.", false)
	}
	return nil
}
func cacheKey(r Request) string {
	b, _ := json.Marshal([]string{"v1", r.Text, r.Provider, "Chirp3-HD", r.Voice, r.Language, "mp3"})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (e *Engine) Generate(ctx context.Context, s Settings, text string, o Options) (Generation, error) {
	if err := ValidateText(text); err != nil {
		return Generation{}, err
	}
	r := Request{text, s.Provider, s.Language, s.Voice}
	key := cacheKey(r)
	out := o.Output
	if out == "" {
		out = filepath.Join(s.OutputDir, key+".mp3")
	}
	out, err := filepath.Abs(out)
	if err != nil || !strings.EqualFold(filepath.Ext(out), ".mp3") {
		return Generation{}, Fail("INVALID_INPUT", "Output must be an MP3 file path.", false)
	}
	p := filepath.Join(s.CacheDir, "v1", key[:2], key+".entry")
	if samePath(out, p) {
		return Generation{}, Fail("INVALID_INPUT", "Output conflicts with the cache entry.", false)
	}
	result := Generation{File: out, Provider: r.Provider, Voice: r.Voice, Language: r.Language, Characters: utf8.RuneCountInString(text), DryRun: o.DryRun}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	if o.NoCache {
		result.CacheStatus = "bypassed"
	} else if o.DryRun {
		_, status := readCache(p)
		result.CacheStatus = status
		result.Cached = status == "hit"
	}
	if o.DryRun {
		return result, nil
	}
	if err := checkOutput(out); err != nil {
		return Generation{}, err
	}
	var data []byte
	if o.NoCache {
		data, err = e.withRetry(ctx, r)
	} else {
		work := e.cachedAudio(ctx, r, p)
		data, result.Cached, err = work.data, work.cached, work.err
		result.CacheStatus = "miss"
		if result.Cached {
			result.CacheStatus = "hit"
		}
	}
	if err != nil {
		return Generation{}, err
	}
	if err = ctx.Err(); err != nil {
		return Generation{}, err
	}
	if err = writeOutput(out, data); err != nil {
		return Generation{}, err
	}
	return result, nil
}

func (e *Engine) cachedAudio(ctx context.Context, r Request, path string) *pending {
	e.mu.Lock()
	if e.pending == nil {
		e.pending = map[string]*pending{}
	}
	if p, ok := e.pending[path]; ok {
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return &pending{err: ctx.Err()}
		case <-p.done:
			return p
		}
	}
	p := &pending{done: make(chan struct{})}
	e.pending[path] = p
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.pending, path); close(p.done); e.mu.Unlock() }()
	var status string
	p.data, status = readCache(path)
	if status == "hit" {
		p.cached = true
		return p
	}
	if status == "corrupt" && e.Warn != nil {
		e.Warn("Ignoring a corrupt cache entry; remove it to restore caching for this request.")
	}
	p.data, p.err = e.withRetry(ctx, r)
	if p.err != nil || status == "corrupt" {
		return p
	}
	if err := storeCache(path, p.data); err != nil {
		if data, s := readCache(path); s == "hit" {
			p.data = data
			p.cached = true
		} else if e.Warn != nil {
			e.Warn("Could not save audio to the cache.")
		}
	}
	return p
}

func (e *Engine) withRetry(ctx context.Context, r Request) ([]byte, error) {
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		data, err := e.Provider.Synthesize(attempt, r)
		cancel()
		if err == nil {
			if !validMP3(data) {
				return nil, Fail("INVALID_AUDIO", "Google returned invalid or incomplete MP3 audio.", false)
			}
			return data, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if i >= 2 || !Public(err).Retryable {
			return nil, err
		}
		base := 250 * time.Millisecond << i
		delay := base + time.Duration(rand.Int64N(int64(base)))
		wait := e.Wait
		if wait == nil {
			wait = waitContext
		}
		if err := wait(ctx, delay); err != nil {
			return nil, err
		}
	}
}
func waitContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func FormatFile(g Generation) string {
	if g.DryRun {
		return fmt.Sprintf("%s (dry run; cache: %s)", g.File, g.CacheStatus)
	}
	if g.Cached {
		return g.File + " (cached)"
	}
	return g.File
}
