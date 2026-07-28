package semanticbuild

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

// These tests fail if RunProjectionBatch reads a fresh work revision instead of
// honouring its caller's immutable run watermark.
func TestProjectionBatchProcessesOnlyPinnedWatermark(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "at-watermark", ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}},
		{Kind: "item", SourceKey: "above-watermark", ContentHash: "two", Sections: []retrievalchunk.Section{{Role: "raw", Text: "two"}}},
	}}

	progress, err := RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := st.replacements, []string{"item:at-watermark"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replacements=%v, want %v", got, want)
	}
	if progress.HasMore || progress.Remaining != 0 {
		t.Fatalf("progress=%+v, want no pinned work remaining", progress)
	}
	if got, want := st.watermarks, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selector watermarks=%v, want %v", got, want)
	}
}

func TestProjectionBatchHasMoreExcludesAboveWatermark(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "one", ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}},
		{Kind: "item", SourceKey: "two", ContentHash: "two", Sections: []retrievalchunk.Section{{Role: "raw", Text: "two"}}},
		{Kind: "item", SourceKey: "later", ContentHash: "later", Sections: []retrievalchunk.Section{{Role: "raw", Text: "later"}}},
	}}

	progress, err := RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !progress.HasMore || progress.Remaining != 0 {
		t.Fatalf("progress=%+v, want only the second pinned parent reported as remaining work", progress)
	}
	if got, want := st.replacements, []string{"item:one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replacements=%v, want %v", got, want)
	}
}

func TestProjectionBatchResumesStagedGiantAtSameWatermark(t *testing.T) {
	parent := retrievalchunk.Parent{Kind: "source", SourceKey: "giant", ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "already staged"}}}
	hash, err := retrievalchunk.ParentProjectionHash(parent)
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeStore{parents: []retrievalchunk.Parent{parent}, staging: map[string]store.RetrievalProjectionCheckpoint{
		"source:giant": {WorkID: "persisted-staging", DirtyRevision: 7, ParentKind: "source", ParentSourceKey: "giant", ProjectionHash: hash, StagedChunks: 3},
	}}
	st.listDirty = func(context.Context, int64, int) ([]store.RetrievalParentWork, error) {
		return []store.RetrievalParentWork{{Parent: parent, DirtyRevision: 7}}, nil
	}

	progress, err := RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 7, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.stageCalls) != 0 || len(st.promotions) != 1 || progress.Generated != 1 {
		t.Fatalf("progress=%+v stage_calls=%d promotions=%d; want promotion of persisted staging without rebuilding it", progress, len(st.stageCalls), len(st.promotions))
	}
}

func TestProjectionBatchStaleRevisionLeavesRedirtiedWorkForSuccessor(t *testing.T) {
	parent := retrievalchunk.Parent{Kind: "item", SourceKey: "changed", ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}}
	st := &redirtyOnApplyStore{fakeStore: &fakeStore{}, parent: parent, revision: 1}

	progress, err := RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Current != 1 || progress.Remaining != 0 || progress.HasMore || st.revision != 2 || len(st.applyInputs) != 0 {
		t.Fatalf("stale first batch did not leave revision two for successor: progress=%+v revision=%d applies=%d", progress, st.revision, len(st.applyInputs))
	}
	progress, err = RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Generated != 1 || len(st.applyInputs) != 1 || st.applyInputs[0].DirtyRevision != 2 {
		t.Fatalf("successor watermark did not process revision two: progress=%+v applies=%+v", progress, st.applyInputs)
	}
}

func TestProjectionBatchValidatesBeforeStoreAccess(t *testing.T) {
	for _, opts := range []ProjectionBatchOptions{{Watermark: -1, Limit: 1}, {Watermark: 0, Limit: 0}, {Watermark: 0, Limit: 5_001}} {
		t.Run("invalid", func(t *testing.T) {
			st := &failOnProjectionBatchStore{t: t}
			if _, err := RunProjectionBatch(context.Background(), st, opts); err == nil {
				t.Fatalf("RunProjectionBatch(%+v) unexpectedly succeeded", opts)
			}
		})
	}
}

func TestProjectionBatchEmptyReturnsBoundedZeroProgress(t *testing.T) {
	st := &fakeStore{}
	progress, err := RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 42, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 0 || progress.Remaining != 0 || progress.HasMore || progress.Checkpoint != nil || progress.NextAfterSourceKey != "" || len(progress.Snapshots) != 0 || progress.LastSnapshot != nil {
		t.Fatalf("progress=%+v, want bounded empty batch", progress)
	}
}

func TestChunkUntilIdlePinsOneWatermarkForItsInvocation(t *testing.T) {
	st := &countingProjectionRevisionStore{fakeStore: &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "one", ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}},
		{Kind: "item", SourceKey: "two", ContentHash: "two", Sections: []retrievalchunk.Section{{Role: "raw", Text: "two"}}},
		{Kind: "item", SourceKey: "successor", ContentHash: "three", Sections: []retrievalchunk.Section{{Role: "raw", Text: "three"}}},
	}}}

	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, UntilIdle: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.calls != 1 || progress.Scanned != 2 {
		t.Fatalf("work-revision calls=%d progress=%+v, want one captured watermark and two parents", st.calls, progress)
	}
	if got, want := st.replacements, []string{"item:one", "item:two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replacements=%v, want %v", got, want)
	}
	if got, want := st.watermarks, []int64{2, 2, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selector watermarks=%v, want %v", got, want)
	}
}

func TestChunkLargeLimitUsesOnlyBoundedProjectionBatches(t *testing.T) {
	parents := make([]retrievalchunk.Parent, 5_001)
	for i := range parents {
		parents[i] = retrievalchunk.Parent{Kind: "item", SourceKey: fmt.Sprintf("parent-%d", i), ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}}
	}
	st := &observedProjectionBatchStore{fakeStore: &fakeStore{parents: parents}}

	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 5_001})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 5_001 || len(st.applyInputs) != 5_001 {
		t.Fatalf("progress=%+v applies=%d, want all 5001 requested parents", progress, len(st.applyInputs))
	}
	if got, want := st.listLimits, []int{5_001, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selector limits=%v, want physical batches of 5000 then 1 plus their lookahead rows", got)
	}
}

func TestChunkLargeLimitStopsBeforeSecondBatchAtDeadline(t *testing.T) {
	parents := make([]retrievalchunk.Parent, 5_001)
	for i := range parents {
		parents[i] = retrievalchunk.Parent{Kind: "item", SourceKey: fmt.Sprintf("parent-%d", i), ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}}
	}
	st := &fakeStore{parents: parents}
	base := time.Unix(2_000, 0).UTC()
	nowCalls := 0
	now := func() time.Time {
		nowCalls++
		if nowCalls <= 5_000 {
			return base
		}
		return base.Add(2 * time.Second)
	}

	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 5_001, MaxDuration: time.Second, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 5_000 || len(st.applyInputs) != 5_000 || !progress.Interrupted || !progress.HasMore {
		t.Fatalf("progress=%+v applies=%d, want deadline before second physical batch", progress, len(st.applyInputs))
	}
}

type countingProjectionRevisionStore struct {
	*fakeStore
	calls int
}

func (s *countingProjectionRevisionStore) ProjectionWorkRevision(context.Context) (int64, error) {
	s.calls++
	return int64(s.calls + 1), nil
}

type observedProjectionBatchStore struct {
	*fakeStore
	listLimits []int
}

type redirtyOnApplyStore struct {
	*fakeStore
	parent   retrievalchunk.Parent
	revision int64
	stale    bool
}

func (s *redirtyOnApplyStore) ListDirtyRetrievalParents(_ context.Context, watermark int64, _ int) ([]store.RetrievalParentWork, error) {
	if watermark < s.revision || s.applied[s.parent.Kind+":"+s.parent.SourceKey] {
		return nil, nil
	}
	return []store.RetrievalParentWork{{Parent: s.parent, DirtyRevision: s.revision}}, nil
}

func (s *redirtyOnApplyStore) ApplyRetrievalProjection(ctx context.Context, input store.ApplyRetrievalProjectionInput) (store.ChunkReplaceResult, error) {
	if !s.stale {
		s.stale = true
		s.revision = 2
		return store.ChunkReplaceResult{}, &store.RetrievalProjectionStaleWorkError{ParentKind: input.ParentKind, ParentSourceKey: input.ParentSourceKey, SelectedRevision: input.DirtyRevision, CurrentRevision: 2, Reason: "dirty revision no longer matches"}
	}
	return s.fakeStore.ApplyRetrievalProjection(ctx, input)
}

func (s *observedProjectionBatchStore) ListDirtyRetrievalParents(ctx context.Context, watermark int64, limit int) ([]store.RetrievalParentWork, error) {
	s.listLimits = append(s.listLimits, limit)
	return s.fakeStore.ListDirtyRetrievalParents(ctx, watermark, limit)
}

type failOnProjectionBatchStore struct{ t *testing.T }

func (s *failOnProjectionBatchStore) fail(method string) {
	s.t.Fatalf("invalid projection batch accessed store method %s", method)
}

func (s *failOnProjectionBatchStore) ProjectionWorkRevision(context.Context) (int64, error) {
	s.fail("ProjectionWorkRevision")
	return 0, nil
}
func (s *failOnProjectionBatchStore) ListDirtyRetrievalParents(context.Context, int64, int) ([]store.RetrievalParentWork, error) {
	s.fail("ListDirtyRetrievalParents")
	return nil, nil
}
func (s *failOnProjectionBatchStore) ApplyRetrievalProjection(context.Context, store.ApplyRetrievalProjectionInput) (store.ChunkReplaceResult, error) {
	s.fail("ApplyRetrievalProjection")
	return store.ChunkReplaceResult{}, nil
}
func (s *failOnProjectionBatchStore) LoadRetrievalProjectionStaging(context.Context, retrievalchunk.Parent, int64) (store.RetrievalProjectionCheckpoint, bool, error) {
	s.fail("LoadRetrievalProjectionStaging")
	return store.RetrievalProjectionCheckpoint{}, false, nil
}
func (s *failOnProjectionBatchStore) StageRetrievalProjectionBatch(context.Context, store.StageRetrievalProjectionInput) (store.RetrievalProjectionCheckpoint, error) {
	s.fail("StageRetrievalProjectionBatch")
	return store.RetrievalProjectionCheckpoint{}, nil
}
func (s *failOnProjectionBatchStore) PromoteRetrievalProjectionStaging(context.Context, store.RetrievalProjectionCheckpoint) (store.ChunkReplaceResult, error) {
	s.fail("PromoteRetrievalProjectionStaging")
	return store.ChunkReplaceResult{}, nil
}
func (s *failOnProjectionBatchStore) BlockRetrievalProjectionTooLarge(context.Context, retrievalchunk.Parent, int64, string) error {
	s.fail("BlockRetrievalProjectionTooLarge")
	return nil
}
