package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"loglens/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ready(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) CreateSource(ctx context.Context, source domain.Source) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sources (
			id, name, original_name, stored_path, format, status, size_bytes, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		source.ID, source.Name, source.OriginalName, source.StoredPath,
		source.Format, source.Status, source.SizeBytes, source.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, original_name, stored_path, format, status, size_bytes,
		       entry_count, parsed_count, failed_count, created_at, imported_at
		FROM sources WHERE status <> 'deleted' ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]domain.Source, 0)
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, rows.Err()
}

func (s *Store) GetSource(ctx context.Context, id string) (domain.Source, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, original_name, stored_path, format, status, size_bytes,
		       entry_count, parsed_count, failed_count, created_at, imported_at
		FROM sources WHERE id = ? AND status <> 'deleted'`, id)
	source, err := scanSource(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Source{}, ErrNotFound
	}
	return source, err
}

func (s *Store) UpdateSourceFormat(ctx context.Context, id, format string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sources SET format = ? WHERE id = ?`, format, id)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) PrepareSourceImport(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE source_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sources
		SET status = 'queued', entry_count = 0, parsed_count = 0,
		    failed_count = 0, imported_at = NULL
		WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if err := requireChanged(result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkSourceImporting(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET status = 'importing' WHERE id = ?`, id)
	return err
}

func (s *Store) CompleteSource(ctx context.Context, id string, total, parsed, failed int64, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE sources
		SET status = ?, entry_count = ?, parsed_count = ?, failed_count = ?, imported_at = ?
		WHERE id = ?`, status, total, parsed, failed, now, id)
	return err
}

func (s *Store) MarkSourceFailed(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) DeleteSourceRecord(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) CreateJob(ctx context.Context, job domain.ImportJob) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO import_jobs (id, source_id, status, total_bytes)
		VALUES (?, ?, ?, ?)`, job.ID, job.SourceID, job.Status, job.TotalBytes)
	return err
}

func (s *Store) StartJob(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE import_jobs SET status = 'running', started_at = ? WHERE id = ?`, now, id)
	return err
}

func (s *Store) UpdateJobProgress(ctx context.Context, id string, bytes, entries, failures int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE import_jobs
		SET bytes_processed = ?, entries_found = ?, parse_failures = ?
		WHERE id = ?`, bytes, entries, failures, id)
	return err
}

func (s *Store) FinishJob(ctx context.Context, id, status, code, message string, bytes, entries, failures int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE import_jobs
		SET status = ?, bytes_processed = ?, entries_found = ?, parse_failures = ?,
		    completed_at = ?, error_code = NULLIF(?, ''), error_message = NULLIF(?, '')
		WHERE id = ?`, status, bytes, entries, failures, now, code, message, id)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (domain.ImportJob, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, source_id, status, bytes_processed, total_bytes, entries_found,
		       parse_failures, started_at, completed_at, COALESCE(error_code, ''),
		       COALESCE(error_message, '')
		FROM import_jobs WHERE id = ?`, id)
	var job domain.ImportJob
	var started, completed sql.NullString
	err := row.Scan(&job.ID, &job.SourceID, &job.Status, &job.BytesProcessed,
		&job.TotalBytes, &job.EntriesFound, &job.ParseFailures, &started, &completed,
		&job.ErrorCode, &job.ErrorMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ImportJob{}, ErrNotFound
	}
	if err != nil {
		return domain.ImportJob{}, err
	}
	job.StartedAt = parseNullableTime(started)
	job.CompletedAt = parseNullableTime(completed)
	return job, nil
}

func (s *Store) InterruptRunningJobs(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE import_jobs
		SET status = 'interrupted', completed_at = ?, error_code = 'server_restarted',
		    error_message = 'The server restarted during import'
		WHERE status IN ('queued', 'running')`, now)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE sources SET status = 'failed' WHERE status IN ('queued', 'importing')`)
	return err
}

func (s *Store) InsertEntries(ctx context.Context, entries []domain.IndexedEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO entries (
			source_id, entry_number, byte_offset, byte_length, timestamp_ns,
			level, service, request_id, parse_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, entry := range entries {
		var timestamp any
		if entry.Timestamp != nil {
			timestamp = entry.Timestamp.UnixNano()
		}
		if _, err := stmt.ExecContext(ctx, entry.SourceID, entry.Number, entry.ByteOffset,
			entry.ByteLength, timestamp, nullable(entry.Level), nullable(entry.Service),
			nullable(entry.RequestID), entry.ParseStatus); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) QueryEntryMetadata(ctx context.Context, sourceID string, filter domain.EntryFilter) ([]domain.IndexedEntry, bool, error) {
	limit := filter.Limit
	if limit < 1 || limit > 500 {
		limit = 200
	}
	args := []any{sourceID, filter.Cursor}
	where := []string{"source_id = ?", "entry_number >= ?"}
	addInFilter := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		placeholders := make([]string, len(values))
		for i, value := range values {
			placeholders[i] = "?"
			args = append(args, value)
		}
		where = append(where, column+" IN ("+strings.Join(placeholders, ",")+")")
	}
	addInFilter("level", filter.Levels)
	addInFilter("service", filter.Services)
	addInFilter("parse_status", filter.ParseStatus)
	if filter.RequestID != "" {
		where = append(where, "request_id = ?")
		args = append(args, filter.RequestID)
	}
	if filter.From != nil {
		where = append(where, "timestamp_ns >= ?")
		args = append(args, filter.From.UnixNano())
	}
	if filter.To != nil {
		where = append(where, "timestamp_ns <= ?")
		args = append(args, filter.To.UnixNano())
	}
	args = append(args, limit+1)
	query := `
		SELECT source_id, entry_number, byte_offset, byte_length, timestamp_ns,
		       COALESCE(level, ''), COALESCE(service, ''), COALESCE(request_id, ''),
		       parse_status
		FROM entries WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY entry_number LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]domain.IndexedEntry, 0, limit+1)
	for rows.Next() {
		var entry domain.IndexedEntry
		var timestamp sql.NullInt64
		if err := rows.Scan(&entry.SourceID, &entry.Number, &entry.ByteOffset,
			&entry.ByteLength, &timestamp, &entry.Level, &entry.Service,
			&entry.RequestID, &entry.ParseStatus); err != nil {
			return nil, false, err
		}
		if timestamp.Valid {
			value := time.Unix(0, timestamp.Int64).UTC()
			entry.Timestamp = &value
		}
		result = append(result, entry)
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, rows.Err()
}

func (s *Store) GetEntryMetadata(ctx context.Context, sourceID string, number int64) (domain.IndexedEntry, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT source_id, entry_number, byte_offset, byte_length, timestamp_ns,
		       COALESCE(level, ''), COALESCE(service, ''), COALESCE(request_id, ''),
		       parse_status
		FROM entries WHERE source_id = ? AND entry_number = ?`, sourceID, number)
	var entry domain.IndexedEntry
	var timestamp sql.NullInt64
	err := row.Scan(&entry.SourceID, &entry.Number, &entry.ByteOffset, &entry.ByteLength,
		&timestamp, &entry.Level, &entry.Service, &entry.RequestID, &entry.ParseStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IndexedEntry{}, ErrNotFound
	}
	if timestamp.Valid {
		value := time.Unix(0, timestamp.Int64).UTC()
		entry.Timestamp = &value
	}
	return entry, err
}

func (s *Store) Statistics(ctx context.Context, sourceID string) (domain.Statistics, error) {
	stats := domain.Statistics{ByLevel: map[string]int64{}, ByService: map[string]int64{}}
	var earliest, latest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN parse_status = 'parsed' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN parse_status IN ('malformed', 'oversized') THEN 1 ELSE 0 END),
		       MIN(timestamp_ns), MAX(timestamp_ns)
		FROM entries WHERE source_id = ?`, sourceID).Scan(
		&stats.TotalEntries, &stats.ParsedEntries, &stats.MalformedEntries, &earliest, &latest)
	if err != nil {
		return stats, err
	}
	if earliest.Valid {
		value := time.Unix(0, earliest.Int64).UTC()
		stats.Earliest = &value
	}
	if latest.Valid {
		value := time.Unix(0, latest.Int64).UTC()
		stats.Latest = &value
	}
	if err := s.readCounts(ctx, sourceID, "level", stats.ByLevel); err != nil {
		return stats, err
	}
	if err := s.readCounts(ctx, sourceID, "service", stats.ByService); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *Store) Timeline(ctx context.Context, sourceID string) ([]domain.TimelinePoint, error) {
	var minValue, maxValue sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(timestamp_ns), MAX(timestamp_ns) FROM entries
		WHERE source_id = ? AND timestamp_ns IS NOT NULL`, sourceID).Scan(&minValue, &maxValue); err != nil {
		return nil, err
	}
	if !minValue.Valid || !maxValue.Valid {
		return []domain.TimelinePoint{}, nil
	}
	rangeDuration := time.Duration(maxValue.Int64 - minValue.Int64)
	bucket := time.Hour
	switch {
	case rangeDuration <= 10*time.Minute:
		bucket = 10 * time.Second
	case rangeDuration <= 2*time.Hour:
		bucket = time.Minute
	case rangeDuration <= 48*time.Hour:
		bucket = 15 * time.Minute
	}
	bucketNs := int64(bucket)
	rows, err := s.db.QueryContext(ctx, `
		SELECT (timestamp_ns / ?) * ? AS bucket,
		       COUNT(*),
		       SUM(CASE WHEN level IN ('ERROR', 'FATAL') THEN 1 ELSE 0 END)
		FROM entries
		WHERE source_id = ? AND timestamp_ns IS NOT NULL
		GROUP BY bucket ORDER BY bucket`, bucketNs, bucketNs, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	points := make([]domain.TimelinePoint, 0)
	for rows.Next() {
		var timestampNs, count, errorsCount int64
		if err := rows.Scan(&timestampNs, &count, &errorsCount); err != nil {
			return nil, err
		}
		points = append(points, domain.TimelinePoint{
			Timestamp: time.Unix(0, timestampNs).UTC(), Count: count, Errors: errorsCount,
		})
	}
	return points, rows.Err()
}

func (s *Store) readCounts(ctx context.Context, sourceID, column string, target map[string]int64) error {
	query := fmt.Sprintf(`
		SELECT COALESCE(%s, ''), COUNT(*) FROM entries
		WHERE source_id = ? AND COALESCE(%s, '') <> ''
		GROUP BY %s ORDER BY COUNT(*) DESC LIMIT 50`, column, column, column)
	rows, err := s.db.QueryContext(ctx, query, sourceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		target[key] = count
	}
	return rows.Err()
}

func scanSource(scanner interface{ Scan(...any) error }) (domain.Source, error) {
	var source domain.Source
	var created string
	var imported sql.NullString
	err := scanner.Scan(&source.ID, &source.Name, &source.OriginalName, &source.StoredPath,
		&source.Format, &source.Status, &source.SizeBytes, &source.EntryCount,
		&source.ParsedCount, &source.FailedCount, &created, &imported)
	if err != nil {
		return domain.Source{}, err
	}
	source.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return domain.Source{}, err
	}
	source.ImportedAt = parseNullableTime(imported)
	return source, nil
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func requireChanged(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
