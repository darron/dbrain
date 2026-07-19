package researchhybrid

import (
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/retrieval"
)

func TestConsolidateOverlapGapPrimaryAndDeterminism(t *testing.T) {
	primary := chunkDoc("p", "b", 3, 7)
	primary.Excerpt = "defg"
	fused := 1.0
	primary.Retrieval = &retrieval.RetrievalInfo{FusedScore: &fused}
	primary.Chunk.Index = 2
	primary.Chunk.Heading = "primary"
	left := chunkDoc("p", "a", 0, 5)
	left.Excerpt = "abcde"
	left.Chunk.Index = 1
	left.Chunk.Heading = "left"
	right := chunkDoc("p", "c", 9, 11)
	right.Excerpt = "jk"
	right.Chunk.Index = 3
	got := Consolidate([]retrieval.EvidenceDocument{primary, right, left}, 100, nil)
	if len(got) != 1 || got[0].Excerpt != "abcdefg\njk" || got[0].Chunk.ID != "b" || got[0].Chunk.Heading != "primary" || got[0].Chunk.Index != 2 || !reflect.DeepEqual(got[0].Chunk.ContributingIDs, []string{"a", "b", "c"}) || got[0].Chunk.WindowHash == "" {
		t.Fatalf("got %+v", got)
	}
	reversed := Consolidate([]retrieval.EvidenceDocument{left, right, primary}, 100, nil)
	if !reflect.DeepEqual(got, reversed) {
		t.Fatalf("nondeterministic: %#v %#v", got, reversed)
	}
}

func TestConsolidateCompatibilityBudgetCapAndNonNullEmpty(t *testing.T) {
	if got := Consolidate(nil, 10, nil); got == nil {
		t.Fatal("nil empty result")
	}
	primary := chunkDoc("p", "a", 0, 4)
	primary.Excerpt = "abcd"
	tooBig := chunkDoc("p", "b", 4, 20)
	tooBig.Excerpt = "efghijklmnop"
	otherSection := chunkDoc("p", "c", 4, 5)
	otherSection.Chunk.SectionOrdinal = 2
	otherRole := chunkDoc("p", "d", 4, 5)
	otherRole.EvidenceRole, otherRole.Chunk.Role = "summary", "summary"
	otherParent := chunkDoc("q", "e", 0, 1)
	got := Consolidate([]retrieval.EvidenceDocument{primary, tooBig, otherSection, otherRole, otherParent}, 5, nil)
	if len(got) != 4 || got[0].Excerpt != "abcd" {
		t.Fatalf("got %+v", got)
	}
}

func TestConsolidateUsesFusionTieChainAndNearestNeighbors(t *testing.T) {
	primary := chunkDoc("p", "primary", 100, 110)
	primary.Chunk.Index = 3
	primary.Excerpt = "primary"
	score := 1.0
	primary.Retrieval = &retrieval.RetrievalInfo{FusedScore: &score, Lanes: []retrieval.RetrievalLane{{Name: LaneSemantic, Rank: 1}}}
	far := chunkDoc("p", "far", 0, 10)
	far.Chunk.Index = 1
	far.Excerpt = "far"
	left := chunkDoc("p", "left", 90, 100)
	left.Chunk.Index = 2
	left.Excerpt = "left"
	right := chunkDoc("p", "right", 110, 120)
	right.Chunk.Index = 4
	right.Excerpt = "right"
	got := Consolidate([]retrieval.EvidenceDocument{far, right, left, primary}, 100, nil)
	if len(got) == 0 || got[0].Chunk.ID != "primary" || !reflect.DeepEqual(got[0].Chunk.ContributingIDs, []string{"left", "primary", "right"}) {
		t.Fatalf("got=%+v", got)
	}
}

func TestConsolidateBudgetRejectedRowRemainsWithinGlobalParentCap(t *testing.T) {
	primary := chunkDoc("p", "a", 0, 4)
	primary.Excerpt = "abcd"
	score := 1.0
	primary.Retrieval = &retrieval.RetrievalInfo{FusedScore: &score}
	big := chunkDoc("p", "b", 4, 20)
	big.Excerpt = "0123456789abcdef"
	third := chunkDoc("p", "c", 30, 31)
	third.Excerpt = "c"
	fourth := chunkDoc("p", "d", 40, 41)
	fourth.Excerpt = "d"
	got := Consolidate([]retrieval.EvidenceDocument{primary, big, third, fourth}, 5, nil)
	count := 0
	for _, row := range got {
		if row.SourceKey == "p" {
			if row.Chunk != nil {
				count += max(1, len(row.Chunk.ContributingIDs))
			}
		}
	}
	if count != 3 {
		t.Fatalf("global parent chunk count=%d got=%+v", count, got)
	}
}

func TestConsolidateExactSignalUsesProtectedExpansion(t *testing.T) {
	rows := []retrieval.EvidenceDocument{chunkDoc("p", "a", 0, 1), chunkDoc("p", "b", 1, 2), chunkDoc("p", "c", 2, 3), chunkDoc("p", "d", 3, 4)}
	for i := range rows {
		rows[i].Chunk.Index = i + 1
	}
	rows[0].Retrieval = &retrieval.RetrievalInfo{Signals: []retrieval.RetrievalSignal{{Name: "exact_phrase_title"}}}
	got := Consolidate(rows, 100, nil)
	if len(got) != 3 || len(got[0].Chunk.ContributingIDs) != 2 {
		t.Fatalf("got=%+v", got)
	}
}

func TestConsolidateOnlyExpandsOrdinalPredecessorAndSuccessor(t *testing.T) {
	primary := chunkDoc("p", "p", 20, 30)
	primary.Chunk.Index = 3
	score := 1.0
	primary.Retrieval = &retrieval.RetrievalInfo{FusedScore: &score}
	leftAdjacent := chunkDoc("p", "left-adjacent", 10, 20)
	leftAdjacent.Chunk.Index = 2
	leftFar := chunkDoc("p", "left-far", 0, 10)
	leftFar.Chunk.Index = 1
	rightGap := chunkDoc("p", "right-gap", 30, 40)
	rightGap.Chunk.Index = 5
	got := Consolidate([]retrieval.EvidenceDocument{leftFar, rightGap, leftAdjacent, primary}, 100, nil)
	if len(got) != 2 || !reflect.DeepEqual(got[0].Chunk.ContributingIDs, []string{"left-adjacent", "p"}) {
		t.Fatalf("got=%+v", got)
	}
}

func TestConsolidateIncompatibleOrdinalNeighborDoesNotConsumeQuota(t *testing.T) {
	primary := chunkDoc("p", "primary", 20, 30)
	primary.Chunk.Index = 3
	primary.Excerpt = "primary"
	score := 1.0
	primary.Retrieval = &retrieval.RetrievalInfo{FusedScore: &score}

	incompatibleLeft := chunkDoc("p", "z-incompatible-left", 10, 20)
	incompatibleLeft.Chunk.Index = 2
	incompatibleLeft.Chunk.SectionOrdinal = 2
	incompatibleLeft.Excerpt = "incompatible"
	compatibleRight := chunkDoc("p", "right", 30, 40)
	compatibleRight.Chunk.Index = 4
	compatibleRight.Excerpt = "right"
	compatibleFallback := chunkDoc("p", "a-compatible-fallback", 0, 10)
	compatibleFallback.Chunk.Index = 1
	compatibleFallback.Excerpt = "fallback"

	got := Consolidate([]retrieval.EvidenceDocument{incompatibleLeft, compatibleFallback, compatibleRight, primary}, 100, nil)
	var selected []string
	for _, row := range got {
		selected = append(selected, row.Chunk.ContributingIDs...)
	}
	if !reflect.DeepEqual(selected, []string{"primary", "right", "a-compatible-fallback"}) {
		t.Fatalf("incompatible ordinal neighbor consumed parent quota: selected=%v got=%+v", selected, got)
	}
}

func TestConsolidatePrefersCompatibleFallbackDespiteChunkIDOrder(t *testing.T) {
	primary := chunkDoc("p", "primary", 20, 30)
	primary.Chunk.Index = 3
	primary.Excerpt = "primary"
	score := 1.0
	primary.Retrieval = &retrieval.RetrievalInfo{FusedScore: &score}

	incompatibleLeft := chunkDoc("p", "a-incompatible-left", 10, 20)
	incompatibleLeft.Chunk.Index = 2
	incompatibleLeft.Chunk.SectionOrdinal = 2
	incompatibleLeft.Excerpt = "incompatible"
	compatibleRight := chunkDoc("p", "right", 30, 40)
	compatibleRight.Chunk.Index = 4
	compatibleRight.Excerpt = "right"
	compatibleFallback := chunkDoc("p", "z-compatible-fallback", 0, 10)
	compatibleFallback.Chunk.Index = 1
	compatibleFallback.Excerpt = "fallback"

	got := Consolidate([]retrieval.EvidenceDocument{incompatibleLeft, compatibleFallback, compatibleRight, primary}, 100, nil)
	var selected []string
	for _, row := range got {
		selected = append(selected, row.Chunk.ContributingIDs...)
	}
	if !reflect.DeepEqual(selected, []string{"primary", "right", "z-compatible-fallback"}) {
		t.Fatalf("chunk ID order let incompatible neighbor consume quota: selected=%v got=%+v", selected, got)
	}
}

func TestConsolidateKeepsSameSourceKeyKindsDistinct(t *testing.T) {
	item := chunkDoc("shared", "item-chunk", 0, 10)
	item.Kind = " ITEM "
	source := chunkDoc("shared", "source-chunk", 10, 20)
	source.Kind = "source"

	got := Consolidate([]retrieval.EvidenceDocument{item, source}, 100, nil)
	if len(got) != 2 {
		t.Fatalf("same-key item and source parents were consolidated: %+v", got)
	}
}

func TestSingletonWindowHashMatchesSemanticProducerEncoding(t *testing.T) {
	doc := chunkDoc("p", "a", 0, 1)
	doc.Excerpt = "x"
	doc.Chunk.Hash = "hash-a"
	got := mergeWindow(doc, []retrieval.EvidenceDocument{doc})
	want := retrieval.WindowHash([]string{"a"}, []string{"hash-a"}, "x")
	if got.Chunk.WindowHash != want {
		t.Fatalf("got %q want %q", got.Chunk.WindowHash, want)
	}
}
