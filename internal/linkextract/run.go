package linkextract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	neturl "net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

type Options struct {
	DiscoverLimit int
	Limit         int
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

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.DiscoverLimit <= 0 {
		opts.DiscoverLimit = 500
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}

	stats := Stats{}

	items, err := st.ListItemsForLinkDiscovery(ctx, opts.DiscoverLimit, opts.Force)
	if err != nil {
		return stats, err
	}
	debugLog(opts.Logger, "link discovery candidates loaded", "items", len(items), "limit", opts.DiscoverLimit, "force", opts.Force)

	for _, item := range items {
		candidates, err := collectCandidates(item)
		if err != nil {
			return stats, fmt.Errorf("collect link candidates for %s: %w", item.SourceKey, err)
		}
		stats.ItemsScanned++
		stats.LinksFound += len(candidates)
		debugLog(opts.Logger, "link discovery item", "source_key", item.SourceKey, "candidate_count", len(candidates))
		for _, candidate := range candidates {
			result, err := st.UpsertSourceLink(ctx, item.ID, candidate)
			if err != nil {
				return stats, fmt.Errorf("upsert source link %s for %s: %w", candidate.CanonicalURL, item.SourceKey, err)
			}
			if result.SourceCreated {
				stats.SourcesCreated++
			}
			if result.LinkCreated {
				stats.LinksCreated++
			}
		}
		if err := st.MarkItemLinkDiscovery(ctx, item.ID, time.Now().UTC()); err != nil {
			return stats, err
		}
		stats.ItemsMarked++
	}

	enrichStats, _, err := sourceenrich.RunPending(ctx, cfg, st, sourceenrich.Options{
		Limit:     opts.Limit,
		Force:     opts.Force,
		Summarize: opts.Summarize,
		Model:     opts.Model,
		CLI:       opts.CLI,
		Length:    opts.Length,
		Timeout:   opts.Timeout,
		Logger:    opts.Logger,
	})
	if err != nil {
		return stats, err
	}

	stats.SourcesQueued = enrichStats.SourcesQueued
	stats.SourcesExtracted = enrichStats.SourcesExtracted
	stats.SourcesSummarized = enrichStats.SourcesSummarized
	stats.SourcesRendered = enrichStats.SourcesRendered
	stats.SourcesUnchanged = enrichStats.SourcesUnchanged
	stats.Errors = enrichStats.Errors

	return stats, nil
}

func collectCandidates(item model.Item) ([]model.SourceCandidate, error) {
	var rawLinks []string
	if err := json.Unmarshal([]byte(item.LinksJSON), &rawLinks); err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	candidates := make([]model.SourceCandidate, 0, len(rawLinks))
	for _, raw := range rawLinks {
		candidate, ok := normalizeCandidate(raw)
		if !ok {
			continue
		}
		if _, exists := seen[candidate.NormalizedURL]; exists {
			continue
		}
		seen[candidate.NormalizedURL] = struct{}{}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func normalizeCandidate(raw string) (model.SourceCandidate, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return model.SourceCandidate{}, false
	}

	u, err := neturl.Parse(trimmed)
	if err != nil || u.Host == "" {
		return model.SourceCandidate{}, false
	}

	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "twitter.com", "mobile.twitter.com":
		host = "x.com"
	}

	sourceType := classifySourceType(host, u.Path)
	if sourceType == "skip" {
		return model.SourceCandidate{}, false
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	if scheme != "http" && scheme != "https" {
		return model.SourceCandidate{}, false
	}
	if host != "localhost" && net.ParseIP(host) == nil {
		scheme = "https"
	}

	cleanedPath := path.Clean("/" + strings.TrimSpace(u.EscapedPath()))
	if cleanedPath == "/." {
		cleanedPath = "/"
	}
	u = &neturl.URL{
		Scheme: scheme,
		Host:   host,
		Path:   cleanedPath,
	}

	query := filterQueryParams(trimmed, host)
	u.RawQuery = query.Encode()
	u.Fragment = ""

	canonical := strings.TrimSuffix(u.String(), "?")
	if host == "github.com" {
		canonical = trimGitHubURL(canonical)
	}

	keyHash := shortHash(canonical)
	slugBase := host
	if slugBase == "" {
		slugBase = sourceType
	}
	noteSlug := slugify(slugBase + "-" + keyHash)

	return model.SourceCandidate{
		OriginalURL:   trimmed,
		CanonicalURL:  canonical,
		NormalizedURL: canonical,
		SourceType:    sourceType,
		Domain:        host,
		SourceKey:     "src:" + keyHash,
		NotePath:      vault.SourceNoteRelativePath(sourceType, noteSlug),
	}, true
}

func classifySourceType(host, urlPath string) string {
	switch host {
	case "t.co", "pbs.twimg.com", "video.twimg.com":
		return "skip"
	case "x.com":
		if strings.HasPrefix(urlPath, "/i/article/") {
			return "x_article"
		}
		return "skip"
	case "github.com":
		return "github"
	case "youtu.be", "youtube.com", "www.youtube.com":
		return "youtube"
	}
	if strings.HasSuffix(strings.ToLower(urlPath), ".pdf") {
		return "pdf"
	}
	return "web"
}

func filterQueryParams(raw, host string) neturl.Values {
	u, err := neturl.Parse(raw)
	if err != nil {
		return neturl.Values{}
	}
	values := u.Query()
	filtered := neturl.Values{}
	for key, vals := range values {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" || lower == "igshid" || lower == "ref_src" || lower == "si" {
			continue
		}
		if host == "youtube.com" || host == "youtu.be" || host == "www.youtube.com" {
			if lower == "t" || lower == "start" || lower == "feature" {
				continue
			}
		}
		for _, value := range vals {
			filtered.Add(key, value)
		}
	}
	return filtered
}

func trimGitHubURL(value string) string {
	u, err := neturl.Parse(value)
	if err != nil {
		return value
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 {
		u.Path = "/" + parts[0] + "/" + parts[1]
		u.RawQuery = ""
		u.Fragment = ""
	}
	return u.String()
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonSlug.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "source"
	}
	if len(value) > 80 {
		return strings.Trim(value[:80], "-")
	}
	return value
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
