# Changelog

All notable changes are documented here. The project follows Semantic
Versioning and uses the Keep a Changelog format.

## [Unreleased]

### Added

- v0.5 Structured Analysis with typed POST filters, indexed aggregations, safe
  derived fields, saved analyses, and table/bar/line result views.
- v0.4 Extraction Studio with reusable parser profiles and immutable versions.
- Priority-ordered regex, literal-pattern, key-value, and JSON semantic rules.
- Typed dynamic fields, selective typed indexes, preview coverage, warnings,
  source assignment, and cancellable offset-preserving reindex.
- Parser-aware watched-file live tail with SSE entry, rotation, and
  backpressure events.
- Extraction Studio, source extraction settings, server-path import, coverage,
  and extracted-field explorer UI.
- Backward-compatible migrations for existing raw-mode sources and entries.

- Initial streaming JSONL/plain-text import pipeline with bounded entry reads,
  byte-offset indexing, SQLite batch writes, cancellation, and SSE progress.
- Seek-based entry windows, indexed filters, statistics, timeline, context API,
  and raw/parsed hydration.
- React source library, import workflow, virtual explorer, timeline, and entry
  inspector.
- OpenAPI 3.1 contract, deterministic sample generator, Docker Compose, Caddy,
  tests, and CI.
