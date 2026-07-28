package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

func TestConfiguredSemanticRefreshAdmission(t *testing.T) {
	ready := semanticindex.Capability{
		State:   semanticindex.CapabilitySupportedReady,
		Backend: semanticindex.BackendUSearch,
		Version: semanticindex.USearchVersion,
	}
	tests := []struct {
		name       string
		mode       semanticconfig.Mode
		capability semanticindex.Capability
		wantReason string
		wantCode   string
	}{
		{
			name:       "mode off wins over unsupported capability",
			mode:       semanticconfig.ModeOff,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantReason: "semantic_mode_off",
		},
		{
			name:       "mode off wins over broken capability",
			mode:       semanticconfig.ModeOff,
			capability: semanticindex.Capability{State: semanticindex.CapabilitySupportedBroken, Reason: "unavailable"},
			wantReason: "semantic_mode_off",
		},
		{
			name:       "shadow skips unsupported native backend",
			mode:       semanticconfig.ModeShadow,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantReason: "native_backend_unsupported",
		},
		{
			name:       "on skips unsupported native backend",
			mode:       semanticconfig.ModeOn,
			capability: semanticindex.Capability{State: semanticindex.CapabilityUnsupported},
			wantReason: "native_backend_unsupported",
		},
		{
			name: "broken backend fails before side effects",
			mode: semanticconfig.ModeOn,
			capability: semanticindex.Capability{
				State:   semanticindex.CapabilitySupportedBroken,
				Backend: semanticindex.BackendUSearch,
				Version: semanticindex.USearchVersion,
				Reason:  "load /Users/alice/private/libusearch.dylib failed " + strings.Repeat("x", 600),
			},
			wantCode: semanticrefresh.ErrorBackendBroken,
		},
		{
			name: "ready provenance mismatch fails before side effects",
			mode: semanticconfig.ModeOn,
			capability: semanticindex.Capability{
				State:   semanticindex.CapabilitySupportedReady,
				Backend: semanticindex.BackendUSearch,
				Version: "wrong-version",
			},
			wantCode: semanticrefresh.ErrorBackendBroken,
		},
		{
			name:       "ready reaches the admitted path",
			mode:       semanticconfig.ModeOn,
			capability: ready,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			dbPath := t.TempDir() + "/brain.db"
			deps := semanticRefreshDeps{
				resolve: func(root string) (semanticconfig.Config, error) {
					calls = append(calls, "resolve:"+root)
					return semanticRefreshTestConfig(test.mode), nil
				},
				capability: func() semanticindex.Capability {
					calls = append(calls, "capability")
					return test.capability
				},
				openWritable: func(path string) (*store.Store, error) {
					calls = append(calls, "open:"+path)
					return store.Open(path)
				},
				provider: func(semanticconfig.Config) (embedding.Provider, error) {
					calls = append(calls, "provider")
					return &semanticRefreshTestProvider{info: semanticRefreshTestInfo()}, nil
				},
				nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
					calls = append(calls, "native")
					return &semanticRefreshTestNative{}, nil
				},
				runRefresh: func(
					_ context.Context,
					_ semanticrefresh.RunLedger,
					executor semanticrefresh.StageExecutor,
					request semanticrefresh.Request,
				) (semanticrefresh.Result, error) {
					calls = append(calls, "run")
					if executor == nil {
						t.Fatal("runner received a nil pipeline")
					}
					if request.Capability != test.capability {
						t.Fatalf("request capability=%+v want=%+v", request.Capability, test.capability)
					}
					return semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted}, nil
				},
			}
			cfg := config.Config{
				RootDir:  "/configured/root",
				DBPath:   dbPath,
				CacheDir: t.TempDir(),
			}

			result, err := runConfiguredSemanticRefresh(t.Context(), cfg, deps, nil)
			if result.Capability.State != test.capability.State ||
				result.Capability.Backend != test.capability.Backend ||
				result.Capability.Version != test.capability.Version {
				t.Fatalf("result capability=%+v want state/backend/version from %+v", result.Capability, test.capability)
			}
			if test.wantCode != "" {
				refreshErr := requireSemanticRefreshHelperError(t, err, test.wantCode)
				if refreshErr.Stage != "" {
					t.Fatalf("admission error stage=%q want empty", refreshErr.Stage)
				}
				if len(calls) != 2 {
					t.Fatalf("calls=%v want resolve then capability only", calls)
				}
				encoded, marshalErr := json.Marshal(result)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if strings.Contains(string(encoded), "/Users/alice") ||
					strings.Contains(string(encoded), "libusearch.dylib") {
					t.Fatalf("result leaked capability path: %s", encoded)
				}
				if len(result.Capability.Reason) > 256 ||
					!utf8.ValidString(result.Capability.Reason) {
					t.Fatalf("capability reason is not bounded UTF-8: %q", result.Capability.Reason)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.wantReason != "" {
				if result.Outcome != semanticrefresh.OutcomeSkipped ||
					result.SkipReason != test.wantReason {
					t.Fatalf("result=%+v want skipped reason %q", result, test.wantReason)
				}
				if len(calls) != 2 {
					t.Fatalf("calls=%v want resolve then capability only", calls)
				}
				return
			}
			if result.Outcome != semanticrefresh.OutcomeCompleted {
				t.Fatalf("result=%+v want completed", result)
			}
			wantCalls := []string{
				"resolve:/configured/root",
				"capability",
				"open:" + dbPath,
				"provider",
				"native",
				"run",
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls=%v want=%v", calls, wantCalls)
			}
		})
	}
}

func TestConfiguredSemanticRefreshCapturesStoreStateAndClosesAfterRun(t *testing.T) {
	dbPath := t.TempDir() + "/brain.db"
	var opened *store.Store
	var gotRequest semanticrefresh.Request
	var progressCalled bool
	progress := func(semanticrefresh.Progress) error {
		progressCalled = true
		return nil
	}
	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
		capability: semanticRefreshReadyCapability,
		openWritable: func(path string) (*store.Store, error) {
			var err error
			opened, err = store.Open(path)
			if err != nil {
				return nil, err
			}
			now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
			_, err = opened.UpsertItem(t.Context(), model.Item{
				SourceKey:   "helper:item",
				SourceType:  "test",
				Title:       "helper item",
				Text:        "semantic refresh helper",
				ContentHash: "hash",
				UpdatedAt:   now,
				LastSeenAt:  now,
			})
			return opened, err
		},
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return &semanticRefreshTestProvider{info: semanticRefreshTestInfo()}, nil
		},
		nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
			return &semanticRefreshTestNative{}, nil
		},
		runRefresh: func(
			ctx context.Context,
			ledger semanticrefresh.RunLedger,
			executor semanticrefresh.StageExecutor,
			request semanticrefresh.Request,
		) (semanticrefresh.Result, error) {
			gotRequest = request
			if ctx != t.Context() {
				t.Fatal("runner did not receive caller context")
			}
			if ledger != opened {
				t.Fatalf("ledger=%T want opened store", ledger)
			}
			if executor == nil {
				t.Fatal("runner received nil pipeline")
			}
			epoch, err := opened.RetrievalPurgeEpoch(ctx)
			if err != nil {
				t.Fatal(err)
			}
			watermark, err := opened.ProjectionWorkRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if request.PurgeEpoch != epoch || request.ProjectionWatermark != watermark {
				t.Fatalf("request epoch/watermark=%d/%d want=%d/%d",
					request.PurgeEpoch, request.ProjectionWatermark, epoch, watermark)
			}
			if watermark <= 0 {
				t.Fatalf("watermark=%d want seeded positive work revision", watermark)
			}
			return semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted}, nil
		},
	}
	cfg := config.Config{
		RootDir:  "/configured/root",
		DBPath:   dbPath,
		CacheDir: t.TempDir(),
	}

	result, err := runConfiguredSemanticRefresh(t.Context(), cfg, deps, progress)
	if err != nil {
		t.Fatal(err)
	}
	if result.Capability != semanticRefreshReadyCapability() {
		t.Fatalf("result capability=%+v", result.Capability)
	}
	profileID, err := semanticbuild.Profile(semanticRefreshTestInfo()).ID()
	if err != nil {
		t.Fatal(err)
	}
	if gotRequest.ProfileID != profileID {
		t.Fatalf("profile ID=%q want=%q", gotRequest.ProfileID, profileID)
	}
	if gotRequest.Progress == nil {
		t.Fatal("progress callback was not forwarded")
	}
	if err := gotRequest.Progress(semanticrefresh.Progress{}); err != nil {
		t.Fatal(err)
	}
	if !progressCalled {
		t.Fatal("forwarded progress callback was not callable")
	}
	if gotRequest.Now == nil || gotRequest.Now().IsZero() {
		t.Fatal("runner request clock is missing")
	}
	if _, err := opened.ProjectionWorkRevision(t.Context()); err == nil {
		t.Fatal("opened store remained usable after helper returned; want reliable close")
	}
}

func TestConfiguredSemanticRefreshSetupErrorsAreTyped(t *testing.T) {
	resolveErr := errors.New("invalid semantic config /Users/alice/private/config.yaml")
	openErr := errors.New("open /Users/alice/private/brain.db")
	providerErr := errors.New(`provider body {"embedding":[0.1],"text":"private"}`)
	nativeErr := errors.New("native root /Users/alice/private/cache failed")
	tests := []struct {
		name      string
		failAt    string
		cause     error
		wantCode  string
		wantStage store.SemanticRefreshStage
	}{
		{name: "configuration", failAt: "resolve", cause: resolveErr, wantCode: semanticrefresh.ErrorBackendBroken},
		{name: "writable store", failAt: "open", cause: openErr, wantCode: semanticrefresh.ErrorBackendBroken},
		{name: "provider", failAt: "provider", cause: providerErr, wantCode: semanticrefresh.ErrorEmbedding, wantStage: store.SemanticRefreshEmbedding},
		{name: "native lifecycle", failAt: "native", cause: nativeErr, wantCode: semanticrefresh.ErrorNativeRoot, wantStage: store.SemanticRefreshVerify},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dbPath := t.TempDir() + "/brain.db"
			var calls []string
			deps := semanticRefreshDeps{
				resolve: func(string) (semanticconfig.Config, error) {
					calls = append(calls, "resolve")
					if test.failAt == "resolve" {
						return semanticconfig.Config{}, test.cause
					}
					return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
				},
				capability: func() semanticindex.Capability {
					calls = append(calls, "capability")
					return semanticRefreshReadyCapability()
				},
				openWritable: func(path string) (*store.Store, error) {
					calls = append(calls, "open")
					if test.failAt == "open" {
						return nil, test.cause
					}
					return store.Open(path)
				},
				provider: func(semanticconfig.Config) (embedding.Provider, error) {
					calls = append(calls, "provider")
					if test.failAt == "provider" {
						return nil, test.cause
					}
					return &semanticRefreshTestProvider{info: semanticRefreshTestInfo()}, nil
				},
				nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
					calls = append(calls, "native")
					if test.failAt == "native" {
						return nil, test.cause
					}
					return &semanticRefreshTestNative{}, nil
				},
				runRefresh: func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error) {
					calls = append(calls, "run")
					return semanticrefresh.Result{}, nil
				},
			}
			result, err := runConfiguredSemanticRefresh(t.Context(), config.Config{
				RootDir:  "/configured/root",
				DBPath:   dbPath,
				CacheDir: t.TempDir(),
			}, deps, nil)
			refreshErr := requireSemanticRefreshHelperError(t, err, test.wantCode)
			if refreshErr.Stage != test.wantStage {
				t.Fatalf("error stage=%q want=%q", refreshErr.Stage, test.wantStage)
			}
			if !errors.Is(err, test.cause) {
				t.Fatalf("error=%v does not unwrap cause %v", err, test.cause)
			}
			if result.Capability != semanticRefreshReadyCapability() {
				t.Fatalf("result capability=%+v want ready capability", result.Capability)
			}
			encoded, marshalErr := json.Marshal(refreshErr)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for _, secret := range []string{"/Users/alice", "0.1", "private"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("public error leaked %q: %s", secret, encoded)
				}
			}
			if calls[len(calls)-1] == "run" {
				t.Fatalf("runner called after %s failure: %v", test.failAt, calls)
			}
		})
	}
}

func TestConfiguredSemanticRefreshCancellationAlwaysReturnsTypedError(t *testing.T) {
	t.Run("cancelled mode off does not become a successful skip", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var sideEffects int
		result, err := runConfiguredSemanticRefresh(ctx, config.Config{}, semanticRefreshDeps{
			resolve: func(string) (semanticconfig.Config, error) {
				return semanticRefreshTestConfig(semanticconfig.ModeOff), nil
			},
			capability: semanticRefreshReadyCapability,
			openWritable: func(string) (*store.Store, error) {
				sideEffects++
				return nil, errors.New("unexpected open")
			},
			provider: func(semanticconfig.Config) (embedding.Provider, error) {
				sideEffects++
				return nil, errors.New("unexpected provider")
			},
			nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
				sideEffects++
				return nil, errors.New("unexpected native")
			},
			runRefresh: func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error) {
				sideEffects++
				return semanticrefresh.Result{}, nil
			},
		}, nil)
		_ = requireSemanticRefreshHelperError(t, err, semanticrefresh.ErrorCancelled)
		if result.Outcome == semanticrefresh.OutcomeSkipped {
			t.Fatalf("cancelled result became successful skip: %+v", result)
		}
		if sideEffects != 0 {
			t.Fatalf("side effects=%d want zero", sideEffects)
		}
	})

	t.Run("runner cannot convert cancellation to success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		dbPath := t.TempDir() + "/brain.db"
		result, err := runConfiguredSemanticRefresh(ctx, config.Config{
			DBPath:   dbPath,
			CacheDir: t.TempDir(),
		}, semanticRefreshDeps{
			resolve: func(string) (semanticconfig.Config, error) {
				return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
			},
			capability:   semanticRefreshReadyCapability,
			openWritable: store.Open,
			provider: func(semanticconfig.Config) (embedding.Provider, error) {
				return &semanticRefreshTestProvider{info: semanticRefreshTestInfo()}, nil
			},
			nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
				return &semanticRefreshTestNative{}, nil
			},
			runRefresh: func(context.Context, semanticrefresh.RunLedger, semanticrefresh.StageExecutor, semanticrefresh.Request) (semanticrefresh.Result, error) {
				cancel()
				return semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted}, nil
			},
		}, nil)
		_ = requireSemanticRefreshHelperError(t, err, semanticrefresh.ErrorCancelled)
		if result.Outcome == semanticrefresh.OutcomeCompleted {
			t.Fatalf("cancelled result remained completed: %+v", result)
		}
	})
}

func TestConfiguredSemanticRefreshProfileChangeUsesLedgerSupersession(t *testing.T) {
	dbPath := t.TempDir() + "/brain.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	old, _, err := st.StartOrResumeSemanticRefreshRun(t.Context(), store.StartSemanticRefreshRunInput{
		RunID:               "old-run",
		ProfileID:           "old-profile",
		PurgeEpoch:          0,
		ProjectionWatermark: 0,
		Now:                 now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	deps := semanticRefreshDeps{
		resolve: func(string) (semanticconfig.Config, error) {
			return semanticRefreshTestConfig(semanticconfig.ModeOn), nil
		},
		capability:   semanticRefreshReadyCapability,
		openWritable: store.Open,
		provider: func(semanticconfig.Config) (embedding.Provider, error) {
			return &semanticRefreshTestProvider{info: semanticRefreshTestInfo()}, nil
		},
		nativeLifecycle: func(semanticconfig.Config) (semanticrefresh.NativeLifecycle, error) {
			return &semanticRefreshTestNative{}, nil
		},
		runRefresh: func(
			ctx context.Context,
			ledger semanticrefresh.RunLedger,
			_ semanticrefresh.StageExecutor,
			request semanticrefresh.Request,
		) (semanticrefresh.Result, error) {
			request.Now = func() time.Time { return now.Add(time.Minute) }
			request.NewRunIDFunc = func() (string, error) {
				return "0123456789abcdef0123456789abcdef", nil
			}
			return semanticrefresh.Run(ctx, ledger, semanticRefreshCompletingExecutor{}, request)
		},
	}
	result, err := runConfiguredSemanticRefresh(t.Context(), config.Config{
		DBPath:   dbPath,
		CacheDir: t.TempDir(),
	}, deps, nil)
	if err != nil {
		t.Fatalf("run configured refresh: %v (cause: %v)", err, errors.Unwrap(err))
	}
	if result.Run == nil || result.Run.ProfileID == old.ProfileID {
		t.Fatalf("result run=%+v want newly selected profile", result.Run)
	}

	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	storedOld, err := st.LatestSemanticRefreshRun(t.Context(), old.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOld == nil || storedOld.State != store.SemanticRefreshRunSuperseded {
		t.Fatalf("old run=%+v want ledger-transaction superseded", storedOld)
	}
}

func TestSemanticRefreshCapabilityNativeConstructorBoundary(t *testing.T) {
	lifecycle, err := newSemanticNativeLifecycle(semanticRefreshTestConfig(semanticconfig.ModeOn))
	switch capability := semanticindex.RuntimeCapability(); capability.State {
	case semanticindex.CapabilityUnsupported:
		if err == nil || lifecycle != nil {
			t.Fatalf("default native lifecycle=(%T, %v), want defensive unavailable error", lifecycle, err)
		}
	case semanticindex.CapabilitySupportedReady:
		if err != nil || lifecycle == nil {
			t.Fatalf("tagged native lifecycle=(%T, %v), want available", lifecycle, err)
		}
	case semanticindex.CapabilitySupportedBroken:
		t.Fatalf("tagged native runtime is broken: %+v", capability)
	default:
		t.Fatalf("unknown native capability: %+v", capability)
	}
}

func TestNativeLifecycleBuildsAndVerifiesActualUSearchRoot(t *testing.T) {
	capability := semanticindex.RuntimeCapability()
	if capability.State == semanticindex.CapabilityUnsupported {
		t.Skip("requires usearch and cgo build tags")
	}
	if capability.State != semanticindex.CapabilitySupportedReady {
		t.Fatalf("native capability=%+v want supported_ready", capability)
	}
	lifecycle, err := newSemanticNativeLifecycle(semanticRefreshTestConfig(semanticconfig.ModeOn))
	if err != nil {
		t.Fatal(err)
	}
	vectorBytes := embedding.EncodeDenseF32([]float32{1, 0})
	vectorSHA := sha256.Sum256(vectorBytes)
	payload, err := lifecycle.Build(t.Context(), []store.RetrievalEmbeddingRow{{
		ChunkID:     "chunk-1",
		Dimensions:  2,
		Revision:    7,
		VectorHash:  hex.EncodeToString(vectorSHA[:]),
		VectorBytes: vectorBytes,
	}})
	if err != nil {
		t.Fatalf("build actual usearch payload: %v", err)
	}
	var encoded bytes.Buffer
	if err := payload(&encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.Len() == 0 {
		t.Fatal("actual usearch builder returned an empty payload")
	}

	cacheDir := t.TempDir()
	segment, err := semanticsegment.PublishSegment(cacheDir, semanticsegment.SegmentInput{
		DatabaseID:     "database-1",
		ProfileID:      "profile-1",
		Backend:        semanticindex.BackendUSearch,
		BackendVersion: semanticindex.USearchVersion,
		DistanceMetric: "cosine",
		Dimensions:     2,
		Members: []semanticsegment.Member{{
			Ordinal:    0,
			ChunkID:    "chunk-1",
			Revision:   7,
			VectorHash: hex.EncodeToString(vectorSHA[:]),
		}},
		Payload: func(writer io.Writer) error {
			_, err := writer.Write(encoded.Bytes())
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := semanticsegment.PublishRoot(cacheDir, semanticsegment.RootInput{
		DatabaseID:       "database-1",
		ProfileID:        "profile-1",
		GenerationID:     "generation-1",
		SnapshotRevision: 7,
		PurgeEpoch:       3,
		Segments: []semanticsegment.RootSegment{{
			Hash:         segment.Hash,
			RelativePath: segment.RelativePath,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	expect := semanticrefresh.RootExpectation{
		CacheDir:         cacheDir,
		DatabaseID:       "database-1",
		ProfileID:        "profile-1",
		GenerationID:     "generation-1",
		DescriptorSHA256: root.Manifest.DescriptorSHA256,
		SnapshotRevision: 7,
		PurgeEpoch:       3,
		Dimensions:       2,
		BackendVersion:   semanticindex.USearchVersion,
	}
	if err := lifecycle.VerifyRoot(t.Context(), expect); err != nil {
		t.Fatalf("verify actual usearch root: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*semanticrefresh.RootExpectation)
	}{
		{name: "cache", mutate: func(value *semanticrefresh.RootExpectation) { value.CacheDir = t.TempDir() }},
		{name: "database", mutate: func(value *semanticrefresh.RootExpectation) { value.DatabaseID = "database-2" }},
		{name: "profile", mutate: func(value *semanticrefresh.RootExpectation) { value.ProfileID = "profile-2" }},
		{name: "generation", mutate: func(value *semanticrefresh.RootExpectation) { value.GenerationID = "generation-2" }},
		{name: "dimensions", mutate: func(value *semanticrefresh.RootExpectation) { value.Dimensions = 3 }},
		{name: "snapshot revision", mutate: func(value *semanticrefresh.RootExpectation) { value.SnapshotRevision++ }},
		{name: "purge epoch", mutate: func(value *semanticrefresh.RootExpectation) { value.PurgeEpoch++ }},
		{name: "backend version", mutate: func(value *semanticrefresh.RootExpectation) { value.BackendVersion = "wrong" }},
		{name: "descriptor hash", mutate: func(value *semanticrefresh.RootExpectation) { value.DescriptorSHA256 = strings.Repeat("0", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := expect
			test.mutate(&changed)
			if err := lifecycle.VerifyRoot(t.Context(), changed); err == nil {
				t.Fatalf("VerifyRoot accepted mismatched %s", test.name)
			}
		})
	}
}

type semanticRefreshTestProvider struct {
	info embedding.Info
}

func (p *semanticRefreshTestProvider) Info() embedding.Info {
	return p.info
}

func (p *semanticRefreshTestProvider) Embed(context.Context, embedding.Request) (embedding.Response, error) {
	return embedding.Response{
		Provider:   p.info.Provider,
		Model:      p.info.Model,
		Dimensions: p.info.Dimensions,
	}, nil
}

type semanticRefreshTestNative struct{}

func (*semanticRefreshTestNative) Build(context.Context, []store.RetrievalEmbeddingRow) (func(io.Writer) error, error) {
	return func(writer io.Writer) error {
		_, err := writer.Write([]byte("test-payload"))
		return err
	}, nil
}

func (*semanticRefreshTestNative) Begin(_ context.Context, expected int) (semanticbuild.StreamingSegmentPayloadSession, error) {
	return &semanticRefreshTestSession{expected: expected}, nil
}

func (*semanticRefreshTestNative) VerifyRoot(context.Context, semanticrefresh.RootExpectation) error {
	return nil
}

type semanticRefreshTestSession struct {
	expected int
	added    int
}

func (s *semanticRefreshTestSession) Add(context.Context, store.RetrievalEmbeddingRow) error {
	s.added++
	return nil
}

func (s *semanticRefreshTestSession) Finish(context.Context) (func(io.Writer) error, error) {
	if s.added != s.expected {
		return nil, errors.New("unexpected test session member count")
	}
	return func(writer io.Writer) error {
		_, err := writer.Write([]byte("test-payload"))
		return err
	}, nil
}

func (*semanticRefreshTestSession) Close() error {
	return nil
}

type semanticRefreshCompletingExecutor struct{}

func (semanticRefreshCompletingExecutor) Execute(context.Context, store.SemanticRefreshRun) (semanticrefresh.StageOutcome, error) {
	return semanticrefresh.StageOutcome{
		NextStage:  store.SemanticRefreshReadiness,
		Checkpoint: "readiness:state=ready",
		Readiness:  "ready",
		Complete:   true,
	}, nil
}

func semanticRefreshTestConfig(mode semanticconfig.Mode) semanticconfig.Config {
	return semanticconfig.Config{
		Mode:                   mode,
		Provider:               semanticconfig.ProviderOllama,
		Model:                  "test-embedding-v1",
		Dimensions:             2,
		IndexBackend:           semanticconfig.IndexBackendExact,
		CandidateDepth:         50,
		ExactFallbackMaxChunks: 25_000,
		OllamaBaseURL:          "http://127.0.0.1:11434",
	}
}

func semanticRefreshTestInfo() embedding.Info {
	return embedding.Info{
		Provider:   string(semanticconfig.ProviderOllama),
		Model:      "test-embedding-v1",
		Dimensions: 2,
	}
}

func semanticRefreshReadyCapability() semanticindex.Capability {
	return semanticindex.Capability{
		State:   semanticindex.CapabilitySupportedReady,
		Backend: semanticindex.BackendUSearch,
		Version: semanticindex.USearchVersion,
	}
}

func requireSemanticRefreshHelperError(t *testing.T, err error, code string) *semanticrefresh.RefreshError {
	t.Helper()
	if err == nil {
		t.Fatalf("error=nil want code %s", code)
	}
	var refreshErr *semanticrefresh.RefreshError
	if !errors.As(err, &refreshErr) {
		t.Fatalf("error=%T %v want *semanticrefresh.RefreshError", err, err)
	}
	if refreshErr.Code != code {
		t.Fatalf("error code=%q want=%q", refreshErr.Code, code)
	}
	return refreshErr
}
