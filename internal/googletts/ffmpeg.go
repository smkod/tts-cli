package googletts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/smkod/tts-cli/internal/core"
)

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("encoder output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func findFFmpeg(ctx context.Context) (string, error) {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var path string
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			path = candidate
		}
	}
	if path == "" {
		path, _ = exec.LookPath(name)
	}
	if path == "" {
		return "", core.Fail("FFMPEG_MISSING", "Install FFmpeg with libmp3lame or place it beside ttsi.", false)
	}
	// Check the encoder before making a billable synthesis request.
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-encoders")
	out := &limitedBuffer{limit: 1 << 20}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", core.Fail("FFMPEG_UNAVAILABLE", "Cannot run FFmpeg.", false)
	}
	if !strings.Contains(out.String(), "libmp3lame") {
		return "", core.Fail("FFMPEG_UNAVAILABLE", "FFmpeg must include the libmp3lame encoder.", false)
	}
	return path, nil
}

func encodeMP3(ctx context.Context, path string, wav []byte, bitrate int) ([]byte, error) {
	if bitrate == 0 {
		bitrate = core.MP3BitrateKbps
	}
	if !core.ValidBitrate(bitrate) {
		return nil, core.Fail("INVALID_CONFIG", "Unsupported MP3 bitrate.", false)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "wav", "-i", "pipe:0", "-map", "0:a:0", "-map_metadata", "-1", "-c:a", "libmp3lame", "-b:a", strconv.Itoa(bitrate) + "k"}
	// MPEG-2 at the native 24 kHz supports at most 160 kbps.
	if bitrate > 160 {
		args = append(args, "-ar", "48000")
	}
	args = append(args, "-f", "mp3", "pipe:1")
	cmd := exec.CommandContext(ctx, path, args...)
	out := &limitedBuffer{limit: 64 << 20}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(wav), out, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, core.Fail("AUDIO_CONVERSION", "FFmpeg could not encode the synthesized WAV as MP3.", false)
	}
	return out.Bytes(), nil
}
