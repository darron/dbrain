package semanticrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticlock"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/store"
)

func TestRefreshIntegrationResumesCommittedProjectionAndEmbeddingWithoutDuplicateProviderCalls(t *testing.T) {
	st := openRefreshIntegrationStore(t)
	now := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	sensitive := "source text must remain private"
	seedRefreshItem(t, st, "resume-one", sensitive+" one", now)
	seedRefreshItem(t, st, "resume-two", sensitive+" two", now)
	watermark := refreshWorkRevision(t, st)
	epoch := refreshPurgeEpoch(t, st)

	provider := newRefreshProvider("resume-v1")
	pipeline := newRefreshIntegrationPipeline(t, st, provider, now, 1, 1)
	ids := &refreshRunIDSequence{}
	var observed []Progress

	firstContext, cancelFirst := context.WithCancel(t.Context())
	firstRequest := refreshIntegrationRequest(provider.profile(), epoch, watermark, now, ids.next)
	firstRequest.Progress = func(progress Progress) error {
		observed = append(observed, progress)
		if progress.Counters.ProjectedParents == 1 {
			cancelFirst()
		}
		return nil
	}
	first, err := Run(firstContext, st, pipeline, firstRequest)
	_ = assertRefreshIntegrationError(t, err, ErrorCancelled)
	if first.Run == nil || first.Run.State != store.SemanticRefreshRunCancelled ||
		first.Run.Counters.ProjectedParents != 1 {
		t.Fatalf("first result=%+v", first)
	}
	runID := first.Run.RunID

	secondContext, cancelSecond := context.WithCancel(t.Context())
	secondRequest := refreshIntegrationRequest(provider.profile(), epoch, watermark, now, ids.next)
	secondRequest.Progress = func(progress Progress) error {
		observed = append(observed, progress)
		if progress.Counters.EmbeddedChunks == 1 {
			cancelSecond()
		}
		return nil
	}
	second, err := Run(secondContext, st, pipeline, secondRequest)
	_ = assertRefreshIntegrationError(t, err, ErrorCancelled)
	if second.Run == nil || second.Run.RunID != runID ||
		second.Run.Counters.ProjectedParents != 2 ||
		second.Run.Counters.EmbeddedChunks != 1 {
		t.Fatalf("second result=%+v", second)
	}
	if calls, texts := provider.snapshot(); calls != 1 || texts != 1 {
		t.Fatalf("provider calls=%d texts=%d after embedding interruption", calls, texts)
	}

	thirdRequest := refreshIntegrationRequest(provider.profile(), epoch, watermark, now, ids.next)
	thirdRequest.Progress = func(progress Progress) error {
		observed = append(observed, progress)
		return nil
	}
	completed, err := Run(t.Context(), st, pipeline, thirdRequest)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Run == nil || completed.Run.RunID != runID ||
		completed.Run.State != store.SemanticRefreshRunCompleted ||
		completed.Run.Counters.ProjectedParents != 2 ||
		completed.Run.Counters.EmbeddedChunks != 2 {
		t.Fatalf("completed result=%+v", completed)
	}
	if calls, texts := provider.snapshot(); calls != 2 || texts != 2 {
		t.Fatalf("provider calls=%d texts=%d, want each committed chunk exactly once", calls, texts)
	}
	assertRefreshPublicValuesBounded(t, completed, nil, observed, sensitive)
}

func TestRefreshIntegrationProviderCircuitResumesSameFailedRun(t *testing.T) {
	st := openRefreshIntegrationStore(t)
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	sensitive := "private retry source text"
	seedRefreshItem(t, st, "retry", sensitive, now)
	watermark := refreshWorkRevision(t, st)
	epoch := refreshPurgeEpoch(t, st)

	provider := newRefreshProvider("circuit-v1")
	provider.retryableFailures = 3
	currentNow := now
	pipeline := newRefreshIntegrationPipelineWithClock(
		t,
		st,
		provider,
		func() time.Time { return currentNow },
		1,
		1,
	)
	ids := &refreshRunIDSequence{}
	var observed []Progress
	request := refreshIntegrationRequest(provider.profile(), epoch, watermark, now, ids.next)
	request.Progress = func(progress Progress) error {
		observed = append(observed, progress)
		return nil
	}

	failed, err := Run(t.Context(), st, pipeline, request)
	circuitErr := assertRefreshIntegrationError(t, err, ErrorEmbeddingCircuit)
	if failed.Run == nil || failed.Run.State != store.SemanticRefreshRunFailed {
		t.Fatalf("failed result=%+v", failed)
	}
	failedRunID := failed.Run.RunID
	if calls, _ := provider.snapshot(); calls != semanticbuild.MaxConsecutiveProviderFailures {
		t.Fatalf("provider calls=%d", calls)
	}

	currentNow = now.Add(20 * time.Minute)
	resumedRequest := refreshIntegrationRequest(
		provider.profile(),
		epoch,
		watermark,
		currentNow,
		ids.next,
	)
	resumedRequest.Progress = func(progress Progress) error {
		observed = append(observed, progress)
		return nil
	}
	completed, err := Run(t.Context(), st, pipeline, resumedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Run == nil || completed.Run.RunID != failedRunID ||
		completed.Run.State != store.SemanticRefreshRunCompleted ||
		completed.Run.Counters.EmbeddedChunks != 1 {
		t.Fatalf("completed result=%+v", completed)
	}
	if calls, texts := provider.snapshot(); calls != 4 || texts != 4 {
		t.Fatalf("provider calls=%d texts=%d, want three failed attempts plus one resumed success", calls, texts)
	}
	assertRefreshPublicValuesBounded(t, completed, circuitErr, observed, sensitive)
}

func TestRefreshIntegrationHigherWatermarkCompletesOneSuccessor(t *testing.T) {
	st := openRefreshIntegrationStore(t)
	now := time.Date(2026, 7, 28, 16, 0, 0, 0, time.UTC)
	seedRefreshItem(t, st, "successor", "initial private text", now)
	watermark := refreshWorkRevision(t, st)
	epoch := refreshPurgeEpoch(t, st)

	provider := newRefreshProvider("successor-v1")
	pipeline := newRefreshIntegrationPipeline(t, st, provider, now, 1, 1)
	ids := &refreshRunIDSequence{}
	redirtied := false
	runIDs := make(map[string]struct{})
	request := refreshIntegrationRequest(provider.profile(), epoch, watermark, now, ids.next)
	request.Progress = func(progress Progress) error {
		runIDs[progress.RunID] = struct{}{}
		if !redirtied && progress.Counters.ProjectedParents == 1 {
			redirtied = true
			seedRefreshItem(
				t,
				st,
				"successor",
				"updated private text",
				now.Add(time.Minute),
			)
		}
		return nil
	}

	completed, err := Run(t.Context(), st, pipeline, request)
	if err != nil {
		t.Fatal(err)
	}
	if !redirtied || completed.Run == nil ||
		completed.Run.State != store.SemanticRefreshRunCompleted ||
		completed.Run.ProjectionWatermark <= watermark ||
		completed.Run.Counters.SuccessorRuns != 1 ||
		len(runIDs) != 2 {
		t.Fatalf("redirtied=%v run_ids=%v result=%+v", redirtied, runIDs, completed)
	}
	if got := refreshWorkRevision(t, st); completed.Run.ProjectionWatermark != got {
		t.Fatalf("successor watermark=%d latest=%d", completed.Run.ProjectionWatermark, got)
	}
}

func TestRefreshIntegrationDifferentProfileSupersedesResumableRun(t *testing.T) {
	st := openRefreshIntegrationStore(t)
	now := time.Date(2026, 7, 28, 17, 0, 0, 0, time.UTC)
	seedRefreshItem(t, st, "profile-switch", "private profile text", now)
	watermark := refreshWorkRevision(t, st)
	epoch := refreshPurgeEpoch(t, st)
	ids := &refreshRunIDSequence{}

	oldProvider := newRefreshProvider("old-v1")
	oldProfileID, err := oldProvider.profile().ID()
	if err != nil {
		t.Fatal(err)
	}
	old, resumed, err := st.StartOrResumeSemanticRefreshRun(t.Context(), store.StartSemanticRefreshRunInput{
		RunID:               ids.nextMust(t),
		ProfileID:           oldProfileID,
		PurgeEpoch:          epoch,
		ProjectionWatermark: watermark,
		Now:                 now,
	})
	if err != nil || resumed {
		t.Fatalf("old run=%+v resumed=%v err=%v", old, resumed, err)
	}

	newProvider := newRefreshProvider("new-v1")
	newPipeline := newRefreshIntegrationPipeline(t, st, newProvider, now, 1, 1)
	request := refreshIntegrationRequest(
		newProvider.profile(),
		epoch,
		watermark,
		now,
		ids.next,
	)
	completed, err := Run(t.Context(), st, newPipeline, request)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Run == nil || completed.Run.State != store.SemanticRefreshRunCompleted {
		t.Fatalf("new profile result=%+v", completed)
	}
	storedOld, err := st.LatestSemanticRefreshRun(t.Context(), oldProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOld == nil || storedOld.RunID != old.RunID ||
		storedOld.State != store.SemanticRefreshRunSuperseded {
		t.Fatalf("old run after profile switch=%+v", storedOld)
	}
}

func TestRefreshIntegrationResumesAfterActivatedFlushWithoutDuplicateProviderOrFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("large real-store two-flush integration")
	}
	st := openRefreshIntegrationStore(t)
	now := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	// ASCII windows are independently bounded by the 1,800-byte v3 ceiling.
	// This produces just over 10,000 distinct chunks. The third embedding batch
	// must first flush 5,000 rows to preserve hard L0 headroom; the ordinary
	// flush stage then commits the second 5,000-row segment.
	seedRefreshItem(
		t,
		st,
		"flush-resume",
		refreshDistinctFlushText(2*store.RetrievalSegmentTarget),
		now,
	)
	watermark := refreshWorkRevision(t, st)
	epoch := refreshPurgeEpoch(t, st)
	provider := newRefreshProvider("flush-v1")
	native := &refreshNative{verifyFailures: 1}
	cacheDir := t.TempDir()
	executor, err := NewPipeline(st, PipelineOptions{
		Profile:         provider.profile(),
		Provider:        provider,
		Native:          native,
		CacheDir:        cacheDir,
		ExactMaxChunks:  semanticreadiness.DefaultExactMaxChunks,
		ProjectionBatch: 1,
		EmbeddingBatch:  semanticbuild.MaxEmbeddingBatchSize,
		Now:             func() time.Time { return now },
		Sleep:           func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	executor = lockRefreshIntegrationPipeline(t, st, cacheDir, executor)
	ids := &refreshRunIDSequence{}
	request := refreshIntegrationRequest(
		provider.profile(),
		epoch,
		watermark,
		now,
		ids.next,
	)

	failed, err := Run(t.Context(), st, executor, request)
	_ = assertRefreshIntegrationError(t, err, ErrorNativeRoot)
	providerCalls, providerTexts := provider.snapshot()
	wantFlushed := int64(2 * store.RetrievalSegmentTarget)
	wantBuilds := int(wantFlushed) / store.RetrievalSegmentTarget
	if failed.Run == nil ||
		failed.Run.State != store.SemanticRefreshRunFailed ||
		providerTexts <= 2*store.RetrievalSegmentTarget ||
		providerTexts > 3*store.RetrievalSegmentTarget ||
		failed.Run.Counters.EmbeddedChunks != int64(providerTexts) ||
		failed.Run.Counters.FlushedVectors != wantFlushed ||
		failed.Run.CurrentGenerationID == "" {
		t.Fatalf(
			"failed run=%+v provider_calls=%d provider_texts=%d want_flushed=%d",
			failed.Run,
			providerCalls,
			providerTexts,
			wantFlushed,
		)
	}
	failedRunID := failed.Run.RunID
	failedGeneration := failed.Run.CurrentGenerationID
	failedRevision := failed.Run.EmbeddingRevision
	if providerCalls != 3 {
		t.Fatalf("provider calls=%d texts=%d", providerCalls, providerTexts)
	}
	if builds, verifies := native.snapshot(); builds != wantBuilds || verifies != 1 {
		t.Fatalf("native builds=%d verifies=%d", builds, verifies)
	}
	profileID, profileErr := provider.profile().ID()
	if profileErr != nil {
		t.Fatal(profileErr)
	}
	profileRow, profileErr := st.RetrievalEmbeddingProfile(t.Context(), profileID)
	if profileErr != nil {
		t.Fatal(profileErr)
	}
	if profileRow.ActiveGenerationID != failedGeneration ||
		profileRow.ActiveSnapshotRevision != failedRevision ||
		profileRow.ActiveIndexedCount != int(wantFlushed) ||
		profileRow.L0ReadyCount != providerTexts-int(wantFlushed) {
		t.Fatalf("activated two-flush profile=%+v failed_run=%+v", profileRow, failed.Run)
	}

	completed, err := Run(t.Context(), st, executor, request)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Run == nil ||
		completed.Run.RunID != failedRunID ||
		completed.Run.State != store.SemanticRefreshRunCompleted ||
		completed.Run.CurrentGenerationID != failedGeneration ||
		completed.Run.EmbeddingRevision != failedRevision ||
		completed.Run.Counters.FlushedVectors != wantFlushed {
		t.Fatalf("completed result=%+v", completed)
	}
	if calls, texts := provider.snapshot(); calls != providerCalls ||
		texts != providerTexts {
		t.Fatalf("provider duplicated work calls=%d texts=%d", calls, texts)
	}
	if builds, verifies := native.snapshot(); builds != wantBuilds || verifies != 2 {
		t.Fatalf("native builds=%d verifies=%d, want one flush and resumed root proof", builds, verifies)
	}
}

func refreshDistinctFlushText(windows int) string {
	var text strings.Builder
	text.Grow(windows*1_800 + 1)
	padding := strings.Repeat("x", 1_792)
	for index := 0; index < windows; index++ {
		_, _ = fmt.Fprintf(&text, "%08x", index)
		text.WriteString(padding)
	}
	text.WriteByte('z')
	return text.String()
}

func openRefreshIntegrationStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return st
}

func seedRefreshItem(
	t *testing.T,
	st *store.Store,
	key string,
	text string,
	now time.Time,
) {
	t.Helper()
	digest := sha256.Sum256([]byte(text))
	_, err := st.UpsertItem(t.Context(), model.Item{
		SourceKey:    "refresh:" + key,
		SourceType:   "refresh_test",
		ExternalID:   key,
		CanonicalURL: "https://example.test/" + key,
		Title:        "Refresh " + key,
		Text:         text,
		ContentHash:  hex.EncodeToString(digest[:]),
		NotePath:     "items/" + key + ".md",
		RawJSON:      "{}",
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func refreshWorkRevision(t *testing.T, st *store.Store) int64 {
	t.Helper()
	revision, err := st.ProjectionWorkRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func refreshPurgeEpoch(t *testing.T, st *store.Store) int64 {
	t.Helper()
	epoch, err := st.RetrievalPurgeEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}

func newRefreshIntegrationPipeline(
	t *testing.T,
	st *store.Store,
	provider *refreshProvider,
	now time.Time,
	projectionBatch int,
	embeddingBatch int,
) StageExecutor {
	t.Helper()
	return newRefreshIntegrationPipelineWithClock(
		t,
		st,
		provider,
		func() time.Time { return now },
		projectionBatch,
		embeddingBatch,
	)
}

func newRefreshIntegrationPipelineWithClock(
	t *testing.T,
	st *store.Store,
	provider *refreshProvider,
	now func() time.Time,
	projectionBatch int,
	embeddingBatch int,
) StageExecutor {
	t.Helper()
	cacheDir := t.TempDir()
	executor, err := NewPipeline(st, PipelineOptions{
		Profile:         provider.profile(),
		Provider:        provider,
		Native:          &pipelineTestNative{},
		CacheDir:        cacheDir,
		ExactMaxChunks:  semanticreadiness.DefaultExactMaxChunks,
		ProjectionBatch: projectionBatch,
		EmbeddingBatch:  embeddingBatch,
		Now:             now,
		Sleep:           func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return lockRefreshIntegrationPipeline(t, st, cacheDir, executor)
}

func lockRefreshIntegrationPipeline(
	t *testing.T,
	st *store.Store,
	cacheDir string,
	executor StageExecutor,
) StageExecutor {
	t.Helper()
	databaseID, err := st.RetrievalDatabaseID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := semanticlock.NewScope(cacheDir, databaseID)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := NewLockedPipeline(executor, scope)
	if err != nil {
		t.Fatal(err)
	}
	return locked
}

func refreshIntegrationRequest(
	profile embedding.Profile,
	epoch int64,
	watermark int64,
	now time.Time,
	newRunID func() (string, error),
) Request {
	profileID, err := profile.ID()
	if err != nil {
		panic(err)
	}
	return Request{
		ProfileID:           profileID,
		PurgeEpoch:          epoch,
		ProjectionWatermark: watermark,
		Capability: semanticindex.Capability{
			State:   semanticindex.CapabilitySupportedReady,
			Backend: semanticindex.BackendUSearch,
			Version: semanticindex.USearchVersion,
		},
		Now:          func() time.Time { return now },
		NewRunIDFunc: newRunID,
	}
}

func assertRefreshIntegrationError(
	t *testing.T,
	err error,
	code string,
) *RefreshError {
	t.Helper()
	var refreshErr *RefreshError
	if !errors.As(err, &refreshErr) || refreshErr.Code != code {
		t.Fatalf("err=%v, want *RefreshError code %s", err, code)
	}
	return refreshErr
}

func assertRefreshPublicValuesBounded(
	t *testing.T,
	result Result,
	refreshErr *RefreshError,
	progress []Progress,
	forbidden string,
) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Result   Result        `json:"result"`
		Error    *RefreshError `json:"error,omitempty"`
		Progress []Progress    `json:"progress"`
	}{
		Result: result, Error: refreshErr, Progress: progress,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, secret := range []string{
		forbidden,
		"source text",
		"private retry",
		"[0.6,0.8]",
		"0.600000",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("public refresh payload leaked %q: %s", secret, text)
		}
	}
	if result.Run != nil {
		if len(result.Run.Checkpoint) > 256 ||
			len(result.Run.ReadinessState) > 64 ||
			len(result.Run.ErrorCode) > 64 ||
			len(result.Run.ErrorText) > 512 {
			t.Fatalf("unbounded result run=%+v", result.Run)
		}
	}
}

type refreshProvider struct {
	mu                sync.Mutex
	info              embedding.Info
	calls             int
	texts             int
	retryableFailures int
}

func newRefreshProvider(modelName string) *refreshProvider {
	return &refreshProvider{
		info: embedding.Info{
			Provider:   "refresh-test",
			Model:      modelName,
			Dimensions: 2,
		},
	}
}

func (p *refreshProvider) profile() embedding.Profile {
	return semanticbuild.Profile(p.info)
}

func (p *refreshProvider) Info() embedding.Info { return p.info }

func (p *refreshProvider) Embed(
	_ context.Context,
	request embedding.Request,
) (embedding.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.texts += len(request.Texts)
	if p.retryableFailures > 0 {
		p.retryableFailures--
		return embedding.Response{}, embedding.RetryableError(
			errors.New("deterministic provider outage"),
		)
	}
	vectors := make([][]float32, len(request.Texts))
	for index := range vectors {
		vectors[index] = []float32{0.6, 0.8}
	}
	return embedding.Response{
		Vectors:    vectors,
		Provider:   p.info.Provider,
		Model:      p.info.Model,
		Dimensions: p.info.Dimensions,
	}, nil
}

func (p *refreshProvider) snapshot() (calls int, texts int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.texts
}

type refreshRunIDSequence struct {
	mu     sync.Mutex
	nextID uint64
}

func (s *refreshRunIDSequence) next() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return fmt.Sprintf("%032x", s.nextID), nil
}

func (s *refreshRunIDSequence) nextMust(t *testing.T) string {
	t.Helper()
	value, err := s.next()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type refreshNative struct {
	mu             sync.Mutex
	builds         int
	verifies       int
	verifyFailures int
}

func (n *refreshNative) Build(
	_ context.Context,
	rows []store.RetrievalEmbeddingRow,
) (func(io.Writer) error, error) {
	n.mu.Lock()
	n.builds++
	n.mu.Unlock()
	if len(rows) != store.RetrievalSegmentTarget {
		return nil, fmt.Errorf("native build rows=%d", len(rows))
	}
	return func(writer io.Writer) error {
		_, err := io.WriteString(writer, "deterministic-native-payload")
		return err
	}, nil
}

func (*refreshNative) Begin(
	context.Context,
	int,
) (semanticbuild.StreamingSegmentPayloadSession, error) {
	return pipelineTestNativeSession{}, nil
}

func (n *refreshNative) VerifyRoot(
	_ context.Context,
	_ RootExpectation,
) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.verifies++
	if n.verifyFailures > 0 {
		n.verifyFailures--
		return errors.New("deterministic native root interruption")
	}
	return nil
}

func (n *refreshNative) snapshot() (builds int, verifies int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.builds, n.verifies
}
