package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"loglens/internal/domain"
)

func (s *Store) CreateParserProfile(ctx context.Context, profile domain.ParserProfile) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO parser_profiles (id, name, description, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?)`,
		profile.ID, profile.Name, profile.Description,
		profile.CreatedAt.Format(time.RFC3339Nano), profile.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListParserProfiles(ctx context.Context) ([]domain.ParserProfile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(description, ''), created_at, updated_at
		FROM parser_profiles ORDER BY updated_at DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := make([]domain.ParserProfile, 0)
	for rows.Next() {
		profile, err := scanParserProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (s *Store) GetParserProfile(ctx context.Context, id string) (domain.ParserProfile, error) {
	profile, err := scanParserProfile(s.db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(description, ''), created_at, updated_at
		FROM parser_profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ParserProfile{}, ErrNotFound
	}
	return profile, err
}

func scanParserProfile(scanner interface{ Scan(...any) error }) (domain.ParserProfile, error) {
	var profile domain.ParserProfile
	var createdAt, updatedAt string
	if err := scanner.Scan(&profile.ID, &profile.Name, &profile.Description, &createdAt, &updatedAt); err != nil {
		return domain.ParserProfile{}, err
	}
	var err error
	profile.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return domain.ParserProfile{}, err
	}
	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return profile, err
}

func (s *Store) CreateParserVersion(ctx context.Context, version *domain.ParserProfileVersion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var profileExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM parser_profiles WHERE id = ?`, version.ProfileID).Scan(&profileExists); err != nil {
		return err
	}
	if profileExists == 0 {
		return ErrNotFound
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM parser_profile_versions WHERE profile_id = ?`, version.ProfileID).Scan(&version.Version); err != nil {
		return err
	}
	if version.ID == "" {
		version.ID = uuid.NewString()
	}
	if version.CreatedAt.IsZero() {
		version.CreatedAt = time.Now().UTC()
	}
	if version.Configuration == nil {
		version.Configuration = map[string]any{}
	}
	configuration, err := json.Marshal(version.Configuration)
	if err != nil {
		return fmt.Errorf("encode parser configuration: %w", err)
	}
	version.ConfigurationJSON = string(configuration)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO parser_profile_versions
		    (id, profile_id, version, record_format, configuration_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		version.ID, version.ProfileID, version.Version, version.RecordFormat,
		version.ConfigurationJSON, version.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for index := range version.Rules {
		rule := &version.Rules[index]
		if rule.ID == "" {
			rule.ID = uuid.NewString()
		}
		rule.ParserVersionID = version.ID
		config, err := json.Marshal(rule.MatcherConfig)
		if err != nil {
			return fmt.Errorf("encode matcher %q: %w", rule.Name, err)
		}
		rule.MatcherConfigJSON = string(config)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO extraction_rules
			    (id, parser_version_id, name, event_type, priority, matcher_type, matcher_config_json, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			rule.ID, version.ID, rule.Name, rule.EventType, rule.Priority,
			rule.MatcherType, rule.MatcherConfigJSON, boolInt(rule.Enabled)); err != nil {
			return err
		}
	}
	for index := range version.Fields {
		field := &version.Fields[index]
		if field.ID == "" {
			field.ID = uuid.NewString()
		}
		field.ParserVersionID = version.ID
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO field_definitions
			    (id, parser_version_id, name, display_name, field_type, indexed, aggregatable, nullable)
			VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`,
			field.ID, version.ID, field.Name, field.DisplayName, field.FieldType,
			boolInt(field.Indexed), boolInt(field.Aggregatable), boolInt(field.Nullable)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE parser_profiles SET updated_at = ? WHERE id = ?`,
		version.CreatedAt.Format(time.RFC3339Nano), version.ProfileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListParserVersions(ctx context.Context, profileID string) ([]domain.ParserProfileVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, profile_id, version, record_format, configuration_json, created_at
		FROM parser_profile_versions WHERE profile_id = ? ORDER BY version DESC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]domain.ParserProfileVersion, 0)
	for rows.Next() {
		version, err := scanParserVersion(rows)
		if err != nil {
			return nil, err
		}
		if err := s.loadParserVersionChildren(ctx, &version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Store) GetParserVersion(ctx context.Context, profileID string, versionNumber int) (domain.ParserProfileVersion, error) {
	version, err := scanParserVersion(s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, version, record_format, configuration_json, created_at
		FROM parser_profile_versions WHERE profile_id = ? AND version = ?`,
		profileID, versionNumber))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ParserProfileVersion{}, ErrNotFound
	}
	if err != nil {
		return domain.ParserProfileVersion{}, err
	}
	if err := s.loadParserVersionChildren(ctx, &version); err != nil {
		return domain.ParserProfileVersion{}, err
	}
	return version, nil
}

func (s *Store) GetParserVersionByID(ctx context.Context, id string) (domain.ParserProfileVersion, error) {
	version, err := scanParserVersion(s.db.QueryRowContext(ctx, `
		SELECT id, profile_id, version, record_format, configuration_json, created_at
		FROM parser_profile_versions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ParserProfileVersion{}, ErrNotFound
	}
	if err != nil {
		return domain.ParserProfileVersion{}, err
	}
	if err := s.loadParserVersionChildren(ctx, &version); err != nil {
		return domain.ParserProfileVersion{}, err
	}
	return version, nil
}

func scanParserVersion(scanner interface{ Scan(...any) error }) (domain.ParserProfileVersion, error) {
	var version domain.ParserProfileVersion
	var createdAt string
	if err := scanner.Scan(&version.ID, &version.ProfileID, &version.Version,
		&version.RecordFormat, &version.ConfigurationJSON, &createdAt); err != nil {
		return domain.ParserProfileVersion{}, err
	}
	if err := json.Unmarshal([]byte(version.ConfigurationJSON), &version.Configuration); err != nil {
		return domain.ParserProfileVersion{}, err
	}
	var err error
	version.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	return version, err
}

func (s *Store) loadParserVersionChildren(ctx context.Context, version *domain.ParserProfileVersion) error {
	rules, err := s.db.QueryContext(ctx, `
		SELECT id, parser_version_id, name, event_type, priority, matcher_type,
		       matcher_config_json, enabled
		FROM extraction_rules WHERE parser_version_id = ?
		ORDER BY priority ASC, id ASC`, version.ID)
	if err != nil {
		return err
	}
	for rules.Next() {
		var rule domain.ExtractionRule
		var enabled int
		if err := rules.Scan(&rule.ID, &rule.ParserVersionID, &rule.Name, &rule.EventType,
			&rule.Priority, &rule.MatcherType, &rule.MatcherConfigJSON, &enabled); err != nil {
			rules.Close()
			return err
		}
		rule.Enabled = enabled != 0
		if err := json.Unmarshal([]byte(rule.MatcherConfigJSON), &rule.MatcherConfig); err != nil {
			rules.Close()
			return err
		}
		version.Rules = append(version.Rules, rule)
	}
	if err := rules.Close(); err != nil {
		return err
	}

	fields, err := s.db.QueryContext(ctx, `
		SELECT id, parser_version_id, name, COALESCE(display_name, ''), field_type,
		       indexed, aggregatable, nullable
		FROM field_definitions WHERE parser_version_id = ? ORDER BY name`, version.ID)
	if err != nil {
		return err
	}
	defer fields.Close()
	for fields.Next() {
		var field domain.FieldDefinition
		var indexed, aggregatable, nullable int
		if err := fields.Scan(&field.ID, &field.ParserVersionID, &field.Name,
			&field.DisplayName, &field.FieldType, &indexed, &aggregatable, &nullable); err != nil {
			return err
		}
		field.Indexed = indexed != 0
		field.Aggregatable = aggregatable != 0
		field.Nullable = nullable != 0
		version.Fields = append(version.Fields, field)
	}
	return fields.Err()
}

func (s *Store) AssignSourceParser(ctx context.Context, sourceID, profileID string, versionNumber int) error {
	var versionID string
	if err := s.db.QueryRowContext(ctx, `
		SELECT id FROM parser_profile_versions
		WHERE profile_id = ? AND version = ?`, profileID, versionNumber).Scan(&versionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE sources
		SET parser_profile_id = ?, parser_version = ?, extraction_status = 'configured',
		    reindex_required = CASE WHEN entry_count > 0 THEN 1 ELSE 0 END
		WHERE id = ?`, profileID, versionNumber, sourceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) CreateReindexJob(ctx context.Context, job domain.ReindexJob) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reindex_jobs
		    (id, source_id, parser_version_id, status, total_entries)
		VALUES (?, ?, ?, ?, ?)`,
		job.ID, job.SourceID, job.ParserVersionID, job.Status, job.TotalEntries); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sources SET extraction_status = 'indexing' WHERE id = ?`, job.SourceID)
	if err != nil {
		return err
	}
	if err := requireChanged(result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StartReindexJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE reindex_jobs SET status = 'running', started_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) UpdateReindexProgress(ctx context.Context, id string, processed, warnings int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE reindex_jobs SET entries_processed = ?, warnings = ? WHERE id = ?`,
		processed, warnings, id)
	return err
}

func (s *Store) FinishReindexJob(ctx context.Context, id, sourceID, status, sourceStatus, message string, processed, warnings int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE reindex_jobs
		SET status = ?, entries_processed = ?, warnings = ?, completed_at = ?,
		    error_message = NULLIF(?, '')
		WHERE id = ?`,
		status, processed, warnings, time.Now().UTC().Format(time.RFC3339Nano), message, id); err != nil {
		return err
	}
	reindexRequired := 1
	if status == "completed" {
		reindexRequired = 0
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sources SET extraction_status = ?, reindex_required = ? WHERE id = ?`,
		sourceStatus, reindexRequired, sourceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetReindexJob(ctx context.Context, id string) (domain.ReindexJob, error) {
	return scanReindexJob(s.db.QueryRowContext(ctx, `
		SELECT id, source_id, parser_version_id, status, entries_processed,
		       total_entries, warnings, started_at, completed_at, COALESCE(error_message, '')
		FROM reindex_jobs WHERE id = ?`, id))
}

func (s *Store) GetLatestReindexJob(ctx context.Context, sourceID string) (domain.ReindexJob, error) {
	return scanReindexJob(s.db.QueryRowContext(ctx, `
		SELECT id, source_id, parser_version_id, status, entries_processed,
		       total_entries, warnings, started_at, completed_at, COALESCE(error_message, '')
		FROM reindex_jobs WHERE source_id = ?
		ORDER BY COALESCE(started_at, ''), rowid DESC LIMIT 1`, sourceID))
}

func scanReindexJob(scanner interface{ Scan(...any) error }) (domain.ReindexJob, error) {
	var job domain.ReindexJob
	var started, completed sql.NullString
	err := scanner.Scan(&job.ID, &job.SourceID, &job.ParserVersionID, &job.Status,
		&job.EntriesProcessed, &job.TotalEntries, &job.Warnings, &started,
		&completed, &job.ErrorMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReindexJob{}, ErrNotFound
	}
	if err != nil {
		return domain.ReindexJob{}, err
	}
	job.StartedAt = parseNullableTime(started)
	job.CompletedAt = parseNullableTime(completed)
	return job, nil
}

func (s *Store) InterruptRunningReindexes(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE reindex_jobs
		SET status = 'interrupted', completed_at = ?,
		    error_message = 'The server restarted during reindex'
		WHERE status IN ('queued', 'running')`, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE sources SET extraction_status = 'partial', reindex_required = 1
		WHERE extraction_status = 'indexing'`)
	return err
}

func (s *Store) ApplyExtractionBatch(ctx context.Context, updates []domain.ExtractionUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updateEntry, err := tx.PrepareContext(ctx, `
		UPDATE entries
		SET event_type = NULLIF(?, ''), matched_rule_id = NULLIF(?, ''),
		    custom_fields_json = ?, parser_version = ?, warning_count = ?,
		    parse_status = ?
		WHERE id = ?`)
	if err != nil {
		return err
	}
	defer updateEntry.Close()
	deleteFields, err := tx.PrepareContext(ctx, `DELETE FROM entry_field_values WHERE entry_id = ?`)
	if err != nil {
		return err
	}
	defer deleteFields.Close()
	insertField, err := tx.PrepareContext(ctx, `
		INSERT INTO entry_field_values
		    (entry_id, field_definition_id, value_text, value_integer, value_real,
		     value_boolean, value_timestamp_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertField.Close()
	for _, update := range updates {
		if _, err := updateEntry.ExecContext(ctx, update.EventType, update.MatchedRuleID,
			update.FieldsJSON, update.ParserVersion, update.WarningCount,
			update.ParseStatus, update.EntryID); err != nil {
			return err
		}
		if _, err := deleteFields.ExecContext(ctx, update.EntryID); err != nil {
			return err
		}
		for fieldID, value := range update.IndexedValues {
			textValue, integerValue, realValue, booleanValue, timestampValue := fieldColumns(value)
			if _, err := insertField.ExecContext(ctx, update.EntryID, fieldID,
				textValue, integerValue, realValue, booleanValue, timestampValue); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ExtractionStatistics(ctx context.Context, sourceID string) (domain.ExtractionStatistics, error) {
	stats := domain.ExtractionStatistics{ByEventType: map[string]int64{}}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN event_type IS NOT NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN event_type IS NULL THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN warning_count > 0 THEN 1 ELSE 0 END), 0)
		FROM entries WHERE source_id = ?`, sourceID).Scan(
		&stats.TotalEntries, &stats.MatchedEntries, &stats.UnmatchedEntries, &stats.WarningEntries); err != nil {
		return stats, err
	}
	if stats.TotalEntries > 0 {
		stats.Coverage = float64(stats.MatchedEntries) / float64(stats.TotalEntries)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_type, COUNT(*) FROM entries
		WHERE source_id = ? AND event_type IS NOT NULL
		GROUP BY event_type ORDER BY COUNT(*) DESC`, sourceID)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var eventType string
		var count int64
		if err := rows.Scan(&eventType, &count); err != nil {
			return stats, err
		}
		stats.ByEventType[eventType] = count
	}
	return stats, rows.Err()
}

func (s *Store) SourceFields(ctx context.Context, sourceID string) ([]domain.FieldStatistics, error) {
	source, err := s.GetSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if source.ParserProfileID == nil || source.ParserVersion == nil {
		return []domain.FieldStatistics{}, nil
	}
	version, err := s.GetParserVersion(ctx, *source.ParserProfileID, *source.ParserVersion)
	if err != nil {
		return nil, err
	}
	result := make([]domain.FieldStatistics, 0, len(version.Fields))
	for _, field := range version.Fields {
		var present int64
		path := "$." + field.Name
		if err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM entries
			WHERE source_id = ? AND json_type(COALESCE(custom_fields_json, '{}'), ?) IS NOT NULL`,
			sourceID, path).Scan(&present); err != nil {
			return nil, err
		}
		stats := domain.FieldStatistics{
			FieldDefinition: field, PresentEntries: present, TotalEntries: source.EntryCount,
		}
		if source.EntryCount > 0 {
			stats.Coverage = float64(present) / float64(source.EntryCount)
		}
		result = append(result, stats)
	}
	return result, nil
}

func fieldColumns(value domain.FieldValue) (any, any, any, any, any) {
	switch value.Type {
	case domain.FieldInteger:
		if value.IntegerValue != nil {
			return nil, *value.IntegerValue, nil, nil, nil
		}
	case domain.FieldDuration:
		if value.DurationValue != nil {
			return nil, int64(*value.DurationValue), nil, nil, nil
		}
	case domain.FieldFloat:
		if value.FloatValue != nil {
			return nil, nil, *value.FloatValue, nil, nil
		}
	case domain.FieldBoolean:
		if value.BooleanValue != nil {
			return nil, nil, nil, boolInt(*value.BooleanValue), nil
		}
	case domain.FieldDateTime:
		if value.TimestampValue != nil {
			return nil, nil, nil, nil, value.TimestampValue.UnixNano()
		}
	default:
		if value.StringValue != nil {
			return *value.StringValue, nil, nil, nil, nil
		}
	}
	return nil, nil, nil, nil, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
