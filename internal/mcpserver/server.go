package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

const protocolVersion = "2025-03-26"

type Server struct {
	cfg config.Config
	st  *store.Store
}

func New(cfg config.Config, st *store.Store) *Server {
	return &Server{cfg: cfg, st: st}
}

func Serve(ctx context.Context, cfg config.Config, in io.Reader, out io.Writer) error {
	start := time.Now()
	logMCPServer("starting", "db_path", cfg.DBPath, "pid", fmt.Sprintf("%d", os.Getpid()))
	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		logMCPServer("store_open_failed", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	logMCPServer("store_opened", "duration", time.Since(start).String())
	defer func() {
		_ = st.Close()
	}()

	server := New(cfg, st)
	logMCPServer("ready")
	if err := server.Serve(ctx, in, out); err != nil {
		logMCPServer("exiting", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	logMCPServer("exiting", "duration", time.Since(start).String(), "error", "")
	return nil
}

func logMCPServer(event string, fields ...string) {
	_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp server event=%s", time.Now().Format("15:04:05.000"), event)
	for i := 0; i+1 < len(fields); i += 2 {
		_, _ = fmt.Fprintf(os.Stderr, " %s=%q", fields[i], fields[i+1])
	}
	_, _ = fmt.Fprintln(os.Stderr)
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

	for {
		payload, err := readFrame(reader)
		if err != nil {
			if err == io.EOF {
				logMCPServer("stdin_eof")
				return nil
			}
			logMCPServer("read_failed", "error", err.Error())
			return err
		}

		response, ok := s.processPayload(ctx, payload)
		if !ok {
			continue
		}
		if err := writeFrame(writer, response); err != nil {
			return err
		}
	}
}

func (s *Server) processPayload(ctx context.Context, payload []byte) (interface{}, bool) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return rpcError(nil, -32700, "parse error"), true
	}
	if trimmed[0] != '[' {
		start := time.Now()
		resp, ok := s.handle(ctx, trimmed)
		logMCPRequest(trimmed, resp, ok, time.Since(start))
		return resp, ok
	}

	var batch []json.RawMessage
	if err := json.Unmarshal(trimmed, &batch); err != nil {
		return rpcError(nil, -32700, "parse error"), true
	}
	if len(batch) == 0 {
		return rpcError(nil, -32600, "invalid request"), true
	}

	responses := make([]response, 0, len(batch))
	for _, raw := range batch {
		start := time.Now()
		resp, ok := s.handle(ctx, raw)
		logMCPRequest(raw, resp, ok, time.Since(start))
		if ok {
			responses = append(responses, resp)
		}
	}
	if len(responses) == 0 {
		return nil, false
	}
	return responses, true
}

func logMCPRequest(payload []byte, resp response, responded bool, duration time.Duration) {
	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp request method=parse_error status=error duration=%s\n", time.Now().Format("15:04:05.000"), duration)
		return
	}

	tool := ""
	if req.Method == "tools/call" {
		var params struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil {
			tool = strings.TrimSpace(params.Name)
		}
	}

	status := "ok"
	if !responded {
		status = "notification"
	} else if resp.Error != nil {
		status = "error"
	} else if result, ok := resp.Result.(map[string]interface{}); ok {
		if isError, ok := result["isError"].(bool); ok && isError {
			status = "tool_error"
		}
	}

	if tool != "" {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp request method=%s tool=%s status=%s duration=%s\n", time.Now().Format("15:04:05.000"), req.Method, tool, status, duration)
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp request method=%s status=%s duration=%s\n", time.Now().Format("15:04:05.000"), req.Method, status, duration)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handle(ctx context.Context, payload []byte) (response, bool) {
	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		return rpcError(nil, -32700, "parse error"), true
	}

	isNotification := len(req.ID) == 0
	if req.Method == "" {
		return response{}, false
	}
	if isNotification {
		return response{}, false
	}
	switch req.Method {
	case "initialize":
		result := map[string]interface{}{
			"protocolVersion": protocolVersion,
			"serverInfo": map[string]string{
				"name":    "dbrain",
				"version": "0.1.0",
			},
			"capabilities": map[string]interface{}{
				"prompts": map[string]interface{}{
					"listChanged": false,
				},
				"resources": map[string]interface{}{
					"listChanged": false,
					"subscribe":   false,
				},
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "notifications/initialized":
		return response{}, false
	case "ping":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}, true
	case "resources/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: s.handleResourcesList()}, true
	case "resources/templates/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: s.handleResourceTemplatesList()}, true
	case "resources/read":
		result, err := s.handleResourceRead(ctx, req.Params)
		if err != nil {
			return rpcError(req.ID, -32000, err.Error()), true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "prompts/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: s.handlePromptsList()}, true
	case "prompts/get":
		result, err := s.handlePromptGet(req.Params)
		if err != nil {
			return rpcError(req.ID, -32000, err.Error()), true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "tools/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": toolDefinitions()}}, true
	case "tools/call":
		result, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			return response{JSONRPC: "2.0", ID: req.ID, Result: toolErrorResult(err)}, true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	default:
		if isNotification {
			return response{}, false
		}
		return rpcError(req.ID, -32601, "method not found"), true
	}
}

func rpcError(id json.RawMessage, code int, message string) response {
	return response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	}
}

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

func toolDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "dbrain_search",
			"description": "Search the local brain across items and linked sources.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":        map[string]interface{}{"type": "string", "description": "Search query."},
					"limit":        map[string]interface{}{"type": "integer", "description": "Maximum number of results.", "default": 10},
					"source_types": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters like github, web, x_bookmark, apple_note."},
				},
				"required": []string{"query"},
			},
			"outputSchema": searchOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_get",
			"description": "Load a specific item or source from the local brain by source key, URL, external id, or note path. Defaults to capped DB-backed evidence sections; rendered Markdown is available with content_mode=rendered.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookup":                map[string]interface{}{"type": "string", "description": "Source key, external id, URL, or note path."},
					"query":                 map[string]interface{}{"type": "string", "description": "Optional query used to window evidence sections around matching text. Applies to content_mode=evidence."},
					"content_mode":          map[string]interface{}{"type": "string", "enum": []string{"brief", "evidence", "raw", "rendered"}, "description": "Content to return: brief metadata only, capped DB evidence sections, capped raw DB sections, or rendered Markdown note.", "default": "evidence"},
					"max_chars_per_section": map[string]interface{}{"type": "integer", "description": "Maximum characters per returned content section. Hard-capped to prevent accidental huge context.", "default": defaultGetSectionChars},
					"include_content":       map[string]interface{}{"type": "boolean", "description": "Deprecated compatibility flag. false maps to content_mode=brief; true maps to content_mode=evidence unless content_mode is set.", "default": true},
				},
				"required": []string{"lookup"},
			},
			"outputSchema": getOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_get_many",
			"description": "Batch-load several specific items or sources from the local brain. Use after search/research_pack when inspecting multiple evidence rows; returns per-lookup payloads and partial errors without failing the whole batch.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookups":               map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Source keys, external ids, URLs, or note paths. Maximum 20."},
					"query":                 map[string]interface{}{"type": "string", "description": "Optional query used to window evidence sections around matching text. Applies to content_mode=evidence."},
					"content_mode":          map[string]interface{}{"type": "string", "enum": []string{"brief", "evidence", "raw", "rendered"}, "description": "Content to return for each lookup.", "default": "evidence"},
					"max_chars_per_section": map[string]interface{}{"type": "integer", "description": "Maximum characters per returned content section. Hard-capped to prevent accidental huge context.", "default": defaultGetSectionChars},
					"include_content":       map[string]interface{}{"type": "boolean", "description": "Deprecated compatibility flag. false maps to content_mode=brief; true maps to content_mode=evidence unless content_mode is set.", "default": true},
				},
				"required": []string{"lookups"},
			},
			"outputSchema": getManyOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_research_pack",
			"description": "Build a compact read-only research pack for a question. Expands text queries with bounded model-assisted query planning when configured, hyphenated tag aliases, entity matches, optional graph links, and an optional topic brief so agents can answer broad corpus questions with one call.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question":            map[string]interface{}{"type": "string", "description": "Question to investigate from the local brain."},
					"topic":               map[string]interface{}{"type": "string", "description": "Optional explicit topic for the topic brief. If omitted, broad questions infer one."},
					"limit":               map[string]interface{}{"type": "integer", "description": "Maximum evidence documents.", "default": 8},
					"source_types":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters."},
					"include_related":     map[string]interface{}{"type": "boolean", "description": "Whether to append linked related evidence.", "default": false},
					"related_limit":       map[string]interface{}{"type": "integer", "description": "Maximum related evidence documents.", "default": 2},
					"seed_limit":          map[string]interface{}{"type": "integer", "description": "Maximum primary topic nodes when a topic brief is included.", "default": 6},
					"include_topic_brief": map[string]interface{}{"type": "boolean", "description": "Force topic brief on or off. Defaults to on only when a broad topic can be inferred."},
					"max_chars_per_doc":   map[string]interface{}{"type": "integer", "description": "Maximum summary/excerpt characters per evidence document.", "default": 700},
					"planner_model":       map[string]interface{}{"type": "string", "description": "Optional model for query planning; empty uses the configured summary model."},
					"use_model_planner":   map[string]interface{}{"type": "boolean", "description": "Use the configured model for bounded query planning before retrieval. Defaults to true unless disable_planner is set.", "default": true},
					"disable_planner":     map[string]interface{}{"type": "boolean", "description": "Disable model-assisted query planning and use deterministic planning only.", "default": false},
				},
				"required": []string{"question"},
			},
			"outputSchema": researchPackOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_entity_map",
			"description": "Search derived entities built from stable local metadata such as X authors, GitHub repos, GitHub owners, and site domains.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "Entity search query. Can be empty to list the top derived entities."},
					"kind":  map[string]interface{}{"type": "string", "description": "Optional kind filter: person, org, project, or site."},
					"limit": map[string]interface{}{"type": "integer", "description": "Maximum number of entities.", "default": 20},
				},
			},
			"outputSchema": entityMapOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_topic_map",
			"description": "Build a compact topic map from the local brain by combining search seeds with item/source graph expansion.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":         map[string]interface{}{"type": "string", "description": "Concept, keyword, or theme to map."},
					"source_types":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters."},
					"seed_limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of primary seed nodes.", "default": 6},
					"related_limit": map[string]interface{}{"type": "integer", "description": "Maximum related nodes to expand from each seed.", "default": 2},
				},
				"required": []string{"topic"},
			},
			"outputSchema": topicMapOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_topic_brief",
			"description": "Build a richer topic brief from the local brain, including grouped entity pivots and a rendered markdown preview.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"topic":         map[string]interface{}{"type": "string", "description": "Concept, keyword, or theme to map."},
					"source_types":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional source type filters."},
					"seed_limit":    map[string]interface{}{"type": "integer", "description": "Maximum number of primary seed nodes.", "default": 6},
					"related_limit": map[string]interface{}{"type": "integer", "description": "Maximum related nodes to expand from each seed.", "default": 2},
				},
				"required": []string{"topic"},
			},
			"outputSchema": topicBriefOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_related",
			"description": "Traverse the local item/source graph. For an item lookup, return linked sources. For a source lookup, return item backlinks.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"lookup": map[string]interface{}{"type": "string", "description": "Source key, external id, URL, or note path for an item or source."},
				},
				"required": []string{"lookup"},
			},
			"outputSchema": relatedOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_items",
			"description": "Read item counts from the local brain, optionally filtered by source type and grouped by source type or none.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source_type": map[string]interface{}{"type": "string", "description": "Optional item source type filter."},
					"group_by":    map[string]interface{}{"type": "string", "description": "Grouping: source-type or none.", "default": "source-type"},
				},
			},
			"outputSchema": countOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_sources",
			"description": "Read source counts from the local brain, optionally filtered by source type, extract tool, extract status, or summary status.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source_type":    map[string]interface{}{"type": "string", "description": "Optional source type filter."},
					"extract_tool":   map[string]interface{}{"type": "string", "description": "Optional extract tool filter."},
					"summary_status": map[string]interface{}{"type": "string", "description": "Optional summary status filter."},
					"extract_status": map[string]interface{}{"type": "string", "description": "Optional extract status filter."},
					"group_by":       map[string]interface{}{"type": "string", "description": "Grouping: source-type, summary-status, extract-status, or none.", "default": "source-type"},
				},
			},
			"outputSchema": countOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_activity",
			"description": "Read recent activity timestamps and counts for the local brain pipeline.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"window_seconds": map[string]interface{}{"type": "integer", "description": "Lookback window in seconds.", "default": 900},
				},
			},
			"outputSchema": activityOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
		{
			"name":        "dbrain_stats_backlog",
			"description": "Read the remaining queued work in the local brain pipeline.",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
			"outputSchema": backlogOutputSchema(),
			"annotations":  map[string]bool{"readOnlyHint": true, "idempotentHint": true},
		},
	}
}

func searchOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"count":             scalarSchema("integer", "Number of search hits returned."),
		"results":           arraySchema(searchResultSchema()),
		"tag_aliases":       arraySchema(scalarSchema("string", "Hyphenated tag aliases checked for exact user_tags matches.")),
		"exact_tag_matches": arraySchema(researchBucketSchema()),
	}, "count", "results")
}

func getOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"kind":                  enumSchema("item or source", "item", "source"),
		"title":                 scalarSchema("string", "Resolved title."),
		"source_key":            scalarSchema("string", "Resolved source key."),
		"url":                   scalarSchema("string", "Canonical URL when available."),
		"note":                  scalarSchema("string", "Absolute path to the rendered note."),
		"note_path":             scalarSchema("string", "Relative rendered note path."),
		"query":                 scalarSchema("string", "Optional query used to window evidence sections around matches."),
		"content_mode":          enumSchema("Returned content mode.", "brief", "evidence", "raw", "rendered"),
		"max_chars_per_section": scalarSchema("integer", "Maximum characters returned per content section."),
		"available_sections":    arraySchema(getSectionSchema()),
		"content_sections":      arraySchema(getSectionSchema()),
		"content":               scalarSchema("string", "Rendered markdown note content when content_mode is rendered."),
		"item":                  genericObjectSchema("Slim item metadata when the lookup resolved to an item."),
		"source":                genericObjectSchema("Slim source metadata when the lookup resolved to a source."),
		"related_items":         arraySchema(genericObjectSchema("Slim child item metadata, for example quoted posts.")),
		"related_sources":       arraySchema(itemSourceRefSchema()),
		"backlinks":             arraySchema(sourceBacklinkSchema()),
	}, "kind", "title", "source_key", "note", "content_mode", "available_sections", "content_sections")
}

func getManyOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"lookups":               arraySchema(scalarSchema("string", "Lookup values requested.")),
		"query":                 scalarSchema("string", "Optional query used to window evidence sections around matches."),
		"content_mode":          enumSchema("Returned content mode.", "brief", "evidence", "raw", "rendered"),
		"max_chars_per_section": scalarSchema("integer", "Maximum characters returned per content section."),
		"count":                 scalarSchema("integer", "Number of lookups successfully resolved."),
		"results":               arraySchema(getOutputSchema()),
		"errors":                arraySchema(getManyErrorSchema()),
	}, "lookups", "content_mode", "max_chars_per_section", "count", "results", "errors")
}

func getManyErrorSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"lookup": scalarSchema("string", "Lookup value that failed."),
		"error":  scalarSchema("string", "Error returned while resolving the lookup."),
	}, "lookup", "error")
}

func getSectionSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"name":      scalarSchema("string", "Section name, for example summary_text, extracted_text, x_post_text, or rendered_note."),
		"role":      scalarSchema("string", "Section role such as raw, derived, metadata, raw_json, or rendered."),
		"status":    scalarSchema("string", "Pipeline status for this section when applicable."),
		"model":     scalarSchema("string", "Model provenance for derived sections when applicable."),
		"tool":      scalarSchema("string", "Tool provenance when applicable."),
		"at":        scalarSchema("string", "Timestamp for the section when applicable."),
		"chars":     scalarSchema("integer", "Original section character count before truncation."),
		"text":      scalarSchema("string", "Section text when returned by the selected content_mode."),
		"truncated": scalarSchema("boolean", "Whether text was truncated to max_chars_per_section."),
	}, "name", "role", "chars", "truncated")
}

func researchPackOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"schema_version":     scalarSchema("string", "Version of the research pack schema."),
		"question":           scalarSchema("string", "Original research question."),
		"mode":               scalarSchema("string", "Whether this pack contains evidence only or a topic brief plus evidence."),
		"query_plan":         researchQueryPlanSchema(),
		"coverage":           researchCoverageSchema(),
		"topic":              scalarSchema("string", "Inferred topic phrase when a topic brief was attached."),
		"used_topic_brief":   scalarSchema("boolean", "Whether a topic brief was inferred and attached."),
		"evidence":           arraySchema(evidenceSchema()),
		"exact_tag_evidence": arraySchema(evidenceSchema()),
		"topic_brief":        topicBriefOutputSchema(),
		"next_steps":         arraySchema(researchNextStepSchema()),
	}, "schema_version", "question", "mode", "query_plan", "coverage", "used_topic_brief", "evidence")
}

func researchQueryPlanSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"text_query":          scalarSchema("string", "Text query submitted to corpus search after stopword removal."),
		"query_terms":         arraySchema(scalarSchema("string", "Normalized query term.")),
		"tag_queries":         arraySchema(scalarSchema("string", "Hyphenated user_tag aliases searched in addition to text search.")),
		"query_variants":      arraySchema(researchQueryVariantSchema()),
		"concepts":            arraySchema(researchQueryConceptSchema()),
		"planner":             scalarSchema("string", "Planner path used for retrieval, such as deterministic or model_assisted."),
		"planner_model":       scalarSchema("string", "Model used for query planning when model-assisted planning ran."),
		"planner_error":       scalarSchema("string", "Non-fatal planner error when deterministic fallback was used."),
		"source_types":        arraySchema(scalarSchema("string", "Optional source type filters.")),
		"limit":               scalarSchema("integer", "Maximum evidence documents requested."),
		"max_chars_per_doc":   scalarSchema("integer", "Maximum summary/excerpt characters per evidence document."),
		"include_related":     scalarSchema("boolean", "Whether graph-related evidence was requested."),
		"related_limit":       scalarSchema("integer", "Maximum related evidence documents."),
		"topic":               scalarSchema("string", "Topic used for the topic brief when present."),
		"topic_source":        scalarSchema("string", "How the topic was selected: explicit, inferred, or normalized_question."),
		"include_topic_brief": scalarSchema("boolean", "Whether a topic brief was requested for this pack."),
	}, "text_query", "query_terms", "tag_queries", "limit", "max_chars_per_doc", "include_related", "include_topic_brief")
}

func researchQueryVariantSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"query":  scalarSchema("string", "Bounded keyword query variant used for retrieval."),
		"reason": scalarSchema("string", "Why this query variant was added."),
	}, "query")
}

func researchQueryConceptSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":       scalarSchema("string", "Canonical concept key used for evidence scoring."),
		"preferred": scalarSchema("string", "Preferred search term for this concept."),
		"terms":     arraySchema(scalarSchema("string", "Alias or alternate phrase that can satisfy the concept.")),
		"required":  scalarSchema("boolean", "Whether missing this concept should penalize evidence."),
	}, "key", "terms", "required")
}

func researchCoverageSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"evidence_count":      scalarSchema("integer", "Number of evidence rows returned."),
		"by_kind":             arraySchema(researchBucketSchema()),
		"by_source_type":      arraySchema(researchBucketSchema()),
		"top_user_tags":       arraySchema(researchBucketSchema()),
		"exact_tag_matches":   arraySchema(researchBucketSchema()),
		"item_text_matches":   scalarSchema("integer", "Total item rows matching the topic/text phrase."),
		"source_text_matches": scalarSchema("integer", "Total source rows matching the topic/text phrase."),
		"topic_node_count":    scalarSchema("integer", "Number of nodes included in the attached topic brief."),
		"topic_edge_count":    scalarSchema("integer", "Number of edges included in the attached topic brief."),
		"displayed_limit":     scalarSchema("integer", "Requested evidence limit for this pack."),
		"related_limit":       scalarSchema("integer", "Requested related-evidence limit for this pack."),
		"recall_note":         scalarSchema("string", "Human-readable reminder that returned evidence is capped and how much matching corpus exists."),
	}, "evidence_count", "by_kind", "by_source_type")
}

func researchBucketSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":   scalarSchema("string", "Bucket key."),
		"count": scalarSchema("integer", "Bucket count."),
	}, "key", "count")
}

func researchNextStepSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"action": scalarSchema("string", "Semantic action identifier, such as inspect_top_evidence or expand_related."),
		"label":  scalarSchema("string", "Human-readable action label."),
		"reason": scalarSchema("string", "Why this follow-up helps."),
		"params": genericObjectSchema("Suggested action parameters."),
	}, "action", "label")
}

func topicMapOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"topic":         scalarSchema("string", "Topic that was mapped."),
		"seed_limit":    scalarSchema("integer", "Maximum number of primary seed nodes."),
		"related_limit": scalarSchema("integer", "Maximum number of related nodes expanded per seed."),
		"synthesis":     topicSynthesisSchema(),
		"entities":      arraySchema(topicMapEntitySchema()),
		"pivots":        topicPivotsSchema(),
		"nodes":         arraySchema(topicMapNodeSchema()),
		"edges":         arraySchema(topicMapEdgeSchema()),
	}, "topic", "seed_limit", "related_limit", "nodes", "edges")
}

func topicBriefOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"topic":         scalarSchema("string", "Topic that was mapped."),
		"source_types":  arraySchema(scalarSchema("string", "Optional source type filters applied to the topic build.")),
		"seed_limit":    scalarSchema("integer", "Maximum number of primary seed nodes."),
		"related_limit": scalarSchema("integer", "Maximum number of related nodes expanded per seed."),
		"summary":       scalarSchema("string", "Compact natural-language summary of the topic graph."),
		"synthesis":     topicSynthesisSchema(),
		"pivots":        topicPivotsSchema(),
		"entities":      arraySchema(topicMapEntitySchema()),
		"nodes":         arraySchema(topicMapNodeSchema()),
		"edges":         arraySchema(topicMapEdgeSchema()),
		"markdown":      scalarSchema("string", "Rendered markdown topic note preview."),
	}, "topic", "seed_limit", "related_limit", "summary", "pivots", "nodes", "edges", "markdown")
}

func entityMapOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"count":    scalarSchema("integer", "Number of entities returned."),
		"entities": arraySchema(entitySchema()),
	}, "count", "entities")
}

func relatedOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"kind":            enumSchema("Whether the lookup resolved to an item or a source.", "item", "source"),
		"lookup":          scalarSchema("string", "Lookup value used."),
		"item":            genericObjectSchema("Slim item metadata when lookup resolved to an item."),
		"source":          genericObjectSchema("Slim source metadata when lookup resolved to a source."),
		"count":           scalarSchema("integer", "Number of related rows returned."),
		"related_items":   arraySchema(genericObjectSchema("Slim child item metadata, for example quoted posts.")),
		"related_sources": arraySchema(itemSourceRefSchema()),
		"backlinks":       arraySchema(sourceBacklinkSchema()),
	}, "kind", "count")
}

func countOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"group_by": scalarSchema("string", "Grouping applied to the count buckets."),
		"total":    scalarSchema("integer", "Total count across buckets."),
		"buckets":  arraySchema(countBucketSchema()),
	}, "group_by", "total", "buckets")
}

func activityOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"now":                          scalarSchema("string", "Current UTC time in RFC3339 format."),
		"window":                       scalarSchema("string", "Lookback window duration string."),
		"latest_item_updated_at":       scalarSchema("string", "Latest item update timestamp."),
		"latest_source_updated_at":     scalarSchema("string", "Latest source update timestamp."),
		"latest_source_summary_at":     scalarSchema("string", "Latest source summary timestamp."),
		"items_updated_in_window":      scalarSchema("integer", "Number of items updated in the window."),
		"sources_updated_in_window":    scalarSchema("integer", "Number of sources updated in the window."),
		"sources_summarized_in_window": scalarSchema("integer", "Number of sources summarized in the window."),
	}, "now", "window", "items_updated_in_window", "sources_updated_in_window", "sources_summarized_in_window")
}

func backlogOutputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"x_hydration_pending":               scalarSchema("integer", "Pending X hydration items."),
		"link_discovery_pending":            scalarSchema("integer", "Pending link discovery items."),
		"source_extraction_pending":         scalarSchema("integer", "Pending source extraction rows."),
		"source_summary_pending":            scalarSchema("integer", "Pending source summary rows."),
		"source_extraction_pending_by_type": arraySchema(countBucketSchema()),
		"source_summary_pending_by_type":    arraySchema(countBucketSchema()),
		"drained":                           scalarSchema("boolean", "Whether all current queues are empty."),
	}, "x_hydration_pending", "link_discovery_pending", "source_extraction_pending", "source_summary_pending", "drained")
}

func searchResultSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key":     scalarSchema("string", "Stable key for the item or source."),
		"source_type":    scalarSchema("string", "Underlying source type."),
		"external_id":    scalarSchema("string", "External source id when present."),
		"title":          scalarSchema("string", "Best available title."),
		"author_handle":  scalarSchema("string", "Author handle when present."),
		"author_name":    scalarSchema("string", "Author display name when present."),
		"canonical_url":  scalarSchema("string", "Canonical URL."),
		"primary_domain": scalarSchema("string", "Primary domain for item rows or source domain for source rows."),
		"note_path":      scalarSchema("string", "Relative rendered note path."),
		"user_tags":      scalarSchema("string", "Comma-separated user tags for item or source rows."),
		"snippet":        scalarSchema("string", "Search snippet."),
	}, "source_key", "title", "canonical_url", "note_path")
}

func evidenceSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key":     scalarSchema("string", "Stable key for the evidence row."),
		"kind":           scalarSchema("string", "Evidence kind, such as item or source."),
		"title":          scalarSchema("string", "Best available title."),
		"url":            scalarSchema("string", "Canonical URL."),
		"note_path":      scalarSchema("string", "Rendered note path."),
		"summary":        scalarSchema("string", "Summary text if available."),
		"excerpt":        scalarSchema("string", "Excerpt used for retrieval."),
		"author":         scalarSchema("string", "Author when present."),
		"source_type":    scalarSchema("string", "Underlying source type."),
		"published_at":   scalarSchema("string", "Published timestamp when present."),
		"extracted_at":   scalarSchema("string", "Extraction timestamp when present."),
		"summarized_at":  scalarSchema("string", "Summary timestamp when present."),
		"user_tags":      scalarSchema("string", "Comma-separated user tags for item or source evidence."),
		"entity_matches": arraySchema(scalarSchema("string", "Derived entities that matched the query and reference this note.")),
		"related_to":     scalarSchema("string", "Parent source key when added as related evidence."),
		"relationship":   scalarSchema("string", "How this evidence relates to another node."),
		"retrieval":      retrievalInfoSchema(),
	}, "source_key", "kind", "title", "url", "note_path", "summary", "excerpt")
}

func retrievalInfoSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"score":         scalarSchema("integer", "Final retrieval score used to rank this evidence row."),
		"signals":       arraySchema(retrievalSignalSchema()),
		"matched_terms": arraySchema(scalarSchema("string", "Query terms found in title, tags, summary, excerpt, URL, or author fields.")),
		"missing_terms": arraySchema(scalarSchema("string", "Query terms not found in the returned evidence fields.")),
	}, "score")
}

func retrievalSignalSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"name":   scalarSchema("string", "Name of the ranking signal, for example query_term_title or exact_phrase_user_tags."),
		"detail": scalarSchema("string", "Matched term, phrase, entity, or relationship detail when available."),
		"weight": scalarSchema("integer", "Score contribution from this signal."),
	}, "name", "weight")
}

func entitySchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":             scalarSchema("string", "Stable derived entity key."),
		"name":            scalarSchema("string", "Display name for the entity."),
		"kind":            scalarSchema("string", "Entity kind such as person, org, project, or site."),
		"aliases":         arraySchema(scalarSchema("string", "Entity alias.")),
		"canonical_url":   scalarSchema("string", "Canonical URL when available."),
		"domain":          scalarSchema("string", "Domain when available."),
		"note_path":       scalarSchema("string", "Relative rendered entity note path."),
		"source_types":    arraySchema(scalarSchema("string", "Underlying source types contributing to the entity.")),
		"reference_count": scalarSchema("integer", "Number of distinct notes referencing the entity."),
		"references":      arraySchema(entityReferenceSchema()),
		"links":           arraySchema(entityLinkSchema()),
	}, "key", "name", "kind", "note_path", "reference_count")
}

func entityReferenceSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"ref_kind":     scalarSchema("string", "Whether the reference came from an item or source note."),
		"source_key":   scalarSchema("string", "Stable source key for the referencing note."),
		"title":        scalarSchema("string", "Best available title."),
		"note_path":    scalarSchema("string", "Relative rendered note path."),
		"url":          scalarSchema("string", "Canonical URL."),
		"source_type":  scalarSchema("string", "Underlying source type."),
		"relationship": scalarSchema("string", "How the note relates to the entity."),
	}, "ref_kind", "source_key", "title", "note_path", "relationship")
}

func entityLinkSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":          scalarSchema("string", "Stable key for the linked entity."),
		"name":         scalarSchema("string", "Display name for the linked entity."),
		"kind":         scalarSchema("string", "Kind for the linked entity."),
		"note_path":    scalarSchema("string", "Relative note path for the linked entity."),
		"relationship": scalarSchema("string", "How the linked entity relates to the current entity."),
	}, "key", "name", "kind", "note_path", "relationship")
}

func itemSourceRefSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_id":      scalarSchema("integer", "Internal source id."),
		"source_key":     scalarSchema("string", "Stable source key."),
		"canonical_url":  scalarSchema("string", "Canonical URL."),
		"source_type":    scalarSchema("string", "Source type."),
		"title":          scalarSchema("string", "Best available title."),
		"note_path":      scalarSchema("string", "Relative rendered note path."),
		"extract_status": scalarSchema("string", "Extraction status."),
		"summary_status": scalarSchema("string", "Summary status."),
		"user_tags":      scalarSchema("string", "Comma-separated source user tags."),
	}, "source_id", "source_key", "canonical_url", "source_type", "title", "note_path")
}

func sourceBacklinkSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"item_id":       scalarSchema("integer", "Internal item id."),
		"source_key":    scalarSchema("string", "Stable item source key."),
		"source_type":   scalarSchema("string", "Underlying item source type."),
		"canonical_url": scalarSchema("string", "Canonical item URL."),
		"title":         scalarSchema("string", "Best available title."),
		"note_path":     scalarSchema("string", "Relative rendered note path."),
		"author_handle": scalarSchema("string", "Author handle when present."),
		"author_name":   scalarSchema("string", "Author display name when present."),
		"published_at":  scalarSchema("string", "Published timestamp when present."),
		"user_tags":     scalarSchema("string", "Comma-separated user tags from the saved item that references the source."),
	}, "item_id", "source_key", "canonical_url", "title", "note_path")
}

func topicMapNodeSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"source_key":  scalarSchema("string", "Stable node key."),
		"kind":        scalarSchema("string", "Whether the node is an item or source."),
		"title":       scalarSchema("string", "Best available title."),
		"url":         scalarSchema("string", "Canonical URL."),
		"note_path":   scalarSchema("string", "Relative rendered note path."),
		"source_type": scalarSchema("string", "Underlying source type when known."),
		"role":        scalarSchema("string", "Whether the node is a seed or related node."),
	}, "source_key", "kind", "title", "url", "note_path", "role")
}

func topicMapEntitySchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":                 scalarSchema("string", "Stable derived entity key."),
		"name":                scalarSchema("string", "Display name for the entity."),
		"kind":                scalarSchema("string", "Entity kind."),
		"note_path":           scalarSchema("string", "Relative rendered entity note path."),
		"canonical_url":       scalarSchema("string", "Canonical URL when available."),
		"reference_count":     scalarSchema("integer", "Total references to this entity across the brain."),
		"matched_references":  scalarSchema("integer", "Number of mapped nodes that reference the entity."),
		"matched_source_keys": arraySchema(scalarSchema("string", "Mapped node source keys that reference the entity.")),
	}, "key", "name", "kind", "note_path", "reference_count", "matched_references")
}

func topicPivotsSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"projects":      arraySchema(topicMapEntitySchema()),
		"orgs":          arraySchema(topicMapEntitySchema()),
		"sites":         arraySchema(topicMapEntitySchema()),
		"people":        arraySchema(topicMapEntitySchema()),
		"seed_nodes":    arraySchema(topicMapNodeSchema()),
		"related_nodes": arraySchema(topicMapNodeSchema()),
	})
}

func topicSynthesisSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"overview":       scalarSchema("string", "High-level synthesized explanation of the topic as it appears in the local corpus."),
		"angles":         arraySchema(scalarSchema("string", "Distinct angles or surfaces that show up repeatedly in the mapped corpus.")),
		"signals":        arraySchema(topicSignalSchema()),
		"open_questions": arraySchema(scalarSchema("string", "Question or tension worth revisiting in this topic.")),
		"why_it_matters": scalarSchema("string", "Why this topic looks worth keeping and revisiting."),
	})
}

func topicSignalSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"title":       scalarSchema("string", "Short label for the repeated signal."),
		"detail":      scalarSchema("string", "Evidence-backed detail for the signal."),
		"source_keys": arraySchema(scalarSchema("string", "Notes that support this signal.")),
	}, "title", "detail")
}

func topicMapEdgeSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"from":         scalarSchema("string", "Source key of the origin node."),
		"to":           scalarSchema("string", "Source key of the target node."),
		"relationship": scalarSchema("string", "Relationship label between the nodes."),
	}, "from", "to", "relationship")
}

func countBucketSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"key":   scalarSchema("string", "Bucket key."),
		"count": scalarSchema("integer", "Bucket count."),
	}, "key", "count")
}

func objectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func genericObjectSchema(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object"}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func arraySchema(items interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":  "array",
		"items": items,
	}
}

func scalarSchema(valueType string, description string) map[string]interface{} {
	schema := map[string]interface{}{
		"type": valueType,
	}
	if strings.TrimSpace(description) != "" {
		schema["description"] = description
	}
	return schema
}

func enumSchema(description string, values ...string) map[string]interface{} {
	schema := scalarSchema("string", description)
	enumValues := make([]interface{}, 0, len(values))
	for _, value := range values {
		enumValues = append(enumValues, value)
	}
	schema["enum"] = enumValues
	return schema
}

func toolOKResult(text string, payload interface{}) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": text},
		},
		"structuredContent": payload,
		"isError":           false,
	}
}

func toolErrorResult(err error) map[string]interface{} {
	message := err.Error()
	return map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": message},
		},
		"structuredContent": map[string]interface{}{
			"error": map[string]interface{}{
				"message":     message,
				"suggestions": toolErrorSuggestions(message),
			},
		},
		"isError": true,
	}
}

func toolErrorSuggestions(message string) []string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unknown tool"):
		return []string{
			"Call tools/list and choose one of the advertised dbrain_* tools.",
			"Use dbrain_research_pack for broad research, dbrain_search for keyword lookup, dbrain_get for a known lookup, or dbrain_related for graph expansion.",
		}
	case strings.Contains(lower, "lookup is required"):
		return []string{
			"Pass a source key, external id, URL, or note path as lookup.",
			"If you do not know the lookup yet, call dbrain_research_pack or dbrain_search first.",
		}
	case strings.Contains(lower, "lookup not found"):
		return []string{
			"Verify the source key, external id, URL, or note path and retry.",
			"Use dbrain_search or dbrain_research_pack to find a current lookup before calling dbrain_get or dbrain_related.",
		}
	case strings.Contains(lower, "lookups is required"):
		return []string{
			"Pass one or more source keys, external ids, URLs, or note paths in lookups.",
			"Use dbrain_research_pack next_steps or dbrain_search results as inputs to dbrain_get_many.",
		}
	case strings.Contains(lower, "too many lookups"):
		return []string{
			fmt.Sprintf("Split the request into batches of %d or fewer lookups.", maxGetManyLookups),
		}
	case strings.Contains(lower, "unsupported content_mode"):
		return []string{
			"Use content_mode=brief, evidence, raw, or rendered.",
			"Prefer content_mode=evidence for agent research and content_mode=rendered only when note shape matters.",
		}
	default:
		return []string{
			"Inspect the error message, adjust the tool arguments, and retry.",
			"Call dbrain://mcp/overview or tools/list for the supported workflow and tool surface.",
		}
	}
}

func formatSearchResults(cfg config.Config, results []model.SearchResult) string {
	if len(results) == 0 {
		return "No results."
	}
	var b strings.Builder
	for _, result := range results {
		b.WriteString("- [")
		b.WriteString(result.SourceKey)
		b.WriteString("] ")
		b.WriteString(result.Title)
		b.WriteString("\n")
		b.WriteString("  URL: ")
		b.WriteString(result.CanonicalURL)
		b.WriteString("\n")
		b.WriteString("  Note: ")
		b.WriteString(filepath.Join(cfg.VaultDir, filepath.FromSlash(result.NotePath)))
		b.WriteString("\n")
		if strings.TrimSpace(result.UserTags) != "" {
			b.WriteString("  User tags: ")
			b.WriteString(strings.TrimSpace(result.UserTags))
			b.WriteString("\n")
		}
		if strings.TrimSpace(result.Snippet) != "" {
			b.WriteString("  Snippet: ")
			b.WriteString(strings.TrimSpace(result.Snippet))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func searchTagAliases(query string) []string {
	aliases := []string{}
	aliases = append(aliases, queryterms.TagQueries(queryterms.Terms(query))...)
	trimmed := strings.TrimSpace(strings.ToLower(query))
	if strings.Contains(trimmed, "-") && !strings.ContainsAny(trimmed, " \t\n\r") {
		aliases = append(aliases, trimmed)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(strings.ToLower(alias))
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func dedupeSearchResults(results []model.SearchResult, limit int) []model.SearchResult {
	if limit <= 0 {
		limit = len(results)
	}
	seen := map[string]struct{}{}
	out := make([]model.SearchResult, 0, min(len(results), limit))
	for _, result := range results {
		if strings.TrimSpace(result.SourceKey) == "" {
			continue
		}
		if _, exists := seen[result.SourceKey]; exists {
			continue
		}
		seen[result.SourceKey] = struct{}{}
		out = append(out, result)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func formatRelatedSources(lookup string, refs []model.ItemSourceRef) string {
	if len(refs) == 0 {
		return fmt.Sprintf("No linked sources found for %s.", lookup)
	}
	var b strings.Builder
	b.WriteString("Linked sources:\n")
	for _, ref := range refs {
		b.WriteString("- [")
		b.WriteString(ref.SourceKey)
		b.WriteString("] ")
		b.WriteString(firstNonEmpty(ref.Title, ref.CanonicalURL))
		b.WriteString("\n")
		b.WriteString("  Type: ")
		b.WriteString(ref.SourceType)
		b.WriteString("\n")
		b.WriteString("  URL: ")
		b.WriteString(ref.CanonicalURL)
		b.WriteString("\n")
		b.WriteString("  Note: ")
		b.WriteString(ref.NotePath)
		b.WriteString("\n")
		if strings.TrimSpace(ref.UserTags) != "" {
			b.WriteString("  User tags: ")
			b.WriteString(strings.TrimSpace(ref.UserTags))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatRelatedItemGraph(lookup string, refs []model.ItemSourceRef, childItems []map[string]interface{}) string {
	if len(refs) == 0 && len(childItems) == 0 {
		return fmt.Sprintf("No linked sources or child items found for %s.", lookup)
	}
	var b strings.Builder
	if len(childItems) > 0 {
		b.WriteString("Related child items:\n")
		for _, child := range childItems {
			sourceKey, _ := child["source_key"].(string)
			title, _ := child["title"].(string)
			url, _ := child["canonical_url"].(string)
			notePath, _ := child["note_path"].(string)
			sourceType, _ := child["source_type"].(string)
			b.WriteString("- [")
			b.WriteString(sourceKey)
			b.WriteString("] ")
			b.WriteString(firstNonEmpty(title, url))
			b.WriteString("\n")
			b.WriteString("  Type: ")
			b.WriteString(sourceType)
			b.WriteString("\n")
			b.WriteString("  URL: ")
			b.WriteString(url)
			b.WriteString("\n")
			b.WriteString("  Note: ")
			b.WriteString(notePath)
			b.WriteString("\n")
		}
	}
	if len(refs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatRelatedSources(lookup, refs))
	}
	return strings.TrimSpace(b.String())
}

func formatBacklinks(lookup string, refs []model.SourceBacklink) string {
	if len(refs) == 0 {
		return fmt.Sprintf("No backlinks found for %s.", lookup)
	}
	var b strings.Builder
	b.WriteString("Backlinks:\n")
	for _, ref := range refs {
		b.WriteString("- [")
		b.WriteString(ref.SourceKey)
		b.WriteString("] ")
		b.WriteString(firstNonEmpty(ref.Title, ref.CanonicalURL))
		b.WriteString("\n")
		if ref.AuthorHandle != "" || ref.AuthorName != "" {
			b.WriteString("  Author: ")
			b.WriteString(firstNonEmpty(ref.AuthorName, ref.AuthorHandle))
			b.WriteString("\n")
		}
		if strings.TrimSpace(ref.UserTags) != "" {
			b.WriteString("  User tags: ")
			b.WriteString(strings.TrimSpace(ref.UserTags))
			b.WriteString("\n")
		}
		b.WriteString("  URL: ")
		b.WriteString(ref.CanonicalURL)
		b.WriteString("\n")
		b.WriteString("  Note: ")
		b.WriteString(ref.NotePath)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func formatCountBuckets(groupBy string, buckets []store.CountBucket) string {
	if len(buckets) == 0 {
		if strings.TrimSpace(groupBy) == "none" {
			return "Count: 0"
		}
		return "Total: 0"
	}
	var b strings.Builder
	total := 0
	grouped := strings.TrimSpace(groupBy) != "none"
	for _, bucket := range buckets {
		total += bucket.Count
		if !grouped {
			continue
		}
		b.WriteString(displayBucketKey(groupBy, bucket.Key))
		b.WriteString(": ")
		_, _ = fmt.Fprintf(&b, "%d", bucket.Count)
		b.WriteString("\n")
	}
	if grouped {
		b.WriteString("Total: ")
		_, _ = fmt.Fprintf(&b, "%d", total)
		return strings.TrimSpace(b.String())
	}
	return fmt.Sprintf("Count: %d", total)
}

func displayBucketKey(groupBy string, key string) string {
	value := strings.TrimSpace(key)
	if value != "" {
		return value
	}
	switch strings.TrimSpace(groupBy) {
	case "summary-status", "extract-status":
		return "pending"
	default:
		return "(empty)"
	}
}

func countBucketTotal(buckets []store.CountBucket) int {
	total := 0
	for _, bucket := range buckets {
		total += bucket.Count
	}
	return total
}

func filterSearchResults(ctx context.Context, st *store.Store, results []model.SearchResult, sourceTypes []string) []model.SearchResult {
	if len(sourceTypes) == 0 {
		return results
	}
	filtered := make([]model.SearchResult, 0, len(results))
	for _, result := range results {
		if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, item.SourceType) {
				filtered = append(filtered, result)
			}
			continue
		}
		if source, err := st.GetSource(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, source.SourceType) {
				filtered = append(filtered, result)
			}
		}
	}
	return filtered
}

func matchesSourceTypes(filters []string, sourceType string) bool {
	if len(filters) == 0 {
		return true
	}
	sourceType = strings.TrimSpace(strings.ToLower(sourceType))
	family := sourceTypeFamily(sourceType)
	for _, filter := range filters {
		filter = strings.TrimSpace(strings.ToLower(filter))
		if filter == "" {
			continue
		}
		if filter == sourceType || filter == family {
			return true
		}
	}
	return false
}

func sourceTypeFamily(value string) string {
	if idx := strings.IndexByte(value, '_'); idx > 0 {
		return value[:idx]
	}
	return value
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func secondsTimeout(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Second
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}

func readNote(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF && line == "" {
			return nil, err
		}
		if err != io.EOF {
			return nil, err
		}
	}
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, io.EOF
	}

	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []byte(trimmed), nil
	}

	contentLength := -1
	for {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength); err != nil {
				return nil, fmt.Errorf("parse content length: %w", err)
			}
		}

		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFrame(writer *bufio.Writer, value interface{}) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(value); err != nil {
		return err
	}
	if _, err := writer.Write(buf.Bytes()); err != nil {
		return err
	}
	return writer.Flush()
}
