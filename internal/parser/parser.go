package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"loglens/internal/domain"
)

var (
	timestampAliases = []string{"timestamp", "time", "ts", "datetime", "date", "@timestamp"}
	levelAliases     = []string{"level", "severity", "log_level", "loglevel"}
	messageAliases   = []string{"message", "msg", "text", "body"}
	serviceAliases   = []string{"service", "service_name", "servicename", "component", "application", "app"}
	requestAliases   = []string{"request_id", "requestid", "trace_id", "traceid", "correlation_id", "correlationid"}

	plainPattern   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ][0-9:.+\-Z]+)\s+(TRACE|DEBUG|INFO|WARN(?:ING)?|ERROR|ERR|FATAL|CRITICAL)\s*(?:\[([^\]]+)\])?\s*(.*)$`)
	levelPattern   = regexp.MustCompile(`(?i)\b(TRACE|DEBUG|INFO|WARN(?:ING)?|ERROR|ERR|FATAL|CRITICAL)\b`)
	requestPattern = regexp.MustCompile(`(?i)\b(?:request[_-]?id|request|trace[_-]?id|correlation[_-]?id)[=:]\s*([A-Za-z0-9._:-]+)`)
)

type Result struct {
	Timestamp   *time.Time
	Level       string
	Service     string
	RequestID   string
	Message     string
	ParseStatus string
	Fields      map[string]any
}

func DetectFile(path string, maxLines int) (domain.Detection, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.Detection{}, err
	}
	defer file.Close()
	return Detect(file, maxLines)
}

func Detect(reader io.Reader, maxLines int) (domain.Detection, error) {
	if maxLines <= 0 {
		maxLines = 100
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	total, jsonObjects, structured := 0, 0, 0
	fieldHits := map[string]map[string]int{
		"timestamp": {}, "level": {}, "message": {}, "service": {}, "requestId": {},
	}
	preview := make([]domain.Entry, 0, 8)
	for scanner.Scan() && total < maxLines {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		total++
		var object map[string]any
		if json.Unmarshal(raw, &object) == nil && object != nil {
			jsonObjects++
			normalized := normalizeKeys(object)
			hasUsefulField := false
			for logical, aliases := range map[string][]string{
				"timestamp": timestampAliases,
				"level":     levelAliases,
				"message":   messageAliases,
				"service":   serviceAliases,
				"requestId": requestAliases,
			} {
				if key, ok := findKey(normalized, aliases); ok {
					fieldHits[logical][key]++
					if logical == "message" || logical == "timestamp" {
						hasUsefulField = true
					}
				}
			}
			if hasUsefulField {
				structured++
			}
		}
		if len(preview) < cap(preview) {
			result := Parse("jsonl", raw)
			preview = append(preview, domain.Entry{
				Number:      int64(total),
				Timestamp:   result.Timestamp,
				Level:       result.Level,
				Service:     result.Service,
				RequestID:   result.RequestID,
				Message:     result.Message,
				ParseStatus: result.ParseStatus,
				Raw:         string(raw),
				Fields:      result.Fields,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.Detection{}, err
	}
	if total == 0 {
		return domain.Detection{Format: "plain_text", Confidence: 0, DetectedFields: map[string]string{}}, nil
	}

	format := "plain_text"
	confidence := 0.65
	ratio := float64(jsonObjects) / float64(total)
	if ratio >= 0.8 && structured > 0 {
		format = "jsonl"
		confidence = ratio
	}
	fields := make(map[string]string)
	if format == "jsonl" {
		for logical, hits := range fieldHits {
			best, count := "", 0
			for key, value := range hits {
				if value > count {
					best, count = key, value
				}
			}
			if best != "" {
				fields[logical] = best
			}
		}
	} else {
		preview = nil
	}
	return domain.Detection{
		Format:         format,
		Confidence:     confidence,
		DetectedFields: fields,
		Preview:        preview,
	}, nil
}

func Parse(format string, raw []byte) Result {
	raw = bytes.TrimRight(raw, "\r\n")
	if format == "jsonl" {
		return parseJSONL(raw)
	}
	return parsePlainText(raw)
}

func parseJSONL(raw []byte) Result {
	result := Result{Level: "UNKNOWN", Message: string(raw), ParseStatus: "malformed"}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return result
	}
	normalized := normalizeKeys(object)
	result.Fields = object
	result.ParseStatus = "parsed"
	result.Message = valueString(findValue(normalized, messageAliases))
	if result.Message == "" {
		result.Message = string(raw)
		result.ParseStatus = "partial"
	}
	result.Level = NormalizeLevel(valueString(findValue(normalized, levelAliases)))
	result.Service = valueString(findValue(normalized, serviceAliases))
	result.RequestID = valueString(findValue(normalized, requestAliases))
	if rawTimestamp := valueString(findValue(normalized, timestampAliases)); rawTimestamp != "" {
		if parsed, err := ParseTimestamp(rawTimestamp); err == nil {
			result.Timestamp = &parsed
		} else {
			result.ParseStatus = "partial"
		}
	}
	return result
}

func parsePlainText(raw []byte) Result {
	text := string(raw)
	result := Result{Level: "UNKNOWN", Message: text, ParseStatus: "unparsed"}
	if matches := plainPattern.FindStringSubmatch(text); matches != nil {
		if parsed, err := ParseTimestamp(matches[1]); err == nil {
			result.Timestamp = &parsed
		}
		result.Level = NormalizeLevel(matches[2])
		result.Service = matches[3]
		result.Message = strings.TrimSpace(matches[4])
		result.ParseStatus = "parsed"
	}
	if result.Level == "UNKNOWN" {
		if level := levelPattern.FindStringSubmatch(text); level != nil {
			result.Level = NormalizeLevel(level[1])
			result.ParseStatus = "partial"
		}
	}
	if request := requestPattern.FindStringSubmatch(text); request != nil {
		result.RequestID = request[1]
		if result.ParseStatus == "unparsed" {
			result.ParseStatus = "partial"
		}
	}
	return result
}

func NormalizeLevel(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "TRACE":
		return "TRACE"
	case "DEBUG":
		return "DEBUG"
	case "INFO", "INFORMATION":
		return "INFO"
	case "WARN", "WARNING":
		return "WARN"
	case "ERROR", "ERR":
		return "ERROR"
	case "FATAL", "CRITICAL", "CRIT", "PANIC":
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

func ParseTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}

func normalizeKeys(object map[string]any) map[string]keyValue {
	result := make(map[string]keyValue, len(object))
	for key, value := range object {
		result[strings.ToLower(key)] = keyValue{original: key, value: value}
	}
	return result
}

type keyValue struct {
	original string
	value    any
}

func findKey(values map[string]keyValue, aliases []string) (string, bool) {
	for _, alias := range aliases {
		if found, ok := values[strings.ToLower(alias)]; ok {
			return found.original, true
		}
	}
	return "", false
}

func findValue(values map[string]keyValue, aliases []string) any {
	for _, alias := range aliases {
		if found, ok := values[strings.ToLower(alias)]; ok {
			return found.value
		}
	}
	return nil
}

func valueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%v", typed)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", typed)
	}
}

var ErrEntryTooLarge = errors.New("entry exceeds configured maximum")
