package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"loglens/internal/domain"
	"loglens/internal/extraction"
	"loglens/internal/parser"
	"loglens/internal/reader"
	"loglens/internal/store"
)

type Event struct {
	Type               string        `json:"type"`
	SourceID           string        `json:"sourceId"`
	Entry              *domain.Entry `json:"entry,omitempty"`
	RotationGeneration int           `json:"rotationGeneration"`
	Message            string        `json:"message,omitempty"`
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type Manager struct {
	store        *store.Store
	maxEntrySize int
	interval     time.Duration
	mu           sync.Mutex
	workers      map[string]worker
	subscribers  map[string]map[chan Event]struct{}
}

func NewManager(storage *store.Store, maxEntrySize int, interval time.Duration) *Manager {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	return &Manager{
		store: storage, maxEntrySize: maxEntrySize, interval: interval,
		workers:     make(map[string]worker),
		subscribers: make(map[string]map[chan Event]struct{}),
	}
}

func (manager *Manager) Resume(ctx context.Context) error {
	sources, err := manager.store.ListWatchedSources(ctx)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if err := manager.Start(ctx, source); err != nil {
			slog.Error("could not resume watched source", "source_id", source.ID, "error", err)
			_ = manager.store.StopWatching(context.Background(), source.ID, "failed")
		}
	}
	return nil
}

func (manager *Manager) Start(ctx context.Context, source domain.Source) error {
	if source.SourceType == "uploaded_file" {
		return fmt.Errorf("only server files can be watched")
	}
	if source.Status != "ready" && source.Status != "partially_parsed" {
		return fmt.Errorf("source must be indexed before live tail starts")
	}
	info, err := os.Stat(source.StoredPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("watched path is not a regular file")
	}
	manager.mu.Lock()
	if _, exists := manager.workers[source.ID]; exists {
		manager.mu.Unlock()
		return fmt.Errorf("source is already watched")
	}
	manager.mu.Unlock()

	var engine *extraction.Engine
	if source.ParserProfileID != nil && source.ParserVersion != nil {
		version, err := manager.store.GetParserVersion(ctx, *source.ParserProfileID, *source.ParserVersion)
		if err != nil {
			return err
		}
		engine, err = extraction.Compile(version)
		if err != nil {
			return err
		}
	}
	offset := source.WatchOffset
	if offset == 0 && source.EntryCount > 0 {
		offset = source.SizeBytes
	}
	if offset > info.Size() {
		offset = 0
	}
	identity := source.StoredPath
	if err := manager.store.StartWatching(ctx, source.ID, offset, identity); err != nil {
		return err
	}
	source.SourceType = "watched_file"
	source.WatchStatus = "running"
	source.WatchOffset = offset
	source.FileIdentity = identity
	runCtx, cancel := context.WithCancel(context.Background())
	current := worker{cancel: cancel, done: make(chan struct{})}
	manager.mu.Lock()
	manager.workers[source.ID] = current
	manager.mu.Unlock()
	go manager.run(runCtx, current.done, source, engine)
	manager.publish(Event{
		Type: "live.started", SourceID: source.ID,
		RotationGeneration: source.RotationGeneration,
	})
	return nil
}

func (manager *Manager) Stop(sourceID string) bool {
	manager.mu.Lock()
	current, exists := manager.workers[sourceID]
	manager.mu.Unlock()
	if !exists {
		return false
	}
	current.cancel()
	select {
	case <-current.done:
	case <-time.After(2 * time.Second):
	}
	_ = manager.store.StopWatching(context.Background(), sourceID, "stopped")
	manager.publish(Event{Type: "live.stopped", SourceID: sourceID})
	return true
}

func (manager *Manager) Refresh(ctx context.Context, source domain.Source) error {
	manager.Stop(source.ID)
	updated, err := manager.store.GetSource(ctx, source.ID)
	if err != nil {
		return err
	}
	return manager.Start(ctx, updated)
}

func (manager *Manager) Subscribe(sourceID string) (<-chan Event, func()) {
	channel := make(chan Event, 64)
	manager.mu.Lock()
	if manager.subscribers[sourceID] == nil {
		manager.subscribers[sourceID] = make(map[chan Event]struct{})
	}
	manager.subscribers[sourceID][channel] = struct{}{}
	manager.mu.Unlock()
	return channel, func() {
		manager.mu.Lock()
		if subscribers := manager.subscribers[sourceID]; subscribers != nil {
			delete(subscribers, channel)
			if len(subscribers) == 0 {
				delete(manager.subscribers, sourceID)
			}
		}
		manager.mu.Unlock()
	}
}

func (manager *Manager) run(ctx context.Context, done chan struct{}, source domain.Source, engine *extraction.Engine) {
	defer close(done)
	defer func() {
		manager.mu.Lock()
		delete(manager.workers, source.ID)
		manager.mu.Unlock()
	}()
	ticker := time.NewTicker(manager.interval)
	defer ticker.Stop()
	offset := source.WatchOffset
	nextNumber := source.EntryCount + 1
	rotationGeneration := source.RotationGeneration
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(source.StoredPath)
			if err != nil {
				manager.publish(Event{
					Type: "live.source_unavailable", SourceID: source.ID,
					RotationGeneration: rotationGeneration, Message: err.Error(),
				})
				continue
			}
			rotated := info.Size() < offset
			if rotated {
				offset = 0
				rotationGeneration++
				manager.publish(Event{
					Type: "live.rotated", SourceID: source.ID,
					RotationGeneration: rotationGeneration,
					Message:            "File size decreased; live tail restarted at byte zero",
				})
			}
			if info.Size() <= offset {
				if rotated {
					_ = manager.store.AppendLiveEntries(ctx, source.ID, nil, offset,
						info.Size(), source.StoredPath, true)
				}
				continue
			}
			entries, newOffset, events, err := manager.readAppended(
				ctx, source, engine, offset, info.Size(), nextNumber, rotationGeneration)
			if err != nil {
				manager.publish(Event{
					Type: "live.failed", SourceID: source.ID,
					RotationGeneration: rotationGeneration, Message: err.Error(),
				})
				continue
			}
			if newOffset == offset {
				continue
			}
			if err := manager.store.AppendLiveEntries(ctx, source.ID, entries, newOffset,
				info.Size(), source.StoredPath, rotated); err != nil {
				manager.publish(Event{
					Type: "live.failed", SourceID: source.ID,
					RotationGeneration: rotationGeneration, Message: err.Error(),
				})
				continue
			}
			offset = newOffset
			nextNumber += int64(len(entries))
			for _, event := range events {
				manager.publish(event)
			}
		}
	}
}

func (manager *Manager) readAppended(
	ctx context.Context,
	source domain.Source,
	engine *extraction.Engine,
	offset, size, nextNumber int64,
	rotationGeneration int,
) ([]domain.IndexedEntry, int64, []Event, error) {
	file, err := os.Open(source.StoredPath)
	if err != nil {
		return nil, offset, nil, err
	}
	defer file.Close()
	completeEnd, err := lastCompleteOffset(file, offset, size)
	if err != nil || completeEnd == offset {
		return nil, offset, nil, err
	}
	entries := make([]domain.IndexedEntry, 0, 64)
	events := make([]Event, 0, 64)
	section := io.NewSectionReader(file, offset, completeEnd-offset)
	err = reader.StreamLines(ctx, section, manager.maxEntrySize, func(line reader.Line) error {
		raw := line.Raw
		envelope := parser.Parse(source.Format, raw)
		entry := domain.IndexedEntry{
			SourceID: source.ID, Number: nextNumber + int64(len(entries)),
			ByteOffset: offset + line.ByteOffset, ByteLength: line.ByteLength,
			Timestamp: envelope.Timestamp, Level: envelope.Level, Service: envelope.Service,
			RequestID: envelope.RequestID, Message: envelope.Message,
			ParseStatus: envelope.ParseStatus,
		}
		if line.Oversized {
			entry.ParseStatus = "oversized"
		}
		if engine != nil {
			result := engine.Extract(raw, envelope)
			applySemantic(&entry, engine, result)
		}
		entries = append(entries, entry)
		rawText := string(raw)
		eventEntry := domain.Entry{
			SourceID: source.ID, Number: entry.Number, ByteOffset: entry.ByteOffset,
			ByteLength: entry.ByteLength, Timestamp: entry.Timestamp, Level: entry.Level,
			Service: entry.Service, RequestID: entry.RequestID, Message: entry.Message,
			ParseStatus: entry.ParseStatus, Raw: rawText, Fields: entry.CustomFields,
			EventType: entry.EventType, MatchedRuleID: entry.MatchedRuleID,
			ParserVersion: entry.ParserVersion, WarningCount: entry.WarningCount,
		}
		events = append(events, Event{
			Type: "live.entry", SourceID: source.ID, Entry: &eventEntry,
			RotationGeneration: rotationGeneration,
		})
		return nil
	})
	return entries, completeEnd, events, err
}

func applySemantic(entry *domain.IndexedEntry, engine *extraction.Engine, result domain.ExtractionResult) {
	entry.EventType = result.EventType
	if result.MatchedRuleID != nil {
		entry.MatchedRuleID = *result.MatchedRuleID
	}
	version := engine.Version().Version
	entry.ParserVersion = &version
	entry.WarningCount = len(result.Warnings)
	if entry.WarningCount > 0 && entry.ParseStatus != "malformed" && entry.ParseStatus != "oversized" {
		entry.ParseStatus = "partial"
	}
	entry.CustomFields = make(map[string]any, len(result.Fields))
	entry.IndexedValues = make(map[string]domain.FieldValue)
	for name, value := range result.Fields {
		entry.CustomFields[name] = fieldJSONValue(value)
		if field, ok := engine.Field(name); ok && (field.Indexed || field.Aggregatable) {
			entry.IndexedValues[field.ID] = value
		}
	}
}

func lastCompleteOffset(file *os.File, start, end int64) (int64, error) {
	const chunkSize = int64(64 << 10)
	for position := end; position > start; {
		chunkStart := position - chunkSize
		if chunkStart < start {
			chunkStart = start
		}
		buffer := make([]byte, int(position-chunkStart))
		if _, err := file.ReadAt(buffer, chunkStart); err != nil && !errors.Is(err, io.EOF) {
			return start, err
		}
		for index := len(buffer) - 1; index >= 0; index-- {
			if buffer[index] == '\n' {
				return chunkStart + int64(index) + 1, nil
			}
		}
		position = chunkStart
	}
	return start, nil
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

func (manager *Manager) publish(event Event) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for subscriber := range manager.subscribers[event.SourceID] {
		select {
		case subscriber <- event:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- Event{
				Type: "live.backpressure", SourceID: event.SourceID,
				RotationGeneration: event.RotationGeneration,
				Message:            "Live client fell behind; older UI events were dropped after indexing",
			}:
			default:
			}
		}
	}
}
