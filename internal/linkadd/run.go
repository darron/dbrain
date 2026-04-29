package linkadd

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

type Options struct {
	Enrich      bool
	Force       bool
	Summarize   bool
	Model       string
	CLI         string
	Length      string
	Timeout     time.Duration
	Concurrency int
	Logger      *slog.Logger
}

type Result struct {
	URL           string `json:"url"`
	SourceID      int64  `json:"source_id,omitempty"`
	SourceKey     string `json:"source_key,omitempty"`
	CanonicalURL  string `json:"canonical_url,omitempty"`
	SourceType    string `json:"source_type,omitempty"`
	Domain        string `json:"domain,omitempty"`
	NotePath      string `json:"note_path,omitempty"`
	SourceCreated bool   `json:"source_created"`
	Queued        bool   `json:"queued"`
	Error         string `json:"error,omitempty"`
}

type Stats struct {
	Submitted         int                `json:"submitted"`
	Queued            int                `json:"queued"`
	SourcesCreated    int                `json:"sources_created"`
	SourcesExisting   int                `json:"sources_existing"`
	SourcesExtracted  int                `json:"sources_extracted"`
	SourcesSummarized int                `json:"sources_summarized"`
	SourcesRendered   int                `json:"sources_rendered"`
	SourcesUnchanged  int                `json:"sources_unchanged"`
	Errors            int                `json:"errors"`
	Results           []Result           `json:"results"`
	Enrichment        sourceenrich.Stats `json:"enrichment,omitempty"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, urls []string, opts Options) (Stats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.Length == "" {
		opts.Length = "medium"
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}

	stats := Stats{Submitted: len(urls)}
	sourceIDs := make([]int64, 0, len(urls))
	seen := map[int64]struct{}{}
	for _, raw := range urls {
		result := addOne(ctx, st, raw)
		stats.Results = append(stats.Results, result)
		if result.Error != "" {
			stats.Errors++
			continue
		}
		stats.Queued++
		if result.SourceCreated {
			stats.SourcesCreated++
		} else {
			stats.SourcesExisting++
		}
		if _, ok := seen[result.SourceID]; ok {
			continue
		}
		seen[result.SourceID] = struct{}{}
		sourceIDs = append(sourceIDs, result.SourceID)
		debugLog(opts.Logger, "manual link queued", "source_id", result.SourceID, "source_key", result.SourceKey, "url", result.CanonicalURL, "created", result.SourceCreated)
	}

	if opts.Enrich && len(sourceIDs) > 0 {
		enrichStats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, sourceIDs, sourceenrich.Options{
			Limit:       len(sourceIDs),
			Concurrency: opts.Concurrency,
			Force:       opts.Force,
			Summarize:   opts.Summarize,
			Model:       opts.Model,
			CLI:         opts.CLI,
			Length:      opts.Length,
			Timeout:     opts.Timeout,
			Logger:      opts.Logger,
		})
		stats.Enrichment = enrichStats
		stats.SourcesExtracted = enrichStats.SourcesExtracted
		stats.SourcesSummarized = enrichStats.SourcesSummarized
		stats.SourcesRendered = enrichStats.SourcesRendered
		stats.SourcesUnchanged = enrichStats.SourcesUnchanged
		stats.Errors += enrichStats.Errors
		if err != nil {
			return stats, err
		}
	}

	return stats, nil
}

func addOne(ctx context.Context, st *store.Store, raw string) Result {
	trimmed := strings.TrimSpace(raw)
	result := Result{URL: trimmed}
	candidate, ok := linkextract.NormalizeCandidate(trimmed)
	if !ok {
		result.Error = "unsupported or invalid URL"
		return result
	}
	upserted, err := st.UpsertSource(ctx, candidate)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.SourceID = upserted.SourceID
	result.SourceKey = candidate.SourceKey
	result.CanonicalURL = candidate.CanonicalURL
	result.SourceType = candidate.SourceType
	result.Domain = candidate.Domain
	result.NotePath = candidate.NotePath
	result.SourceCreated = upserted.SourceCreated
	result.Queued = true
	return result
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
