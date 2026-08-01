package retrievalchunk

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParentProjectionHashContextMatchesIdentityAndHonorsBounds(t *testing.T) {
	parent := Parent{
		Kind: "source", SourceKey: "bounded-hash", Title: "Title",
		Sections: []Section{{Key: "body", Role: "raw", Text: strings.Repeat("evidence ", 2_000)}},
	}
	want, err := ParentProjectionHash(parent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParentProjectionHashContext(context.Background(), parent)
	if err != nil || got != want {
		t.Fatalf("bounded hash=%q err=%v want=%q", got, err, want)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParentProjectionHashContext(ctx, parent); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash error=%v", err)
	}
	cooperativeCtx := &cancelAfterErrChecks{remaining: 3}
	if _, err := ParentProjectionHashContext(cooperativeCtx, parent); !errors.Is(err, context.Canceled) || cooperativeCtx.checks <= 3 {
		t.Fatalf("cooperative hash error=%v checks=%d", err, cooperativeCtx.checks)
	}

	oversized := parent
	oversized.Sections = []Section{{Key: "body", Role: "raw", Text: "123456789"}}
	if _, err := parentProjectionHashContext(context.Background(), oversized, V3MaximumPlanningSections, 8); err == nil || !strings.Contains(err.Error(), "planning byte ceiling") {
		t.Fatalf("oversized hash error=%v", err)
	}
}

func TestParentProjectionHashExcludesRawContentHashProvenance(t *testing.T) {
	base := Parent{
		Kind:        "source",
		SourceKey:   "source:provenance-only",
		ContentHash: "raw-hash-v1",
		Title:       "Projected title",
		SourceType:  "article",
		Author:      "example.com",
		Sections: []Section{{
			Key: "source:extract", Role: "raw", Heading: "Projected title", Text: "Projected evidence",
		}},
	}

	before, err := ParentProjectionHash(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.ContentHash = "raw-hash-v2"
	after, err := ParentProjectionHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("content-hash-only provenance update changed parent projection hash: before=%q after=%q", before, after)
	}
	beforeProjection, err := BuildProjection(base, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	afterProjection, err := BuildProjection(changed, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeProjection.Chunks) != 1 || len(afterProjection.Chunks) != 1 {
		t.Fatalf("unexpected projection chunks: before=%d after=%d", len(beforeProjection.Chunks), len(afterProjection.Chunks))
	}
	if beforeProjection.Chunks[0].ID != afterProjection.Chunks[0].ID {
		t.Fatal("content-hash-only provenance update changed chunk identity")
	}
	if beforeProjection.Chunks[0].InputContentHash != "raw-hash-v1" || afterProjection.Chunks[0].InputContentHash != "raw-hash-v2" {
		t.Fatalf("raw provenance was not retained on chunks: before=%q after=%q",
			beforeProjection.Chunks[0].InputContentHash, afterProjection.Chunks[0].InputContentHash)
	}

	changed.Title = "Actually changed projected title"
	projectedChange, err := ParentProjectionHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if projectedChange == before {
		t.Fatal("projected title update did not change parent projection hash")
	}
}
