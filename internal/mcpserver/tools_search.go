package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
)

func (s *Server) toolSearch(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Query       string   `json:"query"`
		Limit       int      `json:"limit"`
		SourceTypes []string `json:"source_types"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode search args: %w", err)
	}
	query := strings.TrimSpace(args.Query)
	limit := defaultInt(args.Limit, 10)
	results, err := s.st.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	sourceResults, err := s.st.SearchSources(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	results = append(results, sourceResults...)
	tagAliases := searchTagAliases(query)
	exactTagMatches := make([]researchBucket, 0, len(tagAliases))
	for _, alias := range tagAliases {
		count, err := s.st.CountExactUserTag(ctx, alias, args.SourceTypes)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			exactTagMatches = append(exactTagMatches, researchBucket{Key: alias, Count: count})
			tagResults, err := s.st.SearchExactUserTag(ctx, alias, limit)
			if err != nil {
				return nil, err
			}
			results = append(results, tagResults...)
		}
	}
	results = filterSearchResults(ctx, s.st, results, args.SourceTypes)
	results = dedupeSearchResults(results, limit)
	responseResults, err := s.searchResultsWithMedia(ctx, results)
	if err != nil {
		return nil, err
	}
	content := formatSearchResults(responseResults)
	return toolOKResult(content, map[string]interface{}{
		"results":           responseResults,
		"count":             len(results),
		"tag_aliases":       tagAliases,
		"exact_tag_matches": exactTagMatches,
	}), nil
}

type searchResultWithMedia struct {
	model.SearchResult
	Media []retrieval.MediaRef `json:"media,omitempty"`
}

func (s *Server) searchResultsWithMedia(ctx context.Context, results []model.SearchResult) ([]searchResultWithMedia, error) {
	sourceKeys := make([]string, 0, len(results))
	for _, result := range results {
		sourceKeys = append(sourceKeys, result.SourceKey)
	}
	mediaBySourceKey, err := s.st.ListItemMediaRefsForSourceKeys(ctx, sourceKeys)
	if err != nil {
		return nil, err
	}
	out := make([]searchResultWithMedia, 0, len(results))
	for _, result := range results {
		out = append(out, searchResultWithMedia{
			SearchResult: result,
			Media:        retrieval.MediaRefs(mediaBySourceKey[result.SourceKey]),
		})
	}
	return out, nil
}
