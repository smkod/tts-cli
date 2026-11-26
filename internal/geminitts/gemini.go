package geminitts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/smkod/tts-cli/internal/core"
	"github.com/smkod/tts-cli/internal/googletts"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Gemini implements core.Backend using the Gemini 3.8 TTS REST API with multi-key failover.
type Gemini struct {
	BaseURL    string
	HTTPClient *http.Client
	Pool       *KeyPool
}

// New creates a Gemini TTS backend with default HTTP client and key pool.
func New() *Gemini {
	return &Gemini{
		BaseURL:    defaultBaseURL,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Pool:       NewKeyPool(),
	}
}

func (g *Gemini) Close() error { return nil }

func (g *Gemini) baseURL() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/")
	}
	return defaultBaseURL
}

func (g *Gemini) httpClient() *http.Client {
	if g.HTTPClient != nil {
		return g.HTTPClient
	}
	return http.DefaultClient
}

func (g *Gemini) pool() *KeyPool {
	if g.Pool != nil {
		return g.Pool
	}
	return NewKeyPool()
}

type generateContentRequest struct {
	Contents         []requestContent        `json:"contents"`
	GenerationConfig requestGenerationConfig `json:"generationConfig"`
}

type requestContent struct {
	Role  string        `json:"role,omitempty"`
	Parts []requestPart `json:"parts"`
}

type requestPart struct {
	Text           string                 `json:"text"`
	SpeechMetadata *requestSpeechMetadata `json:"speechMetadata,omitempty"`
}

type requestSpeechMetadata struct {
	Style   string `json:"style,omitempty"`
	Speaker string `json:"speaker,omitempty"`
}

type requestGenerationConfig struct {
	ResponseModalities []string            `json:"responseModalities"`
	SpeechConfig       requestSpeechConfig `json:"speechConfig"`
}

type requestSpeechConfig struct {
	LanguageCode            string                          `json:"languageCode,omitempty"`
	VoiceConfig             *requestVoiceConfig             `json:"voiceConfig,omitempty"`
	MultiSpeakerVoiceConfig *requestMultiSpeakerVoiceConfig `json:"multiSpeakerVoiceConfig,omitempty"`
}

type requestMultiSpeakerVoiceConfig struct {
	SpeakerVoiceConfigs []requestSpeakerVoiceConfig `json:"speakerVoiceConfigs"`
}

type requestSpeakerVoiceConfig struct {
	Speaker     string             `json:"speaker"`
	VoiceConfig requestVoiceConfig `json:"voiceConfig"`
}

type requestVoiceConfig struct {
	PrebuiltVoiceConfig requestPrebuiltVoice `json:"prebuiltVoiceConfig"`
}

type requestPrebuiltVoice struct {
	VoiceName string `json:"voiceName"`
}

type createVoiceRequest struct {
	Store bool               `json:"store"`
	Voice createVoicePayload `json:"voice"`
}

type createVoicePayload struct {
	Model        string              `json:"model"`
	Type         string              `json:"type"`
	DisplayName  string              `json:"displayName,omitempty"`
	Gender       string              `json:"gender,omitempty"`
	LanguageCode string              `json:"languageCode,omitempty"`
	Prompted     createVoicePrompted `json:"prompted"`
}

type createVoicePrompted struct {
	Input string `json:"input"`
}

type createVoiceResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Model        string `json:"model"`
	LanguageCode string `json:"languageCode"`
	Gender       string `json:"gender"`
	ExpireTime   string `json:"expireTime"`
	SampleAudio  *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"sampleAudio"`
}

type generateContentResponse struct {
	UsageMetadata *struct {
		PromptTokenCount     *int64 `json:"promptTokenCount"`
		CandidatesTokenCount *int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

type apiErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type       string `json:"@type"`
			Reason     string `json:"reason"`
			RetryDelay string `json:"retryDelay"`
		} `json:"details"`
	} `json:"error"`
}

// Synthesize generates speech via Gemini 3.8 TTS, failing over across configured keys when limits are hit.
func (g *Gemini) Synthesize(ctx context.Context, r core.Request) ([]byte, error) {
	var encoder string
	if r.Format == core.FormatMP3 {
		var err error
		encoder, err = googletts.FindFFmpeg(ctx)
		if err != nil {
			return nil, err
		}
	}

	model := r.Model
	if model == "" {
		model = core.DefaultGeminiModel
	}
	payload := buildGenerateContentRequest(r)

	var wav []byte
	err := g.withKeyFailover(ctx, r.ConfigDir, r.CacheDir, r.KeyTier, r.Warn, func(apiKey string) (int, time.Duration, string, error) {
		var status int
		var retryAfter time.Duration
		var reason string
		var reqErr error
		wav, status, retryAfter, reason, reqErr = g.callGenerateContent(ctx, apiKey, model, payload, r.ReportUsage)
		return status, retryAfter, reason, reqErr
	})
	if err != nil {
		return nil, err
	}
	if r.Format == core.FormatMP3 {
		return googletts.EncodeMP3(ctx, encoder, wav, r.Bitrate)
	}
	return wav, nil
}

// DesignVoice creates a reusable prompted custom voice via the Gemini Voices API.
func (g *Gemini) DesignVoice(ctx context.Context, r core.VoiceDesignRequest) (core.VoiceDesignResult, error) {
	var encoder string
	if r.Format == core.FormatMP3 {
		var err error
		encoder, err = googletts.FindFFmpeg(ctx)
		if err != nil {
			return core.VoiceDesignResult{}, err
		}
	}
	model := r.Model
	if model == "" {
		model = core.DefaultGeminiModel
	}
	reqBody := createVoiceRequest{
		Store: true,
		Voice: createVoicePayload{
			Model:        model,
			Type:         "prompted",
			DisplayName:  r.DisplayName,
			Gender:       strings.ToUpper(r.Gender),
			LanguageCode: r.LanguageCode,
			Prompted:     createVoicePrompted{Input: r.Prompt},
		},
	}

	var parsed createVoiceResponse
	err := g.withKeyFailover(ctx, r.ConfigDir, r.CacheDir, r.KeyTier, r.Warn, func(apiKey string) (int, time.Duration, string, error) {
		var status int
		var retryAfter time.Duration
		var reason string
		var reqErr error
		parsed, status, retryAfter, reason, reqErr = g.callCreateVoice(ctx, apiKey, reqBody)
		return status, retryAfter, reason, reqErr
	})
	if err != nil {
		return core.VoiceDesignResult{}, err
	}

	id := parsed.ID
	if id == "" {
		id = strings.TrimPrefix(parsed.Name, "voices/")
	}
	name := parsed.Name
	if name == "" && id != "" {
		name = "voices/" + id
	}
	res := core.VoiceDesignResult{
		ID:           id,
		Name:         name,
		DisplayName:  parsed.DisplayName,
		Model:        parsed.Model,
		LanguageCode: parsed.LanguageCode,
		Gender:       strings.ToLower(parsed.Gender),
		ExpireTime:   parsed.ExpireTime,
	}
	if res.Model == "" {
		res.Model = model
	}
	if parsed.SampleAudio != nil && parsed.SampleAudio.Data != "" {
		raw, decErr := base64.StdEncoding.DecodeString(parsed.SampleAudio.Data)
		if decErr != nil {
			return core.VoiceDesignResult{}, core.Fail("INVALID_AUDIO", "Gemini returned invalid base64 sample audio.", false)
		}
		wav := ensureWAV(raw, parsed.SampleAudio.MimeType)
		if r.Format == core.FormatMP3 {
			encoded, encErr := googletts.EncodeMP3(ctx, encoder, wav, r.Bitrate)
			if encErr != nil {
				return core.VoiceDesignResult{}, encErr
			}
			res.SampleAudio = encoded
		} else {
			res.SampleAudio = wav
		}
	}
	return res, nil
}

func (g *Gemini) withKeyFailover(ctx context.Context, configDir, cacheDir, tier string, warn func(string), fn func(apiKey string) (int, time.Duration, string, error)) error {
	keys, err := LoadKeys(configDir)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return core.Fail("AUTH_SETUP", "No Gemini API keys configured. Run ttsi keys add --tier free <KEY> or set GEMINI_API_KEY.", false)
	}

	if tier == "" {
		tier = core.KeyTierAuto
	}
	if len(filterAndOrderKeys(keys, tier)) == 0 {
		return core.Fail("AUTH_SETUP", fmt.Sprintf("No Gemini API keys configured for tier %q.", tier), false)
	}

	statePath := StatePath(configDir, cacheDir)
	pool := g.pool()
	tried := map[string]bool{}
	var lastErr error
	var lastFailedKey string

	for {
		entry, ok, _ := pool.Acquire(keys, tier, statePath, tried)
		if !ok {
			if lastErr != nil {
				return lastErr
			}
			return core.Fail("QUOTA_EXHAUSTED", fmt.Sprintf("All Gemini API keys for tier %q are currently exhausted or in cooldown. Run ttsi keys reset or add another key.", tier), false)
		}
		if lastFailedKey != "" && warn != nil {
			warn(fmt.Sprintf("Gemini key %s hit its limit; switching to %s (%s).", lastFailedKey, entry.Name, entry.Tier))
		}
		tried[entry.Key] = true

		status, retryAfter, reason, reqErr := fn(entry.Key)
		if reqErr == nil {
			return nil
		}
		if errors.Is(reqErr, context.Canceled) || ctx.Err() != nil {
			return reqErr
		}

		failover, transient, publicErr := classifyHTTPFailure(status, reason, reqErr)
		if !failover {
			return publicErr
		}

		pool.MarkExhausted(entry.Key, retryAfter, reason, transient, statePath)
		lastErr = publicErr
		lastFailedKey = fmt.Sprintf("%s (%s)", entry.Name, entry.Tier)
	}
}

func buildGenerateContentRequest(r core.Request) generateContentRequest {
	multiSpeaker := len(r.Speakers) > 1
	var parts []requestPart
	if len(r.Turns) > 0 {
		parts = make([]requestPart, 0, len(r.Turns))
		for _, t := range r.Turns {
			part := requestPart{Text: t.Text}
			speaker := t.Speaker
			if !multiSpeaker {
				speaker = ""
			}
			if t.Style != "" || speaker != "" {
				part.SpeechMetadata = &requestSpeechMetadata{
					Style:   t.Style,
					Speaker: speaker,
				}
			}
			parts = append(parts, part)
		}
	} else {
		part := requestPart{Text: r.Text}
		if r.Style != "" {
			part.SpeechMetadata = &requestSpeechMetadata{Style: r.Style}
		}
		parts = []requestPart{part}
	}

	speechCfg := requestSpeechConfig{}
	if r.LanguagePinned && r.Language != "" && !strings.EqualFold(r.Language, core.LanguageAuto) {
		speechCfg.LanguageCode = r.Language
	}
	if multiSpeaker {
		aliases := make([]string, 0, len(r.Speakers))
		for alias := range r.Speakers {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		cfgs := make([]requestSpeakerVoiceConfig, 0, len(aliases))
		for _, alias := range aliases {
			cfgs = append(cfgs, requestSpeakerVoiceConfig{
				Speaker: alias,
				VoiceConfig: requestVoiceConfig{
					PrebuiltVoiceConfig: requestPrebuiltVoice{VoiceName: core.GeminiVoiceName(r.Speakers[alias])},
				},
			})
		}
		speechCfg.MultiSpeakerVoiceConfig = &requestMultiSpeakerVoiceConfig{SpeakerVoiceConfigs: cfgs}
	} else {
		voiceName := core.GeminiVoiceName(r.Voice)
		if len(r.Speakers) == 1 {
			for _, v := range r.Speakers {
				voiceName = core.GeminiVoiceName(v)
			}
		}
		speechCfg.VoiceConfig = &requestVoiceConfig{
			PrebuiltVoiceConfig: requestPrebuiltVoice{VoiceName: voiceName},
		}
	}

	return generateContentRequest{
		Contents: []requestContent{{Role: "user", Parts: parts}},
		GenerationConfig: requestGenerationConfig{
			ResponseModalities: []string{"AUDIO"},
			SpeechConfig:       speechCfg,
		},
	}
}

func (g *Gemini) callGenerateContent(ctx context.Context, apiKey, model string, payload generateContentRequest, reports ...func(core.Usage)) ([]byte, int, time.Duration, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, 0, "", err
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", g.baseURL(), url.PathEscape(model))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := g.httpClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, 0, "TIMEOUT", core.Fail("PROVIDER_TIMEOUT", "Gemini TTS timed out.", true)
		}
		return nil, 0, 0, "NETWORK", core.Fail("PROVIDER_UNAVAILABLE", "Cannot reach Gemini TTS API.", true)
	}
	defer resp.Body.Close()

	const maxResp = 64 << 20
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResp))
	if err != nil {
		return nil, resp.StatusCode, 0, "READ_ERROR", core.Fail("PROVIDER_ERROR", "Failed reading Gemini TTS response.", true)
	}
	if resp.StatusCode != http.StatusOK {
		retryAfter, reason := parseErrorDetails(resp.Header.Get("Retry-After"), respBytes)
		return nil, resp.StatusCode, retryAfter, reason, fmt.Errorf("gemini status %d: %s", resp.StatusCode, reason)
	}

	var parsed generateContentResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, resp.StatusCode, 0, "", core.Fail("INVALID_AUDIO", "Gemini returned malformed JSON.", false)
	}
	if len(reports) > 0 && reports[0] != nil {
		u := core.Usage{CredentialID: KeyID(apiKey)}
		if parsed.UsageMetadata != nil {
			u.InputTokens = parsed.UsageMetadata.PromptTokenCount
			u.OutputTokens = parsed.UsageMetadata.CandidatesTokenCount
		}
		reports[0](u)
	}
	for _, cand := range parsed.Candidates {
		for _, part := range cand.Content.Parts {
			if part.InlineData != nil && part.InlineData.Data != "" {
				raw, decErr := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if decErr != nil {
					return nil, resp.StatusCode, 0, "", core.Fail("INVALID_AUDIO", "Gemini returned invalid base64 audio.", false)
				}
				return ensureWAV(raw, part.InlineData.MimeType), resp.StatusCode, 0, "", nil
			}
		}
	}
	return nil, resp.StatusCode, 0, "", core.Fail("INVALID_AUDIO", "Gemini response contained no audio data.", false)
}

func (g *Gemini) callCreateVoice(ctx context.Context, apiKey string, payload createVoiceRequest) (createVoiceResponse, int, time.Duration, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return createVoiceResponse{}, 0, 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL()+"/voices", bytes.NewReader(body))
	if err != nil {
		return createVoiceResponse{}, 0, 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := g.httpClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return createVoiceResponse{}, 0, 0, "TIMEOUT", core.Fail("PROVIDER_TIMEOUT", "Gemini Voices API timed out.", true)
		}
		return createVoiceResponse{}, 0, 0, "NETWORK", core.Fail("PROVIDER_UNAVAILABLE", "Cannot reach Gemini Voices API.", true)
	}
	defer resp.Body.Close()

	const maxResp = 32 << 20
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResp))
	if err != nil {
		return createVoiceResponse{}, resp.StatusCode, 0, "READ_ERROR", core.Fail("PROVIDER_ERROR", "Failed reading Gemini Voices API response.", true)
	}
	if resp.StatusCode != http.StatusOK {
		retryAfter, reason := parseErrorDetails(resp.Header.Get("Retry-After"), respBytes)
		return createVoiceResponse{}, resp.StatusCode, retryAfter, reason, fmt.Errorf("gemini voices status %d: %s", resp.StatusCode, reason)
	}

	var parsed createVoiceResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return createVoiceResponse{}, resp.StatusCode, 0, "", core.Fail("PROVIDER_ERROR", "Gemini returned malformed voice design JSON.", false)
	}
	return parsed, resp.StatusCode, 0, "", nil
}

// ListVoices queries Gemini's v1beta/voices endpoint using available keys.
func (g *Gemini) ListVoices(ctx context.Context, lang string) ([]core.Voice, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	keys, err := LoadKeys("")
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, core.Fail("AUTH_SETUP", "No Gemini API keys configured. Run ttsi keys add --tier free <KEY> or set GEMINI_API_KEY.", false)
	}

	pool := g.pool()
	tried := map[string]bool{}
	var lastErr error
	for {
		entry, ok, _ := pool.Acquire(keys, core.KeyTierAuto, "", tried)
		if !ok {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, core.Fail("QUOTA_EXHAUSTED", "All Gemini API keys are currently exhausted.", false)
		}
		tried[entry.Key] = true

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL()+"/voices", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-goog-api-key", entry.Key)
		resp, err := g.httpClient().Do(req)
		if err != nil {
			return nil, core.Fail("PROVIDER_UNAVAILABLE", "Cannot reach Gemini voices endpoint.", true)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, core.Fail("PROVIDER_ERROR", "Failed reading Gemini voices response.", true)
		}
		if resp.StatusCode != http.StatusOK {
			retryAfter, reason := parseErrorDetails(resp.Header.Get("Retry-After"), body)
			failover, transient, publicErr := classifyHTTPFailure(resp.StatusCode, reason, errors.New(reason))
			if !failover {
				return nil, publicErr
			}
			pool.MarkExhausted(entry.Key, retryAfter, reason, transient, "")
			lastErr = publicErr
			continue
		}

		var payload struct {
			Voices []struct {
				Name          string   `json:"name"`
				LanguageCodes []string `json:"languageCodes"`
			} `json:"voices"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, core.Fail("PROVIDER_ERROR", "Gemini returned malformed voices JSON.", false)
		}
		out := make([]core.Voice, 0, len(payload.Voices))
		for _, v := range payload.Voices {
			name := strings.TrimPrefix(v.Name, "voices/")
			vLang := ""
			if len(v.LanguageCodes) > 0 {
				vLang = v.LanguageCodes[0]
			}
			if lang != "" && !containsFold(v.LanguageCodes, lang) {
				continue
			}
			if parsed, ok := core.ParseVoice(name); ok {
				out = append(out, parsed)
			} else {
				out = append(out, core.Voice{Name: name, Language: vLang, Alias: strings.ToLower(name)})
			}
		}
		core.SortVoices(out)
		return out, nil
	}
}

func containsFold(items []string, target string) bool {
	for _, it := range items {
		if strings.EqualFold(it, target) {
			return true
		}
	}
	return false
}

func ensureWAV(data []byte, mimeType string) []byte {
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		return data
	}
	sampleRate := uint32(24000)
	lower := strings.ToLower(mimeType)
	for _, part := range strings.Split(lower, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "rate=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(part, "rate=")); err == nil && n > 0 {
				sampleRate = uint32(n)
			}
		}
	}
	return wrapPCMAsWAV(data, sampleRate, 1, 16)
}

func wrapPCMAsWAV(pcm []byte, sampleRate uint32, channels, bitsPerSample uint16) []byte {
	byteRate := sampleRate * uint32(channels) * uint32(bitsPerSample) / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := uint32(len(pcm))
	out := make([]byte, 44+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], 36+dataSize)
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], channels)
	binary.LittleEndian.PutUint32(out[24:28], sampleRate)
	binary.LittleEndian.PutUint32(out[28:32], byteRate)
	binary.LittleEndian.PutUint16(out[32:34], blockAlign)
	binary.LittleEndian.PutUint16(out[34:36], bitsPerSample)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], dataSize)
	copy(out[44:], pcm)
	return out
}

func parseErrorDetails(retryHeader string, body []byte) (time.Duration, string) {
	var delay time.Duration
	if retryHeader != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryHeader)); err == nil && secs > 0 {
			delay = time.Duration(secs) * time.Second
		}
	}
	var parsed apiErrorBody
	if json.Unmarshal(body, &parsed) != nil {
		return delay, ""
	}
	reason := parsed.Error.Status
	for _, d := range parsed.Error.Details {
		if d.Reason != "" {
			reason = d.Reason
		}
		if d.RetryDelay != "" && delay == 0 {
			if parsedDelay, err := time.ParseDuration(d.RetryDelay); err == nil && parsedDelay > 0 {
				delay = parsedDelay
			}
		}
	}
	return delay, reason
}

func classifyHTTPFailure(status int, reason string, err error) (failover, transient bool, pub *core.Problem) {
	if status == 0 {
		return false, true, core.Public(err)
	}
	switch status {
	case http.StatusTooManyRequests:
		isRateLimit := reason == "RATE_LIMIT_EXCEEDED" || reason == "USER_RATE_LIMIT_EXCEEDED"
		if isRateLimit {
			return true, true, core.Fail("RATE_LIMITED", "Gemini API rate limit exceeded.", true)
		}
		return true, false, core.Fail("QUOTA_EXHAUSTED", "Gemini API quota is exhausted.", false)
	case http.StatusForbidden:
		if strings.Contains(strings.ToUpper(reason), "QUOTA") || strings.Contains(strings.ToUpper(reason), "LIMIT") || strings.Contains(strings.ToUpper(reason), "BILLING") {
			return true, false, core.Fail("QUOTA_EXHAUSTED", "Gemini API quota or billing limit reached.", false)
		}
		return true, false, core.Fail("PERMISSION_DENIED", "Gemini rejected the API key permissions.", false)
	case http.StatusUnauthorized:
		return true, false, core.Fail("AUTH_INVALID", "Gemini rejected the API key.", false)
	case http.StatusBadRequest:
		if strings.Contains(strings.ToUpper(reason), "API_KEY") {
			return true, false, core.Fail("AUTH_INVALID", "Gemini rejected the API key.", false)
		}
		return false, false, core.Fail("PROVIDER_REJECTED", "Gemini rejected the request.", false)
	case http.StatusNotFound:
		return false, false, core.Fail("INVALID_VOICE", "Gemini could not find the requested model or voice.", false)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return false, true, core.Fail("PROVIDER_TIMEOUT", "Gemini TTS timed out.", true)
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusInternalServerError:
		return false, true, core.Fail("PROVIDER_UNAVAILABLE", "Gemini TTS is temporarily unavailable.", true)
	default:
		return false, false, core.Fail("PROVIDER_ERROR", "Gemini TTS failed.", false)
	}
}
