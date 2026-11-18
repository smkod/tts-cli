package googletts

import (
	"context"
	"testing"
)

func TestLimitedBuffer(t *testing.T) {
	b := &limitedBuffer{limit: 3}
	if _, err := b.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("d")); err == nil {
		t.Fatal("accepted excess output")
	}
}
func TestInvalidEncoderBitrate(t *testing.T) {
	_, err := encodeMP3(context.Background(), "not-executed", nil, 97)
	if err == nil {
		t.Fatal("accepted unsupported bitrate")
	}
}
