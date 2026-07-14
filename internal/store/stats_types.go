package store

import "time"

type CountBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type SourceCountFilter struct {
	SourceType    string
	ExtractTool   string
	SummaryStatus string
	ExtractStatus string
}

type ActivityStats struct {
	Now                       time.Time `json:"now"`
	Window                    string    `json:"window"`
	LatestItemUpdatedAt       time.Time `json:"latest_item_updated_at"`
	LatestSourceUpdatedAt     time.Time `json:"latest_source_updated_at"`
	LatestSourceSummaryAt     time.Time `json:"latest_source_summary_at"`
	ItemsUpdatedInWindow      int       `json:"items_updated_in_window"`
	SourcesUpdatedInWindow    int       `json:"sources_updated_in_window"`
	SourcesSummarizedInWindow int       `json:"sources_summarized_in_window"`
}

type BacklogStats struct {
	XHydrationPending             int           `json:"x_hydration_pending"`
	LinkDiscoveryPending          int           `json:"link_discovery_pending"`
	SourceExtractionPending       int           `json:"source_extraction_pending"`
	SourceSummaryPending          int           `json:"source_summary_pending"`
	SourceExtractionPendingByType []CountBucket `json:"source_extraction_pending_by_type"`
	SourceSummaryPendingByType    []CountBucket `json:"source_summary_pending_by_type"`
	Drained                       bool          `json:"drained"`
}

type PipelineStageRow struct {
	Kind           string  `json:"kind"`
	Total          int     `json:"total"`
	Current        int     `json:"current"`
	Pending        int     `json:"pending"`
	Blocked        int     `json:"blocked"`
	Terminal       int     `json:"terminal"`
	Failed         int     `json:"failed"`
	Unknown        int     `json:"unknown"`
	PartitionValid bool    `json:"partition_valid"`
	PercentCurrent float64 `json:"percent_current"`
}

type PipelineStats struct {
	SummaryPromptVersion string             `json:"summary_prompt_version"`
	SummaryTool          string             `json:"summary_tool"`
	SummaryToolVersion   string             `json:"summary_tool_version"`
	Hydration            []PipelineStageRow `json:"hydration"`
	Extraction           []PipelineStageRow `json:"extraction"`
	Summary              []PipelineStageRow `json:"summary"`
	Transcription        []PipelineStageRow `json:"transcription"`
	OCR                  []PipelineStageRow `json:"ocr"`
	MediaArchive         []PipelineStageRow `json:"media_archive"`
}

type SourceActivityEvent struct {
	SourceID     int64     `json:"source_id"`
	SourceKey    string    `json:"source_key"`
	SourceType   string    `json:"source_type"`
	Domain       string    `json:"domain"`
	FailureKind  string    `json:"failure_kind,omitempty"`
	CanonicalURL string    `json:"canonical_url"`
	Title        string    `json:"title"`
	NotePath     string    `json:"note_path"`
	EventKind    string    `json:"event_kind"`
	Status       string    `json:"status"`
	Message      string    `json:"message,omitempty"`
	EventAt      time.Time `json:"event_at"`
}

type SourceFailureHotspot struct {
	Domain        string    `json:"domain"`
	SourceType    string    `json:"source_type"`
	Status        string    `json:"status"`
	FailureKind   string    `json:"failure_kind,omitempty"`
	Count         int       `json:"count"`
	LatestEventAt time.Time `json:"latest_event_at"`
}

type SourceActivityTrendPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	Label        string    `json:"label"`
	SuccessCount int       `json:"success_count"`
	FailureCount int       `json:"failure_count"`
}

type SourceActivityFeed struct {
	Window             string                     `json:"window"`
	RecentSuccesses    []SourceActivityEvent      `json:"recent_successes"`
	RecentFailures     []SourceActivityEvent      `json:"recent_failures"`
	FailureHotspots    []SourceFailureHotspot     `json:"failure_hotspots"`
	FailureKinds       []CountBucket              `json:"failure_kinds"`
	FailureStatuses    []CountBucket              `json:"failure_statuses"`
	FailureDomains     []CountBucket              `json:"failure_domains"`
	FailureTable       []SourceActivityEvent      `json:"failure_table"`
	FailureTableTotal  int                        `json:"failure_table_total"`
	FailureTableOffset int                        `json:"failure_table_offset"`
	FailureTableLimit  int                        `json:"failure_table_limit"`
	FailureTableSort   string                     `json:"failure_table_sort"`
	TrendBucket        string                     `json:"trend_bucket"`
	Trend              []SourceActivityTrendPoint `json:"trend"`
}

type SourceActivityFilter struct {
	Limit         int
	FailureOffset int
	FailureSort   string
	SourceType    string
	Domain        string
	Status        string
	FailureKind   string
	Message       string
	Window        time.Duration
}

const (
	sourceActivityDefaultLimit        = 8
	sourceActivityDefaultWindow       = 24 * time.Hour
	sourceActivityDefaultFacetLimit   = 8
	sourceActivityDefaultHotspotLimit = 8
	sourceActivityDefaultFailureSort  = "newest"
)
