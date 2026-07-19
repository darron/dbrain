package semanticbuild

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

type fakeStore struct {
	parents        []retrievalchunk.Parent
	chunks         []store.RetrievalChunkRow
	replacements   []string
	writes         []store.RetrievalEmbeddingRow
	readyErrs      []error
	readyCalls     int
	blockErrs      []error
	blockCalls     []*store.RetrievalEmbeddingCorruptionError
	operations     []string
	candidateCount int
	replaceResult  store.ChunkReplaceResult
	replaceErrKey  string
}

func (f *fakeStore) ListRetrievalParents(_ context.Context, after string, limit int) ([]retrievalchunk.Parent, error) {
	var result []retrievalchunk.Parent
	keys := 0
	last := ""
	for _, parent := range f.parents {
		if parent.SourceKey <= after {
			continue
		}
		if parent.SourceKey != last {
			if keys >= limit {
				break
			}
			keys++
			last = parent.SourceKey
		}
		result = append(result, parent)
	}
	return result, nil
}

func (f *fakeStore) ReplaceRetrievalChunks(_ context.Context, kind, key string, chunks []retrievalchunk.Chunk) (store.ChunkReplaceResult, error) {
	f.replacements = append(f.replacements, kind+":"+key)
	if kind+":"+key == f.replaceErrKey {
		return store.ChunkReplaceResult{}, errors.New("replace failed")
	}
	if f.replaceResult != (store.ChunkReplaceResult{}) {
		return f.replaceResult, nil
	}
	return store.ChunkReplaceResult{Created: len(chunks)}, nil
}
func (f *fakeStore) ListChunksNeedingEmbeddingAt(context.Context, string, string, int, time.Time) ([]store.RetrievalChunkRow, error) {
	f.operations = append(f.operations, "candidates")
	return append([]store.RetrievalChunkRow(nil), f.chunks...), nil
}
func (f *fakeStore) PutRetrievalEmbedding(_ context.Context, row store.RetrievalEmbeddingRow) error {
	f.operations = append(f.operations, "put")
	f.writes = append(f.writes, row)
	return nil
}
func (f *fakeStore) CountChunksNeedingEmbeddingAt(context.Context, string, time.Time) (int, error) {
	f.operations = append(f.operations, "count")
	if f.candidateCount != 0 {
		return f.candidateCount, nil
	}
	return len(f.chunks), nil
}
func (f *fakeStore) ListReadyEmbeddings(context.Context, string, int) ([]store.RetrievalEmbeddingRow, error) {
	f.operations = append(f.operations, "validate")
	call := f.readyCalls
	f.readyCalls++
	if call < len(f.readyErrs) {
		return nil, f.readyErrs[call]
	}
	return make([]store.RetrievalEmbeddingRow, 0), nil
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
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, Progress: func(p ChunkProgress) error { snapshots = append(snapshots, p); return nil }})
	if err != nil {
		t.Fatalf("RunChunk: %v", err)
	}
	if progress.Scanned != 2 || len(st.replacements) != 2 || progress.Created != 2 || progress.Deleted != 0 || progress.Remaining != 0 || progress.HasMore || progress.NextAfterSourceKey != "a" || len(snapshots) != 1 || len(progress.Snapshots) != 1 {
		t.Fatalf("progress=%+v replacements=%v", progress, st.replacements)
	}
}

func TestChunkResumesAfterAtomicSourceKeyGroup(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{
		{Kind: "item", SourceKey: "a", ContentHash: "ha", Sections: []retrievalchunk.Section{{Role: "raw", Text: "alpha"}}},
		{Kind: "source", SourceKey: "a", ContentHash: "hb", Sections: []retrievalchunk.Section{{Role: "raw", Text: "bravo"}}},
		{Kind: "item", SourceKey: "b", ContentHash: "hc", Sections: []retrievalchunk.Section{{Role: "raw", Text: "charlie"}}},
	}}
	first, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.NextAfterSourceKey != "a" || first.Scanned != 2 {
		t.Fatalf("first=%+v", first)
	}
	second, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1, AfterSourceKey: first.NextAfterSourceKey})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || second.NextAfterSourceKey != "b" || second.Scanned != 1 {
		t.Fatalf("second=%+v", second)
	}
	if got := st.replacements; !reflect.DeepEqual(got, []string{"item:a", "source:a", "item:b"}) {
		t.Fatalf("replacements=%v", got)
	}
}

func TestChunkProgressAdvancesPerAtomicSourceKeyGroup(t *testing.T) {
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
	if len(snapshots) != 2 || len(progress.Snapshots) != 2 || snapshots[0].NextAfterSourceKey != "a" || snapshots[0].Remaining != 1 || snapshots[1].NextAfterSourceKey != "b" || snapshots[1].Remaining != 0 {
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
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err == nil {
		t.Fatal("RunChunk unexpectedly succeeded")
	}
	if progress.NextAfterSourceKey != "" || progress.Remaining != 1 {
		t.Fatalf("failed group progress=%+v, want prior cursor and one unfinished row", progress)
	}
}

func TestChunkClassifiesEmptyProjectionAsBlocked(t *testing.T) {
	st := &fakeStore{parents: []retrievalchunk.Parent{{Kind: "item", SourceKey: "empty", ContentHash: "hash"}}}
	progress, err := RunChunk(context.Background(), st, ChunkOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Blocked != 1 || progress.Current != 0 || progress.Generated != 0 || progress.NextAfterSourceKey != "empty" || len(st.replacements) != 1 {
		t.Fatalf("progress=%+v replacements=%v", progress, st.replacements)
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
	if progress.Generated != 3 || progress.Remaining != 2 || len(st.writes) != 3 || len(snapshots) != 2 || len(progress.Snapshots) != 2 {
		t.Fatalf("progress=%+v writes=%d", progress, len(st.writes))
	}
	if snapshots[0].Scanned != 2 || snapshots[0].Remaining != 3 || snapshots[1].Scanned != 3 || snapshots[1].Remaining != 2 {
		t.Fatalf("snapshots=%+v", snapshots)
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

func TestEmbedQuarantinesCorruptReadyRowsBeforeCandidateWork(t *testing.T) {
	corruption := &store.RetrievalEmbeddingCorruptionError{ChunkID: "corrupt", ProfileID: "profile", Reason: "bad bytes"}
	st := &fakeStore{
		readyErrs: []error{corruption, nil},
		chunks:    []store.RetrievalChunkRow{{ChunkID: "candidate", ChunkTextHash: "hash", Text: "candidate"}},
	}
	provider := &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}}
	progress, err := RunEmbed(context.Background(), st, provider, EmbedOptions{Limit: 1, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Quarantined != 1 || len(st.blockCalls) != 1 || st.blockCalls[0] != corruption {
		t.Fatalf("progress=%+v block_calls=%+v", progress, st.blockCalls)
	}
	wantPrefix := []string{"validate", "block", "validate", "count", "candidates", "put"}
	if !reflect.DeepEqual(st.operations, wantPrefix) {
		t.Fatalf("operations=%v want=%v", st.operations, wantPrefix)
	}
}

func TestEmbedRetriesValidationAfterRepairedCorruptionRace(t *testing.T) {
	corruption := &store.RetrievalEmbeddingCorruptionError{ChunkID: "repaired", ProfileID: "profile", Reason: "stale diagnostic"}
	st := &fakeStore{
		readyErrs: []error{corruption, nil},
		blockErrs: []error{store.ErrRetrievalEmbeddingNoLongerCorrupt},
	}
	progress, err := RunEmbed(context.Background(), st, &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}}, EmbedOptions{Limit: 1, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if progress.Quarantined != 0 || st.readyCalls != 2 || len(st.blockCalls) != 1 {
		t.Fatalf("progress=%+v ready_calls=%d block_calls=%d", progress, st.readyCalls, len(st.blockCalls))
	}
	if want := []string{"validate", "block", "validate", "count", "candidates"}; !reflect.DeepEqual(st.operations, want) {
		t.Fatalf("operations=%v want=%v", st.operations, want)
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

func TestProgressJSONUsesNonNullSnapshots(t *testing.T) {
	payload, err := json.Marshal(Progress{Stage: "embed", Quarantined: 2, Snapshots: make([]Progress, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || strings.Contains(string(payload), `"snapshots":null`) || !strings.Contains(string(payload), `"quarantined":2`) {
		t.Fatalf("progress JSON=%s", payload)
	}
}
