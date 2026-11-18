# tts-cli

A lightweight text-to-speech CLI for Windows, macOS, and Linux. It generates MP3
audio using Google Chirp 3 HD, with local caching, batch manifests, and stable JSON
output. It does not depend on Anki.

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

Make sure your Google Cloud project has billing and the Text-to-Speech API
enabled. With Google Cloud CLI on your PATH, run:

```sh
ttsi login google --project YOUR_PROJECT_ID
ttsi doctor --online --json
```

The login command opens Google's sign-in flow through
`gcloud auth application-default login`. With `--project`, it also sets the
ADC quota project after sign-in. This updates local ADC, which can be shared
with other tools. If `GOOGLE_APPLICATION_CREDENTIALS` points to a credentials
file, it takes precedence over those local credentials.

See [Cloud TTS authentication](https://docs.cloud.google.com/text-to-speech/docs/authentication).
On Google Cloud, use Application Default Credentials (ADC) with an attached
identity instead of storing keys in the configuration file. The online doctor
checks API access without synthesizing audio; it does not prove synthesis
permission or available quota.

## Commands

Start with one MP3. Once you've configured Google credentials, run:

```sh
ttsi "Hello, how are you?" -o hello.mp3
```

| Command | Shortcut | What it does |
| --- | --- | --- |
| `ttsi generate <text>` | `ttsi g <text>` | Turns one text argument into one MP3. Use `--output` (`-o`) to name the file. |
| `ttsi batch <file>` | `ttsi b <file>` | Reads a [JSON batch file](#batch-and-json-contract-v1), generates MP3s, and prints a JSON report to stdout even without `--json`. Use `--output-dir` for the destination and `--jobs` for parallelism. |
| `ttsi voices list` | `ttsi v ls` | Lists Chirp 3 HD voice IDs and aliases from the bundled catalog. Use `--lang` to filter by exact locale or `--online` to query Google. |
| `ttsi providers list` | `ttsi p ls` | Lists implemented providers. Currently only `google` is available. |
| `ttsi login google [--project ID]` | — | Signs in using Google Cloud CLI and optionally sets the ADC quota project. |
| `ttsi capabilities` | `ttsi cap` | Summarizes supported formats and limits. Use `--json` for details such as locales and supported parameters. |
| `ttsi config show` | `ttsi cfg sh` | Shows the config path and effective settings. With `--json`, it also shows saved YAML values separately. |
| `ttsi config set <key=value>...` | `ttsi cfg s <key=value>...` | Validates and saves one or more settings together, for example `ttsi config set language=it-IT voice=kore`. |
| `ttsi config unset <key>...` | `ttsi cfg u <key>...` | Removes saved settings, for example `ttsi config unset language voice`. Environment overrides and defaults still apply. |
| `ttsi doctor` | `ttsi d` | Checks config, directory writability, and local credential hints. Add `--online` to probe Google without generating audio. |
| `ttsi help [command]` | — | Shows help. Use `ttsi --help` for the top-level list or, for example, `ttsi batch --help` for flags. |
| `ttsi completion <shell>` | — | Generates a completion script for `bash`, `zsh`, `fish`, or `powershell`. See `ttsi completion --help`. |

Both `generate` and `batch` accept `--lang`, `--voice`, `--provider`,
`--cache-dir`, `--no-cache`, and `--dry-run`. The global flags are
`--config PATH` to select a YAML file and `--json` for machine-readable results.

```sh
ttsi generate "Come stai?" --lang it-IT --voice kore --output italian.mp3
ttsi generate --dry-run --json -- "-text beginning with a dash"
ttsi batch sentences.json --jobs 4 --output-dir audio
ttsi voices list --lang it-IT
```

`ttsi "<text>"` is shorthand for `ttsi generate "<text>"`. Put the text before
generation-specific flags when using the shorthand. To speak a command name or
alias literally, use the explicit form: `ttsi generate -- "g"`.

The default voice is `en-US-Chirp3-HD-Kore`. Aliases resolve against the bundled
catalog and are case-insensitive. A full Chirp 3 HD voice name can supply the locale
when no language was set. Use `voices list --online` to discover full IDs before a
local catalog update. The local catalog is a dated snapshot from
[Google's published voice list](https://docs.cloud.google.com/text-to-speech/docs/list-voices-and-types).
Offline listing is not a live availability guarantee.

The CLI accepts nonblank plain UTF-8 text, up to
[5,000 bytes](https://docs.cloud.google.com/text-to-speech/quotas) per item.
It preserves the original text, including surrounding whitespace.
Synthesis uses synchronous MP3 on the global endpoint. SSML, pitch, rate,
and other encodings are not exposed, including
[Google's preview controls](https://docs.cloud.google.com/text-to-speech/docs/chirp3-hd).

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
| `language` | `TTS_LANGUAGE` | `en-US` (or the locale of a full voice ID) |
| `voice` | `TTS_VOICE` | `kore` |
| `output_dir` | `TTS_OUTPUT_DIR` | Current directory |
| `cache_dir` | `TTS_CACHE_DIR` | `tts` under the OS user cache directory |
| `jobs` | `TTS_JOBS` | `4` (range: 1–16) |

Relative paths resolve against the working directory. The CLI does not load
`.env` files automatically. Never store credentials in YAML.

Generated files default to `<request-hash>.mp3` in the output directory.
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
{"items":[{"id":"note-101","text":"How are you?"},{"id":"note-102","text":"Come stai?","language":"it-IT","voice":"kore"}]}
```

IDs must be nonempty and unique. Language and voice are optional. Bad items do
not prevent valid items from running. Transient synthesis failures are retried
at most twice; permanent errors and hard quota failures are not retried.
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
| `generate` | `file` (absolute), `provider`, `voice`, `language`, `cached`, `characters` (Unicode code points), `cache_status` (`hit`, `miss`, `corrupt` for preview, or `bypassed`), and optional `dry_run` |
| `batch` | `items` (input order), `total`, `succeeded`, `failed`, optional `dry_run`; each item has `id`, `success` and either generation `data` or `error` |
| `voices list` | `source` (`local` or `google`), `voices` (`name`, `language`, `alias`), and `catalog_date` for the snapshot |
| `providers list` | `providers` (provider IDs) |
| `login google` | `provider` (Google provider ID), `quota_project` (explicit project ID, or empty if omitted) |
| `capabilities` | `providers`, `default_provider`, `voice_family`, `languages`, `voices_command`, `catalog_source`, `catalog_date`, `input_formats`, `output_formats`, `max_text_bytes`, `synthesis_parameters`, `unsupported_parameters`, `batch`, `cache`, `dry_run` |
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
