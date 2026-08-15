package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/linkadd"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

// The SQLite busy handler, not the Go context, is the effective bound for
// admission lock waits. Keep the request context aligned with the store's
// per-connection busy timeout so the two budgets cannot drift by hand.
const linkCaptureAdmissionTimeout = store.LinkCaptureAdmissionBusyTimeout

func (s *server) handleTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req TagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Lookup == "" {
		writeMessage(w, http.StatusBadRequest, "lookup is required")
		return
	}
	item, err := s.store.GetItem(r.Context(), req.Lookup)
	if err == nil {
		if err := s.store.SaveItemUserTags(r.Context(), item.ID, req.Tags); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		item.UserTags = req.Tags
		writeJSON(w, http.StatusOK, itemWebResponse(item))
		return
	}

	source, err := s.store.GetSource(r.Context(), req.Lookup)
	if err != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("item or source not found: %s", req.Lookup))
		return
	}
	if err := s.store.SaveSourceUserTags(r.Context(), source.ID, req.Tags); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	source.UserTags = req.Tags
	writeJSON(w, http.StatusOK, sourceWebResponse(source))
}

func (s *server) handleLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req LinkAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	urls := append([]string{}, req.URLs...)
	if strings.TrimSpace(req.URL) != "" {
		urls = append(urls, req.URL)
	}
	if len(urls) == 0 {
		writeMessage(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.Defer {
		s.handleDeferredLinks(w, r, urls)
		return
	}

	feedURLs, linkURLs, feedCandidates := splitFeedInputs(r.Context(), urls)
	var feedStats feedimport.Stats
	for _, raw := range feedURLs {
		_, _, stats, err := feedimport.Add(r.Context(), s.cfg, s.store, raw, feedimport.AddOptions{
			Enabled: true,
			Fetch:   true,
			Import:  true,
		})
		if err != nil {
			feedStats.Errors++
			continue
		}
		feedStats.FeedsChecked += stats.FeedsChecked
		feedStats.FeedsChanged += stats.FeedsChanged
		feedStats.FeedsUnchanged += stats.FeedsUnchanged
		feedStats.FeedsFailed += stats.FeedsFailed
		feedStats.EntriesSeen += stats.EntriesSeen
		feedStats.ItemsCreated += stats.ItemsCreated
		feedStats.ItemsUpdated += stats.ItemsUpdated
		feedStats.ItemsUnchanged += stats.ItemsUnchanged
		feedStats.VersionsCreated += stats.VersionsCreated
		feedStats.SourcesCreated += stats.SourcesCreated
		feedStats.SourcesLinked += stats.SourcesLinked
		feedStats.ItemsRendered += stats.ItemsRendered
		feedStats.Errors += stats.Errors
		feedStats.Results = append(feedStats.Results, stats.Results...)
	}
	if len(linkURLs) == 0 {
		status := http.StatusOK
		if feedStats.Errors > 0 && feedStats.ItemsCreated+feedStats.ItemsUpdated+feedStats.ItemsUnchanged == 0 {
			status = http.StatusBadRequest
		}
		response := map[string]any{"feeds": feedStats}
		if len(feedCandidates) > 0 {
			response["feed_candidates"] = feedCandidates
		}
		writeJSON(w, status, response)
		return
	}

	stats, err := linkadd.Run(r.Context(), s.cfg, s.store, linkURLs, linkadd.Options{
		Enrich:    req.Enrich,
		Summarize: true,
		CLI:       defaultWebCLI,
		Length:    "medium",
		Timeout:   2 * time.Minute,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if stats.Errors > 0 && stats.Queued == 0 {
		writeJSON(w, http.StatusBadRequest, stats)
		return
	}
	if len(feedURLs) > 0 {
		response := map[string]any{"links": stats, "feeds": feedStats}
		if len(feedCandidates) > 0 {
			response["feed_candidates"] = feedCandidates
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(feedCandidates) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"links": stats, "feed_candidates": feedCandidates})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

type deferredLinkCaptureResult struct {
	URL          string `json:"url"`
	CanonicalURL string `json:"canonical_url,omitempty"`
	SourceKey    string `json:"source_key,omitempty"`
	Queued       bool   `json:"queued"`
	Reopened     bool   `json:"reopened,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (s *server) handleDeferredLinks(w http.ResponseWriter, r *http.Request, urls []string) {
	admissionStarted := time.Now()
	candidates := make([]model.SourceCandidate, 0, len(urls))
	candidateResultIndexes := make([]int, 0, len(urls))
	results := make([]deferredLinkCaptureResult, 0, len(urls))
	for _, raw := range urls {
		candidate, ok := linkextract.NormalizeCandidate(raw)
		if !ok {
			results = append(results, deferredLinkCaptureResult{
				URL: strings.TrimSpace(raw), Error: "unsupported or invalid URL",
			})
			continue
		}
		candidateResultIndexes = append(candidateResultIndexes, len(results))
		candidates = append(candidates, candidate)
		results = append(results, deferredLinkCaptureResult{URL: candidate.OriginalURL})
	}
	if len(candidates) == 0 {
		s.logLinkCaptureAdmission(admissionStarted, "invalid", 0)
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"deferred": true,
			"queued":   0,
			"results":  results,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), linkCaptureAdmissionTimeout)
	defer cancel()
	now := time.Now().UTC()
	queued := 0
	admissionErrors := 0
	for i, candidate := range candidates {
		result := &results[candidateResultIndexes[i]]
		enqueued, err := s.store.EnqueueLinkCapture(ctx, candidate, now)
		if err != nil {
			result.Error = "durable link capture unavailable"
			admissionErrors++
			continue
		}
		result.CanonicalURL = candidate.CanonicalURL
		result.SourceKey = candidate.SourceKey
		result.Queued = true
		result.Reopened = enqueued.Reopened
		queued++
	}

	if queued == 0 {
		outcome := "invalid"
		status := http.StatusBadRequest
		response := map[string]any{
			"deferred": true,
			"queued":   0,
			"results":  results,
		}
		if admissionErrors > 0 {
			outcome = "error"
			status = http.StatusServiceUnavailable
			response["error"] = "durable link capture unavailable"
		}
		s.logLinkCaptureAdmission(admissionStarted, outcome, 0)
		writeJSON(w, status, response)
		return
	}

	s.logLinkCaptureAdmission(admissionStarted, "accepted", queued)
	response := map[string]any{
		"deferred": true,
		"queued":   queued,
		"results":  results,
	}
	if admissionErrors > 0 {
		response["partial"] = true
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *server) logLinkCaptureAdmission(started time.Time, outcome string, queued int) {
	if s == nil || s.logOutput == nil {
		return
	}
	pool := store.PoolStats{}
	if s.store != nil {
		pool = s.store.LinkCaptureAdmissionPoolStats()
	}
	_, _ = fmt.Fprintf(s.logOutput, "DEBUG link capture admission duration=%s outcome=%s queued=%d admission_db_max_open=%d admission_db_open=%d admission_db_in_use=%d admission_db_idle=%d admission_db_wait_count=%d admission_db_wait_duration=%s\n", time.Since(started), outcome, queued, pool.MaxOpenConnections, pool.OpenConnections, pool.InUse, pool.Idle, pool.WaitCount, pool.WaitDuration)
}

func splitFeedInputs(ctx context.Context, urls []string) ([]string, []string, []feedimport.DiscoveryCandidate) {
	var feeds []string
	var links []string
	var candidates []feedimport.DiscoveryCandidate
	for _, raw := range urls {
		if isLikelyFeedURL(raw) {
			feeds = append(feeds, raw)
			continue
		}
		discovered, ok := feedimport.DiscoverURL(ctx, raw, feedimport.AddOptions{Fetch: true})
		if ok {
			if len(discovered) == 1 && discovered[0].Type == "feed" {
				feeds = append(feeds, discovered[0].URL)
				continue
			}
			candidates = append(candidates, discovered...)
			continue
		}
		links = append(links, raw)
	}
	return feeds, links, candidates
}

func isLikelyFeedURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(lower, "/feed") ||
		strings.Contains(lower, "/rss") ||
		strings.Contains(lower, "/atom") ||
		strings.HasSuffix(lower, ".rss") ||
		strings.HasSuffix(lower, ".xml") ||
		strings.HasSuffix(lower, ".atom") ||
		strings.Contains(lower, "format=rss") ||
		strings.Contains(lower, "format=atom")
}
