package researchhybrid

import (
	"math"
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/retrieval"
)

func TestLaneStatusesKeepSemanticDisabledUntilConfigured(t *testing.T) {
	t.Parallel()

	lanes := LaneStatuses(Options{UseSemantic: true})
	if len(lanes) != 2 {
		t.Fatalf("expected lexical and semantic lane statuses, got %#v", lanes)
	}
	if lanes[0].Name != LaneLexical || lanes[0].Status != StatusUsed {
		t.Fatalf("expected lexical lane used, got %#v", lanes[0])
	}
	if lanes[1].Name != LaneSemantic || lanes[1].Status != StatusDisabled || lanes[1].Reason != "not_configured" {
		t.Fatalf("expected semantic lane not_configured, got %#v", lanes[1])
	}

	lanes = LaneStatuses(Options{DisableSemantic: true})
	if lanes[1].Reason != "disabled_for_lexical_debugging" {
		t.Fatalf("expected explicit disable reason, got %#v", lanes[1])
	}
}

func TestFuseExactRRFMathAndChunkIdentity(t *testing.T) {
	lex := []retrieval.EvidenceDocument{chunkDoc("parent", "a", 0, 10), chunkDoc("parent", "b", 20, 30)}
	lex[1].Chunk.SectionOrdinal = 2
	raw := 12.5
	lex[0].Retrieval = &retrieval.RetrievalInfo{Score: 99, Lanes: []retrieval.RetrievalLane{{Name: LaneLexical, RawScore: &raw}}}
	sem := []retrieval.EvidenceDocument{chunkDoc("parent", "a", 0, 10), chunkDoc("parent", "b", 20, 30)}
	sem[1].Chunk.SectionOrdinal = 2
	distance := .2
	sem[0].Retrieval = &retrieval.RetrievalInfo{Lanes: []retrieval.RetrievalLane{{Name: LaneSemantic, Rank: 9, RawDistance: &distance, Profile: "p", Backend: "exact", Generation: "g"}}}
	got := Fuse(lex, sem, 10, 100, nil)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Chunk.ID != "a" || got[0].Retrieval.FusedScore == nil || math.Abs(*got[0].Retrieval.FusedScore-2.0/61.0) > 1e-12 {
		t.Fatalf("first=%+v", got[0])
	}
	if len(got[0].Retrieval.Lanes) != 2 || got[0].Retrieval.Lanes[0].Rank != 1 || got[0].Retrieval.Lanes[0].Contribution == nil || math.Abs(*got[0].Retrieval.Lanes[0].Contribution-1.0/61.0) > 1e-12 {
		t.Fatalf("lanes=%+v", got[0].Retrieval.Lanes)
	}
	if got[1].Chunk.ID != "b" {
		t.Fatalf("same-parent distinct chunks were collapsed: %+v", got)
	}
}

func TestFuseLexicalIdentityWhenSemanticEmptyAndCapsDepthAndLimit(t *testing.T) {
	lex := []retrieval.EvidenceDocument{{SourceKey: "one"}, {SourceKey: "two"}}
	got := Fuse(lex, nil, 1, 100, nil)
	if !reflect.DeepEqual(got, lex) {
		t.Fatalf("lexical fallback mutated: got %#v want %#v", got, lex)
	}
	if gotInvalid := Fuse(lex, []retrieval.EvidenceDocument{{}}, 1, 100, nil); !reflect.DeepEqual(gotInvalid, lex) {
		t.Fatalf("invalid semantic row mutated lexical fallback: %#v", gotInvalid)
	}
	sem := make([]retrieval.EvidenceDocument, 60)
	for i := range sem {
		sem[i] = chunkDoc(string(rune('a'+i)), string(rune('a'+i)), 0, 1)
	}
	got = Fuse(nil, sem, 30, 100, nil)
	if len(got) != DefaultFusedCandidateWindow {
		t.Fatalf("len=%d", len(got))
	}
}

func TestFuseProtectedTieAndMissingRanks(t *testing.T) {
	lex := []retrieval.EvidenceDocument{chunkDoc("z", "z", 0, 1), chunkDoc("a", "a", 0, 1)}
	sem := []retrieval.EvidenceDocument{chunkDoc("a", "a", 0, 1), chunkDoc("z", "z", 0, 1)}
	got := Fuse(lex, sem, 1, 100, map[string]struct{}{"z": {}})
	if len(got) != 1 || got[0].SourceKey != "z" {
		t.Fatalf("protected tie lost: %+v", got)
	}
}

func TestFuseParentLexicalRowMergesIntoSemanticChunk(t *testing.T) {
	lex := []retrieval.EvidenceDocument{{SourceKey: "p", Excerpt: "parent", Retrieval: &retrieval.RetrievalInfo{MatchedTerms: []string{"alpha"}, MissingTerms: []string{"beta"}}}}
	sem := []retrieval.EvidenceDocument{chunkDoc("p", "chunk", 0, 5)}
	got := Fuse(lex, sem, 5, 100, nil)
	if len(got) != 1 || got[0].Chunk == nil || got[0].Chunk.ID != "chunk" || len(got[0].Retrieval.Lanes) != 2 || got[0].Retrieval.FusedScore == nil || math.Abs(*got[0].Retrieval.FusedScore-2.0/61.0) > 1e-12 {
		t.Fatalf("got=%+v", got)
	}
}

func TestFuseRetainsProtectedCandidateBeyondScoreCutoff(t *testing.T) {
	lex := []retrieval.EvidenceDocument{chunkDoc("top", "top", 0, 1), chunkDoc("protected", "protected", 0, 1)}
	sem := []retrieval.EvidenceDocument{chunkDoc("top", "top", 0, 1), chunkDoc("other", "other", 0, 1), chunkDoc("protected", "protected", 0, 1)}
	got := Fuse(lex, sem, 1, 100, map[string]struct{}{"protected": {}})
	if len(got) != 1 || got[0].SourceKey != "protected" {
		t.Fatalf("got=%+v", got)
	}
}

func TestFuseRecomputesProtectionAfterLaneMerge(t *testing.T) {
	lex := []retrieval.EvidenceDocument{chunkDoc("top", "top", 0, 1), chunkDoc("late", "late", 0, 1)}
	sem := []retrieval.EvidenceDocument{chunkDoc("top", "top", 0, 1), chunkDoc("late", "late", 0, 1)}
	sem[1].Retrieval = &retrieval.RetrievalInfo{Lanes: []retrieval.RetrievalLane{{Name: LaneExactTag, Status: StatusUsed}}}
	got := Fuse(lex, sem, 1, 100, nil)
	if len(got) != 1 || got[0].SourceKey != "late" {
		t.Fatalf("got=%+v", got)
	}
}

func TestFuseMergeRemovesMatchedTermsFromMissing(t *testing.T) {
	lex := []retrieval.EvidenceDocument{chunkDoc("p", "a", 0, 1)}
	lex[0].Retrieval = &retrieval.RetrievalInfo{MatchedTerms: []string{"alpha"}, MissingTerms: []string{"beta"}}
	sem := []retrieval.EvidenceDocument{chunkDoc("p", "a", 0, 1)}
	sem[0].Retrieval = &retrieval.RetrievalInfo{MatchedTerms: []string{"beta"}, MissingTerms: []string{"alpha"}}
	got := Fuse(lex, sem, 5, 100, nil)
	if len(got[0].Retrieval.MissingTerms) != 0 {
		t.Fatalf("retrieval=%+v", got[0].Retrieval)
	}
}

func chunkDoc(parent, id string, start, end int) retrieval.EvidenceDocument {
	return retrieval.EvidenceDocument{SourceKey: parent, Excerpt: id, EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: id, ParentSourceKey: parent, StartChar: start, EndChar: end, SectionOrdinal: 1, Hash: "hash-" + id, ContributingIDs: []string{id}}}
}

func TestMergeKeepsLexicalOrderBeforeVectorOnlyNearMatches(t *testing.T) {
	t.Parallel()

	lexical := []retrieval.EvidenceDocument{
		{
			SourceKey: "src:exact",
			Title:     "Exact lexical evidence",
			Retrieval: &retrieval.RetrievalInfo{Score: 40, Signals: []retrieval.RetrievalSignal{{
				Name:   "exact_phrase_title",
				Detail: "exact",
				Weight: 15,
			}}},
		},
	}
	semantic := []retrieval.EvidenceDocument{
		{
			SourceKey: "src:near",
			Title:     "Semantic near match",
			Retrieval: &retrieval.RetrievalInfo{Score: 90, Signals: []retrieval.RetrievalSignal{{
				Name:   "vector_similarity",
				Detail: "near meaning",
				Weight: 90,
			}}},
		},
	}

	merged := Merge(lexical, semantic, 2)
	if got := []string{merged[0].SourceKey, merged[1].SourceKey}; got[0] != "src:exact" || got[1] != "src:near" {
		t.Fatalf("expected lexical evidence to remain first, got %#v", got)
	}
	if len(merged[0].Retrieval.Lanes) != 1 || merged[0].Retrieval.Lanes[0].Name != LaneLexical {
		t.Fatalf("expected lexical lane provenance, got %#v", merged[0].Retrieval)
	}
	if len(merged[1].Retrieval.Lanes) != 1 || merged[1].Retrieval.Lanes[0].Name != LaneSemantic {
		t.Fatalf("expected semantic lane provenance, got %#v", merged[1].Retrieval)
	}
}

func TestMergeDeduplicatesAndPreservesBothLaneProvenance(t *testing.T) {
	t.Parallel()

	merged := Merge(
		[]retrieval.EvidenceDocument{{SourceKey: "src:shared", Retrieval: &retrieval.RetrievalInfo{Score: 20}}},
		[]retrieval.EvidenceDocument{{SourceKey: "src:shared", Retrieval: &retrieval.RetrievalInfo{Score: 70}}},
		5,
	)
	if len(merged) != 1 {
		t.Fatalf("expected duplicate source to merge, got %#v", merged)
	}
	var names []string
	for _, lane := range merged[0].Retrieval.Lanes {
		names = append(names, lane.Name)
	}
	if len(names) != 2 || names[0] != LaneLexical || names[1] != LaneSemantic {
		t.Fatalf("expected lexical and semantic provenance, got %#v", names)
	}
}
