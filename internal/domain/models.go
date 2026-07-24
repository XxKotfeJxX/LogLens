package domain

import "time"

type Source struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	OriginalName string     `json:"originalName"`
	StoredPath   string     `json:"-"`
	SourceType   string     `json:"sourceType"`
	Format       string     `json:"format"`
	Status       string     `json:"status"`
	SizeBytes    int64      `json:"sizeBytes"`
	EntryCount   int64      `json:"entryCount"`
	ParsedCount  int64      `json:"parsedCount"`
	FailedCount  int64      `json:"failedCount"`
	CreatedAt    time.Time  `json:"createdAt"`
	ImportedAt   *time.Time `json:"importedAt,omitempty"`
}

type ImportJob struct {
	ID             string     `json:"id"`
	SourceID       string     `json:"sourceId"`
	Status         string     `json:"status"`
	BytesProcessed int64      `json:"bytesProcessed"`
	TotalBytes     int64      `json:"totalBytes"`
	EntriesFound   int64      `json:"entriesFound"`
	ParseFailures  int64      `json:"parseFailures"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
}

type IndexedEntry struct {
	SourceID    string
	Number      int64
	ByteOffset  int64
	ByteLength  int64
	Timestamp   *time.Time
	Level       string
	Service     string
	RequestID   string
	Message     string
	ParseStatus string
}

type Entry struct {
	Number      int64          `json:"number"`
	ByteOffset  int64          `json:"byteOffset"`
	ByteLength  int64          `json:"byteLength"`
	Timestamp   *time.Time     `json:"timestamp,omitempty"`
	Level       string         `json:"level"`
	Service     string         `json:"service,omitempty"`
	RequestID   string         `json:"requestId,omitempty"`
	Message     string         `json:"message"`
	ParseStatus string         `json:"parseStatus"`
	Raw         string         `json:"raw"`
	Fields      map[string]any `json:"fields,omitempty"`
}

type Detection struct {
	Format         string            `json:"format"`
	Confidence     float64           `json:"confidence"`
	DetectedFields map[string]string `json:"detectedFields"`
	Preview        []Entry           `json:"preview"`
}

type EntryFilter struct {
	Cursor      int64
	Limit       int
	Levels      []string
	Services    []string
	RequestID   string
	Query       string
	From        *time.Time
	To          *time.Time
	ParseStatus []string
}

type Statistics struct {
	TotalEntries     int64            `json:"totalEntries"`
	ParsedEntries    int64            `json:"parsedEntries"`
	MalformedEntries int64            `json:"malformedEntries"`
	Earliest         *time.Time       `json:"earliestTimestamp,omitempty"`
	Latest           *time.Time       `json:"latestTimestamp,omitempty"`
	ByLevel          map[string]int64 `json:"byLevel"`
	ByService        map[string]int64 `json:"byService"`
}

type TimelinePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int64     `json:"count"`
	Errors    int64     `json:"errors"`
}
