package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (api *API) startWatch(w http.ResponseWriter, r *http.Request) {
	source, err := api.store.GetSource(r.Context(), chi.URLParam(r, "sourceID"))
	if !api.handleStoreError(w, err) {
		return
	}
	if source.ExtractionStatus == "indexing" {
		writeError(w, http.StatusConflict, "source_busy", "Wait for reindex to finish before starting live tail")
		return
	}
	if err := api.live.Start(r.Context(), source); err != nil {
		writeError(w, http.StatusConflict, "watch_not_started", err.Error())
		return
	}
	updated, err := api.store.GetSource(r.Context(), source.ID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, updated)
}

func (api *API) stopWatch(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "sourceID")
	if _, err := api.store.GetSource(r.Context(), sourceID); !api.handleStoreError(w, err) {
		return
	}
	if !api.live.Stop(sourceID) {
		writeError(w, http.StatusConflict, "watch_not_running", "This source is not watched by this server")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopped"})
}

func (api *API) liveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "streaming_unavailable", "Streaming is not supported")
		return
	}
	sourceID := chi.URLParam(r, "sourceID")
	source, err := api.store.GetSource(r.Context(), sourceID)
	if !api.handleStoreError(w, err) {
		return
	}
	events, unsubscribe := api.live.Subscribe(sourceID)
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "live.state", map[string]any{
		"sourceId": sourceID, "status": source.WatchStatus,
		"rotationGeneration": source.RotationGeneration,
	})
	flusher.Flush()
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
			writeSSE(w, event.Type, event)
			flusher.Flush()
		}
	}
}
