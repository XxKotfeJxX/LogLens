package importjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"loglens/internal/domain"
	"loglens/internal/parser"
	"loglens/internal/reader"
	"loglens/internal/store"
)

type Event struct {
	Type string           `json:"type"`
	Job  domain.ImportJob `json:"job"`
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
	return &Manager{
		store: storage, batchSize: batchSize, maxEntrySize: maxEntrySize,
		cancels:     make(map[string]context.CancelFunc),
		subscribers: make(map[string]map[chan Event]struct{}),
	}
}

func (m *Manager) Start(ctx context.Context, source domain.Source) (domain.ImportJob, error) {
	if source.Status == "importing" || source.Status == "queued" {
		return domain.ImportJob{}, fmt.Errorf("source is already being imported")
	}
	if err := m.store.PrepareSourceImport(ctx, source.ID); err != nil {
		return domain.ImportJob{}, err
	}
	job := domain.ImportJob{
		ID: uuid.NewString(), SourceID: source.ID, Status: "queued", TotalBytes: source.SizeBytes,
	}
	if err := m.store.CreateJob(ctx, job); err != nil {
		return domain.ImportJob{}, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancels[job.ID] = cancel
	m.mu.Unlock()
	go m.run(runCtx, job, source)
	return job, nil
}

func (m *Manager) Cancel(jobID string) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[jobID]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (m *Manager) Subscribe(jobID string) (<-chan Event, func()) {
	channel := make(chan Event, 8)
	m.mu.Lock()
	if m.subscribers[jobID] == nil {
		m.subscribers[jobID] = make(map[chan Event]struct{})
	}
	m.subscribers[jobID][channel] = struct{}{}
	m.mu.Unlock()
	return channel, func() {
		m.mu.Lock()
		if subscribers := m.subscribers[jobID]; subscribers != nil {
			delete(subscribers, channel)
			if len(subscribers) == 0 {
				delete(m.subscribers, jobID)
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) run(ctx context.Context, initial domain.ImportJob, source domain.Source) {
	defer func() {
		m.mu.Lock()
		delete(m.cancels, initial.ID)
		m.mu.Unlock()
	}()
	if err := m.store.StartJob(ctx, initial.ID); err != nil {
		m.fail(initial, source, "database_error", err)
		return
	}
	if err := m.store.MarkSourceImporting(ctx, source.ID); err != nil {
		m.fail(initial, source, "database_error", err)
		return
	}
	job, _ := m.store.GetJob(context.Background(), initial.ID)
	m.publish(Event{Type: "import.started", Job: job})

	file, err := os.Open(source.StoredPath)
	if err != nil {
		m.fail(initial, source, "source_unavailable", err)
		return
	}
	defer file.Close()

	batch := make([]domain.IndexedEntry, 0, m.batchSize)
	var total, parsed, failures, bytesProcessed int64
	lastProgress := time.Now()
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := m.store.InsertEntries(ctx, batch); err != nil {
			return err
		}
		batch = batch[:0]
		if time.Since(lastProgress) >= 300*time.Millisecond || bytesProcessed == source.SizeBytes {
			if err := m.updateProgress(initial.ID, bytesProcessed, total, failures); err != nil {
				return err
			}
			lastProgress = time.Now()
		}
		return nil
	}

	err = reader.StreamLines(ctx, file, m.maxEntrySize, func(line reader.Line) error {
		result := parser.Parse(source.Format, line.Raw)
		if line.Oversized {
			result.ParseStatus = "oversized"
		}
		if result.ParseStatus == "parsed" {
			parsed++
		} else if result.ParseStatus == "malformed" || result.ParseStatus == "oversized" {
			failures++
		}
		total++
		bytesProcessed = line.ByteOffset + line.ByteLength
		batch = append(batch, domain.IndexedEntry{
			SourceID: source.ID, Number: line.Number, ByteOffset: line.ByteOffset,
			ByteLength: line.ByteLength, Timestamp: result.Timestamp, Level: result.Level,
			Service: result.Service, RequestID: result.RequestID, Message: result.Message,
			ParseStatus: result.ParseStatus,
		})
		if len(batch) >= m.batchSize {
			return flush()
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.cancelled(initial, source, bytesProcessed, total, failures)
			return
		}
		m.failProgress(initial, source, "import_failed", err, bytesProcessed, total, failures)
		return
	}

	status := "ready"
	if failures > 0 {
		status = "partially_parsed"
	}
	background := context.Background()
	if err := m.store.CompleteSource(background, source.ID, total, parsed, failures, status); err != nil {
		m.failProgress(initial, source, "database_error", err, bytesProcessed, total, failures)
		return
	}
	_ = m.store.FinishJob(background, initial.ID, "completed", "", "", bytesProcessed, total, failures)
	job, _ = m.store.GetJob(background, initial.ID)
	m.publish(Event{Type: "import.completed", Job: job})
	slog.Info("source import completed", "source_id", source.ID, "entries", total, "bytes", bytesProcessed)
}

func (m *Manager) updateProgress(jobID string, bytes, entries, failures int64) error {
	background := context.Background()
	if err := m.store.UpdateJobProgress(background, jobID, bytes, entries, failures); err != nil {
		return err
	}
	job, err := m.store.GetJob(background, jobID)
	if err == nil {
		m.publish(Event{Type: "import.progress", Job: job})
	}
	return nil
}

func (m *Manager) cancelled(initial domain.ImportJob, source domain.Source, bytes, entries, failures int64) {
	background := context.Background()
	_ = m.store.FinishJob(background, initial.ID, "cancelled", "", "", bytes, entries, failures)
	_ = m.store.MarkSourceFailed(background, source.ID, "cancelled")
	job, _ := m.store.GetJob(background, initial.ID)
	m.publish(Event{Type: "import.cancelled", Job: job})
}

func (m *Manager) fail(initial domain.ImportJob, source domain.Source, code string, err error) {
	m.failProgress(initial, source, code, err, 0, 0, 0)
}

func (m *Manager) failProgress(initial domain.ImportJob, source domain.Source, code string, err error, bytes, entries, failures int64) {
	background := context.Background()
	_ = m.store.FinishJob(background, initial.ID, "failed", code, err.Error(), bytes, entries, failures)
	_ = m.store.MarkSourceFailed(background, source.ID, "failed")
	job, _ := m.store.GetJob(background, initial.ID)
	m.publish(Event{Type: "import.failed", Job: job})
	slog.Error("source import failed", "source_id", source.ID, "job_id", initial.ID, "error", err)
}

func (m *Manager) publish(event Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for subscriber := range m.subscribers[event.Job.ID] {
		select {
		case subscriber <- event:
		default:
		}
	}
}
