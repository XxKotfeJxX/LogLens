package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"loglens/internal/analysis"
	"loglens/internal/domain"
	"loglens/internal/store"
)

var analysisFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func (api *API) querySource(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	var request domain.StructuredQuery
	if err := decodeJSON(w, r, &request, 256<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Limit < 1 {
		request.Limit = 200
	}
	if request.Limit > 1000 {
		request.Limit = 1000
	}
	derived, err := api.sourceDerivedFields(r, source)
	if err != nil {
		writeInternal(w, err)
		return
	}
	sqlFilters, derivedFilters := splitAnalysisFilters(request.Filters, derived)
	storeRequest := request
	storeRequest.Filters = sqlFilters
	if len(derivedFilters) > 0 {
		storeRequest.Limit = 5000
	}
	metadata, err := api.store.QueryStructuredEntries(r.Context(), source.ID, storeRequest)
	if err != nil {
		api.writeAnalysisError(w, err)
		return
	}
	entries, err := api.hydrateEntries(source, metadata)
	if err != nil {
		writeInternal(w, err)
		return
	}
	result := make([]domain.Entry, 0, min(request.Limit, len(entries)))
	for index := range entries {
		applyDerivedFields(&entries[index], derived)
		if matchesFilters(entryAnalysisValues(entries[index]), derivedFilters) {
			result = append(result, entries[index])
			if len(result) == request.Limit {
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result, "count": len(result)})
}

func (api *API) aggregateSource(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	var request domain.AggregationRequest
	if err := decodeJSON(w, r, &request, 256<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	derived, err := api.sourceDerivedFields(r, source)
	if err != nil {
		writeInternal(w, err)
		return
	}
	derivedNames := derivedFieldNames(derived)
	_, derivedFilters := splitAnalysisFilters(request.Filters, derived)
	if !derivedNames[request.Field] && !derivedNames[request.GroupBy] && len(derivedFilters) == 0 {
		result, err := api.store.AggregateStructuredEntries(r.Context(), source.ID, request)
		if err != nil {
			api.writeAnalysisError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := api.aggregateDerived(r, source, request, derived)
	if err != nil {
		api.writeAnalysisError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (api *API) aggregateDerived(
	r *http.Request,
	source domain.Source,
	request domain.AggregationRequest,
	derived []domain.DerivedFieldDefinition,
) (domain.AggregationResult, error) {
	operation := strings.ToLower(request.Operation)
	if operation == "average" {
		operation = "avg"
	}
	if operation != "count" && operation != "sum" && operation != "avg" &&
		operation != "min" && operation != "max" {
		return domain.AggregationResult{}, fmt.Errorf("%w: unsupported aggregation %q", store.ErrInvalidQuery, request.Operation)
	}
	derivedNames := derivedFieldNames(derived)
	if operation != "count" && !derivedNames[request.Field] {
		return domain.AggregationResult{}, fmt.Errorf("%w: derived execution requires a derived measure", store.ErrInvalidQuery)
	}
	sqlFilters, _ := splitAnalysisFilters(request.Filters, derived)
	type accumulator struct {
		count int64
		sum   float64
		min   float64
		max   float64
	}
	accumulators := map[string]*accumulator{}
	groups := map[string]any{}
	var cursor int64
	for {
		filters := append([]domain.TypedFilter{}, sqlFilters...)
		if cursor > 0 {
			filters = append(filters, domain.TypedFilter{Field: "entry_number", Operator: "gt", Value: cursor})
		}
		page, err := api.store.QueryStructuredEntries(r.Context(), source.ID, domain.StructuredQuery{
			Filters: filters, Sort: []domain.QuerySort{{Field: "entry_number", Direction: "asc"}}, Limit: 5000,
		})
		if err != nil {
			return domain.AggregationResult{}, err
		}
		for _, item := range page {
			cursor = item.Number
			values := indexedAnalysisValues(item)
			applyDerivedValues(values, derived)
			if !matchesFilters(values, request.Filters) {
				continue
			}
			group := any(nil)
			if request.GroupBy != "" {
				group = values[request.GroupBy]
			}
			key := fmt.Sprintf("%T:%v", group, group)
			current := accumulators[key]
			if current == nil {
				current = &accumulator{min: math.Inf(1), max: math.Inf(-1)}
				accumulators[key], groups[key] = current, group
			}
			current.count++
			if operation == "count" {
				continue
			}
			number, ok := analysisNumber(values[request.Field])
			if !ok {
				current.count--
				continue
			}
			current.sum += number
			current.min = math.Min(current.min, number)
			current.max = math.Max(current.max, number)
		}
		if len(page) < 5000 {
			break
		}
	}
	keys := make([]string, 0, len(accumulators))
	for key := range accumulators {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := domain.AggregationResult{
		Operation: operation, Field: request.Field, GroupBy: request.GroupBy,
		Buckets: []domain.AggregationBucket{},
	}
	for _, key := range keys {
		current := accumulators[key]
		if current.count == 0 {
			continue
		}
		value := float64(current.count)
		switch operation {
		case "sum":
			value = current.sum
		case "avg":
			value = current.sum / float64(current.count)
		case "min":
			value = current.min
		case "max":
			value = current.max
		}
		result.Buckets = append(result.Buckets, domain.AggregationBucket{Group: groups[key], Value: value})
	}
	return result, nil
}

func (api *API) createDerivedField(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profileID")
	if _, err := api.store.GetParserProfile(r.Context(), profileID); !api.handleStoreError(w, err) {
		return
	}
	var request struct {
		Name       string           `json:"name"`
		ResultType domain.FieldType `json:"resultType"`
		Expression string           `json:"expression"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Expression = strings.TrimSpace(request.Expression)
	if !analysisFieldName.MatchString(request.Name) {
		writeError(w, http.StatusBadRequest, "invalid_field_name", "Name must be a safe identifier with at most 64 characters")
		return
	}
	if !validDerivedType(request.ResultType) {
		writeError(w, http.StatusBadRequest, "invalid_result_type", "Derived result type must be string, integer, float, datetime, or duration")
		return
	}
	if _, err := analysis.CompileExpression(request.Expression); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_expression", err.Error())
		return
	}
	field := domain.DerivedFieldDefinition{
		ID: uuid.NewString(), ParserProfileID: profileID, Name: request.Name,
		ResultType: request.ResultType, Expression: request.Expression, CreatedAt: time.Now().UTC(),
	}
	if err := api.store.CreateDerivedField(r.Context(), field); err != nil {
		writeError(w, http.StatusConflict, "derived_field_not_created", "A derived field with this name already exists")
		return
	}
	writeJSON(w, http.StatusCreated, field)
}

func (api *API) listDerivedFields(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profileID")
	if _, err := api.store.GetParserProfile(r.Context(), profileID); !api.handleStoreError(w, err) {
		return
	}
	fields, err := api.store.ListDerivedFields(r.Context(), profileID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": fields})
}

func (api *API) deleteDerivedField(w http.ResponseWriter, r *http.Request) {
	err := api.store.DeleteDerivedField(r.Context(), chi.URLParam(r, "profileID"), chi.URLParam(r, "fieldID"))
	if !api.handleStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) createSavedAnalysis(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "sourceID")
	if _, err := api.store.GetSource(r.Context(), sourceID); !api.handleStoreError(w, err) {
		return
	}
	var request struct {
		Name          string         `json:"name"`
		Configuration map[string]any `json:"configuration"`
		ChartType     string         `json:"chartType"`
	}
	if err := decodeJSON(w, r, &request, 256<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name, request.ChartType = strings.TrimSpace(request.Name), strings.ToLower(strings.TrimSpace(request.ChartType))
	if request.Name == "" || len(request.Name) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_name", "Analysis name must contain 1 to 120 characters")
		return
	}
	if request.ChartType != "table" && request.ChartType != "bar" && request.ChartType != "line" {
		writeError(w, http.StatusBadRequest, "invalid_chart_type", "Chart type must be table, bar, or line")
		return
	}
	if request.Configuration == nil {
		request.Configuration = map[string]any{}
	}
	now := time.Now().UTC()
	saved := domain.SavedAnalysis{
		ID: uuid.NewString(), Name: request.Name, SourceID: sourceID,
		Configuration: request.Configuration, ChartType: request.ChartType,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := api.store.CreateSavedAnalysis(r.Context(), saved); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (api *API) listSavedAnalyses(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "sourceID")
	if _, err := api.store.GetSource(r.Context(), sourceID); !api.handleStoreError(w, err) {
		return
	}
	items, err := api.store.ListSavedAnalyses(r.Context(), sourceID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (api *API) deleteSavedAnalysis(w http.ResponseWriter, r *http.Request) {
	err := api.store.DeleteSavedAnalysis(r.Context(), chi.URLParam(r, "sourceID"), chi.URLParam(r, "analysisID"))
	if !api.handleStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) sourceDerivedFields(r *http.Request, source domain.Source) ([]domain.DerivedFieldDefinition, error) {
	if source.ParserProfileID == nil {
		return []domain.DerivedFieldDefinition{}, nil
	}
	return api.store.ListDerivedFields(r.Context(), *source.ParserProfileID)
}

func (api *API) writeAnalysisError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrInvalidQuery) {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	if !api.handleStoreError(w, err) {
		return
	}
}

func derivedFieldNames(fields []domain.DerivedFieldDefinition) map[string]bool {
	result := make(map[string]bool, len(fields))
	for _, field := range fields {
		result[field.Name] = true
	}
	return result
}

func splitAnalysisFilters(filters []domain.TypedFilter, derived []domain.DerivedFieldDefinition) ([]domain.TypedFilter, []domain.TypedFilter) {
	names := derivedFieldNames(derived)
	sqlFilters, derivedFilters := make([]domain.TypedFilter, 0, len(filters)), make([]domain.TypedFilter, 0)
	for _, filter := range filters {
		if names[filter.Field] {
			derivedFilters = append(derivedFilters, filter)
		} else {
			sqlFilters = append(sqlFilters, filter)
		}
	}
	return sqlFilters, derivedFilters
}

func applyDerivedFields(entry *domain.Entry, definitions []domain.DerivedFieldDefinition) {
	if entry.Fields == nil {
		entry.Fields = map[string]any{}
	}
	values := entryAnalysisValues(*entry)
	applyDerivedValues(values, definitions)
	for _, definition := range definitions {
		entry.Fields[definition.Name] = values[definition.Name]
	}
}

func applyDerivedValues(values map[string]any, definitions []domain.DerivedFieldDefinition) {
	for _, definition := range definitions {
		expression, err := analysis.CompileExpression(definition.Expression)
		if err != nil {
			values[definition.Name] = nil
			continue
		}
		value, err := expression.Evaluate(values)
		if err != nil {
			values[definition.Name] = nil
			continue
		}
		values[definition.Name] = coerceDerivedValue(value, definition.ResultType)
	}
}

func entryAnalysisValues(entry domain.Entry) map[string]any {
	values := make(map[string]any, len(entry.Fields)+7)
	for name, value := range entry.Fields {
		values[name] = value
	}
	values["entry_number"], values["timestamp"], values["level"] = entry.Number, entry.Timestamp, entry.Level
	values["service"], values["request_id"], values["event_type"] = entry.Service, entry.RequestID, entry.EventType
	values["parse_status"] = entry.ParseStatus
	return values
}

func indexedAnalysisValues(entry domain.IndexedEntry) map[string]any {
	values := make(map[string]any, len(entry.CustomFields)+7)
	for name, value := range entry.CustomFields {
		values[name] = value
	}
	values["entry_number"], values["timestamp"], values["level"] = entry.Number, entry.Timestamp, entry.Level
	values["service"], values["request_id"], values["event_type"] = entry.Service, entry.RequestID, entry.EventType
	values["parse_status"] = entry.ParseStatus
	return values
}

func matchesFilters(values map[string]any, filters []domain.TypedFilter) bool {
	for _, filter := range filters {
		if !matchesValue(values[filter.Field], strings.ToLower(filter.Operator), filter.Value) {
			return false
		}
	}
	return true
}

func matchesValue(actual any, operator string, expected any) bool {
	if operator == "" {
		operator = "eq"
	}
	if actual == nil {
		return operator == "eq" && expected == nil || operator == "ne" && expected != nil
	}
	if operator == "contains" {
		return strings.Contains(strings.ToLower(fmt.Sprint(actual)), strings.ToLower(fmt.Sprint(expected)))
	}
	if operator == "in" {
		items, ok := expected.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if matchesValue(actual, "eq", item) {
				return true
			}
		}
		return false
	}
	if operator == "between" {
		items, ok := expected.([]any)
		return ok && len(items) == 2 && matchesValue(actual, "gte", items[0]) && matchesValue(actual, "lte", items[1])
	}
	left, leftNumber := analysisNumber(actual)
	right, rightNumber := analysisNumber(expected)
	var comparison int
	if leftNumber && rightNumber {
		comparison = compareFloat(left, right)
	} else {
		comparison = strings.Compare(fmt.Sprint(actual), fmt.Sprint(expected))
	}
	switch operator {
	case "eq":
		return comparison == 0
	case "ne":
		return comparison != 0
	case "gt":
		return comparison > 0
	case "gte":
		return comparison >= 0
	case "lt":
		return comparison < 0
	case "lte":
		return comparison <= 0
	default:
		return false
	}
}

func analysisNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	default:
		number, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		return number, err == nil
	}
}

func compareFloat(left, right float64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func coerceDerivedValue(value any, fieldType domain.FieldType) any {
	switch fieldType {
	case domain.FieldInteger:
		if number, ok := analysisNumber(value); ok {
			return int64(number)
		}
	case domain.FieldFloat, domain.FieldDuration:
		if number, ok := analysisNumber(value); ok {
			return number
		}
	case domain.FieldDateTime:
		if parsed, err := time.Parse(time.RFC3339Nano, fmt.Sprint(value)); err == nil {
			return parsed.Format(time.RFC3339Nano)
		}
	case domain.FieldString:
		return fmt.Sprint(value)
	}
	return nil
}

func validDerivedType(value domain.FieldType) bool {
	return value == domain.FieldString || value == domain.FieldInteger || value == domain.FieldFloat ||
		value == domain.FieldDateTime || value == domain.FieldDuration
}
