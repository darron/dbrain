package feedimport

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

const (
	DefaultPollInterval = time.Hour
	DefaultTimeout      = 30 * time.Second
	DefaultMaxBodyBytes = 10 << 20
	DefaultConcurrency  = 6
	DefaultUserAgent    = "dbrain feed importer"
	initialBackoff      = 15 * time.Minute
	maxBackoff          = 24 * time.Hour
	deadFailureCount    = 5
	deadFailureWindow   = 24 * time.Hour
)

var ExtensionContentHashWhitelist = map[string]map[string]bool{
	"content": {
		"encoded": true,
	},
	"dc": {
		"creator": true,
	},
	"media": {
		"content":   true,
		"thumbnail": true,
	},
	"markdown": {
		"content": true,
	},
}

type Fetcher interface {
	Fetch(ctx context.Context, feed store.Feed, opts Options) (FetchResult, error)
}

type Options struct {
	Limit               int
	Force               bool
	MetadataOnly        bool
	IncludeBlocked      bool
	Concurrency         int
	DefaultPollInterval time.Duration
	Timeout             time.Duration
	MaxBodyBytes        int64
	UserAgent           string
	Client              *http.Client
	Fetcher             Fetcher
	Now                 func() time.Time
	Logger              *slog.Logger
}

type AddOptions struct {
	Enabled             bool
	Disabled            bool
	Import              bool
	PollInterval        time.Duration
	UserTags            string
	Fetch               bool
	DefaultPollInterval time.Duration
	Timeout             time.Duration
	MaxBodyBytes        int64
	UserAgent           string
	Client              *http.Client
	Fetcher             Fetcher
	Now                 func() time.Time
	Logger              *slog.Logger
}

type Stats struct {
	FeedsChecked      int      `json:"feeds_checked"`
	FeedsChanged      int      `json:"feeds_changed"`
	FeedsUnchanged    int      `json:"feeds_unchanged"`
	FeedsFailed       int      `json:"feeds_failed"`
	EntriesSeen       int      `json:"entries_seen"`
	ItemsCreated      int      `json:"items_created"`
	ItemsUpdated      int      `json:"items_updated"`
	ItemsUnchanged    int      `json:"items_unchanged"`
	VersionsCreated   int      `json:"versions_created"`
	SourcesCreated    int      `json:"sources_created"`
	SourcesLinked     int      `json:"sources_linked"`
	ItemsRendered     int      `json:"items_rendered"`
	IdentityConflicts int      `json:"identity_conflicts"`
	Errors            int      `json:"errors"`
	Results           []Result `json:"results,omitempty"`
}

type Result struct {
	FeedKey        string `json:"feed_key"`
	URL            string `json:"url"`
	Status         string `json:"status"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	EntriesSeen    int    `json:"entries_seen"`
	ItemsCreated   int    `json:"items_created"`
	ItemsUpdated   int    `json:"items_updated"`
	ItemsUnchanged int    `json:"items_unchanged"`
	Error          string `json:"error,omitempty"`
}

type FetchResult struct {
	RequestURL        string
	FinalURL          string
	HTTPStatus        int
	HeadersJSON       string
	ContentEncoding   string
	DecodedBody       []byte
	DecodedBodyHash   string
	WireResponseBytes []byte
	DecodedSizeBytes  int64
	ETag              string
	LastModified      string
	RetryAfter        time.Time
	NotModified       bool
	UnchangedBody     bool
}

func normalizeOptions(opts Options) Options {
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.DefaultPollInterval <= 0 {
		opts.DefaultPollInterval = DefaultPollInterval
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Fetcher == nil {
		opts.Fetcher = NewHTTPFetcher(opts.Client)
	}
	return opts
}

func normalizeAddOptions(opts AddOptions) AddOptions {
	if opts.DefaultPollInterval <= 0 {
		opts.DefaultPollInterval = DefaultPollInterval
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = opts.DefaultPollInterval
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Fetcher == nil {
		opts.Fetcher = NewHTTPFetcher(opts.Client)
	}
	return opts
}

type RunFunc func(context.Context, config.Config, *store.Store, Options) (Stats, error)
