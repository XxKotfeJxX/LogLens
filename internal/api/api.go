package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"loglens/internal/domain"
	"loglens/internal/importjob"
	"loglens/internal/live"
	"loglens/internal/parser"
	"loglens/internal/reindex"
	"loglens/internal/store"
)

type Config struct {
	WebOrigin         string
	SourceStoragePath string
	TempStoragePath   string
	ImportRoot        string
	AllowServerImport bool
	MaxUploadSize     int64
	MaxEntrySize      int
}

type API struct {
	store     *store.Store
	imports   *importjob.Manager
	reindexes *reindex.Manager
	live      *live.Manager
	config    Config
}

type Dependencies struct {
	Reindexes *reindex.Manager
	Live      *live.Manager
}

func New(storage *store.Store, imports *importjob.Manager, config Config, dependencies ...Dependencies) http.Handler {
	reindexes := reindex.NewManager(storage, 500, config.MaxEntrySize)
	liveTail := live.NewManager(storage, config.MaxEntrySize, 250*time.Millisecond)
	if len(dependencies) > 0 {
		if dependencies[0].Reindexes != nil {
			reindexes = dependencies[0].Reindexes
		}
		if dependencies[0].Live != nil {
			liveTail = dependencies[0].Live
		}
	}
	api := &API{store: storage, imports: imports, reindexes: reindexes, live: liveTail, config: config}
	router := chi.NewRouter()
	router.Use(api.recoverer, api.requestLogger, api.cors)
	router.Get("/healthz", api.health)
	router.Get("/readyz", api.ready)
	router.Route("/api/v1", func(router chi.Router) {
		router.Route("/sources", func(router chi.Router) {
			router.Post("/upload", api.uploadSource)
			router.Post("/import", api.importServerSource)
			router.Get("/", api.listSources)
			router.Get("/{sourceID}", api.getSource)
			router.Delete("/{sourceID}", api.deleteSource)
			router.Post("/{sourceID}/detect", api.detectSource)
			router.Post("/{sourceID}/imports", api.startImport)
			router.Get("/{sourceID}/entries", api.listEntries)
			router.Get("/{sourceID}/entries/{entryNumber}", api.getEntry)
			router.Get("/{sourceID}/entries/{entryNumber}/context", api.getEntryContext)
			router.Get("/{sourceID}/statistics", api.statistics)
			router.Get("/{sourceID}/timeline", api.timeline)
			router.Put("/{sourceID}/parser", api.assignSourceParser)
			router.Post("/{sourceID}/reindex", api.startReindex)
			router.Get("/{sourceID}/reindex", api.getSourceReindex)
			router.Post("/{sourceID}/reindex/cancel", api.cancelSourceReindex)
			router.Get("/{sourceID}/fields", api.sourceFields)
			router.Get("/{sourceID}/event-types", api.sourceEventTypes)
			router.Get("/{sourceID}/extraction-statistics", api.extractionStatistics)
			router.Post("/{sourceID}/query", api.querySource)
			router.Post("/{sourceID}/aggregations", api.aggregateSource)
			router.Post("/{sourceID}/analyses", api.createSavedAnalysis)
			router.Get("/{sourceID}/analyses", api.listSavedAnalyses)
			router.Delete("/{sourceID}/analyses/{analysisID}", api.deleteSavedAnalysis)
			router.Post("/{sourceID}/watch", api.startWatch)
			router.Post("/{sourceID}/watch/stop", api.stopWatch)
			router.Get("/{sourceID}/live/events", api.liveEvents)
		})
		router.Route("/imports", func(router chi.Router) {
			router.Get("/{jobID}", api.getImport)
			router.Post("/{jobID}/cancel", api.cancelImport)
			router.Get("/{jobID}/events", api.importEvents)
		})
		router.Route("/parser-profiles", func(router chi.Router) {
			router.Post("/", api.createParserProfile)
			router.Get("/", api.listParserProfiles)
			router.Get("/{profileID}", api.getParserProfile)
			router.Post("/{profileID}/versions", api.createParserVersion)
			router.Get("/{profileID}/versions", api.listParserVersions)
			router.Post("/{profileID}/preview", api.previewParserProfile)
			router.Post("/{profileID}/derived-fields", api.createDerivedField)
			router.Get("/{profileID}/derived-fields", api.listDerivedFields)
			router.Delete("/{profileID}/derived-fields/{fieldID}", api.deleteDerivedField)
		})
		router.Route("/source-groups", func(router chi.Router) {
			router.Post("/", api.createSourceGroup)
			router.Get("/", api.listSourceGroups)
			router.Get("/{groupID}", api.getSourceGroup)
			router.Put("/{groupID}", api.updateSourceGroup)
			router.Delete("/{groupID}", api.deleteSourceGroup)
			router.Put("/{groupID}/members/{sourceID}", api.upsertSourceGroupMember)
			router.Delete("/{groupID}/members/{sourceID}", api.deleteSourceGroupMember)
			router.Post("/{groupID}/mappings", api.upsertCanonicalMapping)
			router.Delete("/{groupID}/mappings/{mappingID}", api.deleteCanonicalMapping)
			router.Post("/{groupID}/query", api.querySourceGroup)
			router.Get("/{groupID}/timeline", api.sourceGroupTimeline)
			router.Post("/{groupID}/correlations", api.correlateSourceGroup)
		})
		router.Get("/reindexes/{jobID}/events", api.reindexEvents)
	})
	return router
}

func (api *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) ready(w http.ResponseWriter, r *http.Request) {
	if err := api.store.Ready(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Database is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (api *API) uploadSource(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, api.config.MaxUploadSize+(1<<20))
	multipart, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_multipart", "Expected a multipart upload")
		return
	}
	if err := os.MkdirAll(api.config.SourceStoragePath, 0o755); err != nil {
		writeInternal(w, err)
		return
	}
	if err := os.MkdirAll(api.config.TempStoragePath, 0o755); err != nil {
		writeInternal(w, err)
		return
	}

	var source domain.Source
	found := false
	for {
		part, err := multipart.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_upload", "Could not read uploaded file")
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		if found {
			part.Close()
			writeError(w, http.StatusBadRequest, "multiple_files", "Upload one file at a time")
			return
		}
		found = true
		originalName := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		extension := strings.ToLower(filepath.Ext(originalName))
		if !allowedExtension(extension) {
			part.Close()
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_extension", "Supported extensions: .log, .txt, .jsonl, .ndjson")
			return
		}
		id := uuid.NewString()
		tempPath := filepath.Join(api.config.TempStoragePath, id+".upload")
		targetPath := filepath.Join(api.config.SourceStoragePath, id+extension)
		temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			part.Close()
			writeInternal(w, err)
			return
		}
		written, copyErr := io.Copy(temp, io.LimitReader(part, api.config.MaxUploadSize+1))
		closeErr := temp.Close()
		part.Close()
		if copyErr != nil || closeErr != nil || written > api.config.MaxUploadSize {
			_ = os.Remove(tempPath)
			if written > api.config.MaxUploadSize {
				writeError(w, http.StatusRequestEntityTooLarge, "upload_too_large", "The file exceeds the configured upload limit")
			} else {
				writeError(w, http.StatusBadRequest, "upload_failed", "The uploaded file could not be stored")
			}
			return
		}
		if written == 0 {
			_ = os.Remove(tempPath)
			writeError(w, http.StatusBadRequest, "empty_file", "The uploaded file is empty")
			return
		}
		if err := os.Rename(tempPath, targetPath); err != nil {
			_ = os.Remove(tempPath)
			writeInternal(w, err)
			return
		}
		detection, err := parser.DetectFile(targetPath, 100)
		if err != nil {
			_ = os.Remove(targetPath)
			writeError(w, http.StatusUnprocessableEntity, "detection_failed", "Could not detect the log format")
			return
		}
		source = domain.Source{
			ID: id, Name: originalName, OriginalName: originalName, StoredPath: targetPath,
			SourceType: "uploaded_file", Format: detection.Format, Status: "uploaded",
			SizeBytes: written, ExtractionStatus: "not_configured",
			WatchStatus: "stopped", CreatedAt: time.Now().UTC(),
		}
		if err := api.store.CreateSource(r.Context(), source); err != nil {
			_ = os.Remove(targetPath)
			writeInternal(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"source": source, "detection": detection})
		return
	}
	if !found {
		writeError(w, http.StatusBadRequest, "file_required", "Multipart field \"file\" is required")
	}
}

func (api *API) importServerSource(w http.ResponseWriter, r *http.Request) {
	if !api.config.AllowServerImport {
		writeError(w, http.StatusForbidden, "server_import_disabled", "Server-path imports are disabled")
		return
	}
	var request struct {
		RelativePath string `json:"relativePath"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "A relativePath JSON field is required")
		return
	}
	path, info, err := resolveServerImport(api.config.ImportRoot, request.RelativePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_server_path", err.Error())
		return
	}
	if info.Size() == 0 {
		writeError(w, http.StatusBadRequest, "empty_file", "The selected file is empty")
		return
	}
	if info.Size() > api.config.MaxUploadSize {
		writeError(w, http.StatusRequestEntityTooLarge, "source_too_large", "The file exceeds the configured source limit")
		return
	}
	detection, err := parser.DetectFile(path, 100)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "detection_failed", "Could not detect the log format")
		return
	}
	name := filepath.Base(path)
	source := domain.Source{
		ID: uuid.NewString(), Name: name, OriginalName: name, StoredPath: path,
		SourceType: "server_file", Format: detection.Format, Status: "uploaded",
		SizeBytes: info.Size(), ExtractionStatus: "not_configured",
		WatchStatus: "stopped", CreatedAt: time.Now().UTC(),
	}
	if err := api.store.CreateSource(r.Context(), source); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"source": source, "detection": detection})
}

func (api *API) listSources(w http.ResponseWriter, r *http.Request) {
	sources, err := api.store.ListSources(r.Context())
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sources})
}

func (api *API) getSource(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, source)
}

func (api *API) deleteSource(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	if source.Status == "importing" || source.Status == "queued" {
		writeError(w, http.StatusConflict, "source_busy", "Cancel the active import before deleting this source")
		return
	}
	if source.SourceType == "uploaded_file" {
		if err := os.Remove(source.StoredPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusInternalServerError, "file_delete_failed", "The source file could not be deleted; its record was kept")
			return
		}
	}
	if err := api.store.DeleteSourceRecord(r.Context(), source.ID); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func resolveServerImport(root, relativePath string) (string, os.FileInfo, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", nil, fmt.Errorf("relativePath must be a non-empty relative path")
	}
	clean := filepath.Clean(relativePath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("relativePath must stay inside IMPORT_ROOT")
	}
	if !allowedExtension(strings.ToLower(filepath.Ext(clean))) {
		return "", nil, fmt.Errorf("supported extensions: .log, .txt, .jsonl, .ndjson")
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("IMPORT_ROOT is invalid")
	}
	rootResolved, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return "", nil, fmt.Errorf("IMPORT_ROOT is unavailable")
	}
	candidate := filepath.Join(rootResolved, clean)
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", nil, fmt.Errorf("selected file is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("symbolic links are not allowed")
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("selected path is not a regular file")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, fmt.Errorf("selected file is unavailable")
	}
	inside, err := filepath.Rel(rootResolved, resolved)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("relativePath must stay inside IMPORT_ROOT")
	}
	return resolved, info, nil
}

func (api *API) detectSource(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	detection, err := parser.DetectFile(source.StoredPath, 100)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if err := api.store.UpdateSourceFormat(r.Context(), source.ID, detection.Format); err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detection)
}

func (api *API) startImport(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	job, err := api.imports.Start(r.Context(), source)
	if err != nil {
		writeError(w, http.StatusConflict, "import_not_started", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (api *API) getImport(w http.ResponseWriter, r *http.Request) {
	job, err := api.store.GetJob(r.Context(), chi.URLParam(r, "jobID"))
	if !api.handleStoreError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (api *API) cancelImport(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	job, err := api.store.GetJob(r.Context(), jobID)
	if !api.handleStoreError(w, err) {
		return
	}
	if job.Status != "running" && job.Status != "queued" {
		writeError(w, http.StatusConflict, "job_not_running", "This import is not running")
		return
	}
	if !api.imports.Cancel(jobID) {
		writeError(w, http.StatusConflict, "job_not_cancellable", "This import cannot be cancelled on this server")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

func (api *API) importEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "streaming_unavailable", "Streaming is not supported")
		return
	}
	jobID := chi.URLParam(r, "jobID")
	events, unsubscribe := api.imports.Subscribe(jobID)
	defer unsubscribe()
	job, err := api.store.GetJob(r.Context(), jobID)
	if !api.handleStoreError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, eventType(job.Status), job)
	flusher.Flush()
	if terminal(job.Status) {
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
			if terminal(event.Job.Status) {
				return
			}
		}
	}
}

func (api *API) listEntries(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	metadata, hasMore, err := api.store.QueryEntryMetadata(r.Context(), source.ID, filter)
	if err != nil {
		writeInternal(w, err)
		return
	}
	entries, err := api.hydrateEntries(source, metadata)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if filter.Query != "" {
		needle := strings.ToLower(filter.Query)
		filtered := entries[:0]
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.Raw), needle) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	var nextCursor *int64
	if len(metadata) > 0 {
		value := metadata[len(metadata)-1].Number + 1
		nextCursor = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": entries,
		"page":  map[string]any{"nextCursor": nextCursor, "hasMore": hasMore},
	})
}

func (api *API) getEntry(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	number, err := strconv.ParseInt(chi.URLParam(r, "entryNumber"), 10, 64)
	if err != nil || number < 1 {
		writeError(w, http.StatusBadRequest, "invalid_entry_number", "Entry number must be positive")
		return
	}
	metadata, err := api.store.GetEntryMetadata(r.Context(), source.ID, number)
	if !api.handleStoreError(w, err) {
		return
	}
	entries, err := api.hydrateEntries(source, []domain.IndexedEntry{metadata})
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries[0])
}

func (api *API) getEntryContext(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.ParseInt(chi.URLParam(r, "entryNumber"), 10, 64)
	if err != nil || number < 1 {
		writeError(w, http.StatusBadRequest, "invalid_entry_number", "Entry number must be positive")
		return
	}
	before := boundedInt(r.URL.Query().Get("before"), 20, 0, 100)
	after := boundedInt(r.URL.Query().Get("after"), 20, 0, 100)
	clone := r.Clone(r.Context())
	query := clone.URL.Query()
	start := number - int64(before)
	if start < 1 {
		start = 1
	}
	query.Set("cursor", strconv.FormatInt(start, 10))
	query.Set("limit", strconv.Itoa(before+after+1))
	clone.URL.RawQuery = query.Encode()
	api.listEntries(w, clone)
}

func (api *API) statistics(w http.ResponseWriter, r *http.Request) {
	if _, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID")); !api.handleStoreError(w, err) {
		return
	}
	stats, err := api.store.Statistics(r.Context(), chi.URLParam(r, "sourceID"))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (api *API) timeline(w http.ResponseWriter, r *http.Request) {
	if _, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID")); !api.handleStoreError(w, err) {
		return
	}
	points, err := api.store.Timeline(r.Context(), chi.URLParam(r, "sourceID"))
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": points})
}

func (api *API) hydrateEntries(source domain.Source, metadata []domain.IndexedEntry) ([]domain.Entry, error) {
	file, err := os.Open(source.StoredPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries := make([]domain.Entry, 0, len(metadata))
	for _, item := range metadata {
		readLength := item.ByteLength
		if readLength > int64(api.config.MaxEntrySize) {
			readLength = int64(api.config.MaxEntrySize)
		}
		raw := make([]byte, int(readLength))
		n, err := file.ReadAt(raw, item.ByteOffset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		raw = raw[:n]
		parsed := parser.Parse(source.Format, raw)
		if item.ParseStatus == "oversized" {
			parsed.Message += " … [truncated]"
		}
		fields := item.CustomFields
		if fields == nil {
			fields = parsed.Fields
		}
		entries = append(entries, domain.Entry{
			SourceID: source.ID, Number: item.Number, ByteOffset: item.ByteOffset, ByteLength: item.ByteLength,
			Timestamp: item.Timestamp, Level: item.Level, Service: item.Service,
			RequestID: item.RequestID, Message: sanitizeText(parsed.Message),
			ParseStatus: item.ParseStatus, Raw: sanitizeText(strings.TrimRight(string(raw), "\r\n")),
			Fields: fields, EventType: item.EventType, MatchedRuleID: item.MatchedRuleID,
			ParserVersion: item.ParserVersion, WarningCount: item.WarningCount,
		})
	}
	return entries, nil
}

func parseFilter(r *http.Request) (domain.EntryFilter, error) {
	query := r.URL.Query()
	filter := domain.EntryFilter{
		Cursor:      boundedInt64(query.Get("cursor"), 1, 1, 1<<62),
		Limit:       boundedInt(query.Get("limit"), 200, 1, 500),
		Levels:      splitCSV(strings.ToUpper(query.Get("levels"))),
		Services:    splitCSV(query.Get("services")),
		RequestID:   strings.TrimSpace(query.Get("requestId")),
		Query:       strings.TrimSpace(query.Get("query")),
		ParseStatus: splitCSV(query.Get("parseStatus")),
		EventTypes:  splitCSV(query.Get("eventType")),
	}
	var err error
	if value := query.Get("from"); value != "" {
		filter.From, err = parseQueryTime(value)
		if err != nil {
			return filter, fmt.Errorf("invalid from timestamp")
		}
	}
	if value := query.Get("to"); value != "" {
		filter.To, err = parseQueryTime(value)
		if err != nil {
			return filter, fmt.Errorf("invalid to timestamp")
		}
	}
	return filter, nil
}

func parseQueryTime(value string) (*time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (api *API) handleStoreError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "The requested resource was not found")
	} else {
		writeInternal(w, err)
	}
	return false
}

func (api *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.config.WebOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", api.config.WebOrigin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (api *API) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (api *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("http panic", "panic", recovered)
				writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func allowedExtension(extension string) bool {
	switch extension {
	case ".log", ".txt", ".jsonl", ".ndjson":
		return true
	default:
		return false
	}
}

func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func eventType(status string) string {
	if status == "running" || status == "queued" {
		return "import.progress"
	}
	return "import." + status
}

func writeSSE(w io.Writer, event string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeInternal(w http.ResponseWriter, err error) {
	slog.Error("api error", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func boundedInt(raw string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func boundedInt64(raw string, fallback, minimum, maximum int64) int64 {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

var ansiPattern = regexp.MustCompile(`\x1b(?:[@-_][0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)

func sanitizeText(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' || !unicode.IsControl(character) {
			return character
		}
		return -1
	}, value)
}
