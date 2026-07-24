package store

const schema = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    original_name TEXT NOT NULL,
    stored_path TEXT NOT NULL UNIQUE,
    format TEXT NOT NULL,
    status TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    entry_count INTEGER NOT NULL DEFAULT 0,
    parsed_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    imported_at TEXT
);

CREATE TABLE IF NOT EXISTS import_jobs (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    bytes_processed INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL,
    entries_found INTEGER NOT NULL DEFAULT 0,
    parse_failures INTEGER NOT NULL DEFAULT 0,
    started_at TEXT,
    completed_at TEXT,
    error_code TEXT,
    error_message TEXT
);

CREATE TABLE IF NOT EXISTS entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    entry_number INTEGER NOT NULL,
    byte_offset INTEGER NOT NULL,
    byte_length INTEGER NOT NULL,
    timestamp_ns INTEGER,
    level TEXT,
    service TEXT,
    request_id TEXT,
    parse_status TEXT NOT NULL,
    UNIQUE(source_id, entry_number)
);

CREATE INDEX IF NOT EXISTS idx_entries_source_number ON entries(source_id, entry_number);
CREATE INDEX IF NOT EXISTS idx_entries_source_timestamp ON entries(source_id, timestamp_ns);
CREATE INDEX IF NOT EXISTS idx_entries_source_level ON entries(source_id, level);
CREATE INDEX IF NOT EXISTS idx_entries_source_service ON entries(source_id, service);
CREATE INDEX IF NOT EXISTS idx_entries_source_request_id ON entries(source_id, request_id);
`
