package youtubeimport

import (
	"log/slog"
	"time"
)

type Options struct {
	Browser          string
	Profile          string
	Limit            int
	WatchLater       bool
	Liked            bool
	Summarize        bool
	Force            bool
	Transcriber      string
	Model            string
	CLI              string
	Length           string
	Timeout          time.Duration
	Logger           *slog.Logger
	YTDLPBinary      string
	WhisperBinary    string
	WhisperModelPath string
	MacWhisperBinary string
	SummarizeBinary  string
}

type Stats struct {
	FeedsProcessed    int `json:"feeds_processed"`
	ItemsProcessed    int `json:"items_processed"`
	ItemsCreated      int `json:"items_created"`
	ItemsDeleted      int `json:"items_deleted"`
	ItemsUpdated      int `json:"items_updated"`
	ItemsUnchanged    int `json:"items_unchanged"`
	ItemsRendered     int `json:"items_rendered"`
	ItemsSkipped      int `json:"items_skipped"`
	SourcesCreated    int `json:"sources_created"`
	LinksCreated      int `json:"links_created"`
	SourcesQueued     int `json:"sources_queued"`
	SourcesDeleted    int `json:"sources_deleted"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}

type feed struct {
	name       string
	sourceType string
	url        string
}

type playlistEnvelope struct {
	ID      string       `json:"id"`
	Title   string       `json:"title"`
	Entries []videoEntry `json:"entries"`
}

type videoEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	WebpageURL  string `json:"webpage_url"`
	Description string `json:"description"`
	Uploader    string `json:"uploader"`
	UploaderID  string `json:"uploader_id"`
	Channel     string `json:"channel"`
	ChannelID   string `json:"channel_id"`
	UploadDate  string `json:"upload_date"`
	Timestamp   int64  `json:"timestamp"`
}

type cleanupStats struct {
	ItemsDeleted   int
	SourcesDeleted int
}
