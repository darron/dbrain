package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestMCPGetAndResourceRejectVaultEscape(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "sentinel.md"), []byte("outside sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(cfg.VaultDir, "escape")); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "item:mcp-escape",
		SourceType:   "test",
		ExternalID:   "mcp-escape",
		CanonicalURL: "https://example.test/mcp-escape",
		Title:        "MCP escape",
		LinksJSON:    "[]",
		ContentHash:  "mcp-escape",
		NotePath:     "escape/sentinel.md",
		RawJSON:      "{}",
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	if payload, _, err := server.getPayloadForLookup(context.Background(), "item:mcp-escape", getModeRendered, 1000, ""); err == nil {
		if content, _ := payload["content"].(string); strings.Contains(content, "outside sentinel") {
			t.Fatal("MCP get returned outside sentinel")
		}
		t.Fatal("expected MCP get to reject escaping note path")
	}

	resource, err := server.readItemResource(context.Background(), "dbrain://item/item:mcp-escape", "item:mcp-escape")
	if err != nil {
		t.Fatalf("read item resource: %v", err)
	}
	if strings.Contains(resource[0]["text"], "outside sentinel") {
		t.Fatal("MCP resource returned outside sentinel")
	}
	if !strings.Contains(resource[0]["text"], "Note unreadable") {
		t.Fatalf("expected unreadable marker, got %q", resource[0]["text"])
	}
}

func TestMCPGetAndResourceAllowContainedNote(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	notePath := "items/contained.md"
	absolute := filepath.Join(cfg.VaultDir, filepath.FromSlash(notePath))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("contained note"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "item:mcp-contained",
		SourceType:   "test",
		ExternalID:   "mcp-contained",
		CanonicalURL: "https://example.test/mcp-contained",
		Title:        "MCP contained",
		LinksJSON:    "[]",
		ContentHash:  "mcp-contained",
		NotePath:     notePath,
		RawJSON:      "{}",
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	server := New(cfg, st)
	payload, _, err := server.getPayloadForLookup(context.Background(), "item:mcp-contained", getModeRendered, 1000, "")
	if err != nil {
		t.Fatalf("MCP get contained note: %v", err)
	}
	if content, _ := payload["content"].(string); content != "contained note" {
		t.Fatalf("MCP get content = %q", content)
	}
	resource, err := server.readItemResource(context.Background(), "dbrain://item/item:mcp-contained", "item:mcp-contained")
	if err != nil {
		t.Fatalf("read contained item resource: %v", err)
	}
	if !strings.Contains(resource[0]["text"], "contained note") {
		t.Fatalf("contained resource omitted note: %q", resource[0]["text"])
	}
}
