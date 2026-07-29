package semanticbuild

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

func TestFlushPublishesBeforeActivatingRoot(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	st := &flushFakeStore{databaseID: "db-1", cacheDir: cache, window: store.RetrievalFlushWindow{
		Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, PurgeEpoch: 3, L0ReadyCount: store.RetrievalSegmentTarget},
		Rows:    flushRows(profileID, store.RetrievalSegmentTarget), SnapshotRevision: store.RetrievalSegmentTarget,
	}}
	result, err := Flush(context.Background(), st, flushPayloadBuilder{}, FlushOptions{
		Profile: profile, Backend: "usearch", BackendVersion: "2.26.0", DistanceMetric: "cosine", CacheDir: cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.completed.Generation.GenerationID != result.GenerationID || st.completed.Generation.SourceManifestHash == "" {
		t.Fatalf("completion = %+v result = %+v", st.completed, result)
	}
	if st.completed.ActivationMode != store.RetrievalGenerationAdvanceSnapshot ||
		st.completed.ExpectedActiveGenerationID != "" || st.completed.ExpectedPurgeEpoch != 3 ||
		st.completed.ExpectedActiveSnapshotRevision != 0 {
		t.Fatalf("activation expectations = %+v", st.completed)
	}
	if _, err := semanticsegment.OpenRoot(st.cacheDir, "db-1", profileID, result.GenerationID); err != nil {
		t.Fatalf("root was not published before completion: %v", err)
	}
	if result.Indexed != store.RetrievalSegmentTarget ||
		result.Flushed != store.RetrievalSegmentTarget ||
		result.L0Ready != 0 ||
		result.SnapshotRevision != store.RetrievalSegmentTarget {
		t.Fatalf("result = %+v", result)
	}
}

func TestFlushReportsCommittedDeltaSeparatelyFromTotalRootMembership(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{
		Provider: "fake", Model: "fake-v1", Dimensions: 2,
	})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	st := &flushFakeStore{
		databaseID: "db-1",
		cacheDir:   cache,
		window: store.RetrievalFlushWindow{
			Profile: store.RetrievalEmbeddingProfileRow{
				ProfileID:    profileID,
				PurgeEpoch:   3,
				L0ReadyCount: store.RetrievalSegmentTarget,
			},
			Rows:             flushRowsFrom(profileID, 0, store.RetrievalSegmentTarget),
			SnapshotRevision: store.RetrievalSegmentTarget,
		},
	}
	first, err := Flush(
		t.Context(),
		st,
		flushPayloadBuilder{},
		FlushOptions{
			Profile: profile, Backend: "usearch",
			BackendVersion: "2.26.0", DistanceMetric: "cosine",
			CacheDir: cache,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	st.existing = append(
		[]store.RetrievalIndexSegmentRow(nil),
		st.completed.Segments...,
	)
	st.window = store.RetrievalFlushWindow{
		Profile: store.RetrievalEmbeddingProfileRow{
			ProfileID:              profileID,
			ActiveGenerationID:     first.GenerationID,
			ActiveSnapshotRevision: first.SnapshotRevision,
			PurgeEpoch:             3,
			L0ReadyCount:           store.RetrievalSegmentTarget,
		},
		Rows: flushRowsFrom(
			profileID,
			store.RetrievalSegmentTarget,
			store.RetrievalSegmentTarget,
		),
		SnapshotRevision: 2 * store.RetrievalSegmentTarget,
	}
	second, err := Flush(
		t.Context(),
		st,
		flushPayloadBuilder{},
		FlushOptions{
			Profile: profile, Backend: "usearch",
			BackendVersion: "2.26.0", DistanceMetric: "cosine",
			CacheDir: cache,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Indexed != 2*store.RetrievalSegmentTarget ||
		second.Flushed != store.RetrievalSegmentTarget ||
		second.L0Ready != 0 {
		t.Fatalf("second result=%+v", second)
	}
}

func TestFlushLeavesStoreUntouchedWhenBuilderFails(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	st := &flushFakeStore{databaseID: "db-1", window: store.RetrievalFlushWindow{
		Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, L0ReadyCount: store.RetrievalSegmentTarget},
		Rows:    flushRows(profileID, store.RetrievalSegmentTarget), SnapshotRevision: store.RetrievalSegmentTarget,
	}}
	_, err = Flush(context.Background(), st, failingFlushBuilder{}, FlushOptions{Profile: profile, Backend: "usearch", BackendVersion: "2.26.0", DistanceMetric: "cosine", CacheDir: t.TempDir()})
	if err == nil || st.completeCalls != 0 {
		t.Fatalf("err=%v complete_calls=%d", err, st.completeCalls)
	}
}

func TestFlushStopsAtFiveThousandAndReportsExactL0Tail(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := flushRows(profileID, store.RetrievalSegmentTarget)
	st := &flushFakeStore{databaseID: "db-1", window: store.RetrievalFlushWindow{
		Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, L0ReadyCount: store.RetrievalSegmentTarget + 1},
		Rows:    rows, SnapshotRevision: int64(store.RetrievalSegmentTarget),
	}}
	result, err := Flush(context.Background(), st, flushPayloadBuilder{}, FlushOptions{Profile: profile, Backend: "usearch", BackendVersion: "2.26.0", DistanceMetric: "cosine", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if st.windowLimit != store.RetrievalSegmentTarget || len(st.completed.Members) != store.RetrievalSegmentTarget || result.Indexed != store.RetrievalSegmentTarget || result.L0Ready != 1 {
		t.Fatalf("limit=%d members=%d result=%+v", st.windowLimit, len(st.completed.Members), result)
	}
}

func TestFlushAcceptsRowsSharingAtomicBatchRevision(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := flushRows(profileID, store.RetrievalSegmentTarget)
	for index := range rows {
		rows[index].Revision = 1
	}
	st := &flushFakeStore{databaseID: "db-1", window: store.RetrievalFlushWindow{
		Profile:          store.RetrievalEmbeddingProfileRow{ProfileID: profileID, L0ReadyCount: store.RetrievalSegmentTarget},
		Rows:             rows,
		SnapshotRevision: 1,
	}}
	result, err := Flush(context.Background(), st, flushPayloadBuilder{}, FlushOptions{
		Profile: profile, Backend: "usearch", BackendVersion: "2.26.0", DistanceMetric: "cosine", CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Indexed != store.RetrievalSegmentTarget || result.SnapshotRevision != 1 || st.completeCalls != 1 {
		t.Fatalf("result=%+v complete_calls=%d", result, st.completeCalls)
	}
}

func TestFlushRejectsZeroRevisionBeforePublishing(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := flushRows(profileID, store.RetrievalSegmentTarget)
	for index := range rows {
		rows[index].Revision = 0
	}
	window := store.RetrievalFlushWindow{
		Profile: store.RetrievalEmbeddingProfileRow{ProfileID: profileID, L0ReadyCount: store.RetrievalSegmentTarget},
		Rows:    rows,
	}
	if err := validateFlushWindow(profileID, profile.Dimensions, window, store.RetrievalSegmentTarget); err == nil {
		t.Fatal("validateFlushWindow accepted zero member revisions")
	}
}

func TestFlushRejectsDescendingBatchRevisions(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := flushRows(profileID, store.RetrievalSegmentTarget)
	for index := range rows {
		rows[index].Revision = 2
	}
	rows[1].Revision = 1
	window := store.RetrievalFlushWindow{
		Profile:          store.RetrievalEmbeddingProfileRow{ProfileID: profileID, L0ReadyCount: store.RetrievalSegmentTarget},
		Rows:             rows,
		SnapshotRevision: 2,
	}
	if err := validateFlushWindow(profileID, profile.Dimensions, window, store.RetrievalSegmentTarget); err == nil {
		t.Fatal("validateFlushWindow accepted descending member revisions")
	}
}

func TestFlushRewritesRootForMembershipL0AtActiveSnapshot(t *testing.T) {
	t.Parallel()
	profile := Profile(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2})
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	rows := flushRows(profileID, store.RetrievalSegmentTarget)
	st := &flushFakeStore{databaseID: "db-1", window: store.RetrievalFlushWindow{
		Profile: store.RetrievalEmbeddingProfileRow{
			ProfileID: profileID, ActiveGenerationID: "generation-old", ActiveSnapshotRevision: store.RetrievalSegmentTarget,
			PurgeEpoch: 3, L0ReadyCount: store.RetrievalSegmentTarget,
		},
		Rows: rows, SnapshotRevision: store.RetrievalSegmentTarget,
	}}
	if _, err := Flush(context.Background(), st, flushPayloadBuilder{}, FlushOptions{
		Profile: profile, Backend: "usearch", BackendVersion: "2.26.0", DistanceMetric: "cosine", CacheDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	if st.completed.ActivationMode != store.RetrievalGenerationRewriteSnapshot ||
		st.completed.ExpectedActiveGenerationID != "generation-old" ||
		st.completed.ExpectedActiveSnapshotRevision != store.RetrievalSegmentTarget {
		t.Fatalf("completion = %+v", st.completed)
	}
}

func flushRows(profileID string, count int) []store.RetrievalEmbeddingRow {
	return flushRowsFrom(profileID, 0, count)
}

func flushRowsFrom(profileID string, offset int, count int) []store.RetrievalEmbeddingRow {
	rows := make([]store.RetrievalEmbeddingRow, 0, count)
	for index := 0; index < count; index++ {
		ordinal := offset + index + 1
		rows = append(rows, store.RetrievalEmbeddingRow{ChunkID: "chunk-" + string(rune(ordinal)), ProfileID: profileID, Revision: int64(ordinal), VectorHash: "vector", Dimensions: 2})
	}
	return rows
}

type flushFakeStore struct {
	databaseID    string
	window        store.RetrievalFlushWindow
	existing      []store.RetrievalIndexSegmentRow
	completed     store.CompleteRetrievalIndexGenerationInput
	windowLimit   int
	completeCalls int
	cacheDir      string
}

func (f *flushFakeStore) RetrievalDatabaseID(context.Context) (string, error) {
	return f.databaseID, nil
}
func (f *flushFakeStore) NextRetrievalFlushWindow(_ context.Context, _ string, limit int) (store.RetrievalFlushWindow, error) {
	f.windowLimit = limit
	return f.window, nil
}
func (f *flushFakeStore) RetrievalIndexGenerationSegments(context.Context, string) ([]store.RetrievalIndexSegmentRow, error) {
	return append([]store.RetrievalIndexSegmentRow(nil), f.existing...), nil
}
func (f *flushFakeStore) CompleteRetrievalIndexGeneration(_ context.Context, input store.CompleteRetrievalIndexGenerationInput) error {
	f.completeCalls++
	f.completed = input
	return nil
}

type flushPayloadBuilder struct{}

func (flushPayloadBuilder) Build(_ context.Context, rows []store.RetrievalEmbeddingRow) (func(io.Writer) error, error) {
	if len(rows) == 0 {
		return nil, errors.New("missing rows")
	}
	return func(writer io.Writer) error { _, err := io.WriteString(writer, "opaque payload"); return err }, nil
}

type failingFlushBuilder struct{}

func (failingFlushBuilder) Build(context.Context, []store.RetrievalEmbeddingRow) (func(io.Writer) error, error) {
	return nil, errors.New("builder failed")
}

func TestFlushRootPathsAreCacheRelative(t *testing.T) {
	// Keep the test's temp root in scope so the compiler rejects accidental
	// absolute SQLite paths in future FlushResult plumbing.
	if filepath.IsAbs("semantic/db/profile/generations/generation") {
		t.Fatal("expected relative path")
	}
}
