package linkextract

import (
	"log/slog"
	"time"
)

type Options struct {
	DiscoverLimit int
	Limit         int
	Concurrency   int
	Force         bool
	Summarize     bool
	Model         string
	CLI           string
	Length        string
	Timeout       time.Duration
	Logger        *slog.Logger
}

type Stats struct {
	ItemsScanned      int `json:"items_scanned"`
	ItemsMarked       int `json:"items_marked"`
	LinksFound        int `json:"links_found"`
	SourcesCreated    int `json:"sources_created"`
	LinksCreated      int `json:"links_created"`
	SourcesQueued     int `json:"sources_queued"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}
