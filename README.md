# LogLens

**Explore multi-gigabyte log files without loading them into memory.**

LogLens is a local or self-hosted log explorer built with Go, React, TypeScript, and SQLite. It streams uploaded JSONL and plain-text logs, extracts common fields, stores byte offsets, and reads only the raw ranges needed by the current UI window.

> Current status: the repository implements the v0.1 vertical slice—upload, detection, streaming indexing, progress/cancellation, filters, statistics, timeline, raw inspection, virtual scrolling, Docker, and tests. Export jobs, custom plain-text regex mappings, multiline grouping, server-path import, and live tail remain roadmap items.

## Why it is different

A normal browser file viewer eventually turns the entire file into strings and DOM nodes. LogLens keeps the raw source immutable on disk. SQLite contains compact metadata used to find an entry:

```text
source_id · entry_number · byte_offset · byte_length
timestamp · level · service · request_id · parse_status
```

The React application requests windows of 200 indexed records and renders only visible rows with TanStack Virtual.

## Quick start

Docker Compose is the shortest production-like path:

```bash
docker compose up --build
```

Open [http://localhost:8088](http://localhost:8088). The named `loglens-data` volume retains the SQLite database and managed raw files.

For local development:

```bash
# terminal 1 — Go 1.26.5
cp .env.example .env
go run ./cmd/loglens

# terminal 2 — Node 24 and pnpm 10
cd web
pnpm install
pnpm dev
```

Vite proxies `/api`, `/healthz`, and `/readyz` to `localhost:8080`.

## How LogLens reads an 8 GB file with bounded memory

```text
managed raw file
      │
      ▼
bufio.Reader + bounded entry buffer
      │  byte offset and byte length assigned here
      ▼
JSONL / plain-text parser
      │  timestamp, level, service, request ID
      ▼
bounded in-process batch (default 2,000)
      │
      ▼
single SQLite transaction
```

The line reader never retains more than `MAX_ENTRY_SIZE` bytes for one entry. An oversized line is still indexed with its complete byte range and an `oversized` parse status, while only its bounded preview is parsed or returned.

SQLite uses WAL mode, a short busy timeout, indexed filter columns, and batch transactions. Restarted `queued` or `running` jobs become `interrupted`; their sources become `failed` and can be reindexed from zero.

When entries are requested, the API:

1. selects metadata using `(source_id, entry_number)` and field indexes;
2. opens the immutable raw file once for the response;
3. uses `ReadAt` on only the selected byte ranges;
4. parses display fields and returns the requested window.

## Supported input

- Extensions: `.log`, `.txt`, `.jsonl`, `.ndjson`
- Source types: managed browser uploads and root-bound server files
- JSON aliases:
  - timestamp: `timestamp`, `time`, `ts`, `datetime`, `date`, `@timestamp`
  - level: `level`, `severity`, `log_level`, `logLevel`
  - message: `message`, `msg`, `text`, `body`
  - service: `service`, `service_name`, `serviceName`, `component`, `application`, `app`
  - request ID: `request_id`, `requestId`, `trace_id`, `traceId`, `correlation_id`, `correlationId`
- Plain text: generic timestamp–level–service–message records, with unstructured fallback
- Newlines: LF, CRLF, and a final line without a newline
- Normalized levels: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`, `UNKNOWN`

Format detection samples up to 100 non-empty records. JSONL requires at least 80% valid JSON objects and useful timestamp or message fields.

## Explorer

The “Forensic Tape” interface provides:

- source history and status;
- streaming upload progress;
- format confidence and extracted field mapping;
- live import progress over SSE;
- import cancellation;
- virtualized log rows;
- level, service, request ID, time, and substring API filters;
- adaptive timeline buckets;
- level and service statistics;
- a raw/parsed entry inspector;
- source deletion with raw-file cleanup.

Raw content is rendered only as text. The API strips ANSI escape sequences and unsafe control characters, and the frontend does not use `dangerouslySetInnerHTML`.

## API

The OpenAPI 3.1 contract is in [`api/openapi.yaml`](api/openapi.yaml). Core endpoints:

```text
POST   /api/v1/sources/upload
POST   /api/v1/sources/import
GET    /api/v1/sources/
GET    /api/v1/sources/{id}
DELETE /api/v1/sources/{id}
POST   /api/v1/sources/{id}/detect
POST   /api/v1/sources/{id}/imports

GET    /api/v1/imports/{id}
POST   /api/v1/imports/{id}/cancel
GET    /api/v1/imports/{id}/events

GET    /api/v1/sources/{id}/entries
GET    /api/v1/sources/{id}/entries/{number}
GET    /api/v1/sources/{id}/entries/{number}/context
GET    /api/v1/sources/{id}/statistics
GET    /api/v1/sources/{id}/timeline
```

Example entry window:

```http
GET /api/v1/sources/abc/entries?cursor=15000&limit=200&levels=ERROR,WARN&services=payment&query=timeout
```

## Configuration

All settings have safe local defaults. See [`.env.example`](.env.example).

| Setting | Default | Purpose |
|---|---:|---|
| `DATABASE_PATH` | `./storage/loglens.db` | SQLite metadata database |
| `SOURCE_STORAGE_PATH` | `./storage/sources` | Managed immutable raw files |
| `TEMP_STORAGE_PATH` | `./storage/temp` | In-progress uploads |
| `MAX_UPLOAD_SIZE` | `20GB` | Hard request/file limit |
| `MAX_ENTRY_SIZE` | `4MB` | In-memory entry and response preview bound |
| `IMPORT_BATCH_SIZE` | `2000` | SQLite rows per transaction |
| `WEB_ORIGIN` | `http://localhost:5173` | Development CORS origin |
| `IMPORT_ROOT` | empty/disabled | Root directory for server-file imports |
| `ALLOW_SERVER_IMPORT` | `false` | Enables root-bound relative-path imports |

Server imports accept only a relative path below `IMPORT_ROOT`. Absolute paths,
traversal, symlinks, non-regular files, unsupported extensions, empty files,
and files above the configured size limit are rejected. Deleting a
`server_file` removes its metadata and index but never deletes the original
file.

## Generate reproducible samples

```bash
go run ./scripts/generate-sample.go \
  --format jsonl \
  --entries 10000000 \
  --output ./samples/generated-10m.jsonl \
  --error-rate 0.02 \
  --malformed-rate 0.001 \
  --seed 42
```

The generator produces deterministic timestamps, services, request IDs, malformed JSON, bursts of errors, stack-trace-like messages, and occasional large payloads. Use `--format plain` for generic plain text.

## Tests and builds

```bash
go test ./...
go test -race ./...

pnpm --dir web test
pnpm --dir web build

docker compose build
```

The backend integration test exercises a real SQLite database and the entire HTTP path from multipart upload through import and filtered seek-based retrieval. CI runs formatting, the race detector, frontend tests, production builds, and both container builds.

## Performance claims

The design targets files up to 10 GB, up to 25 million entries, bounded import memory, sub-second indexed-source opening, and smooth virtual scrolling. These are **targets, not published results**. This README intentionally contains no invented throughput or latency numbers.

Before publishing benchmark numbers, record:

- machine model, CPU, RAM, and storage;
- generator seed and dataset size;
- import MB/s and entries/s;
- peak resident memory;
- SQLite database size;
- entry-page and filter latency distributions.

## Roadmap

- **v0.1 hardening:** multiline grouping, custom plain-text mappings, filtered export jobs, server-root imports, broader frontend tests, benchmark harness
- **v0.2:** SQLite FTS5, saved queries, nearby-entry workflow, request-ID correlation
- **v0.3:** watched directories and live tail
- **v1.0:** stable self-hosted packaging and migration compatibility

LogLens deliberately is not a replacement for Elasticsearch, Loki, a SIEM, distributed tracing, or a cloud observability platform.

## Git workflow

- `main` contains deployable versions only.
- `dev` is the integration and default development branch.
- Feature branches start from `dev` using Conventional Commit prefixes and
  return through pull requests.
- Feature pull requests are squash-merged after backend, frontend, integration,
  and release-readiness checks pass.
- Releases move from `dev` to `main` through a dedicated release pull request.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the complete policy.

## License

[MIT](LICENSE)
