# tts-cli

A starting point for a text-to-speech CLI written in Go. It currently displays help and does not generate audio yet.

## Local development

Requires Go 1.26.0 or later. From the repository root:

```sh
go run . --help
```

`main.go` contains the entry point. The `run` function accepts arguments and output writers so CLI behavior can be tested without launching a separate process. There are no external dependencies.
