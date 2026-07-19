package semanticindex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/store"
)

type fakeReadyStore struct {
	rows       []store.RetrievalEmbeddingRow
	err        error
	gotLimit   int
	gotProfile string
}

func (f *fakeReadyStore) ListReadyEmbeddings(_ context.Context, profileID string, limit int) ([]store.RetrievalEmbeddingRow, error) {
	f.gotProfile, f.gotLimit = profileID, limit
	if f.err != nil {
		return nil, f.err
	}
	rows := append([]store.RetrievalEmbeddingRow(nil), f.rows...)
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func TestExactSearchComputesCosineDistanceAndDeterministicTies(t *testing.T) {
	st := &fakeReadyStore{rows: []store.RetrievalEmbeddingRow{
		readyRow("z", "profile", []float32{0, 1}, "item", "z", "raw"),
		readyRow("best", "profile", []float32{1, 0}, "item", "best", "raw"),
		readyRow("a", "profile", []float32{0, 1}, "item", "a", "raw"),
		readyRow("worst", "profile", []float32{-1, 0}, "item", "worst", "raw"),
	}}
	hits, status, err := NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 3, MaxChunks: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateSearched || status.Backend != BackendExact || status.Scanned != 4 {
		t.Fatalf("status=%+v", status)
	}
	if got := []string{hits[0].ChunkID, hits[1].ChunkID, hits[2].ChunkID}; !reflect.DeepEqual(got, []string{"best", "a", "z"}) {
		t.Fatalf("hit order=%v hits=%+v", got, hits)
	}
	if hits[0].Rank != 1 || hits[1].Rank != 2 || math.Abs(hits[0].Distance) > 1e-9 || math.Abs(hits[1].Distance-1) > 1e-9 {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestExactSearchFiltersBeforeTopK(t *testing.T) {
	st := &fakeReadyStore{rows: []store.RetrievalEmbeddingRow{
		readyRow("disallowed", "profile", []float32{1, 0}, "item", "blocked-parent", "raw"),
		readyRow("allowed", "profile", []float32{0.6, 0.8}, "source", "allowed-parent", "summary"),
	}}
	hits, status, err := NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 1, MaxChunks: 10,
		Filters: Filters{
			AllowedParentKeys:    []string{" allowed-parent "},
			AllowedParentKinds:   []string{"source"},
			AllowedEvidenceRoles: []string{"summary"},
		},
	})
	if err != nil || status.State != StateSearched {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if len(hits) != 1 || hits[0].ChunkID != "allowed" {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestExactSearchFiltersSourceTypeBeforeTopKAndCarriesMetadata(t *testing.T) {
	disallowed := readyRow("disallowed", "profile", []float32{1, 0}, "item", "blocked-parent", "raw")
	disallowed.SourceType = "x_bookmark"
	allowed := readyRow("allowed", "profile", []float32{0.6, 0.8}, "source", "allowed-parent", "summary")
	allowed.SourceType, allowed.SectionOrdinal = "article", 4
	st := &fakeReadyStore{rows: []store.RetrievalEmbeddingRow{disallowed, allowed}}
	hits, status, err := NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 1, MaxChunks: 10,
		Filters: Filters{AllowedSourceTypes: []string{" ARTICLE "}},
	})
	if err != nil || status.State != StateSearched {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if len(hits) != 1 || hits[0].ChunkID != "allowed" || hits[0].SourceType != "article" || hits[0].SectionOrdinal != 4 {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestExactSearchSourceTypeFamilyFilter(t *testing.T) {
	x := readyRow("x", "profile", []float32{1, 0}, "item", "x", "raw")
	x.SourceType = "x_bookmark"
	article := readyRow("article", "profile", []float32{0, 1}, "source", "article", "raw")
	article.SourceType = "article"
	hits, status, err := NewExact(&fakeReadyStore{rows: []store.RetrievalEmbeddingRow{x, article}}).Search(context.Background(), []float32{1, 0}, SearchOptions{Profile: exactTestProfile(), Limit: 2, MaxChunks: 10, Filters: Filters{AllowedSourceTypes: []string{"x"}}})
	if err != nil || status.State != StateSearched || len(hits) != 1 || hits[0].ChunkID != "x" {
		t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
	}
}

func TestExactSearchCorruptRowFailsClosedWithoutPartialHits(t *testing.T) {
	healthy := readyRow("healthy", "profile", []float32{1, 0}, "item", "healthy", "raw")
	corrupt := readyRow("corrupt", "profile", []float32{0, 1}, "item", "corrupt", "raw")
	corrupt.VectorBytes = []byte{1}
	hits, status, err := NewExact(&fakeReadyStore{rows: []store.RetrievalEmbeddingRow{healthy, corrupt}}).Search(context.Background(), []float32{1, 0}, SearchOptions{Profile: exactTestProfile(), Limit: 5, MaxChunks: 10})
	if err != nil || status.Reason != ReasonIndexCorrupt || hits == nil || len(hits) != 0 {
		t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
	}
}

func TestExactSearchEnforcesCapAtExactBoundary(t *testing.T) {
	rows := []store.RetrievalEmbeddingRow{
		readyRow("a", "profile", []float32{1, 0}, "item", "a", "raw"),
		readyRow("b", "profile", []float32{0, 1}, "item", "b", "raw"),
	}
	st := &fakeReadyStore{rows: rows}
	hits, status, err := NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 1, MaxChunks: 2,
	})
	if err != nil || status.State != StateSearched || len(hits) != 1 || st.gotLimit != 3 {
		t.Fatalf("hits=%+v status=%+v err=%v requested_limit=%d", hits, status, err, st.gotLimit)
	}
	st.rows = append(rows, readyRow("c", "profile", []float32{-1, 0}, "item", "c", "raw"))
	hits, status, err = NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 1, MaxChunks: 2,
	})
	if err != nil || status.State != StateUnavailable || status.Reason != ReasonTooLarge || hits == nil || len(hits) != 0 || st.gotLimit != 3 {
		t.Fatalf("hits=%+v status=%+v err=%v requested_limit=%d", hits, status, err, st.gotLimit)
	}
}

func TestExactSearchDistinguishesSearchedEmptyFromUnavailable(t *testing.T) {
	hits, status, err := NewExact(&fakeReadyStore{}).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 5, MaxChunks: 10,
	})
	if err != nil || status.State != StateSearched || status.Reason != ReasonNone || hits == nil || len(hits) != 0 {
		t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
	}
}

func TestExactSearchReportsProfileDimensionAndCorruptionUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  store.RetrievalEmbeddingRow
		err  error
		want StatusReason
	}{
		{name: "profile", row: readyRow("a", "other-profile", []float32{1, 0}, "item", "a", "raw"), want: ReasonProfileMismatch},
		{name: "dimension", row: readyRow("a", "profile", []float32{1, 0, 0}, "item", "a", "raw"), want: ReasonDimensionMismatch},
		{name: "non-unit", row: readyRow("a", "profile", []float32{3, 4}, "item", "a", "raw"), want: ReasonIndexCorrupt},
		{name: "wrong normalization", row: func() store.RetrievalEmbeddingRow {
			row := readyRow("a", "profile", []float32{1, 0}, "item", "a", "raw")
			row.Normalization = embedding.NormalizationNone
			return row
		}(), want: ReasonIndexCorrupt},
		{name: "typed corruption", err: &store.RetrievalEmbeddingCorruptionError{ChunkID: "a", ProfileID: "profile", Reason: "bad bytes"}, want: ReasonIndexCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeReadyStore{err: tc.err}
			if tc.row.ChunkID != "" {
				st.rows = []store.RetrievalEmbeddingRow{tc.row}
			}
			hits, status, err := NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
				Profile: exactTestProfile(), Limit: 1, MaxChunks: 10,
			})
			if err != nil || status.State != StateUnavailable || status.Reason != tc.want || hits == nil || len(hits) != 0 {
				t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
			}
		})
	}
}

func TestExactSearchRejectsMislabeledProviderAndModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*store.RetrievalEmbeddingRow)
	}{
		{name: "provider", mutate: func(row *store.RetrievalEmbeddingRow) { row.Provider = "other-provider" }},
		{name: "model", mutate: func(row *store.RetrievalEmbeddingRow) { row.Model = "other-model" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := readyRow("a", "profile", []float32{1, 0}, "item", "a", "raw")
			tc.mutate(&row)
			hits, status, err := NewExact(&fakeReadyStore{rows: []store.RetrievalEmbeddingRow{row}}).Search(context.Background(), []float32{1, 0}, SearchOptions{
				Profile: exactTestProfile(), Limit: 1, MaxChunks: 10,
			})
			if err != nil || status.State != StateUnavailable || status.Reason != ReasonProfileMismatch || hits == nil || len(hits) != 0 {
				t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
			}
		})
	}
}

func TestExactSearchReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hits, status, err := NewExact(&fakeReadyStore{}).Search(ctx, []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 1, MaxChunks: 10,
	})
	if !errors.Is(err, context.Canceled) || status.State != StateUnavailable || status.Reason != ReasonCanceled || hits == nil {
		t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
	}
}

func TestExactSearchReportsCancellationReturnedByStore(t *testing.T) {
	st := &fakeReadyStore{err: fmt.Errorf("read ready embeddings: %w", context.Canceled)}
	hits, status, err := NewExact(st).Search(context.Background(), []float32{1, 0}, SearchOptions{
		Profile: exactTestProfile(), Limit: 1, MaxChunks: 10,
	})
	if !errors.Is(err, context.Canceled) || status.State != StateUnavailable || status.Reason != ReasonCanceled || hits == nil {
		t.Fatalf("hits=%+v status=%+v err=%v", hits, status, err)
	}
}

func readyRow(id, profile string, vector []float32, parentKind, parentKey, role string) store.RetrievalEmbeddingRow {
	if profile == "profile" {
		profile, _ = exactTestProfile().ID()
	}
	return store.RetrievalEmbeddingRow{
		ChunkID: id, ProfileID: profile, Provider: "fake", Model: "m", Dimensions: len(vector),
		Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2,
		VectorBytes: embedding.EncodeDenseF32(vector), ChunkTextHash: "hash-" + id,
		Status: store.RetrievalEmbeddingReady, ParentKind: parentKind, ParentSourceKey: parentKey,
		EvidenceRole: role, Text: "text " + id,
	}
}

func exactTestProfile() embedding.Profile {
	return embedding.Profile{
		Provider: "fake", Model: "m", ProjectionVersion: "projection-v1", ChunkerVersion: "chunker-v1",
		Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2, Dimensions: 2,
	}
}
