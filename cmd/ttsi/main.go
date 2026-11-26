package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/smkod/tts-cli/internal/cli"
	"github.com/smkod/tts-cli/internal/geminitts"
	"github.com/smkod/tts-cli/internal/googletts"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	backend := &cli.Router{
		Google: googletts.New(),
		Gemini: geminitts.New(),
	}
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, backend)
	stop()
	os.Exit(code)
}
