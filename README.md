# tts-cli

A lightweight text-to-speech CLI for Windows, macOS, and Linux. It generates WAV or MP3
audio using Google Chirp 3 HD or Gemini 3.8 TTS, with local caching, batch manifests,
expressive style/modifier validation, and stable JSON output. It does not depend on Anki.

## Install and authenticate

Download the package for your platform from [GitHub Releases](https://github.com/smkod/tts-cli/releases).
On Windows x64, run the setup executable and open a new terminal so it picks
up the updated user PATH. If Google Cloud CLI is not already on your PATH, you
can select the optional task to download and install it for your user. This
requires internet access and does not sign you in. Uninstalling `ttsi` does not
remove Google Cloud CLI. On macOS or Linux, extract the matching archive,
run `chmod +x ttsi` if needed, and place `ttsi` in a directory on your PATH.
Prebuilt packages do not require Go. Compare the download's SHA-256 hash with
`SHA256SUMS.txt` in the release assets.

### Install from source

With Go 1.26 or later, run `go install ./cmd/ttsi` from a checkout and put Go's bin
directory on your PATH.

### Configure Google Cloud access

1. Open the [Google Cloud Console](https://console.cloud.google.com/). Use the
   project selector at the top to select an existing project or choose **New
   Project**, enter a name, and click **Create**. Select the new project before
   continuing.
2. Open **Billing** and make sure the selected project is linked to an active
   billing account. Link one if needed; Cloud Text-to-Speech requires billing.
3. In the same project, open the [Cloud Text-to-Speech API page](https://console.cloud.google.com/apis/library/texttospeech.googleapis.com)
   and click **Enable**. See [Google's setup guide](https://docs.cloud.google.com/text-to-speech/docs/get-started)
   for details.
4. Open the console's [Welcome page](https://console.cloud.google.com/welcome)
   with your project selected and copy the **Project ID** shown near the Welcome
   heading. Use the ID (for example, `my-tts-project-123`), not the display name
   or numeric project number. See [Google's guide to finding project identifiers](https://docs.cloud.google.com/resource-manager/docs/view-update-projects#find_the_project_name_number_and_id).

With [Google Cloud CLI](https://docs.cloud.google.com/sdk/docs/install) on your
PATH, replace `YOUR_PROJECT_ID` with the ID you copied and run:

```sh
ttsi login google --project YOUR_PROJECT_ID
ttsi doctor --online --json
```

The login command opens Google's sign-in flow through
`gcloud auth application-default login`. With `--project`, it also sets the
ADC quota project after sign-in. That project provides quota and is billed for
API usage. Sign in with an account that has `serviceusage.services.use` on the
project. If setting the quota project fails because this permission is missing,
ask a project administrator for the **Service Usage Consumer** role
(`roles/serviceusage.serviceUsageConsumer`) on that project.

This updates local ADC, which can be shared
with other tools. If `GOOGLE_APPLICATION_CREDENTIALS` points to a credentials
file, it takes precedence over those local credentials.

See [Cloud TTS authentication](https://docs.cloud.google.com/text-to-speech/docs/authentication).
On Google Cloud, use Application Default Credentials (ADC) with an attached
identity instead of storing keys in the configuration file. The online doctor
checks API access without synthesizing audio; it does not prove synthesis
permission or available quota.

### Configure Gemini 3.8 TTS access

For `--provider gemini` (`gemini-3.8-flash-tts` and `gemini-3.8-flash-lite-tts`),
configure one or more Gemini API keys using `ttsi keys add` or `GEMINI_API_KEY`:

```sh
ttsi keys add --tier free YOUR_FREE_KEY
ttsi keys add --tier paid YOUR_PAID_KEY
ttsi keys list
```

## Commands

Start with one MP3. Once you've configured credentials, run:

```sh
ttsi "Hello, how are you?" -o hello.mp3
```

| Command | Shortcut | What it does |
| --- | --- | --- |
| `ttsi generate [<text>]` | `ttsi g [<text>]` | Turns one text argument or `--script <file>` into one audio file. Use `--output` (`-o`) to name the file and `--style` for Gemini delivery direction. |
| `ttsi batch <file>` | `ttsi b <file>` | Reads a [JSON batch file](#batch-and-json-contract-v1), generates audio files, and prints a JSON report to stdout even without `--json`. Use `--output-dir` for the destination and `--jobs` for parallelism. |
| `ttsi voices list` | `ttsi v ls` | Lists Chirp 3 HD voice IDs and aliases from the bundled catalog. Use `--lang` to filter by exact locale or `--online` to query Google or Gemini. |
| `ttsi voices design <description>` | — | Creates a custom Gemini 3.8 TTS prompted voice from a natural-language description and optionally saves its preview audio (`--output`). |
| `ttsi modifiers list` | `ttsi m ls` | Lists supported inline vocal tags for Gemini 3.8 TTS (`<...>`) and pause markup for Chirp 3 HD (`[pause...]`). |
| `ttsi providers list` | `ttsi p ls` | Lists implemented providers (`google` and `gemini`). |
| `ttsi keys add/list/remove/reset` | `ttsi k ...` | Manages Gemini API keys and free/paid tier failover cooldowns. |
| `ttsi login google [--project ID]` | — | Signs in using Google Cloud CLI and optionally sets the ADC quota project. |
| `ttsi agent-guide` | — | Prints bundled instructions for AI agents without network or credentials. Add `--json` for structured output. |
| `ttsi capabilities` | `ttsi cap` | Summarizes supported formats and limits. Use `--json` for details such as locales and supported parameters. |
| `ttsi config show` | `ttsi cfg sh` | Shows the config path and effective settings. With `--json`, it also shows saved YAML values separately. |
| `ttsi config set <key=value>...` | `ttsi cfg s <key=value>...` | Validates and saves one or more settings together, for example `ttsi config set language=it-IT voice=kore`. |
| `ttsi config unset <key>...` | `ttsi cfg u <key>...` | Removes saved settings, for example `ttsi config unset language voice`. Environment overrides and defaults still apply. |
| `ttsi doctor` | `ttsi d` | Checks config, directory writability, and local credential hints. Add `--online` to probe Google or Gemini without generating audio. |
| `ttsi help [command]` | — | Shows help. Use `ttsi --help` for the top-level list or, for example, `ttsi batch --help` for flags. |
| `ttsi completion <shell>` | — | Generates a completion script for `bash`, `zsh`, `fish`, or `powershell`. See `ttsi completion --help`. |

Both `generate` and `batch` accept `--lang`, `--voice`, `--provider`, `--model`,
`--key-tier`, `--style`, `--format`, `--cache-dir`, `--no-cache`, and `--dry-run`.
`generate` also accepts `--script <file>` for multi-turn or two-speaker Gemini
scripts. The global flags are `--config PATH` to select a YAML file and `--json`
for machine-readable results.

```sh
ttsi generate "Come stai?" --lang it-IT --voice kore --output italian.mp3
ttsi generate "Come stai?" --lang it-IT --format wav -o italian.wav
ttsi generate "Wait... <short pause> did you hear that? <gasp>" --provider gemini --voice kore --style "whispered, nervous" -o suspense.mp3
ttsi generate --provider gemini --script dialogue.json -o podcast.mp3
ttsi generate "Hello there, [pause short] how can I help?" -o paused.mp3
ttsi voices design "Warm storyteller in his 50s" --name "Narrator" --lang en-US --gender male -o preview.wav
ttsi modifiers list --provider gemini
ttsi config set format=wav
ttsi generate --dry-run --json -- "-text beginning with a dash"
ttsi batch sentences.json --jobs 4 --output-dir audio
ttsi voices list --lang it-IT
```

`ttsi "<text>"` is shorthand for `ttsi generate "<text>"`. Put the text before
generation-specific flags when using the shorthand. To speak a command name or
alias literally, use the explicit form: `ttsi generate -- "g"`.

By default, each generation (including each batch item) randomly picks a voice
for its locale from the bundled catalog. A voice set in the configuration,
environment, or batch item still applies unless overridden. Pass `--voice random`
to force random selection or `--voice <alias>` to repeat a specific voice.
Use `ttsi voices list --lang it-IT` to see the IDs and aliases for a locale.
Aliases resolve against the bundled
catalog and are case-insensitive. A full Chirp 3 HD voice name can supply the locale
when no language was set. Use `voices list --online` to discover full IDs before a
local catalog update. The local catalog is a dated snapshot from
[Google's published voice list](https://docs.cloud.google.com/text-to-speech/docs/list-voices-and-types).
Offline listing is not a live availability guarantee.

The CLI accepts nonblank plain UTF-8 text, up to
[5,000 bytes](https://docs.cloud.google.com/text-to-speech/quotas) per item.
It preserves the original text, including surrounding whitespace.
With `--provider gemini`, you can combine `--style` (sustained delivery direction),
inline angle-bracket vocal modifiers (`<short pause>`, `<laugh>`, `<sigh>`, `<gasp>`,
etc. — run `ttsi modifiers list --provider gemini`), `--lang auto` for automatic
multilingual detection, and `--script` JSON files with multi-turn or two-speaker
`turns` and `speakers`. With `--provider google`, `[pause]`, `[pause short]`, and
`[pause long]` are sent via Chirp 3 HD markup. Invalid or provider-mismatched tags
are rejected locally before calling any remote API.
Synthesis uses the synchronous global endpoint. MP3 at 96 kbps is the default and is the most widely compatible audio format
across [Anki clients](https://docs.ankiweb.net/media.html). WAV (`LINEAR16`,
uncompressed 16-bit PCM) uses the native sample rate. SSML, pitch, speaking rate,
and LLM sampling parameters (`temperature`, `seed`, `systemInstruction`) are not exposed.

## Interactive terminal interface

Run `ttsi tui` in an interactive terminal to open the workspace with Generate,
Usage, and Config views. Use
`ttsi tui --config PATH` or `TTS_CONFIG` to select the same YAML configuration
as other commands; environment settings override YAML defaults. Editing the
Generate form affects only the current session and does not save configuration,
while the Config view inspects and updates the shared YAML file via the same
validation and locking path as `ttsi config set` and `ttsi config unset`.

F1 opens Generate; F2 opens Usage; F3 opens Config. Ctrl+P previews without
provider calls or file writes. Ctrl+R refreshes local usage (or reloads config
from disk in Config); in Usage, `r` also refreshes, while arrow keys and Page Up
/ Page Down scroll the report. In Config, Tab / Shift+Tab or Up / Down moves
between settings, Left / Right cycles supported choices, Ctrl+U marks the
focused field to be unset, and Ctrl+S saves changes to YAML (syncing untouched
Generate defaults).
Tab / Shift+Tab moves between fields. Ctrl+G generates from any field, or press
Enter on Generate. Esc cancels an active generation. Ctrl+Q or Ctrl+C quits,
canceling and waiting for any active generation before closing the backend.
The form supports provider, model, language, voice, style, output path, format,
bitrate, and key tier; smaller terminals display the focused field.
Left / Right cycles provider, format, or bundled voices when that field is
focused; voice names also accept direct input.

This interface generates one plain-text audio file at a time using the same
validation, cache, encoding, and provider pipeline as `generate`. It does not
provide batch, script, key-management, or voice-design screens. Generation can
incur provider charges. Provider warnings are suppressed while the interface
is active to protect terminal rendering; generation errors appear in the form.

Usage shows combined estimates, service totals, and Gemini API-key configurations
(including configured keys with no recorded requests). Names, tiers, and stable
nonsecret identifiers identify configurations; API keys and key substrings are
never displayed. Attribution follows the key that actually served a successful
response, including failover. Historical records without attribution remain
`unattributed`, as does Google default-credential usage: account/project identity
is not known. Usage refreshes on entry and after generation.

These are local gross paid-list estimates, not a bill or a free-tier balance.
Free tiers, credits, discounts, and taxes are excluded, and local tracking can
be incomplete. The same breakdown is available through `ttsi usage --json` in
the additive `configurations` field, grouped by provider then credential ID.

`--json`, piped stdin, and redirected stdout are not supported by `tui`; use
`ttsi generate` or `ttsi batch` for automation. Running `ttsi` without arguments
still shows help, and the existing text shorthand is unchanged. To synthesize
the reserved command name literally, use `ttsi generate -- "tui"`.

## Configuration and files

The precedence is **flags → batch item → environment → YAML → built-in defaults**.
The default file is `tts/config.yaml` under Go's
[OS user config directory](https://pkg.go.dev/os#UserConfigDir).
Use `--config PATH` or `TTS_CONFIG` to select a different file. An explicitly
selected missing file is an error except for edits, which can create it.
`config show --json` reports saved values separately from effective settings. `set`
validates settings together, and `unset` deletes saved values.
YAML edits rewrite the file and do not preserve comments. An interrupted edit
can leave a `.lock` file; remove it only after confirming no writer is active.

| YAML key | Environment | Default |
| --- | --- | --- |
| `provider` | `TTS_PROVIDER` | `google` |
| `model` | `TTS_MODEL` | `gemini-3.8-flash-tts` (when `provider=gemini`) |
| `key_tier` | `TTS_KEY_TIER` | `auto` (`auto`, `free`, or `paid`) |
| `language` | `TTS_LANGUAGE` | `en-US` (or `auto` for Gemini 3.8 auto-detection) |
| `voice` | `TTS_VOICE` | `random` |
| `style` | `TTS_STYLE` | Unset (Gemini 3.8 sustained delivery direction) |
| `format` | `TTS_FORMAT` | `mp3` (`wav` is also supported) |
| `output_dir` | `TTS_OUTPUT_DIR` | Current directory |
| `cache_dir` | `TTS_CACHE_DIR` | `tts` under the OS user cache directory |
| `jobs` | `TTS_JOBS` | `4` (range: 1–16) |

Relative paths resolve against the working directory. The CLI does not load
`.env` files automatically. Never store credentials in YAML.

Generated files default to `<request-hash>.wav` or `<request-hash>.mp3` in the
output directory. Cache entries are separate by format. An explicit output
path must end with the extension for the selected format.
The selected voice is part of the cache key and is reported as `data.voice` in
JSON. A dry run also picks a voice; pass that voice ID with `--voice` if the
actual run must match the preview.
Use `generate --output` or `batch --output-dir` to change the destination.
Output publication requires filesystem hard-link support. Existing byte-identical
files are reused, but different files or symlinks are never overwritten; choose
a new output path on conflict. A cache miss can require synthesis before a
byte-for-byte output comparison is possible.

Use `--dry-run` to inspect the request without contacting Google or writing files.
A valid cache hit also works without Google credentials. `--no-cache` skips
cache reads and writes; use a new output path if regenerating an existing file.
The cache stores audio and checksums, not text or credentials. Keep the output
and cache directories private: audio may contain sensitive content and names
contain deterministic hashes. Files use restrictive creation modes where
supported; on Windows, keep the parent directory's ACL private. Corrupt
entries are ignored with a warning; remove them while the CLI is idle to
restore caching. Cache files are not automatically evicted.

## Batch and JSON contract (v1)

A batch file is UTF-8 JSON up to 32 MiB:

```json
{"items":[{"id":"note-101","text":"How are you?"},{"id":"note-102","text":"Come stai?","language":"it-IT","voice":"kore"},{"id":"dialogue-103","provider":"gemini","speakers":{"Host":"puck","Guest":"kore"},"turns":[{"speaker":"Host","style":"warm","text":"Welcome! <short pause>"},{"speaker":"Guest","style":"tired","text":"<sigh> Thanks."}]}]}
```

IDs must be nonempty and unique. Each item must provide either `text` or `turns`
(never both). Language, voice, provider, model, key_tier, style, speakers,
bitrate, and format are optional. Bad items do not prevent valid items from
running. Transient synthesis failures are retried at most twice; permanent
errors and hard quota failures are not retried.
The batch always prints an ordered JSON manifest to stdout. To recover from
exit 3, inspect each item's error, fix the cause, and resubmit failed IDs. A
normal rerun can reuse cached successful audio.

Operational commands with `--json` print one JSON object to stdout. Help and
shell completion remain textual. Diagnostics go to stderr. The envelope is
`{"schema_version":1,"success":true,"data":{...}}`, or
`{"schema_version":1,"success":false,"error":{"code":"…","message":"…","retryable":false}}`.
A partial batch has `success:false` with a manifest in `data` instead of a
top-level error. Doctor failures can include both check details and an error.

| Operation | `data` |
| --- | --- |
| `generate` | `file` (absolute), `provider`, `model`, `voice`, `language`, `style`, `turns`, `speakers`, `detected_tags`, `format`, `cached`, `characters` (Unicode code points), `cache_status` (`hit`, `miss`, `corrupt` for preview, or `bypassed`), and optional `dry_run` |
| `batch` | `items` (input order), `total`, `succeeded`, `failed`, optional `dry_run`; each item has `id`, `success` and either generation `data` or `error` |
| `voices list` | `source` (`local`, `google`, or `gemini`), `voices` (`name`, `language`, `alias`), and `catalog_date` for the snapshot |
| `voices design` | `id`, `name`, `display_name`, `model`, `language_code`, `gender`, `expire_time`, `sample_file` |
| `modifiers list` | `tags` (`tag`, `name`, `category`, `provider`, `description`), `gemini_style_supported`, `gemini_style_examples` |
| `providers list` | `providers` (provider IDs) |
| `login google` | `provider` (Google provider ID), `quota_project` (explicit project ID, or empty if omitted) |
| `agent-guide` | `guide_version`, `format` (`markdown`), `instructions` (bundled usage guide) |
| `capabilities` | `providers`, `default_provider`, `voice_family`, `gemini_models`, `key_tiers`, `languages`, `voices_command`, `modifiers_command`, `catalog_source`, `catalog_date`, `input_formats`, `output_formats`, `default_output_format`, `max_text_bytes`, `synthesis_parameters`, `unsupported_parameters`, `gemini_capabilities`, `google_capabilities`, `batch`, `cache`, `dry_run` |
| `config show/set/unset` | `file`, `stored` (saved strings), `effective` (resolved settings) |
| `doctor` | `checks` (`check`, `status`, `message`), `online` |

Exit codes: `0` success; `1` operational failure; `2` usage, configuration,
or input error; `3` completed batch with at least one failed item.

- **Correct the input:** `INVALID_USAGE`, `INVALID_CONFIG`, `INVALID_INPUT`,
  `INVALID_TEXT`, `INVALID_VOICE`, `INVALID_BATCH`, `INVALID_ITEM`, `DUPLICATE_ID`.
- **Fix local setup:** `FILE_IO`, `OUTPUT_EXISTS`, `CONFIG_BUSY` (retry after the writer finishes).
- **Fix Google setup or quota:** `AUTH_SETUP`, `AUTH_INVALID`, `PERMISSION_DENIED`,
  `QUOTA_EXHAUSTED`, `PROVIDER_REJECTED`, `INVALID_AUDIO`.
- **Inspect retryability:** `RATE_LIMITED`, `PROVIDER_TIMEOUT`, `PROVIDER_UNAVAILABLE`,
  `PROVIDER_ERROR` (may be permanent), `CANCELED`, `INTERNAL`.

Readers should tolerate additive fields and use `error.code`, not message text,
for branching. A change to an existing field's meaning or shape requires a
new schema version.

## Agents and development

Run `ttsi agent-guide` to obtain self-contained usage instructions directly from
the executable, or `ttsi agent-guide --json` to read them in `data.instructions`.

See [the agent skill](skills/tts-cli/SKILL.md) for CLI automation and optional
AnkiConnect interoperability. Anki, a GUI, and additional TTS providers are
not required or bundled. The future desktop settings UI can reuse these same
settings operations without introducing a separate configuration path.

Run the credential-free tests with `go test ./...` when verification is
authorized. To verify actual Google synthesis, explicitly opt in by running
`ttsi g "Hello world" --output smoke-test.mp3` after configuring ADC. A cache
miss can incur billing. No automated test calls the live Google Cloud API.

### Release packaging

To test packaging without publishing a release, run the **Release** workflow
manually in GitHub Actions once the workflow is on the default branch. It builds
the packages, smoke-tests the Windows installer and uninstaller without Google
credentials, and uploads the **tts-release** artifact. Download that artifact
to try the installer on a separate Windows machine. The installer is unsigned.

To publish, push a version tag such as `v0.1.0` on the intended commit.
A tag such as `v0.1.0-rc.1` publishes a prerelease. GitHub-hosted runners build
and publish the packages; no local installer toolchain or GitHub CLI is needed.

## Local MP3 encoding with FFmpeg

MP3 generation requests uncompressed LINEAR16 WAV from Google and encodes it
locally with FFmpeg `libmp3lame`. Intermediate WAV audio stays in memory, not in
an output file. WAV output remains available and does not require FFmpeg.
FFmpeg beside the `ttsi` executable takes precedence over PATH. MP3 cache hits
and dry runs do not require it. Missing or incompatible FFmpeg is rejected before
making a billable synthesis request.

```sh
ttsi generate "Hello" --bitrate 96 -o hello.mp3
ttsi config set bitrate=128
```

The `bitrate` YAML setting, `TTS_BITRATE` environment variable, and `--bitrate`
flag select kbps. Batch items may include a numeric `"bitrate": 64` field.
Precedence is flags, batch item, environment, YAML, default (96).
Supported rates: 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144,
160, 192, 224, 256, 320. Bitrate is included in MP3 cache keys; old direct-Google
MP3 entries are not reused. Above 160 kbps the encoder resamples to 48 kHz for
MPEG-1 compatibility; this does not add source detail. Lower rates save space
but lose more information. Bitrate does not affect WAV output.

Release maintainers can bundle reviewed static FFmpeg binaries for every target
in `FFMPEG_BUNDLE_ROOT/<os>-<arch>/`, with `ffmpeg` (`ffmpeg.exe` on Windows),
`SHA256SUMS.txt`, and `LICENSES/`. The checksum file must contain the executable's
SHA-256 followed by its filename. LICENSES must contain the distribution notices,
license texts, build configuration, upstream source URL and corresponding-source
availability instructions required by that exact build. Verify redistribution
compliance before publishing; a checksum alone does not establish provenance.
No unverified third-party downloads are performed by the release workflow.

If `FFMPEG_BUNDLE_ROOT` is unset, release packaging warns and omits FFmpeg;
users of those packages must install FFmpeg with `libmp3lame` separately for MP3
output. Once a bundle root is supplied, missing assets or invalid checksums and
notices fail packaging rather than silently publishing an incomplete bundle.

## Estimated cost and local usage

```powershell
ttsi "Hello" --show-cost
ttsi generate "Hello" --show-cost --json
ttsi batch manifest.json --show-cost
ttsi usage
ttsi usage --json
```

`--show-cost` is off by default. It adds a `cost` object to generation JSON;
for human output it writes an estimate to stderr, leaving the audio path on
stdout unchanged. Batch JSON includes each item's cost when requested, with
per-item summaries on stderr. `--show-cost=false` disables the display.

Successful provider responses are tracked locally whether or not cost display
is enabled. `ttsi usage` reports a combined total and a breakdown by provider;
JSON preserves the combined fields and adds a `providers` object. It reports
cumulative usage recorded by this installation,
not Google account spending or historical usage. Records live in
`usage/v1/` beside the selected configuration file (`--config` / `TTS_CONFIG`),
separate from the audio cache. Each immutable JSON record contains a timestamp,
provider/model, character/token counts, pricing source/date and the original
estimate; no input text, audio, API keys, or credentials. Concurrent processes
publish separate atomic records, so they do not overwrite each other's totals.
Changing configuration directories selects a separate history. Deleting the
usage directory resets that history; deleting the audio cache does not.

Estimates use **gross standard paid-list USD pricing**, even for a Gemini free
key. Free tiers, credits, negotiated discounts, taxes, and other cloud services
are excluded. A local batch uses ordinary synchronous API pricing, not Google's
Batch API discount. Cache hits, coalesced duplicate requests, and dry runs do not
add provider usage; a displayed cache-hit cost is zero incremental cost, not the
original generation's cost. Dry runs do not emit a cost object.

Pricing snapshot verified November 26, 2026:

- [Chirp 3 HD](https://cloud.google.com/text-to-speech/pricing): $30 per million
  submitted Unicode characters, including whitespace (not UTF-8 bytes).
- [Gemini 3.8 TTS](https://ai.google.dev/gemini-api/docs/pricing): per million
  input/output tokens, Flash $0.50/$9 and Flash-Lite $0.50/$6 through December
  31, 2026; $1/$18 and $1/$12 respectively starting January 1, 2027 (UTC).

Gemini uses returned `usageMetadata` prompt and candidate token counts, including
style/script overhead metered by Google. Missing metadata or unsupported pricing
is **unknown**, never assumed free. JSON `unknown_requests` counts those requests;
`estimated_usd` is only the known subtotal when that count is nonzero. Historical
records retain their original estimate when rates change. Prices are bundled,
not fetched live; update the CLI when Google changes pricing.

Tracking occurs before local encoding/output writes, so a successful provider
response can count even if saving audio later fails. Failed/rejected requests,
network timeouts with uncertain server-side completion, responses that cannot be
parsed, crashes before recording, and voice-design operations are not included.
Retries/failover count each observed successful response, not rejected attempts.
Persistence failures warn without failing otherwise successful synthesis; usage
may then be incomplete. Corrupt records cause `ttsi usage` to report an error
rather than silently returning misleading totals. Treat this as a local estimate,
not a billing ledger; use Google Billing for billed charges.
