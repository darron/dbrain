package githubimport

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	apiVersion        = "2022-11-28"
	githubSiteName    = "GitHub"
)

type Options struct {
	Limit     int
	Force     bool
	Summarize bool
	Model     string
	CLI       string
	Length    string
	Timeout   time.Duration
	Logger    *slog.Logger
	Token     string
	APIBase   string
	Binary    string
	UserAgent string
}

type Stats struct {
	Viewer             string `json:"viewer"`
	PagesFetched       int    `json:"pages_fetched"`
	StarsProcessed     int    `json:"stars_processed"`
	ItemsCreated       int    `json:"items_created"`
	ItemsUpdated       int    `json:"items_updated"`
	ItemsUnchanged     int    `json:"items_unchanged"`
	ItemsRendered      int    `json:"items_rendered"`
	SourcesCreated     int    `json:"sources_created"`
	LinksCreated       int    `json:"links_created"`
	SourcesQueued      int    `json:"sources_queued"`
	SourcesExtracted   int    `json:"sources_extracted"`
	SourcesSummarized  int    `json:"sources_summarized"`
	SourcesRendered    int    `json:"sources_rendered"`
	SourcesUnchanged   int    `json:"sources_unchanged"`
	HomepageDiscovered int    `json:"homepage_discovered"`
	Errors             int    `json:"errors"`
}

type client struct {
	baseURL    string
	token      string
	userAgent  string
	httpClient *http.Client
}

type viewer struct {
	Login string `json:"login"`
}

type starRecord struct {
	StarredAt string     `json:"starred_at"`
	Repo      repository `json:"repo"`
}

type repository struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	FullName      string      `json:"full_name"`
	HTMLURL       string      `json:"html_url"`
	Description   string      `json:"description"`
	Homepage      string      `json:"homepage"`
	Language      string      `json:"language"`
	Topics        []string    `json:"topics"`
	DefaultBranch string      `json:"default_branch"`
	Private       bool        `json:"private"`
	Archived      bool        `json:"archived"`
	Disabled      bool        `json:"disabled"`
	Fork          bool        `json:"fork"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
	PushedAt      string      `json:"pushed_at"`
	Owner         owner       `json:"owner"`
	License       *licenseRef `json:"license"`
}

type owner struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type licenseRef struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	SPDXID string `json:"spdx_id"`
}

type readmePayload struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	HTMLURL  string `json:"html_url"`
}
