package semanticbuild

import (
	"context"
	"reflect"
	"testing"

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

func TestProjectionBatchStaleOldRevisionLeavesSuccessorWork(t *testing.T) {
	parent := retrievalchunk.Parent{Kind: "item", SourceKey: "changed", ContentHash: "one", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}}
	st := &staleApplyProjectionStore{fakeStore: &fakeStore{parents: []retrievalchunk.Parent{parent}}}

	progress, err := RunProjectionBatch(context.Background(), st, ProjectionBatchOptions{Watermark: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Current != 1 || progress.Remaining != 0 || progress.Generated != 0 {
		t.Fatalf("progress=%+v, want stale old revision skipped", progress)
	}
	if len(st.applied) != 0 {
		t.Fatalf("stale work was marked complete: applied=%v", st.applied)
	}
}

func TestProjectionBatchValidatesBeforeStoreAccess(t *testing.T) {
	for _, opts := range []ProjectionBatchOptions{{Watermark: -1, Limit: 1}, {Watermark: 0, Limit: 0}, {Watermark: 0, Limit: 5_001}} {
		t.Run("invalid", func(t *testing.T) {
			st := &fakeStore{listDirty: func(context.Context, int64, int) ([]store.RetrievalParentWork, error) {
				t.Fatal("invalid options accessed the store")
				return nil, nil
			}}
			if _, err := RunProjectionBatch(context.Background(), st, opts); err == nil {
				t.Fatalf("RunProjectionBatch(%+v) unexpectedly succeeded", opts)
			}
			if len(st.watermarks) != 0 {
				t.Fatalf("invalid options listed work: %v", st.watermarks)
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

type staleApplyProjectionStore struct{ *fakeStore }

func (s *staleApplyProjectionStore) ApplyRetrievalProjection(context.Context, store.ApplyRetrievalProjectionInput) (store.ChunkReplaceResult, error) {
	return store.ChunkReplaceResult{}, &store.RetrievalProjectionStaleWorkError{ParentKind: "item", ParentSourceKey: "changed", SelectedRevision: 1, CurrentRevision: 2, Reason: "parent projection hash changed"}
}

type countingProjectionRevisionStore struct {
	*fakeStore
	calls int
}

func (s *countingProjectionRevisionStore) ProjectionWorkRevision(context.Context) (int64, error) {
	s.calls++
	return int64(s.calls + 1), nil
}
