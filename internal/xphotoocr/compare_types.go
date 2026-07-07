package xphotoocr

import "time"

const compareDownloadMaxBytes = 25 << 20

type CompareOptions struct {
	Limit           int
	Models          []string
	Concurrency     int
	Timeout         time.Duration
	DownloadMissing bool
	FOCRBinary      string
	TesseractBinary string
	OpenRouterBase  string
	OpenRouterKey   string
	OpenRouterTitle string
	OpenRouterRef   string
	UserAgent       string
	OllamaBase      string
	OllamaKey       string
}

type CompareResult struct {
	SchemaVersion string                `json:"schema_version"`
	StartedAt     time.Time             `json:"started_at"`
	FinishedAt    time.Time             `json:"finished_at"`
	DurationMS    int64                 `json:"duration_ms"`
	Limit         int                   `json:"limit"`
	Models        []string              `json:"models"`
	Images        []CompareImageResult  `json:"images"`
	Summary       []CompareModelSummary `json:"summary"`
	Errors        int                   `json:"errors"`
}

type CompareImageResult struct {
	Index        int          `json:"index"`
	ItemID       int64        `json:"item_id"`
	SourceKey    string       `json:"source_key"`
	Title        string       `json:"title,omitempty"`
	CanonicalURL string       `json:"canonical_url,omitempty"`
	NotePath     string       `json:"note_path,omitempty"`
	PhotoOrdinal int          `json:"photo_ordinal"`
	LocalPath    string       `json:"local_path"`
	InputPath    string       `json:"input_path,omitempty"`
	InputSource  string       `json:"input_source,omitempty"`
	RemoteURL    string       `json:"remote_url,omitempty"`
	ExpandedURL  string       `json:"expanded_url,omitempty"`
	ExistingOCR  ExistingOCR  `json:"existing_ocr"`
	Runs         []CompareRun `json:"runs"`
}

type ExistingOCR struct {
	Status string `json:"status,omitempty"`
	Model  string `json:"model,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Text   string `json:"text,omitempty"`
}

type CompareRun struct {
	Model                  string  `json:"model"`
	ReportedModel          string  `json:"reported_model,omitempty"`
	Tool                   string  `json:"tool,omitempty"`
	Status                 string  `json:"status"`
	Error                  string  `json:"error,omitempty"`
	Text                   string  `json:"text,omitempty"`
	DurationMS             int64   `json:"duration_ms"`
	CharCount              int     `json:"char_count"`
	LineCount              int     `json:"line_count"`
	WordCount              int     `json:"word_count"`
	BaselineWordOverlap    float64 `json:"baseline_word_overlap,omitempty"`
	BaselineOnlyWordCount  int     `json:"baseline_only_word_count,omitempty"`
	CandidateOnlyWordCount int     `json:"candidate_only_word_count,omitempty"`
}

type CompareModelSummary struct {
	Model                      string  `json:"model"`
	OK                         int     `json:"ok"`
	Errors                     int     `json:"errors"`
	TotalDurationMS            int64   `json:"total_duration_ms"`
	AverageDurationMS          int64   `json:"average_duration_ms"`
	TotalChars                 int     `json:"total_chars"`
	AverageChars               int     `json:"average_chars"`
	AverageBaselineWordOverlap float64 `json:"average_baseline_word_overlap,omitempty"`
}
