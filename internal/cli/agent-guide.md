# ttsi agent guide

Use ttsi to generate Google Chirp 3 HD or Gemini 3.8 TTS speech from plain text
or structured dialogue scripts. This guide is bundled with the executable; no
repository, network, configuration or credentials are needed to read it. Use
`ttsi agent-guide --json` for the same instructions inside `data.instructions`
(Markdown, `data.guide_version: 1`).

## Discover before generating

- Run `ttsi capabilities --json` for supported providers, formats, locales,
  parameters and limits. Use `ttsi <command> --help` for exact flags. Do not
  invent parameters: SSML, speaking rate, pitch, custom pronunciation,
  temperature, seed and system instructions are not supported.
- Run `ttsi voices list --lang it-IT --json` to discover voice aliases for the
  requested locale. The catalog is local; use `--online` only when a live
  inventory check is needed (add `--provider gemini` for Gemini voices).
  Specify `--voice kore` for a stable voice; the unconfigured default is random
  selection, not a fixed voice.
- Run `ttsi modifiers list --json` to inspect supported inline vocal tags for
  Gemini 3.8 (`<laugh>`, `<sigh>`, `<short pause>`, etc.) and Chirp 3 HD pause
  markup (`[pause]`, `[pause short]`, `[pause long]`).
- Run `ttsi doctor --json` to inspect local setup. `ttsi doctor --online --json`
  checks API access without synthesizing. Google synthesis requires enabled
  Cloud Text-to-Speech and Application Default Credentials (`ttsi login google
  --help`); Gemini synthesis requires keys (`ttsi keys --help` or `GEMINI_API_KEY`).
  Do not expose secrets or store them in configuration.

## Generate one file

Preview: `ttsi generate "Come stai?" --lang it-IT --voice kore --dry-run --json`

Generate: `ttsi generate "Come stai?" --lang it-IT --voice kore --json`

Expressive Gemini 3.8:
`ttsi generate "Wait... <short pause> did you hear that? <gasp>" --provider gemini --voice kore --style "whispered, nervous" --json`

- Preserve the user's text. Each item must be nonblank valid UTF-8 and at most
  5,000 UTF-8 bytes (not 5,000 characters).
- Never embed prose stage directions (such as `"Say cheerfully: ..."`) or
  square-bracket tags (`[laughs]`) in Gemini 3.8 text; use `--style` for
  sustained emotion/pacing and `<...>` inline tags for momentary vocal events.
  Invalid or mismatched tags fail local validation before any network call.
- For Gemini 3.8 multi-turn or two-speaker dialogue, pass `--script script.json`
  with `turns` (`[{"speaker":"Host","style":"warm","text":"Hi!"}]`) and
  optional `speakers` (`{"Host":"puck","Guest":"kore"}`).
- Use `--lang auto` with `--provider gemini` for automatic multilingual
  detection; omit `--lang` unless you want to pin an accent/locale.
- Use `ttsi voices design "<description>" --json` to create a custom Gemini
  prompted voice (`voice_<id>`) usable with `--voice`.
- Use explicit `generate` and put flags before `--` when text resembles a command
  or starts with a dash: `ttsi generate --lang en-US --json -- "-hello"`.
- MP3 is the default; use `--format wav` for uncompressed audio. Use
  `--output <path>` for an explicit destination with the matching extension.
  Prefer per-command flags to changing persistent settings without permission.
- Parse `data.file` (absolute path) and `data.cached`; never guess an output path.
  A dry run creates no audio file, so do not upload its predicted path. Random
  voice selection may differ between preview and generation; pin a voice when
  reproducibility matters.
- Keep cache enabled unless regeneration was requested. Cache hits need no
  credentials. On OUTPUT_EXISTS, choose a new destination; do not delete
  existing audio or bypass cache to work around the conflict.

## Generate a batch

Write a JSON file such as:

```json
{"items":[{"id":"note-42","text":"Come stai?","language":"it-IT","voice":"kore"},{"id":"dramatic-1","provider":"gemini","voice":"kore","style":"warm and cheerful","text":"Welcome back! <chuckle>"}]}
```

Items can also use `"turns"` and optional `"speakers"` instead of `"text"` for
multi-turn or two-speaker Gemini dialogue.

Run `ttsi batch items.json --json` (add `--dry-run` to preview).
IDs must be nonempty and unique. Per-item language, voice, provider, model,
style and format are optional. The batch always emits a JSON manifest; parse
`data.items` by ID and use only successful items with real files. Exit 3 means
at least one item failed, not that every item failed. Correct the cause and
resubmit only failed items as needed.

## Interpret results and recover

- Use `--json` for automation. Stdout contains an envelope with `schema_version`,
  `success` and `data` or `error`; diagnostics go to stderr. Help stays textual.
  Tolerate additive fields. Branch on `error.code`, not message text.
- Exit codes: 0 success; 1 operational failure; 2 invalid usage, configuration or
  input; 3 batch with failed items. Partial batches have `success:false` and
  per-item errors in `data.items`, not necessarily a top-level error.
- Correct invalid input before retrying. Stop on authentication, permission,
  hard quota or permanent provider errors until the cause is resolved. Transient
  synthesis failures are already retried at most twice; inspect `error.retryable`
  and avoid unbounded retry loops.
- Do not claim a file was generated on a failed result or a dry run. Uploading
  audio or changing another application is a separate action requiring the
  user's requested scope; this CLI does not perform it.
