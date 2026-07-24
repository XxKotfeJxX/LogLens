package store

import (
	"context"
	"database/sql"
	"fmt"
)

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
    source_type TEXT NOT NULL DEFAULT 'uploaded_file',
    format TEXT NOT NULL,
    status TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    entry_count INTEGER NOT NULL DEFAULT 0,
    parsed_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    parser_profile_id TEXT,
    parser_version INTEGER,
    extraction_status TEXT NOT NULL DEFAULT 'not_configured',
    reindex_required INTEGER NOT NULL DEFAULT 0,
    watch_status TEXT NOT NULL DEFAULT 'stopped',
    watch_offset INTEGER NOT NULL DEFAULT 0,
    file_identity TEXT,
    rotation_generation INTEGER NOT NULL DEFAULT 0,
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
    event_type TEXT,
    matched_rule_id TEXT,
    custom_fields_json TEXT,
    parser_version INTEGER,
    warning_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(source_id, entry_number)
);

CREATE INDEX IF NOT EXISTS idx_entries_source_number ON entries(source_id, entry_number);
CREATE INDEX IF NOT EXISTS idx_entries_source_timestamp ON entries(source_id, timestamp_ns);
CREATE INDEX IF NOT EXISTS idx_entries_source_level ON entries(source_id, level);
CREATE INDEX IF NOT EXISTS idx_entries_source_service ON entries(source_id, service);
CREATE INDEX IF NOT EXISTS idx_entries_source_request_id ON entries(source_id, request_id);
`

const extractionSchema = `
CREATE INDEX IF NOT EXISTS idx_entries_source_event_type ON entries(source_id, event_type);

CREATE TABLE IF NOT EXISTS parser_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS parser_profile_versions (
    id TEXT PRIMARY KEY,
    profile_id TEXT NOT NULL REFERENCES parser_profiles(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    record_format TEXT NOT NULL,
    configuration_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(profile_id, version)
);

CREATE TABLE IF NOT EXISTS extraction_rules (
    id TEXT PRIMARY KEY,
    parser_version_id TEXT NOT NULL REFERENCES parser_profile_versions(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    event_type TEXT NOT NULL,
    priority INTEGER NOT NULL,
    matcher_type TEXT NOT NULL,
    matcher_config_json TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_extraction_rules_version_priority
ON extraction_rules(parser_version_id, priority, id);

CREATE TABLE IF NOT EXISTS field_definitions (
    id TEXT PRIMARY KEY,
    parser_version_id TEXT NOT NULL REFERENCES parser_profile_versions(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    display_name TEXT,
    field_type TEXT NOT NULL,
    indexed INTEGER NOT NULL DEFAULT 0,
    aggregatable INTEGER NOT NULL DEFAULT 0,
    nullable INTEGER NOT NULL DEFAULT 1,
    UNIQUE(parser_version_id, name)
);

CREATE TABLE IF NOT EXISTS entry_field_values (
    entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    field_definition_id TEXT NOT NULL REFERENCES field_definitions(id) ON DELETE CASCADE,
    value_text TEXT,
    value_integer INTEGER,
    value_real REAL,
    value_boolean INTEGER,
    value_timestamp_ns INTEGER,
    PRIMARY KEY(entry_id, field_definition_id)
);

CREATE INDEX IF NOT EXISTS idx_field_text
ON entry_field_values(field_definition_id, value_text);
CREATE INDEX IF NOT EXISTS idx_field_integer
ON entry_field_values(field_definition_id, value_integer);
CREATE INDEX IF NOT EXISTS idx_field_real
ON entry_field_values(field_definition_id, value_real);
CREATE INDEX IF NOT EXISTS idx_field_timestamp
ON entry_field_values(field_definition_id, value_timestamp_ns);

CREATE TABLE IF NOT EXISTS reindex_jobs (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    parser_version_id TEXT NOT NULL REFERENCES parser_profile_versions(id),
    status TEXT NOT NULL,
    entries_processed INTEGER NOT NULL DEFAULT 0,
    total_entries INTEGER NOT NULL,
    warnings INTEGER NOT NULL DEFAULT 0,
    started_at TEXT,
    completed_at TEXT,
    error_message TEXT
);
`

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	columns := []struct {
		table      string
		name       string
		definition string
	}{
		{"sources", "source_type", "TEXT NOT NULL DEFAULT 'uploaded_file'"},
		{"sources", "parser_profile_id", "TEXT"},
		{"sources", "parser_version", "INTEGER"},
		{"sources", "extraction_status", "TEXT NOT NULL DEFAULT 'not_configured'"},
		{"sources", "reindex_required", "INTEGER NOT NULL DEFAULT 0"},
		{"sources", "watch_status", "TEXT NOT NULL DEFAULT 'stopped'"},
		{"sources", "watch_offset", "INTEGER NOT NULL DEFAULT 0"},
		{"sources", "file_identity", "TEXT"},
		{"sources", "rotation_generation", "INTEGER NOT NULL DEFAULT 0"},
		{"entries", "event_type", "TEXT"},
		{"entries", "matched_rule_id", "TEXT"},
		{"entries", "custom_fields_json", "TEXT"},
		{"entries", "parser_version", "INTEGER"},
		{"entries", "warning_count", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range columns {
		if err := ensureColumn(ctx, db, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, extractionSchema); err != nil {
		return err
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, name, definition string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if columnName == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if table != "sources" && table != "entries" {
		return fmt.Errorf("unsupported migration table %q", table)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, definition))
	return err
}
