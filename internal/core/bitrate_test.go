package core

import "testing"

func TestValidBitrate(t *testing.T) {
	for _, tc := range []struct {
		rate  int
		valid bool
	}{{8, true}, {32, true}, {96, true}, {128, true}, {192, true}, {320, true}, {0, false}, {97, false}, {321, false}} {
		if got := ValidBitrate(tc.rate); got != tc.valid {
			t.Errorf("ValidBitrate(%d) = %v, want %v", tc.rate, got, tc.valid)
		}
	}
}
func TestMP3CacheBitrate(t *testing.T) {
	r := Request{Text: "hello", Format: FormatMP3, Bitrate: 96}
	first := cacheKey(r)
	r.Bitrate = 128
	if first == cacheKey(r) {
		t.Fatal("distinct MP3 bitrates share cache key")
	}
	r.Format = FormatWAV
	first = cacheKey(r)
	r.Bitrate = 64
	if first != cacheKey(r) {
		t.Fatal("bitrate changed WAV cache key")
	}
}
