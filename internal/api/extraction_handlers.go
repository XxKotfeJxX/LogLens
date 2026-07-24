package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"loglens/internal/domain"
	"loglens/internal/extraction"
	"loglens/internal/parser"
	"loglens/internal/store"
)

type fieldDefinitionRequest struct {
	Name         string           `json:"name"`
	DisplayName  string           `json:"displayName"`
	FieldType    domain.FieldType `json:"fieldType"`
	Indexed      bool             `json:"indexed"`
	Aggregatable bool             `json:"aggregatable"`
	Nullable     *bool            `json:"nullable"`
}

type extractionRuleRequest struct {
	Name          string         `json:"name"`
	EventType     string         `json:"eventType"`
	Priority      int            `json:"priority"`
	MatcherType   string         `json:"matcherType"`
	MatcherConfig map[string]any `json:"matcherConfig"`
	Enabled       *bool          `json:"enabled"`
}

func (api *API) createParserProfile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	if request.Name == "" || len(request.Name) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_profile_name", "Profile name must contain 1 to 120 characters")
		return
	}
	now := time.Now().UTC()
	profile := domain.ParserProfile{
		ID: uuid.NewString(), Name: request.Name, Description: request.Description,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := api.store.CreateParserProfile(r.Context(), profile); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

func (api *API) listParserProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := api.store.ListParserProfiles(r.Context())
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": profiles})
}

func (api *API) getParserProfile(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profileID")
	profile, err := api.store.GetParserProfile(r.Context(), profileID)
	if !api.handleStoreError(w, err) {
		return
	}
	versions, err := api.store.ListParserVersions(r.Context(), profileID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": profile, "versions": versions})
}

func (api *API) createParserVersion(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profileID")
	if _, err := api.store.GetParserProfile(r.Context(), profileID); !api.handleStoreError(w, err) {
		return
	}
	var request struct {
		RecordFormat  string                   `json:"recordFormat"`
		Configuration map[string]any           `json:"configuration"`
		Rules         []extractionRuleRequest  `json:"rules"`
		Fields        []fieldDefinitionRequest `json:"fields"`
	}
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.RecordFormat == "" {
		request.RecordFormat = "plain_text"
	}
	if request.RecordFormat != "plain_text" && request.RecordFormat != "jsonl" {
		writeError(w, http.StatusBadRequest, "invalid_record_format", "recordFormat must be plain_text or jsonl")
		return
	}
	version := domain.ParserProfileVersion{
		ProfileID: profileID, RecordFormat: request.RecordFormat,
		Configuration: request.Configuration,
	}
	for index, item := range request.Rules {
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		if item.Priority == 0 {
			item.Priority = index + 1
		}
		item.Name = strings.TrimSpace(item.Name)
		item.EventType = strings.TrimSpace(item.EventType)
		if item.Name == "" || item.EventType == "" {
			writeError(w, http.StatusBadRequest, "invalid_rule", "Every rule needs a name and eventType")
			return
		}
		version.Rules = append(version.Rules, domain.ExtractionRule{
			ID: uuid.NewString(), Name: item.Name, EventType: item.EventType,
			Priority: item.Priority, MatcherType: item.MatcherType,
			MatcherConfig: item.MatcherConfig, Enabled: enabled,
		})
	}
	for _, item := range request.Fields {
		nullable := true
		if item.Nullable != nil {
			nullable = *item.Nullable
		}
		version.Fields = append(version.Fields, domain.FieldDefinition{
			ID: uuid.NewString(), Name: strings.TrimSpace(item.Name),
			DisplayName: strings.TrimSpace(item.DisplayName), FieldType: item.FieldType,
			Indexed: item.Indexed, Aggregatable: item.Aggregatable, Nullable: nullable,
		})
	}
	if _, err := extraction.Compile(version); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_parser_version", err.Error())
		return
	}
	if err := api.store.CreateParserVersion(r.Context(), &version); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (api *API) listParserVersions(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profileID")
	if _, err := api.store.GetParserProfile(r.Context(), profileID); !api.handleStoreError(w, err) {
		return
	}
	versions, err := api.store.ListParserVersions(r.Context(), profileID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": versions})
}

func (api *API) previewParserProfile(w http.ResponseWriter, r *http.Request) {
	profileID := chi.URLParam(r, "profileID")
	var request struct {
		SourceID string `json:"sourceId"`
		Version  int    `json:"version"`
		Limit    int    `json:"limit"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	source, err := api.store.GetSource(r.Context(), strings.TrimSpace(request.SourceID))
	if !api.handleStoreError(w, err) {
		return
	}
	var version domain.ParserProfileVersion
	if request.Version > 0 {
		version, err = api.store.GetParserVersion(r.Context(), profileID, request.Version)
	} else {
		var versions []domain.ParserProfileVersion
		versions, err = api.store.ListParserVersions(r.Context(), profileID)
		if err == nil && len(versions) == 0 {
			err = store.ErrNotFound
		}
		if err == nil {
			version = versions[0]
		}
	}
	if !api.handleStoreError(w, err) {
		return
	}
	engine, err := extraction.Compile(version)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_parser_version", err.Error())
		return
	}
	limit := request.Limit
	if limit < 1 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}
	metadata, _, err := api.store.QueryEntryMetadata(r.Context(), source.ID, domain.EntryFilter{
		Cursor: 1, Limit: min(limit, 500),
	})
	if err != nil {
		writeInternal(w, err)
		return
	}
	entries, err := api.hydrateEntries(source, metadata)
	if err != nil {
		writeInternal(w, err)
		return
	}
	type previewItem struct {
		Number        int64                 `json:"number"`
		Raw           string                `json:"raw"`
		EventType     string                `json:"eventType,omitempty"`
		MatchedRuleID *string               `json:"matchedRuleId,omitempty"`
		Fields        map[string]any        `json:"fields"`
		Warnings      []domain.ParseWarning `json:"warnings"`
	}
	items := make([]previewItem, 0, len(entries))
	matched, typeErrors := 0, 0
	for _, entry := range entries {
		raw := []byte(entry.Raw)
		result := engine.Extract(raw, parser.Parse(source.Format, raw))
		fields := make(map[string]any, len(result.Fields))
		for name, value := range result.Fields {
			fields[name] = extractionJSONValue(value)
		}
		if result.EventType != "" {
			matched++
		}
		for _, warning := range result.Warnings {
			if strings.HasPrefix(warning.Code, "invalid_") {
				typeErrors++
			}
		}
		items = append(items, previewItem{
			Number: entry.Number, Raw: entry.Raw, EventType: result.EventType,
			MatchedRuleID: result.MatchedRuleID, Fields: fields, Warnings: result.Warnings,
		})
	}
	coverage := 0.0
	if len(items) > 0 {
		coverage = float64(matched) / float64(len(items))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version, "sampleSize": len(items), "matched": matched,
		"unmatched": len(items) - matched, "coverage": coverage,
		"typeErrors": typeErrors, "items": items,
	})
}

func (api *API) assignSourceParser(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "sourceID")
	previous, err := api.store.GetSource(r.Context(), sourceID)
	if !api.handleStoreError(w, err) {
		return
	}
	var request struct {
		ProfileID string `json:"profileId"`
		Version   int    `json:"version"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.ProfileID == "" || request.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_parser_assignment", "profileId and a positive version are required")
		return
	}
	if err := api.store.AssignSourceParser(r.Context(), sourceID, request.ProfileID, request.Version); !api.handleStoreError(w, err) {
		return
	}
	source, err := api.store.GetSource(r.Context(), sourceID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if previous.WatchStatus == "running" {
		if err := api.live.Refresh(r.Context(), source); err != nil {
			writeError(w, http.StatusConflict, "live_parser_switch_failed", err.Error())
			return
		}
		source, _ = api.store.GetSource(r.Context(), sourceID)
	}
	writeJSON(w, http.StatusOK, source)
}

func (api *API) startReindex(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	job, err := api.reindexes.Start(r.Context(), source)
	if err != nil {
		writeError(w, http.StatusConflict, "reindex_not_started", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (api *API) getSourceReindex(w http.ResponseWriter, r *http.Request) {
	job, err := api.store.GetLatestReindexJob(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (api *API) cancelSourceReindex(w http.ResponseWriter, r *http.Request) {
	job, err := api.store.GetLatestReindexJob(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	if job.Status != "queued" && job.Status != "running" {
		writeError(w, http.StatusConflict, "reindex_not_running", "This reindex is not running")
		return
	}
	if !api.reindexes.Cancel(job.ID) {
		writeError(w, http.StatusConflict, "reindex_not_cancellable", "This reindex cannot be cancelled on this server")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (api *API) reindexEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "streaming_unavailable", "Streaming is not supported")
		return
	}
	jobID := chi.URLParam(r, "jobID")
	events, unsubscribe := api.reindexes.Subscribe(jobID)
	defer unsubscribe()
	job, err := api.store.GetReindexJob(r.Context(), jobID)
	if !api.handleStoreError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "reindex."+job.Status, job)
	flusher.Flush()
	if terminalReindex(job.Status) {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case event := <-events:
			writeSSE(w, event.Type, event.Job)
			flusher.Flush()
			if terminalReindex(event.Job.Status) {
				return
			}
		}
	}
}

func (api *API) sourceFields(w http.ResponseWriter, r *http.Request) {
	fields, err := api.store.SourceFields(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": fields})
}

func (api *API) sourceEventTypes(w http.ResponseWriter, r *http.Request) {
	stats, err := api.store.ExtractionStatistics(r.Context(), chi.URLParam(r, "sourceID"))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": stats.ByEventType})
}

func (api *API) extractionStatistics(w http.ResponseWriter, r *http.Request) {
	stats, err := api.store.ExtractionStatistics(r.Context(), chi.URLParam(r, "sourceID"))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	return nil
}

func extractionJSONValue(value domain.FieldValue) any {
	switch value.Type {
	case domain.FieldInteger:
		if value.IntegerValue != nil {
			return *value.IntegerValue
		}
	case domain.FieldFloat:
		if value.FloatValue != nil {
			return *value.FloatValue
		}
	case domain.FieldBoolean:
		if value.BooleanValue != nil {
			return *value.BooleanValue
		}
	case domain.FieldDateTime:
		if value.TimestampValue != nil {
			return value.TimestampValue.Format(time.RFC3339Nano)
		}
	case domain.FieldDuration:
		if value.DurationValue != nil {
			return value.DurationValue.String()
		}
	default:
		if value.StringValue != nil {
			return *value.StringValue
		}
	}
	return nil
}

func terminalReindex(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func parsePositiveVersion(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("version must be positive")
	}
	return value, nil
}
