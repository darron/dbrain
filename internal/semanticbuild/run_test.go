package semanticbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

type fakeStore struct {
	parents          []retrievalchunk.Parent
	workRevision     int64
	watermarks       []int64
	applied          map[string]bool
	applyInputs      []store.ApplyRetrievalProjectionInput
	chunks           []store.RetrievalChunkRow
	replacements     []string
	writes           []store.RetrievalEmbeddingRow
	writeBatches     [][]store.RetrievalEmbeddingRow
	purgeEpoch       int64
	vectorRows       []store.RetrievalVectorRow
	verification     store.RetrievalEmbeddingVerificationState
	verificationErr  error
	vectorListCalls  int
	blockErrs        []error
	blockCalls       []*store.RetrievalEmbeddingCorruptionError
	operations       []string
	candidateCount   int
	replaceResult    store.ChunkReplaceResult
	replaceErrKey    string
	candidateProfile embedding.Profile
	candidateErr     error
	staging          map[string]store.RetrievalProjectionCheckpoint
	stageCalls       []store.StageRetrievalProjectionInput
	promotions       []store.RetrievalProjectionCheckpoint
	blockedGiant     []string
	candidateTimes   []time.Time
	candidateAfters  []string
	candidateLimits  []int
	listDirty        func(context.Context, int64, int) ([]store.RetrievalParentWork, error)
	repairCalls      int
	repairErr        error
}

func (f *fakeStore) ProjectionWorkRevision(context.Context) (int64, error) {
	if f.workRevision > 0 {
		return f.workRevision, nil
	}
	return int64(len(f.parents)), nil
}

func (f *fakeStore) ListDirtyRetrievalParents(ctx context.Context, watermark int64, limit int) ([]store.RetrievalParentWork, error) {
	f.watermarks = append(f.watermarks, watermark)
	if f.listDirty != nil {
		return f.listDirty(ctx, watermark, limit)
	}
	result := make([]store.RetrievalParentWork, 0, limit)
	for i, parent := range f.parents {
		revision := int64(i + 1)
		identity := parent.Kind + ":" + parent.SourceKey
		if revision > watermark || f.applied[identity] {
			continue
		}
		result = append(result, store.RetrievalParentWork{Parent: parent, DirtyRevision: revision})
		if limit > 0 && len(result) == limit {
			break
		}
	}
	return result, nil
}

func (f *fakeStore) ApplyRetrievalProjection(_ context.Context, input store.ApplyRetrievalProjectionInput) (store.ChunkReplaceResult, error) {
	identity := input.ParentKind + ":" + input.ParentSourceKey
	f.replacements = append(f.replacements, identity)
	f.applyInputs = append(f.applyInputs, input)
	if identity == f.replaceErrKey {
		return store.ChunkReplaceResult{}, errors.New("replace failed")
	}
	if f.applied == nil {
		f.applied = make(map[string]bool)
	}
	f.applied[identity] = true
	if f.replaceResult != (store.ChunkReplaceResult{}) {
		return f.replaceResult, nil
	}
	return store.ChunkReplaceResult{Created: len(input.Projection.Chunks)}, nil
}

func (f *fakeStore) LoadRetrievalProjectionStaging(_ context.Context, parent retrievalchunk.Parent, revision int64) (store.RetrievalProjectionCheckpoint, bool, error) {
	cp, ok := f.staging[parent.Kind+":"+parent.SourceKey]
	if !ok || cp.DirtyRevision != revision {
		return store.RetrievalProjectionCheckpoint{}, false, nil
	}
	return cp, true, nil
}

func (f *fakeStore) StageRetrievalProjectionBatch(_ context.Context, input store.StageRetrievalProjectionInput) (store.RetrievalProjectionCheckpoint, error) {
	if input.ExpectedPurgeEpoch != f.purgeEpoch {
		return store.RetrievalProjectionCheckpoint{}, store.ErrRetrievalPurgeEpochChanged
	}
	f.stageCalls = append(f.stageCalls, input)
	if f.staging == nil {
		f.staging = make(map[string]store.RetrievalProjectionCheckpoint)
	}
	workID := input.WorkID
	if workID == "" {
		workID = "fake-work-" + input.ParentKind + "-" + input.ParentSourceKey
	}
	seen := make(map[string]struct{})
	for _, call := range f.stageCalls {
		for _, row := range call.Rows {
			seen[row.Chunk.ID] = struct{}{}
		}
	}
	chunks := len(seen)
	cp := store.RetrievalProjectionCheckpoint{WorkID: workID, DirtyRevision: input.DirtyRevision, ExpectedPurgeEpoch: input.ExpectedPurgeEpoch, ParentKind: input.ParentKind, ParentSourceKey: input.ParentSourceKey, ProjectionHash: input.ProjectionHash, SectionKey: input.Cursor.SectionKey, NextBoundary: input.Cursor.NextBoundary, StagedChunks: chunks}
	f.staging[input.ParentKind+":"+input.ParentSourceKey] = cp
	return cp, nil
}

func (f *fakeStore) PromoteRetrievalProjectionStaging(_ context.Context, checkpoint store.RetrievalProjectionCheckpoint) (store.ChunkReplaceResult, error) {
	if checkpoint.ExpectedPurgeEpoch != f.purgeEpoch {
		return store.ChunkReplaceResult{}, store.ErrRetrievalPurgeEpochChanged
	}
	f.promotions = append(f.promotions, checkpoint)
	delete(f.staging, checkpoint.ParentKind+":"+checkpoint.ParentSourceKey)
	if f.applied == nil {
		f.applied = make(map[string]bool)
	}
	f.applied[checkpoint.ParentKind+":"+checkpoint.ParentSourceKey] = true
	return store.ChunkReplaceResult{Created: checkpoint.StagedChunks}, nil
}

func (f *fakeStore) BlockRetrievalProjectionTooLarge(_ context.Context, parent retrievalchunk.Parent, revision int64, projectionHash string, expectedPurgeEpoch int64) error {
	if expectedPurgeEpoch != f.purgeEpoch {
		return store.ErrRetrievalPurgeEpochChanged
	}
	f.blockedGiant = append(f.blockedGiant, parent.Kind+":"+parent.SourceKey)
	delete(f.staging, parent.Kind+":"+parent.SourceKey)
	if f.applied == nil {
		f.applied = make(map[string]bool)
	}
	f.applied[parent.Kind+":"+parent.SourceKey] = true
	return nil
}
func (f *fakeStore) ListChunksNeedingEmbeddingForProfileAt(_ context.Context, profile embedding.Profile, after string, limit int, now time.Time) ([]store.RetrievalChunkRow, error) {
	f.operations = append(f.operations, "candidates")
	f.candidateTimes = append(f.candidateTimes, now)
	f.candidateAfters = append(f.candidateAfters, after)
	f.candidateLimits = append(f.candidateLimits, limit)
	f.candidateProfile = profile
	if f.candidateErr != nil {
		return nil, f.candidateErr
	}
	completed := make(map[string]struct{}, len(f.writes))
	for _, row := range f.writes {
		completed[row.ChunkID] = struct{}{}
	}
	result := make([]store.RetrievalChunkRow, 0, min(limit, len(f.chunks)))
	for _, row := range f.chunks {
		if row.ChunkID <= after {
			continue
		}
		if _, ok := completed[row.ChunkID]; ok {
			continue
		}
		result = append(result, row)
		if limit > 0 && len(result) == limit {
			break
		}
	}
	return result, nil
}
func (f *fakeStore) RetrievalPurgeEpoch(context.Context) (int64, error) { return f.purgeEpoch, nil }
func (f *fakeStore) RetrievalEmbeddingProfile(_ context.Context, profileID string) (store.RetrievalEmbeddingProfileRow, error) {
	return store.RetrievalEmbeddingProfileRow{}, fmt.Errorf("profile %s: %w", profileID, store.ErrRetrievalEmbeddingProfileNotFound)
}
func (f *fakeStore) PutRetrievalEmbeddingBatch(_ context.Context, input store.PutRetrievalEmbeddingBatchInput) (int64, error) {
	f.operations = append(f.operations, "batch")
	rows := append([]store.RetrievalEmbeddingRow(nil), input.Rows...)
	f.writeBatches = append(f.writeBatches, rows)
	f.writes = append(f.writes, rows...)
	return int64(len(f.writeBatches)), nil
}
func (f *fakeStore) CountChunksNeedingEmbeddingForProfileAt(_ context.Context, profile embedding.Profile, _ time.Time) (int, error) {
	f.operations = append(f.operations, "count")
	f.candidateProfile = profile
	if f.candidateErr != nil {
		return 0, f.candidateErr
	}
	if f.candidateCount != 0 {
		return f.candidateCount, nil
	}
	return len(f.chunks), nil
}
func (f *fakeStore) ListRetrievalVectors(_ context.Context, _ string, page store.VectorPage) ([]store.RetrievalVectorRow, error) {
	f.vectorListCalls++
	result := make([]store.RetrievalVectorRow, 0, page.Limit)
	for _, row := range f.vectorRows {
		if row.ChunkID <= page.AfterChunkID {
			continue
		}
		result = append(result, row)
		if len(result) == page.Limit {
			break
		}
	}
	return result, nil
}
func (f *fakeStore) RetrievalEmbeddingVerificationState(context.Context, string) (store.RetrievalEmbeddingVerificationState, error) {
	return f.verification, f.verificationErr
}
func (f *fakeStore) BlockCorruptRetrievalEmbedding(_ context.Context, corruption *store.RetrievalEmbeddingCorruptionError) error {
	f.operations = append(f.operations, "block")
	f.blockCalls = append(f.blockCalls, corruption)
	call := len(f.blockCalls) - 1
	if call < len(f.blockErrs) {
		return f.blockErrs[call]
	}
	return nil
}
func (f *fakeStore) RepairRetrievalRuntimeReadinessCounters(context.Context) error {
	f.repairCalls++
	f.operations = append(f.operations, "repair-counters")
	return f.repairErr
}

type fakeProvider struct {
	info     embedding.Info
	requests []embedding.Request
	err      error
	vectors  [][]float32
	embed    func(embedding.Request) (embedding.Response, error)
}

func (f *fakeProvider) Info() embedding.Info { return f.info }
func (f *fakeProvider) Embed(_ context.Context, req embedding.Request) (embedding.Response, error) {
	f.requests = append(f.requests, req)
	if f.embed != nil {
		return f.embed(req)
	}
	if f.err != nil {
		return embedding.Response{}, f.err
	}
	vectors := f.vectors
	if vectors == nil {
		vectors = make([][]float32, len(req.Texts))
		for i := range vectors {
			vectors[i] = []float32{0.6, 0.8}
		}
	}
	return embedding.Response{Vectors: vectors, Provider: f.info.Provider, Model: f.info.Model, Dimensions: f.info.Dimensions}, nil
}

func TestChunkIsParentBoundedAndReportsCreatedDeleted(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
		{Kind: "source", SourceKey: "a", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
	}}
	var snapshots []ChunkProgress
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 2, Progress: func(p ChunkProgress) error { snapshots = append(snapshots, p); return nil }})
	if err != nil {
		t.Fatalf("RunChunk: %v", err)
	}
	if progress.Scanned != 2 || len(st.replacements) != 2 || progress.Created != 2 || progress.Deleted != 0 || progress.Remaining != 0 || progress.HasMore || progress.NextAfterSourceKey != "" || len(snapshots) != 2 || len(progress.Snapshots) != 1 {
		t.Fatalf("progress=%+v replacements=%v", progress, st.replacements)
	}
}

func TestChunkRejectsLegacySourceCursor(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{{Kind: "item", SourceKey: "a"}}}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, AfterSourceKey: "item:old-cursor"})
	if err == nil || err.Error() != "semantic chunk --after-source-key is no longer supported; rerun without it because the durable dirty queue resumes automatically" {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	if len(st.watermarks) != 0 || len(st.applyInputs) != 0 {
		t.Fatalf("legacy cursor performed work: watermarks=%v applies=%d", st.watermarks, len(st.applyInputs))
	}
}

func TestChunkPagesOnlyThroughCapturedWorkRevision(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
		{Kind: "source", SourceKey: "a", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
		{Kind: "item", SourceKey: "b", ContentHash: "hc", Sections: []retrievalchunk.Section{{Role: "raw", Text: "charlie"}}},
	}, workRevision: 2}
	first, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.NextAfterSourceKey != "" || first.Scanned != 1 {
		t.Fatalf("first=%+v", first)
	}
	second, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || second.NextAfterSourceKey != "" || second.Scanned != 1 {
		t.Fatalf("second=%+v", second)
	}
	if got := st.replacements; !reflect.DeepEqual(got, []string{"item:a", "source:a"}) {
		t.Fatalf("replacements=%v", got)
	}
	if !reflect.DeepEqual(st.watermarks, []int64{2, 2}) {
		t.Fatalf("selector watermarks=%v, want every page bounded by W=2", st.watermarks)
	}
}

func TestChunkUntilIdleProcessesEveryDurableQueuePage(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
		{Kind: "item", SourceKey: "b", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
		{Kind: "item", SourceKey: "c", ContentHash: "hc", Sections: []retrievalchunk.Section{{Role: "raw", Text: "charlie"}}},
	}}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, UntilIdle: true})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 3 || progress.Generated != 3 || progress.Created != 3 || progress.Remaining != 0 || progress.HasMore || progress.Checkpoint != nil {
		t.Fatalf("progress=%+v", progress)
	}
	if !reflect.DeepEqual(st.replacements, []string{"item:a", "item:b", "item:c"}) {
		t.Fatalf("replacements=%v", st.replacements)
	}
	if len(progress.Snapshots) != 1 || progress.LastSnapshot == nil || progress.LastSnapshot.Scanned != 3 {
		t.Fatalf("bounded final snapshots=%+v", progress)
	}
}

func TestChunkOwnMaxDurationEndsGracefullyButCallerCancellationPropagates(t *testing.T) {
	blockingStore := func() *fakeStore {
		return &fakeStore{listDirty: func(ctx context.Context, _ int64, _ int) ([]store.RetrievalParentWork, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	progress, err := RunChunk(context.Background(), blockingStore(), ChunkOptions{Limit: 1, UntilIdle: true, MaxDuration: 10 * time.Millisecond})
	if err != nil || !progress.Interrupted || !progress.HasMore {
		t.Fatalf("own deadline progress=%+v err=%v", progress, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunChunk(ctx, blockingStore(), ChunkOptions{Limit: 1, UntilIdle: true, MaxDuration: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation err=%v", err)
	}
}

func TestChunkOwnDeadlineDoesNotMaskIndependentStoreError(t *testing.T) {
	independent := errors.New("independent list failure")
	st := &fakeStore{listDirty: func(ctx context.Context, _ int64, _ int) ([]store.RetrievalParentWork, error) {
		<-ctx.Done()
		return nil, independent
	}}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, UntilIdle: true, MaxDuration: 10 * time.Millisecond})
	if !errors.Is(err, independent) || progress.Interrupted {
		t.Fatalf("progress=%+v err=%v, want independent failure", progress, err)
	}
}

func TestChunkUntilIdleStopsOnNondurableHashAndPlannerFailures(t *testing.T) {
	t.Run("hash", func(t *testing.T) {
		st := &fakeStore{parents: []retrievalchunk.Parent{{SourceKey: "missing-kind", Sections: []retrievalchunk.Section{{Role: "raw", Text: "text"}}}}}
		progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, UntilIdle: true})
		if err == nil || !strings.Contains(err.Error(), "parent kind is required") || progress.Failed != 1 || len(st.watermarks) != 1 {
			t.Fatalf("progress=%+v err=%v watermarks=%v", progress, err, st.watermarks)
		}
	})

	t.Run("planner", func(t *testing.T) {
		st := &fakeStore{parents: []retrievalchunk.Parent{{Kind: "source", SourceKey: "invalid-options", Sections: []retrievalchunk.Section{{Role: "raw", Text: "x"}}}}}
		progress, err := runChunkUntilIdle(context.Background(), st, ChunkOptions{Limit: 1, UntilIdle: true}, defaultChunkExecutionLimits, retrievalchunk.Options{})
		if err == nil || !strings.Contains(err.Error(), "invalid chunk sizes") || progress.Failed != 1 || len(st.watermarks) != 1 {
			t.Fatalf("progress=%+v err=%v watermarks=%v", progress, err, st.watermarks)
		}
	})
}

func TestChunkResumeUsesBoundedContextPlannerValidation(t *testing.T) {
	sections := make([]retrievalchunk.Section, retrievalchunk.V3MaximumPlanningSections+1)
	for i := range sections {
		sections[i] = retrievalchunk.Section{Key: fmt.Sprintf("section-%d", i), Role: "raw", Text: "x"}
	}
	parent := retrievalchunk.Parent{Kind: "source", SourceKey: "legacy-staging", Sections: sections}
	hash, err := retrievalchunk.ParentProjectionHash(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, preparedPlan := range []string{"", `{}`} {
		name := "missing-plan"
		if preparedPlan != "" {
			name = "persisted-plan"
		}
		t.Run(name, func(t *testing.T) {
			st := &fakeStore{parents: []retrievalchunk.Parent{parent}, staging: map[string]store.RetrievalProjectionCheckpoint{
				"source:legacy-staging": {WorkID: "legacy", DirtyRevision: 1, ParentKind: "source", ParentSourceKey: "legacy-staging", ProjectionHash: hash, SectionKey: "not-empty", PreparedPlan: preparedPlan},
			}}
			progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, UntilIdle: true})
			if err == nil || !strings.Contains(err.Error(), "planning section ceiling") || progress.Failed != 1 || len(st.stageCalls) != 0 || len(st.promotions) != 0 {
				t.Fatalf("progress=%+v err=%v stage_calls=%d promotions=%d", progress, err, len(st.stageCalls), len(st.promotions))
			}
		})
	}
}

func TestChunkNonUntilIdleCooperativeDeadlineReportsInterrupted(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
		{Kind: "item", SourceKey: "b", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
	}}
	base := time.Unix(2_000, 0).UTC()
	times := []time.Time{base, base.Add(3 * time.Second)}
	nowCall := 0
	progress, err := RunChunk(context.Background(), st, ChunkOptions{
		Limit: 2, MaxDuration: 2 * time.Second,
		Now: func() time.Time {
			value := times[min(nowCall, len(times)-1)]
			nowCall++
			return value
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Interrupted || !progress.HasMore || progress.Scanned != 1 || progress.Remaining != 1 {
		t.Fatalf("progress=%+v", progress)
	}
	if progress.LastSnapshot == nil || !progress.LastSnapshot.Interrupted {
		t.Fatalf("final snapshot=%+v", progress.LastSnapshot)
	}
}

func TestChunkProgressKeepsOnlyLastSample(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
		{Kind: "source", SourceKey: "a", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
		{Kind: "item", SourceKey: "b", ContentHash: "hc", Sections: []retrievalchunk.Section{{Role: "raw", Text: "charlie"}}},
	}}
	var snapshots []ChunkProgress
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 2, Progress: func(p ChunkProgress) error {
		snapshots = append(snapshots, p)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || len(progress.Snapshots) != 1 || snapshots[0].NextAfterSourceKey != "" || snapshots[0].Remaining != 1 || snapshots[1].NextAfterSourceKey != "" || snapshots[1].Remaining != 0 {
		t.Fatalf("snapshots=%+v progress=%+v", snapshots, progress)
	}
}

func TestChunkDoesNotAdvanceCursorPastFailedSourceKeyGroup(t *testing.T) {
	st := &fakeStore{
		parents: []retrievalchunk.Parent{
			{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
			{Kind: "source", SourceKey: "a", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
		},
		replaceErrKey: "source:a",
	}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 2})
	if err == nil {
		t.Fatal("RunChunk unexpectedly succeeded")
	}
	if progress.NextAfterSourceKey != "" || progress.Remaining != 1 {
		t.Fatalf("failed work progress=%+v, want no legacy cursor and one unfinished row", progress)
	}
}

func TestChunkClassifiesEmptyProjectionAsBlocked(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{{Kind: "item", SourceKey: "empty", ContentHash: "hash"}}}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Blocked != 1 || progress.Current != 0 || progress.Generated != 0 || progress.NextAfterSourceKey != "" || len(st.replacements) != 1 || len(st.applyInputs) != 1 || st.applyInputs[0].Status != store.RetrievalProjectionEmpty || st.applyInputs[0].Reason != "no_chunkable_content" {
		t.Fatalf("progress=%+v replacements=%v", progress, st.replacements)
	}
}

func TestChunkDeletedAndIneligibleParentsBypassAbsentStagingAndApplyCleanup(t *testing.T) {
	st := &fakeStore{
		parents: []retrievalchunk.Parent{
			{Kind: "source", SourceKey: "deleted"},
			{Kind: "item", SourceKey: "ineligible"},
		},
		replaceResult: store.ChunkReplaceResult{Deleted: 1},
	}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.applyInputs) != 2 || len(st.stageCalls) != 0 || progress.Blocked != 2 || progress.Deleted != 2 || progress.Remaining != 0 {
		t.Fatalf("progress=%+v applies=%+v stage_calls=%d", progress, st.applyInputs, len(st.stageCalls))
	}
	for _, input := range st.applyInputs {
		if input.Status != store.RetrievalProjectionEmpty || input.Reason != "no_chunkable_content" {
			t.Fatalf("cleanup apply=%+v", input)
		}
	}
}

func TestChunkReportsCurrentReuse(t *testing.T) {
	st := &fakeStore{
		parents:       []retrievalchunk.Parent{{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}}},
		replaceResult: store.ChunkReplaceResult{Reused: 1},
	}
	got, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Current != 1 || got.Generated != 0 || got.Remaining != 0 {
		t.Fatalf("progress=%+v", got)
	}
}

func TestGiantChunkProjectionStopsAfterTwoDurableBatchesAndResumes(t *testing.T) {
	parent := retrievalchunk.Parent{
		Kind: "source", SourceKey: "giant", ContentHash: "v1",
		Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: strings.Repeat("abcdefghij ", 30)}},
	}
	st := &fakeStore{parents: []retrievalchunk.Parent{parent}}
	base := time.Unix(1_000, 0).UTC()
	times := []time.Time{base, base.Add(time.Second), base.Add(3 * time.Second)}
	nowCall := 0
	now := func() time.Time {
		if nowCall >= len(times) {
			return times[len(times)-1]
		}
		value := times[nowCall]
		nowCall++
		return value
	}
	opts := ChunkOptions{Limit: 1, MaxDuration: 2 * time.Second, Now: now}
	limits := chunkExecutionLimits{GiantThreshold: 2, StageBatchSize: 2, HardChunkLimit: 50}
	first, err := runChunkWithLimits(context.Background(), st, opts, limits, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.stageCalls) != 2 || len(st.promotions) != 0 || first.Checkpoint == nil || first.Remaining != 1 || !first.Interrupted {
		t.Fatalf("first=%+v stage_calls=%d promotions=%d", first, len(st.stageCalls), len(st.promotions))
	}
	if !first.HasMore {
		t.Fatalf("paused giant must report pending durable work: %+v", first)
	}
	checkpoint := *first.Checkpoint
	if checkpoint.WorkID == "" || checkpoint.DirtyRevision != 1 || checkpoint.SectionKey == "" || checkpoint.NextBoundary <= 0 {
		t.Fatalf("checkpoint=%+v", checkpoint)
	}
	if len(st.stageCalls[1].Rows) == 0 || st.stageCalls[1].Rows[0].Occurrence.StartChar < st.stageCalls[0].Cursor.NextBoundary {
		t.Fatalf("second batch restarted: first cursor=%+v second first=%+v", st.stageCalls[0].Cursor, st.stageCalls[1].Rows)
	}

	second, err := runChunkWithLimits(context.Background(), st, ChunkOptions{Limit: 1}, limits, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.promotions) != 1 || second.Generated != 1 || second.Remaining != 0 || second.Checkpoint != nil {
		t.Fatalf("second=%+v promotions=%+v", second, st.promotions)
	}
}

func TestProjectionTooLargeBlocksTerminallyWithoutApply(t *testing.T) {
	var text strings.Builder
	for i := 0; i < 80; i++ {
		_, _ = fmt.Fprintf(&text, "%010d ", i)
	}
	parent := retrievalchunk.Parent{
		Kind: "source", SourceKey: "too-large", ContentHash: "v1",
		Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: text.String()}},
	}
	st := &fakeStore{parents: []retrievalchunk.Parent{parent}}
	progress, err := runChunkWithLimits(context.Background(), st, ChunkOptions{Limit: 1}, chunkExecutionLimits{GiantThreshold: 2, StageBatchSize: 2, HardChunkLimit: 4}, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.blockedGiant, []string{"source:too-large"}) || len(st.applyInputs) != 0 || len(st.promotions) != 0 || progress.Blocked != 1 || progress.Remaining != 0 {
		t.Fatalf("progress=%+v blocked=%v applies=%d promotions=%d", progress, st.blockedGiant, len(st.applyInputs), len(st.promotions))
	}
}

func TestEmbedBatchesInOrderWritesL2VectorsAndProvenance(t *testing.T) {
	st := &fakeStore{chunks: []store.RetrievalChunkRow{
		{ChunkID: "a", ChunkTextHash: "ha", Text: "alpha", AttemptCount: 1},
		{ChunkID: "b", ChunkTextHash: "hb", Text: "bravo", AttemptCount: 2},
		{ChunkID: "c", ChunkTextHash: "hc", Text: "charlie"},
	}}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "fake-model", Dimensions: 2}}
	st.candidateCount = 5
	var snapshots []Progress
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 3, BatchSize: 2, Now: func() time.Time { return time.Unix(123, 0).UTC() }, Progress: func(p Progress) error { snapshots = append(snapshots, p); return nil }})
	if err != nil {
		t.Fatalf("RunEmbed: %v", err)
	}
	if got := []int{len(provider.requests[0].Texts), len(provider.requests[1].Texts)}; !reflect.DeepEqual(got, []int{2, 1}) {
		t.Fatalf("batch sizes=%v", got)
	}
	if progress.Generated != 3 || progress.Remaining != 2 || len(st.writes) != 3 || len(snapshots) != 2 || len(progress.Snapshots) != 1 || progress.SnapshotCount != 2 || !progress.SnapshotsTruncated {
		t.Fatalf("progress=%+v writes=%d", progress, len(st.writes))
	}
	if snapshots[0].Scanned != 2 || snapshots[0].Remaining != 3 || snapshots[1].Scanned != 3 || snapshots[1].Remaining != 2 {
		t.Fatalf("snapshots=%+v", snapshots)
	}
	if want := []string{"count", "candidates", "batch", "candidates", "candidates", "batch", "candidates"}; !reflect.DeepEqual(st.operations, want) {
		t.Fatalf("normal embed operations=%v want=%v; it must not scan ready vectors", st.operations, want)
	}
	if want := []int{2, 1, 1, 1}; !reflect.DeepEqual(st.candidateLimits, want) {
		t.Fatalf("candidate limits=%v want=%v", st.candidateLimits, want)
	}
	profile := Profile(provider.Info())
	profileID, _ := profile.ID()
	for i, row := range st.writes {
		if row.ChunkID != st.chunks[i].ChunkID || row.ProfileID != profileID || row.AttemptCount != st.chunks[i].AttemptCount+1 || row.Status != store.RetrievalEmbeddingReady || row.Normalization != embedding.NormalizationL2 {
			t.Fatalf("write[%d]=%+v", i, row)
		}
		decoded, err := embedding.DecodeDenseF32(row.VectorBytes, 2)
		if err != nil || !reflect.DeepEqual(decoded, []float32{0.6, 0.8}) {
			t.Fatalf("decoded=%v err=%v", decoded, err)
		}
	}
}

func TestEmbedUntilIdleRetriesSameBatchAndFreezesEligibilityTime(t *testing.T) {
	chunks := make([]store.RetrievalChunkRow, 5)
	for i := range chunks {
		chunks[i] = store.RetrievalChunkRow{ChunkID: fmt.Sprintf("chunk-%d", i), ChunkTextHash: fmt.Sprintf("hash-%d", i), Text: "text"}
	}
	st := &fakeStore{chunks: chunks}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: embedding.RetryableError(errors.New("down"))}
	eligibility := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	nowCalls := 0
	var sleeps []time.Duration
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{
		Limit: 2, BatchSize: 1, UntilIdle: true,
		sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
		Now: func() time.Time {
			nowCalls++
			return eligibility.Add(time.Duration(nowCalls) * time.Hour)
		},
	})
	if !errors.Is(err, ErrEmbedCircuitOpen) {
		t.Fatalf("err=%v", err)
	}
	if len(provider.requests) != 3 || len(st.writes) != 1 || progress.Scanned != 1 || progress.Remaining != 4 {
		t.Fatalf("progress=%+v requests=%d writes=%d", progress, len(provider.requests), len(st.writes))
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("sleeps=%v", sleeps)
	}
	for _, req := range provider.requests {
		if !reflect.DeepEqual(req.Texts, []string{"text"}) {
			t.Fatalf("retried different texts: requests=%+v", provider.requests)
		}
	}
	if nowCalls != 1 || len(st.candidateTimes) != 2 {
		t.Fatalf("now calls=%d candidate times=%v", nowCalls, st.candidateTimes)
	}
	if !reflect.DeepEqual(st.candidateAfters, []string{"", ""}) {
		t.Fatalf("candidate cursors=%v", st.candidateAfters)
	}
	for _, got := range st.candidateTimes {
		if !got.Equal(eligibility.Add(time.Hour)) {
			t.Fatalf("eligibility drifted: got=%s times=%v", got, st.candidateTimes)
		}
	}
}

func TestEmbedUntilIdleProcessesAllPagesWithBoundedProgress(t *testing.T) {
	chunks := make([]store.RetrievalChunkRow, 5)
	for i := range chunks {
		chunks[i] = store.RetrievalChunkRow{ChunkID: fmt.Sprintf("chunk-%d", i), ChunkTextHash: fmt.Sprintf("hash-%d", i), Text: "text"}
	}
	st := &fakeStore{chunks: chunks}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}}
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 2, BatchSize: 1, UntilIdle: true})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 5 || progress.Generated != 5 || progress.Remaining != 0 || len(st.writes) != 5 {
		t.Fatalf("progress=%+v writes=%d", progress, len(st.writes))
	}
	if len(st.candidateAfters) != 10 {
		t.Fatalf("candidate probes=%d cursors=%v", len(st.candidateAfters), st.candidateAfters)
	}
	for _, after := range st.candidateAfters {
		if after != "" {
			t.Fatalf("manual batch used an unbounded page cursor: cursors=%v", st.candidateAfters)
		}
	}
	if len(progress.Snapshots) != 1 || progress.LastSnapshot == nil || progress.LastSnapshot.Scanned != 5 || progress.SnapshotCount != 5 {
		t.Fatalf("progress snapshots=%+v", progress)
	}
}

func TestEmbedOwnMaxDurationEndsGracefullyButCallerCancellationPropagates(t *testing.T) {
	blocking := func(ctx context.Context) embedding.Provider {
		info := embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}
		return &contextProvider{info: info, embed: func(ctx context.Context, _ embedding.Request) (embedding.Response, error) {
			<-ctx.Done()
			return embedding.Response{}, ctx.Err()
		}}
	}
	st := &fakeStore{chunks: []store.RetrievalChunkRow{{ChunkID: "a", ChunkTextHash: "ha", Text: "alpha"}}}
	progress, err := RunEmbed(context.Background(), st, blocking(context.Background()), EmbedOptions{Limit: 1, BatchSize: 1, UntilIdle: true, MaxDuration: 10 * time.Millisecond})
	if err != nil || !progress.Interrupted || progress.Scanned != 0 || progress.Remaining != 1 || len(st.writes) != 0 {
		t.Fatalf("own deadline progress=%+v err=%v writes=%d", progress, err, len(st.writes))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunEmbed(ctx, st, blocking(ctx), EmbedOptions{Limit: 1, BatchSize: 1, UntilIdle: true, MaxDuration: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation err=%v", err)
	}
}

func TestEmbedOwnDeadlineDoesNotMaskIndependentProviderError(t *testing.T) {
	independent := errors.New("independent provider failure")
	provider := &contextProvider{
		info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2},
		embed: func(ctx context.Context, _ embedding.Request) (embedding.Response, error) {
			<-ctx.Done()
			return embedding.Response{}, independent
		},
	}
	st := &fakeStore{chunks: []store.RetrievalChunkRow{{ChunkID: "a", ChunkTextHash: "ha", Text: "alpha"}}}
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 1, BatchSize: 1, UntilIdle: true, MaxDuration: 10 * time.Millisecond})
	if !errors.Is(err, independent) || progress.Interrupted {
		t.Fatalf("progress=%+v err=%v, want independent failure", progress, err)
	}
}

type contextProvider struct {
	info  embedding.Info
	embed func(context.Context, embedding.Request) (embedding.Response, error)
}

func (p *contextProvider) Info() embedding.Info { return p.info }
func (p *contextProvider) Embed(ctx context.Context, req embedding.Request) (embedding.Response, error) {
	return p.embed(ctx, req)
}

func TestEmbedRejectsStaleChunkProvenanceBeforeProviderCall(t *testing.T) {
	stale := &store.RetrievalChunkProfileMismatchError{
		ProjectionVersion: retrievalchunk.ProjectionVersion,
		ChunkerVersion:    retrievalchunk.Version,
		Count:             1,
	}
	st := &fakeStore{candidateErr: stale}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}}

	_, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 1, BatchSize: 1})
	if !errors.Is(err, stale) {
		t.Fatalf("RunEmbed error = %v, want stale chunk provenance", err)
	}
	if len(provider.requests) != 0 || len(st.writes) != 0 {
		t.Fatalf("provider requests=%+v writes=%+v, want no embedding work", provider.requests, st.writes)
	}
	if st.candidateProfile.ProjectionVersion != retrievalchunk.ProjectionVersion || st.candidateProfile.ChunkerVersion != retrievalchunk.Version {
		t.Fatalf("candidate profile=%+v", st.candidateProfile)
	}
}

func TestEmbedClassifiesRetryBlockedFatalAndCancellation(t *testing.T) {
	base := store.RetrievalChunkRow{ChunkID: "a", ChunkTextHash: "ha", Text: "alpha", AttemptCount: 4}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	t.Run("retry", func(t *testing.T) {
		st := &fakeStore{chunks: []store.RetrievalChunkRow{base}}
		provider := &fakeProvider{
			info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2},
			err:  embedding.RetryableError(errors.New("down")),
		}
		var sleeps []time.Duration
		_, err := RunEmbed(context.Background(), st, provider, EmbedOptions{
			Limit: 1, BatchSize: 1, Now: func() time.Time { return now },
			sleep: func(_ context.Context, delay time.Duration) error {
				sleeps = append(sleeps, delay)
				return nil
			},
		})
		if !errors.Is(err, ErrEmbedCircuitOpen) || len(st.writes) != 1 {
			t.Fatalf("retry err=%v writes=%+v", err, st.writes)
		}
		if got := st.writes[0]; got.Status != store.RetrievalEmbeddingError || got.AttemptCount != 7 || !got.NextAttemptAt.After(now) {
			t.Fatalf("retry row=%+v", got)
		}
		if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, 2 * time.Second}) {
			t.Fatalf("sleeps=%v", sleeps)
		}
	})
	for _, tc := range []struct {
		name  string
		err   error
		want  store.RetrievalEmbeddingStatus
		fatal bool
	}{
		{"blocked", embedding.BlockedError(errors.New("too long")), store.RetrievalEmbeddingBlocked, false},
		{"fatal", embedding.FatalConfigError(errors.New("bad config")), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{chunks: []store.RetrievalChunkRow{base}}
			provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: tc.err}
			_, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 1, BatchSize: 1, Now: func() time.Time { return now }})
			if tc.fatal {
				if err == nil || len(st.writes) != 0 {
					t.Fatalf("fatal err=%v writes=%v", err, st.writes)
				}
				return
			}
			if err != nil || len(st.writes) != 1 || st.writes[0].Status != tc.want || st.writes[0].AttemptCount != 5 {
				t.Fatalf("err=%v writes=%+v", err, st.writes)
			}
			if tc.want == store.RetrievalEmbeddingBlocked && !st.writes[0].NextAttemptAt.IsZero() {
				t.Fatalf("blocked scheduled=%s", st.writes[0].NextAttemptAt)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := &fakeStore{chunks: []store.RetrievalChunkRow{base}}
	_, err := RunEmbed(ctx, st, &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}}, EmbedOptions{Limit: 1, BatchSize: 1})
	if !errors.Is(err, context.Canceled) || len(st.writes) != 0 {
		t.Fatalf("cancel err=%v writes=%v", err, st.writes)
	}
}

func TestEmbedRejectsNonUnitProviderVectorWithoutWriting(t *testing.T) {
	st := &fakeStore{chunks: []store.RetrievalChunkRow{{ChunkID: "a", ChunkTextHash: "ha", Text: "alpha"}}}
	provider := &fakeProvider{
		info:    embedding.Info{Provider: "fake", Model: "m", Dimensions: 2},
		vectors: [][]float32{{3, 4}},
	}
	_, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 1, BatchSize: 1})
	if err == nil || len(st.writes) != 0 {
		t.Fatalf("non-unit response err=%v writes=%+v", err, st.writes)
	}
}

func TestEmbedIsolatesBlockedInputBeforeTerminalWrites(t *testing.T) {
	st := &fakeStore{chunks: []store.RetrievalChunkRow{
		{ChunkID: "bad", ChunkTextHash: "bad-hash", Text: "oversized", AttemptCount: 3},
		{ChunkID: "good", ChunkTextHash: "good-hash", Text: "ordinary", AttemptCount: 7},
	}}
	info := embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}
	provider := &fakeProvider{info: info}
	provider.embed = func(req embedding.Request) (embedding.Response, error) {
		if len(req.Texts) > 1 || req.Texts[0] == "oversized" {
			return embedding.Response{}, embedding.BlockedError(errors.New("input too large"))
		}
		return embedding.Response{Vectors: [][]float32{{0.6, 0.8}}, Provider: info.Provider, Model: info.Model, Dimensions: info.Dimensions}, nil
	}
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 2, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 3 || len(st.writes) != 2 || progress.Blocked != 1 || progress.Generated != 1 || progress.Scanned != 2 {
		t.Fatalf("requests=%+v writes=%+v progress=%+v", provider.requests, st.writes, progress)
	}
	if st.writes[0].ChunkID != "bad" || st.writes[0].Status != store.RetrievalEmbeddingBlocked || st.writes[0].AttemptCount != 5 {
		t.Fatalf("blocked write=%+v", st.writes[0])
	}
	if st.writes[1].ChunkID != "good" || st.writes[1].Status != store.RetrievalEmbeddingReady || st.writes[1].AttemptCount != 9 {
		t.Fatalf("ready write=%+v", st.writes[1])
	}
}

func TestSemanticVerifyPagesAndQuarantinesCorruption(t *testing.T) {
	valid := embedding.EncodeDenseF32([]float32{0.6, 0.8})
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	profileID, _ := profile.ID()
	verification := store.RetrievalEmbeddingVerificationState{ProfileID: profileID, Profile: profile, LatestRevision: 3, PurgeEpoch: 4, GlobalPurgeEpoch: 4}
	st := &fakeStore{verification: verification, vectorRows: []store.RetrievalVectorRow{
		{ChunkID: "a", ProfileID: profileID, Provider: "fake", Model: "m", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: valid, VectorHash: vectorHash(valid), ChunkTextHash: "ha", CurrentChunkTextHash: "ha", Revision: 1},
		{ChunkID: "b", ProfileID: profileID, Provider: "fake", Model: "m", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: []byte{0}, VectorHash: "bad", ChunkTextHash: "hb", CurrentChunkTextHash: "hb", Revision: 2},
		{ChunkID: "c", ProfileID: profileID, Provider: "fake", Model: "m", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: valid, VectorHash: vectorHash(valid), ChunkTextHash: "hc", CurrentChunkTextHash: "hc", Revision: 3},
	}}
	progress, err := RunVerify(context.Background(), st, VerifyOptions{Profile: profile, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Scanned != 2 || progress.Quarantined != 1 || progress.Resume != "b" || !progress.HasMore || len(st.blockCalls) != 1 || st.blockCalls[0].ChunkID != "b" {
		t.Fatalf("progress=%+v blocks=%+v", progress, st.blockCalls)
	}
	next, err := RunVerify(context.Background(), st, VerifyOptions{Profile: profile, Limit: 2, Resume: progress.Resume})
	if err != nil || next.Scanned != 1 || next.Resume != "c" || next.Quarantined != 0 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestSemanticVerifyReturnsLastSuccessfulCursorWhenRowValidationFails(t *testing.T) {
	valid := embedding.EncodeDenseF32([]float32{0.6, 0.8})
	profile := Profile(embedding.Info{
		Provider: "fake", Model: "m", Dimensions: 2,
	})
	profileID, _ := profile.ID()
	row := func(id string, revision int64) store.RetrievalVectorRow {
		return store.RetrievalVectorRow{
			ChunkID: id, ProfileID: profileID,
			Provider: "fake", Model: "m", Dimensions: 2,
			ProjectionVersion: retrievalchunk.ProjectionVersion,
			ChunkerVersion:    retrievalchunk.Version,
			Representation:    embedding.RepresentationDenseF32,
			Normalization:     embedding.NormalizationL2,
			VectorBytes:       valid, VectorHash: vectorHash(valid),
			ChunkTextHash: "hash-" + id, CurrentChunkTextHash: "hash-" + id,
			Revision: revision,
		}
	}
	st := &fakeStore{
		verification: store.RetrievalEmbeddingVerificationState{
			ProfileID: profileID, Profile: profile,
			LatestRevision: 3, PurgeEpoch: 4, GlobalPurgeEpoch: 4,
		},
		vectorRows: []store.RetrievalVectorRow{
			row("a", 1),
			row("b", 2),
			row("c", 3),
		},
	}
	st.vectorRows[1].ChunkerVersion = "invalid"

	first, err := RunVerify(
		t.Context(),
		st,
		VerifyOptions{Profile: profile, Limit: 3},
	)
	if err == nil {
		t.Fatal("invalid row unexpectedly verified")
	}
	if first.Scanned != 1 || first.Current != 1 || first.Resume != "a" {
		t.Fatalf("failed-page progress=%+v", first)
	}

	st.vectorRows[1].ChunkerVersion = retrievalchunk.Version
	resumed, err := RunVerify(
		t.Context(),
		st,
		VerifyOptions{Profile: profile, Limit: 3, Resume: first.Resume},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Scanned != 2 || resumed.Current != 2 || resumed.Resume != "c" {
		t.Fatalf("resumed progress=%+v", resumed)
	}
}

func TestSemanticVerifyReturnsLastSuccessfulCursorWhenQuarantineCommitFails(t *testing.T) {
	valid := embedding.EncodeDenseF32([]float32{0.6, 0.8})
	profile := Profile(embedding.Info{
		Provider: "fake", Model: "m", Dimensions: 2,
	})
	profileID, _ := profile.ID()
	validRow := func(id string, revision int64) store.RetrievalVectorRow {
		return store.RetrievalVectorRow{
			ChunkID: id, ProfileID: profileID,
			Provider: "fake", Model: "m", Dimensions: 2,
			ProjectionVersion: retrievalchunk.ProjectionVersion,
			ChunkerVersion:    retrievalchunk.Version,
			Representation:    embedding.RepresentationDenseF32,
			Normalization:     embedding.NormalizationL2,
			VectorBytes:       valid, VectorHash: vectorHash(valid),
			ChunkTextHash: "hash-" + id, CurrentChunkTextHash: "hash-" + id,
			Revision: revision,
		}
	}
	corrupt := validRow("b", 2)
	corrupt.VectorBytes = []byte{0}
	corrupt.VectorHash = "invalid"
	blockErr := errors.New("quarantine commit failed")
	st := &fakeStore{
		verification: store.RetrievalEmbeddingVerificationState{
			ProfileID: profileID, Profile: profile,
			LatestRevision: 3, PurgeEpoch: 4, GlobalPurgeEpoch: 4,
		},
		vectorRows: []store.RetrievalVectorRow{
			validRow("a", 1),
			corrupt,
			validRow("c", 3),
		},
		blockErrs: []error{blockErr},
	}

	first, err := RunVerify(
		t.Context(),
		st,
		VerifyOptions{Profile: profile, Limit: 3},
	)
	if !errors.Is(err, blockErr) {
		t.Fatalf("first err=%v", err)
	}
	if first.Scanned != 1 || first.Current != 1 || first.Resume != "a" {
		t.Fatalf("failed-page progress=%+v", first)
	}

	resumed, err := RunVerify(
		t.Context(),
		st,
		VerifyOptions{Profile: profile, Limit: 3, Resume: first.Resume},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Scanned != 2 ||
		resumed.Current != 1 ||
		resumed.Quarantined != 1 ||
		resumed.Resume != "c" {
		t.Fatalf("resumed progress=%+v", resumed)
	}
}

func TestSemanticVerifyRepairsReadinessCountersOnlyWhenExplicitlyRequested(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	profileID, _ := profile.ID()
	state := store.RetrievalEmbeddingVerificationState{ProfileID: profileID, Profile: profile}

	ordinary := &fakeStore{verification: state}
	progress, err := RunVerify(context.Background(), ordinary, VerifyOptions{Profile: profile, Limit: 1})
	if err != nil || ordinary.repairCalls != 0 || progress.CountersRepaired {
		t.Fatalf("ordinary progress=%+v repair_calls=%d err=%v", progress, ordinary.repairCalls, err)
	}

	repair := &fakeStore{verification: state}
	progress, err = RunVerify(context.Background(), repair, VerifyOptions{Profile: profile, Limit: 1, RepairCounters: true})
	if err != nil || repair.repairCalls != 1 || !progress.CountersRepaired {
		t.Fatalf("repair progress=%+v repair_calls=%d err=%v", progress, repair.repairCalls, err)
	}
	if len(repair.operations) == 0 || repair.operations[0] != "repair-counters" {
		t.Fatalf("operations=%v", repair.operations)
	}

	failed := &fakeStore{verification: state, repairErr: errors.New("repair failed")}
	progress, err = RunVerify(context.Background(), failed, VerifyOptions{Profile: profile, Limit: 1, RepairCounters: true})
	if err == nil || progress.CountersRepaired || failed.repairCalls != 1 {
		t.Fatalf("failed progress=%+v repair_calls=%d err=%v", progress, failed.repairCalls, err)
	}
}

func TestSemanticVerifyTreatsMissingProfileAsEmptyBeginningState(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "unbuilt", Dimensions: 2})
	st := &fakeStore{verificationErr: fmt.Errorf("missing configured profile: %w", store.ErrRetrievalEmbeddingProfileNotFound)}

	progress, err := RunVerify(context.Background(), st, VerifyOptions{Profile: profile, Limit: 1, RepairCounters: true})
	if err != nil {
		t.Fatal(err)
	}
	if !progress.CountersRepaired || progress.Scanned != 0 || progress.HasMore || progress.Resume != "" {
		t.Fatalf("progress=%+v", progress)
	}
	if st.repairCalls != 1 || st.vectorListCalls != 0 {
		t.Fatalf("repair_calls=%d vector_list_calls=%d", st.repairCalls, st.vectorListCalls)
	}
}

func TestSemanticVerifyRejectsProfileRootAndRevisionProvenance(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	profileID, _ := profile.ID()
	valid := embedding.EncodeDenseF32([]float32{0.6, 0.8})
	base := store.RetrievalEmbeddingVerificationState{ProfileID: profileID, Profile: profile, LatestRevision: 2, PurgeEpoch: 1, GlobalPurgeEpoch: 1}
	seedValidRoot := func(state *store.RetrievalEmbeddingVerificationState) {
		state.ActiveGenerationID = "root"
		state.ActiveSnapshotRevision = 2
		state.ActiveIndexedCount = 2
		state.ActiveTombstoneCount = 1
		state.GenerationBackend = semanticindex.BackendUSearch
		state.GenerationBackendVersion = semanticindex.USearchVersion
		state.GenerationStatus = store.RetrievalGenerationCompleted
		state.GenerationActive = true
		state.GenerationDimensions = profile.Dimensions
		state.GenerationDistanceMetric = "cosine"
		state.GenerationIndexedChunkCount = 2
	}
	for _, tc := range []struct {
		name   string
		mutate func(*store.RetrievalEmbeddingVerificationState, *store.RetrievalVectorRow)
	}{
		{"profile id", func(state *store.RetrievalEmbeddingVerificationState, _ *store.RetrievalVectorRow) {
			state.ProfileID = "wrong"
		}},
		{"purge epoch", func(state *store.RetrievalEmbeddingVerificationState, _ *store.RetrievalVectorRow) {
			state.GlobalPurgeEpoch++
		}},
		{"backend", func(state *store.RetrievalEmbeddingVerificationState, _ *store.RetrievalVectorRow) {
			state.ActiveGenerationID, state.GenerationBackend, state.GenerationStatus = "root", "hnsw", store.RetrievalGenerationCompleted
			state.GenerationActive, state.GenerationDimensions, state.GenerationDistanceMetric = true, 2, "dot"
		}},
		{"chunker", func(_ *store.RetrievalEmbeddingVerificationState, row *store.RetrievalVectorRow) {
			row.ChunkerVersion = "old"
		}},
		{"revision", func(_ *store.RetrievalEmbeddingVerificationState, row *store.RetrievalVectorRow) { row.Revision = 3 }},
		{"tombstones exceed indexed membership", func(state *store.RetrievalEmbeddingVerificationState, _ *store.RetrievalVectorRow) {
			seedValidRoot(state)
			state.ActiveTombstoneCount = 3
		}},
		{"generation indexed count mismatch", func(state *store.RetrievalEmbeddingVerificationState, _ *store.RetrievalVectorRow) {
			seedValidRoot(state)
			state.GenerationIndexedChunkCount = 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := base
			row := store.RetrievalVectorRow{ChunkID: "a", ProfileID: profileID, Provider: "fake", Model: "m", Dimensions: 2, ProjectionVersion: retrievalchunk.ProjectionVersion, ChunkerVersion: retrievalchunk.Version, Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, VectorBytes: valid, VectorHash: vectorHash(valid), ChunkTextHash: "ha", CurrentChunkTextHash: "ha", Revision: 1}
			tc.mutate(&state, &row)
			st := &fakeStore{verification: state, vectorRows: []store.RetrievalVectorRow{row}}
			if _, err := RunVerify(context.Background(), st, VerifyOptions{Profile: profile, Limit: 1}); err == nil {
				t.Fatal("invalid verification provenance accepted")
			}
		})
	}
}

func TestValidateVerificationStateAcceptsPinnedUSearchGeneration(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	state := validUSearchVerificationState(profileID, profile)

	if err := validateVerificationState(profileID, profile, state); err != nil {
		t.Fatalf("valid pinned USearch generation rejected: %v", err)
	}
}

func TestValidateVerificationStateRejectsUnsupportedGenerationProvenance(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*store.RetrievalEmbeddingVerificationState)
	}{
		{"exact backend", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationBackend = "exact"
		}},
		{"unknown backend", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationBackend = "unknown"
		}},
		{"wrong USearch version", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationBackendVersion = "2.25.0"
		}},
		{"non-cosine metric", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationDistanceMetric = "dot"
		}},
		{"wrong dimensions", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationDimensions = profile.Dimensions + 1
		}},
		{"inactive generation", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationActive = false
		}},
		{"non-completed generation", func(state *store.RetrievalEmbeddingVerificationState) {
			state.GenerationStatus = store.RetrievalGenerationBuilding
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := validUSearchVerificationState(profileID, profile)
			tc.mutate(&state)

			if err := validateVerificationState(profileID, profile, state); err == nil {
				t.Fatal("unsupported active generation provenance accepted")
			}
		})
	}
}

func validUSearchVerificationState(profileID string, profile embedding.Profile) store.RetrievalEmbeddingVerificationState {
	return store.RetrievalEmbeddingVerificationState{
		ProfileID:                   profileID,
		Profile:                     profile,
		LatestRevision:              2,
		PurgeEpoch:                  1,
		GlobalPurgeEpoch:            1,
		ActiveGenerationID:          "root",
		ActiveSnapshotRevision:      2,
		ActiveIndexedCount:          2,
		ActiveTombstoneCount:        1,
		GenerationBackend:           semanticindex.BackendUSearch,
		GenerationBackendVersion:    semanticindex.USearchVersion,
		GenerationDistanceMetric:    "cosine",
		GenerationDimensions:        profile.Dimensions,
		GenerationIndexedChunkCount: 2,
		GenerationStatus:            store.RetrievalGenerationCompleted,
		GenerationActive:            true,
	}
}

func TestEmbedCircuitBreakerPreservesUnattemptedRows(t *testing.T) {
	chunks := make([]store.RetrievalChunkRow, 5)
	for i := range chunks {
		chunks[i] = store.RetrievalChunkRow{ChunkID: fmt.Sprintf("chunk-%d", i), ChunkTextHash: fmt.Sprintf("hash-%d", i), Text: "text"}
	}
	st := &fakeStore{chunks: chunks}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: embedding.RetryableError(errors.New("down"))}
	var sleeps []time.Duration
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{
		Limit: 5, BatchSize: 1,
		sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	})
	if !errors.Is(err, ErrEmbedCircuitOpen) || len(provider.requests) != 3 || len(st.writes) != 1 || progress.Scanned != 1 || progress.Remaining != 4 {
		t.Fatalf("progress=%+v err=%v requests=%d writes=%d", progress, err, len(provider.requests), len(st.writes))
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("sleeps=%v", sleeps)
	}
	for _, req := range provider.requests {
		if !reflect.DeepEqual(req.Texts, []string{"text"}) {
			t.Fatalf("circuit retried different candidate: requests=%+v", provider.requests)
		}
	}
}

func TestEmbedManualLimitFiveThousandOneUsesTwoBoundedBatchCalls(t *testing.T) {
	chunks := make([]store.RetrievalChunkRow, 5001)
	for i := range chunks {
		chunks[i] = store.RetrievalChunkRow{ChunkID: fmt.Sprintf("chunk-%05d", i), ChunkTextHash: fmt.Sprintf("hash-%d", i), Text: "text"}
	}
	st := &fakeStore{chunks: chunks}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}}
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: len(chunks), BatchSize: 9000})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Generated != len(chunks) || len(provider.requests) != 2 || len(provider.requests[0].Texts) != 5000 || len(provider.requests[1].Texts) != 1 || len(st.writeBatches) != 2 || len(st.writeBatches[0]) != 5000 {
		t.Fatalf("progress=%+v requests=%d batches=%d", progress, len(provider.requests), len(st.writeBatches))
	}
	if want := []int{5000, 1, 1, 1}; !reflect.DeepEqual(st.candidateLimits, want) {
		t.Fatalf("candidate limits=%v want=%v; RunEmbedBatch must stay physically bounded", st.candidateLimits, want)
	}
}

func TestProfileUsesExportedProjectionAndChunkVersions(t *testing.T) {
	p := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	if p.ProjectionVersion != retrievalchunk.ProjectionVersion || p.ChunkerVersion != retrievalchunk.Version {
		t.Fatalf("profile=%+v", p)
	}
}

type fakeStatusStore struct {
	status      semanticreadiness.Snapshot
	err         error
	observedCap *int
	latest      *store.SemanticRefreshRun
	latestErr   error
}

func (f fakeStatusStore) SemanticReadinessSnapshotAt(_ context.Context, _ embedding.Profile, exactCap int, _ time.Time) (semanticreadiness.Snapshot, error) {
	if f.observedCap != nil {
		*f.observedCap = exactCap
	}
	return f.status, f.err
}

func (f fakeStatusStore) LatestSemanticRefreshRun(context.Context, string) (*store.SemanticRefreshRun, error) {
	return f.latest, f.latestErr
}

func TestStatusClampsConfiguredExactCapToSafetyCeiling(t *testing.T) {
	observedCap := 0
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	got, err := ReadStatus(context.Background(), fakeStatusStore{
		err: store.ErrRetrievalUnavailable, observedCap: &observedCap,
	}, Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}), true, true, 300_000, semanticindex.Capability{State: semanticindex.CapabilityUnsupported}, now)
	if err != nil {
		t.Fatal(err)
	}
	if observedCap != semanticreadiness.DefaultExactMaxChunks || got.Store.ExactMaxChunks != semanticreadiness.DefaultExactMaxChunks {
		t.Fatalf("observed_cap=%d store.exact_max_chunks=%d", observedCap, got.Store.ExactMaxChunks)
	}
}

func TestStatusPriorityKeepsConfiguredOffModeDisabled(t *testing.T) {
	got, err := ReadStatus(context.Background(), fakeStatusStore{err: store.ErrRetrievalUnavailable}, Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}), true, false, 25_000, semanticindex.Capability{State: semanticindex.CapabilityUnsupported}, time.Now())
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if got.Status != "disabled" {
		t.Fatalf("status=%q reason=%q", got.Status, got.Reason)
	}
}

func TestReadinessStatusDelegatesToPureEvaluator(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	snapshot := semanticreadiness.Snapshot{
		Available: true, ProfileExists: true, ProfileProvenanceValid: true,
		ExpectedParents: 2, CurrentParents: 1, PendingParents: 1, DirtyParents: 1,
		EstimatedNotReadyChunks: 1, OldestDirtyAt: now.Add(-time.Minute),
		ChunkableParents: 1, ParentsWithReadyChunk: 1, ChunkCount: 1, ReadyEmbeddings: 1,
		GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1, LatestRevision: 1, ObservedLatestRevision: 1,
		L0ReadyCount: 1, ObservedL0ReadyCount: 1,
	}
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	got, err := ReadStatus(context.Background(), fakeStatusStore{status: snapshot}, profile, true, true, 25_000, semanticindex.Capability{State: semanticindex.CapabilityUnsupported}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != string(semanticreadiness.StateCatchingUp) || !got.Searchable || got.Reason == "" || got.Store.EstimatedNotReadyChunks != 1 {
		t.Fatalf("status=%+v", got)
	}
}

func TestReadStatusCapabilityAdmission(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	exact := semanticreadiness.Snapshot{
		Available: true, ProfileExists: true, ProfileProvenanceValid: true,
		ExpectedParents: 1, CurrentParents: 1, ChunkableParents: 1, ParentsWithReadyChunk: 1,
		ChunkCount: 1, ReadyEmbeddings: 1,
		GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
		LatestRevision: 1, ObservedLatestRevision: 1,
		L0ReadyCount: 1, ObservedL0ReadyCount: 1,
	}
	active := exact
	active.ActiveGenerationID = "root"
	active.ActiveGenerationValid = true
	active.ActiveSnapshotRevision = 1
	active.ActiveGenerationBackend = semanticindex.BackendUSearch
	active.ActiveGenerationBackendVersion = semanticindex.USearchVersion
	active.ActiveGenerationDistanceMetric = "cosine"
	active.ActiveGenerationDimensions = 2
	active.ActiveIndexedCount = 1
	corrupt := active
	corrupt.ActiveGenerationValid = false
	disabled := active
	needsIndex := active
	needsIndex.L0ReadyCount = semanticreadiness.CatchUpL0Limit + 1
	needsIndex.ObservedL0ReadyCount = semanticreadiness.CatchUpL0Limit + 1

	tests := []struct {
		name       string
		snapshot   semanticreadiness.Snapshot
		capability semanticindex.Capability
		enabled    bool
		wantState  semanticreadiness.State
		wantReason string
		searchable bool
	}{
		{
			name: "exact small remains ready without native support", snapshot: exact,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			enabled:    true,
			wantState:  semanticreadiness.StateReady, searchable: true,
		},
		{
			name: "matching native backend is admitted", snapshot: active,
			capability: semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion},
			enabled:    true,
			wantState:  semanticreadiness.StateReady, searchable: true,
		},
		{
			name: "corrupt active generation keeps readiness repair state", snapshot: corrupt,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			enabled:    true,
			wantState:  semanticreadiness.StateCorrupt, wantReason: "active semantic generation provenance is unproven",
		},
		{
			name: "disabled mode keeps readiness repair state despite old active generation", snapshot: disabled,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantState:  semanticreadiness.StateDisabled, wantReason: "semantic retrieval mode is off",
		},
		{
			name: "needs index active generation keeps readiness repair state", snapshot: needsIndex,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			enabled:    true,
			wantState:  semanticreadiness.StateNeedsIndex, wantReason: "active semantic generation exceeds the L0 or tombstone safety limit",
		},
		{
			name: "unsupported native backend is unavailable", snapshot: active,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			enabled:    true,
			wantState:  semanticreadiness.StateUnavailable, wantReason: "native_backend_unsupported",
		},
		{
			name: "broken native backend is unavailable", snapshot: active,
			capability: semanticindex.Capability{State: semanticindex.CapabilitySupportedBroken, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion, Reason: "load /private/tmp/libusearch.dylib failed"},
			enabled:    true,
			wantState:  semanticreadiness.StateUnavailable, wantReason: "native_backend_broken: load [path] failed",
		},
		{
			name: "native provenance mismatch is unavailable", snapshot: active,
			capability: semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: "2.25.0"},
			enabled:    true,
			wantState:  semanticreadiness.StateUnavailable, wantReason: "native_backend_provenance_mismatch",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadStatus(context.Background(), fakeStatusStore{status: tc.snapshot}, profile, true, tc.enabled, 25_000, tc.capability, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != string(tc.wantState) || got.Searchable != tc.searchable {
				t.Fatalf("status=%+v want_state=%s searchable=%t", got, tc.wantState, tc.searchable)
			}
			if tc.wantReason != "" && got.Reason != tc.wantReason {
				t.Fatalf("reason=%q want=%q", got.Reason, tc.wantReason)
			}
			if got.BackendCapability != tc.capability {
				t.Fatalf("backend_capability=%+v want=%+v", got.BackendCapability, tc.capability)
			}
			payload, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(payload), `"backend_capability":`) || strings.Contains(string(payload), `"problems":null`) || strings.Contains(string(payload), `"next_steps":null`) {
				t.Fatalf("status JSON=%s", payload)
			}
		})
	}

	unconfigured, err := ReadStatus(context.Background(), nil, embedding.Profile{}, false, false, 25_000, semanticindex.Capability{State: semanticindex.CapabilityUnsupported}, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(unconfigured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"backend_capability":{"state":"unsupported"}`) || strings.Contains(string(payload), `"problems":null`) || strings.Contains(string(payload), `"next_steps":null`) {
		t.Fatalf("unconfigured status JSON=%s", payload)
	}
}

func TestReadStatusNativeArtifactValidation(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	snapshot := semanticreadiness.Snapshot{
		Available: true, ProfileExists: true, ProfileProvenanceValid: true,
		ExpectedParents: 1, CurrentParents: 1, ChunkableParents: 1, ParentsWithReadyChunk: 1,
		ChunkCount: 1, ReadyEmbeddings: 1,
		GlobalPurgeEpoch: 1, ProfilePurgeEpoch: 1,
		LatestRevision: 1, ObservedLatestRevision: 1,
		L0ReadyCount: 1, ObservedL0ReadyCount: 1,
		ActiveGenerationID: "root", ActiveGenerationValid: true, ActiveSnapshotRevision: 1,
		ActiveGenerationBackend: semanticindex.BackendUSearch, ActiveGenerationBackendVersion: semanticindex.USearchVersion,
		ActiveGenerationDistanceMetric: "cosine", ActiveGenerationDimensions: 2, ActiveIndexedCount: 1,
	}
	capability := semanticindex.Capability{State: semanticindex.CapabilitySupportedReady, Backend: semanticindex.BackendUSearch, Version: semanticindex.USearchVersion}

	for _, tc := range []struct {
		name       string
		validate   func(context.Context, semanticreadiness.Snapshot) error
		searchable bool
		wantStatus semanticreadiness.State
		wantReason string
	}{
		{name: "healthy native artifact remains ready", validate: func(context.Context, semanticreadiness.Snapshot) error { return nil }, searchable: true, wantStatus: semanticreadiness.StateReady},
		{name: "damaged native artifact is unavailable", validate: func(context.Context, semanticreadiness.Snapshot) error {
			return errors.New("open /private/cache/root.json: no such file")
		}, searchable: false, wantStatus: semanticreadiness.StateUnavailable, wantReason: "native_root_artifacts_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadStatusWithNativeValidation(context.Background(), fakeStatusStore{status: snapshot}, profile, true, true, 25_000, capability, now, tc.validate)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != string(tc.wantStatus) || got.Searchable != tc.searchable || (tc.wantReason != "" && got.Reason != tc.wantReason) {
				t.Fatalf("status=%+v", got)
			}
			if strings.Contains(strings.Join(got.Problems, " "), "/private/cache") {
				t.Fatalf("status leaked artifact path: %+v", got)
			}
		})
	}

	t.Run("unsupported native backend does not validate artifacts", func(t *testing.T) {
		called := false
		got, err := ReadStatusWithNativeValidation(context.Background(), fakeStatusStore{status: snapshot}, profile, true, true, 25_000, semanticindex.Capability{State: semanticindex.CapabilityUnsupported}, now, func(context.Context, semanticreadiness.Snapshot) error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if called || got.Searchable || got.Reason != "native_backend_unsupported" {
			t.Fatalf("called=%t status=%+v", called, got)
		}
	})

	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run("validator interruption propagates: "+want.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			got, err := ReadStatusWithNativeValidation(ctx, fakeStatusStore{status: snapshot}, profile, true, true, 25_000, capability, now, func(context.Context, semanticreadiness.Snapshot) error {
				if errors.Is(want, context.Canceled) {
					cancel()
				}
				return want
			})
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want %v", err, want)
			}
			if got.Status == string(semanticreadiness.StateUnavailable) || got.Reason == "native_root_artifacts_unavailable" || !got.Searchable || len(got.Problems) != 0 {
				t.Fatalf("interrupted status=%+v want no artifact downgrade", got)
			}
		})
	}

	t.Run("canceled context takes precedence over artifact validation error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got, err := ReadStatusWithNativeValidation(ctx, fakeStatusStore{status: snapshot}, profile, true, true, 25_000, capability, now, func(context.Context, semanticreadiness.Snapshot) error {
			cancel()
			return errors.New("open /private/cache/root.json: no such file")
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v want context cancellation", err)
		}
		if got.Status == string(semanticreadiness.StateUnavailable) || got.Reason == "native_root_artifacts_unavailable" || !got.Searchable || len(got.Problems) != 0 {
			t.Fatalf("interrupted status=%+v want no artifact downgrade", got)
		}
	})
}

func TestBoundedProgressJSONUsesOnlyLatestSnapshot(t *testing.T) {
	last := Progress{Stage: "embed", Scanned: 8}
	payload, err := json.Marshal(Progress{Stage: "embed", Interrupted: true, Quarantined: 2, SnapshotCount: 9, SnapshotsTruncated: true, LastSnapshot: &last, Snapshots: []Progress{last}})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || strings.Contains(string(payload), `"snapshots"`) || !strings.Contains(string(payload), `"interrupted":true`) || !strings.Contains(string(payload), `"snapshot_count":9`) || !strings.Contains(string(payload), `"snapshots_truncated":true`) || !strings.Contains(string(payload), `"last_snapshot"`) {
		t.Fatalf("progress JSON=%s", payload)
	}
}
