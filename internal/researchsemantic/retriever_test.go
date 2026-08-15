package researchsemantic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/store"
)

type fakeProvider struct {
	info     embedding.Info
	response embedding.Response
	err      error
	requests []embedding.Request
}

func (f *fakeProvider) Info() embedding.Info { return f.info }
func (f *fakeProvider) Embed(_ context.Context, req embedding.Request) (embedding.Response, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return embedding.Response{}, f.err
	}
	return f.response, nil
}

type fakeSearcher struct {
	hits     []semanticindex.Hit
	status   semanticindex.Status
	err      error
	queries  [][]float32
	options  []semanticindex.SearchOptions
	closes   int
	closeErr error
}

type generationContextSearcher struct {
	want GenerationLease
}

func (s *generationContextSearcher) Search(ctx context.Context, _ []float32, _ semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	if got := GenerationLeaseFromContext(ctx); got != s.want {
		return nil, semanticindex.Status{}, fmt.Errorf("generation lease in search context = %#v, want %#v", got, s.want)
	}
	return []semanticindex.Hit{}, semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact}, nil
}

func (f *fakeSearcher) Search(_ context.Context, query []float32, opts semanticindex.SearchOptions) ([]semanticindex.Hit, semanticindex.Status, error) {
	f.queries = append(f.queries, append([]float32(nil), query...))
	f.options = append(f.options, opts)
	return append([]semanticindex.Hit(nil), f.hits...), f.status, f.err
}

func (f *fakeSearcher) Close() error {
	f.closes++
	return f.closeErr
}

type fakeHydrationStore struct {
	rows  []store.RetrievalChunkEvidenceRow
	err   error
	calls [][]string
}

func (f *fakeHydrationStore) HydrateRetrievalChunks(_ context.Context, ids []string) ([]store.RetrievalChunkEvidenceRow, error) {
	f.calls = append(f.calls, append([]string(nil), ids...))
	return append([]store.RetrievalChunkEvidenceRow(nil), f.rows...), f.err
}

func TestRetrieverEmbedsCleanQueryAndBatchHydratesInHitOrder(t *testing.T) {
	profile := testProfile()
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	provider := unitProvider()
	searcher := &fakeSearcher{
		hits: []semanticindex.Hit{
			{ChunkID: "deleted", Rank: 1, Distance: 0.05},
			{ChunkID: "source-chunk", Rank: 2, Distance: 0.1, SourceType: "article", SectionOrdinal: 6},
			{ChunkID: "item-chunk", Rank: 3, Distance: 0.2, SourceType: "x_bookmark", SectionOrdinal: 7},
		},
		status: semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact, ProfileID: profileID, GenerationID: "generation-a"},
	}
	hydrator := &fakeHydrationStore{rows: []store.RetrievalChunkEvidenceRow{
		{
			ChunkID: "item-chunk", ParentKind: "item", ParentSourceKey: "item:one", EvidenceRole: "raw",
			Ordinal: 4, StartChar: 10, EndChar: 20, Heading: "Item heading", ChunkTextHash: "item-hash", Text: "item chunk text",
			Title: "Item title", URL: "https://example.com/item", NotePath: "items/one.md", Summary: "Item summary",
			Author: "Author @author", SourceType: "x_bookmark", PublishedAt: "2026-07-18", UserTags: "tag-a",
		},
		{
			ChunkID: "source-chunk", ParentKind: "source", ParentSourceKey: "src:one", EvidenceRole: "summary",
			Ordinal: 2, StartChar: 2, EndChar: 9, Heading: "Source heading", ChunkTextHash: "source-hash", Text: "source chunk text",
			Title: "Source title", URL: "https://example.com/source", NotePath: "sources/one.md", Summary: "Source summary",
			SourceType: "article", ExtractedAt: "2026-07-17T00:00:00Z", SummarizedAt: "2026-07-18T00:00:00Z", UserTags: "tag-b",
		},
	}}
	docs, status, err := New(provider, searcher, hydrator).Retrieve(context.Background(), "  semantic query  ", Options{
		Profile: profile, Limit: 3, MaxChunks: 25,
		Filters: semanticindex.Filters{AllowedParentKinds: []string{"item", "source"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != semanticindex.StateSearched || len(docs) != 2 || docs == nil {
		t.Fatalf("docs=%+v status=%+v", docs, status)
	}
	if len(provider.requests) != 1 || !reflect.DeepEqual(provider.requests[0], embedding.Request{Texts: []string{"semantic query"}, Purpose: embedding.PurposeQuery}) {
		t.Fatalf("provider requests=%+v", provider.requests)
	}
	if len(searcher.options) != 1 || !reflect.DeepEqual(searcher.options[0].Profile, profile) || searcher.options[0].MaxChunks != 25 {
		t.Fatalf("search options=%+v", searcher.options)
	}
	if len(hydrator.calls) != 1 || !reflect.DeepEqual(hydrator.calls[0], []string{"deleted", "source-chunk", "item-chunk"}) {
		t.Fatalf("hydrate calls=%v", hydrator.calls)
	}
	if docs[0].SourceKey != "src:one" || docs[1].SourceKey != "item:one" || docs[0].Excerpt != "source chunk text" || docs[0].EvidenceRole != "summary" {
		t.Fatalf("docs=%+v", docs)
	}
	if docs[0].Chunk == nil || docs[0].Chunk.ID != "source-chunk" || docs[0].Chunk.Index != 2 || docs[0].Chunk.SectionOrdinal != 6 || docs[0].Chunk.Hash != "source-hash" || docs[0].Chunk.Heading != "Source heading" || !reflect.DeepEqual(docs[0].Chunk.ContributingIDs, []string{"source-chunk"}) || docs[0].Chunk.WindowHash == "" {
		t.Fatalf("source chunk=%+v", docs[0].Chunk)
	}
	if docs[0].Chunk.WindowHash != retrieval.WindowHash([]string{"source-chunk"}, []string{"source-hash"}, "source chunk text") {
		t.Fatalf("singleton window hash=%q", docs[0].Chunk.WindowHash)
	}
	assertSemanticLane(t, docs[0], 2, 0.1, profileID, semanticindex.BackendExact, "generation-a")
	assertSemanticLane(t, docs[1], 3, 0.2, profileID, semanticindex.BackendExact, "generation-a")
}

func TestRetrieverPreservesSearchedEmptyWithoutHydration(t *testing.T) {
	provider := unitProvider()
	searcher := &fakeSearcher{hits: make([]semanticindex.Hit, 0), status: semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact}}
	hydrator := &fakeHydrationStore{}
	docs, status, err := New(provider, searcher, hydrator).Retrieve(context.Background(), "query", Options{Profile: testProfile(), Limit: 5, MaxChunks: 10})
	if err != nil || status.State != semanticindex.StateSearched || docs == nil || len(docs) != 0 || len(hydrator.calls) != 0 {
		t.Fatalf("docs=%+v status=%+v err=%v hydrate_calls=%v", docs, status, err, hydrator.calls)
	}
}

func TestRetrieverPinsGenerationThroughHydrationAndWriterIntentPreventsBarging(t *testing.T) {
	testCtx, cancelTest := context.WithCancel(t.Context())
	defer cancelTest()
	scope, err := semanticlock.NewScope(t.TempDir(), "database-query-pin")
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile()
	profileID, err := profile.ID()
	if err != nil {
		t.Fatal(err)
	}
	hydrationStarted := make(chan struct{})
	releaseHydration := make(chan struct{})
	firstHydrator := &blockingHydrationStore{
		started: hydrationStarted,
		release: releaseHydration,
		rows: []store.RetrievalChunkEvidenceRow{{
			ChunkID: "chunk-a", ParentKind: "source", ParentSourceKey: "source:a",
			EvidenceRole: "raw", ChunkTextHash: "hash-a", Text: "semantic evidence",
		}},
	}
	leasePaths := make(chan string, 1)
	acquire := GenerationLeaseAcquirer(func(ctx context.Context) (GenerationLease, error) {
		acquireCtx, cancelAcquire := context.WithTimeout(ctx, 40*time.Millisecond)
		defer cancelAcquire()
		lease, err := scope.AcquireGenerationShared(acquireCtx, "owner=research-query\n")
		if err == nil {
			select {
			case leasePaths <- lease.Path():
			default:
			}
		}
		return lease, err
	})
	first := NewWithGenerationLease(
		unitProvider(),
		&fakeSearcher{
			hits:   []semanticindex.Hit{{ChunkID: "chunk-a", Rank: 1, Distance: 0.1}},
			status: semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact, ProfileID: profileID},
		},
		firstHydrator,
		acquire,
	)
	firstResult := make(chan retrieveResult, 1)
	go func() {
		docs, status, err := first.Retrieve(testCtx, "first query", Options{Profile: profile, Limit: 1, MaxChunks: 10})
		firstResult <- retrieveResult{docs: docs, status: status, err: err}
	}()
	select {
	case <-hydrationStarted:
	case <-time.After(time.Second):
		t.Fatal("first query did not reach blocked hydration")
	}
	var generationPath string
	select {
	case generationPath = <-leasePaths:
	case <-time.After(time.Second):
		t.Fatal("first query did not acquire generation lease")
	}

	activationAcquired := make(chan struct{})
	releaseActivation := make(chan struct{})
	activationErr := make(chan error, 1)
	go func() {
		maintenance, err := scope.AcquireMaintenanceExclusive(testCtx, "owner=activation\n")
		if err != nil {
			activationErr <- err
			return
		}
		generation, err := maintenance.AcquireGenerationExclusive(testCtx, "owner=activation\n")
		if err != nil {
			activationErr <- errors.Join(err, maintenance.Close())
			return
		}
		close(activationAcquired)
		var waitErr error
		select {
		case <-releaseActivation:
		case <-testCtx.Done():
			waitErr = testCtx.Err()
		}
		activationErr <- errors.Join(waitErr, generation.Close(), maintenance.Close())
	}()
	waitForGenerationWriterIntent(t, generationPath)

	laterSearcher := &fakeSearcher{}
	laterHydrator := &fakeHydrationStore{}
	later := NewWithGenerationLease(unitProvider(), laterSearcher, laterHydrator, acquire)
	laterCtx, cancelLater := context.WithTimeout(t.Context(), time.Second)
	defer cancelLater()
	docs, status, err := later.Retrieve(laterCtx, "later query", Options{Profile: profile, Limit: 1, MaxChunks: 10})
	if err != nil || status.State != semanticindex.StateUnavailable || status.Reason != ReasonGenerationBusy || len(docs) != 0 {
		t.Fatalf("later docs=%+v status=%+v err=%v", docs, status, err)
	}
	if len(laterSearcher.queries) != 0 || len(laterHydrator.calls) != 0 {
		t.Fatalf("later query barged: searches=%d hydrations=%d", len(laterSearcher.queries), len(laterHydrator.calls))
	}
	select {
	case <-activationAcquired:
		t.Fatal("activation acquired while first query hydration was blocked")
	default:
	}

	close(releaseHydration)
	select {
	case result := <-firstResult:
		if result.err != nil || result.status.State != semanticindex.StateSearched || len(result.docs) != 1 {
			t.Fatalf("first result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("first query did not finish after hydration release")
	}
	select {
	case <-activationAcquired:
	case <-time.After(time.Second):
		t.Fatal("activation did not acquire after hydrated evidence construction")
	}
	close(releaseActivation)
	if err := <-activationErr; err != nil {
		t.Fatal(err)
	}
}

func TestRetrieverPlacesAcquiredGenerationLeaseInSearchContext(t *testing.T) {
	lease := &fakeGenerationLease{}
	retriever := NewWithGenerationLease(
		unitProvider(),
		&generationContextSearcher{want: lease},
		&fakeHydrationStore{},
		func(context.Context) (GenerationLease, error) { return lease, nil },
	)
	docs, status, err := retriever.Retrieve(t.Context(), "query", Options{Profile: testProfile(), Limit: 1, MaxChunks: 10})
	if err != nil || status.State != semanticindex.StateSearched || len(docs) != 0 {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
	if lease.closes != 1 {
		t.Fatalf("lease closes=%d want 1", lease.closes)
	}
}

func TestRetrieverGenerationLeaseReleaseFailureFailsClosed(t *testing.T) {
	releaseErr := errors.New("release generation failed")
	searcher := &fakeSearcher{
		hits:   []semanticindex.Hit{{ChunkID: "chunk-a", Rank: 1}},
		status: semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact},
	}
	hydrator := &fakeHydrationStore{rows: []store.RetrievalChunkEvidenceRow{{
		ChunkID: "chunk-a", ParentKind: "source", ParentSourceKey: "source:a",
		EvidenceRole: "raw", ChunkTextHash: "hash-a", Text: "semantic evidence",
	}}}
	lease := &fakeGenerationLease{err: releaseErr}
	retriever := NewWithGenerationLease(
		unitProvider(),
		searcher,
		hydrator,
		func(context.Context) (GenerationLease, error) { return lease, nil },
	)
	docs, status, err := retriever.Retrieve(t.Context(), "query", Options{Profile: testProfile(), Limit: 1, MaxChunks: 10})
	if !errors.Is(err, releaseErr) || status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonSearchError || len(docs) != 0 {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
	if lease.closes != 1 {
		t.Fatalf("lease closes=%d want 1", lease.closes)
	}
}

func TestRetrieverCancellationDuringHydrationReleasesGenerationLease(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	hydrationStarted := make(chan struct{})
	hydrator := &blockingHydrationStore{
		started: hydrationStarted,
		release: make(chan struct{}),
	}
	lease := &fakeGenerationLease{}
	retriever := NewWithGenerationLease(
		unitProvider(),
		&fakeSearcher{
			hits:   []semanticindex.Hit{{ChunkID: "chunk-a", Rank: 1}},
			status: semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact},
		},
		hydrator,
		func(context.Context) (GenerationLease, error) { return lease, nil },
	)
	result := make(chan retrieveResult, 1)
	go func() {
		docs, status, err := retriever.Retrieve(ctx, "query", Options{Profile: testProfile(), Limit: 1, MaxChunks: 10})
		result <- retrieveResult{docs: docs, status: status, err: err}
	}()
	select {
	case <-hydrationStarted:
	case <-time.After(time.Second):
		t.Fatal("query did not reach hydration")
	}
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.err, context.Canceled) || got.status.State != semanticindex.StateUnavailable || got.status.Reason != semanticindex.ReasonCanceled || len(got.docs) != 0 {
			t.Fatalf("result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled query did not return")
	}
	if lease.closes != 1 {
		t.Fatalf("lease closes=%d want 1", lease.closes)
	}
}

func TestRetrieverCallerDeadlineDuringGenerationAcquisitionRemainsCancellation(t *testing.T) {
	searcher := &fakeSearcher{}
	retriever := NewWithGenerationLease(
		unitProvider(),
		searcher,
		&fakeHydrationStore{},
		func(ctx context.Context) (GenerationLease, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	docs, status, err := retriever.Retrieve(ctx, "query", Options{Profile: testProfile(), Limit: 1, MaxChunks: 10})
	if !errors.Is(err, context.DeadlineExceeded) || status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonCanceled || len(docs) != 0 {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
	if len(searcher.queries) != 0 {
		t.Fatalf("search ran after caller deadline: queries=%d", len(searcher.queries))
	}
}

func TestRetrieverCloseClosesSearcherOnce(t *testing.T) {
	searcher := &fakeSearcher{}
	retriever := New(unitProvider(), searcher, &fakeHydrationStore{})
	if err := retriever.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retriever.Close(); err != nil {
		t.Fatal(err)
	}
	if searcher.closes != 1 {
		t.Fatalf("searcher closes=%d", searcher.closes)
	}
}

type retrieveResult struct {
	docs   []retrieval.EvidenceDocument
	status semanticindex.Status
	err    error
}

type blockingHydrationStore struct {
	started chan struct{}
	release chan struct{}
	rows    []store.RetrievalChunkEvidenceRow
}

func (b *blockingHydrationStore) HydrateRetrievalChunks(ctx context.Context, _ []string) ([]store.RetrievalChunkEvidenceRow, error) {
	close(b.started)
	select {
	case <-b.release:
		return append([]store.RetrievalChunkEvidenceRow(nil), b.rows...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type fakeGenerationLease struct {
	closes int
	err    error
}

func (l *fakeGenerationLease) Close() error {
	l.closes++
	return l.err
}

func waitForGenerationWriterIntent(t *testing.T, generationPath string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	pattern := generationPath + ".writer-*.intent"
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("generation writer intent did not appear at %s", pattern)
}

func TestRetrieverMapsProviderFailuresAndRejectsNonUnitQuery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider *fakeProvider
		want     semanticindex.StatusReason
	}{
		{name: "provider mismatch", provider: &fakeProvider{info: embedding.Info{Provider: "fake", Model: "other", Dimensions: 2}}, want: semanticindex.ReasonProfileMismatch},
		{name: "retryable", provider: &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: embedding.RetryableError(errors.New("offline"))}, want: semanticindex.ReasonProviderUnavailable},
		{name: "provider-owned deadline", provider: &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: embedding.RetryableError(context.DeadlineExceeded)}, want: semanticindex.ReasonProviderUnavailable},
		{name: "blocked", provider: &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, err: embedding.BlockedError(errors.New("bad input"))}, want: semanticindex.ReasonQueryEmbeddingFailed},
		{name: "non-unit", provider: &fakeProvider{info: embedding.Info{Provider: "fake", Model: "m", Dimensions: 2}, response: embedding.Response{Vectors: [][]float32{{3, 4}}, Provider: "fake", Model: "m", Dimensions: 2}}, want: semanticindex.ReasonQueryEmbeddingFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searcher := &fakeSearcher{}
			docs, status, err := New(tc.provider, searcher, &fakeHydrationStore{}).Retrieve(context.Background(), "query", Options{Profile: testProfile(), Limit: 5, MaxChunks: 10})
			if err != nil || status.State != semanticindex.StateUnavailable || status.Reason != tc.want || docs == nil || len(docs) != 0 || len(searcher.queries) != 0 {
				t.Fatalf("docs=%+v status=%+v err=%v search_queries=%v", docs, status, err, searcher.queries)
			}
		})
	}
}

func TestRetrieverFailsOpenWhenOllamaOwnedTimeoutExpiresBeforeLiveContext(t *testing.T) {
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(accepted)
		<-r.Context().Done()
	}))
	defer server.Close()

	provider, err := embedding.NewOllama(embedding.OllamaOptions{BaseURL: server.URL, Model: "m", Dimensions: 2, Timeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile()
	profile.Provider = embedding.ProviderOllama
	docs, status, err := New(provider, &fakeSearcher{}, &fakeHydrationStore{}).Retrieve(context.Background(), "query", Options{Profile: profile, Limit: 5, MaxChunks: 10})
	if err != nil || status.State != semanticindex.StateUnavailable || status.Reason != semanticindex.ReasonProviderUnavailable || docs == nil || len(docs) != 0 {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
	select {
	case <-accepted:
	default:
		t.Fatal("Ollama server never accepted the request")
	}
}

func TestRetrieverReturnsTypedCancellationAndSearchUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	docs, status, err := New(unitProvider(), &fakeSearcher{}, &fakeHydrationStore{}).Retrieve(ctx, "query", Options{Profile: testProfile(), Limit: 5, MaxChunks: 10})
	if !errors.Is(err, context.Canceled) || status.Reason != semanticindex.ReasonCanceled || docs == nil {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
	want := semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonTooLarge, Backend: semanticindex.BackendExact}
	searcher := &fakeSearcher{hits: make([]semanticindex.Hit, 0), status: want}
	docs, status, err = New(unitProvider(), searcher, &fakeHydrationStore{}).Retrieve(context.Background(), "query", Options{Profile: testProfile(), Limit: 5, MaxChunks: 10})
	if err != nil || status != want || docs == nil || len(docs) != 0 {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
}

func TestRetrieverPreservesProviderCancellation(t *testing.T) {
	provider := unitProvider()
	provider.err = fmt.Errorf("query embedding: %w", context.Canceled)
	docs, status, err := New(provider, &fakeSearcher{}, &fakeHydrationStore{}).Retrieve(context.Background(), "query", Options{Profile: testProfile(), Limit: 5, MaxChunks: 10})
	if !errors.Is(err, context.Canceled) || status.Reason != semanticindex.ReasonCanceled || docs == nil {
		t.Fatalf("docs=%+v status=%+v err=%v", docs, status, err)
	}
}

func unitProvider() *fakeProvider {
	return &fakeProvider{
		info:     embedding.Info{Provider: "fake", Model: "m", Dimensions: 2},
		response: embedding.Response{Vectors: [][]float32{{0.6, 0.8}}, Provider: "fake", Model: "m", Dimensions: 2},
	}
}

func testProfile() embedding.Profile {
	return embedding.Profile{
		Provider: "fake", Model: "m", Dimensions: 2,
		ProjectionVersion: "projection-v1", ChunkerVersion: "chunker-v1",
		Representation: embedding.RepresentationDenseF32, Normalization: embedding.NormalizationL2,
	}
}

func assertSemanticLane(t *testing.T, doc retrieval.EvidenceDocument, rank int, distance float64, profile, backend, generation string) {
	t.Helper()
	if doc.Retrieval == nil || len(doc.Retrieval.Lanes) != 1 {
		t.Fatalf("retrieval=%+v", doc.Retrieval)
	}
	lane := doc.Retrieval.Lanes[0]
	if lane.Name != "semantic" || lane.Status != "used" || lane.Rank != rank || lane.RawDistance == nil || *lane.RawDistance != distance || lane.Profile != profile || lane.Backend != backend || lane.Generation != generation || lane.Reason != "" {
		t.Fatalf("lane=%+v", lane)
	}
}
