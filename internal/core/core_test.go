package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider func(context.Context, Request) ([]byte, error)

func (f fakeProvider) Synthesize(ctx context.Context, r Request) ([]byte, error) { return f(ctx, r) }

func frame(marker byte) []byte {
	const length = 417
	data := make([]byte, length)
	copy(data, []byte{0xff, 0xfb, 0x90, 0x00})
	data[20] = marker
	return data
}
func testSettings(t *testing.T) Settings {
	return Settings{Format: FormatMP3, Provider: "google", Language: "en-US", Voice: "en-US-Chirp3-HD-Kore", OutputDir: t.TempDir(), CacheDir: t.TempDir(), Jobs: 4}
}

func TestVoiceResolution(t *testing.T) {
	for _, tc := range []struct {
		name, lang, voice, wantLang, wantVoice string
		bad                                    bool
	}{
		{"default", "", "kore", "en-US", "en-US-Chirp3-HD-Kore", false},
		{"case insensitive", "IT-it", "KoRE", "it-IT", "it-IT-Chirp3-HD-Kore", false},
		{"full ID infers locale", "", "it-it-chirp3-hd-kore", "it-IT", "it-IT-Chirp3-HD-Kore", false},
		{"conflict", "en-US", "it-IT-Chirp3-HD-Kore", "", "", true},
		{"unknown alias", "", "missing", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lang, voice, err := ResolveVoice(tc.lang, tc.voice)
			if (err != nil) != tc.bad || lang != tc.wantLang || voice != tc.wantVoice {
				t.Fatalf("got %q %q %v", lang, voice, err)
			}
		})
	}
}
func TestTextValidation(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		bad        bool
	}{
		{"blank", " \n\t", true}, {"invalid UTF-8", string([]byte{0xff}), true},
		{"exact byte ceiling", strings.Repeat("é", 2500), false}, {"over byte ceiling", strings.Repeat("é", 2501), true},
		{"preserve spaces", "  good morning  ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateText(tc.text); (err != nil) != tc.bad {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func TestMP3Validation(t *testing.T) {
	good := frame(1)
	for _, tc := range []struct {
		name  string
		audio []byte
		want  bool
	}{
		{"frame", good, true}, {"truncated", good[:len(good)-1], false}, {"HTML", []byte("<html>error</html>"), false},
		{"tag only", []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0}, false},
		{"tag and frame", append([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0}, good...), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validMP3(tc.audio); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
func TestWAVValidation(t *testing.T) {
	valid := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\xc0\x5d\x00\x00\x80\xbb\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00")
	badSize := append([]byte(nil), valid...)
	badSize[40] = 0xff
	for _, tc := range []struct {
		name  string
		audio []byte
		want  bool
	}{
		{"PCM WAV", valid, true},
		{"truncated", valid[:len(valid)-1], false},
		{"oversized data chunk", badSize, false},
		{"MP3", frame(1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validAudio(tc.audio, FormatWAV); got != tc.want {
				t.Fatalf("validAudio = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCacheAndOutputSafety(t *testing.T) {
	ctx := context.Background()
	s := testSettings(t)
	calls := 0
	e := &Engine{Provider: fakeProvider(func(_ context.Context, r Request) ([]byte, error) {
		calls++
		if r.Text != " hello " {
			t.Fatalf("text was changed: %q", r.Text)
		}
		return frame(1), nil
	})}
	g, err := e.Generate(ctx, s, " hello ", Options{})
	if err != nil || g.Cached || g.Characters != 7 || !filepath.IsAbs(g.File) || calls != 1 {
		t.Fatalf("initial: %+v %v (calls %d)", g, err, calls)
	}
	again, err := e.Generate(ctx, s, " hello ", Options{})
	if err != nil || !again.Cached || again.File != g.File || calls != 1 {
		t.Fatalf("cache hit: %+v %v (calls %d)", again, err, calls)
	}
	preview, err := e.Generate(ctx, s, "other", Options{DryRun: true})
	if err != nil || preview.CacheStatus != "miss" || calls != 1 {
		t.Fatalf("dry run: %+v %v", preview, err)
	}
	if _, err = os.Stat(preview.File); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run wrote output: %v", err)
	}
	if err = os.WriteFile(g.File, []byte("do not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Generate(ctx, s, " hello ", Options{}); err == nil || Public(err).Code != "OUTPUT_EXISTS" {
		t.Fatalf("expected a no-clobber error: %v", err)
	}
	b, err := os.ReadFile(g.File)
	if err != nil || string(b) != "do not replace" || calls != 1 {
		t.Fatalf("output was touched: %q %v", b, err)
	}
	next := filepath.Join(s.OutputDir, "new.mp3")
	bypass, err := e.Generate(ctx, s, " hello ", Options{Output: next, NoCache: true})
	if err != nil || bypass.Cached || bypass.CacheStatus != "bypassed" || calls != 2 {
		t.Fatalf("cache bypass: %+v %v", bypass, err)
	}
}
func TestCacheKey(t *testing.T) {
	r := Request{Format: FormatMP3, Text: "hello", Provider: "google", Language: "en-US", Voice: "en-US-Chirp3-HD-Kore"}
	first := cacheKey(r)
	if len(first) != 64 || first != cacheKey(r) {
		t.Fatal("cache key is not stable SHA-256")
	}
	r.Text = " hello"
	if cacheKey(r) == first {
		t.Fatal("cache collapsed distinct source text")
	}
}
func TestCorruptCacheIsNotReused(t *testing.T) {
	s := testSettings(t)
	key := cacheKey(Request{Text: "hello", Provider: s.Provider, Language: s.Language, Voice: s.Voice, Format: s.Format})
	p := filepath.Join(s.CacheDir, "v1", key[:2], key+".entry")
	if err := storeCache(p, frame(1)); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	entry[len(entry)-1] ^= 1
	if err := os.WriteFile(p, entry, 0600); err != nil {
		t.Fatal(err)
	}
	warns := 0
	e := &Engine{Warn: func(string) { warns++ }, Provider: fakeProvider(func(context.Context, Request) ([]byte, error) { return frame(2), nil })}
	g, err := e.Generate(context.Background(), s, "hello", Options{})
	if err != nil || g.Cached || warns != 1 {
		t.Fatalf("corrupt cache reused: %+v %v", g, err)
	}
	if b, _ := os.ReadFile(g.File); len(b) != 417 || b[20] != 2 {
		t.Fatal("expected freshly synthesized data")
	}
}
func TestConcurrentCachePublication(t *testing.T) {
	s := testSettings(t)
	var calls atomic.Int32
	both := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider := fakeProvider(func(ctx context.Context, _ Request) ([]byte, error) {
		n := calls.Add(1)
		if n == 2 {
			close(both)
		}
		select {
		case <-both:
			return frame(byte(n)), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	var results [2]Generation
	var errs [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			e := &Engine{Provider: provider}
			results[i], errs[i] = e.Generate(ctx, s, "same", Options{})
		})
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Cached == results[1].Cached {
		t.Fatalf("expected one valid winner: %+v %v", results, errs)
	}
}
func TestRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		problem *Problem
		cancel  bool
		calls   int
		code    string
	}{
		{"transient", Fail("RATE_LIMITED", "Slow down.", true), false, 3, "RATE_LIMITED"},
		{"permanent", Fail("QUOTA_EXHAUSTED", "Out of quota.", false), false, 1, "QUOTA_EXHAUSTED"},
		{"canceled during wait", Fail("PROVIDER_UNAVAILABLE", "Unavailable.", true), true, 1, "CANCELED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			e := &Engine{Provider: fakeProvider(func(context.Context, Request) ([]byte, error) { calls++; return nil, tc.problem }),
				Wait: func(ctx context.Context, _ time.Duration) error {
					if tc.cancel {
						cancel()
					}
					return ctx.Err()
				}}
			_, err := e.Generate(ctx, testSettings(t), "hello", Options{})
			if calls != tc.calls || Public(err).Code != tc.code {
				t.Fatalf("got %d calls, err %v", calls, err)
			}
		})
	}
}
