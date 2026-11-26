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

func randomVoiceCandidates(lang string) []Voice {
	if lang == "" {
		lang = "en-US"
	}
	return LocalVoices(lang)
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

// GeminiVoiceName returns the short prebuilt voice name for Gemini TTS (e.g. "Kore").
func GeminiVoiceName(voice string) string {
	if v, ok := ParseVoice(voice); ok && len(v.Alias) > 0 {
		return strings.ToUpper(v.Alias[:1]) + v.Alias[1:]
	}
	return voice
}

// ValidLocale reports whether s is a valid BCP-47 locale tag such as en-US.
func ValidLocale(s string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(s))
	if !validLocale(lower) {
		return "", false
	}
	return canonicalLocale(lower), true
}

func invalidVoice() *Problem {
	return Fail("INVALID_VOICE", "Unknown Chirp 3 HD voice or locale. Check the voice list for supported aliases.", false)
}

// Turn represents one sequential segment in a multi-turn or multi-speaker Gemini TTS request.
type Turn struct {
	Text    string `json:"text"`
	Style   string `json:"style,omitempty"`
	Speaker string `json:"speaker,omitempty"`
}

// VoiceDesignRequest describes a prompt-based custom Gemini voice creation request.
type VoiceDesignRequest struct {
	Prompt       string
	DisplayName  string
	Gender       string
	LanguageCode string
	Model        string
	KeyTier      string
	Format       string
	Bitrate      int
	ConfigDir    string
	CacheDir     string
	Warn         func(string) `json:"-"`
}

// VoiceDesignResult describes a newly created custom Gemini voice and optional sample audio.
type VoiceDesignResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name,omitempty"`
	Model        string `json:"model"`
	LanguageCode string `json:"language_code,omitempty"`
	Gender       string `json:"gender,omitempty"`
	ExpireTime   string `json:"expire_time,omitempty"`
	SampleFile   string `json:"sample_file,omitempty"`
	SampleAudio  []byte `json:"-"`
}

type Request struct {
	Text, Provider, Model, KeyTier, Language, Voice, Format string
	Style                                                   string
	LanguagePinned                                          bool
	Turns                                                   []Turn
	Speakers                                                map[string]string
	ConfigDir, CacheDir                                     string
	Bitrate                                                 int
	Warn                                                    func(string) `json:"-"`
}

const (
	MP3SampleRateHz = 48_000
	MP3BitrateKbps  = 96
)

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
	Bitrate      int      `json:"bitrate,omitempty"`
	Format       string   `json:"format"`
	File         string   `json:"file"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model,omitempty"`
	Voice        string   `json:"voice"`
	Language     string   `json:"language"`
	Style        string   `json:"style,omitempty"`
	Turns        int      `json:"turns,omitempty"`
	Speakers     []string `json:"speakers,omitempty"`
	DetectedTags []string `json:"detected_tags,omitempty"`
	Cached       bool     `json:"cached"`
	Characters   int      `json:"characters"`
	DryRun       bool     `json:"dry_run,omitempty"`
	CacheStatus  string   `json:"cache_status"`
}
type Options struct {
	Output          string
	NoCache, DryRun bool
	Turns           []Turn
	Speakers        map[string]string
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
	family := "Chirp3-HD"
	if r.Provider == ProviderGemini {
		family = r.Model
	}
	fields := []any{"v1", r.Text, r.Provider, family, r.Voice, r.Language, r.Format}
	if r.Format == FormatMP3 {
		bitrate := r.Bitrate
		if bitrate == 0 {
			bitrate = MP3BitrateKbps
		}
		fields = append(fields, "ffmpeg-libmp3lame-v1", bitrate)
	}
	if r.Style != "" {
		fields = append(fields, "style", r.Style)
	}
	if r.LanguagePinned && r.Provider == ProviderGemini {
		fields = append(fields, "lang_pinned", true)
	}
	if len(r.Turns) > 0 {
		fields = append(fields, "turns", r.Turns)
	}
	if len(r.Speakers) > 0 {
		keys := make([]string, 0, len(r.Speakers))
		for k := range r.Speakers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([][2]string, len(keys))
		for i, k := range keys {
			pairs[i] = [2]string{k, r.Speakers[k]}
		}
		fields = append(fields, "speakers", pairs)
	}
	b, _ := json.Marshal(fields)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func resolveSingleGeminiVoice(lang, choice string) (string, error) {
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", Fail("INVALID_VOICE", "Speaker voice must not be blank.", false)
	}
	if strings.EqualFold(choice, "random") {
		poolLang := lang
		if poolLang == "" || poolLang == LanguageAuto {
			poolLang = "en-US"
		}
		voices := randomVoiceCandidates(poolLang)
		if len(voices) == 0 {
			voices = randomVoiceCandidates("en-US")
		}
		if len(voices) == 0 {
			return "", invalidVoice()
		}
		return GeminiVoiceName(voices[rand.IntN(len(voices))].Name), nil
	}
	poolLang := lang
	if poolLang == LanguageAuto {
		poolLang = ""
	}
	if _, v, err := ResolveVoice(poolLang, choice); err == nil {
		return GeminiVoiceName(v), nil
	}
	return choice, nil
}

func (e *Engine) Generate(ctx context.Context, s Settings, text string, o Options) (Generation, error) {
	isScript := len(o.Turns) > 0
	if !isScript {
		if err := ValidateText(text); err != nil {
			return Generation{}, err
		}
	} else if text != "" {
		return Generation{}, Fail("INVALID_INPUT", "Provide either plain text or structured turns, not both.", false)
	}
	if s.Provider != ProviderGemini {
		if s.Style != "" {
			return Generation{}, Fail("INVALID_CONFIG", "Style direction is only supported with the gemini provider.", false)
		}
		if isScript || len(o.Speakers) > 0 {
			return Generation{}, Fail("INVALID_INPUT", "Multi-turn and multi-speaker scripts are only supported with the gemini provider.", false)
		}
		if s.Language == LanguageAuto {
			return Generation{}, Fail("INVALID_CONFIG", "Automatic language detection (auto) is only supported with the gemini provider.", false)
		}
	} else if len(o.Speakers) > 0 && !isScript {
		return Generation{}, Fail("INVALID_INPUT", "Multi-speaker mapping requires structured turns.", false)
	}

	var (
		turns        []Turn
		speakers     map[string]string
		speakerNames []string
		detectedTags []string
		seenTags     = map[string]bool{}
		totalBytes   int
		totalChars   int
	)
	addTags := func(tags []string) {
		for _, t := range tags {
			if !seenTags[t] {
				seenTags[t] = true
				detectedTags = append(detectedTags, t)
			}
		}
	}

	if len(o.Speakers) > 0 {
		if len(o.Speakers) > 2 {
			return Generation{}, Fail("INVALID_INPUT", "Gemini multi-speaker synthesis supports at most 2 speakers.", false)
		}
		speakers = make(map[string]string, len(o.Speakers))
		for rawAlias, rawVoice := range o.Speakers {
			alias := strings.TrimSpace(rawAlias)
			if alias == "" {
				return Generation{}, Fail("INVALID_INPUT", "Speaker aliases must not be blank.", false)
			}
			if _, dup := speakers[alias]; dup {
				return Generation{}, Fail("INVALID_INPUT", "Duplicate speaker alias after trimming whitespace.", false)
			}
			resolvedVoice, err := resolveSingleGeminiVoice(s.Language, rawVoice)
			if err != nil {
				return Generation{}, err
			}
			speakers[alias] = resolvedVoice
			speakerNames = append(speakerNames, alias)
		}
		sort.Strings(speakerNames)
		var voiceParts []string
		for _, alias := range speakerNames {
			voiceParts = append(voiceParts, alias+"="+speakers[alias])
		}
		s.Voice = strings.Join(voiceParts, ",")
	} else if s.Voice == "random" {
		poolLang := s.Language
		if poolLang == LanguageAuto {
			poolLang = "en-US"
		}
		voices := randomVoiceCandidates(poolLang)
		if len(voices) == 0 {
			return Generation{}, invalidVoice()
		}
		v := voices[rand.IntN(len(voices))]
		if s.Language != LanguageAuto {
			s.Language = v.Language
		}
		s.Voice = v.Name
		if s.Provider == ProviderGemini {
			s.Voice = GeminiVoiceName(v.Name)
		}
	}

	if isScript {
		turns = make([]Turn, len(o.Turns))
		for i, t := range o.Turns {
			if err := ValidateText(t.Text); err != nil {
				return Generation{}, err
			}
			totalBytes += len(t.Text)
			if totalBytes > MaxTextBytes {
				return Generation{}, Fail("INVALID_TEXT", "Combined turns text must be no more than 5,000 UTF-8 bytes.", false)
			}
			tags, err := ValidateInlineModifiers(s.Provider, t.Text)
			if err != nil {
				return Generation{}, err
			}
			addTags(tags)
			totalChars += utf8.RuneCountInString(t.Text)
			style := strings.TrimSpace(t.Style)
			if style == "" {
				style = s.Style
			}
			speaker := strings.TrimSpace(t.Speaker)
			if len(speakers) > 0 {
				if speaker == "" {
					return Generation{}, Fail("INVALID_INPUT", "Every turn in a multi-speaker script must specify a speaker.", false)
				}
				if _, ok := speakers[speaker]; !ok {
					return Generation{}, Fail("INVALID_INPUT", fmt.Sprintf("Turn references unknown speaker %q.", speaker), false)
				}
			} else if speaker != "" {
				return Generation{}, Fail("INVALID_INPUT", "Turn specifies a speaker, but no speakers mapping was provided.", false)
			}
			turns[i] = Turn{Text: t.Text, Style: style, Speaker: speaker}
		}
	} else {
		tags, err := ValidateInlineModifiers(s.Provider, text)
		if err != nil {
			return Generation{}, err
		}
		addTags(tags)
		totalChars = utf8.RuneCountInString(text)
	}

	r := Request{
		Text: text, Provider: s.Provider, Model: s.Model, KeyTier: s.KeyTier,
		Language: s.Language, LanguagePinned: s.LanguagePinned, Voice: s.Voice,
		Style: s.Style, Turns: turns, Speakers: speakers,
		Format: s.Format, Bitrate: s.Bitrate,
		ConfigDir: s.ConfigDir, CacheDir: s.CacheDir, Warn: e.Warn,
	}
	if r.Format != FormatWAV && r.Format != FormatMP3 {
		return Generation{}, Fail("INVALID_CONFIG", "Format must be wav or mp3.", false)
	}
	ext := "." + r.Format
	key := cacheKey(r)
	out := o.Output
	if out == "" {
		out = filepath.Join(s.OutputDir, key+ext)
	}
	out, err := filepath.Abs(out)
	if err != nil || !strings.EqualFold(filepath.Ext(out), ext) {
		return Generation{}, Fail("INVALID_INPUT", "Output path must end in "+ext+" to match the selected format.", false)
	}
	p := filepath.Join(s.CacheDir, "v1", key[:2], key+".entry")
	if samePath(out, p) {
		return Generation{}, Fail("INVALID_INPUT", "Output conflicts with the cache entry.", false)
	}
	result := Generation{
		Format: r.Format, File: out, Provider: r.Provider, Model: r.Model, Voice: r.Voice,
		Language: r.Language, Style: r.Style, Turns: len(turns), Speakers: speakerNames,
		DetectedTags: detectedTags, Characters: totalChars, DryRun: o.DryRun,
	}
	if r.Format == FormatMP3 {
		result.Bitrate = r.Bitrate
		if result.Bitrate == 0 {
			result.Bitrate = MP3BitrateKbps
		}
	}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	if o.NoCache {
		result.CacheStatus = "bypassed"
	} else if o.DryRun {
		_, status := readCache(p, r.Format)
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
	p.data, status = readCache(path, r.Format)
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
		if data, s := readCache(path, r.Format); s == "hit" {
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
			if !validAudio(data, r.Format) {
				return nil, Fail("INVALID_AUDIO", "Google returned invalid or incomplete audio for the selected format.", false)
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
