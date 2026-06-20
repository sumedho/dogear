# DogEar

DogEar is a local CLI and web application for searching and asking questions
about Markdown manuals. It combines SQLite FTS5 search, optional sqlite-vec
embeddings, local reranking, and citation-grounded answers from
OpenAI-compatible providers. It is aimed at synthesizer manuals and other
reference documents that have already been converted to Markdown.

![UI](images/)

## Requirements

- Go 1.26.3 or newer.
- Node.js and npm only when rebuilding or developing the React frontend.
- An OpenAI-compatible chat endpoint for generated answers. Ollama-style local
  endpoints are supported.
- An optional OpenAI-compatible embedding endpoint for hybrid search.

SQLite is provided through the pure-Go `modernc.org/sqlite` driver; a separate
SQLite installation is not required.

## Build

The repository includes a prebuilt frontend under `internal/server/static`, so
a normal Go build produces a self-contained executable:

```sh
go build -o dogear ./cmd/dogear
```

The server uses `go:embed`, so the resulting `./dogear` binary contains the web
UI and does not need frontend files beside it at runtime.

When frontend source under `web/` has changed, rebuild the assets before the Go
binary:

```sh
cd web
npm ci
npm test
npm run build
cd ..
go build -o dogear ./cmd/dogear
```

`npm run build` writes hashed assets to `internal/server/static/`. Rebuilding an
already-running binary does not update that process; restart `dogear serve` to
load the new embedded assets.

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

By default DogEar stores its SQLite database at `.dogear/dogear.db`. Use `--db PATH` with any command to target a different database.

`dogear init` also creates `.dogear/config.toml` if it does not already exist. Use `--config PATH` to target a different config file.

Available commands are `init`, `import`, `index`, `list`, `info`, `remove`,
`search`, `show`, `context`, `doctor`, `eval`, `ask`, and `serve`. Run
`./dogear COMMAND --help` for command-specific flags. `convert` is currently a
placeholder and returns a not-implemented error.

## Importing Manuals

Import one file:

```sh
./dogear import ./manuals/yamaha-dx7.md --id yamaha-dx7 --brand Yamaha --model DX7 --tags synth,fm
```

Import a directory recursively:

```sh
./dogear import ./manuals
```

If a document id already exists, DogEar refuses to overwrite it. Use `--replace` to remove the old document, chunks, and FTS rows before importing the new version:

```sh
./dogear import ./manuals/yamaha-dx7.md --id yamaha-dx7 --replace
```

## Managing Documents

```sh
./dogear list
./dogear info yamaha-dx7
./dogear remove yamaha-dx7
```

Use `--json` on `list`, `info`, `search`, `show`, `context`, `doctor`, `eval`,
and `ask` for machine-readable output.

## Page Markers

Markdown converted from PDFs often loses exact page breaks. DogEar can infer some pages from a table of contents, but explicit page markers are more accurate.

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

This uses the configured retrieval path—hybrid when the vector index is current,
otherwise FTS5—and prints ranked source chunks with stable source labels, page
when known, heading, and line range.

Available context formats:

```sh
./dogear context "How do I turn off local control?" --format text
./dogear context "How do I turn off local control?" --format json
./dogear context "How do I turn off local control?" --format prompt
```

The `prompt` format emits the bounded context block used by `ask`. Sources are labeled per response as `[1]`, `[2]`, and so on, and answers should cite those labels.

DogEar retrieves with SQLite FTS5, then locally reranks candidates to prefer real prose sections over table-of-contents, index, and short page-reference chunks. Use `--debug` to inspect raw BM25 score, rerank score, quality class, and reason flags:

```sh
./dogear context "How do I configure MIDI sync?" --debug
./dogear context "How do I configure MIDI sync?" --format json --debug
./dogear search "MIDI sync" --debug
```

## Asking Questions

DogEar supports local and online OpenAI-compatible chat completion endpoints.

Provider settings can live in `.dogear/config.toml` or be edited from the web
UI. API keys are masked and are never returned by the settings API.

```toml
[provider]
base_url = "http://localhost:11434/v1"
model = "llama3.1"
api_key = ""
timeout = "60s"

[embedding]
base_url = "http://localhost:8000/v1"
model = "Qwen3-Embedding-8B-4bit-DWQ"
api_key = ""
dimensions = 1024
batch_size = 16
query_instruction = "Retrieve relevant passages from product manuals that answer the user's question."
timeout = "120s"
```

Build the optional sqlite-vec index explicitly:

```sh
./dogear index --embeddings
```

Use `--force` to rebuild embeddings even when the current index matches the
configured model, dimensions, and document set:

```sh
./dogear index --embeddings --force
```

Search, context, ask, and the web API use hybrid FTS/vector retrieval when the
embedding index is current. They fall back to FTS when it is missing, stale, or
the embedding endpoint is unavailable. Imports mark the vector index stale.

Retrieval evaluation fixtures are JSON files containing queries and expected
heading/page/text selectors. Compare modes with:

```sh
./dogear eval evaluation.json --mode both
./dogear eval evaluation.json --mode hybrid --answers --json
```

Evaluation runs can enforce minimum quality gates for CI or model comparisons:

```sh
./dogear eval evaluation.json --mode both \
  --min-mrr 0.75 --min-recall-at-5 0.90
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

Chat-provider configuration precedence is: CLI flags, environment variables,
config file, defaults. Embedding configuration uses environment variables,
then the config file, then defaults.

Embedding settings use the corresponding
`DOGEAR_EMBEDDING_BASE_URL`, `DOGEAR_EMBEDDING_API_KEY`,
`DOGEAR_EMBEDDING_MODEL`, `DOGEAR_EMBEDDING_DIMENSIONS`,
`DOGEAR_EMBEDDING_BATCH_SIZE`, `DOGEAR_EMBEDDING_QUERY_INSTRUCTION`, and
`DOGEAR_EMBEDDING_TIMEOUT` environment variables.

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
manuals), streams answers, and can import one or more `.md` or `.markdown`
files. It includes suggested prompts, answer retry/regeneration, question
editing, copy actions, chat-deletion undo, chat backup import/export, chat and
manual-library filtering, readiness status, and full-text search inside the
manual viewer. Keyboard shortcuts include `Cmd/Ctrl+N` for a new chat,
`Cmd/Ctrl+K` for search, and `/` to focus the composer.

Embedded base64 PNG, JPEG, GIF, and WebP image data is stored separately from
searchable chunk text, while image alt text participates in FTS and embedding
retrieval. Matching images are returned with search and context results and
shown with relevant sources. When a question explicitly requests an image,
matching retrieved images are also displayed inline beneath the answer.

Citation cards open a deep-linked manual viewer at the retrieved chunk. The
Settings panel edits masked chat/embedding configuration, tests both endpoints,
shows vector-index coverage, and streams explicit embedding rebuild progress.
When hybrid retrieval is unavailable, answers remain usable through automatic
FTS fallback and the UI reports that fallback.

The streaming endpoint uses server-sent events:

```sh
curl -N -X POST http://127.0.0.1:8765/api/ask/stream \
  -H 'Content-Type: application/json' \
  -d '{"question":"How do I turn off local control?","history":[]}'
```

The production UI is always served from assets embedded in the Go binary.
Generated files under `internal/server/static/` should be committed with
frontend source changes.

## Development and Verification

Run the complete Go checks from the repository root:

```sh
go test ./...
go vet ./...
```

Run frontend tests and produce production assets from `web/`:

```sh
npm test
npm run test:e2e
npm run build
```

The end-to-end suite runs the primary viewport checks in Chromium and WebKit.
Install its browser runtimes once with `npx playwright install chromium webkit`.

After `npm run build`, compile the Go binary again to embed the new assets.

## Architecture

DogEar keeps application behavior separate from delivery and persistence:

- `internal/app` contains provider-independent question-answering workflows.
- `internal/adapters/dogear` adapts the persistence retrieval interface for the
  application layer.
- `internal/dogear` contains focused SQLite components for schema management,
  document CRUD, FTS indexing, search, retrieval, images, embeddings, and
  diagnostics. Narrow interfaces in `interfaces.go` define these capabilities.
- `internal/cli` defines each Cobra command in a dedicated command file and
  keeps shared JSON and text formatting separate.
- `internal/server` exposes the JSON/SSE API and embeds the React frontend from
  `internal/server/static`; JSON request decoding and long-running jobs have
  dedicated helpers.
- `internal/retrievalpolicy` centralizes retrieval limits and ranking breadth so
  CLI, application, persistence, and HTTP defaults do not drift.

The CLI uses a single SQLite connection. The server uses a small WAL-mode pool
with per-connection foreign-key enforcement and a busy timeout. Schema upgrades
are transactional, and embedding rebuilds run as one process-owned job that
clients can reattach to after an SSE disconnect.

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

## Roadmap

Document conversion is the remaining placeholder command:

```sh
./dogear convert
```

SQLite vector search, hybrid retrieval, retrieval evaluation, streaming
answers, and embedding-index management are implemented. Native conversion
and OCR pipelines for PDF and other source formats are planned next.
