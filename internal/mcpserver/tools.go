package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode tools/call params: %w", err)
	}

	switch params.Name {
	case "dbrain_search":
		return s.toolSearch(ctx, params.Arguments)
	case "dbrain_get":
		return s.toolGet(ctx, params.Arguments)
	case "dbrain_get_many":
		return s.toolGetMany(ctx, params.Arguments)
	case "dbrain_research_pack":
		return s.toolResearchPack(ctx, params.Arguments)
	case "dbrain_entity_map":
		return s.toolEntityMap(ctx, params.Arguments)
	case "dbrain_topic_map":
		return s.toolTopicMap(ctx, params.Arguments)
	case "dbrain_topic_brief":
		return s.toolTopicBrief(ctx, params.Arguments)
	case "dbrain_related":
		return s.toolRelated(ctx, params.Arguments)
	case "dbrain_stats_items":
		return s.toolStatsItems(ctx, params.Arguments)
	case "dbrain_stats_sources":
		return s.toolStatsSources(ctx, params.Arguments)
	case "dbrain_stats_activity":
		return s.toolStatsActivity(ctx, params.Arguments)
	case "dbrain_stats_backlog":
		return s.toolStatsBacklog(ctx)
	default:
		return nil, fmt.Errorf("unknown tool %q", params.Name)
	}
}

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
	content := formatSearchResults(s.cfg, results)
	return toolOKResult(content, map[string]interface{}{
		"results":           results,
		"count":             len(results),
		"tag_aliases":       tagAliases,
		"exact_tag_matches": exactTagMatches,
	}), nil
}

func (s *Server) toolGet(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookup             string `json:"lookup"`
		Query              string `json:"query"`
		ContentMode        string `json:"content_mode"`
		MaxCharsPerSection int    `json:"max_chars_per_section"`
		IncludeContent     *bool  `json:"include_content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode get args: %w", err)
	}
	contentMode, err := resolveGetContentMode(args.ContentMode, args.IncludeContent)
	if err != nil {
		return nil, err
	}
	maxChars := maxGetSectionChars(args.MaxCharsPerSection)

	payload, text, err := s.getPayloadForLookup(ctx, args.Lookup, contentMode, maxChars, args.Query)
	if err != nil {
		return nil, err
	}
	return toolOKResult(text, payload), nil
}

func (s *Server) toolGetMany(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookups            []string `json:"lookups"`
		Query              string   `json:"query"`
		ContentMode        string   `json:"content_mode"`
		MaxCharsPerSection int      `json:"max_chars_per_section"`
		IncludeContent     *bool    `json:"include_content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode get_many args: %w", err)
	}
	contentMode, err := resolveGetContentMode(args.ContentMode, args.IncludeContent)
	if err != nil {
		return nil, err
	}
	maxChars := maxGetSectionChars(args.MaxCharsPerSection)

	lookups := uniqueGetLookups(args.Lookups)
	if len(lookups) == 0 {
		return nil, fmt.Errorf("lookups is required")
	}
	if len(lookups) > maxGetManyLookups {
		return nil, fmt.Errorf("too many lookups: got %d, maximum is %d", len(lookups), maxGetManyLookups)
	}

	results := make([]map[string]interface{}, 0, len(lookups))
	errors := make([]getManyError, 0)
	texts := make([]string, 0, len(lookups))
	for _, lookup := range lookups {
		payload, text, err := s.getPayloadForLookup(ctx, lookup, contentMode, maxChars, args.Query)
		if err != nil {
			errors = append(errors, getManyError{Lookup: lookup, Error: err.Error()})
			continue
		}
		payload["lookup"] = lookup
		results = append(results, payload)
		texts = append(texts, text)
	}

	payload := map[string]interface{}{
		"lookups":               lookups,
		"content_mode":          contentMode,
		"max_chars_per_section": maxChars,
		"count":                 len(results),
		"results":               results,
		"errors":                errors,
	}
	if query := strings.TrimSpace(args.Query); query != "" {
		payload["query"] = query
	}
	return toolOKResult(formatGetManyPayload(payload, texts), payload), nil
}

func (s *Server) toolEntityMap(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode entity map args: %w", err)
	}
	results, err := entities.Search(ctx, s.st, strings.TrimSpace(args.Query), entities.SearchOptions{
		Kind:  args.Kind,
		Limit: defaultInt(args.Limit, 20),
	})
	if err != nil {
		return nil, err
	}
	return toolOKResult(entities.FormatText(results), map[string]interface{}{
		"count":    len(results),
		"entities": results,
	}), nil
}

func (s *Server) toolTopicMap(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Topic        string   `json:"topic"`
		SourceTypes  []string `json:"source_types"`
		SeedLimit    int      `json:"seed_limit"`
		RelatedLimit int      `json:"related_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode topic map args: %w", err)
	}
	graph, err := topics.Build(ctx, s.st, args.Topic, topics.Options{
		SourceTypes:  args.SourceTypes,
		SeedLimit:    args.SeedLimit,
		RelatedLimit: args.RelatedLimit,
	})
	if err != nil {
		return nil, err
	}
	return toolOKResult(topics.FormatText(graph), graph), nil
}

func (s *Server) toolTopicBrief(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Topic        string   `json:"topic"`
		SourceTypes  []string `json:"source_types"`
		SeedLimit    int      `json:"seed_limit"`
		RelatedLimit int      `json:"related_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode topic brief args: %w", err)
	}
	graph, err := topics.Build(ctx, s.st, args.Topic, topics.Options{
		SourceTypes:  args.SourceTypes,
		SeedLimit:    args.SeedLimit,
		RelatedLimit: args.RelatedLimit,
	})
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"topic":         graph.Topic,
		"source_types":  graph.SourceTypes,
		"seed_limit":    graph.SeedLimit,
		"related_limit": graph.RelatedLimit,
		"summary":       topics.SummaryText(graph),
		"synthesis":     graph.Synthesis,
		"pivots":        graph.Pivots,
		"entities":      graph.Entities,
		"nodes":         graph.Nodes,
		"edges":         graph.Edges,
		"markdown":      vault.RenderTopic(graph),
	}
	return toolOKResult(payload["markdown"].(string), payload), nil
}

func (s *Server) toolRelated(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookup string `json:"lookup"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode related args: %w", err)
	}
	lookup := strings.TrimSpace(args.Lookup)
	if lookup == "" {
		return nil, fmt.Errorf("lookup is required")
	}

	if item, err := s.st.GetItem(ctx, lookup); err == nil {
		related, err := s.st.ListSourcesForItem(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		childIDs, err := s.st.ListItemChildLinks(ctx, item.ID, "quoted_post")
		if err != nil {
			return nil, err
		}
		relatedItems := make([]map[string]interface{}, 0, len(childIDs))
		for _, childID := range childIDs {
			child, err := s.st.GetItemByID(ctx, childID)
			if err != nil {
				continue
			}
			relatedItems = append(relatedItems, slimItem(child))
		}
		payload := map[string]interface{}{
			"kind":            "item",
			"lookup":          lookup,
			"item":            slimItem(item),
			"related_sources": related,
			"related_items":   relatedItems,
			"count":           len(related) + len(relatedItems),
		}
		return toolOKResult(formatRelatedItemGraph(item.SourceKey, related, relatedItems), payload), nil
	}

	source, err := s.st.GetSource(ctx, lookup)
	if err != nil {
		return nil, lookupNotFoundError(lookup)
	}
	backlinks, err := s.st.ListBacklinksForSource(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"kind":      "source",
		"lookup":    lookup,
		"source":    slimSource(source),
		"backlinks": backlinks,
		"count":     len(backlinks),
	}
	return toolOKResult(formatBacklinks(source.SourceKey, backlinks), payload), nil
}

func (s *Server) toolStatsItems(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		SourceType string `json:"source_type"`
		GroupBy    string `json:"group_by"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode stats items args: %w", err)
	}
	groupBy := firstNonEmpty(args.GroupBy, "source-type")
	buckets, err := s.st.CountItems(ctx, args.SourceType, groupBy)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"source_type": strings.TrimSpace(args.SourceType),
		"group_by":    groupBy,
		"buckets":     buckets,
		"total":       countBucketTotal(buckets),
	}
	return toolOKResult(formatCountBuckets(groupBy, buckets), payload), nil
}

func (s *Server) toolStatsSources(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		SourceType    string `json:"source_type"`
		ExtractTool   string `json:"extract_tool"`
		SummaryStatus string `json:"summary_status"`
		ExtractStatus string `json:"extract_status"`
		GroupBy       string `json:"group_by"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode stats sources args: %w", err)
	}
	groupBy := firstNonEmpty(args.GroupBy, "source-type")
	filter := store.SourceCountFilter{
		SourceType:    strings.TrimSpace(args.SourceType),
		ExtractTool:   strings.TrimSpace(args.ExtractTool),
		SummaryStatus: strings.TrimSpace(args.SummaryStatus),
		ExtractStatus: strings.TrimSpace(args.ExtractStatus),
	}
	buckets, err := s.st.CountSources(ctx, filter, groupBy)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"filter":   filter,
		"group_by": groupBy,
		"buckets":  buckets,
		"total":    countBucketTotal(buckets),
	}
	return toolOKResult(formatCountBuckets(groupBy, buckets), payload), nil
}

func (s *Server) toolStatsActivity(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		WindowSeconds int `json:"window_seconds"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode stats activity args: %w", err)
	}
	window := secondsTimeout(args.WindowSeconds)
	if window <= 0 {
		window = 15 * time.Minute
	}
	stats, err := s.st.Activity(ctx, timeNowUTC(), window)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Latest source summary: %s\nSources summarized in window: %d", stats.LatestSourceSummaryAt.Format("2006-01-02T15:04:05Z07:00"), stats.SourcesSummarizedInWindow)
	return toolOKResult(text, stats), nil
}

func (s *Server) toolStatsBacklog(ctx context.Context) (map[string]interface{}, error) {
	stats, err := s.st.Backlog(ctx, sourceenrich.SummaryPromptVersion, "", "")
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Queue drained: %t\nSource extraction pending: %d\nSource summary pending: %d", stats.Drained, stats.SourceExtractionPending, stats.SourceSummaryPending)
	return toolOKResult(text, stats), nil
}
