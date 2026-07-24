package parser

import (
	"strings"
	"testing"
)

func TestParseJSONLAliases(t *testing.T) {
	raw := []byte(`{"time":"2026-07-24T10:15:43.123Z","severity":"warning","component":"payments","msg":"retry scheduled","traceId":"req-42"}`)
	result := Parse("jsonl", raw)
	if result.ParseStatus != "parsed" {
		t.Fatalf("ParseStatus = %q, want parsed", result.ParseStatus)
	}
	if result.Level != "WARN" {
		t.Errorf("Level = %q, want WARN", result.Level)
	}
	if result.Service != "payments" || result.RequestID != "req-42" {
		t.Errorf("unexpected mapped fields: service=%q request=%q", result.Service, result.RequestID)
	}
	if result.Timestamp == nil || result.Timestamp.Nanosecond() != 123000000 {
		t.Errorf("timestamp was not parsed with precision: %v", result.Timestamp)
	}
}

func TestDetectJSONL(t *testing.T) {
	input := strings.NewReader(
		"{\"timestamp\":\"2026-07-24T10:15:43Z\",\"level\":\"INFO\",\"message\":\"ok\"}\n" +
			"{\"timestamp\":\"2026-07-24T10:15:44Z\",\"level\":\"ERROR\",\"message\":\"failed\"}\n" +
			"malformed\n")
	detection, err := Detect(input, 100)
	if err != nil {
		t.Fatal(err)
	}
	if detection.Format != "plain_text" {
		t.Fatalf("format = %q; two out of three lines should not pass the 80%% JSONL threshold", detection.Format)
	}

	input = strings.NewReader(
		"{\"timestamp\":\"2026-07-24T10:15:43Z\",\"message\":\"one\"}\n" +
			"{\"timestamp\":\"2026-07-24T10:15:44Z\",\"message\":\"two\"}\n")
	detection, err = Detect(input, 100)
	if err != nil {
		t.Fatal(err)
	}
	if detection.Format != "jsonl" {
		t.Fatalf("format = %q, want jsonl", detection.Format)
	}
	if detection.DetectedFields["message"] != "message" {
		t.Errorf("message mapping = %q", detection.DetectedFields["message"])
	}
}

func TestParsePlainTextAndNormalizeLevel(t *testing.T) {
	result := Parse("plain_text", []byte("2026-07-24 13:42:17 ERROR [payment] request=abc-123 Payment timeout"))
	if result.Level != "ERROR" || result.Service != "payment" || result.RequestID != "abc-123" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if NormalizeLevel("critical") != "FATAL" || NormalizeLevel("err") != "ERROR" {
		t.Fatal("level aliases were not normalized")
	}
}
