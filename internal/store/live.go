package store

import (
	"context"

	"loglens/internal/domain"
)

func (s *Store) StartWatching(ctx context.Context, sourceID string, offset int64, identity string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sources
		SET source_type = 'watched_file', watch_status = 'running',
		    watch_offset = ?, file_identity = NULLIF(?, '')
		WHERE id = ?`, offset, identity, sourceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) StopWatching(ctx context.Context, sourceID, status string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sources SET watch_status = ? WHERE id = ?`, status, sourceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) ListWatchedSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, original_name, stored_path, source_type, format, status, size_bytes,
		       entry_count, parsed_count, failed_count, parser_profile_id, parser_version,
		       extraction_status, reindex_required, watch_status, watch_offset,
		       COALESCE(file_identity, ''), rotation_generation, created_at, imported_at
		FROM sources
		WHERE source_type = 'watched_file' AND watch_status = 'running'
		ORDER BY created_at`)
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

func (s *Store) AppendLiveEntries(
	ctx context.Context,
	sourceID string,
	entries []domain.IndexedEntry,
	offset, currentSize int64,
	identity string,
	rotated bool,
) error {
	if len(entries) == 0 {
		_, err := s.db.ExecContext(ctx, `
			UPDATE sources
			SET watch_offset = ?, size_bytes = ?, file_identity = NULLIF(?, ''),
			    rotation_generation = rotation_generation + ?
			WHERE id = ?`, offset, currentSize, identity, boolInt(rotated), sourceID)
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertEntriesTx(ctx, tx, entries); err != nil {
		return err
	}
	var parsed, failed int64
	for _, entry := range entries {
		if entry.ParseStatus == "parsed" {
			parsed++
		}
		if entry.ParseStatus == "malformed" || entry.ParseStatus == "oversized" {
			failed++
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources
		SET entry_count = entry_count + ?, parsed_count = parsed_count + ?,
		    failed_count = failed_count + ?, watch_offset = ?, size_bytes = ?,
		    file_identity = NULLIF(?, ''),
		    rotation_generation = rotation_generation + ?
		WHERE id = ?`,
		len(entries), parsed, failed, offset, currentSize, identity,
		boolInt(rotated), sourceID); err != nil {
		return err
	}
	return tx.Commit()
}
