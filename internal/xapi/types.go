package xapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type Options struct {
	Limit       int
	Force       bool
	QuoteOnly   bool
	Concurrency int
	Browser     string
	Profile     string
	CT0         string
	AuthToken   string
	Timeout     time.Duration
	Logger      *slog.Logger
}

type Stats struct {
	Candidates      int `json:"candidates"`
	Requested       int `json:"requested"`
	Hydrated        int `json:"hydrated"`
	Missing         int `json:"missing"`
	APIErrors       int `json:"api_errors"`
	Rendered        int `json:"rendered"`
	Unchanged       int `json:"unchanged"`
	MediaCandidates int `json:"media_candidates"`
	MediaRequested  int `json:"media_requested"`
	MediaDownloaded int `json:"media_downloaded"`
	MediaGone       int `json:"media_gone"`
	MediaErrors     int `json:"media_errors"`
}

type Client struct {
	httpClient   *http.Client
	csrfToken    string
	cookieHeader string
	logger       *slog.Logger
}

type fetchResult struct {
	item      model.Item
	hydration model.XHydration
	requested bool
	err       error
}
