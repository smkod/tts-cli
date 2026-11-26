package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/smkod/tts-cli/internal/core"
)

type scriptSpec struct {
	Turns    []core.Turn
	Speakers map[string]string
}

func decodeTurns(raw json.RawMessage) ([]core.Turn, bool) {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil || len(arr) == 0 {
		return nil, false
	}
	turns := make([]core.Turn, len(arr))
	for i, elem := range arr {
		fields, ok := object(elem)
		if !ok {
			return nil, false
		}
		text, hasText := jsonText(fields["text"])
		if !hasText {
			return nil, false
		}
		var style, speaker string
		for k, v := range fields {
			switch k {
			case "text":
			case "style":
				s, ok := jsonText(v)
				if !ok || strings.TrimSpace(s) == "" {
					return nil, false
				}
				style = strings.TrimSpace(s)
			case "speaker":
				sp, ok := jsonText(v)
				if !ok || strings.TrimSpace(sp) == "" {
					return nil, false
				}
				speaker = strings.TrimSpace(sp)
			default:
				return nil, false
			}
		}
		turns[i] = core.Turn{Text: text, Style: style, Speaker: speaker}
	}
	return turns, true
}

func decodeSpeakers(raw json.RawMessage) (map[string]string, bool) {
	fields, ok := object(raw)
	if !ok || len(fields) == 0 || len(fields) > 2 {
		return nil, false
	}
	speakers := make(map[string]string, len(fields))
	for rawAlias, rawVoice := range fields {
		alias := strings.TrimSpace(rawAlias)
		voice, validVoice := jsonText(rawVoice)
		if alias == "" || !validVoice || strings.TrimSpace(voice) == "" {
			return nil, false
		}
		if _, dup := speakers[alias]; dup {
			return nil, false
		}
		speakers[alias] = strings.TrimSpace(voice)
	}
	return speakers, true
}

func loadScript(path string) (scriptSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return scriptSpec{}, core.Fail("FILE_IO", "Cannot open the script file.", false)
	}
	defer f.Close()
	const limit = 1 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return scriptSpec{}, core.Fail("FILE_IO", "Cannot read the script file.", false)
	}
	invalid := core.Fail("INVALID_INPUT", "Provide a UTF-8 JSON script up to 1 MiB with a nonempty turns array and optional speakers map.", false)
	if len(b) > limit || !utf8.Valid(b) {
		return scriptSpec{}, invalid
	}
	doc, ok := object(b)
	if !ok {
		return scriptSpec{}, invalid
	}
	var spec scriptSpec
	hasTurns := false
	for k, v := range doc {
		switch k {
		case "turns":
			turns, validTurns := decodeTurns(v)
			if !validTurns {
				return scriptSpec{}, invalid
			}
			spec.Turns = turns
			hasTurns = true
		case "speakers":
			speakers, validSpeakers := decodeSpeakers(v)
			if !validSpeakers {
				return scriptSpec{}, invalid
			}
			spec.Speakers = speakers
		default:
			return scriptSpec{}, invalid
		}
	}
	if !hasTurns {
		return scriptSpec{}, invalid
	}
	return spec, nil
}
