package retrieval

type ContentSection struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status,omitempty"`
	Model     string `json:"model,omitempty"`
	Tool      string `json:"tool,omitempty"`
	At        string `json:"at,omitempty"`
	Chars     int    `json:"chars"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated"`
}

type EvidenceDocument struct {
	SourceKey     string         `json:"source_key"`
	Kind          string         `json:"kind"`
	Title         string         `json:"title"`
	URL           string         `json:"url"`
	NotePath      string         `json:"note_path"`
	Summary       string         `json:"summary"`
	Excerpt       string         `json:"excerpt"`
	Author        string         `json:"author,omitempty"`
	SourceType    string         `json:"source_type,omitempty"`
	PublishedAt   string         `json:"published_at,omitempty"`
	ExtractedAt   string         `json:"extracted_at,omitempty"`
	SummarizedAt  string         `json:"summarized_at,omitempty"`
	UserTags      string         `json:"user_tags,omitempty"`
	EntityMatches []string       `json:"entity_matches,omitempty"`
	RelatedTo     string         `json:"related_to,omitempty"`
	Relationship  string         `json:"relationship,omitempty"`
	Media         []MediaRef     `json:"media,omitempty"`
	Retrieval     *RetrievalInfo `json:"retrieval,omitempty"`
}

type MediaRef struct {
	MediaAssetID   int64  `json:"media_asset_id"`
	Ordinal        int    `json:"ordinal"`
	ExpandedURL    string `json:"expanded_url,omitempty"`
	RemoteURL      string `json:"remote_url,omitempty"`
	MediaType      string `json:"media_type"`
	DownloadStatus string `json:"download_status,omitempty"`
	ArchiveURL     string `json:"archive_url,omitempty"`
	ArchiveStatus  string `json:"archive_status,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
}

type RelatedDocument struct {
	ID                     int64  `json:"id"`
	SourceKey              string `json:"source_key"`
	SourceType             string `json:"source_type"`
	ExternalID             string `json:"external_id"`
	CanonicalURL           string `json:"canonical_url"`
	Title                  string `json:"title"`
	AuthorHandle           string `json:"author_handle"`
	AuthorName             string `json:"author_name"`
	PublishedAt            string `json:"published_at"`
	SavedAt                string `json:"saved_at"`
	Language               string `json:"language"`
	PrimaryCategory        string `json:"primary_category"`
	PrimaryDomain          string `json:"primary_domain"`
	NotePath               string `json:"note_path"`
	UserTags               string `json:"user_tags"`
	XPostStatus            string `json:"x_post_status"`
	SummaryStatus          string `json:"summary_status"`
	SummaryModel           string `json:"summary_model"`
	SummaryTool            string `json:"summary_tool"`
	OCRStatus              string `json:"ocr_status"`
	OCRModel               string `json:"ocr_model"`
	OCRTool                string `json:"ocr_tool"`
	XMediaTranscriptStatus string `json:"x_media_transcript_status"`
	ImportedAt             string `json:"imported_at"`
	UpdatedAt              string `json:"updated_at"`
	LastSeenAt             string `json:"last_seen_at"`
}

type RetrievalInfo struct {
	Score        int               `json:"score"`
	Signals      []RetrievalSignal `json:"signals,omitempty"`
	MatchedTerms []string          `json:"matched_terms,omitempty"`
	MissingTerms []string          `json:"missing_terms,omitempty"`
}

type RetrievalSignal struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Weight int    `json:"weight"`
}
