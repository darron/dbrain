package xmediatranscribe

import (
	"log/slog"
	"time"
)

const transcriptArticleTitle = "X Media Transcript"

type Options struct {
	Limit            int
	Force            bool
	Concurrency      int
	Timeout          time.Duration
	MacWhisperBinary string
	MacWhisperModel  string
	FFprobeBinary    string
	Summarize        bool
	SummaryModel     string
	SummaryCLI       string
	SummaryLength    string
	SummaryLanguage  string
	Logger           *slog.Logger
}

type Stats struct {
	ItemsQueued       int `json:"items_queued"`
	ItemsProcessed    int `json:"items_processed"`
	ItemsUpdated      int `json:"items_updated"`
	ItemsUnchanged    int `json:"items_unchanged"`
	ItemsSkipped      int `json:"items_skipped"`
	MediaCandidates   int `json:"media_candidates"`
	MediaWithAudio    int `json:"media_with_audio"`
	MediaTranscribed  int `json:"media_transcribed"`
	SummaryCandidates int `json:"summary_candidates"`
	ItemsSummarized   int `json:"items_summarized"`
	SummaryErrors     int `json:"summary_errors"`
	Errors            int `json:"errors"`
}

type transcriptBlock struct {
	Heading     string
	RemoteURL   string
	ExpandedURL string
	LocalPath   string
	Text        string
}

type itemTranscriptOutcome struct {
	Status string
	Error  string
}

var transcriptNoiseMarkers = map[string]struct{}{
	"[music]":       {},
	"[blank_audio]": {},
}
