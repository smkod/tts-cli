package core

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateInlineModifiersGemini(t *testing.T) {
	tags, err := ValidateInlineModifiers(ProviderGemini, "Wait... <short pause> did you hear that? <gasp> <short pause>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(tags, []string{"short pause", "gasp"}) {
		t.Fatalf("unexpected detected tags: %v", tags)
	}

	// Math comparisons and non-modifier brackets in prose remain valid.
	tags, err = ValidateInlineModifiers(ProviderGemini, "Check 2 < 3 and 5 > 1 for [Noun] item [1].")
	if err != nil || len(tags) != 0 {
		t.Fatalf("unexpected result for prose/math: %v %v", tags, err)
	}

	for _, tc := range []struct {
		name, text, wantSubstr string
	}{
		{"unknown angle tag", "Hello <sniff> world", "<sniff>"},
		{"plural alias suggestion", "Hello <laughs> world", "<laugh>"},
		{"uppercase tag", "Hello <LAUGH> world", "<laugh>"},
		{"legacy square bracket laugh", "Hello [laughs] world", "<laugh>"},
		{"legacy square bracket pause", "Hello [pause short] world", "<short pause>"},
		{"square bracket style cue", "Hello [whispering] world", "--style"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateInlineModifiers(ProviderGemini, tc.text)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			p := Public(err)
			if p.Code != "INVALID_TEXT" || !strings.Contains(p.Message, tc.wantSubstr) {
				t.Fatalf("unexpected error: %+v", p)
			}
		})
	}
}

func TestValidateInlineModifiersGoogle(t *testing.T) {
	tags, err := ValidateInlineModifiers(ProviderGoogle, "Hello [pause short] world [pause long].")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(tags, []string{"pause short", "pause long"}) {
		t.Fatalf("unexpected detected tags: %v", tags)
	}
	if !HasGoogleMarkup("Hello [pause] world") || HasGoogleMarkup("Hello world") {
		t.Fatal("HasGoogleMarkup returned unexpected result")
	}

	for _, tc := range []struct {
		name, text string
	}{
		{"gemini angle tag", "Hello <laugh> world"},
		{"invalid pause bracket", "Hello [pause medium] world"},
		{"uppercase pause bracket", "Hello [PAUSE] world"},
		{"unsupported emotion bracket", "Hello [laugh] world"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateInlineModifiers(ProviderGoogle, tc.text)
			if err == nil || Public(err).Code != "INVALID_TEXT" {
				t.Fatalf("expected INVALID_TEXT error, got %v", err)
			}
		})
	}
}
