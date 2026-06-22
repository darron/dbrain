package store

import "time"

const (
	ReviewEventKindItemImported         = "item_imported"
	ReviewEventKindItemUpdated          = "item_updated"
	ReviewEventKindSourceCreated        = "source_created"
	ReviewEventKindSourceExtracted      = "source_extracted"
	ReviewEventKindSourceSummarized     = "source_summarized"
	ReviewEventKindItemSummarized       = "item_summarized"
	ReviewEventKindXMediaTranscribed    = "x_media_transcribed"
	ReviewEventKindXPhotoOCRed          = "x_photo_ocred"
	ReviewEventKindSourceFailed         = "source_failed"
	ReviewEventKindItemEnrichmentFailed = "item_enrichment_failed"
	ReviewEventKindBlocked              = "blocked"
)

const (
	ReviewActionabilityReview     = "review"
	ReviewActionabilityBackground = "background"
	ReviewActionabilityBlocked    = "blocked"
	ReviewActionabilityFailure    = "failure"
)

const (
	reviewEventTypeImports     = "imports"
	reviewEventTypeEnrichments = "enrichments"
	reviewEventTypeFailures    = "failures"
	reviewEventTypeAll         = "all"

	reviewEventsDefaultLimit = 50
	reviewEventsMaxLimit     = 500
)

const (
	ReviewEventViewEvents   = "events"
	ReviewEventViewEntities = "entities"
)

var reviewEventImportKinds = []string{
	ReviewEventKindItemImported,
	ReviewEventKindItemUpdated,
	ReviewEventKindSourceCreated,
}

var reviewEventEnrichmentKinds = []string{
	ReviewEventKindSourceExtracted,
	ReviewEventKindSourceSummarized,
	ReviewEventKindItemSummarized,
	ReviewEventKindXMediaTranscribed,
	ReviewEventKindXPhotoOCRed,
}

var reviewEventFailureKinds = []string{
	ReviewEventKindSourceFailed,
	ReviewEventKindItemEnrichmentFailed,
	ReviewEventKindBlocked,
}

var reviewEventAllKinds = append(append(append([]string{}, reviewEventImportKinds...), reviewEventEnrichmentKinds...), reviewEventFailureKinds...)

type ReviewEvent struct {
	EventID       string    `json:"event_id"`
	EventKind     string    `json:"event_kind"`
	EventAt       time.Time `json:"event_at"`
	EntityKind    string    `json:"entity_kind"`
	EntityID      int64     `json:"entity_id"`
	EntityKey     string    `json:"entity_key"`
	EventStage    string    `json:"event_stage"`
	SourceType    string    `json:"source_type"`
	Title         string    `json:"title"`
	URL           string    `json:"url"`
	NotePath      string    `json:"note_path"`
	Summary       string    `json:"summary"`
	Tags          []string  `json:"tags"`
	Status        string    `json:"status"`
	Message       string    `json:"message,omitempty"`
	Actionability string    `json:"actionability"`
	Importance    int       `json:"importance"`
	Reasons       []string  `json:"reasons"`
}

type ReviewCursor struct {
	EventAt    time.Time `json:"event_at"`
	EventKind  string    `json:"event_kind"`
	EntityKind string    `json:"entity_kind"`
	EntityID   int64     `json:"entity_id"`
	EventStage string    `json:"event_stage"`
}

type ReviewEventFilter struct {
	Cursor ReviewCursor
	Limit  int
	Types  []string
	View   string
}

type ReviewEntityEvent struct {
	EventID       string    `json:"event_id"`
	EventKind     string    `json:"event_kind"`
	EventAt       time.Time `json:"event_at"`
	EventStage    string    `json:"event_stage"`
	Status        string    `json:"status"`
	Actionability string    `json:"actionability"`
	Importance    int       `json:"importance"`
	Message       string    `json:"message,omitempty"`
	Reasons       []string  `json:"reasons"`
}

type ReviewEntityGroup struct {
	EntityKind       string              `json:"entity_kind"`
	EntityID         int64               `json:"entity_id"`
	EntityKey        string              `json:"entity_key"`
	SourceType       string              `json:"source_type"`
	Title            string              `json:"title"`
	URL              string              `json:"url"`
	NotePath         string              `json:"note_path"`
	FirstEventAt     time.Time           `json:"first_event_at"`
	LatestEventAt    time.Time           `json:"latest_event_at"`
	EventCount       int                 `json:"event_count"`
	EventKinds       []string            `json:"event_kinds"`
	Summary          string              `json:"summary"`
	SummaryEventID   string              `json:"summary_event_id"`
	SummaryEventKind string              `json:"summary_event_kind"`
	Tags             []string            `json:"tags"`
	Status           string              `json:"status"`
	Message          string              `json:"message,omitempty"`
	Actionability    string              `json:"actionability"`
	Importance       int                 `json:"importance"`
	Reasons          []string            `json:"reasons"`
	Events           []ReviewEntityEvent `json:"events"`
}

type ReviewEventPage struct {
	View          string              `json:"view"`
	Cursor        ReviewCursor        `json:"cursor"`
	NextCursor    string              `json:"next_cursor"`
	HighWatermark time.Time           `json:"high_watermark"`
	Events        []ReviewEvent       `json:"events"`
	Entities      []ReviewEntityGroup `json:"entities"`
	Truncated     bool                `json:"truncated"`
	Counts        []CountBucket       `json:"counts"`
}
