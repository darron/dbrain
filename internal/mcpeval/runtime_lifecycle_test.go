package mcpeval

import (
	"context"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
)

func TestEvalResearchCasesUseReportMCPServerAndAskCasesStayIndependent(t *testing.T) {
	cfg, st := newMCPEvalStore(t)
	server := mcpserver.New(cfg, st)
	if err := server.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}

	_, err := runCase(context.Background(), cfg, st, server, Case{
		Name:                "research",
		Question:            "Alpha eval",
		MinExactTagEvidence: 1,
	})
	if err == nil {
		t.Fatal("research-pack case ignored the report MCP server lifecycle")
	}

	_, err = runCase(context.Background(), cfg, st, server, Case{
		Name:     "ask",
		Question: "Alpha eval",
	})
	if err != nil {
		t.Fatalf("ordinary ask case changed with closed MCP server: %v", err)
	}
}

func newMCPEvalStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
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
	t.Cleanup(func() { _ = st.Close() })
	return cfg, st
}
