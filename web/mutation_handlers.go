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
)

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

	feedURLs, linkURLs, feedCandidates := splitFeedInputs(r.Context(), urls)
	var feedStats feedimport.Stats
	for _, raw := range feedURLs {
		_, _, stats, err := feedimport.Add(r.Context(), s.cfg, s.store, raw, feedimport.AddOptions{
			Enabled: true,
			Fetch:   true,
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
