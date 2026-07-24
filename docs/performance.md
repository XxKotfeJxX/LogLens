# Performance evidence

This file records reproducible measurements, not product guarantees.

## v0.4 semantic extraction microbenchmark

Measured on 2026-07-24:

- OS/architecture: Windows amd64
- CPU: 13th Gen Intel Core i5-13420H
- Go: 1.26.5
- input: one 36-byte combat record
- engine: one precompiled named-capture regex with three typed fields

Command:

```bash
go test -run '^$' -bench BenchmarkCompiledRegexExtraction -benchmem ./internal/extraction
```

Result:

```text
BenchmarkCompiledRegexExtraction-12  454356  2753 ns/op  13.08 MB/s  1583 B/op  18 allocs/op
```

This isolates the compiled semantic engine. It does not include raw-file I/O,
SQLite writes, reindex batches, frontend rendering, or operating-system cache
effects. End-to-end import, reindex, query, peak-RSS, and database-size
measurements remain required before publishing capacity or latency claims.

## v0.6 canonical correlation transform microbenchmark

Measured on 2026-07-24:

- OS/architecture: Windows amd64
- CPU: 13th Gen Intel Core i5-13420H
- Go: 1.26.5
- input: one structured event, three canonical mappings, Europe/Kyiv timezone,
  and one manual clock offset

Command:

```bash
go test -run '^$' -bench BenchmarkCanonicalMappingAndClockCorrection -benchmem ./internal/api
```

Result:

```text
BenchmarkCanonicalMappingAndClockCorrection-12  106065  16738 ns/op  3016 B/op  25 allocs/op
```

This isolates canonical projection plus timezone/clock correction. Group query
latency also depends on per-source SQLite indexes, raw-file hydration, source
count, cache state, and result limits; this value is not an end-to-end
capacity claim.
