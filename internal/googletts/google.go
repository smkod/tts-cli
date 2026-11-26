package googletts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	pb "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/googleapis/gax-go/v2"
	"github.com/googleapis/gax-go/v2/apierror"
	"github.com/smkod/tts-cli/internal/core"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Google struct {
	mu      sync.Mutex
	client  *texttospeech.Client
	initErr error
}

func New() *Google { return &Google{} }

func (g *Google) get(ctx context.Context) (*texttospeech.Client, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g.client != nil {
		return g.client, nil
	}
	if g.initErr != nil {
		return nil, g.initErr
	}
	client, err := texttospeech.NewClient(ctx,
		option.WithEndpoint("texttospeech.googleapis.com:443"),
		option.WithGRPCDialOption(grpc.WithDisableRetry()),
		option.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		g.initErr = core.Fail("AUTH_SETUP", "Cannot initialize Google Cloud TTS. Check Application Default Credentials and project setup.", false)
		return nil, g.initErr
	}
	g.client = client
	return client, nil
}
func (g *Google) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.client == nil {
		return nil
	}
	return g.client.Close()
}

func noRetry() gax.Retryer { return nil }

func buildSynthesisInput(text string) *pb.SynthesisInput {
	if core.HasGoogleMarkup(text) {
		return &pb.SynthesisInput{InputSource: &pb.SynthesisInput_Markup{Markup: text}}
	}
	return &pb.SynthesisInput{InputSource: &pb.SynthesisInput_Text{Text: text}}
}

func (g *Google) Synthesize(ctx context.Context, r core.Request) ([]byte, error) {
	var encoder string
	if r.Format == core.FormatMP3 {
		var err error
		encoder, err = FindFFmpeg(ctx)
		if err != nil {
			return nil, err
		}
	}
	c, err := g.get(ctx)
	if err != nil {
		return nil, err
	}
	res, err := c.SynthesizeSpeech(ctx, &pb.SynthesizeSpeechRequest{
		Input:       buildSynthesisInput(r.Text),
		Voice:       &pb.VoiceSelectionParams{LanguageCode: r.Language, Name: r.Voice},
		AudioConfig: &pb.AudioConfig{AudioEncoding: pb.AudioEncoding_LINEAR16},
	}, gax.WithRetry(noRetry))
	if err != nil {
		return nil, classify(err, true)
	}
	if r.ReportUsage != nil {
		r.ReportUsage(core.Usage{Characters: utf8.RuneCountInString(r.Text)})
	}
	if r.Format == core.FormatMP3 {
		return EncodeMP3(ctx, encoder, res.GetAudioContent(), r.Bitrate)
	}
	return res.GetAudioContent(), nil
}

func (g *Google) ListVoices(ctx context.Context, lang string) ([]core.Voice, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := g.get(ctx)
	if err != nil {
		return nil, err
	}
	res, err := c.ListVoices(ctx, &pb.ListVoicesRequest{LanguageCode: lang}, gax.WithRetry(noRetry))
	if err != nil {
		return nil, classify(err, false)
	}
	out := []core.Voice{}
	seen := map[string]bool{}
	for _, x := range res.GetVoices() {
		v, ok := core.ParseVoice(x.GetName())
		if ok && !seen[v.Name] && (lang == "" || strings.EqualFold(lang, v.Language)) {
			seen[v.Name] = true
			out = append(out, v)
		}
	}
	core.SortVoices(out)
	return out, nil
}

func classify(err error, synthesis bool) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return core.Fail("PROVIDER_TIMEOUT", "Google Cloud TTS timed out.", true)
	}
	st := status.Convert(err)
	parsed, ok := apierror.FromError(err)
	if ok {
		st = parsed.GRPCStatus()
	}
	switch st.Code() {
	case codes.Canceled:
		return context.Canceled
	case codes.Unauthenticated:
		return core.Fail("AUTH_INVALID", "Google rejected the credentials. Check Application Default Credentials.", false)
	case codes.PermissionDenied:
		return core.Fail("PERMISSION_DENIED", "Check the Google Cloud project, API enablement, billing, and permissions.", false)
	case codes.ResourceExhausted:
		// Unspecified exhaustion is treated as a hard limit to avoid additional paid requests.
		transient, hard := false, false
		if ok {
			d := parsed.Details()
			transient = d.RetryInfo != nil
			switch d.ErrorInfo.GetReason() {
			case "RATE_LIMIT_EXCEEDED", "USER_RATE_LIMIT_EXCEEDED":
				transient = true
			case "QUOTA_EXCEEDED", "DAILY_LIMIT_EXCEEDED", "BILLING_DISABLED":
				hard = true
			}
		}
		if transient && !hard {
			return core.Fail("RATE_LIMITED", "Google Cloud TTS rate limit exceeded.", true)
		}
		return core.Fail("QUOTA_EXHAUSTED", "Google Cloud TTS quota is exhausted.", false)
	case codes.DeadlineExceeded:
		return core.Fail("PROVIDER_TIMEOUT", "Google Cloud TTS timed out.", true)
	case codes.Unavailable:
		return core.Fail("PROVIDER_UNAVAILABLE", "Google Cloud TTS is temporarily unavailable.", true)
	case codes.Internal:
		return core.Fail("PROVIDER_ERROR", "Google Cloud TTS encountered a transient error.", true)
	case codes.NotFound, codes.InvalidArgument:
		if synthesis {
			msg := strings.ToLower(st.Message())
			if st.Code() == codes.NotFound || (strings.Contains(msg, "voice") && !strings.Contains(msg, "encoding")) {
				return core.Fail("INVALID_VOICE", "Google rejected the selected Chirp 3 HD voice. Check the online voice list.", false)
			}
		}
		return core.Fail("PROVIDER_REJECTED", "Google rejected the request.", false)
	}
	return core.Fail("PROVIDER_ERROR", "Google Cloud TTS failed.", false)
}
