package semanticbuild

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

func TestTask5LoadedCompleteOversizedCheckpointBlocksBeforePromotion(t *testing.T) {
	parent := retrievalchunk.Parent{
		Kind: "source", SourceKey: "loaded-oversized", ContentHash: "v1",
		Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: "authoritative evidence"}},
	}
	projectionHash, err := retrievalchunk.ParentProjectionHash(parent)
	if err != nil {
		t.Fatal(err)
	}
	identity := parent.Kind + ":" + parent.SourceKey
	st := &fakeStore{
		parents: []retrievalchunk.Parent{parent},
		staging: map[string]store.RetrievalProjectionCheckpoint{
			identity: {
				WorkID: "crash-after-complete-stage", DirtyRevision: 1,
				ParentKind: parent.Kind, ParentSourceKey: parent.SourceKey,
				ProjectionHash: projectionHash, StagedChunks: 5,
			},
		},
	}
	progress, err := runChunkWithLimits(context.Background(), st, ChunkOptions{Limit: 1}, chunkExecutionLimits{
		GiantThreshold: 2, StageBatchSize: 2, HardChunkLimit: 4,
	}, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.blockedGiant, []string{identity}) || len(st.promotions) != 0 || progress.Blocked != 1 || progress.Remaining != 0 || progress.Checkpoint != nil {
		t.Fatalf("progress=%+v blocked=%v promotions=%v", progress, st.blockedGiant, st.promotions)
	}
}

func TestTask5RepeatedOccurrencesEnterBoundedStagingBeforeUniqueThreshold(t *testing.T) {
	parent := retrievalchunk.Parent{Kind: "source", SourceKey: "repeated", ContentHash: "v1", Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: strings.Repeat("same repeated window ", 300)}}}
	st := &task5PreparedFakeStore{fakeStore: &fakeStore{parents: []retrievalchunk.Parent{parent}}}
	base := time.Unix(1_000, 0).UTC()
	times := []time.Time{base, base.Add(2 * time.Second)}
	n := 0
	now := func() time.Time { value := times[min(n, len(times)-1)]; n++; return value }
	planningCalls := 0
	prepare := func(parent retrievalchunk.Parent, opts retrievalchunk.Options, max int) (retrievalchunk.PreparedStreamPlan, error) {
		planningCalls++
		return retrievalchunk.PrepareStream(parent, opts, max)
	}
	limits := chunkExecutionLimits{
		GiantThreshold: 1_000, StageBatchSize: 5, StageBatchBytes: 1 << 20,
		HardChunkLimit: 2_000, HardOccurrenceLimit: 100_000, HardStagedBytes: 1 << 30,
	}
	chunkOpts := retrievalchunk.Options{TargetRunes: 12, MaxRunes: 16}
	progress, err := runChunkWithLimitsAndPlanner(context.Background(), st, ChunkOptions{Limit: 1, MaxDuration: time.Second, Now: now}, limits, chunkOpts, prepare)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.stageCalls) != 1 || len(st.stageCalls[0].Rows) > 5 || st.staging["source:repeated"].StagedChunks >= 1_000 || progress.Checkpoint == nil || planningCalls != 1 {
		t.Fatalf("progress=%+v stage_calls=%+v checkpoint=%+v", progress, st.stageCalls, st.staging["source:repeated"])
	}
	if _, err := runChunkWithLimitsAndPlanner(context.Background(), st, ChunkOptions{Limit: 1}, limits, chunkOpts, prepare); err != nil {
		t.Fatal(err)
	}
	if planningCalls != 1 {
		t.Fatalf("durable restart replanned boundaries %d times", planningCalls)
	}
}

type task5PreparedFakeStore struct{ *fakeStore }

func (f *task5PreparedFakeStore) StageRetrievalProjectionBatch(ctx context.Context, input store.StageRetrievalProjectionInput) (store.RetrievalProjectionCheckpoint, error) {
	cp, err := f.fakeStore.StageRetrievalProjectionBatch(ctx, input)
	if err != nil {
		return cp, err
	}
	cp.PreparedPlan = string(input.PreparedPlan)
	cp.StagedOccurrences += len(input.Rows)
	for _, row := range input.Rows {
		cp.StagedBytes += int64(stagedProjectionRowBytes(row.Chunk, row.Occurrence))
	}
	f.staging[input.ParentKind+":"+input.ParentSourceKey] = cp
	return cp, nil
}

func TestTask5LoadedCheckpointByteCapBlocksBeforePromotion(t *testing.T) {
	parent := retrievalchunk.Parent{Kind: "source", SourceKey: "loaded-byte-cap", ContentHash: "v1", Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: "evidence"}}}
	hash, _ := retrievalchunk.ParentProjectionHash(parent)
	identity := parent.Kind + ":" + parent.SourceKey
	st := &fakeStore{parents: []retrievalchunk.Parent{parent}, staging: map[string]store.RetrievalProjectionCheckpoint{
		identity: {WorkID: "byte-cap", DirtyRevision: 1, ParentKind: parent.Kind, ParentSourceKey: parent.SourceKey, ProjectionHash: hash, StagedChunks: 1, StagedOccurrences: 1, StagedBytes: 5},
	}}
	progress, err := runChunkWithLimits(context.Background(), st, ChunkOptions{Limit: 1}, chunkExecutionLimits{GiantThreshold: 2, StageBatchSize: 2, StageBatchBytes: 2, HardChunkLimit: 4, HardOccurrenceLimit: 4, HardStagedBytes: 4}, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.promotions) != 0 || !reflect.DeepEqual(st.blockedGiant, []string{identity}) || progress.Blocked != 1 {
		t.Fatalf("progress=%+v blocked=%v promotions=%v", progress, st.blockedGiant, st.promotions)
	}
}

func TestTask5MaxDurationStopsBetweenOrdinaryParents(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "source", SourceKey: "one", ContentHash: "v1", Sections: []retrievalchunk.Section{{Role: "raw", Text: "one"}}},
		{Kind: "source", SourceKey: "two", ContentHash: "v1", Sections: []retrievalchunk.Section{{Role: "raw", Text: "two"}}},
	}}
	base := time.Unix(2_000, 0).UTC()
	times := []time.Time{base, base.Add(2 * time.Second)}
	n := 0
	now := func() time.Time { value := times[min(n, len(times)-1)]; n++; return value }
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 2, MaxDuration: time.Second, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 1 || progress.Remaining != 1 || !progress.HasMore || len(st.applyInputs) != 1 {
		t.Fatalf("progress=%+v applies=%+v", progress, st.applyInputs)
	}
}
