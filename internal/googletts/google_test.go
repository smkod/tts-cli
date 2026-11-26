package googletts

import (
	"strings"
	"testing"

	pb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/smkod/tts-cli/internal/core"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassification(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     codes.Code
		reason   string
		expected string
		retry    bool
	}{
		{"unavailable", codes.Unavailable, "", "PROVIDER_UNAVAILABLE", true},
		{"credential rejection", codes.Unauthenticated, "", "AUTH_INVALID", false},
		{"wrong voice", codes.InvalidArgument, "", "INVALID_VOICE", false},
		{"quota", codes.ResourceExhausted, "QUOTA_EXCEEDED", "QUOTA_EXHAUSTED", false},
		{"rate", codes.ResourceExhausted, "RATE_LIMIT_EXCEEDED", "RATE_LIMITED", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const secret = "private voice request"
			s := status.New(tc.code, secret)
			if tc.reason != "" {
				var err error
				s, err = s.WithDetails(&errdetails.ErrorInfo{Reason: tc.reason})
				if err != nil {
					t.Fatal(err)
				}
			}
			p := core.Public(classify(s.Err(), true))
			if p.Code != tc.expected || p.Retryable != tc.retry || strings.Contains(p.Message, secret) {
				t.Fatalf("wrong or unsafe classification: %+v", p)
			}
		})
	}
}

func TestBuildSynthesisInputMarkupRouting(t *testing.T) {
	plain := buildSynthesisInput("Hello world")
	if _, ok := plain.InputSource.(*pb.SynthesisInput_Text); !ok {
		t.Fatalf("expected SynthesisInput_Text for plain text, got %T", plain.InputSource)
	}
	markup := buildSynthesisInput("Hello [pause short] world")
	if m, ok := markup.InputSource.(*pb.SynthesisInput_Markup); !ok || m.Markup != "Hello [pause short] world" {
		t.Fatalf("expected SynthesisInput_Markup for pause tag, got %T", markup.InputSource)
	}
}
