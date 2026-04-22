package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestServerInitializeAndToolsList(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)

	input := framedJSON(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(responses))
	}

	initResult := responses[0]["result"].(map[string]interface{})
	if initResult["protocolVersion"] != protocolVersion {
		t.Fatalf("unexpected protocol version: %#v", initResult["protocolVersion"])
	}
	capabilities := initResult["capabilities"].(map[string]interface{})
	if _, ok := capabilities["resources"]; !ok {
		t.Fatalf("expected resources capability: %#v", capabilities)
	}
	if _, ok := capabilities["prompts"]; !ok {
		t.Fatalf("expected prompts capability: %#v", capabilities)
	}

	listResult := responses[1]["result"].(map[string]interface{})
	tools := listResult["tools"].([]interface{})
	if len(tools) == 0 {
		t.Fatal("expected non-empty tools list")
	}
	firstTool := tools[0].(map[string]interface{})
	if _, ok := firstTool["outputSchema"]; !ok {
		t.Fatalf("expected tool output schema, got %#v", firstTool)
	}
	var foundResearchPack bool
	for _, entry := range tools {
		tool := entry.(map[string]interface{})
		if tool["name"] == "dbrain_research_pack" {
			foundResearchPack = true
			break
		}
	}
	if !foundResearchPack {
		t.Fatalf("expected dbrain_research_pack in tools list: %#v", tools)
	}
}

func TestServerListsResourcesTemplatesAndPrompts(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)

	input := framedJSON(`{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":2,"method":"resources/templates/list","params":{}}`) +
		framedJSON(`{"jsonrpc":"2.0","id":3,"method":"prompts/list","params":{}}`)

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if got := len(responses); got != 3 {
		t.Fatalf("expected 3 responses, got %d", got)
	}

	resources := responses[0]["result"].(map[string]interface{})["resources"].([]interface{})
	if len(resources) == 0 {
		t.Fatal("expected non-empty resources list")
	}
	var foundOverview bool
	for _, entry := range resources {
		resource := entry.(map[string]interface{})
		if resource["uri"] == "dbrain://mcp/overview" {
			foundOverview = true
			break
		}
	}
	if !foundOverview {
		t.Fatalf("expected mcp overview resource in %#v", resources)
	}
	templates := responses[1]["result"].(map[string]interface{})["resourceTemplates"].([]interface{})
	if len(templates) == 0 {
		t.Fatal("expected non-empty resource templates list")
	}
	prompts := responses[2]["result"].(map[string]interface{})["prompts"].([]interface{})
	if len(prompts) == 0 {
		t.Fatal("expected non-empty prompts list")
	}
	var foundTopicMap bool
	var foundTopicBrief bool
	var foundEntityBrowse bool
	var foundResearchTemplate bool
	for _, entry := range templates {
		template := entry.(map[string]interface{})
		if template["uriTemplate"] == "dbrain://research/{query}" {
			foundResearchTemplate = true
			break
		}
	}
	if !foundResearchTemplate {
		t.Fatalf("expected research resource template in %#v", templates)
	}
	for _, entry := range prompts {
		prompt := entry.(map[string]interface{})
		if prompt["name"] == "brain_topic_map" {
			foundTopicMap = true
		}
		if prompt["name"] == "brain_topic_brief" {
			foundTopicBrief = true
		}
		if prompt["name"] == "brain_entity_browse" {
			foundEntityBrowse = true
		}
	}
	if !foundTopicMap {
		t.Fatalf("expected brain_topic_map prompt in %#v", prompts)
	}
	if !foundTopicBrief {
		t.Fatalf("expected brain_topic_brief prompt in %#v", prompts)
	}
	if !foundEntityBrowse {
		t.Fatalf("expected brain_entity_browse prompt in %#v", prompts)
	}
}

func TestServerSearchTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	_, err = st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-search",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-search",
		CanonicalURL: "https://x.com/example/status/test-mcp-search",
		Title:        "MCP Search Item",
		Text:         "special mcp search phrase",
		ContentHash:  "mcp-search-hash",
		NotePath:     "items/x/2026/test-mcp-search.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_search","arguments":{"query":"special mcp search phrase","limit":5}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	result := responses[0]["result"].(map[string]interface{})
	if result["isError"] != false {
		t.Fatalf("expected non-error tool result: %#v", result)
	}
	structured := result["structuredContent"].(map[string]interface{})
	if int(structured["count"].(float64)) < 1 {
		t.Fatalf("expected at least one search result: %#v", structured)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "x:test-mcp-search") {
		t.Fatalf("expected search result text to contain source key, got %q", text)
	}
}

func TestServerAskRetrieveOnlyTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "gh-star:darron:test-mcp-ask",
		SourceType:   "github_star",
		ExternalID:   "test-mcp-ask",
		CanonicalURL: "https://github.com/test/mcp-ask",
		Title:        "mcp ask repo",
		ContentHash:  "mcp-ask-item-hash",
		NotePath:     "items/github/test-mcp-ask.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-ask",
		OriginalURL:   "https://github.com/test/mcp-ask",
		CanonicalURL:  "https://github.com/test/mcp-ask",
		NormalizedURL: "https://github.com/test/mcp-ask",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-ask.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/mcp-ask",
		FinalURL:     "https://github.com/test/mcp-ask",
		Title:        "mcp ask repo",
		Content:      "This project helps answer questions from a knowledge base.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "mcp-ask-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_ask","arguments":{"question":"knowledge base questions","retrieve_only":true,"limit":3}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	evidence := structured["evidence"].([]interface{})
	if len(evidence) == 0 {
		t.Fatalf("expected evidence in ask response: %#v", structured)
	}
}

func TestServerEntityMapTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-entity",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-entity",
		CanonicalURL: "https://x.com/example/status/test-mcp-entity",
		Title:        "MCP Entity Item",
		AuthorHandle: "entitymcp",
		AuthorName:   "Entity MCP",
		Text:         "entity mcp body",
		ContentHash:  "mcp-entity-hash",
		NotePath:     "items/x/2026/test-mcp-entity.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_entity_map","arguments":{"query":"entitymcp","kind":"person","limit":5}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if int(structured["count"].(float64)) != 1 {
		t.Fatalf("expected one entity result, got %#v", structured)
	}
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "x-author:entitymcp") {
		t.Fatalf("unexpected entity map text: %q", text)
	}
}

func TestServerRelatedToolForItem(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-related",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-related",
		CanonicalURL: "https://x.com/example/status/test-mcp-related",
		Title:        "MCP Related Item",
		ContentHash:  "mcp-related-item-hash",
		NotePath:     "items/x/2026/test-mcp-related.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-related",
		OriginalURL:   "https://example.com/related",
		CanonicalURL:  "https://example.com/related",
		NormalizedURL: "https://example.com/related",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-mcp-related.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_related","arguments":{"lookup":"x:test-mcp-related"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["kind"] != "item" {
		t.Fatalf("expected item related result, got %#v", structured)
	}
	related := structured["related_sources"].([]interface{})
	if len(related) != 1 {
		t.Fatalf("expected 1 related source, got %#v", related)
	}
}

func TestServerStatsItemsTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if _, err := st.UpsertItem(context.Background(), model.Item{
			SourceKey:    fmt.Sprintf("gh-star:test-mcp-stats-items-%d", i),
			SourceType:   "github_star",
			ExternalID:   fmt.Sprintf("test-mcp-stats-items-%d", i),
			CanonicalURL: fmt.Sprintf("https://github.com/test/stats-items-%d", i),
			Title:        "Stats Item",
			ContentHash:  fmt.Sprintf("mcp-stats-items-%d", i),
			NotePath:     fmt.Sprintf("items/github/test-mcp-stats-items-%d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("upsert item %d: %v", i, err)
		}
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_stats_items","arguments":{"source_type":"github_star","group_by":"none"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if got := int(structured["total"].(float64)); got != 2 {
		t.Fatalf("expected total 2, got %#v", structured)
	}
}

func TestServerStatsSourcesTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-stats-sources",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-stats-sources",
		CanonicalURL: "https://x.com/example/status/test-mcp-stats-sources",
		Title:        "MCP Stats Source Item",
		ContentHash:  "mcp-stats-source-item",
		NotePath:     "items/x/2026/test-mcp-stats-source.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-stats-source",
		OriginalURL:   "https://github.com/test/stats-source",
		CanonicalURL:  "https://github.com/test/stats-source",
		NormalizedURL: "https://github.com/test/stats-source",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-stats-source.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/stats-source",
		FinalURL:     "https://github.com/test/stats-source",
		Title:        "mcp stats source",
		Content:      "repo overview",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "mcp-stats-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_stats_sources","arguments":{"source_type":"github","extract_tool":"github-api","group_by":"extract-status"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if got := int(structured["total"].(float64)); got != 1 {
		t.Fatalf("expected total 1, got %#v", structured)
	}
}

func TestServerTopicMapTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-topic",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic",
		Title:        "Agent Memory Discussion",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory systems and retrieval",
		ContentHash:  "mcp-topic-item",
		NotePath:     "items/x/2026/test-mcp-topic.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-topic",
		OriginalURL:   "https://github.com/test/agent-memory",
		CanonicalURL:  "https://github.com/test/agent-memory",
		NormalizedURL: "https://github.com/test/agent-memory",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-topic.md",
	})
	if err != nil {
		t.Fatalf("source link: %v", err)
	}
	if _, err := st.SaveSourceExtraction(context.Background(), link.SourceID, model.ExtractResult{
		CanonicalURL: "https://github.com/test/agent-memory",
		FinalURL:     "https://github.com/test/agent-memory",
		Title:        "Agent Memory Repo",
		Content:      "Agent memory frameworks and retrieval tooling.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "github-api",
		ToolVersion:  "test",
	}, "mcp-topic-source-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_topic_map","arguments":{"topic":"agent memory","seed_limit":4,"related_limit":2}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	nodes := structured["nodes"].([]interface{})
	edges := structured["edges"].([]interface{})
	entities := structured["entities"].([]interface{})
	pivots := structured["pivots"].(map[string]interface{})
	synthesis := structured["synthesis"].(map[string]interface{})
	if len(nodes) == 0 || len(edges) == 0 || len(entities) == 0 || len(pivots) == 0 || synthesis["overview"] == "" {
		t.Fatalf("expected non-empty topic map, got %#v", structured)
	}
}

func TestServerTopicBriefTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-topic-brief",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic-brief",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic-brief",
		Title:        "Vector Search Discussion",
		AuthorHandle: "vectorsearch",
		AuthorName:   "Vector Search",
		Text:         "vector database retrieval and ranking",
		ContentHash:  "mcp-topic-brief-item",
		NotePath:     "items/x/2026/test-mcp-topic-brief.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-topic-brief",
		OriginalURL:   "https://github.com/test/vector-search",
		CanonicalURL:  "https://github.com/test/vector-search",
		NormalizedURL: "https://github.com/test/vector-search",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-topic-brief.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_topic_brief","arguments":{"topic":"vector search","seed_limit":4,"related_limit":2}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if !strings.Contains(structured["summary"].(string), "vector search") {
		t.Fatalf("unexpected topic brief summary: %#v", structured)
	}
	if _, ok := structured["synthesis"].(map[string]interface{}); !ok {
		t.Fatalf("expected synthesis payload in topic brief: %#v", structured)
	}
	if !strings.Contains(structured["markdown"].(string), "## Suggested Starting Notes") {
		t.Fatalf("expected markdown preview in topic brief: %#v", structured)
	}
}

func TestServerResearchPackTool(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-research-pack",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-research-pack",
		CanonicalURL: "https://x.com/example/status/test-mcp-research-pack",
		Title:        "Agent Memory Discussion",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory retrieval and persistence",
		ContentHash:  "mcp-research-pack-item",
		NotePath:     "items/x/2026/test-mcp-research-pack.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if _, err := st.UpsertSourceLink(context.Background(), itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:test-mcp-research-pack",
		OriginalURL:   "https://github.com/test/agent-memory",
		CanonicalURL:  "https://github.com/test/agent-memory",
		NormalizedURL: "https://github.com/test/agent-memory",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/test-mcp-research-pack.md",
	}); err != nil {
		t.Fatalf("source link: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dbrain_research_pack","arguments":{"question":"What is agent memory?","limit":6,"include_related":true}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	structured := result["structuredContent"].(map[string]interface{})
	if structured["mode"] != "topic_brief_and_evidence" {
		t.Fatalf("expected topic brief mode, got %#v", structured)
	}
	if structured["topic"] != "agent memory" {
		t.Fatalf("expected inferred topic, got %#v", structured)
	}
	if !structured["used_topic_brief"].(bool) {
		t.Fatalf("expected used_topic_brief=true, got %#v", structured)
	}
	if _, ok := structured["topic_brief"].(map[string]interface{}); !ok {
		t.Fatalf("expected topic_brief payload, got %#v", structured)
	}
}

func TestServerReadItemResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	_, err = st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-resource",
		Title:        "MCP Resource Item",
		Text:         "resource body",
		ContentHash:  "mcp-resource-hash",
		NotePath:     "items/x/2026/test-mcp-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	notePath := filepath.Join(cfg.VaultDir, "items/x/2026/test-mcp-resource.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatalf("mkdir note dir: %v", err)
	}
	if err := os.WriteFile(notePath, []byte("# Resource Note\n\nfull note body"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://item/x%3Atest-mcp-resource"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	if len(contents) != 1 {
		t.Fatalf("expected 1 resource content, got %#v", contents)
	}
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "MCP Resource Item") || !strings.Contains(text, "full note body") {
		t.Fatalf("unexpected resource text: %q", text)
	}
}

func TestServerReadMCPOverviewResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://mcp/overview"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "dbrain_search") || !strings.Contains(text, "brain_topic_map") {
		t.Fatalf("unexpected overview text: %q", text)
	}
}

func TestServerReadStatsItemsResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if _, err := st.UpsertItem(context.Background(), model.Item{
			SourceKey:    fmt.Sprintf("x:test-mcp-stats-resource-%d", i),
			SourceType:   "x_bookmark",
			ExternalID:   fmt.Sprintf("test-mcp-stats-resource-%d", i),
			CanonicalURL: fmt.Sprintf("https://x.com/example/status/test-mcp-stats-resource-%d", i),
			Title:        "MCP Stats Resource Item",
			ContentHash:  fmt.Sprintf("mcp-stats-resource-%d", i),
			NotePath:     fmt.Sprintf("items/x/2026/test-mcp-stats-resource-%d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("upsert item %d: %v", i, err)
		}
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://stats/items?source_type=x_bookmark&group_by=none"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"total": 2`) {
		t.Fatalf("unexpected stats resource text: %q", text)
	}
}

func TestServerReadTopicResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-topic-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic-resource",
		Title:        "Vector Database Note",
		Text:         "vector database and retrieval",
		ContentHash:  "mcp-topic-resource-item",
		NotePath:     "items/x/2026/test-mcp-topic-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://topic/vector%20database?seed_limit=3&related_limit=1"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"topic": "vector database"`) {
		t.Fatalf("unexpected topic resource text: %q", text)
	}
}

func TestServerReadTopicNoteResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-topic-note-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-topic-note-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-topic-note-resource",
		Title:        "Agent Memory Topic Note",
		AuthorHandle: "agentmemory",
		AuthorName:   "Agent Memory",
		Text:         "agent memory retrieval context",
		ContentHash:  "mcp-topic-note-resource-item",
		NotePath:     "items/x/2026/test-mcp-topic-note-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://topic-note/agent%20memory?seed_limit=3&related_limit=1"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "# agent memory") || !strings.Contains(text, "## What This Topic Is") || !strings.Contains(text, "## Suggested Starting Notes") {
		t.Fatalf("unexpected topic-note resource text: %q", text)
	}
}

func TestServerReadEntityResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-entity-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-entity-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-entity-resource",
		Title:        "MCP Entity Resource Item",
		AuthorHandle: "resourceperson",
		AuthorName:   "Resource Person",
		Text:         "entity resource body",
		ContentHash:  "mcp-entity-resource-hash",
		NotePath:     "items/x/2026/test-mcp-entity-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://entity/resourceperson?kind=person&limit=5"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"key": "x-author:resourceperson"`) {
		t.Fatalf("unexpected entity resource text: %q", text)
	}
}

func TestServerReadResearchResource(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-mcp-research-resource",
		SourceType:   "x_bookmark",
		ExternalID:   "test-mcp-research-resource",
		CanonicalURL: "https://x.com/example/status/test-mcp-research-resource",
		Title:        "Agent Memory Resource",
		Text:         "agent memory resource content",
		ContentHash:  "mcp-research-resource-item",
		NotePath:     "items/x/2026/test-mcp-research-resource.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"dbrain://research/What%20is%20agent%20memory%3F?limit=5"}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	contents := result["contents"].([]interface{})
	text := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"mode": "topic_brief_and_evidence"`) {
		t.Fatalf("unexpected research resource text: %q", text)
	}
}

func TestServerPromptGetBrainResearch(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_research","arguments":{"question":"find vector database tools","source_types":"github,web","include_related":"true"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("expected 1 prompt message, got %#v", messages)
	}
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_research_pack") || !strings.Contains(content, `"github", "web"`) {
		t.Fatalf("unexpected prompt content: %q", content)
	}
}

func TestServerPromptGetBrainTopicMap(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_topic_map","arguments":{"topic":"agent memory","source_types":"github,web","max_nodes":"5"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_topic_map") || !strings.Contains(content, "dbrain_get") {
		t.Fatalf("unexpected topic map prompt: %q", content)
	}
}

func TestServerPromptGetBrainTopicBrief(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_topic_brief","arguments":{"topic":"agent memory","source_types":"github,web","max_nodes":"5"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_topic_brief") || !strings.Contains(content, "markdown preview") {
		t.Fatalf("unexpected topic brief prompt: %q", content)
	}
}

func TestServerPromptGetBrainEntityBrowse(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	server := New(cfg, st)
	req := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"brain_entity_browse","arguments":{"query":"openai","kind":"org"}}}`

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(framedJSON(req)), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	responses := parseResponses(t, out.Bytes())
	result := responses[0]["result"].(map[string]interface{})
	messages := result["messages"].([]interface{})
	content := messages[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(content, "dbrain_entity_map") || !strings.Contains(content, "openai") {
		t.Fatalf("unexpected entity prompt: %q", content)
	}
}

func framedJSON(payload string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func parseResponses(t *testing.T, data []byte) []map[string]interface{} {
	t.Helper()

	var responses []map[string]interface{}
	for len(data) > 0 {
		sep := []byte("\r\n\r\n")
		idx := bytes.Index(data, sep)
		if idx < 0 {
			t.Fatalf("missing frame separator in %q", string(data))
		}
		headers := string(data[:idx])
		var length int
		for _, line := range strings.Split(headers, "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")), "%d", &length); err != nil {
					t.Fatalf("parse content length: %v", err)
				}
			}
		}
		start := idx + len(sep)
		end := start + length
		if end > len(data) {
			t.Fatalf("short frame: want %d bytes, have %d", length, len(data)-start)
		}
		payload := data[start:end]
		var decoded map[string]interface{}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		responses = append(responses, decoded)
		data = data[end:]
	}
	return responses
}

func TestReadNoteHelper(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.md")
	const content = "hello"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	got, err := readNote(path)
	if err != nil {
		t.Fatalf("readNote: %v", err)
	}
	if got != content {
		t.Fatalf("unexpected note content: %q", got)
	}
}
