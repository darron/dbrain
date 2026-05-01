package brainresearch

import (
	"context"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestBuildIncludesSourceExactTagEvidence(t *testing.T) {
	t.Parallel()

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

	ctx := context.Background()
	now := time.Now().UTC()
	source, err := st.UpsertSource(ctx, model.SourceCandidate{
		SourceKey:     "src:test-source-tag-only",
		OriginalURL:   "https://example.com/agent-memory",
		CanonicalURL:  "https://example.com/agent-memory",
		NormalizedURL: "https://example.com/agent-memory",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/test-source-tag-only.md",
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, source.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/agent-memory",
		FinalURL:     "https://example.com/agent-memory",
		Title:        "Agent Memory Source",
		Content:      "Long-form source material about agent memory and retrieval.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test",
		ToolVersion:  "test",
	}, "source-tag-only-hash"); err != nil {
		t.Fatalf("save source extraction: %v", err)
	}
	if err := st.SaveSourceUserTags(ctx, source.SourceID, "agent-memory"); err != nil {
		t.Fatalf("save source tags: %v", err)
	}

	pack, err := Build(ctx, cfg, st, Options{
		Question:       "What do I have in my brain about agent memory?",
		Limit:          4,
		MaxCharsPerDoc: 120,
	})
	if err != nil {
		t.Fatalf("build pack: %v", err)
	}

	if pack.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected schema version: %q", pack.SchemaVersion)
	}
	if len(pack.ExactTagEvidence) != 1 {
		t.Fatalf("expected one source exact tag example, got %#v", pack.ExactTagEvidence)
	}
	example := pack.ExactTagEvidence[0]
	if example.SourceKey != "src:test-source-tag-only" || example.Kind != "source" {
		t.Fatalf("expected source exact tag example, got %#v", example)
	}
	if example.UserTags != "agent-memory" {
		t.Fatalf("expected source user tags, got %#v", example)
	}
	if example.Retrieval == nil || len(example.Retrieval.Signals) == 0 {
		t.Fatalf("expected retrieval signal on exact tag example, got %#v", example)
	}
}
