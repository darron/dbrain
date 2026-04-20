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
	"sort"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
	"dbrain/internal/vault"
)

const SummaryPromptVersion = "dbrain-v1"

const summaryPrompt = `Summarize this source for a local second-brain knowledge base.
Focus on durable knowledge, concrete facts, named entities, tools, libraries, APIs, claims, and actionable takeaways.
Use Markdown with exactly these headings:
### What It Is
### Key Ideas
### Why It Matters
### Entities
### Follow-ups
Keep it factual and concise.
Use bullets only in Entities and Follow-ups.
Do not mention ads, sponsors, or irrelevant boilerplate.`

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
	touchedSourceIDs := map[int64]struct{}{}
	toolVersion := summarizecli.Version(ctx, "")

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
			touchedSourceIDs[result.SourceID] = struct{}{}
		}
		if err := st.MarkItemLinkDiscovery(ctx, item.ID, time.Now().UTC()); err != nil {
			return stats, err
		}
		stats.ItemsMarked++
	}

	sources, err := st.ListSourcesForEnrichment(ctx, opts.Limit, opts.Force, opts.Summarize, SummaryPromptVersion, summarizecli.ToolName, toolVersion)
	if err != nil {
		return stats, err
	}
	stats.SourcesQueued = len(sources)
	debugLog(opts.Logger, "source enrichment candidates loaded", "sources", len(sources), "limit", opts.Limit, "summarize", opts.Summarize)

	for _, source := range sources {
		debugLog(opts.Logger, "enriching source", "source_key", source.SourceKey, "url", source.CanonicalURL)
		localExtract, hasLocalExtract, err := st.GetPreferredLocalSourceExtract(ctx, source.ID)
		if err != nil {
			return stats, err
		}
		if hasLocalExtract {
			debugLog(opts.Logger, "using local cached extract", "source_key", source.SourceKey, "url", source.CanonicalURL, "content_chars", len(localExtract.Content))
			contentHash := hashText(localExtract.Content)
			if changed, err := st.SaveSourceExtraction(ctx, source.ID, localExtract, contentHash); err != nil {
				return stats, err
			} else if changed {
				stats.SourcesExtracted++
			} else {
				stats.SourcesUnchanged++
			}

			if opts.Summarize {
				runResult, err := summarizecli.Run(ctx, summarizecli.Options{
					Input:     "-",
					Stdin:     localExtract.Content,
					Summarize: true,
					Model:     opts.Model,
					CLI:       opts.CLI,
					Prompt:    buildSummaryPrompt(source, localExtract),
					Length:    opts.Length,
					Timeout:   opts.Timeout,
				})
				if err != nil {
					stats.Errors++
					debugLog(opts.Logger, "local source summarization failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
					if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
						Status:        "error",
						Error:         err.Error(),
						Model:         opts.Model,
						PromptVersion: SummaryPromptVersion,
						Tool:          summarizecli.ToolName,
						ToolVersion:   toolVersion,
					}); saveErr != nil {
						return stats, saveErr
					}
					touchedSourceIDs[source.ID] = struct{}{}
					continue
				}
				runResult.Summary.PromptVersion = SummaryPromptVersion
				if changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary); err != nil {
					return stats, err
				} else if changed && runResult.Summary.Status == "ok" {
					stats.SourcesSummarized++
				}
			}

			touchedSourceIDs[source.ID] = struct{}{}
			continue
		}

		runResult, err := summarizecli.Run(ctx, summarizecli.Options{
			Input:     source.CanonicalURL,
			Summarize: opts.Summarize,
			Model:     opts.Model,
			CLI:       opts.CLI,
			Prompt:    summaryPrompt,
			Length:    opts.Length,
			Timeout:   opts.Timeout,
		})
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "source enrichment failed", "source_key", source.SourceKey, "url", source.CanonicalURL, "error", err.Error())
			if _, saveErr := st.SaveSourceExtraction(ctx, source.ID, model.ExtractResult{
				Status:      "error",
				Error:       err.Error(),
				Tool:        summarizecli.ToolName,
				ToolVersion: toolVersion,
			}, source.ContentHash); saveErr != nil {
				return stats, saveErr
			}
			if opts.Summarize {
				if _, saveErr := st.SaveSourceSummary(ctx, source.ID, model.SummaryResult{
					Status:        "error",
					Error:         err.Error(),
					Model:         opts.Model,
					PromptVersion: SummaryPromptVersion,
					Tool:          summarizecli.ToolName,
					ToolVersion:   toolVersion,
				}); saveErr != nil {
					return stats, saveErr
				}
			}
			touchedSourceIDs[source.ID] = struct{}{}
			continue
		}

		contentHash := hashText(runResult.Extract.Content)
		if changed, err := st.SaveSourceExtraction(ctx, source.ID, runResult.Extract, contentHash); err != nil {
			return stats, err
		} else if changed {
			stats.SourcesExtracted++
		} else {
			stats.SourcesUnchanged++
		}

		if opts.Summarize {
			runResult.Summary.PromptVersion = SummaryPromptVersion
			if changed, err := st.SaveSourceSummary(ctx, source.ID, runResult.Summary); err != nil {
				return stats, err
			} else if changed && runResult.Summary.Status == "ok" {
				stats.SourcesSummarized++
			}
		}

		touchedSourceIDs[source.ID] = struct{}{}
	}

	orderedSourceIDs := make([]int64, 0, len(touchedSourceIDs))
	for sourceID := range touchedSourceIDs {
		orderedSourceIDs = append(orderedSourceIDs, sourceID)
	}
	sort.Slice(orderedSourceIDs, func(i, j int) bool { return orderedSourceIDs[i] < orderedSourceIDs[j] })

	for _, sourceID := range orderedSourceIDs {
		source, err := st.GetSourceByID(ctx, sourceID)
		if err != nil {
			return stats, err
		}
		backlinks, err := st.ListBacklinksForSource(ctx, sourceID)
		if err != nil {
			return stats, err
		}
		if err := vault.WriteSource(cfg, source, backlinks); err != nil {
			return stats, err
		}
		stats.SourcesRendered++
	}

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

func buildSummaryPrompt(source model.SourceDocument, extract model.ExtractResult) string {
	var b strings.Builder
	b.WriteString(summaryPrompt)

	contextLines := make([]string, 0, 3)
	if value := strings.TrimSpace(source.CanonicalURL); value != "" {
		contextLines = append(contextLines, "Source URL: "+value)
	}
	title := strings.TrimSpace(extract.Title)
	if title == "" {
		title = strings.TrimSpace(source.Title)
	}
	if title != "" {
		contextLines = append(contextLines, "Source Title: "+title)
	}
	site := strings.TrimSpace(extract.SiteName)
	if site == "" {
		site = strings.TrimSpace(source.SiteName)
	}
	if site == "" {
		site = strings.TrimSpace(source.Domain)
	}
	if site != "" {
		contextLines = append(contextLines, "Source Site: "+site)
	}

	if len(contextLines) == 0 {
		return b.String()
	}

	b.WriteString("\n\nAdditional context:\n")
	for _, line := range contextLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
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

func hashText(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
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
