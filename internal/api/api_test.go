package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
