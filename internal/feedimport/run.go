package feedimport

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func Add(ctx context.Context, cfg config.Config, st *store.Store, rawURL string, opts AddOptions) (store.Feed, bool, Stats, error) {
	opts = normalizeAddOptions(opts)
	enabled := !opts.Disabled
	if opts.Fetch && enabled && !isLikelyFeedURL(rawURL) {
		candidates, ok := DiscoverURL(ctx, rawURL, opts)
		if ok && len(candidates) == 1 {
			rawURL = candidates[0].URL
		}
	}
	normalizedURL, feedKey, err := NormalizeFeedURL(rawURL)
	if err != nil {
		return store.Feed{}, false, Stats{Errors: 1}, err
	}
	feed := store.Feed{
		FeedKey:             feedKey,
		URL:                 rawURL,
		NormalizedURL:       normalizedURL,
		ResolvedURL:         normalizedURL,
		Enabled:             enabled,
		HealthStatus:        store.FeedHealthOK,
		PollIntervalSeconds: int(opts.PollInterval.Seconds()),
		UserTags:            opts.UserTags,
	}
	upsert := store.FeedUpsert{
		FeedKey:             feedKey,
		URL:                 rawURL,
		NormalizedURL:       normalizedURL,
		ResolvedURL:         normalizedURL,
		PollIntervalSeconds: int(opts.PollInterval.Seconds()),
		Enabled:             enabled,
		UserTags:            opts.UserTags,
	}
	result, err := st.UpsertFeed(ctx, upsert)
	if err != nil {
		return store.Feed{}, false, Stats{Errors: 1}, err
	}
	feed.ID = result.FeedID
	if !opts.Fetch {
		stored, getErr := st.GetFeed(ctx, feedKey)
		if getErr == nil {
			feed = stored
		}
		return feed, result.Created, Stats{}, nil
	}

	runStats, err := CheckFeed(ctx, cfg, st, feed, Options{
		Force:               true,
		DefaultPollInterval: opts.DefaultPollInterval,
		Timeout:             opts.Timeout,
		MaxBodyBytes:        opts.MaxBodyBytes,
		UserAgent:           opts.UserAgent,
		Fetcher:             opts.Fetcher,
		Now:                 opts.Now,
		Logger:              opts.Logger,
		MetadataOnly:        !opts.Import,
	})
	stored, getErr := st.GetFeed(ctx, feedKey)
	if getErr == nil {
		feed = stored
	}
	return feed, result.Created, runStats, err
}

func DiscoverURL(ctx context.Context, rawURL string, opts AddOptions) ([]DiscoveryCandidate, bool) {
	opts = normalizeAddOptions(opts)
	fetch, err := opts.Fetcher.Fetch(ctx, store.Feed{
		URL:           rawURL,
		NormalizedURL: rawURL,
		ResolvedURL:   rawURL,
	}, Options{
		Force:        true,
		Timeout:      opts.Timeout,
		MaxBodyBytes: opts.MaxBodyBytes,
		UserAgent:    opts.UserAgent,
		Fetcher:      opts.Fetcher,
		Now:          opts.Now,
		Logger:       opts.Logger,
	})
	if err != nil || len(fetch.DecodedBody) == 0 {
		return nil, false
	}
	if _, err := gofeed.NewParser().Parse(bytes.NewReader(fetch.DecodedBody)); err == nil {
		return []DiscoveryCandidate{{
			URL:  firstNonEmpty(fetch.FinalURL, rawURL),
			Type: "feed",
		}}, true
	}
	candidates, err := DiscoverFromHTML(firstNonEmpty(fetch.FinalURL, rawURL), string(fetch.DecodedBody))
	if err != nil || len(candidates) == 0 {
		return nil, false
	}
	return candidates, true
}

func isLikelyFeedURL(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.Contains(lower, "/feed") ||
		strings.Contains(lower, "/rss") ||
		strings.Contains(lower, "/atom") ||
		strings.HasSuffix(lower, ".rss") ||
		strings.HasSuffix(lower, ".xml") ||
		strings.HasSuffix(lower, ".atom") ||
		strings.Contains(lower, "format=rss") ||
		strings.Contains(lower, "format=atom")
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	opts = normalizeOptions(opts)
	now := opts.Now()
	feeds, err := st.ListFeedsDue(ctx, now, opts.Limit, opts.IncludeBlocked)
	if err != nil {
		return Stats{}, err
	}
	if len(feeds) == 0 {
		return Stats{}, nil
	}

	jobs := make(chan store.Feed)
	results := make(chan Stats)
	var wg sync.WaitGroup
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for feed := range jobs {
				stats, _ := CheckFeed(ctx, cfg, st, feed, opts)
				results <- stats
			}
		}()
	}
	go func() {
	feedLoop:
		for _, feed := range feeds {
			select {
			case <-ctx.Done():
				break feedLoop
			case jobs <- feed:
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var totals Stats
	for stats := range results {
		mergeStats(&totals, stats)
	}
	if ctx.Err() != nil {
		return totals, ctx.Err()
	}
	return totals, nil
}

func CheckFeed(ctx context.Context, cfg config.Config, st *store.Store, feed store.Feed, opts Options) (Stats, error) {
	opts = normalizeOptions(opts)
	now := opts.Now()
	stats := Stats{FeedsChecked: 1}
	result := Result{FeedKey: feed.FeedKey, URL: firstNonEmpty(feed.ResolvedURL, feed.NormalizedURL, feed.URL)}
	fetch, err := opts.Fetcher.Fetch(ctx, feed, opts)
	result.HTTPStatus = fetch.HTTPStatus
	parseStatus := "ok"
	parseErr := ""
	if err != nil {
		parseStatus = "error"
		parseErr = err.Error()
		recordFeedFetch(ctx, st, opts, feedFetchRecord(feed.ID, fetch, now, parseStatus, parseErr))
		next := nextFailureFetchAfter(now, feed, fetch.RetryAfter)
		_ = st.UpdateFeedFailure(ctx, store.FeedFailureState{
			FeedID:         feed.ID,
			HealthStatus:   classifyFeedFailure(feed, fetch.HTTPStatus, now),
			FailureKind:    failureKind(fetch.HTTPStatus),
			LastHTTPStatus: fetch.HTTPStatus,
			Error:          err.Error(),
			FailedAt:       now,
			NextFetchAfter: next,
		})
		stats.FeedsFailed++
		stats.Errors++
		result.Status = "error"
		result.Error = err.Error()
		stats.Results = append(stats.Results, result)
		return stats, err
	}
	if fetch.NotModified || fetch.UnchangedBody {
		recordFeedFetch(ctx, st, opts, feedFetchRecord(feed.ID, fetch, now, "unchanged", ""))
		if err := st.UpdateFeedFetchState(ctx, store.FeedFetchState{
			FeedID:            feed.ID,
			ResolvedURL:       fetch.FinalURL,
			FetchETag:         firstNonEmpty(fetch.ETag, feed.FetchETag),
			FetchLastModified: firstNonEmpty(fetch.LastModified, feed.FetchLastModified),
			FetchBodyHash:     firstNonEmpty(fetch.DecodedBodyHash, feed.FetchBodyHash),
			CheckedAt:         now,
			FetchedAt:         now,
			Changed:           false,
			NextFetchAfter:    nextFetchAfter(now, feed, opts, fetch.RetryAfter),
		}); err != nil {
			stats.Errors++
			return stats, err
		}
		stats.FeedsUnchanged++
		result.Status = "unchanged"
		stats.Results = append(stats.Results, result)
		return stats, nil
	}

	parsed, err := gofeed.NewParser().Parse(bytes.NewReader(fetch.DecodedBody))
	if err != nil {
		parseStatus = "parse_error"
		parseErr = err.Error()
		recordFeedFetch(ctx, st, opts, feedFetchRecord(feed.ID, fetch, now, parseStatus, parseErr))
		_ = st.UpdateFeedFailure(ctx, store.FeedFailureState{
			FeedID:         feed.ID,
			HealthStatus:   store.FeedHealthBlocked,
			FailureKind:    "parse_error",
			LastHTTPStatus: fetch.HTTPStatus,
			Error:          err.Error(),
			FailedAt:       now,
			NextFetchAfter: time.Time{},
		})
		stats.FeedsFailed++
		stats.Errors++
		result.Status = "parse_error"
		result.Error = err.Error()
		stats.Results = append(stats.Results, result)
		return stats, err
	}
	recordFeedFetch(ctx, st, opts, feedFetchRecord(feed.ID, fetch, now, parseStatus, parseErr))

	latestJSON := mustJSON(parsed, "{}")
	if err := st.UpdateFeedFetchState(ctx, store.FeedFetchState{
		FeedID:               feed.ID,
		ResolvedURL:          fetch.FinalURL,
		Title:                strings.TrimSpace(parsed.Title),
		SiteURL:              strings.TrimSpace(parsed.Link),
		Description:          strings.TrimSpace(parsed.Description),
		Language:             strings.TrimSpace(parsed.Language),
		FetchETag:            fetch.ETag,
		FetchLastModified:    fetch.LastModified,
		FetchBodyHash:        fetch.DecodedBodyHash,
		LatestNormalizedJSON: latestJSON,
		CheckedAt:            now,
		FetchedAt:            now,
		Changed:              true,
		NextFetchAfter:       nextFetchAfter(now, feed, opts, fetch.RetryAfter),
	}); err != nil {
		stats.Errors++
		return stats, err
	}
	feed.Title = firstNonEmpty(parsed.Title, feed.Title)
	feed.SiteURL = firstNonEmpty(parsed.Link, feed.SiteURL)
	feed.Description = firstNonEmpty(parsed.Description, feed.Description)
	feed.Language = firstNonEmpty(parsed.Language, feed.Language)
	stats.FeedsChanged++
	result.Status = "changed"
	if opts.MetadataOnly {
		stats.Results = append(stats.Results, result)
		return stats, nil
	}

	for _, item := range parsed.Items {
		entry, ok := buildFeedEntry(feed, parsed, item, now)
		if !ok {
			stats.Errors++
			continue
		}
		stats.EntriesSeen++
		result.EntriesSeen++
		applied, err := st.ApplyFeedEntry(ctx, entry)
		if err != nil {
			stats.Errors++
			result.Error = err.Error()
			continue
		}
		switch {
		case applied.Created:
			stats.ItemsCreated++
			result.ItemsCreated++
			stats.VersionsCreated++
		case applied.Updated:
			stats.ItemsUpdated++
			result.ItemsUpdated++
			stats.VersionsCreated++
		case applied.Unchanged:
			stats.ItemsUnchanged++
			result.ItemsUnchanged++
		}
		if applied.SourceCreated {
			stats.SourcesCreated++
		}
		if applied.SourceLinked {
			stats.SourcesLinked++
		}
		if applied.IdentityConflict {
			stats.IdentityConflicts++
			if opts.Logger != nil {
				opts.Logger.Warn("feed entry identity conflict", "feed_key", feed.FeedKey, "entry_key", entry.EntryKey, "guid", entry.GUID, "normalized_link", entry.NormalizedLink)
			}
		}
		if err := vault.WriteItem(cfg, entry.Item); err != nil {
			stats.Errors++
			result.Error = err.Error()
		} else {
			stats.ItemsRendered++
		}
	}
	stats.Results = append(stats.Results, result)
	return stats, nil
}

func feedFetchRecord(feedID int64, fetch FetchResult, observed time.Time, parseStatus, parseError string) store.FeedFetchRecord {
	return store.FeedFetchRecord{
		FeedID:            feedID,
		ObservedAt:        observed,
		RequestURL:        fetch.RequestURL,
		FinalURL:          fetch.FinalURL,
		HTTPStatus:        fetch.HTTPStatus,
		HeadersJSON:       fetch.HeadersJSON,
		ContentEncoding:   fetch.ContentEncoding,
		DecodedBodyHash:   fetch.DecodedBodyHash,
		WireResponseBytes: fetch.WireResponseBytes,
		DecodedSizeBytes:  fetch.DecodedSizeBytes,
		ParseStatus:       parseStatus,
		ParseError:        parseError,
	}
}

func recordFeedFetch(ctx context.Context, st *store.Store, opts Options, rec store.FeedFetchRecord) {
	if err := st.RecordFeedFetch(ctx, rec); err != nil && opts.Logger != nil {
		opts.Logger.Warn("record feed fetch failed", "feed_id", rec.FeedID, "url", rec.RequestURL, "error", err)
	}
}

func nextFetchAfter(now time.Time, feed store.Feed, opts Options, retryAfter time.Time) time.Time {
	if !retryAfter.IsZero() && retryAfter.After(now) {
		return retryAfter.UTC()
	}
	interval := time.Duration(feed.PollIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = opts.DefaultPollInterval
	}
	return now.Add(interval).UTC()
}

func nextFailureFetchAfter(now time.Time, feed store.Feed, retryAfter time.Time) time.Time {
	if !retryAfter.IsZero() && retryAfter.After(now) {
		return retryAfter.UTC()
	}
	delay := initialBackoff
	for i := 0; i < feed.ErrorCount && delay < maxBackoff; i++ {
		delay *= 2
		if delay >= maxBackoff {
			delay = maxBackoff
			break
		}
	}
	return now.Add(delay).UTC()
}

func classifyFeedFailure(feed store.Feed, status int, now time.Time) string {
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		return store.FeedHealthBlocked
	}
	if terminalLookingFailure(status) {
		firstFailedAt := feed.FirstFailedAt
		if firstFailedAt.IsZero() {
			firstFailedAt = now
		}
		if feed.ErrorCount+1 >= deadFailureCount && !firstFailedAt.After(now.Add(-deadFailureWindow)) {
			return store.FeedHealthDead
		}
	}
	return store.FeedHealthError
}

func terminalLookingFailure(status int) bool {
	return status == http.StatusGone || status == http.StatusNotFound
}

func failureKind(status int) string {
	if status == 0 {
		return "network"
	}
	if status == http.StatusGone || status == http.StatusNotFound {
		return "gone"
	}
	return fmt.Sprintf("http_%d", status)
}

func mergeStats(dst *Stats, src Stats) {
	dst.FeedsChecked += src.FeedsChecked
	dst.FeedsChanged += src.FeedsChanged
	dst.FeedsUnchanged += src.FeedsUnchanged
	dst.FeedsFailed += src.FeedsFailed
	dst.EntriesSeen += src.EntriesSeen
	dst.ItemsCreated += src.ItemsCreated
	dst.ItemsUpdated += src.ItemsUpdated
	dst.ItemsUnchanged += src.ItemsUnchanged
	dst.VersionsCreated += src.VersionsCreated
	dst.SourcesCreated += src.SourcesCreated
	dst.SourcesLinked += src.SourcesLinked
	dst.ItemsRendered += src.ItemsRendered
	dst.IdentityConflicts += src.IdentityConflicts
	dst.Errors += src.Errors
	dst.Results = append(dst.Results, src.Results...)
}
