package cli

import (
	"context"

	"github.com/smkod/tts-cli/internal/core"
)

// ProviderVoiceLister is an optional interface for backends that support listing voices per provider.
type ProviderVoiceLister interface {
	ListVoicesForProvider(ctx context.Context, provider, lang string) ([]core.Voice, error)
}

// VoiceDesigner is an optional interface for backends that support prompt-based voice creation.
type VoiceDesigner interface {
	DesignVoice(ctx context.Context, req core.VoiceDesignRequest) (core.VoiceDesignResult, error)
}

// Router dispatches synthesis and voice listing to provider-specific backends.
type Router struct {
	Google core.Backend
	Gemini core.Backend
}

func (r *Router) Synthesize(ctx context.Context, req core.Request) ([]byte, error) {
	if req.Provider == core.ProviderGemini {
		if r.Gemini == nil {
			return nil, core.Fail("INVALID_CONFIG", "Gemini provider is not initialized.", false)
		}
		return r.Gemini.Synthesize(ctx, req)
	}
	if r.Google == nil {
		return nil, core.Fail("INVALID_CONFIG", "Google provider is not initialized.", false)
	}
	return r.Google.Synthesize(ctx, req)
}

func (r *Router) ListVoices(ctx context.Context, lang string) ([]core.Voice, error) {
	return r.ListVoicesForProvider(ctx, core.ProviderGoogle, lang)
}

func (r *Router) ListVoicesForProvider(ctx context.Context, provider, lang string) ([]core.Voice, error) {
	if provider == core.ProviderGemini {
		if r.Gemini == nil {
			return nil, core.Fail("INVALID_CONFIG", "Gemini provider is not initialized.", false)
		}
		return r.Gemini.ListVoices(ctx, lang)
	}
	if r.Google == nil {
		return nil, core.Fail("INVALID_CONFIG", "Google provider is not initialized.", false)
	}
	return r.Google.ListVoices(ctx, lang)
}

func (r *Router) DesignVoice(ctx context.Context, req core.VoiceDesignRequest) (core.VoiceDesignResult, error) {
	if r.Gemini == nil {
		return core.VoiceDesignResult{}, core.Fail("INVALID_CONFIG", "Gemini provider is not initialized.", false)
	}
	d, ok := r.Gemini.(VoiceDesigner)
	if !ok {
		return core.VoiceDesignResult{}, core.Fail("INVALID_CONFIG", "Gemini provider does not support voice design.", false)
	}
	return d.DesignVoice(ctx, req)
}

func (r *Router) Close() error {
	var firstErr error
	if r.Google != nil {
		if err := r.Google.Close(); err != nil {
			firstErr = err
		}
	}
	if r.Gemini != nil {
		if err := r.Gemini.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
