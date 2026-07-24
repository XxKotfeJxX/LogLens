package api

import (
	"testing"
	"time"

	"loglens/internal/domain"
)

func BenchmarkCanonicalMappingAndClockCorrection(b *testing.B) {
	timestamp := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	entry := domain.Entry{
		Number: 42, Timestamp: &timestamp, Level: "ERROR", Service: "worker",
		RequestID: "trace-7", EventType: "job.failed",
		Fields: map[string]any{
			"correlationId": "trace-7", "username": "andrew", "duration_ms": 250,
		},
	}
	mappings := map[string]domain.CanonicalFieldMapping{
		"correlation_id": {CanonicalName: "correlation_id", SourceField: "correlationId"},
		"actor":          {CanonicalName: "actor", SourceField: "username"},
		"duration":       {CanonicalName: "duration", SourceField: "duration_ms"},
	}
	member := domain.SourceGroupMember{
		TimeZone: "Europe/Kyiv", ManualClockOffsetMS: 3_600_000,
	}
	b.ReportAllocs()
	for b.Loop() {
		values := canonicalEntryValues(entry, mappings)
		corrected := correctSourceTimestamp(entry.Timestamp, member)
		if values["correlation_id"] == nil || corrected == nil {
			b.Fatal("correlation transform returned no value")
		}
	}
}
