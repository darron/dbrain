package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func (s *Server) handleResourcesList() map[string]interface{} {
	return map[string]interface{}{
		"resources": resourceDefinitions(),
	}
}

func (s *Server) handleResourceTemplatesList() map[string]interface{} {
	return map[string]interface{}{
		"resourceTemplates": resourceTemplateDefinitions(),
	}
}

func (s *Server) handleResourceRead(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode resources/read args: %w", err)
	}
	if strings.TrimSpace(args.URI) == "" {
		return nil, fmt.Errorf("resources/read requires a uri")
	}

	content, err := s.readResource(ctx, args.URI)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"contents": content,
	}, nil
}

func resourceDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"uri":         "dbrain://mcp/overview",
			"name":        "MCP Overview",
			"description": "Overview of the dbrain MCP server surface, including tools, resources, prompts, and suggested workflows.",
			"mimeType":    "text/markdown",
		},
		{
			"uri":         "dbrain://stats/activity",
			"name":        "Brain Activity",
			"description": "Recent pipeline activity timestamps and write counts for the local brain.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "dbrain://stats/backlog",
			"name":        "Brain Backlog",
			"description": "Remaining queued work in the local brain pipeline.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "dbrain://stats/items",
			"name":        "Brain Item Counts",
			"description": "Item counts for the local brain. Supports query params source_type and group_by.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "dbrain://stats/sources",
			"name":        "Brain Source Counts",
			"description": "Source counts for the local brain. Supports query params source_type, extract_tool, summary_status, extract_status, and group_by.",
			"mimeType":    "application/json",
		},
	}
}

func resourceTemplateDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"uriTemplate": "dbrain://item/{lookup}",
			"name":        "Brain Item",
			"description": "Rendered item note and metadata for a source key, external id, URL, or note path. URL-encode the lookup value.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "dbrain://source/{lookup}",
			"name":        "Brain Source",
			"description": "Rendered source note and metadata for a source key, canonical URL, or note path. URL-encode the lookup value.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "dbrain://search/{query}",
			"name":        "Brain Search",
			"description": "Search results for a query. Supports query params limit and repeated source_type values.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://entity/{query}",
			"name":        "Brain Entity Map",
			"description": "Derived entities for a query. Supports query params kind and limit.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://topic/{query}",
			"name":        "Brain Topic Map",
			"description": "Topic map for a concept. Supports query params repeated source_type values, seed_limit, and related_limit.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://topic-note/{query}",
			"name":        "Brain Topic Note Preview",
			"description": "Rendered markdown preview for a generated topic note. Supports query params repeated source_type values, seed_limit, and related_limit.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "dbrain://research/{query}",
			"name":        "Brain Research Pack",
			"description": "Research pack for a question. Supports query params repeated source_type values, limit, include_related, related_limit, and seed_limit.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://stats/items{?source_type,group_by}",
			"name":        "Brain Item Count Query",
			"description": "Item counts with optional source_type and group_by query parameters.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "dbrain://stats/sources{?source_type,extract_tool,summary_status,extract_status,group_by}",
			"name":        "Brain Source Count Query",
			"description": "Source counts with optional filters and grouping query parameters.",
			"mimeType":    "application/json",
		},
	}
}

func (s *Server) readResource(ctx context.Context, uri string) ([]map[string]string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse resource uri: %w", err)
	}
	if parsed.Scheme != "dbrain" {
		return nil, fmt.Errorf("unsupported resource scheme %q", parsed.Scheme)
	}

	switch parsed.Host {
	case "mcp":
		return s.readMCPResource(uri, parsed)
	case "stats":
		return s.readStatsResource(ctx, uri, parsed)
	case "item":
		lookup, err := resourceLookup(parsed, "lookup")
		if err != nil {
			return nil, err
		}
		return s.readItemResource(ctx, uri, lookup)
	case "source":
		lookup, err := resourceLookup(parsed, "lookup")
		if err != nil {
			return nil, err
		}
		return s.readSourceResource(ctx, uri, lookup)
	case "search":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readSearchResource(ctx, uri, parsed, query)
	case "entity":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readEntityResource(ctx, uri, parsed, query)
	case "topic":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readTopicResource(ctx, uri, parsed, query)
	case "topic-note":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readTopicNoteResource(ctx, uri, parsed, query)
	case "research":
		query, err := resourceLookup(parsed, "query")
		if err != nil {
			return nil, err
		}
		return s.readResearchResource(ctx, uri, parsed, query)
	default:
		return nil, fmt.Errorf("unsupported resource host %q", parsed.Host)
	}
}

func (s *Server) readMCPResource(uri string, parsed *url.URL) ([]map[string]string, error) {
	switch strings.Trim(parsed.Path, "/") {
	case "overview":
		return []map[string]string{{
			"uri":      uri,
			"mimeType": "text/markdown",
			"text":     strings.TrimSpace(mcpOverviewText()),
		}}, nil
	default:
		return nil, fmt.Errorf("unknown mcp resource %q", parsed.Path)
	}
}

func (s *Server) readStatsResource(ctx context.Context, uri string, parsed *url.URL) ([]map[string]string, error) {
	switch strings.Trim(parsed.Path, "/") {
	case "activity":
		window := secondsTimeout(intFromQuery(parsed.Query(), "window_seconds"))
		if window <= 0 {
			window = 15 * time.Minute
		}
		stats, err := s.st.Activity(ctx, timeNowUTC(), window)
		if err != nil {
			return nil, err
		}
		return jsonResourceContents(uri, stats)
	case "backlog":
		stats, err := s.st.Backlog(ctx, sourceenrich.SummaryPromptVersion, "", "")
		if err != nil {
			return nil, err
		}
		return jsonResourceContents(uri, stats)
	case "items":
		groupBy := firstNonEmpty(strings.TrimSpace(parsed.Query().Get("group_by")), "source-type")
		sourceType := strings.TrimSpace(parsed.Query().Get("source_type"))
		buckets, err := s.st.CountItems(ctx, sourceType, groupBy)
		if err != nil {
			return nil, err
		}
		payload := map[string]interface{}{
			"source_type": sourceType,
			"group_by":    groupBy,
			"buckets":     buckets,
			"total":       countBucketTotal(buckets),
		}
		return jsonResourceContents(uri, payload)
	case "sources":
		groupBy := firstNonEmpty(strings.TrimSpace(parsed.Query().Get("group_by")), "source-type")
		filter := map[string]string{
			"source_type":    strings.TrimSpace(parsed.Query().Get("source_type")),
			"extract_tool":   strings.TrimSpace(parsed.Query().Get("extract_tool")),
			"summary_status": strings.TrimSpace(parsed.Query().Get("summary_status")),
			"extract_status": strings.TrimSpace(parsed.Query().Get("extract_status")),
		}
		buckets, err := s.st.CountSources(ctx, store.SourceCountFilter{
			SourceType:    filter["source_type"],
			ExtractTool:   filter["extract_tool"],
			SummaryStatus: filter["summary_status"],
			ExtractStatus: filter["extract_status"],
		}, groupBy)
		if err != nil {
			return nil, err
		}
		payload := map[string]interface{}{
			"filter":   filter,
			"group_by": groupBy,
			"buckets":  buckets,
			"total":    countBucketTotal(buckets),
		}
		return jsonResourceContents(uri, payload)
	default:
		return nil, fmt.Errorf("unknown stats resource %q", parsed.Path)
	}
}

func (s *Server) readItemResource(ctx context.Context, uri string, lookup string) ([]map[string]string, error) {
	item, err := s.st.GetItem(ctx, lookup)
	if err != nil {
		return nil, err
	}

	notePath := filepath.Join(s.cfg.VaultDir, filepath.FromSlash(item.NotePath))
	noteBody, err := readNote(notePath)
	if err != nil {
		noteBody = fmt.Sprintf("_Note unreadable: %v_", err)
	}
	text := strings.TrimSpace(fmt.Sprintf(`# %s

- Kind: item
- Source key: %s
- Source type: %s
- URL: %s
- Note: %s

## Note

%s
`, firstNonEmpty(item.Title, item.SourceKey), item.SourceKey, item.SourceType, item.CanonicalURL, notePath, noteBody))

	return []map[string]string{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     text,
	}}, nil
}

func (s *Server) readEntityResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	results, err := entities.Search(ctx, s.st, query, entities.SearchOptions{
		Kind:  strings.TrimSpace(parsed.Query().Get("kind")),
		Limit: intFromQuery(parsed.Query(), "limit"),
	})
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"query":    query,
		"kind":     strings.TrimSpace(parsed.Query().Get("kind")),
		"count":    len(results),
		"entities": results,
	}
	return jsonResourceContents(uri, payload)
}

func (s *Server) readSourceResource(ctx context.Context, uri string, lookup string) ([]map[string]string, error) {
	source, err := s.st.GetSource(ctx, lookup)
	if err != nil {
		return nil, err
	}

	notePath := filepath.Join(s.cfg.VaultDir, filepath.FromSlash(source.NotePath))
	noteBody, err := readNote(notePath)
	if err != nil {
		noteBody = fmt.Sprintf("_Note unreadable: %v_", err)
	}
	text := strings.TrimSpace(fmt.Sprintf(`# %s

- Kind: source
- Source key: %s
- Source type: %s
- URL: %s
- Note: %s

## Note

%s
`, firstNonEmpty(source.Title, source.SourceKey, source.CanonicalURL), source.SourceKey, source.SourceType, source.CanonicalURL, notePath, noteBody))

	return []map[string]string{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     text,
	}}, nil
}

func (s *Server) readSearchResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	limit := defaultInt(intFromQuery(parsed.Query(), "limit"), 10)
	results, err := s.st.Search(ctx, strings.TrimSpace(query), limit)
	if err != nil {
		return nil, err
	}
	results = filterSearchResults(ctx, s.st, results, listFromQuery(parsed.Query(), "source_type"))

	payload := map[string]interface{}{
		"query":   query,
		"count":   len(results),
		"results": results,
	}
	return jsonResourceContents(uri, payload)
}

func (s *Server) readTopicResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	graph, err := topics.Build(ctx, s.st, query, topics.Options{
		SourceTypes:  listFromQuery(parsed.Query(), "source_type"),
		SeedLimit:    intFromQuery(parsed.Query(), "seed_limit"),
		RelatedLimit: intFromQuery(parsed.Query(), "related_limit"),
	})
	if err != nil {
		return nil, err
	}
	return jsonResourceContents(uri, graph)
}

func (s *Server) readTopicNoteResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	graph, err := topics.Build(ctx, s.st, query, topics.Options{
		SourceTypes:  listFromQuery(parsed.Query(), "source_type"),
		SeedLimit:    intFromQuery(parsed.Query(), "seed_limit"),
		RelatedLimit: intFromQuery(parsed.Query(), "related_limit"),
	})
	if err != nil {
		return nil, err
	}
	return []map[string]string{{
		"uri":      uri,
		"mimeType": "text/markdown",
		"text":     vault.RenderTopic(graph),
	}}, nil
}

func (s *Server) readResearchResource(ctx context.Context, uri string, parsed *url.URL, query string) ([]map[string]string, error) {
	pack, err := s.buildResearchPack(ctx, researchPackOptions{
		Question:       query,
		Topic:          firstQueryValue(parsed.Query(), "topic"),
		Limit:          intFromQuery(parsed.Query(), "limit"),
		SourceTypes:    listFromQuery(parsed.Query(), "source_type"),
		IncludeRelated: boolFromQuery(parsed.Query(), "include_related"),
		RelatedLimit:   intFromQuery(parsed.Query(), "related_limit"),
		SeedLimit:      intFromQuery(parsed.Query(), "seed_limit"),
		IncludeTopic:   boolPtrFromQuery(parsed.Query(), "include_topic_brief"),
		MaxCharsPerDoc: intFromQuery(parsed.Query(), "max_chars_per_doc"),
	})
	if err != nil {
		return nil, err
	}
	return jsonResourceContents(uri, pack)
}

func jsonResourceContents(uri string, payload interface{}) ([]map[string]string, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal resource payload: %w", err)
	}
	return []map[string]string{{
		"uri":      uri,
		"mimeType": "application/json",
		"text":     string(data),
	}}, nil
}

func resourceLookup(parsed *url.URL, queryKey string) (string, error) {
	raw := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if raw == "" {
		raw = strings.TrimSpace(parsed.Query().Get(queryKey))
	}
	if raw == "" {
		return "", fmt.Errorf("resource uri %q is missing %s", parsed.String(), queryKey)
	}
	value, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("decode resource lookup: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("resource uri %q resolved to an empty %s", parsed.String(), queryKey)
	}
	return value, nil
}

func intFromQuery(values url.Values, key string) int {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func boolFromQuery(values url.Values, key string) bool {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func boolPtrFromQuery(values url.Values, key string) *bool {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return nil
	}
	value := false
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		value = true
	}
	return &value
}

func firstQueryValue(values url.Values, key string) string {
	return strings.TrimSpace(values.Get(key))
}

func listFromQuery(values url.Values, key string) []string {
	rawValues := values[key]
	if len(rawValues) == 0 {
		return nil
	}
	out := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func mcpOverviewText() string {
	return `# dbrain MCP

The local dbrain MCP server is read-only.

## Tools

- ` + "`dbrain_search`" + `: search the local corpus, including exact user-tag aliases for multi-word entity queries
- ` + "`dbrain_get`" + `: load DB-backed item/source metadata, capped content sections, and limited linked/quoted context; use ` + "`content_mode=rendered`" + ` only when rendered Markdown is needed
- ` + "`dbrain_ask`" + `: retrieve evidence and optionally synthesize an answer
- ` + "`dbrain_entity_map`" + `: browse derived entities across the local brain
- ` + "`dbrain_topic_map`" + `: build a compact topic graph around a concept
- ` + "`dbrain_topic_brief`" + `: build a richer topic brief with grouped pivots and markdown preview
- ` + "`dbrain_research_pack`" + `: bundle retrieve-only evidence, query/tag hints, exact tag and corpus coverage counts, suggested follow-ups, and an optional topic brief
- ` + "`dbrain_related`" + `: follow item-to-source links or source backlinks
- ` + "`dbrain_stats_items`" + `: count item signals
- ` + "`dbrain_stats_sources`" + `: count sources by filters or status
- ` + "`dbrain_stats_activity`" + `: inspect recent pipeline activity
- ` + "`dbrain_stats_backlog`" + `: inspect remaining queued work

## Resources

- ` + "`dbrain://mcp/overview`" + `
- ` + "`dbrain://stats/activity`" + `
- ` + "`dbrain://stats/backlog`" + `
- ` + "`dbrain://stats/items`" + `
- ` + "`dbrain://stats/sources`" + `
- ` + "`dbrain://item/{lookup}`" + `
- ` + "`dbrain://source/{lookup}`" + `
- ` + "`dbrain://search/{query}`" + `
- ` + "`dbrain://entity/{query}`" + `
- ` + "`dbrain://topic/{query}`" + `
- ` + "`dbrain://topic-note/{query}`" + `
- ` + "`dbrain://research/{query}`" + `

## Prompts

- ` + "`brain_research`" + `: research a question from the brain
- ` + "`brain_browse`" + `: browse outward from a known note
- ` + "`brain_entity_browse`" + `: browse derived entities from stable local metadata
- ` + "`brain_topic_map`" + `: assemble a topic map from a keyword or concept
- ` + "`brain_topic_brief`" + `: assemble a browsable topic brief and note preview
- ` + "`brain_status`" + `: inspect pipeline activity and backlog

## Suggested workflows

1. Research: call ` + "`dbrain_research_pack`" + ` first, check ` + "`coverage.recall_note`" + ` and exact tag counts, then inspect the strongest hits with ` + "`dbrain_get`" + ` using ` + "`content_mode=evidence`" + ` or expand with ` + "`dbrain_related`" + `. Answer from the collector's saved corpus; do not add outside balance unless asked.
2. Browse: call ` + "`dbrain_get`" + ` on a known item or source, then expand with ` + "`dbrain_related`" + `. Prefer DB-backed modes (` + "`brief`" + `, ` + "`evidence`" + `, ` + "`raw`" + `); media enrichments appear as ` + "`x_media_transcript`" + ` and ` + "`ocr_text`" + ` sections. Sources can have their own ` + "`user_tags`" + `, and source ` + "`backlinks`" + ` include the referencing saved item's ` + "`user_tags`" + ` when that context differs. Use ` + "`rendered`" + ` for note shape only.
3. Entity browse: call ` + "`dbrain_entity_map`" + ` or read ` + "`dbrain://entity/{query}`" + ` to find people, repos, orgs, and sites connected to the corpus.
4. Topic map: call ` + "`dbrain_topic_map`" + ` or read ` + "`dbrain://topic/{query}`" + ` for a compact graph around a concept.
5. Topic brief: call ` + "`dbrain_topic_brief`" + ` or read ` + "`dbrain://topic-note/{query}`" + ` for grouped pivots and a rendered note preview.
6. Monitor: call ` + "`dbrain_stats_activity`" + ` and ` + "`dbrain_stats_backlog`" + `, then use ` + "`dbrain_stats_sources`" + ` for deeper breakdowns.

` + "`dbrain_ask`" + ` defaults to retrieval-only on the MCP surface so clients do not silently spend model usage.`
}
