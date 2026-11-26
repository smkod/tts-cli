package core

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ModifierTag describes a supported inline vocal event or markup tag.
type ModifierTag struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Provider    string `json:"provider"`
	Description string `json:"description"`
}

var geminiInlineTags = []ModifierTag{
	{Tag: "<short pause>", Name: "short pause", Category: "pause", Provider: ProviderGemini, Description: "Brief conversational pause"},
	{Tag: "<long pause>", Name: "long pause", Category: "pause", Provider: ProviderGemini, Description: "Extended dramatic pause"},

	{Tag: "<breath>", Name: "breath", Category: "breath", Provider: ProviderGemini, Description: "Audible intake of breath"},
	{Tag: "<heavy breath>", Name: "heavy breath", Category: "breath", Provider: ProviderGemini, Description: "Deep or labored breath"},
	{Tag: "<exhales>", Name: "exhales", Category: "breath", Provider: ProviderGemini, Description: "Audible exhalation"},
	{Tag: "<pant>", Name: "pant", Category: "breath", Provider: ProviderGemini, Description: "Short, winded panting breath"},
	{Tag: "<sigh>", Name: "sigh", Category: "breath", Provider: ProviderGemini, Description: "Audible sigh"},

	{Tag: "<laugh>", Name: "laugh", Category: "laughter", Provider: ProviderGemini, Description: "Full laugh"},
	{Tag: "<chuckle>", Name: "chuckle", Category: "laughter", Provider: ProviderGemini, Description: "Soft or brief chuckle"},
	{Tag: "<giggle>", Name: "giggle", Category: "laughter", Provider: ProviderGemini, Description: "Light giggle"},
	{Tag: "<snicker>", Name: "snicker", Category: "laughter", Provider: ProviderGemini, Description: "Suppressed or sly snicker"},
	{Tag: "<cackle>", Name: "cackle", Category: "laughter", Provider: ProviderGemini, Description: "Sharp, hearty cackle"},

	{Tag: "<gasp>", Name: "gasp", Category: "reaction", Provider: ProviderGemini, Description: "Sudden gasp of surprise or shock"},
	{Tag: "<cough>", Name: "cough", Category: "reaction", Provider: ProviderGemini, Description: "Cough"},
	{Tag: "<sneeze>", Name: "sneeze", Category: "reaction", Provider: ProviderGemini, Description: "Sneeze"},
	{Tag: "<throat-clearing>", Name: "throat-clearing", Category: "reaction", Provider: ProviderGemini, Description: "Throat clearing"},
	{Tag: "<snort>", Name: "snort", Category: "reaction", Provider: ProviderGemini, Description: "Brief snort"},
	{Tag: "<sob>", Name: "sob", Category: "reaction", Provider: ProviderGemini, Description: "Crying sob"},
	{Tag: "<cry>", Name: "cry", Category: "reaction", Provider: ProviderGemini, Description: "Weeping or crying"},
	{Tag: "<whimper>", Name: "whimper", Category: "reaction", Provider: ProviderGemini, Description: "Soft whimper"},
	{Tag: "<groan>", Name: "groan", Category: "reaction", Provider: ProviderGemini, Description: "Low groan"},
	{Tag: "<grunt>", Name: "grunt", Category: "reaction", Provider: ProviderGemini, Description: "Short grunt of effort"},
	{Tag: "<yawn>", Name: "yawn", Category: "reaction", Provider: ProviderGemini, Description: "Tired yawn"},
	{Tag: "<tsk>", Name: "tsk", Category: "reaction", Provider: ProviderGemini, Description: "Disapproving tsk sound"},
	{Tag: "<argh>", Name: "argh", Category: "reaction", Provider: ProviderGemini, Description: "Frustrated argh"},
	{Tag: "<ugh>", Name: "ugh", Category: "reaction", Provider: ProviderGemini, Description: "Exasperated or disgusted ugh"},
	{Tag: "<grr>", Name: "grr", Category: "reaction", Provider: ProviderGemini, Description: "Growling grr"},
	{Tag: "<hiss>", Name: "hiss", Category: "reaction", Provider: ProviderGemini, Description: "Sharp hiss"},
	{Tag: "<pff>", Name: "pff", Category: "reaction", Provider: ProviderGemini, Description: "Dismissive pff"},
	{Tag: "<phew>", Name: "phew", Category: "reaction", Provider: ProviderGemini, Description: "Relieved phew"},

	{Tag: "<shout>", Name: "shout", Category: "delivery", Provider: ProviderGemini, Description: "Loud vocal burst or shout"},
	{Tag: "<scream>", Name: "scream", Category: "delivery", Provider: ProviderGemini, Description: "Intense scream"},
	{Tag: "<shriek>", Name: "shriek", Category: "delivery", Provider: ProviderGemini, Description: "High-pitched shriek"},
}

var googleMarkupTags = []ModifierTag{
	{Tag: "[pause]", Name: "pause", Category: "pause", Provider: ProviderGoogle, Description: "Standard conversational pause in Chirp 3 HD markup"},
	{Tag: "[pause short]", Name: "pause short", Category: "pause", Provider: ProviderGoogle, Description: "Short conversational pause in Chirp 3 HD markup"},
	{Tag: "[pause long]", Name: "pause long", Category: "pause", Provider: ProviderGoogle, Description: "Long conversational pause in Chirp 3 HD markup"},
}

var (
	geminiTagSet = func() map[string]bool {
		m := make(map[string]bool, len(geminiInlineTags))
		for _, t := range geminiInlineTags {
			m[t.Name] = true
		}
		return m
	}()
	googleTagSet = func() map[string]bool {
		m := make(map[string]bool, len(googleMarkupTags))
		for _, t := range googleMarkupTags {
			m[t.Name] = true
		}
		return m
	}()
	geminiTagAliases = map[string]string{
		"laughs":        "laugh",
		"laughter":      "laugh",
		"laughing":      "laugh",
		"chuckles":      "chuckle",
		"chuckling":     "chuckle",
		"giggles":       "giggle",
		"giggling":      "giggle",
		"snickers":      "snicker",
		"cackles":       "cackle",
		"sighs":         "sigh",
		"sighing":       "sigh",
		"gasps":         "gasp",
		"gasping":       "gasp",
		"coughs":        "cough",
		"coughing":      "cough",
		"sneezes":       "sneeze",
		"snores":        "snort",
		"snorts":        "snort",
		"sobs":          "sob",
		"cries":         "cry",
		"whimpers":      "whimper",
		"groans":        "groan",
		"grunts":        "grunt",
		"yawns":         "yawn",
		"shouts":        "shout",
		"shouting":      "shout",
		"screams":       "scream",
		"screaming":     "scream",
		"shrieks":       "shriek",
		"breathe":       "breath",
		"breathes":      "breath",
		"breathing":     "breath",
		"exhale":        "exhales",
		"inhale":        "breath",
		"pants":         "pant",
		"panting":       "pant",
		"clear throat":  "throat-clearing",
		"clears throat": "throat-clearing",
		"throat clear":  "throat-clearing",
		"pause":         "short pause",
		"pause short":   "short pause",
		"pause long":    "long pause",
	}
	legacyBracketModifiers = func() map[string]string {
		m := make(map[string]string, len(geminiTagSet)+len(geminiTagAliases)+len(googleTagSet)+4)
		for k := range geminiTagSet {
			m[k] = k
		}
		for k, v := range geminiTagAliases {
			m[k] = v
		}
		for k := range googleTagSet {
			if _, ok := m[k]; !ok {
				m[k] = "short pause"
			}
		}
		for _, k := range []string{"whisper", "whispers", "whispering", "nervous", "excited", "angry", "sad", "happy", "cheerful", "sarcastic"} {
			if _, ok := m[k]; !ok {
				m[k] = ""
			}
		}
		return m
	}()

	angleTagRe   = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9 _-]*[A-Za-z0-9]|[A-Za-z])>`)
	bracketTagRe = regexp.MustCompile(`\[([A-Za-z][A-Za-z0-9 _-]*[A-Za-z0-9]|[A-Za-z])\]`)
)

// GeminiInlineTags returns a copy of the supported Gemini 3.8 inline vocal tags.
func GeminiInlineTags() []ModifierTag {
	return slices.Clone(geminiInlineTags)
}

// GoogleMarkupTags returns a copy of the supported Chirp 3 HD markup tags.
func GoogleMarkupTags() []ModifierTag {
	return slices.Clone(googleMarkupTags)
}

// Modifiers returns supported inline tags filtered by provider (empty returns all).
func Modifiers(provider string) []ModifierTag {
	switch provider {
	case ProviderGemini:
		return GeminiInlineTags()
	case ProviderGoogle:
		return GoogleMarkupTags()
	default:
		out := make([]ModifierTag, 0, len(geminiInlineTags)+len(googleMarkupTags))
		out = append(out, geminiInlineTags...)
		out = append(out, googleMarkupTags...)
		return out
	}
}

// HasGoogleMarkup reports whether text contains supported Chirp 3 HD markup pause tags.
func HasGoogleMarkup(text string) bool {
	for _, m := range bracketTagRe.FindAllStringSubmatch(text, -1) {
		if googleTagSet[m[1]] {
			return true
		}
	}
	return false
}

// ValidateInlineModifiers validates inline text modifiers locally for the selected provider
// and returns the unique detected canonical tag names in order of appearance.
func ValidateInlineModifiers(provider, text string) ([]string, error) {
	seen := map[string]bool{}
	var detected []string
	record := func(name string) {
		if !seen[name] {
			seen[name] = true
			detected = append(detected, name)
		}
	}

	for _, m := range angleTagRe.FindAllStringSubmatch(text, -1) {
		raw := m[1]
		if provider != ProviderGemini {
			return nil, Fail("INVALID_TEXT", fmt.Sprintf("Angle-bracket modifier <%s> is only supported with --provider gemini.", raw), false)
		}
		if geminiTagSet[raw] {
			record(raw)
			continue
		}
		lower := strings.ToLower(strings.Join(strings.Fields(raw), " "))
		if suggestion, ok := geminiTagAliases[lower]; ok {
			return nil, Fail("INVALID_TEXT", fmt.Sprintf("Unsupported Gemini inline tag <%s>; use <%s>. Run ttsi modifiers list.", raw, suggestion), false)
		}
		if geminiTagSet[lower] {
			return nil, Fail("INVALID_TEXT", fmt.Sprintf("Unsupported Gemini inline tag <%s>; tags are lowercase, e.g. <%s>. Run ttsi modifiers list.", raw, lower), false)
		}
		return nil, Fail("INVALID_TEXT", fmt.Sprintf("Unsupported Gemini inline tag <%s>. Run ttsi modifiers list for supported tags.", raw), false)
	}

	for _, m := range bracketTagRe.FindAllStringSubmatch(text, -1) {
		raw := m[1]
		lower := strings.ToLower(strings.Join(strings.Fields(raw), " "))
		if provider == ProviderGoogle {
			if googleTagSet[raw] {
				record(raw)
				continue
			}
			if googleTagSet[lower] {
				return nil, Fail("INVALID_TEXT", fmt.Sprintf("Unsupported Chirp 3 HD markup tag [%s]; use lowercase [%s].", raw, lower), false)
			}
			if strings.HasPrefix(lower, "pause") || strings.HasSuffix(lower, "pause") {
				return nil, Fail("INVALID_TEXT", fmt.Sprintf("Unsupported Chirp 3 HD markup tag [%s]. Supported tags: [pause], [pause short], [pause long].", raw), false)
			}
			if _, ok := legacyBracketModifiers[lower]; ok {
				return nil, Fail("INVALID_TEXT", fmt.Sprintf("Modifier [%s] is not supported by Chirp 3 HD; supported tags are [pause], [pause short], [pause long].", raw), false)
			}
			continue
		}
		if provider == ProviderGemini {
			if suggestion, ok := legacyBracketModifiers[lower]; ok {
				if suggestion != "" {
					return nil, Fail("INVALID_TEXT", fmt.Sprintf("Square-bracket modifier [%s] is not supported by Gemini 3.8 TTS; use <%s> instead.", raw, suggestion), false)
				}
				return nil, Fail("INVALID_TEXT", fmt.Sprintf("Square-bracket modifier [%s] is not supported by Gemini 3.8 TTS; pass sustained delivery with --style.", raw), false)
			}
			if strings.HasPrefix(lower, "pause") || strings.HasSuffix(lower, "pause") {
				return nil, Fail("INVALID_TEXT", fmt.Sprintf("Square-bracket modifier [%s] is not supported by Gemini 3.8 TTS; use <short pause> or <long pause>.", raw), false)
			}
		}
	}

	return detected, nil
}
