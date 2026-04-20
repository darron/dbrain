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
	ContentHash          string    `json:"content_hash"`
	NotePath             string    `json:"note_path"`
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
}

type SourceBacklink struct {
	ItemID       int64  `json:"item_id"`
	SourceKey    string `json:"source_key"`
	CanonicalURL string `json:"canonical_url"`
	Title        string `json:"title"`
	NotePath     string `json:"note_path"`
	AuthorHandle string `json:"author_handle"`
	AuthorName   string `json:"author_name"`
	PublishedAt  string `json:"published_at"`
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
