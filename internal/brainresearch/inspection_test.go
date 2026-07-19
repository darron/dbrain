package brainresearch

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/store"
)

func TestInspectPackHydratesBoundedWindowWithoutChangingRetrievalScores(t *testing.T) {
	t.Parallel()

	cfg, st := inspectionTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rows := make([]ask.Evidence, 0, 6)
	for i := 1; i <= 6; i++ {
		key := fmt.Sprintf("x:inspection-%d", i)
		item := model.Item{
			SourceKey:    key,
			SourceType:   "x_bookmark",
			ExternalID:   fmt.Sprintf("inspection-%d", i),
			CanonicalURL: "https://example.com/" + key,
			Title:        fmt.Sprintf("Inspection row %d", i),
			Text:         "General saved note.",
			SummaryText:  "Harness engineering summary.",
			ContentHash:  fmt.Sprintf("inspection-hash-%d", i),
			NotePath:     fmt.Sprintf("items/test/inspection-%d.md", i),
			RawJSON:      `{}`,
			ImportedAt:   now,
			UpdatedAt:    now,
			LastSeenAt:   now,
		}
		if i == 2 {
			item.ArticleTitle = "Raw harness evidence"
			item.ArticleText = "The raw transcript explains harness engineering guardrails."
		}
		if _, err := st.UpsertItem(ctx, item); err != nil {
			t.Fatalf("upsert item %s: %v", key, err)
		}
		rows = append(rows, ask.Evidence{
			SourceKey: key,
			Kind:      "item",
			Title:     item.Title,
			Summary:   item.SummaryText,
			Retrieval: &ask.RetrievalInfo{Score: 100 - i, Signals: []retrieval.RetrievalSignal{{Name: "lexical", Weight: 100 - i}}},
		})
	}
	pack := Pack{
		Question: "What harness engineering guardrails are described?",
		Evidence: rows,
		QueryPlan: QueryPlan{Concepts: []QueryConcept{
			{Key: "harness", Preferred: "harness", Terms: []string{"harness"}, Required: true, Role: conceptRoleContent},
			{Key: "engineering", Preferred: "engineering", Terms: []string{"engineering"}, Required: true, Role: conceptRoleContent},
		}},
	}

	inspected, result := InspectPack(ctx, cfg, st, pack, InspectionOptions{Limit: 5, MaxChars: 1000})

	if !result.Applied || len(result.Rows) != 5 || len(result.Errors) != 0 {
		t.Fatalf("unexpected inspection result: %#v", result)
	}
	if inspected.Evidence[0].SourceKey != "x:inspection-2" {
		t.Fatalf("expected raw-backed evidence to be promoted, got order %#v with inspection %#v", evidenceOrder(inspected.Evidence), result.Rows)
	}
	if inspected.Evidence[5].SourceKey != "x:inspection-6" {
		t.Fatalf("expected evidence outside the inspection window to remain unchanged, got %#v", evidenceOrder(inspected.Evidence))
	}
	for _, row := range inspected.Evidence {
		want := 100 - int(row.SourceKey[len(row.SourceKey)-1]-'0')
		if row.Retrieval == nil || row.Retrieval.Score != want {
			t.Fatalf("retrieval score changed for %s: %#v", row.SourceKey, row.Retrieval)
		}
	}
	if inspected.Inspection == nil || !reflect.DeepEqual(*inspected.Inspection, result) {
		t.Fatalf("inspection metadata was not attached to pack: %#v", inspected.Inspection)
	}

	noConceptPack := pack
	noConceptPack.QueryPlan.Concepts = nil
	withoutVerification, noConceptResult := InspectPack(ctx, cfg, st, noConceptPack, InspectionOptions{Limit: 5, MaxChars: 1000})
	if noConceptResult.Reordered || !reflect.DeepEqual(evidenceOrder(withoutVerification.Evidence), evidenceOrder(rows)) {
		t.Fatalf("raw support without required-concept verification must not change ranking: %#v", noConceptResult)
	}
}

func TestInspectPackHydrationFailureIsRankingNeutral(t *testing.T) {
	t.Parallel()

	cfg, st := inspectionTestStore(t)
	pack := Pack{
		Question: "harness engineering",
		Evidence: []ask.Evidence{
			{SourceKey: "x:missing-one", Kind: "item", Retrieval: &ask.RetrievalInfo{Score: 20}},
			{SourceKey: "x:missing-two", Kind: "item", Retrieval: &ask.RetrievalInfo{Score: 10}},
		},
	}

	inspected, result := InspectPack(context.Background(), cfg, st, pack, InspectionOptions{Limit: 2})

	if len(result.Errors) != 2 || result.Reordered {
		t.Fatalf("expected failed hydration to preserve ranking, got %#v", result)
	}
	if got := evidenceOrder(inspected.Evidence); !reflect.DeepEqual(got, []string{"x:missing-one", "x:missing-two"}) {
		t.Fatalf("unexpected order after failed hydration: %#v", got)
	}
}

func TestInspectPackSemanticRawExcerptAndFusedScore(t *testing.T) {
	cfg, st := inspectionTestStore(t)
	now := time.Now().UTC()
	for _, key := range []string{"x:low", "x:high"} {
		_, err := st.UpsertItem(context.Background(), model.Item{SourceKey: key, SourceType: "x_bookmark", ExternalID: key, CanonicalURL: "https://example.com/" + key, Title: key, Text: "parent without selected phrase", ContentHash: key, NotePath: "items/" + key + ".md", RawJSON: "{}", ImportedAt: now, UpdatedAt: now, LastSeenAt: now})
		if err != nil {
			t.Fatal(err)
		}
	}
	low, high := .1, .2
	pack := Pack{Question: "needle", QueryPlan: QueryPlan{Concepts: []QueryConcept{{Key: "needle", Terms: []string{"needle"}, Required: true, Role: conceptRoleContent}}}, Evidence: []ask.Evidence{
		{SourceKey: "x:low", Kind: "item", Excerpt: "raw needle", EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: "low"}, Retrieval: &ask.RetrievalInfo{Score: 100, FusedScore: &low, Lanes: []retrieval.RetrievalLane{{Name: "semantic"}}}},
		{SourceKey: "x:high", Kind: "item", Excerpt: "raw needle", EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: "high"}, Retrieval: &ask.RetrievalInfo{Score: 1, FusedScore: &high, Lanes: []retrieval.RetrievalLane{{Name: "semantic"}}}},
	}}
	got, result := InspectPack(context.Background(), cfg, st, pack, InspectionOptions{Limit: 2})
	if len(result.Errors) != 0 || got.Evidence[0].SourceKey != "x:high" || len(result.Rows[0].RawMatchedConcepts) != 1 || got.Evidence[0].Chunk.ID != "high" {
		t.Fatalf("got=%+v result=%+v", got.Evidence, result)
	}
}

func TestInspectPackMissingSemanticParentDoesNotValidateStaleExcerpt(t *testing.T) {
	cfg, st := inspectionTestStore(t)
	fused := .2
	pack := Pack{Question: "needle", QueryPlan: QueryPlan{Concepts: []QueryConcept{{Key: "needle", Terms: []string{"needle"}, Required: true, Role: conceptRoleContent}}}, Evidence: []ask.Evidence{{SourceKey: "x:missing", Kind: "item", Excerpt: "raw needle", EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: "stale"}, Retrieval: &ask.RetrievalInfo{FusedScore: &fused, Lanes: []retrieval.RetrievalLane{{Name: "semantic"}}}}}}
	_, result := InspectPack(context.Background(), cfg, st, pack, InspectionOptions{Limit: 1})
	if len(result.Errors) != 1 || len(result.Rows[0].MatchedConcepts) != 0 || len(result.Rows[0].RawMatchedConcepts) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestInspectPackSemanticChunkIgnoresBroadHydratedParentForConceptProof(t *testing.T) {
	cfg, st := inspectionTestStore(t)
	now := time.Now().UTC()
	_, err := st.UpsertItem(context.Background(), model.Item{SourceKey: "x:broad", SourceType: "x_bookmark", ExternalID: "broad", CanonicalURL: "https://example.com/broad", Title: "Unrelated title", Text: "parent content contains needle", ContentHash: "broad", NotePath: "items/broad.md", RawJSON: "{}", ImportedAt: now, UpdatedAt: now, LastSeenAt: now})
	if err != nil {
		t.Fatal(err)
	}
	fused := .2
	pack := Pack{Question: "needle", QueryPlan: QueryPlan{Concepts: []QueryConcept{{Key: "needle", Terms: []string{"needle"}, Required: true, Role: conceptRoleContent}}}, Evidence: []ask.Evidence{{SourceKey: "x:broad", Kind: "item", Excerpt: "selected passage lacks it", EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: "selected"}, Retrieval: &ask.RetrievalInfo{FusedScore: &fused, Lanes: []retrieval.RetrievalLane{{Name: "semantic"}}}}}}
	_, result := InspectPack(context.Background(), cfg, st, pack, InspectionOptions{Limit: 1})
	if len(result.Errors) != 0 || len(result.Rows[0].MatchedConcepts) != 0 || len(result.Rows[0].RawMatchedConcepts) != 0 || len(result.Rows[0].MissingConcepts) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestInspectEvidenceRowSemanticRawRoleUsesSelectedExcerptAsRawSupport(t *testing.T) {
	row := ask.Evidence{SourceKey: "x:selected", Excerpt: "selected raw passage", EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: "chunk"}, Retrieval: &ask.RetrievalInfo{Lanes: []retrieval.RetrievalLane{{Name: "semantic"}}}}
	got := inspectEvidenceRow(row, []QueryConcept{{Key: "selected", Terms: []string{"selected"}, Required: true, Role: conceptRoleContent}})
	if !got.RawSupport || len(got.RawMatchedConcepts) != 1 {
		t.Fatalf("got=%+v", got)
	}
}

func TestInspectPackMixedHydrationFailureKeepsOriginalOrder(t *testing.T) {
	t.Parallel()

	cfg, st := inspectionTestStore(t)
	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:available",
		SourceType:   "x_bookmark",
		ExternalID:   "available",
		CanonicalURL: "https://example.com/available",
		Title:        "Available harness evidence",
		Text:         "Raw harness engineering evidence.",
		SummaryText:  "Harness engineering summary.",
		ContentHash:  "available-hash",
		NotePath:     "items/test/available.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert available item: %v", err)
	}
	pack := Pack{
		Question: "harness engineering",
		Evidence: []ask.Evidence{
			{SourceKey: "x:missing", Kind: "item", Retrieval: &ask.RetrievalInfo{Score: 20}},
			{SourceKey: "x:available", Kind: "item", Retrieval: &ask.RetrievalInfo{Score: 10}},
		},
		QueryPlan: QueryPlan{Concepts: []QueryConcept{
			{Key: "harness", Terms: []string{"harness"}, Required: true, Role: conceptRoleContent},
			{Key: "engineering", Terms: []string{"engineering"}, Required: true, Role: conceptRoleContent},
		}},
	}

	inspected, result := InspectPack(context.Background(), cfg, st, pack, InspectionOptions{Limit: 10})
	if len(result.Errors) != 1 || result.Reordered {
		t.Fatalf("one failed hydration must keep the entire inspected window ranking-neutral: %#v", result)
	}
	if got := evidenceOrder(inspected.Evidence); !reflect.DeepEqual(got, []string{"x:missing", "x:available"}) {
		t.Fatalf("unexpected mixed-hydration order: %#v", got)
	}
	if inspected.Evidence[1].Summary != "Harness engineering summary." {
		t.Fatalf("successful hydration should still refresh content: %#v", inspected.Evidence[1])
	}
}

func evidenceOrder(rows []ask.Evidence) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.SourceKey)
	}
	return keys
}

func inspectionTestStore(t *testing.T) (config.Config, *store.Store) {
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
