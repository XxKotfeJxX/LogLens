package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loglens/internal/domain"
	"loglens/internal/importjob"
	"loglens/internal/store"
)

func TestUploadImportAndReadEntries(t *testing.T) {
	root := t.TempDir()
	storage, err := store.Open(context.Background(), filepath.Join(root, "loglens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	manager := importjob.NewManager(storage, 2, 1<<20)
	server := httptest.NewServer(New(storage, manager, Config{
		SourceStoragePath: filepath.Join(root, "sources"),
		TempStoragePath:   filepath.Join(root, "temp"),
		MaxUploadSize:     1 << 20,
		MaxEntrySize:      1 << 20,
	}))
	defer server.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(file, `{"timestamp":"2026-07-24T10:15:43Z","level":"INFO","service":"api","message":"ready"}`)
	fmt.Fprintln(file, `{"timestamp":"2026-07-24T10:15:44Z","level":"ERROR","service":"payment","message":"timeout","request_id":"req-1"}`)
	writer.Close()

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/sources/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d", response.StatusCode)
	}
	var uploaded struct {
		Source domain.Source `json:"source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if uploaded.Source.Format != "jsonl" {
		t.Fatalf("detected format = %q", uploaded.Source.Format)
	}

	response, err = http.Post(server.URL+"/api/v1/sources/"+uploaded.Source.ID+"/imports", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var job domain.ImportJob
	if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d", response.StatusCode)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err = http.Get(server.URL + "/api/v1/imports/" + job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if job.Status == "completed" {
			break
		}
		if job.Status == "failed" {
			t.Fatalf("import failed: %s", job.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for import")
		}
		time.Sleep(20 * time.Millisecond)
	}

	response, err = http.Get(server.URL + "/api/v1/sources/" + uploaded.Source.ID + "/entries?levels=ERROR")
	if err != nil {
		t.Fatal(err)
	}
	var entries struct {
		Items []domain.Entry `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(entries.Items) != 1 || entries.Items[0].RequestID != "req-1" || entries.Items[0].Message != "timeout" {
		t.Fatalf("unexpected entries: %+v", entries.Items)
	}
}

func TestServerImportIsRootBoundAndDoesNotDeleteOriginal(t *testing.T) {
	root := t.TempDir()
	importRoot := filepath.Join(root, "allowed")
	if err := os.MkdirAll(importRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(importRoot, "service.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{\"timestamp\":\"2026-07-24T10:15:43Z\",\"message\":\"ready\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(context.Background(), filepath.Join(root, "loglens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	manager := importjob.NewManager(storage, 2, 1<<20)
	server := httptest.NewServer(New(storage, manager, Config{
		SourceStoragePath: filepath.Join(root, "sources"),
		TempStoragePath:   filepath.Join(root, "temp"),
		ImportRoot:        importRoot,
		AllowServerImport: true,
		MaxUploadSize:     1 << 20,
		MaxEntrySize:      1 << 20,
	}))
	defer server.Close()

	response, err := http.Post(
		server.URL+"/api/v1/sources/import",
		"application/json",
		strings.NewReader(`{"relativePath":"../outside.jsonl"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal status = %d, want 400", response.StatusCode)
	}

	response, err = http.Post(
		server.URL+"/api/v1/sources/import",
		"application/json",
		strings.NewReader(`{"relativePath":"service.jsonl"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		Source domain.Source `json:"source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&imported); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || imported.Source.SourceType != "server_file" {
		t.Fatalf("unexpected server import: status=%d source=%+v", response.StatusCode, imported.Source)
	}

	request, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sources/"+imported.Source.ID, nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("server source must remain after metadata deletion: %v", err)
	}
}

func TestExtractionPreviewReindexAndLiveTail(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "combat.log")
	initial := "Andrew attacked Goblin with Sword for 42 damage\n" +
		"Andrew connected\n"
	if err := os.WriteFile(sourcePath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := store.Open(context.Background(), filepath.Join(root, "loglens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	source := domain.Source{
		ID: "combat-source", Name: "combat.log", OriginalName: "combat.log",
		StoredPath: sourcePath, SourceType: "server_file", Format: "plain_text",
		Status: "uploaded", SizeBytes: int64(len(initial)), CreatedAt: time.Now().UTC(),
	}
	if err := storage.CreateSource(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	imports := importjob.NewManager(storage, 2, 1<<20)
	importJob, err := imports.Start(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	waitForImport(t, storage, importJob.ID)

	server := httptest.NewServer(New(storage, imports, Config{
		SourceStoragePath: filepath.Join(root, "sources"),
		TempStoragePath:   filepath.Join(root, "temp"),
		MaxUploadSize:     1 << 20,
		MaxEntrySize:      1 << 20,
	}))
	defer server.Close()

	var profile domain.ParserProfile
	doJSON(t, http.MethodPost, server.URL+"/api/v1/parser-profiles/", map[string]any{
		"name": "Combat logs", "description": "Typed combat events",
	}, http.StatusCreated, &profile)

	var version domain.ParserProfileVersion
	doJSON(t, http.MethodPost, server.URL+"/api/v1/parser-profiles/"+profile.ID+"/versions", map[string]any{
		"recordFormat": "plain_text",
		"rules": []map[string]any{{
			"name": "attack", "eventType": "attack", "priority": 1,
			"matcherType": "regex", "enabled": true,
			"matcherConfig": map[string]any{
				"pattern": `(?<player>\w+) attacked (?<target>\w+) with (?<weapon>\w+) for (?<damage>\d+) damage`,
			},
		}},
		"fields": []map[string]any{
			{"name": "player", "fieldType": "string", "indexed": true, "nullable": false},
			{"name": "target", "fieldType": "string", "indexed": true, "nullable": false},
			{"name": "weapon", "fieldType": "string", "indexed": true, "aggregatable": true, "nullable": false},
			{"name": "damage", "fieldType": "integer", "indexed": true, "aggregatable": true, "nullable": false},
			{"name": "note", "fieldType": "string", "indexed": false, "aggregatable": false, "nullable": true},
		},
	}, http.StatusCreated, &version)
	if version.Version != 1 {
		t.Fatalf("version = %d, want 1", version.Version)
	}

	var preview struct {
		Matched   int     `json:"matched"`
		Unmatched int     `json:"unmatched"`
		Coverage  float64 `json:"coverage"`
	}
	doJSON(t, http.MethodPost, server.URL+"/api/v1/parser-profiles/"+profile.ID+"/preview", map[string]any{
		"sourceId": source.ID, "version": 1, "limit": 500,
	}, http.StatusOK, &preview)
	if preview.Matched != 1 || preview.Unmatched != 1 || preview.Coverage != 0.5 {
		t.Fatalf("unexpected preview: %+v", preview)
	}

	var before struct {
		Items []domain.Entry `json:"items"`
	}
	doJSON(t, http.MethodGet, server.URL+"/api/v1/sources/"+source.ID+"/entries?limit=10", nil, http.StatusOK, &before)
	firstOffset := before.Items[0].ByteOffset

	var assigned domain.Source
	doJSON(t, http.MethodPut, server.URL+"/api/v1/sources/"+source.ID+"/parser", map[string]any{
		"profileId": profile.ID, "version": 1,
	}, http.StatusOK, &assigned)
	if !assigned.ReindexRequired || assigned.ExtractionStatus != "configured" {
		t.Fatalf("unexpected assignment: %+v", assigned)
	}

	var reindexJob domain.ReindexJob
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/reindex", nil, http.StatusAccepted, &reindexJob)
	waitForReindex(t, server.URL, source.ID)

	var attacks struct {
		Items []domain.Entry `json:"items"`
	}
	doJSON(t, http.MethodGet, server.URL+"/api/v1/sources/"+source.ID+"/entries?eventType=attack", nil, http.StatusOK, &attacks)
	if len(attacks.Items) != 1 || attacks.Items[0].Fields["damage"].(float64) != 42 {
		t.Fatalf("unexpected extracted entries: %+v", attacks.Items)
	}
	if attacks.Items[0].ByteOffset != firstOffset {
		t.Fatalf("raw offset changed during reindex: got %d want %d", attacks.Items[0].ByteOffset, firstOffset)
	}

	var watched domain.Source
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/watch", nil, http.StatusAccepted, &watched)
	file, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("Morgan attacked Dragon with Axe for 99 damage\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		doJSON(t, http.MethodGet, server.URL+"/api/v1/sources/"+source.ID+"/entries?eventType=attack&limit=10", nil, http.StatusOK, &attacks)
		if len(attacks.Items) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live extraction did not append an attack: %+v", attacks.Items)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if attacks.Items[1].ParserVersion == nil || *attacks.Items[1].ParserVersion != 1 {
		t.Fatalf("live entry did not keep parser v1: %+v", attacks.Items[1])
	}
	var typedQuery struct {
		Items []domain.Entry `json:"items"`
		Count int            `json:"count"`
	}
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/query", map[string]any{
		"filters": []map[string]any{{"field": "damage", "operator": "gt", "value": 40}},
		"sort":    []map[string]any{{"field": "entry_number", "direction": "desc"}},
		"limit":   10,
	}, http.StatusOK, &typedQuery)
	if typedQuery.Count != 2 || typedQuery.Items[0].Number <= typedQuery.Items[1].Number {
		t.Fatalf("unexpected typed query result: %+v", typedQuery)
	}

	var grouped domain.AggregationResult
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/aggregations", map[string]any{
		"operation": "sum", "field": "damage", "groupBy": "weapon", "filters": []any{},
	}, http.StatusOK, &grouped)
	if len(grouped.Buckets) != 2 {
		t.Fatalf("unexpected grouped aggregation: %+v", grouped)
	}

	var derived domain.DerivedFieldDefinition
	doJSON(t, http.MethodPost, server.URL+"/api/v1/parser-profiles/"+profile.ID+"/derived-fields", map[string]any{
		"name": "half_damage", "resultType": "float", "expression": "damage / 2",
	}, http.StatusCreated, &derived)
	typedQuery.Items = nil
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/query", map[string]any{
		"filters": []map[string]any{{"field": "half_damage", "operator": "gt", "value": 40}},
		"limit":   10,
	}, http.StatusOK, &typedQuery)
	if typedQuery.Count != 1 || typedQuery.Items[0].Fields["half_damage"].(float64) != 49.5 {
		t.Fatalf("unexpected derived query: %+v", typedQuery)
	}
	var derivedTotal domain.AggregationResult
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/aggregations", map[string]any{
		"operation": "sum", "field": "half_damage", "filters": []any{},
	}, http.StatusOK, &derivedTotal)
	if len(derivedTotal.Buckets) != 1 || derivedTotal.Buckets[0].Value != 70.5 {
		t.Fatalf("unexpected derived aggregation: %+v", derivedTotal)
	}

	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/query", map[string]any{
		"filters": []map[string]any{{"field": "note", "operator": "eq", "value": "x"}},
	}, http.StatusBadRequest, nil)

	var saved domain.SavedAnalysis
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/analyses", map[string]any{
		"name": "Damage by weapon", "chartType": "bar",
		"configuration": map[string]any{"operation": "sum", "field": "damage", "groupBy": "weapon"},
	}, http.StatusCreated, &saved)
	var savedList struct {
		Items []domain.SavedAnalysis `json:"items"`
	}
	doJSON(t, http.MethodGet, server.URL+"/api/v1/sources/"+source.ID+"/analyses", nil, http.StatusOK, &savedList)
	if len(savedList.Items) != 1 || savedList.Items[0].Configuration["field"] != "damage" {
		t.Fatalf("unexpected saved analyses: %+v", savedList.Items)
	}
	doJSON(t, http.MethodDelete, server.URL+"/api/v1/sources/"+source.ID+"/analyses/"+saved.ID, nil, http.StatusNoContent, nil)
	doJSON(t, http.MethodDelete, server.URL+"/api/v1/parser-profiles/"+profile.ID+"/derived-fields/"+derived.ID, nil, http.StatusNoContent, nil)
	doJSON(t, http.MethodPost, server.URL+"/api/v1/sources/"+source.ID+"/watch/stop", nil, http.StatusAccepted, nil)
}

func TestSourceGroupCanonicalCorrelationAndClockCorrection(t *testing.T) {
	root := t.TempDir()
	storage, err := store.Open(context.Background(), filepath.Join(root, "loglens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	imports := importjob.NewManager(storage, 2, 1<<20)
	sources := []domain.Source{
		{
			ID: "api-source", Name: "api.jsonl", OriginalName: "api.jsonl",
			StoredPath: filepath.Join(root, "api.jsonl"), SourceType: "server_file",
			Format: "jsonl", Status: "uploaded", CreatedAt: time.Now().UTC(),
		},
		{
			ID: "worker-source", Name: "worker.jsonl", OriginalName: "worker.jsonl",
			StoredPath: filepath.Join(root, "worker.jsonl"), SourceType: "server_file",
			Format: "jsonl", Status: "uploaded", CreatedAt: time.Now().UTC(),
		},
	}
	payloads := []string{
		`{"timestamp":"2026-07-24T10:00:00Z","level":"INFO","service":"api","request_id":"trace-7","message":"request accepted"}` + "\n",
		`{"timestamp":"2026-07-24T12:00:00Z","level":"ERROR","service":"worker","correlationId":"trace-7","message":"job failed"}` + "\n",
	}
	for index := range sources {
		if err := os.WriteFile(sources[index].StoredPath, []byte(payloads[index]), 0o600); err != nil {
			t.Fatal(err)
		}
		sources[index].SizeBytes = int64(len(payloads[index]))
		if err := storage.CreateSource(context.Background(), sources[index]); err != nil {
			t.Fatal(err)
		}
		job, err := imports.Start(context.Background(), sources[index])
		if err != nil {
			t.Fatal(err)
		}
		waitForImport(t, storage, job.ID)
	}
	server := httptest.NewServer(New(storage, imports, Config{
		SourceStoragePath: filepath.Join(root, "sources"),
		TempStoragePath:   filepath.Join(root, "temp"),
		MaxUploadSize:     1 << 20,
		MaxEntrySize:      1 << 20,
	}))
	defer server.Close()

	var group domain.SourceGroup
	doJSON(t, http.MethodPost, server.URL+"/api/v1/source-groups/", map[string]any{
		"name": "Incident trace", "description": "API and worker clocks",
	}, http.StatusCreated, &group)
	doJSON(t, http.MethodPut, server.URL+"/api/v1/source-groups/"+group.ID+"/members/api-source", map[string]any{
		"timeZone": "UTC", "manualClockOffsetMs": 0,
	}, http.StatusOK, nil)
	doJSON(t, http.MethodPut, server.URL+"/api/v1/source-groups/"+group.ID+"/members/worker-source", map[string]any{
		"timeZone": "Europe/Kyiv", "manualClockOffsetMs": 3600000,
	}, http.StatusOK, nil)
	for _, sourceID := range []string{"api-source", "worker-source"} {
		doJSON(t, http.MethodPost, server.URL+"/api/v1/source-groups/"+group.ID+"/mappings", map[string]any{
			"sourceId": sourceID, "canonicalName": "correlation_id", "sourceField": "request_id",
		}, http.StatusOK, nil)
	}

	var query struct {
		Items []domain.CorrelatedEntry `json:"items"`
		Count int                      `json:"count"`
	}
	doJSON(t, http.MethodPost, server.URL+"/api/v1/source-groups/"+group.ID+"/query", map[string]any{
		"filters": []map[string]any{{"field": "correlation_id", "operator": "eq", "value": "trace-7"}},
		"sort":    "asc", "limit": 20,
	}, http.StatusOK, &query)
	if query.Count != 2 {
		t.Fatalf("cross-source query returned %+v", query)
	}
	wantCorrected := "2026-07-24T10:00:00Z"
	for _, item := range query.Items {
		if item.CorrectedTimestamp == nil || item.CorrectedTimestamp.Format(time.RFC3339) != wantCorrected {
			t.Fatalf("corrected timestamp = %v, want %s", item.CorrectedTimestamp, wantCorrected)
		}
		if item.CanonicalFields["correlation_id"] != "trace-7" {
			t.Fatalf("canonical mapping missing: %+v", item.CanonicalFields)
		}
	}

	query.Items = nil
	doJSON(t, http.MethodPost, server.URL+"/api/v1/source-groups/"+group.ID+"/query", map[string]any{
		"query": "job failed", "filters": []any{}, "limit": 20,
	}, http.StatusOK, &query)
	if query.Count != 1 || query.Items[0].SourceID != "worker-source" {
		t.Fatalf("cross-source raw search returned %+v", query)
	}

	var timeline struct {
		Items []domain.GroupTimelinePoint `json:"items"`
	}
	doJSON(t, http.MethodGet, server.URL+"/api/v1/source-groups/"+group.ID+"/timeline?bucketSeconds=60", nil, http.StatusOK, &timeline)
	if len(timeline.Items) != 1 || timeline.Items[0].Count != 2 || timeline.Items[0].Errors != 1 {
		t.Fatalf("merged timeline = %+v", timeline.Items)
	}

	var correlation struct {
		Items []domain.CorrelatedEntry `json:"items"`
		Count int                      `json:"count"`
	}
	doJSON(t, http.MethodPost, server.URL+"/api/v1/source-groups/"+group.ID+"/correlations", map[string]any{
		"field": "correlation_id", "value": "trace-7", "limit": 20,
	}, http.StatusOK, &correlation)
	if correlation.Count != 2 {
		t.Fatalf("canonical correlation returned %+v", correlation)
	}

	doJSON(t, http.MethodPut, server.URL+"/api/v1/source-groups/"+group.ID, map[string]any{
		"name": "Incident trace updated", "description": "Corrected clocks",
	}, http.StatusOK, &group)
	if group.Name != "Incident trace updated" {
		t.Fatalf("group update failed: %+v", group)
	}
	doJSON(t, http.MethodDelete, server.URL+"/api/v1/source-groups/"+group.ID+"/members/worker-source", nil, http.StatusNoContent, nil)
	doJSON(t, http.MethodGet, server.URL+"/api/v1/source-groups/"+group.ID, nil, http.StatusOK, &group)
	if len(group.Members) != 1 || len(group.Mappings) != 1 {
		t.Fatalf("member removal did not clean mappings: %+v", group)
	}
	doJSON(t, http.MethodDelete, server.URL+"/api/v1/source-groups/"+group.ID, nil, http.StatusNoContent, nil)
}

func doJSON(t *testing.T, method, url string, body any, expectedStatus int, target any) {
	t.Helper()
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		var payload any
		_ = json.NewDecoder(response.Body).Decode(&payload)
		t.Fatalf("%s %s status = %d, want %d: %+v", method, url, response.StatusCode, expectedStatus, payload)
	}
	if target != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForImport(t *testing.T, storage *store.Store, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := storage.GetJob(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" {
			return
		}
		if job.Status == "failed" {
			t.Fatalf("import failed: %s", job.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for import")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForReindex(t *testing.T, baseURL, sourceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var job domain.ReindexJob
		doJSON(t, http.MethodGet, baseURL+"/api/v1/sources/"+sourceID+"/reindex", nil, http.StatusOK, &job)
		if job.Status == "completed" {
			return
		}
		if job.Status == "failed" {
			t.Fatalf("reindex failed: %s", job.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for reindex")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
