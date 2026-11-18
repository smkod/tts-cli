package main

import (
	"context"
	"github.com/smkod/tts-cli/internal/cli"
	"github.com/smkod/tts-cli/internal/googletts"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, googletts.New())
	stop()
	os.Exit(code)
}
