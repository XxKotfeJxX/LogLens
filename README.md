# LogLens

**Stream large logs, extract structured events, and investigate them without
loading the whole file into memory.**

LogLens is a local or self-hosted log explorer built with Go, React,
TypeScript, and SQLite. Raw files stay immutable. The index stores byte
offsets, envelope metadata, semantic event types, and selected typed dynamic
fields.

Current product history is preserved:

- v0.1 Core Explorer
- v0.2 Search and Investigation
- v0.3 Live Tail
- v0.4 Extraction Studio
- v0.5 Structured Analysis

v0.6 Multi-Source Correlation extends this foundation.

## Processing pipeline

```text
raw log file
    → streaming reader
    → record grouping
    → envelope parser
    → event recognition
    → field extraction
    → type conversion
    → indexing
    → search
    → aggregation
    → visualization
```

Envelope parsing finds common timestamp, level, service, request-ID, and
message fields. Semantic extraction is separate and uses an immutable parser
profile version. Changing a rule creates a new version; it never silently
changes the meaning of previously indexed entries.

## v0.4 Extraction Studio

- Reusable parser profiles with immutable versions.
- Priority-ordered regex, guided literal-pattern, key-value, and JSON rules.
- Typed string, integer, float, boolean, datetime, and duration fields.
- Preview of up to 500 entries with coverage, unmatched records, type errors,
  and ambiguity warnings.
- All extracted fields in JSON; only indexed or aggregatable fields receive
  dedicated typed index rows.
- Explicit source-to-parser assignment and cancellable batch reindex.
- Raw offsets remain unchanged during reindex.
- Existing sources migrate to raw mode without a forced reindex.
- Live-tail entries keep the parser version assigned when the watcher starts.
- Browser uploads and root-bound server-path imports.

The first supported semantic workflow is:

```text
open existing source
    → create an attack rule
    → preview extraction
    → assign parser version
    → reindex
    → filter event_type=attack
    → inspect typed damage
```

## v0.5 Structured Analysis

- POST-based typed queries with equality, range, set, and substring operators.
- `COUNT`, `SUM`, `AVG`, `MIN`, and `MAX` over one optional group-by field.
- Safe derived expressions with arithmetic, case conversion, numeric
  conversion, and timestamp-duration functions.
- Saved analysis configurations with table, bar, and line visualizations.
- Non-indexed fields remain visible in JSON but are rejected as filter fields.

## Quick start

```bash
docker compose up --build
```

Open [http://localhost:8088](http://localhost:8088). The `loglens-data` volume
retains SQLite metadata and managed raw files.

For local development:

```bash
# terminal 1 — Go 1.26.5
go run ./cmd/loglens

# terminal 2 — Node 24 and pnpm 10
pnpm --dir web install
pnpm --dir web dev
```

## Bounded large-file design

The streaming reader retains at most `MAX_ENTRY_SIZE` bytes for one entry.
Every entry receives a byte offset and length before a bounded batch is written
to SQLite. Oversized entries remain addressable and are marked `oversized`.

Entry retrieval:

1. selects compact metadata and typed indexes from SQLite;
2. opens the raw source once per response;
3. uses `ReadAt` only for the requested byte ranges;
4. returns a bounded hydrated window;
5. renders only visible rows with TanStack Virtual.

Reindex repeats semantic extraction from existing offsets. It does not copy or
modify the raw file.

## Supported input

- `.log`, `.txt`, `.jsonl`, and `.ndjson`
- LF, CRLF, and a final non-live record without a newline
- managed browser uploads
- root-bound server files
- watched server files
- generic timestamp/level/service/message text
- common JSON timestamp, level, message, service, and correlation aliases

Server imports accept only relative paths below `IMPORT_ROOT`. Absolute paths,
traversal, symlinks, non-regular files, empty files, unsupported extensions,
and over-limit files are rejected. Deleting a server source removes metadata
and index rows, never the original file.

## API

The OpenAPI 3.1 contract is in
[`api/openapi.yaml`](api/openapi.yaml).

Core explorer:

```text
POST   /api/v1/sources/upload
POST   /api/v1/sources/import
GET    /api/v1/sources/
GET    /api/v1/sources/{id}
POST   /api/v1/sources/{id}/imports
GET    /api/v1/sources/{id}/entries
GET    /api/v1/sources/{id}/entries/{number}/context
GET    /api/v1/sources/{id}/statistics
GET    /api/v1/sources/{id}/timeline
```

Extraction Studio:

```text
POST   /api/v1/parser-profiles/
GET    /api/v1/parser-profiles/
GET    /api/v1/parser-profiles/{id}
POST   /api/v1/parser-profiles/{id}/versions
GET    /api/v1/parser-profiles/{id}/versions
POST   /api/v1/parser-profiles/{id}/preview

PUT    /api/v1/sources/{id}/parser
POST   /api/v1/sources/{id}/reindex
GET    /api/v1/sources/{id}/reindex
POST   /api/v1/sources/{id}/reindex/cancel
GET    /api/v1/sources/{id}/fields
GET    /api/v1/sources/{id}/event-types
GET    /api/v1/sources/{id}/extraction-statistics

POST   /api/v1/sources/{id}/watch
POST   /api/v1/sources/{id}/watch/stop
GET    /api/v1/sources/{id}/live/events
```

Structured Analysis:

```text
POST   /api/v1/sources/{id}/query
POST   /api/v1/sources/{id}/aggregations
POST   /api/v1/sources/{id}/analyses
GET    /api/v1/sources/{id}/analyses
DELETE /api/v1/sources/{id}/analyses/{analysisId}

POST   /api/v1/parser-profiles/{id}/derived-fields
GET    /api/v1/parser-profiles/{id}/derived-fields
DELETE /api/v1/parser-profiles/{id}/derived-fields/{fieldId}
```

Existing endpoints remain backward compatible. `GET /entries` supports the
original filters plus `eventType`; v0.5 adds a separate typed POST query.

## Configuration

| Setting               |                 Default | Purpose                          |
| --------------------- | ----------------------: | -------------------------------- |
| `DATABASE_PATH`       |  `./storage/loglens.db` | SQLite metadata                  |
| `SOURCE_STORAGE_PATH` |     `./storage/sources` | Managed raw files                |
| `TEMP_STORAGE_PATH`   |        `./storage/temp` | In-progress uploads              |
| `MAX_UPLOAD_SIZE`     |                  `20GB` | Request and source limit         |
| `MAX_ENTRY_SIZE`      |                   `4MB` | Per-entry memory bound           |
| `IMPORT_BATCH_SIZE`   |                  `2000` | Import transaction size          |
| `IMPORT_ROOT`         |                     `.` | Allowed server-import root       |
| `ALLOW_SERVER_IMPORT` |                 `false` | Enables server-path registration |
| `WEB_ORIGIN`          | `http://localhost:5173` | Development CORS origin          |

## Verification

```bash
go vet ./...
go test ./...
go test -race ./...

pnpm --dir web lint
pnpm --dir web format:check
pnpm --dir web typecheck
pnpm --dir web test
pnpm --dir web build

docker compose config --quiet
docker compose build
```

Integration tests use real SQLite and cover legacy migration, immutable parser
versions, preview isolation, typed extraction, offset-preserving reindex,
semantic live entries, typed queries, aggregations, derived fields, and saved
analyses.

## Roadmap

- v0.6 Multi-Source Correlation: source groups, canonical field mappings,
  corrected merged timelines, cross-source search, and arbitrary correlation
  keys.
- v0.7 Patterns and Comparison.
- v0.8 Product Hardening.
- v1.0 Stable Self-Hosted Release.

Performance values remain targets until a reproducible report records the
dataset, machine, throughput, allocations, peak RSS, database size, and query
latency.

## Git workflow

`dev` is the default integration branch and `main` contains deployable
releases. Feature branches use Conventional Commits, GitHub issues and
milestones, pull requests into `dev`, protected branches, and backend,
frontend, integration, and release-readiness Actions.

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
