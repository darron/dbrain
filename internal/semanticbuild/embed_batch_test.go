package semanticbuild

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/store"
)

type embedBatchStore struct {
	chunks          []store.RetrievalChunkRow
	completed       map[string]bool
	operations      []string
	listLimits      []int
	listTimes       []time.Time
	writeBatches    [][]store.RetrievalEmbeddingRow
	putRevisions    []int64
	purgeEpoch      int64
	profileRevision int64
	profileExists   bool
}

func (s *embedBatchStore) ListChunksNeedingEmbeddingForProfileAt(_ context.Context, _ embedding.Profile, after string, limit int, now time.Time) ([]store.RetrievalChunkRow, error) {
	s.operations = append(s.operations, "list")
	s.listLimits = append(s.listLimits, limit)
	s.listTimes = append(s.listTimes, now)
	result := make([]store.RetrievalChunkRow, 0, min(limit, len(s.chunks)))
	for _, row := range s.chunks {
		if row.ChunkID <= after || s.completed[row.ChunkID] {
			continue
		}
		result = append(result, row)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *embedBatchStore) CountChunksNeedingEmbeddingForProfileAt(_ context.Context, _ embedding.Profile, _ time.Time) (int, error) {
	s.operations = append(s.operations, "count")
	count := 0
	for _, row := range s.chunks {
		if !s.completed[row.ChunkID] {
			count++
		}
	}
	return count, nil
}

func (s *embedBatchStore) RetrievalPurgeEpoch(context.Context) (int64, error) {
	s.operations = append(s.operations, "epoch")
	return s.purgeEpoch, nil
}

func (s *embedBatchStore) PutRetrievalEmbeddingBatch(_ context.Context, input store.PutRetrievalEmbeddingBatchInput) (int64, error) {
	s.operations = append(s.operations, "put")
	rows := append([]store.RetrievalEmbeddingRow(nil), input.Rows...)
	s.writeBatches = append(s.writeBatches, rows)
	if s.completed == nil {
		s.completed = make(map[string]bool)
	}
	for _, row := range rows {
		s.completed[row.ChunkID] = true
	}
	if len(s.putRevisions) >= len(s.writeBatches) {
		return s.putRevisions[len(s.writeBatches)-1], nil
	}
	return int64(len(s.writeBatches)), nil
}

func (s *embedBatchStore) RetrievalEmbeddingProfile(_ context.Context, profileID string) (store.RetrievalEmbeddingProfileRow, error) {
	s.operations = append(s.operations, "profile")
	if !s.profileExists {
		return store.RetrievalEmbeddingProfileRow{}, fmt.Errorf("profile %s: %w", profileID, sql.ErrNoRows)
	}
	return store.RetrievalEmbeddingProfileRow{ProfileID: profileID, LatestRevision: s.profileRevision}, nil
}

type embedBatchProviderOutcome struct {
	response embedding.Response
	err      error
}

type embedBatchProvider struct {
	info       embedding.Info
	outcomes   []embedBatchProviderOutcome
	requests   []embedding.Request
	operations *[]string
}

func (p *embedBatchProvider) Info() embedding.Info {
	return p.info
}

func (p *embedBatchProvider) Embed(_ context.Context, req embedding.Request) (embedding.Response, error) {
	if p.operations != nil {
		*p.operations = append(*p.operations, "provider")
	}
	p.requests = append(p.requests, req)
	if len(p.outcomes) >= len(p.requests) {
		outcome := p.outcomes[len(p.requests)-1]
		return outcome.response, outcome.err
	}
	return embedBatchResponse(p.info, len(req.Texts)), nil
}

func embedBatchResponse(info embedding.Info, count int) embedding.Response {
	vectors := make([][]float32, count)
	for i := range vectors {
		vectors[i] = []float32{0.6, 0.8}
	}
	return embedding.Response{
		Vectors: vectors, Provider: info.Provider, Model: info.Model, Dimensions: info.Dimensions,
	}
}

func embedBatchChunks(count int) []store.RetrievalChunkRow {
	rows := make([]store.RetrievalChunkRow, count)
	for i := range rows {
		rows[i] = store.RetrievalChunkRow{
			ChunkID: fmt.Sprintf("chunk-%05d", i), ChunkTextHash: fmt.Sprintf("hash-%05d", i), Text: fmt.Sprintf("text-%05d", i),
		}
	}
	return rows
}

func newEmbedBatchProvider() *embedBatchProvider {
	return &embedBatchProvider{info: embedding.Info{Provider: "fake", Model: "embed-v1", Dimensions: 2}}
}

func TestEmbedBatchCapsSelectionProviderAndPersistenceAtFiveThousand(t *testing.T) {
	st := &embedBatchStore{chunks: embedBatchChunks(MaxEmbeddingBatchSize + 1), putRevisions: []int64{73}}
	provider := newEmbedBatchProvider()

	result, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{BatchSize: MaxEmbeddingBatchSize})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 1 || len(provider.requests[0].Texts) != MaxEmbeddingBatchSize {
		t.Fatalf("provider requests=%d texts=%d", len(provider.requests), len(provider.requests[0].Texts))
	}
	if len(st.writeBatches) != 1 || len(st.writeBatches[0]) != MaxEmbeddingBatchSize {
		t.Fatalf("write batches=%d rows=%d", len(st.writeBatches), len(st.writeBatches[0]))
	}
	if result.Scanned != MaxEmbeddingBatchSize || result.Generated != MaxEmbeddingBatchSize || result.Revision != 73 || !result.HasMore {
		t.Fatalf("result=%+v", result)
	}
	if !reflect.DeepEqual(st.listLimits, []int{MaxEmbeddingBatchSize, 1}) {
		t.Fatalf("list limits=%v, want bounded selection and has-more probe", st.listLimits)
	}
}

func TestEmbedBatchValidatesOptionsBeforeStoreProviderOrPersistence(t *testing.T) {
	tests := []struct {
		name string
		opts EmbedBatchOptions
	}{
		{name: "zero batch", opts: EmbedBatchOptions{}},
		{name: "oversized batch", opts: EmbedBatchOptions{BatchSize: MaxEmbeddingBatchSize + 1}},
		{name: "negative cooldown", opts: EmbedBatchOptions{BatchSize: 1, RetryCooldown: -time.Second}},
		{name: "negative initial backoff", opts: EmbedBatchOptions{BatchSize: 1, RetryInitialBackoff: -time.Second}},
		{name: "negative max backoff", opts: EmbedBatchOptions{BatchSize: 1, RetryMaxBackoff: -time.Second}},
		{name: "max backoff above safety cap", opts: EmbedBatchOptions{BatchSize: 1, RetryMaxBackoff: DefaultRetryMaxBackoff + time.Nanosecond}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &embedBatchStore{chunks: embedBatchChunks(1)}
			provider := newEmbedBatchProvider()
			if _, err := RunEmbedBatch(t.Context(), st, provider, tc.opts); err == nil {
				t.Fatal("invalid options accepted")
			}
			if len(st.operations) != 0 || len(provider.requests) != 0 || len(st.writeBatches) != 0 {
				t.Fatalf("operations=%v provider=%d writes=%d", st.operations, len(provider.requests), len(st.writeBatches))
			}
		})
	}
}

func TestEmbedBatchRunsPreflightBeforeProviderAndReturnsExactRevision(t *testing.T) {
	operations := make([]string, 0)
	st := &embedBatchStore{chunks: embedBatchChunks(2), putRevisions: []int64{91}}
	provider := newEmbedBatchProvider()
	provider.operations = &operations

	result, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize: 2,
		BeforeProvider: func(_ context.Context, count int) error {
			operations = append(operations, fmt.Sprintf("before:%d", count))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 91 {
		t.Fatalf("revision=%d, want exact persisted revision 91", result.Revision)
	}
	if !reflect.DeepEqual(operations, []string{"before:2", "provider"}) {
		t.Fatalf("provider ordering=%v", operations)
	}
}

func TestEmbedBatchPreflightFailurePreventsProviderAndPersistence(t *testing.T) {
	preflightErr := errors.New("flush headroom unavailable")
	st := &embedBatchStore{chunks: embedBatchChunks(1)}
	provider := newEmbedBatchProvider()

	_, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize:      1,
		BeforeProvider: func(context.Context, int) error { return preflightErr },
	})
	if !errors.Is(err, preflightErr) {
		t.Fatalf("err=%v", err)
	}
	if len(provider.requests) != 0 || len(st.writeBatches) != 0 {
		t.Fatalf("provider requests=%d writes=%d", len(provider.requests), len(st.writeBatches))
	}
}

func TestEmbedBatchNoCandidatesReturnsCurrentProfileRevisionWithFixedNow(t *testing.T) {
	fixed := time.Date(2026, 7, 28, 9, 30, 0, 0, time.FixedZone("local", -6*60*60))
	nowCalls := 0
	st := &embedBatchStore{profileExists: true, profileRevision: 123}
	provider := newEmbedBatchProvider()

	result, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize: 1,
		Now: func() time.Time {
			nowCalls++
			return fixed
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 123 || result.HasMore || result.Scanned != 0 {
		t.Fatalf("result=%+v", result)
	}
	if nowCalls != 1 || len(st.listTimes) != 1 || !st.listTimes[0].Equal(fixed.UTC()) {
		t.Fatalf("now calls=%d list times=%v", nowCalls, st.listTimes)
	}
	if len(provider.requests) != 0 || len(st.writeBatches) != 0 {
		t.Fatalf("provider requests=%d writes=%d", len(provider.requests), len(st.writeBatches))
	}
}

func TestEmbedBatchNoCandidatesReturnsZeroWhenProfileDoesNotExist(t *testing.T) {
	st := &embedBatchStore{}
	result, err := RunEmbedBatch(t.Context(), st, newEmbedBatchProvider(), EmbedBatchOptions{BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 0 || result.HasMore {
		t.Fatalf("result=%+v", result)
	}
}

func TestEmbedBatchRetriesSameTextsInMemoryThenPersistsOnce(t *testing.T) {
	retryable := embedding.RetryableError(errors.New("temporarily unavailable"))
	st := &embedBatchStore{chunks: embedBatchChunks(2), putRevisions: []int64{42}}
	provider := newEmbedBatchProvider()
	provider.outcomes = []embedBatchProviderOutcome{
		{err: retryable},
		{err: retryable},
		{response: embedBatchResponse(provider.info, 2)},
	}
	var sleeps []time.Duration

	result, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize: 2,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("sleeps=%v", sleeps)
	}
	if len(provider.requests) != 3 || !reflect.DeepEqual(provider.requests[0].Texts, provider.requests[1].Texts) || !reflect.DeepEqual(provider.requests[1].Texts, provider.requests[2].Texts) {
		t.Fatalf("requests=%+v", provider.requests)
	}
	if len(st.writeBatches) != 1 || result.Revision != 42 || result.Generated != 2 {
		t.Fatalf("writes=%d result=%+v", len(st.writeBatches), result)
	}
	for _, row := range st.writeBatches[0] {
		if row.AttemptCount != 3 || row.Status != store.RetrievalEmbeddingReady {
			t.Fatalf("row=%+v", row)
		}
	}
}

func TestEmbedBatchThirdRetryableFailurePersistsOnceAndReturnsCommittedRevision(t *testing.T) {
	retryable := embedding.RetryableError(errors.New("provider down"))
	st := &embedBatchStore{chunks: embedBatchChunks(2), putRevisions: []int64{57}}
	provider := newEmbedBatchProvider()
	provider.outcomes = []embedBatchProviderOutcome{{err: retryable}, {err: retryable}, {err: retryable}}
	var sleeps []time.Duration

	result, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize: 2,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	})
	if !errors.Is(err, ErrEmbedCircuitOpen) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, 2 * time.Second}) || len(provider.requests) != MaxConsecutiveProviderFailures {
		t.Fatalf("sleeps=%v requests=%d", sleeps, len(provider.requests))
	}
	if len(st.writeBatches) != 1 || result.Revision != 57 || result.Failed != 2 || result.Scanned != 2 {
		t.Fatalf("writes=%d result=%+v", len(st.writeBatches), result)
	}
	for _, row := range st.writeBatches[0] {
		if row.AttemptCount != 3 || row.Status != store.RetrievalEmbeddingError || row.NextAttemptAt.IsZero() {
			t.Fatalf("row=%+v", row)
		}
	}
}

func TestEmbedBatchBackoffSaturatesAtConfiguredMaximum(t *testing.T) {
	retryable := embedding.RetryableError(errors.New("provider down"))
	st := &embedBatchStore{chunks: embedBatchChunks(1)}
	provider := newEmbedBatchProvider()
	provider.outcomes = []embedBatchProviderOutcome{{err: retryable}, {err: retryable}, {err: retryable}}
	var sleeps []time.Duration

	_, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize:           1,
		RetryInitialBackoff: 20 * time.Second,
		RetryMaxBackoff:     DefaultRetryMaxBackoff,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	})
	if !errors.Is(err, ErrEmbedCircuitOpen) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{20 * time.Second, 30 * time.Second}) {
		t.Fatalf("sleeps=%v", sleeps)
	}
}

func TestEmbedBatchSuccessResetsRetrySequenceForNextCall(t *testing.T) {
	retryable := embedding.RetryableError(errors.New("provider down"))
	st := &embedBatchStore{chunks: embedBatchChunks(2)}
	provider := newEmbedBatchProvider()
	provider.outcomes = []embedBatchProviderOutcome{
		{err: retryable},
		{response: embedBatchResponse(provider.info, 1)},
		{err: retryable},
		{err: retryable},
		{response: embedBatchResponse(provider.info, 1)},
	}
	var sleeps []time.Duration
	opts := EmbedBatchOptions{
		BatchSize: 1,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	}

	first, err := RunEmbedBatch(t.Context(), st, provider, opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunEmbedBatch(t.Context(), st, provider, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generated != 1 || second.Generated != 1 || len(st.writeBatches) != 2 {
		t.Fatalf("first=%+v second=%+v writes=%d", first, second, len(st.writeBatches))
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{time.Second, time.Second, 2 * time.Second}) {
		t.Fatalf("sleeps=%v, second call did not restart at initial delay", sleeps)
	}
}

func TestEmbedBatchTerminalProviderAndResponseErrorsDoNotSleepOrPersist(t *testing.T) {
	info := newEmbedBatchProvider().info
	tests := []struct {
		name    string
		outcome embedBatchProviderOutcome
	}{
		{name: "fatal configuration", outcome: embedBatchProviderOutcome{err: embedding.FatalConfigError(errors.New("bad model"))}},
		{name: "authentication", outcome: embedBatchProviderOutcome{err: errors.New("authentication denied")}},
		{name: "provenance", outcome: embedBatchProviderOutcome{response: func() embedding.Response {
			response := embedBatchResponse(info, 1)
			response.Model = "other-model"
			return response
		}()}},
		{name: "dimension", outcome: embedBatchProviderOutcome{response: embedding.Response{
			Vectors: [][]float32{{1, 0, 0}}, Provider: info.Provider, Model: info.Model, Dimensions: 3,
		}}},
		{name: "cardinality", outcome: embedBatchProviderOutcome{response: embedBatchResponse(info, 2)}},
		{name: "non-finite vector", outcome: embedBatchProviderOutcome{response: embedding.Response{
			Vectors: [][]float32{{float32(math.NaN()), 0}}, Provider: info.Provider, Model: info.Model, Dimensions: info.Dimensions,
		}}},
		{name: "non-normalized vector", outcome: embedBatchProviderOutcome{response: embedding.Response{
			Vectors: [][]float32{{3, 4}}, Provider: info.Provider, Model: info.Model, Dimensions: info.Dimensions,
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &embedBatchStore{chunks: embedBatchChunks(1)}
			provider := newEmbedBatchProvider()
			provider.outcomes = []embedBatchProviderOutcome{tc.outcome}
			sleepCalls := 0
			_, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
				BatchSize: 1,
				Sleep: func(context.Context, time.Duration) error {
					sleepCalls++
					return nil
				},
			})
			if err == nil {
				t.Fatal("terminal provider result accepted")
			}
			if len(provider.requests) != 1 || sleepCalls != 0 || len(st.writeBatches) != 0 {
				t.Fatalf("requests=%d sleeps=%d writes=%d err=%v", len(provider.requests), sleepCalls, len(st.writeBatches), err)
			}
		})
	}
}

func TestEmbedBatchPreservesBlockedBisectionAndTerminalSingletonWrites(t *testing.T) {
	rows := []store.RetrievalChunkRow{
		{ChunkID: "bad", ChunkTextHash: "bad-hash", Text: "oversized", AttemptCount: 3},
		{ChunkID: "good", ChunkTextHash: "good-hash", Text: "ordinary", AttemptCount: 7},
	}
	st := &embedBatchStore{chunks: rows, putRevisions: []int64{81, 82}}
	provider := newEmbedBatchProvider()
	provider.outcomes = []embedBatchProviderOutcome{
		{err: embedding.BlockedError(errors.New("batch too large"))},
		{err: embedding.BlockedError(errors.New("input too large"))},
		{response: embedBatchResponse(provider.info, 1)},
	}

	result, err := RunEmbedBatch(t.Context(), st, provider, EmbedBatchOptions{
		BatchSize: 2,
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("blocked inputs must not sleep")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 3 || len(st.writeBatches) != 2 {
		t.Fatalf("requests=%d writes=%d", len(provider.requests), len(st.writeBatches))
	}
	if got := st.writeBatches[0][0]; got.ChunkID != "bad" || got.Status != store.RetrievalEmbeddingBlocked || got.AttemptCount != 5 {
		t.Fatalf("blocked row=%+v", got)
	}
	if got := st.writeBatches[1][0]; got.ChunkID != "good" || got.Status != store.RetrievalEmbeddingReady || got.AttemptCount != 9 {
		t.Fatalf("ready row=%+v", got)
	}
	if result.Blocked != 1 || result.Generated != 1 || result.Scanned != 2 || result.Revision != 82 {
		t.Fatalf("result=%+v", result)
	}
}

func TestEmbedBatchCancellationDuringBackoffPreventsAnotherProviderCall(t *testing.T) {
	retryable := embedding.RetryableError(errors.New("provider down"))
	st := &embedBatchStore{chunks: embedBatchChunks(1)}
	provider := newEmbedBatchProvider()
	provider.outcomes = []embedBatchProviderOutcome{{err: retryable}}
	ctx, cancel := context.WithCancel(t.Context())

	_, err := RunEmbedBatch(ctx, st, provider, EmbedBatchOptions{
		BatchSize: 1,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(provider.requests) != 1 || len(st.writeBatches) != 0 {
		t.Fatalf("provider requests=%d writes=%d", len(provider.requests), len(st.writeBatches))
	}
}
