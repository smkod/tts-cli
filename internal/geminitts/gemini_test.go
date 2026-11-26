package geminitts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smkod/tts-cli/internal/core"
)

func testWAV() []byte {
	return wrapPCMAsWAV([]byte{0x01, 0x00, 0x02, 0x00}, 24000, 1, 16)
}

func clearKeyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TTS_GEMINI_KEYS_FILE", "TTS_GEMINI_FREE_KEYS", "TTS_GEMINI_PAID_KEYS", "GEMINI_API_KEYS", "GEMINI_API_KEY"} {
		t.Setenv(k, "")
	}
}

func TestFreeToPaidFailoverAndCooldownPersistence(t *testing.T) {
	clearKeyEnv(t)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()

	if _, err := AddKey(cfgDir, "free-1", TierFree, "AIza-free-key-1111"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKey(cfgDir, "free-2", TierFree, "AIza-free-key-2222"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKey(cfgDir, "paid-1", TierPaid, "AIza-paid-key-9999"); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var calls []string
	var gotModel string
	var gotVoice string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-goog-api-key")
		mu.Lock()
		calls = append(calls, key)
		gotModel = r.URL.Path
		mu.Unlock()

		var body generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("bad request JSON: %v", err)
		}
		gotVoice = body.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName

		if strings.HasPrefix(key, "AIza-free-key") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED","details":[{"reason":"QUOTA_EXCEEDED"}]}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{
								"inlineData": map[string]any{
									"mimeType": "audio/wav",
									"data":     base64.StdEncoding.EncodeToString(testWAV()),
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	var warnings []string
	g := &Gemini{BaseURL: srv.URL, HTTPClient: srv.Client(), Pool: NewKeyPool()}
	req := core.Request{
		Text:      "Hello Gemini 3.8",
		Provider:  core.ProviderGemini,
		Model:     core.DefaultGeminiModel,
		KeyTier:   core.KeyTierAuto,
		Voice:     "en-US-Chirp3-HD-Kore",
		Format:    core.FormatWAV,
		ConfigDir: cfgDir,
		CacheDir:  cacheDir,
		Warn:      func(msg string) { warnings = append(warnings, msg) },
	}

	audio, err := g.Synthesize(context.Background(), req)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(audio) < 44 || string(audio[:4]) != "RIFF" {
		t.Fatalf("expected valid WAV audio, got %d bytes", len(audio))
	}
	if !strings.HasSuffix(gotModel, "/models/gemini-3.8-flash-tts:generateContent") {
		t.Fatalf("unexpected endpoint path: %s", gotModel)
	}
	if gotVoice != "Kore" {
		t.Fatalf("expected voice Kore, got %q", gotVoice)
	}
	if len(calls) != 3 || calls[0] != "AIza-free-key-1111" || calls[1] != "AIza-free-key-2222" || calls[2] != "AIza-paid-key-9999" {
		t.Fatalf("unexpected key call sequence: %v", calls)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 failover warnings, got %v", warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w, "AIza-") {
			t.Fatalf("warning leaked raw API key: %s", w)
		}
	}

	// Simulate a brand new CLI invocation: free keys are in persisted cooldown, so paid-1 is used directly.
	calls = nil
	g2 := &Gemini{BaseURL: srv.URL, HTTPClient: srv.Client(), Pool: NewKeyPool()}
	if _, err := g2.Synthesize(context.Background(), req); err != nil {
		t.Fatalf("second Synthesize failed: %v", err)
	}
	if len(calls) != 1 || calls[0] != "AIza-paid-key-9999" {
		t.Fatalf("expected direct use of paid key on second invocation, got %v", calls)
	}
}

func TestKeyTierFreeOnlyDoesNotSpendPaidKey(t *testing.T) {
	clearKeyEnv(t)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()

	if _, err := AddKey(cfgDir, "free-1", TierFree, "AIza-free-only-1111"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddKey(cfgDir, "paid-1", TierPaid, "AIza-paid-only-9999"); err != nil {
		t.Fatal(err)
	}

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Header.Get("x-goog-api-key"))
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`))
	}))
	defer srv.Close()

	g := &Gemini{BaseURL: srv.URL, HTTPClient: srv.Client(), Pool: NewKeyPool()}
	_, err := g.Synthesize(context.Background(), core.Request{
		Text:      "Free only",
		Provider:  core.ProviderGemini,
		Model:     core.GeminiFlashLiteTTSModel,
		KeyTier:   core.KeyTierFree,
		Voice:     "Puck",
		Format:    core.FormatWAV,
		ConfigDir: cfgDir,
		CacheDir:  cacheDir,
	})
	if err == nil || core.Public(err).Code != "QUOTA_EXHAUSTED" {
		t.Fatalf("expected QUOTA_EXHAUSTED, got %v", err)
	}
	if len(calls) != 1 || calls[0] != "AIza-free-only-1111" {
		t.Fatalf("paid key should not have been called: %v", calls)
	}
}

func TestRawPCMWrappedAsWAV(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv("GEMINI_API_KEY", "AIza-env-key-1234")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{
								"inlineData": map[string]any{
									"mimeType": "audio/L16;rate=24000",
									"data":     base64.StdEncoding.EncodeToString([]byte{0x10, 0x00, 0x20, 0x00}),
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := &Gemini{BaseURL: srv.URL, HTTPClient: srv.Client(), Pool: NewKeyPool()}
	audio, err := g.Synthesize(context.Background(), core.Request{
		Text:     "PCM test",
		Provider: core.ProviderGemini,
		Model:    core.DefaultGeminiModel,
		Voice:    "Kore",
		Format:   core.FormatWAV,
	})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(audio) != 48 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		t.Fatalf("expected 48-byte WAV, got %d bytes", len(audio))
	}
}

func TestKeyPoolResetAndMasking(t *testing.T) {
	if got := MaskKey("AIzaSyAbCdEfGhIjKlMnOpQrStUvWxYz1234"); got != "AIza...1234" {
		t.Fatalf("unexpected mask: %s", got)
	}
	pool := NewKeyPool()
	statePath := StatePath(t.TempDir(), t.TempDir())
	keys := []KeyEntry{{Name: "free-1", Tier: TierFree, Key: "AIza-test-key-0001", Source: "file"}}
	pool.MarkExhausted(keys[0].Key, 5*time.Minute, "QUOTA_EXCEEDED", false, statePath)

	if _, ok, cooled := pool.Acquire(keys, core.KeyTierAuto, statePath, nil); ok || !cooled {
		t.Fatalf("expected key to be in cooldown, ok=%v cooled=%v", ok, cooled)
	}
	if err := pool.ResetCooldowns(statePath); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := pool.Acquire(keys, core.KeyTierAuto, statePath, nil); !ok {
		t.Fatal("expected key to be available after reset")
	}
}

func TestStyleMultiSpeakerAndLanguagePinningPayload(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv("GEMINI_API_KEY", "AIza-env-key-1234")

	var got generateContentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("bad request JSON: %v", err)
		}
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{
								"inlineData": map[string]any{
									"mimeType": "audio/wav",
									"data":     base64.StdEncoding.EncodeToString(testWAV()),
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := &Gemini{BaseURL: srv.URL, HTTPClient: srv.Client(), Pool: NewKeyPool()}
	_, err := g.Synthesize(context.Background(), core.Request{
		Provider:       core.ProviderGemini,
		Model:          core.DefaultGeminiModel,
		Language:       "es-MX",
		LanguagePinned: true,
		Format:         core.FormatWAV,
		Speakers:       map[string]string{"Host": "Puck", "Guest": "Kore"},
		Turns: []core.Turn{
			{Speaker: "Host", Style: "cheerful", Text: "Hola <short pause>"},
			{Speaker: "Guest", Style: "nervous", Text: "<sigh> Que pasa?"},
		},
	})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if got.GenerationConfig.SpeechConfig.LanguageCode != "es-MX" {
		t.Fatalf("expected languageCode es-MX, got %q", got.GenerationConfig.SpeechConfig.LanguageCode)
	}
	if got.GenerationConfig.SpeechConfig.VoiceConfig != nil || got.GenerationConfig.SpeechConfig.MultiSpeakerVoiceConfig == nil {
		t.Fatalf("expected multiSpeakerVoiceConfig only, got %+v", got.GenerationConfig.SpeechConfig)
	}
	if len(got.Contents) != 1 || len(got.Contents[0].Parts) != 2 {
		t.Fatalf("unexpected contents: %+v", got.Contents)
	}
	if got.Contents[0].Parts[0].SpeechMetadata == nil || got.Contents[0].Parts[0].SpeechMetadata.Style != "cheerful" || got.Contents[0].Parts[0].SpeechMetadata.Speaker != "Host" {
		t.Fatalf("unexpected part 0 metadata: %+v", got.Contents[0].Parts[0])
	}
}

func TestDesignVoice(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv("GEMINI_API_KEY", "AIza-env-key-1234")

	var got createVoiceRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/voices" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "voice_abc123",
			"name":         "voices/voice_abc123",
			"displayName":  "Narrator",
			"model":        "gemini-3.8-flash-tts",
			"languageCode": "en-US",
			"gender":       "MALE",
			"expireTime":   "2027-09-24T11:36:00Z",
			"sampleAudio": map[string]any{
				"mimeType": "audio/wav",
				"data":     base64.StdEncoding.EncodeToString(testWAV()),
			},
		})
	}))
	defer srv.Close()

	g := &Gemini{BaseURL: srv.URL, HTTPClient: srv.Client(), Pool: NewKeyPool()}
	res, err := g.DesignVoice(context.Background(), core.VoiceDesignRequest{
		Prompt:       "Warm narrator",
		DisplayName:  "Narrator",
		Gender:       "male",
		LanguageCode: "en-US",
		Model:        core.DefaultGeminiModel,
		Format:       core.FormatWAV,
	})
	if err != nil {
		t.Fatalf("DesignVoice failed: %v", err)
	}
	if !got.Store || got.Voice.Type != "prompted" || got.Voice.Prompted.Input != "Warm narrator" || got.Voice.Gender != "MALE" {
		t.Fatalf("unexpected request payload: %+v", got)
	}
	if res.ID != "voice_abc123" || len(res.SampleAudio) == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
}
