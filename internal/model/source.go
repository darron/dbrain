package model

import "time"

type SourceDocument struct {
	ID                   int64     `json:"id"`
	SourceKey            string    `json:"source_key"`
	CanonicalURL         string    `json:"canonical_url"`
	NormalizedURL        string    `json:"normalized_url"`
	SourceType           string    `json:"source_type"`
	Domain               string    `json:"domain"`
	Title                string    `json:"title"`
	Description          string    `json:"description"`
	SiteName             string    `json:"site_name"`
	ExtractedText        string    `json:"extracted_text"`
	ExtractJSON          string    `json:"extract_json"`
	ExtractStatus        string    `json:"extract_status"`
	ExtractError         string    `json:"extract_error"`
	ExtractFailureKind   string    `json:"extract_failure_kind"`
	ExtractFailureCount  int       `json:"extract_failure_count"`
	ExtractFirstFailedAt time.Time `json:"extract_first_failed_at"`
	ExtractLastFailedAt  time.Time `json:"extract_last_failed_at"`
	ExtractedAt          time.Time `json:"extracted_at"`
	ExtractTool          string    `json:"extract_tool"`
	ExtractToolVersion   string    `json:"extract_tool_version"`
	SummaryText          string    `json:"summary_text"`
	SummaryJSON          string    `json:"summary_json"`
	SummaryStatus        string    `json:"summary_status"`
	SummaryError         string    `json:"summary_error"`
	SummaryModel         string    `json:"summary_model"`
	SummaryContentHash   string    `json:"summary_content_hash"`
	SummaryPromptVersion string    `json:"summary_prompt_version"`
	SummaryTool          string    `json:"summary_tool"`
	SummaryToolVersion   string    `json:"summary_tool_version"`
	SummarizedAt         time.Time `json:"summarized_at"`
	SummaryFailedAt      time.Time `json:"summary_failed_at"`
	ContentHash          string    `json:"content_hash"`
	NotePath             string    `json:"note_path"`
	UserTags             string    `json:"user_tags"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type ItemSourceRef struct {
	SourceID      int64  `json:"source_id"`
	SourceKey     string `json:"source_key"`
	CanonicalURL  string `json:"canonical_url"`
	SourceType    string `json:"source_type"`
	Title         string `json:"title"`
	NotePath      string `json:"note_path"`
	ExtractStatus string `json:"extract_status"`
	SummaryStatus string `json:"summary_status"`
	UserTags      string `json:"user_tags"`
}

type SourceBacklink struct {
	ItemID       int64  `json:"item_id"`
	SourceKey    string `json:"source_key"`
	SourceType   string `json:"source_type"`
	CanonicalURL string `json:"canonical_url"`
	Title        string `json:"title"`
	NotePath     string `json:"note_path"`
	AuthorHandle string `json:"author_handle"`
	AuthorName   string `json:"author_name"`
	PublishedAt  string `json:"published_at"`
	UserTags     string `json:"user_tags"`
}

type SourceCandidate struct {
	OriginalURL   string
	CanonicalURL  string
	NormalizedURL string
	SourceType    string
	Domain        string
	SourceKey     string
	NotePath      string
}

type SourceLinkUpsertResult struct {
	SourceID      int64
	SourceCreated bool
	LinkCreated   bool
}

type SourceUpsertResult struct {
	SourceID      int64 `json:"source_id"`
	SourceCreated bool  `json:"source_created"`
}

type ExtractResult struct {
	CanonicalURL string
	FinalURL     string
	Title        string
	Description  string
	SiteName     string
	Content      string
	RawJSON      string
	Status       string
	Error        string
	FetchedAt    time.Time
	Tool         string
	ToolVersion  string
}

type SummaryResult struct {
	Text          string
	RawJSON       string
	Model         string
	PromptVersion string
	Status        string
	Error         string
	FetchedAt     time.Time
	Tool          string
	ToolVersion   string
}

type OCRResult struct {
	Text        string
	RawJSON     string
	Model       string
	Status      string
	Error       string
	FetchedAt   time.Time
	Tool        string
	ToolVersion string
}
