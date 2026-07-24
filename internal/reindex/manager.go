package reindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"loglens/internal/domain"
	"loglens/internal/extraction"
	"loglens/internal/parser"
	"loglens/internal/store"
)

type Event struct {
	Type string            `json:"type"`
	Job  domain.ReindexJob `json:"job"`
}

type Manager struct {
	store        *store.Store
	batchSize    int
	maxEntrySize int
	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	subscribers  map[string]map[chan Event]struct{}
}

func NewManager(storage *store.Store, batchSize, maxEntrySize int) *Manager {
	if batchSize < 1 {
		batchSize = 500
	}
	if batchSize > 500 {
		batchSize = 500
	}
	return &Manager{
		store: storage, batchSize: batchSize, maxEntrySize: maxEntrySize,
		cancels:     make(map[string]context.CancelFunc),
		subscribers: make(map[string]map[chan Event]struct{}),
	}
}

func (manager *Manager) Start(ctx context.Context, source domain.Source) (domain.ReindexJob, error) {
	if source.ParserProfileID == nil || source.ParserVersion == nil {
		return domain.ReindexJob{}, fmt.Errorf("source has no extraction profile")
	}
	if source.ExtractionStatus == "indexing" {
		return domain.ReindexJob{}, fmt.Errorf("source is already being reindexed")
	}
	version, err := manager.store.GetParserVersion(ctx, *source.ParserProfileID, *source.ParserVersion)
	if err != nil {
		return domain.ReindexJob{}, err
	}
	if _, err := extraction.Compile(version); err != nil {
		return domain.ReindexJob{}, err
	}
	job := domain.ReindexJob{
		ID: uuid.NewString(), SourceID: source.ID, ParserVersionID: version.ID,
		Status: "queued", TotalEntries: source.EntryCount,
	}
	if err := manager.store.CreateReindexJob(ctx, job); err != nil {
		return domain.ReindexJob{}, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	manager.mu.Lock()
	manager.cancels[job.ID] = cancel
	manager.mu.Unlock()
	go manager.run(runCtx, job, source, version)
	return job, nil
}

func (manager *Manager) Cancel(jobID string) bool {
	manager.mu.Lock()
	cancel, ok := manager.cancels[jobID]
	manager.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (manager *Manager) Subscribe(jobID string) (<-chan Event, func()) {
	channel := make(chan Event, 8)
	manager.mu.Lock()
	if manager.subscribers[jobID] == nil {
		manager.subscribers[jobID] = make(map[chan Event]struct{})
	}
	manager.subscribers[jobID][channel] = struct{}{}
	manager.mu.Unlock()
	return channel, func() {
		manager.mu.Lock()
		if subscribers := manager.subscribers[jobID]; subscribers != nil {
			delete(subscribers, channel)
			if len(subscribers) == 0 {
				delete(manager.subscribers, jobID)
			}
		}
		manager.mu.Unlock()
	}
}

func (manager *Manager) run(ctx context.Context, job domain.ReindexJob, source domain.Source, version domain.ParserProfileVersion) {
	defer func() {
		manager.mu.Lock()
		delete(manager.cancels, job.ID)
		manager.mu.Unlock()
	}()
	if err := manager.store.StartReindexJob(context.Background(), job.ID); err != nil {
		manager.finish(job, "failed", "failed", err, 0, 0)
		return
	}
	engine, err := extraction.Compile(version)
	if err != nil {
		manager.finish(job, "failed", "failed", err, 0, 0)
		return
	}
	file, err := os.Open(source.StoredPath)
	if err != nil {
		manager.finish(job, "failed", "failed", err, 0, 0)
		return
	}
	defer file.Close()

	started, _ := manager.store.GetReindexJob(context.Background(), job.ID)
	manager.publish(Event{Type: "reindex.started", Job: started})
	var processed, warningCount int64
	cursor := int64(1)
	for {
		if err := ctx.Err(); err != nil {
			manager.finish(job, "cancelled", "partial", err, processed, warningCount)
			return
		}
		metadata, hasMore, err := manager.store.QueryEntryMetadata(ctx, source.ID, domain.EntryFilter{
			Cursor: cursor, Limit: manager.batchSize,
		})
		if err != nil {
			manager.finish(job, "failed", "failed", err, processed, warningCount)
			return
		}
		if len(metadata) == 0 {
			break
		}
		updates := make([]domain.ExtractionUpdate, 0, len(metadata))
		for _, entry := range metadata {
			raw, err := readEntry(file, entry, manager.maxEntrySize)
			if err != nil {
				manager.finish(job, "failed", "failed", err, processed, warningCount)
				return
			}
			envelope := parser.Parse(source.Format, raw)
			result := engine.Extract(raw, envelope)
			update, err := buildUpdate(entry, version, engine, envelope.ParseStatus, result)
			if err != nil {
				manager.finish(job, "failed", "failed", err, processed, warningCount)
				return
			}
			updates = append(updates, update)
			warningCount += int64(len(result.Warnings))
		}
		if err := manager.store.ApplyExtractionBatch(ctx, updates); err != nil {
			if errors.Is(err, context.Canceled) {
				manager.finish(job, "cancelled", "partial", err, processed, warningCount)
			} else {
				manager.finish(job, "failed", "failed", err, processed, warningCount)
			}
			return
		}
		processed += int64(len(updates))
		cursor = metadata[len(metadata)-1].Number + 1
		_ = manager.store.UpdateReindexProgress(context.Background(), job.ID, processed, warningCount)
		current, _ := manager.store.GetReindexJob(context.Background(), job.ID)
		manager.publish(Event{Type: "reindex.progress", Job: current})
		if !hasMore {
			break
		}
	}
	sourceStatus := "ready"
	if warningCount > 0 {
		sourceStatus = "partial"
	}
	manager.finish(job, "completed", sourceStatus, nil, processed, warningCount)
}

func buildUpdate(
	entry domain.IndexedEntry,
	version domain.ParserProfileVersion,
	engine *extraction.Engine,
	parseStatus string,
	result domain.ExtractionResult,
) (domain.ExtractionUpdate, error) {
	jsonFields := make(map[string]any, len(result.Fields))
	indexed := make(map[string]domain.FieldValue)
	for name, value := range result.Fields {
		jsonFields[name] = fieldJSONValue(value)
		if definition, ok := engine.Field(name); ok && (definition.Indexed || definition.Aggregatable) {
			indexed[definition.ID] = value
		}
	}
	encoded, err := json.Marshal(jsonFields)
	if err != nil {
		return domain.ExtractionUpdate{}, err
	}
	matchedRuleID := ""
	if result.MatchedRuleID != nil {
		matchedRuleID = *result.MatchedRuleID
	}
	if len(result.Warnings) > 0 && parseStatus != "malformed" && parseStatus != "oversized" {
		parseStatus = "partial"
	}
	return domain.ExtractionUpdate{
		EntryID: entry.ID, EventType: result.EventType, MatchedRuleID: matchedRuleID,
		FieldsJSON: string(encoded), ParserVersion: version.Version,
		WarningCount: len(result.Warnings), ParseStatus: parseStatus, IndexedValues: indexed,
	}, nil
}

func fieldJSONValue(value domain.FieldValue) any {
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

func readEntry(file *os.File, entry domain.IndexedEntry, maxEntrySize int) ([]byte, error) {
	readLength := entry.ByteLength
	if readLength > int64(maxEntrySize) {
		readLength = int64(maxEntrySize)
	}
	raw := make([]byte, int(readLength))
	n, err := file.ReadAt(raw, entry.ByteOffset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return raw[:n], nil
}

func (manager *Manager) finish(job domain.ReindexJob, status, sourceStatus string, failure error, processed, warnings int64) {
	message := ""
	if failure != nil && !errors.Is(failure, context.Canceled) {
		message = failure.Error()
	}
	_ = manager.store.FinishReindexJob(context.Background(), job.ID, job.SourceID,
		status, sourceStatus, message, processed, warnings)
	current, _ := manager.store.GetReindexJob(context.Background(), job.ID)
	manager.publish(Event{Type: "reindex." + status, Job: current})
	if failure != nil && !errors.Is(failure, context.Canceled) {
		slog.Error("source reindex failed", "source_id", job.SourceID, "job_id", job.ID, "error", failure)
	}
}

func (manager *Manager) publish(event Event) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for subscriber := range manager.subscribers[event.Job.ID] {
		select {
		case subscriber <- event:
		default:
		}
	}
}
