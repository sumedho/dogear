# Dogear

Dogear is a local CLI for searching Markdown manuals with SQLite FTS5. It is aimed at synthesizer manuals and other reference documents that have already been converted to Markdown.

## Build

```sh
go build ./cmd/dogear
```

This creates `./dogear`.

## Basic Workflow

```sh
./dogear init
./dogear import ./data
./dogear search "MIDI config"
./dogear ask "How do I turn off local control?"
./dogear show analog-four-mkii-analog-four-mkii-user-manual --page 58
./dogear serve
./dogear doctor
```

By default Dogear stores its SQLite database at `.dogear/dogear.db`. Use `--db PATH` with any command to target a different database.

`dogear init` also creates `.dogear/config.toml` if it does not already exist. Use `--config PATH` to target a different config file.

## Importing Manuals

Import one file:

```sh
./dogear import ./manuals/yamaha-dx7.md --id yamaha-dx7 --brand Yamaha --model DX7 --tags synth,fm
```

Import a directory recursively:

```sh
./dogear import ./manuals
```

If a document id already exists, Dogear refuses to overwrite it. Use `--replace` to remove the old document, chunks, and FTS rows before importing the new version:

```sh
./dogear import ./manuals/yamaha-dx7.md --id yamaha-dx7 --replace
```

## Managing Documents

```sh
./dogear list
./dogear info yamaha-dx7
./dogear remove yamaha-dx7
```

Use `--json` on `list`, `info`, `search`, `show`, `context`, and `doctor` for machine-readable output.

## Page Markers

Markdown converted from PDFs often loses exact page breaks. Dogear can infer some pages from a table of contents, but explicit page markers are more accurate.

Supported marker forms:

```md
<!-- page: 84 -->
<!-- dogear:page=84 -->
```

Page markers apply to following sections and override table-of-contents page inference.

## Retrieval Context Preview

To inspect the chunks that `ask` uses:

```sh
./dogear context "How do I turn off local control?"
```

This uses the current FTS5 retrieval path and prints ranked source chunks with stable source labels, page when known, heading, and line range.

Available context formats:

```sh
./dogear context "How do I turn off local control?" --format text
./dogear context "How do I turn off local control?" --format json
./dogear context "How do I turn off local control?" --format prompt
```

The `prompt` format emits the bounded context block used by `ask`. Sources are labeled per response as `[1]`, `[2]`, and so on, and answers should cite those labels.

Dogear retrieves with SQLite FTS5, then locally reranks candidates to prefer real prose sections over table-of-contents, index, and short page-reference chunks. Use `--debug` to inspect raw BM25 score, rerank score, quality class, and reason flags:

```sh
./dogear context "How do I configure MIDI sync?" --debug
./dogear context "How do I configure MIDI sync?" --format json --debug
./dogear search "MIDI sync" --debug
```

## Asking Questions

Dogear supports local and online OpenAI-compatible chat completion endpoints.

Provider settings can live in `.dogear/config.toml`:

```toml
[provider]
base_url = "http://localhost:11434/v1"
model = "llama3.1"
api_key = ""
timeout = "60s"
```

Local-first defaults target Ollama-style endpoints, so this is enough for many local setups:

```sh
./dogear ask "How do I turn off local control?"
```

For online providers, set an API key:

```sh
export DOGEAR_BASE_URL=https://api.openai.com/v1
export DOGEAR_API_KEY=...
export DOGEAR_MODEL=gpt-4.1-mini
./dogear ask "How do I turn off local control?"
```

Equivalent environment variables and flags are available:

```sh
export DOGEAR_BASE_URL=http://localhost:11434/v1
export DOGEAR_MODEL=llama3.1
./dogear ask "How do I turn off local control?" --base-url http://localhost:11434/v1 --model llama3.1
```

Configuration precedence is: CLI flags, environment variables, config file, defaults.

Use `--dry-run` to print the provider URL, redacted headers, and JSON body without making a network call:

```sh
./dogear ask "How do I turn off local control?" --dry-run
```

`ask` prints the answer followed by all retrieved sources. Use `--json` for structured output.

## Local Web UI and JSON API

Serve the embedded React chat UI from the same database and config used by the CLI:

```sh
./dogear serve
```

The default address is `http://127.0.0.1:8765`. Use `--addr` to choose another listen address:

```sh
./dogear serve --addr 127.0.0.1:9000
./dogear --serve
```

The UI calls the local JSON API:

```sh
curl http://127.0.0.1:8765/api/health
curl http://127.0.0.1:8765/api/documents
curl "http://127.0.0.1:8765/api/search?q=local+control&limit=5"
curl "http://127.0.0.1:8765/api/context?q=local+control&limit=5"
curl -X POST http://127.0.0.1:8765/api/ask \
  -H 'Content-Type: application/json' \
  -d '{"question":"How do I turn off local control?","dry_run":true}'
```

The web UI keeps chats in browser storage, supports a manual per chat (or all
manuals), streams answers, and can import `.md` or `.markdown` files. Embedded
base64 PNG, JPEG, GIF, and WebP images are stored outside the search index and
shown with relevant retrieved sources.

The streaming endpoint uses server-sent events:

```sh
curl -N -X POST http://127.0.0.1:8765/api/ask/stream \
  -H 'Content-Type: application/json' \
  -d '{"question":"How do I turn off local control?","history":[]}'
```

To rebuild the embedded frontend after editing `web/`:

```sh
cd web
npm ci
npm test
npm run build
```

The generated files under `internal/server/static/` are embedded in the Go
binary and should be committed with frontend source changes.

## Diagnostic Logging

Command results, JSON, and streamed answers are written to stdout. Operational
diagnostics are structured separately with Go's `slog` package and default to
human-readable records on stderr.

```sh
./dogear serve --log-level debug
./dogear serve --log-format json
./dogear serve --log-format json --log-file .dogear/dogear.log
```

Supported levels are `debug`, `info`, `warn`, and `error`. Setting `--log-file`
redirects diagnostics to that append-only file instead of stderr. Log rotation
and retention are the responsibility of the process supervisor or deployment.

## Not Implemented Yet

These commands are placeholders:

```sh
./dogear convert
```

Future milestones include sqlite-vec vector search, hybrid retrieval, and conversion pipelines.
