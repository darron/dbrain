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
}

func (f *fakeStore) ProjectionWorkRevision(context.Context) (int64, error) {
	if f.workRevision > 0 {
		return f.workRevision, nil
	}
	return int64(len(f.parents)), nil
}

func (f *fakeStore) ListDirtyRetrievalParents(_ context.Context, watermark int64, limit int) ([]store.RetrievalParentWork, error) {
	f.watermarks = append(f.watermarks, watermark)
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
	cp := store.RetrievalProjectionCheckpoint{WorkID: workID, DirtyRevision: input.DirtyRevision, ParentKind: input.ParentKind, ParentSourceKey: input.ParentSourceKey, ProjectionHash: input.ProjectionHash, SectionKey: input.Cursor.SectionKey, NextBoundary: input.Cursor.NextBoundary, StagedChunks: chunks}
	f.staging[input.ParentKind+":"+input.ParentSourceKey] = cp
	return cp, nil
}

func (f *fakeStore) PromoteRetrievalProjectionStaging(_ context.Context, checkpoint store.RetrievalProjectionCheckpoint) (store.ChunkReplaceResult, error) {
	f.promotions = append(f.promotions, checkpoint)
	delete(f.staging, checkpoint.ParentKind+":"+checkpoint.ParentSourceKey)
	if f.applied == nil {
		f.applied = make(map[string]bool)
	}
	f.applied[checkpoint.ParentKind+":"+checkpoint.ParentSourceKey] = true
	return store.ChunkReplaceResult{Created: checkpoint.StagedChunks}, nil
}

func (f *fakeStore) BlockRetrievalProjectionTooLarge(_ context.Context, parent retrievalchunk.Parent, revision int64, projectionHash string) error {
	f.blockedGiant = append(f.blockedGiant, parent.Kind+":"+parent.SourceKey)
	delete(f.staging, parent.Kind+":"+parent.SourceKey)
	if f.applied == nil {
		f.applied = make(map[string]bool)
	}
	f.applied[parent.Kind+":"+parent.SourceKey] = true
	return nil
}
func (f *fakeStore) ListChunksNeedingEmbeddingForProfileAt(_ context.Context, profile embedding.Profile, _ string, _ int, _ time.Time) ([]store.RetrievalChunkRow, error) {
	f.operations = append(f.operations, "candidates")
	f.candidateProfile = profile
	if f.candidateErr != nil {
		return nil, f.candidateErr
	}
	return append([]store.RetrievalChunkRow(nil), f.chunks...), nil
}
func (f *fakeStore) RetrievalPurgeEpoch(context.Context) (int64, error) { return f.purgeEpoch, nil }
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
	return f.verification, nil
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
	if len(st.stageCalls) != 2 || len(st.promotions) != 0 || first.Checkpoint == nil || first.Remaining != 1 {
		t.Fatalf("first=%+v stage_calls=%d promotions=%d", first, len(st.stageCalls), len(st.promotions))
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
	if want := []string{"count", "candidates", "batch", "batch"}; !reflect.DeepEqual(st.operations, want) {
		t.Fatalf("normal embed operations=%v want=%v; it must not scan ready vectors", st.operations, want)
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
	for _, tc := range []struct {
		name  string
		err   error
		want  store.RetrievalEmbeddingStatus
		fatal bool
	}{
		{"retry", embedding.RetryableError(errors.New("down")), store.RetrievalEmbeddingError, false},
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
			if tc.want == store.RetrievalEmbeddingError && !st.writes[0].NextAttemptAt.After(now) {
				t.Fatalf("retry time=%s", st.writes[0].NextAttemptAt)
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

func TestSemanticVerifyRejectsProfileRootAndRevisionProvenance(t *testing.T) {
	profile := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	profileID, _ := profile.ID()
	valid := embedding.EncodeDenseF32([]float32{0.6, 0.8})
	base := store.RetrievalEmbeddingVerificationState{ProfileID: profileID, Profile: profile, LatestRevision: 2, PurgeEpoch: 1, GlobalPurgeEpoch: 1}
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

func TestEmbedCircuitBreakerPreservesUnattemptedRows(t *testing.T) {
	chunks := make([]store.RetrievalChunkRow, 5)
	for i := range chunks {
		chunks[i] = store.RetrievalChunkRow{ChunkID: fmt.Sprintf("chunk-%d", i), ChunkTextHash: fmt.Sprintf("hash-%d", i), Text: "text"}
	}
	st := &fakeStore{chunks: chunks}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: embedding.RetryableError(errors.New("down"))}
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 5, BatchSize: 1})
	if !errors.Is(err, ErrEmbedCircuitOpen) || len(provider.requests) != 3 || len(st.writes) != 3 || progress.Scanned != 3 || progress.Remaining != 2 {
		t.Fatalf("progress=%+v err=%v requests=%d writes=%d", progress, err, len(provider.requests), len(st.writes))
	}
}

func TestEmbedCapsProviderAndPersistenceBatches(t *testing.T) {
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
}

func TestProfileUsesExportedProjectionAndChunkVersions(t *testing.T) {
	p := Profile(embedding.Info{Provider: "fake", Model: "m", Dimensions: 2})
	if p.ProjectionVersion != retrievalchunk.ProjectionVersion || p.ChunkerVersion != retrievalchunk.Version {
		t.Fatalf("profile=%+v", p)
	}
}

type fakeStatusStore struct {
	status store.RetrievalStatus
	err    error
}

func (f fakeStatusStore) RetrievalStatusAt(context.Context, string, time.Time) (store.RetrievalStatus, error) {
	return f.status, f.err
}

func TestStatusPriorityKeepsConfiguredOffModeDisabled(t *testing.T) {
	got, err := ReadStatus(context.Background(), fakeStatusStore{err: store.ErrRetrievalUnavailable}, "profile", true, false, time.Now())
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if got.Status != "disabled" {
		t.Fatalf("status=%q reason=%q", got.Status, got.Reason)
	}
}

func TestBoundedProgressJSONUsesOnlyLatestSnapshot(t *testing.T) {
	last := Progress{Stage: "embed", Scanned: 8}
	payload, err := json.Marshal(Progress{Stage: "embed", Quarantined: 2, SnapshotCount: 9, SnapshotsTruncated: true, LastSnapshot: &last, Snapshots: []Progress{last}})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || strings.Contains(string(payload), `"snapshots"`) || !strings.Contains(string(payload), `"snapshot_count":9`) || !strings.Contains(string(payload), `"snapshots_truncated":true`) || !strings.Contains(string(payload), `"last_snapshot"`) {
		t.Fatalf("progress JSON=%s", payload)
	}
}
