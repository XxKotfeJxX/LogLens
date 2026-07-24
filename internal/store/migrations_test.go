package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"loglens/internal/domain"
)

func TestOldSourcesAndEntriesSurviveExtractionMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := `
		CREATE TABLE sources (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, original_name TEXT NOT NULL,
			stored_path TEXT NOT NULL UNIQUE, format TEXT NOT NULL, status TEXT NOT NULL,
			size_bytes INTEGER NOT NULL, entry_count INTEGER NOT NULL DEFAULT 0,
			parsed_count INTEGER NOT NULL DEFAULT 0, failed_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL, imported_at TEXT
		);
		CREATE TABLE entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			entry_number INTEGER NOT NULL, byte_offset INTEGER NOT NULL,
			byte_length INTEGER NOT NULL, timestamp_ns INTEGER, level TEXT,
			service TEXT, request_id TEXT, parse_status TEXT NOT NULL,
			UNIQUE(source_id, entry_number)
		);`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO sources
		    (id, name, original_name, stored_path, format, status, size_bytes,
		     entry_count, parsed_count, failed_count, created_at)
		VALUES ('legacy', 'legacy.log', 'legacy.log', 'legacy.log', 'plain_text',
		        'ready', 12, 1, 1, 0, ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO entries
		    (source_id, entry_number, byte_offset, byte_length, level, parse_status)
		VALUES ('legacy', 1, 0, 12, 'INFO', 'parsed')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	storage, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	source, err := storage.GetSource(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if source.ExtractionStatus != "not_configured" || source.ParserProfileID != nil {
		t.Fatalf("legacy source was not migrated to raw mode: %+v", source)
	}
	entries, _, err := storage.QueryEntryMetadata(context.Background(), "legacy", domain.EntryFilter{
		Cursor: 1, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ByteOffset != 0 || entries[0].EventType != "" {
		t.Fatalf("legacy entry was not preserved: %+v", entries)
	}
}

func TestParserProfileVersionsAreImmutableSnapshots(t *testing.T) {
	storage, err := Open(context.Background(), filepath.Join(t.TempDir(), "loglens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	now := time.Now().UTC()
	profile := domain.ParserProfile{ID: "profile", Name: "Game", CreatedAt: now, UpdatedAt: now}
	if err := storage.CreateParserProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	first := domain.ParserProfileVersion{
		ProfileID: "profile", RecordFormat: "plain_text",
		Rules: []domain.ExtractionRule{{
			Name: "attack", EventType: "attack", Priority: 1, MatcherType: "regex",
			MatcherConfig: map[string]any{"pattern": "attack"}, Enabled: true,
		}},
		Fields: []domain.FieldDefinition{{Name: "damage", FieldType: domain.FieldInteger, Nullable: true}},
	}
	if err := storage.CreateParserVersion(context.Background(), &first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = ""
	second.Rules = []domain.ExtractionRule{{
		Name: "loot", EventType: "loot", Priority: 1, MatcherType: "regex",
		MatcherConfig: map[string]any{"pattern": "loot"}, Enabled: true,
	}}
	second.Fields = []domain.FieldDefinition{{Name: "damage", FieldType: domain.FieldInteger, Nullable: true}}
	if err := storage.CreateParserVersion(context.Background(), &second); err != nil {
		t.Fatal(err)
	}
	reloaded, err := storage.GetParserVersion(context.Background(), "profile", 1)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Rules[0].EventType != "attack" || second.Version != 2 {
		t.Fatalf("v1 changed while creating v2: v1=%+v v2=%+v", reloaded, second)
	}
}
