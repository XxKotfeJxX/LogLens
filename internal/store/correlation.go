package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"loglens/internal/domain"
)

func (s *Store) CreateSourceGroup(ctx context.Context, group domain.SourceGroup) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_groups (id, name, description, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?)`,
		group.ID, group.Name, group.Description,
		group.CreatedAt.Format(time.RFC3339Nano), group.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListSourceGroups(ctx context.Context) ([]domain.SourceGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, COALESCE(g.description, ''), COUNT(m.source_id),
		       g.created_at, g.updated_at
		FROM source_groups g
		LEFT JOIN source_group_members m ON m.source_group_id = g.id
		GROUP BY g.id
		ORDER BY g.updated_at DESC, g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.SourceGroup, 0)
	for rows.Next() {
		group, err := scanSourceGroup(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, group)
	}
	return result, rows.Err()
}

func (s *Store) GetSourceGroup(ctx context.Context, id string) (domain.SourceGroup, error) {
	group, err := scanSourceGroup(s.db.QueryRowContext(ctx, `
		SELECT g.id, g.name, COALESCE(g.description, ''), COUNT(m.source_id),
		       g.created_at, g.updated_at
		FROM source_groups g
		LEFT JOIN source_group_members m ON m.source_group_id = g.id
		WHERE g.id = ?
		GROUP BY g.id`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SourceGroup{}, ErrNotFound
	}
	if err != nil {
		return domain.SourceGroup{}, err
	}
	group.Members, err = s.ListSourceGroupMembers(ctx, id)
	if err != nil {
		return domain.SourceGroup{}, err
	}
	group.Mappings, err = s.ListCanonicalMappings(ctx, id)
	return group, err
}

func scanSourceGroup(scanner interface{ Scan(...any) error }) (domain.SourceGroup, error) {
	var group domain.SourceGroup
	var createdAt, updatedAt string
	if err := scanner.Scan(&group.ID, &group.Name, &group.Description, &group.MemberCount, &createdAt, &updatedAt); err != nil {
		return domain.SourceGroup{}, err
	}
	var err error
	group.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return domain.SourceGroup{}, err
	}
	group.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return group, err
}

func (s *Store) DeleteSourceGroup(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM source_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) UpdateSourceGroup(ctx context.Context, group domain.SourceGroup) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE source_groups
		SET name = ?, description = NULLIF(?, ''), updated_at = ?
		WHERE id = ?`,
		group.Name, group.Description, group.UpdatedAt.Format(time.RFC3339Nano), group.ID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) UpsertSourceGroupMember(ctx context.Context, member domain.SourceGroupMember) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var groupExists, sourceExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_groups WHERE id = ?`, member.SourceGroupID).Scan(&groupExists); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sources WHERE id = ? AND status <> 'deleted'`, member.SourceID).Scan(&sourceExists); err != nil {
		return err
	}
	if groupExists == 0 || sourceExists == 0 {
		return ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO source_group_members
		    (source_group_id, source_id, time_zone, manual_clock_offset_ms, added_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source_group_id, source_id) DO UPDATE SET
		    time_zone = excluded.time_zone,
		    manual_clock_offset_ms = excluded.manual_clock_offset_ms`,
		member.SourceGroupID, member.SourceID, member.TimeZone,
		member.ManualClockOffsetMS, member.AddedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE source_groups SET updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), member.SourceGroupID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListSourceGroupMembers(ctx context.Context, groupID string) ([]domain.SourceGroupMember, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.source_group_id, m.source_id, s.name, m.time_zone,
		       m.manual_clock_offset_ms, m.added_at
		FROM source_group_members m
		JOIN sources s ON s.id = m.source_id
		WHERE m.source_group_id = ? AND s.status <> 'deleted'
		ORDER BY m.added_at, s.name`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.SourceGroupMember, 0)
	for rows.Next() {
		var member domain.SourceGroupMember
		var addedAt string
		if err := rows.Scan(&member.SourceGroupID, &member.SourceID, &member.SourceName,
			&member.TimeZone, &member.ManualClockOffsetMS, &addedAt); err != nil {
			return nil, err
		}
		member.AddedAt, err = time.Parse(time.RFC3339Nano, addedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, member)
	}
	return result, rows.Err()
}

func (s *Store) DeleteSourceGroupMember(ctx context.Context, groupID, sourceID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM canonical_field_mappings
		WHERE source_group_id = ? AND source_id = ?`, groupID, sourceID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM source_group_members WHERE source_group_id = ? AND source_id = ?`,
		groupID, sourceID)
	if err != nil {
		return err
	}
	if err := requireChanged(result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertCanonicalMapping(ctx context.Context, mapping domain.CanonicalFieldMapping) error {
	var memberExists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM source_group_members
		WHERE source_group_id = ? AND source_id = ?`,
		mapping.SourceGroupID, mapping.SourceID).Scan(&memberExists); err != nil {
		return err
	}
	if memberExists == 0 {
		return ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO canonical_field_mappings
		    (id, source_group_id, source_id, canonical_name, source_field, field_type, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_group_id, source_id, canonical_name) DO UPDATE SET
		    source_field = excluded.source_field,
		    field_type = excluded.field_type`,
		mapping.ID, mapping.SourceGroupID, mapping.SourceID, mapping.CanonicalName,
		mapping.SourceField, mapping.FieldType, mapping.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListCanonicalMappings(ctx context.Context, groupID string) ([]domain.CanonicalFieldMapping, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_group_id, source_id, canonical_name, source_field,
		       field_type, created_at
		FROM canonical_field_mappings WHERE source_group_id = ?
		ORDER BY canonical_name, source_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.CanonicalFieldMapping, 0)
	for rows.Next() {
		var mapping domain.CanonicalFieldMapping
		var createdAt string
		if err := rows.Scan(&mapping.ID, &mapping.SourceGroupID, &mapping.SourceID,
			&mapping.CanonicalName, &mapping.SourceField, &mapping.FieldType, &createdAt); err != nil {
			return nil, err
		}
		mapping.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		result = append(result, mapping)
	}
	return result, rows.Err()
}

func (s *Store) DeleteCanonicalMapping(ctx context.Context, groupID, mappingID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM canonical_field_mappings WHERE id = ? AND source_group_id = ?`,
		mappingID, groupID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}
