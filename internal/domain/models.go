package domain

import "time"

type Source struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	OriginalName       string     `json:"originalName"`
	StoredPath         string     `json:"-"`
	SourceType         string     `json:"sourceType"`
	Format             string     `json:"format"`
	Status             string     `json:"status"`
	SizeBytes          int64      `json:"sizeBytes"`
	EntryCount         int64      `json:"entryCount"`
	ParsedCount        int64      `json:"parsedCount"`
	FailedCount        int64      `json:"failedCount"`
	ParserProfileID    *string    `json:"parserProfileId,omitempty"`
	ParserVersion      *int       `json:"parserVersion,omitempty"`
	ExtractionStatus   string     `json:"extractionStatus"`
	ReindexRequired    bool       `json:"reindexRequired"`
	WatchStatus        string     `json:"watchStatus"`
	WatchOffset        int64      `json:"watchOffset"`
	FileIdentity       string     `json:"fileIdentity,omitempty"`
	RotationGeneration int        `json:"rotationGeneration"`
	CreatedAt          time.Time  `json:"createdAt"`
	ImportedAt         *time.Time `json:"importedAt,omitempty"`
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
	ID            int64
	SourceID      string
	Number        int64
	ByteOffset    int64
	ByteLength    int64
	Timestamp     *time.Time
	Level         string
	Service       string
	RequestID     string
	Message       string
	ParseStatus   string
	EventType     string
	MatchedRuleID string
	CustomFields  map[string]any
	ParserVersion *int
	WarningCount  int
	IndexedValues map[string]FieldValue
}

type Entry struct {
	SourceID      string         `json:"sourceId,omitempty"`
	Number        int64          `json:"number"`
	ByteOffset    int64          `json:"byteOffset"`
	ByteLength    int64          `json:"byteLength"`
	Timestamp     *time.Time     `json:"timestamp,omitempty"`
	Level         string         `json:"level"`
	Service       string         `json:"service,omitempty"`
	RequestID     string         `json:"requestId,omitempty"`
	Message       string         `json:"message"`
	ParseStatus   string         `json:"parseStatus"`
	Raw           string         `json:"raw"`
	Fields        map[string]any `json:"fields,omitempty"`
	EventType     string         `json:"eventType,omitempty"`
	MatchedRuleID string         `json:"matchedRuleId,omitempty"`
	ParserVersion *int           `json:"parserVersion,omitempty"`
	WarningCount  int            `json:"warningCount"`
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
	EventTypes  []string
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

type FieldType string

const (
	FieldString   FieldType = "string"
	FieldInteger  FieldType = "integer"
	FieldFloat    FieldType = "float"
	FieldBoolean  FieldType = "boolean"
	FieldDateTime FieldType = "datetime"
	FieldDuration FieldType = "duration"
)

type FieldValue struct {
	Type           FieldType      `json:"type"`
	StringValue    *string        `json:"stringValue,omitempty"`
	IntegerValue   *int64         `json:"integerValue,omitempty"`
	FloatValue     *float64       `json:"floatValue,omitempty"`
	BooleanValue   *bool          `json:"booleanValue,omitempty"`
	TimestampValue *time.Time     `json:"timestampValue,omitempty"`
	DurationValue  *time.Duration `json:"durationValue,omitempty"`
}

func (value FieldValue) Value() any {
	switch value.Type {
	case FieldInteger:
		return value.IntegerValue
	case FieldFloat:
		return value.FloatValue
	case FieldBoolean:
		return value.BooleanValue
	case FieldDateTime:
		return value.TimestampValue
	case FieldDuration:
		return value.DurationValue
	default:
		return value.StringValue
	}
}

type ParseWarning struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type ExtractionResult struct {
	EventType     string                `json:"eventType,omitempty"`
	MatchedRuleID *string               `json:"matchedRuleId,omitempty"`
	Fields        map[string]FieldValue `json:"fields"`
	Warnings      []ParseWarning        `json:"warnings"`
}

type ParserProfile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ParserProfileVersion struct {
	ID                string            `json:"id"`
	ProfileID         string            `json:"profileId"`
	Version           int               `json:"version"`
	RecordFormat      string            `json:"recordFormat"`
	ConfigurationJSON string            `json:"-"`
	Configuration     map[string]any    `json:"configuration"`
	CreatedAt         time.Time         `json:"createdAt"`
	Rules             []ExtractionRule  `json:"rules"`
	Fields            []FieldDefinition `json:"fields"`
}

type ExtractionRule struct {
	ID                string         `json:"id"`
	ParserVersionID   string         `json:"parserVersionId"`
	Name              string         `json:"name"`
	EventType         string         `json:"eventType"`
	Priority          int            `json:"priority"`
	MatcherType       string         `json:"matcherType"`
	MatcherConfigJSON string         `json:"-"`
	MatcherConfig     map[string]any `json:"matcherConfig"`
	Enabled           bool           `json:"enabled"`
}

type FieldDefinition struct {
	ID              string    `json:"id"`
	ParserVersionID string    `json:"parserVersionId"`
	Name            string    `json:"name"`
	DisplayName     string    `json:"displayName,omitempty"`
	FieldType       FieldType `json:"fieldType"`
	Indexed         bool      `json:"indexed"`
	Aggregatable    bool      `json:"aggregatable"`
	Nullable        bool      `json:"nullable"`
}

type ReindexJob struct {
	ID               string     `json:"id"`
	SourceID         string     `json:"sourceId"`
	ParserVersionID  string     `json:"parserVersionId"`
	Status           string     `json:"status"`
	EntriesProcessed int64      `json:"entriesProcessed"`
	TotalEntries     int64      `json:"totalEntries"`
	Warnings         int64      `json:"warnings"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
	ErrorMessage     string     `json:"errorMessage,omitempty"`
}

type ExtractionUpdate struct {
	EntryID       int64
	EventType     string
	MatchedRuleID string
	FieldsJSON    string
	ParserVersion int
	WarningCount  int
	ParseStatus   string
	IndexedValues map[string]FieldValue
}

type ExtractionStatistics struct {
	TotalEntries     int64            `json:"totalEntries"`
	MatchedEntries   int64            `json:"matchedEntries"`
	UnmatchedEntries int64            `json:"unmatchedEntries"`
	WarningEntries   int64            `json:"warningEntries"`
	Coverage         float64          `json:"coverage"`
	ByEventType      map[string]int64 `json:"byEventType"`
}

type FieldStatistics struct {
	FieldDefinition
	PresentEntries int64   `json:"presentEntries"`
	TotalEntries   int64   `json:"totalEntries"`
	Coverage       float64 `json:"coverage"`
}

type TypedFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type QuerySort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type StructuredQuery struct {
	Filters []TypedFilter `json:"filters"`
	Sort    []QuerySort   `json:"sort"`
	Limit   int           `json:"limit"`
}

type AggregationRequest struct {
	Operation string        `json:"operation"`
	Field     string        `json:"field,omitempty"`
	GroupBy   string        `json:"groupBy,omitempty"`
	Filters   []TypedFilter `json:"filters"`
}

type AggregationBucket struct {
	Group any     `json:"group,omitempty"`
	Value float64 `json:"value"`
}

type AggregationResult struct {
	Operation string              `json:"operation"`
	Field     string              `json:"field,omitempty"`
	GroupBy   string              `json:"groupBy,omitempty"`
	Buckets   []AggregationBucket `json:"buckets"`
}

type DerivedFieldDefinition struct {
	ID              string    `json:"id"`
	ParserProfileID string    `json:"parserProfileId"`
	Name            string    `json:"name"`
	ResultType      FieldType `json:"resultType"`
	Expression      string    `json:"expression"`
	CreatedAt       time.Time `json:"createdAt"`
}

type SavedAnalysis struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	SourceID          string         `json:"sourceId,omitempty"`
	SourceGroupID     string         `json:"sourceGroupId,omitempty"`
	ConfigurationJSON string         `json:"-"`
	Configuration     map[string]any `json:"configuration"`
	ChartType         string         `json:"chartType"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}
