package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxAudio = 32 << 20

func ValidAudio(b []byte, format string) bool {
	return validAudio(b, format)
}

func validAudio(b []byte, format string) bool {
	if len(b) > maxAudio {
		return false
	}
	switch format {
	case FormatWAV:
		return validWAV(b)
	case FormatMP3:
		return validMP3(b)
	default:
		return false
	}
}

// validWAV checks a complete RIFF/WAVE file containing 16-bit PCM.
func validWAV(b []byte) bool {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return false
	}
	order := binary.LittleEndian
	if uint64(order.Uint32(b[4:8])) != uint64(len(b)-8) {
		return false
	}
	var blockAlign uint32
	var hasData bool
	for b = b[12:]; len(b) > 0; {
		if len(b) < 8 {
			return false
		}
		size := order.Uint32(b[4:8])
		padded := uint64(size) + uint64(size%2)
		if padded > uint64(len(b)-8) {
			return false
		}
		chunk := b[8 : 8+int(size)]
		switch string(b[:4]) {
		case "fmt ":
			if blockAlign != 0 || len(chunk) < 16 || order.Uint16(chunk[:2]) != 1 ||
				order.Uint16(chunk[14:16]) != 16 {
				return false
			}
			channels := uint32(order.Uint16(chunk[2:4]))
			rate := uint64(order.Uint32(chunk[4:8]))
			blockAlign = uint32(order.Uint16(chunk[12:14]))
			if channels == 0 || rate == 0 || blockAlign != 2*channels ||
				uint64(order.Uint32(chunk[8:12])) != rate*uint64(blockAlign) {
				return false
			}
		case "data":
			if hasData || blockAlign == 0 || size == 0 || size%blockAlign != 0 {
				return false
			}
			hasData = true
		}
		b = b[8+int(padded):]
	}
	return hasData
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// publish writes complete bytes before the name becomes visible.
func publish(path string, data []byte, replace bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tts-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if replace {
		return replaceFile(name, path)
	}
	return os.Link(name, path)
}

func regularData(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, os.ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, os.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, os.ErrInvalid
	}
	return data, err
}
func readCache(path, format string) ([]byte, string) {
	b, err := regularData(path, maxAudio+sha256.Size)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "miss"
	}
	if err != nil || len(b) < sha256.Size {
		return nil, "corrupt"
	}
	audio := b[sha256.Size:]
	sum := sha256.Sum256(audio)
	if !bytes.Equal(sum[:], b[:sha256.Size]) || !validAudio(audio, format) {
		return nil, "corrupt"
	}
	return audio, "hit"
}
func storeCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	b := make([]byte, len(sum)+len(data))
	copy(b, sum[:])
	copy(b[len(sum):], data)
	return publish(path, b, false)
}

func CheckOutput(path string) error {
	return checkOutput(path)
}

func WriteOutput(path string, data []byte) error {
	return writeOutput(path, data)
}

func checkOutput(path string) error {
	i, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return Fail("FILE_IO", "Cannot inspect the output file.", false)
	}
	if !i.Mode().IsRegular() {
		return outputConflict()
	}
	return nil
}
func outputConflict() *Problem {
	return Fail("OUTPUT_EXISTS", "Output already exists with different contents or is not a regular file. Choose a new path.", false)
}
func writeOutput(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Fail("FILE_IO", "Cannot create the output directory.", false)
	}
	if err := publish(path, data, false); err == nil {
		return nil
	}
	if err := checkOutput(path); err != nil {
		return err
	}
	b, err := regularData(path, maxAudio)
	if err == nil && bytes.Equal(b, data) {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return outputConflict()
	}
	return Fail("FILE_IO", "Cannot publish output safely; the filesystem must support hard links.", false)
}

// validMP3 checks contiguous MPEG Layer III frames, with optional ID3 tags.
func validMP3(b []byte) bool {
	if len(b) < 4 || len(b) > maxAudio {
		return false
	}
	if bytes.HasPrefix(b, []byte("ID3")) {
		if len(b) < 10 || b[3] < 2 || b[3] > 4 || (b[6]|b[7]|b[8]|b[9])&0x80 != 0 {
			return false
		}
		skip := 10 + int(b[6])<<21 + int(b[7])<<14 + int(b[8])<<7 + int(b[9])
		if b[3] == 4 && b[5]&0x10 != 0 {
			skip += 10
		}
		if skip > len(b) {
			return false
		}
		b = b[skip:]
	}
	if len(b) >= 128 && bytes.Equal(b[len(b)-128:len(b)-125], []byte("TAG")) {
		b = b[:len(b)-128]
	}
	if len(b) == 0 {
		return false
	}
	rates := [...]int{44100, 48000, 32000}
	v1 := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	v2 := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	for len(b) > 0 {
		if len(b) < 4 {
			return false
		}
		h := binary.BigEndian.Uint32(b)
		version := (h >> 19) & 3
		bitrate := (h >> 12) & 15
		rate := (h >> 10) & 3
		if h>>21 != 0x7ff || version == 1 || (h>>17)&3 != 1 || bitrate == 0 || bitrate == 15 || rate == 3 {
			return false
		}
		hz := rates[rate]
		factor, kbps := 144000, v1[bitrate]
		if version != 3 {
			factor, kbps = 72000, v2[bitrate]
			if version == 2 {
				hz /= 2
			} else {
				hz /= 4
			}
		}
		n := factor*kbps/hz + int((h>>9)&1)
		if n < 4 || n > len(b) {
			return false
		}
		b = b[n:]
	}
	return true
}

func ProbeDirectory(path string) error {
	// Probe a missing directory's nearest existing ancestor without creating directories.
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return os.ErrInvalid
			}
			f, err := os.CreateTemp(path, ".tts-doctor-*")
			if err != nil {
				return err
			}
			defer os.Remove(f.Name())
			return f.Close()
		}
		parent := filepath.Dir(path)
		if !errors.Is(err, os.ErrNotExist) || parent == path {
			return err
		}
		path = parent
	}
}
