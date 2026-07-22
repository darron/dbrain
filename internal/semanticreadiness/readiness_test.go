package semanticreadiness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestReadinessPriorityAndBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	ready := readySnapshot(now)
	catching := func() Snapshot {
		s := ready
		s.ExpectedParents = 1_500
		s.PendingParents = 500
		s.DirtyParents = 500
		s.EstimatedNotReadyChunks = 2_500
		s.OldestDirtyAt = now.Add(-30 * time.Minute)
		return s
	}
	tests := []struct {
		name       string
		edit       func(*Snapshot)
		want       State
		searchable bool
	}{
		{"not configured outranks unavailable", func(s *Snapshot) { s.Configured = false; s.Available = false }, StateNotConfigured, false},
		{"disabled outranks unavailable", func(s *Snapshot) { s.Enabled = false; s.Available = false }, StateDisabled, false},
		{"unavailable", func(s *Snapshot) { s.Available = false }, StateUnavailable, false},
		{"planner unavailable", func(s *Snapshot) { s.PlanningError = "input exceeds readiness ceiling" }, StateUnavailable, false},
		{"profile provenance mismatch is corrupt", func(s *Snapshot) { s.ProfileProvenanceValid = false }, StateCorrupt, false},
		{"purge epoch mismatch is corrupt", func(s *Snapshot) { s.ProfilePurgeEpoch++ }, StateCorrupt, false},
		{"revision zero is corrupt", func(s *Snapshot) { s.RevisionZeroEmbeddings = 1 }, StateCorrupt, false},
		{"latest revision counter drift is corrupt", func(s *Snapshot) { s.ObservedLatestRevision++ }, StateCorrupt, false},
		{"l0 counter drift is corrupt", func(s *Snapshot) { s.ObservedL0ReadyCount++ }, StateCorrupt, false},
		{"unproven active root is corrupt", func(s *Snapshot) { s.ActiveGenerationID = "root"; s.ActiveGenerationValid = false }, StateCorrupt, false},
		{"generation error is corrupt", func(s *Snapshot) { s.ErrorGenerations = 1 }, StateCorrupt, false},
		{"building", func(s *Snapshot) {
			s.BuildingGenerations = 1
			s.PendingEmbeddings = 1
			s.EstimatedNotReadyChunks = 2_501
		}, StateBuilding, false},
		{"stale", func(s *Snapshot) { s.StaleGenerations = 1; s.PendingEmbeddings = 1; s.EstimatedNotReadyChunks = 2_501 }, StateStale, false},
		{"projection error is degraded", func(s *Snapshot) { s.ErrorParents = 1 }, StateDegradedBlocked, false},
		{"unclassified embedding error is degraded", func(s *Snapshot) { s.UnclassifiedErrors = 1 }, StateDegradedBlocked, false},
		{"projection debt over budget needs projection", func(s *Snapshot) { s.PendingParents = 1; s.DirtyParents = 501; s.EstimatedNotReadyChunks = 1 }, StateNeedsProjection, false},
		{"scheduled retry over budget", func(s *Snapshot) { s.ScheduledRetries = 1; s.EstimatedNotReadyChunks = 2_501 }, StateRetryScheduled, false},
		{"due retry over budget needs embeddings", func(s *Snapshot) { s.DueRetries = 1; s.EstimatedNotReadyChunks = 2_501 }, StateNeedsEmbeddings, false},
		{"complete profile above exact cap needs index", func(s *Snapshot) {
			s.ChunkCount = 25_001
			s.ReadyEmbeddings = 25_001
			s.ExactMaxChunks = 25_000
			s.L0ReadyCount = 25_001
			s.ObservedL0ReadyCount = 25_001
		}, StateNeedsIndex, false},
		{"configured exact cap cannot exceed safety ceiling", func(s *Snapshot) {
			s.ChunkCount = 25_001
			s.ReadyEmbeddings = 25_001
			s.ExactMaxChunks = 300_000
			s.L0ReadyCount = 25_001
			s.ObservedL0ReadyCount = 25_001
		}, StateNeedsIndex, false},
		{"exact cap is ready", func(s *Snapshot) {
			s.ChunkCount = 25_000
			s.ReadyEmbeddings = 25_000
			s.ExactMaxChunks = 25_000
			s.L0ReadyCount = 25_000
			s.ObservedL0ReadyCount = 25_000
		}, StateReady, true},
		{"99.9 ready and 0.1 blocked passes", func(s *Snapshot) { s.ReadyEmbeddings = 999; s.BlockedEmbeddings = 1; s.ParentsWithReadyChunk = 999 }, StateReady, true},
		{"below 99.9 ready fails", func(s *Snapshot) { s.ReadyEmbeddings = 998; s.BlockedEmbeddings = 2; s.ParentsWithReadyChunk = 998 }, StateDegradedBlocked, false},
		{"blocked above 0.1 fails", func(s *Snapshot) { s.ReadyEmbeddings = 998; s.BlockedEmbeddings = 2 }, StateDegradedBlocked, false},
		{"parent coverage below 99.9 fails", func(s *Snapshot) { s.ParentsWithReadyChunk = 998 }, StateDegradedBlocked, false},
		{"ready exact profile", func(*Snapshot) {}, StateReady, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := ready
			tc.edit(&s)
			got := Evaluate(s)
			if got.State != tc.want || got.Searchable != tc.searchable || strings.TrimSpace(got.Reason) == "" {
				t.Fatalf("Evaluate()=%+v want state=%s searchable=%t", got, tc.want, tc.searchable)
			}
		})
	}

	catchTests := []struct {
		name string
		edit func(*Snapshot)
		want State
	}{
		{"exact catch-up boundary", func(*Snapshot) {}, StateCatchingUp},
		{"501 dirty parents", func(s *Snapshot) { s.DirtyParents = 501 }, StateNeedsProjection},
		{"501 pending parents", func(s *Snapshot) { s.PendingParents = 501 }, StateNeedsProjection},
		{"2501 estimated chunks", func(s *Snapshot) { s.EstimatedNotReadyChunks = 2_501 }, StateNeedsProjection},
		{"dirty age above 30 minutes", func(s *Snapshot) { s.OldestDirtyAt = now.Add(-30*time.Minute - time.Nanosecond) }, StateNeedsProjection},
		{"scheduled retry within catch-up", func(s *Snapshot) { s.ScheduledRetries = 1 }, StateCatchingUp},
		{"due retry within catch-up", func(s *Snapshot) { s.DueRetries = 1 }, StateCatchingUp},
		{"blocked coverage over limit cannot catch up", func(s *Snapshot) { s.ReadyEmbeddings = 998; s.BlockedEmbeddings = 2 }, StateDegradedBlocked},
		{"blocked parent cannot catch up", func(s *Snapshot) { s.BlockedParents = 1 }, StateDegradedBlocked},
		{"active root l0 5000 ready target", func(s *Snapshot) { makeValidRoot(s, 5_000, 1) }, StateCatchingUp},
		{"active root l0 10000 catch-up limit", func(s *Snapshot) { makeValidRoot(s, 10_000, 2) }, StateCatchingUp},
		{"active root l0 10001 rejected", func(s *Snapshot) { makeValidRoot(s, 10_001, 2) }, StateNeedsIndex},
		{"active tombstones two percent boundary", func(s *Snapshot) { makeValidRoot(s, 10, 20) }, StateCatchingUp},
		{"active tombstones above two percent", func(s *Snapshot) { makeValidRoot(s, 10, 21) }, StateNeedsIndex},
	}
	for _, tc := range catchTests {
		t.Run(tc.name, func(t *testing.T) {
			s := catching()
			tc.edit(&s)
			got := Evaluate(s)
			if got.State != tc.want || got.Searchable != (tc.want == StateCatchingUp) {
				t.Fatalf("Evaluate()=%+v want=%s", got, tc.want)
			}
		})
	}
}

func TestReadinessUsesIntegerRatiosAtLargeCounts(t *testing.T) {
	s := readySnapshot(time.Now())
	s.ChunkCount, s.ReadyEmbeddings, s.BlockedEmbeddings = 1_000_000_000, 999_000_000, 1_000_000
	s.ChunkableParents, s.ParentsWithReadyChunk = 1_000_000_000, 999_000_000
	s.L0ReadyCount, s.ObservedL0ReadyCount = s.ReadyEmbeddings, s.ReadyEmbeddings
	makeValidRoot(&s, 5_000, 10_000_000)
	s.ActiveIndexedCount = 1_000_000_000
	if got := Evaluate(s); got.State != StateReady {
		t.Fatalf("exact integer boundary rejected: %+v", got)
	}
}

func TestEmbeddingDebtUsesSameCatchUpBudget(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	base := readySnapshot(now)
	base.ChunkCount, base.ReadyEmbeddings, base.PendingEmbeddings = 2_501, 1, 2_500
	base.ChunkableParents, base.ParentsWithReadyChunk = 1, 1
	base.EstimatedNotReadyChunks = 2_500
	base.L0ReadyCount, base.ObservedL0ReadyCount = 1, 1
	if got := Evaluate(base); got.State != StateCatchingUp || !got.Searchable {
		t.Fatalf("2,500 pending boundary=%+v", got)
	}
	base.ChunkCount, base.PendingEmbeddings, base.EstimatedNotReadyChunks = 2_502, 2_501, 2_501
	if got := Evaluate(base); got.State != StateNeedsEmbeddings || got.Searchable {
		t.Fatalf("2,501 pending boundary=%+v", got)
	}
}

func TestEmbeddingDebtUsesSameCatchUpAge(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	base := readySnapshot(now)
	base.ChunkCount, base.ReadyEmbeddings, base.PendingEmbeddings = 2, 1, 1
	base.EstimatedNotReadyChunks = 1
	base.L0ReadyCount, base.ObservedL0ReadyCount = 1, 1
	base.OldestDirtyAt = now.Add(-30 * time.Minute)
	if got := Evaluate(base); got.State != StateCatchingUp {
		t.Fatalf("30 minute boundary=%+v", got)
	}
	base.OldestDirtyAt = now.Add(-30*time.Minute - time.Nanosecond)
	if got := Evaluate(base); got.State != StateNeedsEmbeddings || got.Searchable {
		t.Fatalf("30 minute epsilon=%+v", got)
	}
}

func TestEstimateDirtyParentsUsesExactCappedV3Planning(t *testing.T) {
	newParent := DirtyParent{Parent: retrievalchunk.Parent{Kind: "source", SourceKey: "new", Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: "hello 🌍"}}}}
	projection, err := retrievalchunk.BuildProjection(newParent.Parent, retrievalchunk.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	got, err := EstimateDirtyParents(context.Background(), []DirtyParent{newParent}, 2_500)
	if err != nil || got != len(projection.Occurrences) {
		t.Fatalf("estimate=%d err=%v occurrences=%d", got, err, len(projection.Occurrences))
	}
	existing := newParent
	existing.LastCurrentChunkCount = got + 7
	got, err = EstimateDirtyParents(context.Background(), []DirtyParent{existing}, 2_500)
	if err != nil || got != existing.LastCurrentChunkCount {
		t.Fatalf("existing estimate=%d err=%v", got, err)
	}
}

func TestEstimateDirtyParentsReturnsExactBudgetSentinelAndCancellation(t *testing.T) {
	parents := make([]DirtyParent, 2_501)
	for i := range parents {
		parents[i].Parent = retrievalchunk.Parent{Kind: "source", SourceKey: fmt.Sprintf("source:%04d", i), Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: "x"}}}
	}
	if got, err := EstimateDirtyParents(context.Background(), parents[:2_500], 2_500); err != nil || got != 2_500 {
		t.Fatalf("at cap estimate=%d err=%v", got, err)
	}
	if got, err := EstimateDirtyParents(context.Background(), parents, 2_500); err != nil || got != 2_501 {
		t.Fatalf("over cap estimate=%d err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EstimateDirtyParents(ctx, parents, 2_500); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func TestEstimateDirtyParentStreamStopsLoadingAtOverBudgetSentinel(t *testing.T) {
	loaded := 0
	got, err := EstimateDirtyParentStream(context.Background(), 2_500, func() (DirtyParent, bool, error) {
		loaded++
		if loaded > 2_501 {
			return DirtyParent{}, false, errors.New("loaded parent after sentinel")
		}
		return DirtyParent{Parent: retrievalchunk.Parent{
			Kind: "source", SourceKey: fmt.Sprintf("source:%04d", loaded),
			Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: "x"}},
		}}, true, nil
	})
	if err != nil || got != 2_501 || loaded != 2_501 {
		t.Fatalf("estimate=%d loaded=%d err=%v", got, loaded, err)
	}
}

func readySnapshot(now time.Time) Snapshot {
	return Snapshot{
		Configured: true, Enabled: true, Available: true,
		ProfileExists: true, ProfileProvenanceValid: true,
		ExpectedParents: 1_000, CurrentParents: 1_000, ChunkableParents: 1_000,
		ParentsWithReadyChunk: 1_000, ChunkCount: 1_000, ReadyEmbeddings: 1_000,
		GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
		LatestRevision: 1_000, ObservedLatestRevision: 1_000,
		L0ReadyCount: 1_000, ObservedL0ReadyCount: 1_000,
		ExactMaxChunks: 25_000, Now: now,
	}
}

func makeValidRoot(s *Snapshot, l0, tombstones int) {
	s.ActiveGenerationID = "root"
	s.ActiveGenerationValid = true
	s.ActiveIndexedCount = 1_000
	s.L0ReadyCount, s.ObservedL0ReadyCount = l0, l0
	s.ActiveTombstones = tombstones
}
