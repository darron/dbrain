package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/store"
)

func TestOKFToolDefinitionsAreReadOnlyOnly(t *testing.T) {
	t.Parallel()

	names := map[string]bool{}
	for _, tool := range toolDefinitions() {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, want := range []string{"dbrain_okf_search", "dbrain_okf_get"} {
		if !names[want] {
			t.Fatalf("missing OKF tool %s", want)
		}
	}
	for _, forbidden := range []string{"dbrain_okf_export", "dbrain_okf_validate"} {
		if names[forbidden] {
			t.Fatalf("forbidden OKF MCP tool advertised: %s", forbidden)
		}
	}
}

func TestOKFToolsSearchAndGetGeneratedBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 6, 14, 19, 0, 0, 0, time.UTC)
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:mcp-okf",
		SourceType:   "x_bookmark",
		ExternalID:   "mcp-okf",
		CanonicalURL: "https://x.com/example/status/mcp-okf",
		Title:        "MCP OKF item",
		Text:         "raw MCP OKF evidence",
		SummaryText:  "derived MCP OKF summary",
		ContentHash:  "mcp-okf-hash",
		NotePath:     "items/x/2026/mcp-okf.md",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if _, err := okf.Export(ctx, cfg, st, okf.ExportOptions{
		Profile:        okf.ProfilePrivate,
		IncludeItems:   true,
		IncludeSources: false,
		IncludeRaw:     true,
		Now:            now,
	}); err != nil {
		t.Fatalf("Export OKF: %v", err)
	}
	if !strings.HasPrefix(cfg.OKFDir, filepath.Join(root, "okf")) {
		t.Fatalf("test config OKFDir should be under root okf sibling, got %s", cfg.OKFDir)
	}

	server := New(cfg, st)
	searchResult, err := server.handleToolCall(ctx, json.RawMessage(`{"name":"dbrain_okf_search","arguments":{"query":"MCP OKF","limit":5}}`))
	if err != nil {
		t.Fatalf("dbrain_okf_search: %v", err)
	}
	if isError, _ := searchResult["isError"].(bool); isError {
		t.Fatalf("search returned tool error: %+v", searchResult)
	}
	searchPayload := searchResult["structuredContent"].(map[string]interface{})
	if searchPayload["bundle"] != cfg.OKFDir || searchPayload["count"] != 1 {
		t.Fatalf("unexpected search payload: %+v", searchPayload)
	}
	searchText := toolText(searchResult)
	if !strings.Contains(searchText, "MCP OKF item") || strings.Contains(searchText, "dbrain_okf_export") {
		t.Fatalf("unexpected search text:\n%s", searchText)
	}

	getResult, err := server.handleToolCall(ctx, json.RawMessage(`{"name":"dbrain_okf_get","arguments":{"lookup":"x:mcp-okf","include_markdown":true,"max_chars":2000}}`))
	if err != nil {
		t.Fatalf("dbrain_okf_get: %v", err)
	}
	if isError, _ := getResult["isError"].(bool); isError {
		t.Fatalf("get returned tool error: %+v", getResult)
	}
	getPayload := getResult["structuredContent"].(map[string]interface{})
	if getPayload["bundle"] != cfg.OKFDir || getPayload["include_markdown"] != true {
		t.Fatalf("unexpected get payload metadata: %+v", getPayload)
	}
	concept := getPayload["concept"].(okf.Concept)
	if concept.SourceKey != "x:mcp-okf" || concept.Type != "Item" || !strings.Contains(concept.Markdown, "raw MCP OKF evidence") {
		t.Fatalf("unexpected OKF concept: %+v", concept)
	}
}

func toolText(result map[string]interface{}) string {
	content, _ := result["content"].([]map[string]string)
	if len(content) == 0 {
		return ""
	}
	return content[0]["text"]
}
