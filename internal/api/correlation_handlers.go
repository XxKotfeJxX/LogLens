package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"loglens/internal/domain"
	"loglens/internal/store"
)

var baseCorrelationFields = map[string]domain.FieldType{
	"entry_number": domain.FieldInteger,
	"timestamp":    domain.FieldDateTime,
	"level":        domain.FieldString,
	"service":      domain.FieldString,
	"request_id":   domain.FieldString,
	"event_type":   domain.FieldString,
	"parse_status": domain.FieldString,
}

func (api *API) createSourceGroup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name, request.Description = strings.TrimSpace(request.Name), strings.TrimSpace(request.Description)
	if request.Name == "" || len(request.Name) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_name", "Group name must contain 1 to 120 characters")
		return
	}
	now := time.Now().UTC()
	group := domain.SourceGroup{
		ID: uuid.NewString(), Name: request.Name, Description: request.Description,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := api.store.CreateSourceGroup(r.Context(), group); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, group)
}

func (api *API) listSourceGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := api.store.ListSourceGroups(r.Context())
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": groups})
}

func (api *API) getSourceGroup(w http.ResponseWriter, r *http.Request) {
	group, err := api.store.GetSourceGroup(r.Context(), chi.URLParam(r, "groupID"))
	if !api.handleStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (api *API) updateSourceGroup(w http.ResponseWriter, r *http.Request) {
	group, err := api.store.GetSourceGroup(r.Context(), chi.URLParam(r, "groupID"))
	if !api.handleStoreError(w, err) {
		return
	}
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name, request.Description = strings.TrimSpace(request.Name), strings.TrimSpace(request.Description)
	if request.Name == "" || len(request.Name) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_name", "Group name must contain 1 to 120 characters")
		return
	}
	group.Name, group.Description, group.UpdatedAt = request.Name, request.Description, time.Now().UTC()
	if err := api.store.UpdateSourceGroup(r.Context(), group); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (api *API) deleteSourceGroup(w http.ResponseWriter, r *http.Request) {
	if err := api.store.DeleteSourceGroup(r.Context(), chi.URLParam(r, "groupID")); !api.handleStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) upsertSourceGroupMember(w http.ResponseWriter, r *http.Request) {
	var request struct {
		TimeZone            string `json:"timeZone"`
		ManualClockOffsetMS int64  `json:"manualClockOffsetMs"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.TimeZone == "" {
		request.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(request.TimeZone); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_zone", "timeZone must be an IANA zone such as UTC or Europe/Kyiv")
		return
	}
	if request.ManualClockOffsetMS < -7*24*60*60*1000 || request.ManualClockOffsetMS > 7*24*60*60*1000 {
		writeError(w, http.StatusBadRequest, "invalid_clock_offset", "manualClockOffsetMs must be within seven days")
		return
	}
	member := domain.SourceGroupMember{
		SourceGroupID: chi.URLParam(r, "groupID"), SourceID: chi.URLParam(r, "sourceID"),
		TimeZone: request.TimeZone, ManualClockOffsetMS: request.ManualClockOffsetMS,
		AddedAt: time.Now().UTC(),
	}
	if err := api.store.UpsertSourceGroupMember(r.Context(), member); !api.handleStoreError(w, err) {
		return
	}
	group, err := api.store.GetSourceGroup(r.Context(), member.SourceGroupID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	for _, current := range group.Members {
		if current.SourceID == member.SourceID {
			writeJSON(w, http.StatusOK, current)
			return
		}
	}
	writeInternal(w, fmt.Errorf("member was saved but could not be reloaded"))
}

func (api *API) deleteSourceGroupMember(w http.ResponseWriter, r *http.Request) {
	err := api.store.DeleteSourceGroupMember(r.Context(), chi.URLParam(r, "groupID"), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) upsertCanonicalMapping(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SourceID      string `json:"sourceId"`
		CanonicalName string `json:"canonicalName"`
		SourceField   string `json:"sourceField"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.CanonicalName, request.SourceField = strings.TrimSpace(request.CanonicalName), strings.TrimSpace(request.SourceField)
	if !analysisFieldName.MatchString(request.CanonicalName) || !analysisFieldName.MatchString(request.SourceField) {
		writeError(w, http.StatusBadRequest, "invalid_mapping", "Canonical and source fields must be safe identifiers")
		return
	}
	fieldType, ok, err := api.sourceFieldType(r, request.SourceID, request.SourceField)
	if err != nil {
		if !api.handleStoreError(w, err) {
			return
		}
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_source_field", "The source field does not exist in the assigned schema")
		return
	}
	mapping := domain.CanonicalFieldMapping{
		ID: uuid.NewString(), SourceGroupID: chi.URLParam(r, "groupID"),
		SourceID: request.SourceID, CanonicalName: request.CanonicalName,
		SourceField: request.SourceField, FieldType: fieldType, CreatedAt: time.Now().UTC(),
	}
	groupBeforeSave, err := api.store.GetSourceGroup(r.Context(), mapping.SourceGroupID)
	if err != nil {
		if !api.handleStoreError(w, err) {
			return
		}
	}
	for _, existing := range groupBeforeSave.Mappings {
		if existing.CanonicalName == mapping.CanonicalName && existing.FieldType != mapping.FieldType {
			writeError(w, http.StatusBadRequest, "canonical_type_mismatch",
				fmt.Sprintf("Canonical field %q is already mapped as %s", mapping.CanonicalName, existing.FieldType))
			return
		}
	}
	if err := api.store.UpsertCanonicalMapping(r.Context(), mapping); !api.handleStoreError(w, err) {
		return
	}
	group, err := api.store.GetSourceGroup(r.Context(), mapping.SourceGroupID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	for _, current := range group.Mappings {
		if current.SourceID == mapping.SourceID && current.CanonicalName == mapping.CanonicalName {
			writeJSON(w, http.StatusOK, current)
			return
		}
	}
	writeInternal(w, fmt.Errorf("mapping was saved but could not be reloaded"))
}

func (api *API) deleteCanonicalMapping(w http.ResponseWriter, r *http.Request) {
	err := api.store.DeleteCanonicalMapping(r.Context(), chi.URLParam(r, "groupID"), chi.URLParam(r, "mappingID"))
	if !api.handleStoreError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) querySourceGroup(w http.ResponseWriter, r *http.Request) {
	group, err := api.store.GetSourceGroup(r.Context(), chi.URLParam(r, "groupID"))
	if !api.handleStoreError(w, err) {
		return
	}
	var request domain.SourceGroupQuery
	if err := decodeJSON(w, r, &request, 256<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, err := api.executeGroupQuery(r, group, request)
	if err != nil {
		api.writeAnalysisError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (api *API) correlateSourceGroup(w http.ResponseWriter, r *http.Request) {
	group, err := api.store.GetSourceGroup(r.Context(), chi.URLParam(r, "groupID"))
	if !api.handleStoreError(w, err) {
		return
	}
	var request domain.CorrelationRequest
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !analysisFieldName.MatchString(request.Field) || request.Value == nil {
		writeError(w, http.StatusBadRequest, "invalid_correlation", "field and a non-null value are required")
		return
	}
	items, err := api.executeGroupQuery(r, group, domain.SourceGroupQuery{
		Filters: []domain.TypedFilter{{Field: request.Field, Operator: "eq", Value: request.Value}},
		Limit:   request.Limit, Sort: "asc",
	})
	if err != nil {
		api.writeAnalysisError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"canonicalField": request.Field, "value": request.Value, "items": items, "count": len(items),
	})
}

func (api *API) executeGroupQuery(
	r *http.Request,
	group domain.SourceGroup,
	request domain.SourceGroupQuery,
) ([]domain.CorrelatedEntry, error) {
	limit := request.Limit
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	direction := strings.ToLower(request.Sort)
	if direction == "" {
		direction = "desc"
	}
	if direction != "asc" && direction != "desc" {
		return nil, fmt.Errorf("%w: sort must be asc or desc", store.ErrInvalidQuery)
	}
	selected := make(map[string]bool, len(request.SourceIDs))
	for _, sourceID := range request.SourceIDs {
		selected[sourceID] = true
	}
	result := make([]domain.CorrelatedEntry, 0, limit*max(1, len(group.Members)))
	for _, member := range group.Members {
		if len(selected) > 0 && !selected[member.SourceID] {
			continue
		}
		source, err := api.store.GetSource(r.Context(), member.SourceID)
		if err != nil {
			return nil, err
		}
		available, err := api.sourceFieldTypes(r, member.SourceID)
		if err != nil {
			return nil, err
		}
		mappings := mappingsForSource(group.Mappings, member.SourceID)
		filters := make([]domain.TypedFilter, 0, len(request.Filters))
		sourceCanMatch := true
		for _, filter := range request.Filters {
			sourceField, ok := resolveCanonicalField(filter.Field, mappings, available)
			if !ok {
				sourceCanMatch = false
				break
			}
			filter.Field = sourceField
			filters = append(filters, filter)
		}
		if !sourceCanMatch {
			continue
		}
		sourceLimit := limit
		if request.Query != "" {
			sourceLimit = 5000
		}
		metadata, err := api.store.QueryStructuredEntries(r.Context(), source.ID, domain.StructuredQuery{
			Filters: filters, Sort: []domain.QuerySort{{Field: "timestamp", Direction: direction}},
			Limit: sourceLimit,
		})
		if err != nil {
			return nil, err
		}
		entries, err := api.hydrateEntries(source, metadata)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if request.Query != "" && !strings.Contains(strings.ToLower(entry.Raw), strings.ToLower(request.Query)) {
				continue
			}
			canonical := canonicalEntryValues(entry, mappings)
			corrected := correctSourceTimestamp(entry.Timestamp, member)
			result = append(result, domain.CorrelatedEntry{
				SourceID: source.ID, SourceName: source.Name,
				OriginalTimestamp: entry.Timestamp, CorrectedTimestamp: corrected,
				CanonicalFields: canonical, Entry: entry,
			})
		}
		sortCorrelatedEntries(result, direction)
		if len(result) > limit {
			result = result[:limit]
		}
	}
	sortCorrelatedEntries(result, direction)
	return result, nil
}

func sortCorrelatedEntries(result []domain.CorrelatedEntry, direction string) {
	sort.SliceStable(result, func(left, right int) bool {
		a, b := result[left].CorrectedTimestamp, result[right].CorrectedTimestamp
		if a == nil && b == nil {
			if result[left].SourceID == result[right].SourceID {
				if direction == "asc" {
					return result[left].Entry.Number < result[right].Entry.Number
				}
				return result[left].Entry.Number > result[right].Entry.Number
			}
			if direction == "asc" {
				return result[left].SourceID < result[right].SourceID
			}
			return result[left].SourceID > result[right].SourceID
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		if a.Equal(*b) {
			if result[left].SourceID == result[right].SourceID {
				if direction == "asc" {
					return result[left].Entry.Number < result[right].Entry.Number
				}
				return result[left].Entry.Number > result[right].Entry.Number
			}
			if direction == "asc" {
				return result[left].SourceID < result[right].SourceID
			}
			return result[left].SourceID > result[right].SourceID
		}
		return direction == "asc" && a.Before(*b) || direction == "desc" && a.After(*b)
	})
}

func (api *API) sourceGroupTimeline(w http.ResponseWriter, r *http.Request) {
	group, err := api.store.GetSourceGroup(r.Context(), chi.URLParam(r, "groupID"))
	if !api.handleStoreError(w, err) {
		return
	}
	bucketSeconds := boundedInt(r.URL.Query().Get("bucketSeconds"), 60, 60, 86400)
	bucketSize := time.Duration(bucketSeconds) * time.Second
	points := map[int64]*domain.GroupTimelinePoint{}
	for _, member := range group.Members {
		var cursor int64
		for {
			filters := []domain.TypedFilter{}
			if cursor > 0 {
				filters = append(filters, domain.TypedFilter{Field: "entry_number", Operator: "gt", Value: cursor})
			}
			page, err := api.store.QueryStructuredEntries(r.Context(), member.SourceID, domain.StructuredQuery{
				Filters: filters, Sort: []domain.QuerySort{{Field: "entry_number", Direction: "asc"}}, Limit: 5000,
			})
			if err != nil {
				api.writeAnalysisError(w, err)
				return
			}
			for _, entry := range page {
				cursor = entry.Number
				corrected := correctSourceTimestamp(entry.Timestamp, member)
				if corrected == nil {
					continue
				}
				bucket := corrected.Truncate(bucketSize)
				point := points[bucket.UnixNano()]
				if point == nil {
					point = &domain.GroupTimelinePoint{Timestamp: bucket, BySource: map[string]int64{}}
					points[bucket.UnixNano()] = point
				}
				point.Count++
				point.BySource[member.SourceID]++
				if entry.Level == "ERROR" || entry.Level == "FATAL" {
					point.Errors++
				}
			}
			if len(page) < 5000 {
				break
			}
		}
	}
	result := make([]domain.GroupTimelinePoint, 0, len(points))
	for _, point := range points {
		result = append(result, *point)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Timestamp.Before(result[j].Timestamp) })
	writeJSON(w, http.StatusOK, map[string]any{"items": result, "bucketSeconds": bucketSeconds})
}

func (api *API) sourceFieldTypes(r *http.Request, sourceID string) (map[string]domain.FieldType, error) {
	source, err := api.store.GetSource(r.Context(), sourceID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]domain.FieldType, len(baseCorrelationFields)+8)
	for name, fieldType := range baseCorrelationFields {
		result[name] = fieldType
	}
	if source.ParserProfileID != nil && source.ParserVersion != nil {
		version, err := api.store.GetParserVersion(r.Context(), *source.ParserProfileID, *source.ParserVersion)
		if err != nil {
			return nil, err
		}
		for _, field := range version.Fields {
			result[field.Name] = field.FieldType
		}
	}
	return result, nil
}

func (api *API) sourceFieldType(r *http.Request, sourceID, name string) (domain.FieldType, bool, error) {
	fields, err := api.sourceFieldTypes(r, sourceID)
	if err != nil {
		return "", false, err
	}
	fieldType, ok := fields[name]
	return fieldType, ok, nil
}

func mappingsForSource(mappings []domain.CanonicalFieldMapping, sourceID string) map[string]domain.CanonicalFieldMapping {
	result := map[string]domain.CanonicalFieldMapping{}
	for _, mapping := range mappings {
		if mapping.SourceID == sourceID {
			result[mapping.CanonicalName] = mapping
		}
	}
	return result
}

func resolveCanonicalField(
	canonical string,
	mappings map[string]domain.CanonicalFieldMapping,
	available map[string]domain.FieldType,
) (string, bool) {
	if mapping, ok := mappings[canonical]; ok {
		_, exists := available[mapping.SourceField]
		return mapping.SourceField, exists
	}
	_, exists := available[canonical]
	return canonical, exists
}

func canonicalEntryValues(entry domain.Entry, mappings map[string]domain.CanonicalFieldMapping) map[string]any {
	values := entryAnalysisValues(entry)
	result := make(map[string]any, len(values)+len(mappings))
	for name, value := range values {
		result[name] = value
	}
	for canonical, mapping := range mappings {
		result[canonical] = values[mapping.SourceField]
	}
	return result
}

func correctSourceTimestamp(timestamp *time.Time, member domain.SourceGroupMember) *time.Time {
	if timestamp == nil {
		return nil
	}
	corrected := timestamp.UTC()
	if member.TimeZone != "" && member.TimeZone != "UTC" {
		if location, err := time.LoadLocation(member.TimeZone); err == nil {
			corrected = time.Date(
				timestamp.Year(), timestamp.Month(), timestamp.Day(),
				timestamp.Hour(), timestamp.Minute(), timestamp.Second(), timestamp.Nanosecond(),
				location,
			).UTC()
		}
	}
	corrected = corrected.Add(time.Duration(member.ManualClockOffsetMS) * time.Millisecond)
	return &corrected
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
