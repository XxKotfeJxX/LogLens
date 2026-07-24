package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"loglens/internal/domain"
)

var ErrInvalidQuery = errors.New("invalid structured query")

type resolvedField struct {
	name       string
	fieldType  domain.FieldType
	baseColumn string
	definition *domain.FieldDefinition
}

func (s *Store) QueryStructuredEntries(
	ctx context.Context,
	sourceID string,
	request domain.StructuredQuery,
) ([]domain.IndexedEntry, error) {
	fields, err := s.queryFields(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	where := []string{"e.source_id = ?"}
	args := []any{sourceID}
	for index, filter := range request.Filters {
		clause, values, err := buildTypedFilter(filter, fields, index)
		if err != nil {
			return nil, err
		}
		where = append(where, clause)
		args = append(args, values...)
	}
	order := "e.entry_number ASC"
	if len(request.Sort) > 1 {
		return nil, fmt.Errorf("%w: one sort field is supported", ErrInvalidQuery)
	}
	if len(request.Sort) == 1 {
		sort := request.Sort[0]
		direction := strings.ToUpper(sort.Direction)
		if direction == "" {
			direction = "ASC"
		}
		if direction != "ASC" && direction != "DESC" {
			return nil, fmt.Errorf("%w: sort direction must be asc or desc", ErrInvalidQuery)
		}
		switch sort.Field {
		case "timestamp":
			order = "e.timestamp_ns " + direction + ", e.entry_number " + direction
		case "entry_number", "number":
			order = "e.entry_number " + direction
		default:
			return nil, fmt.Errorf("%w: sorting by %q is not supported", ErrInvalidQuery, sort.Field)
		}
	}
	limit := request.Limit
	if limit < 1 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.source_id, e.entry_number, e.byte_offset, e.byte_length,
		       e.timestamp_ns, COALESCE(e.level, ''), COALESCE(e.service, ''),
		       COALESCE(e.request_id, ''), e.parse_status,
		       COALESCE(e.event_type, ''), COALESCE(e.matched_rule_id, ''),
		       COALESCE(e.custom_fields_json, '{}'), e.parser_version, e.warning_count
		FROM entries e
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStructuredRows(rows)
}

func (s *Store) AggregateStructuredEntries(
	ctx context.Context,
	sourceID string,
	request domain.AggregationRequest,
) (domain.AggregationResult, error) {
	fields, err := s.queryFields(ctx, sourceID)
	if err != nil {
		return domain.AggregationResult{}, err
	}
	operation := strings.ToLower(request.Operation)
	sqlOperation := map[string]string{
		"count": "COUNT", "sum": "SUM", "avg": "AVG", "average": "AVG",
		"min": "MIN", "max": "MAX",
	}[operation]
	if sqlOperation == "" {
		return domain.AggregationResult{}, fmt.Errorf("%w: unsupported aggregation %q", ErrInvalidQuery, request.Operation)
	}
	if operation == "average" {
		operation = "avg"
	}

	joins := make([]string, 0, 2)
	joinArgs := make([]any, 0, 2)
	measureExpression := "*"
	if operation != "count" {
		field, ok := fields[request.Field]
		if !ok || field.definition == nil {
			return domain.AggregationResult{}, fmt.Errorf("%w: aggregation field %q is not a dynamic field", ErrInvalidQuery, request.Field)
		}
		if !field.definition.Aggregatable {
			return domain.AggregationResult{}, fmt.Errorf("%w: field %q is not aggregatable", ErrInvalidQuery, request.Field)
		}
		if field.fieldType != domain.FieldInteger && field.fieldType != domain.FieldFloat &&
			field.fieldType != domain.FieldDuration {
			return domain.AggregationResult{}, fmt.Errorf("%w: field %q is not numeric", ErrInvalidQuery, request.Field)
		}
		joins = append(joins, `JOIN entry_field_values measure
			ON measure.entry_id = e.id AND measure.field_definition_id = ?`)
		joinArgs = append(joinArgs, field.definition.ID)
		measureExpression = typedValueColumn("measure", field.fieldType)
	}

	groupExpression := "NULL"
	if request.GroupBy != "" {
		field, ok := fields[request.GroupBy]
		if !ok {
			return domain.AggregationResult{}, fmt.Errorf("%w: unknown group field %q", ErrInvalidQuery, request.GroupBy)
		}
		if field.definition == nil {
			if field.baseColumn == "timestamp_ns" || field.baseColumn == "entry_number" {
				return domain.AggregationResult{}, fmt.Errorf("%w: group field %q is not categorical", ErrInvalidQuery, request.GroupBy)
			}
			groupExpression = "e." + field.baseColumn
		} else {
			if !field.definition.Aggregatable {
				return domain.AggregationResult{}, fmt.Errorf("%w: field %q is not aggregatable", ErrInvalidQuery, request.GroupBy)
			}
			joins = append(joins, `LEFT JOIN entry_field_values grouping
				ON grouping.entry_id = e.id AND grouping.field_definition_id = ?`)
			joinArgs = append(joinArgs, field.definition.ID)
			groupExpression = typedValueColumn("grouping", field.fieldType)
		}
	}

	where := []string{"e.source_id = ?"}
	filterArgs := []any{sourceID}
	for index, filter := range request.Filters {
		clause, values, err := buildTypedFilter(filter, fields, index)
		if err != nil {
			return domain.AggregationResult{}, err
		}
		where = append(where, clause)
		filterArgs = append(filterArgs, values...)
	}
	query := `SELECT ` + groupExpression + `, ` + sqlOperation + `(` + measureExpression + `)
		FROM entries e ` + strings.Join(joins, " ") + `
		WHERE ` + strings.Join(where, " AND ")
	if request.GroupBy != "" {
		query += ` GROUP BY ` + groupExpression + ` ORDER BY ` + groupExpression
	}
	args := append(joinArgs, filterArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.AggregationResult{}, err
	}
	defer rows.Close()
	result := domain.AggregationResult{
		Operation: operation, Field: request.Field, GroupBy: request.GroupBy,
		Buckets: []domain.AggregationBucket{},
	}
	for rows.Next() {
		var group any
		var value sql.NullFloat64
		if err := rows.Scan(&group, &value); err != nil {
			return result, err
		}
		if !value.Valid {
			continue
		}
		result.Buckets = append(result.Buckets, domain.AggregationBucket{
			Group: normalizeSQLiteValue(group), Value: value.Float64,
		})
	}
	return result, rows.Err()
}

func (s *Store) queryFields(ctx context.Context, sourceID string) (map[string]resolvedField, error) {
	source, err := s.GetSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	fields := map[string]resolvedField{
		"event_type":   {name: "event_type", fieldType: domain.FieldString, baseColumn: "event_type"},
		"timestamp":    {name: "timestamp", fieldType: domain.FieldDateTime, baseColumn: "timestamp_ns"},
		"level":        {name: "level", fieldType: domain.FieldString, baseColumn: "level"},
		"service":      {name: "service", fieldType: domain.FieldString, baseColumn: "service"},
		"request_id":   {name: "request_id", fieldType: domain.FieldString, baseColumn: "request_id"},
		"parse_status": {name: "parse_status", fieldType: domain.FieldString, baseColumn: "parse_status"},
		"entry_number": {name: "entry_number", fieldType: domain.FieldInteger, baseColumn: "entry_number"},
	}
	if source.ParserProfileID == nil || source.ParserVersion == nil {
		return fields, nil
	}
	version, err := s.GetParserVersion(ctx, *source.ParserProfileID, *source.ParserVersion)
	if err != nil {
		return nil, err
	}
	for index := range version.Fields {
		field := version.Fields[index]
		fieldCopy := field
		fields[field.Name] = resolvedField{
			name: field.Name, fieldType: field.FieldType, definition: &fieldCopy,
		}
	}
	return fields, nil
}

func buildTypedFilter(filter domain.TypedFilter, fields map[string]resolvedField, index int) (string, []any, error) {
	field, ok := fields[filter.Field]
	if !ok {
		return "", nil, fmt.Errorf("%w: unknown field %q", ErrInvalidQuery, filter.Field)
	}
	if field.definition != nil && !field.definition.Indexed {
		return "", nil, fmt.Errorf("%w: field %q is visible but not indexed", ErrInvalidQuery, filter.Field)
	}
	operator := strings.ToLower(filter.Operator)
	if operator == "" {
		operator = "eq"
	}
	alias := fmt.Sprintf("filter_%d", index)
	expression := "e." + field.baseColumn
	args := make([]any, 0, 4)
	prefix, suffix := "", ""
	if field.definition != nil {
		expression = typedValueColumn(alias, field.fieldType)
		prefix = fmt.Sprintf(`EXISTS (
			SELECT 1 FROM entry_field_values %s
			WHERE %s.entry_id = e.id AND %s.field_definition_id = ? AND `,
			alias, alias, alias)
		suffix = ")"
		args = append(args, field.definition.ID)
	}
	clause, values, err := comparisonClause(expression, operator, filter.Value, field.fieldType)
	if err != nil {
		return "", nil, fmt.Errorf("%w: field %q: %v", ErrInvalidQuery, filter.Field, err)
	}
	args = append(args, values...)
	return prefix + clause + suffix, args, nil
}

func comparisonClause(expression, operator string, raw any, fieldType domain.FieldType) (string, []any, error) {
	sqlOperator := map[string]string{
		"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=",
	}[operator]
	if sqlOperator != "" {
		value, err := queryValue(raw, fieldType)
		if err != nil {
			return "", nil, err
		}
		return expression + " " + sqlOperator + " ?", []any{value}, nil
	}
	switch operator {
	case "in":
		values, ok := raw.([]any)
		if !ok || len(values) == 0 || len(values) > 100 {
			return "", nil, fmt.Errorf("in expects an array of 1 to 100 values")
		}
		placeholders := make([]string, len(values))
		args := make([]any, len(values))
		for index, value := range values {
			converted, err := queryValue(value, fieldType)
			if err != nil {
				return "", nil, err
			}
			placeholders[index] = "?"
			args[index] = converted
		}
		return expression + " IN (" + strings.Join(placeholders, ",") + ")", args, nil
	case "between":
		values, ok := raw.([]any)
		if !ok || len(values) != 2 {
			return "", nil, fmt.Errorf("between expects exactly two values")
		}
		first, err := queryValue(values[0], fieldType)
		if err != nil {
			return "", nil, err
		}
		second, err := queryValue(values[1], fieldType)
		if err != nil {
			return "", nil, err
		}
		return expression + " BETWEEN ? AND ?", []any{first, second}, nil
	case "contains":
		if fieldType != domain.FieldString {
			return "", nil, fmt.Errorf("contains is only valid for strings")
		}
		return expression + " LIKE ? ESCAPE '\\'", []any{"%" + escapeLike(fmt.Sprint(raw)) + "%"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported operator %q", operator)
	}
}

func queryValue(value any, fieldType domain.FieldType) (any, error) {
	switch fieldType {
	case domain.FieldString:
		return fmt.Sprint(value), nil
	case domain.FieldInteger:
		number, err := numericValue(value)
		if err != nil || math.Trunc(number) != number {
			return nil, fmt.Errorf("expected an integer")
		}
		return int64(number), nil
	case domain.FieldFloat:
		return numericValue(value)
	case domain.FieldBoolean:
		switch typed := value.(type) {
		case bool:
			return boolInt(typed), nil
		case string:
			parsed, err := strconv.ParseBool(typed)
			if err != nil {
				return nil, fmt.Errorf("expected a boolean")
			}
			return boolInt(parsed), nil
		default:
			return nil, fmt.Errorf("expected a boolean")
		}
	case domain.FieldDateTime:
		parsed, err := time.Parse(time.RFC3339Nano, fmt.Sprint(value))
		if err != nil {
			return nil, fmt.Errorf("expected an RFC3339 timestamp")
		}
		return parsed.UnixNano(), nil
	case domain.FieldDuration:
		if text, ok := value.(string); ok {
			parsed, err := time.ParseDuration(text)
			if err != nil {
				return nil, fmt.Errorf("expected a duration such as 250ms or 3s")
			}
			return int64(parsed), nil
		}
		number, err := numericValue(value)
		return int64(number), err
	default:
		return nil, fmt.Errorf("unsupported field type %q", fieldType)
	}
}

func numericValue(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	default:
		number, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		if err != nil {
			return 0, fmt.Errorf("expected a number")
		}
		return number, nil
	}
}

func typedValueColumn(alias string, fieldType domain.FieldType) string {
	switch fieldType {
	case domain.FieldInteger, domain.FieldDuration:
		return alias + ".value_integer"
	case domain.FieldFloat:
		return alias + ".value_real"
	case domain.FieldBoolean:
		return alias + ".value_boolean"
	case domain.FieldDateTime:
		return alias + ".value_timestamp_ns"
	default:
		return alias + ".value_text"
	}
}

func scanStructuredRows(rows *sql.Rows) ([]domain.IndexedEntry, error) {
	result := make([]domain.IndexedEntry, 0)
	for rows.Next() {
		var entry domain.IndexedEntry
		var timestamp, parserVersion sql.NullInt64
		var fieldsJSON string
		if err := rows.Scan(&entry.ID, &entry.SourceID, &entry.Number, &entry.ByteOffset,
			&entry.ByteLength, &timestamp, &entry.Level, &entry.Service, &entry.RequestID,
			&entry.ParseStatus, &entry.EventType, &entry.MatchedRuleID, &fieldsJSON,
			&parserVersion, &entry.WarningCount); err != nil {
			return nil, err
		}
		if timestamp.Valid {
			value := time.Unix(0, timestamp.Int64).UTC()
			entry.Timestamp = &value
		}
		if parserVersion.Valid {
			value := int(parserVersion.Int64)
			entry.ParserVersion = &value
		}
		if err := decodeFields(fieldsJSON, &entry.CustomFields); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func normalizeSQLiteValue(value any) any {
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return value
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (s *Store) CreateDerivedField(ctx context.Context, field domain.DerivedFieldDefinition) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO derived_field_definitions
		    (id, parser_profile_id, name, result_type, expression, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		field.ID, field.ParserProfileID, field.Name, field.ResultType, field.Expression,
		field.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListDerivedFields(ctx context.Context, profileID string) ([]domain.DerivedFieldDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, parser_profile_id, name, result_type, expression, created_at
		FROM derived_field_definitions WHERE parser_profile_id = ?
		ORDER BY created_at, name`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.DerivedFieldDefinition, 0)
	for rows.Next() {
		var field domain.DerivedFieldDefinition
		var createdAt string
		if err := rows.Scan(&field.ID, &field.ParserProfileID, &field.Name,
			&field.ResultType, &field.Expression, &createdAt); err != nil {
			return nil, err
		}
		field.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		result = append(result, field)
	}
	return result, rows.Err()
}

func (s *Store) DeleteDerivedField(ctx context.Context, profileID, fieldID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM derived_field_definitions
		WHERE id = ? AND parser_profile_id = ?`, fieldID, profileID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *Store) CreateSavedAnalysis(ctx context.Context, analysis domain.SavedAnalysis) error {
	configuration, err := json.Marshal(analysis.Configuration)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO saved_analyses
		    (id, name, source_id, source_group_id, configuration_json, chart_type,
		     created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?)`,
		analysis.ID, analysis.Name, analysis.SourceID, analysis.SourceGroupID,
		string(configuration), analysis.ChartType,
		analysis.CreatedAt.Format(time.RFC3339Nano), analysis.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListSavedAnalyses(ctx context.Context, sourceID string) ([]domain.SavedAnalysis, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(source_id, ''), COALESCE(source_group_id, ''),
		       configuration_json, chart_type, created_at, updated_at
		FROM saved_analyses WHERE source_id = ? ORDER BY updated_at DESC`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.SavedAnalysis, 0)
	for rows.Next() {
		var analysis domain.SavedAnalysis
		var createdAt, updatedAt string
		if err := rows.Scan(&analysis.ID, &analysis.Name, &analysis.SourceID,
			&analysis.SourceGroupID, &analysis.ConfigurationJSON, &analysis.ChartType,
			&createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(analysis.ConfigurationJSON), &analysis.Configuration); err != nil {
			return nil, err
		}
		analysis.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		analysis.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, analysis)
	}
	return result, rows.Err()
}

func (s *Store) DeleteSavedAnalysis(ctx context.Context, sourceID, analysisID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM saved_analyses WHERE id = ? AND source_id = ?`, analysisID, sourceID)
	if err != nil {
		return err
	}
	return requireChanged(result)
}
