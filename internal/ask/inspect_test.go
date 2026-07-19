package ask

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/store"
)

func TestInspectEvidencePreservesSemanticPassageAndProvenance(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	_, err = st.UpsertItem(context.Background(), model.Item{SourceKey: "x:semantic", SourceType: "x_bookmark", ExternalID: "semantic", CanonicalURL: "https://example.com/current", Title: "Current title", Text: "authoritative parent text", ContentHash: "h", NotePath: "items/current.md", RawJSON: "{}", ImportedAt: now, UpdatedAt: now, LastSeenAt: now})
	if err != nil {
		t.Fatal(err)
	}
	fused := .5
	original := Evidence{SourceKey: "x:semantic", Kind: "item", Title: "Old", Excerpt: "selected semantic raw excerpt", EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: "chunk", ParentSourceKey: "x:semantic", SectionOrdinal: 2, ContributingIDs: []string{"chunk"}, WindowHash: "window"}, Retrieval: &RetrievalInfo{FusedScore: &fused, Lanes: []RetrievalLane{{Name: "semantic", Rank: 1}}}}
	got, err := InspectEvidence(context.Background(), cfg, st, original, "semantic", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Current title" || got.Excerpt != original.Excerpt || got.EvidenceRole != "raw" || !reflect.DeepEqual(got.Chunk, original.Chunk) || !reflect.DeepEqual(got.Retrieval, original.Retrieval) || len(got.ContentSections) == 0 {
		t.Fatalf("got=%+v", got)
	}
}
