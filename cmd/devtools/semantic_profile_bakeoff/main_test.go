package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
)

type fakeCorpus struct {
	parents   []retrievalchunk.Parent
	onFirst   func()
	firstSeen bool
}

func (f *fakeCorpus) ListRetrievalParents(_ context.Context, after string, limit int) ([]retrievalchunk.Parent, error) {
	if !f.firstSeen {
		f.firstSeen = true
		if f.onFirst != nil {
			f.onFirst()
		}
	}
	result := make([]retrievalchunk.Parent, 0, limit)
	for _, parent := range f.parents {
		if parent.SourceKey > after {
			result = append(result, parent)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

type fakeProvider struct {
	info  embedding.Info
	embed func(context.Context, embedding.Request) (embedding.Response, error)
}

func (f fakeProvider) Info() embedding.Info { return f.info }
func (f fakeProvider) Embed(ctx context.Context, req embedding.Request) (embedding.Response, error) {
	return f.embed(ctx, req)
}

func TestBakeoffWritesInitialReportBeforeProjectionAndActiveProgressAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	parents := syntheticParents(130)
	corpus := &fakeCorpus{parents: parents}
	corpus.onFirst = func() {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("initial report did not exist before projection: %v", err)
		}
		var initial report
		if err := json.Unmarshal(data, &initial); err != nil || initial.Parents != 0 {
			t.Fatalf("initial report = %+v err=%v", initial, err)
		}
	}

	var snapshots []report
	write := func(path string, value report) error {
		snapshots = append(snapshots, cloneReport(t, value))
		return writeReport(path, value)
	}
	var batchSizes []int
	deps := bakeoffDeps{
		newProvider: func(_ embedding.OllamaOptions) (embedding.Provider, error) {
			return normalizedProvider(2, func(size int) { batchSizes = append(batchSizes, size) }), nil
		},
		writeReport: write,
	}
	if err := executeBakeoff(t.Context(), bakeoffOptions{database: "restored.db", reportPath: path, model: "fake", dimensions: []int{2}, maxBytes: retrievalchunk.MaxUTF8Bytes}, corpus, deps); err != nil {
		t.Fatal(err)
	}
	if len(batchSizes) != 3 || batchSizes[0] != 64 || batchSizes[1] != 64 || batchSizes[2] != 2 {
		t.Fatalf("provider batch sizes = %v", batchSizes)
	}
	var sawActive, sawProgress bool
	for _, snapshot := range snapshots {
		if len(snapshot.CandidateProfiles) == 1 && snapshot.CandidateProfiles[0].Status == "running" {
			sawActive = true
			if snapshot.CandidateProfiles[0].Vectors == 64 {
				sawProgress = true
			}
		}
	}
	if !sawActive || !sawProgress {
		t.Fatalf("active/progress snapshots missing: %+v", snapshots)
	}
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("successful atomic report left temporary artifact: %v", err)
	}
}

func TestBakeoffPersistsPeriodicProjectionProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	var projected []int
	deps := bakeoffDeps{writeReport: func(path string, value report) error {
		projected = append(projected, value.Parents)
		return writeReport(path, value)
	}}
	if err := executeBakeoff(t.Context(), bakeoffOptions{database: "restored.db", reportPath: path, model: "fake", maxBytes: retrievalchunk.MaxUTF8Bytes}, &fakeCorpus{parents: syntheticParents(501)}, deps); err != nil {
		t.Fatal(err)
	}
	foundPage := false
	for _, parents := range projected {
		if parents == 500 {
			foundPage = true
		}
	}
	if !foundPage {
		t.Fatalf("projection progress did not persist after first page: %v", projected)
	}
}

func TestBakeoffPersistsCancellationAndProviderFailureTerminalState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		cancel bool
	}{
		{name: "cancellation", cancel: true},
		{name: "provider failure", err: embedding.RetryableError(errors.New("provider unavailable"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			provider := fakeProvider{info: embedding.Info{Provider: "fake", Model: "fake", Dimensions: 2}, embed: func(ctx context.Context, req embedding.Request) (embedding.Response, error) {
				calls++
				if calls == 1 {
					if tc.cancel {
						cancel()
					}
					return normalizedResponse(2, len(req.Texts)), nil
				}
				if tc.cancel {
					return embedding.Response{}, ctx.Err()
				}
				return embedding.Response{}, tc.err
			}}
			deps := bakeoffDeps{newProvider: func(embedding.OllamaOptions) (embedding.Provider, error) { return provider, nil }, writeReport: writeReport}
			err := executeBakeoff(ctx, bakeoffOptions{database: "restored.db", reportPath: path, model: "fake", dimensions: []int{2}, maxBytes: retrievalchunk.MaxUTF8Bytes}, &fakeCorpus{parents: syntheticParents(70)}, deps)
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error = %v", err)
			}
			got := readReport(t, path)
			if len(got.CandidateProfiles) != 1 || got.CandidateProfiles[0].Status != "failed" || got.CandidateProfiles[0].Vectors != 64 || got.CandidateProfiles[0].Error == "" {
				t.Fatalf("terminal report = %+v", got)
			}
		})
	}
}

func TestBakeoffEnforcesProviderCardinalityAndDimensions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response embedding.Response
	}{
		{name: "cardinality", response: normalizedResponse(2, 1)},
		{name: "dimensions", response: normalizedResponse(3, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			provider := fakeProvider{info: embedding.Info{Provider: "fake", Model: "fake", Dimensions: 2}, embed: func(context.Context, embedding.Request) (embedding.Response, error) { return tc.response, nil }}
			deps := bakeoffDeps{newProvider: func(embedding.OllamaOptions) (embedding.Provider, error) { return provider, nil }, writeReport: writeReport}
			_ = executeBakeoff(t.Context(), bakeoffOptions{database: "restored.db", reportPath: path, model: "fake", dimensions: []int{2}, maxBytes: retrievalchunk.MaxUTF8Bytes}, &fakeCorpus{parents: syntheticParents(2)}, deps)
			if got := readReport(t, path).CandidateProfiles[0]; got.Status != "failed" || got.Error == "" {
				t.Fatalf("candidate = %+v", got)
			}
		})
	}
}

func TestWriteReportRemovesTemporaryFileOnEveryFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		ops  reportFileOps
	}{
		{name: "write", ops: reportFileOps{
			writeFile: func(path string, data []byte, mode os.FileMode) error {
				_ = os.WriteFile(path, data, mode)
				return errors.New("write failed")
			},
			rename: os.Rename, remove: os.Remove,
		}},
		{name: "rename", ops: reportFileOps{writeFile: os.WriteFile, rename: func(string, string) error { return errors.New("rename failed") }, remove: os.Remove}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			if err := writeReportWithOps(path, report{}, tc.ops); err == nil {
				t.Fatal("expected write failure")
			}
			if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
				t.Fatalf("temporary artifact remains: %v", err)
			}
		})
	}
}

func TestBakeoffClassifiesDimensionAndContextFailuresNarrowly(t *testing.T) {
	for _, tc := range []struct {
		name            string
		dimensions      int
		status          int
		body            string
		wantStatus      string
		wantContextFail int
	}{
		{name: "384 unsupported", dimensions: 384, status: http.StatusBadRequest, body: `requested dimensions 384 are not supported`, wantStatus: "unsupported"},
		{name: "ordinary fatal HTTP", dimensions: 384, status: http.StatusBadRequest, body: `model configuration is invalid`, wantStatus: "failed"},
		{name: "blocked context limit", dimensions: 384, status: http.StatusBadRequest, body: `input exceeds the context length`, wantStatus: "failed", wantContextFail: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, tc.body, tc.status) }))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "report.json")
			deps := bakeoffDeps{newProvider: func(opts embedding.OllamaOptions) (embedding.Provider, error) { return embedding.NewOllama(opts) }, writeReport: writeReport}
			_ = executeBakeoff(t.Context(), bakeoffOptions{database: "restored.db", reportPath: path, baseURL: server.URL, model: "fake", dimensions: []int{tc.dimensions}, maxBytes: retrievalchunk.MaxUTF8Bytes}, &fakeCorpus{parents: syntheticParents(1)}, deps)
			got := readReport(t, path)
			if got.CandidateProfiles[0].Status != tc.wantStatus || got.ContextFailures != tc.wantContextFail {
				t.Fatalf("report = %+v", got)
			}
		})
	}
}

func TestBakeoffOrdinaryFatalConstructorErrorIsFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	deps := bakeoffDeps{
		newProvider: func(embedding.OllamaOptions) (embedding.Provider, error) {
			return nil, embedding.FatalConfigError(errors.New("bad endpoint"))
		},
		writeReport: writeReport,
	}
	_ = executeBakeoff(t.Context(), bakeoffOptions{database: "restored.db", reportPath: path, model: "fake", dimensions: []int{384}, maxBytes: retrievalchunk.MaxUTF8Bytes}, &fakeCorpus{parents: syntheticParents(1)}, deps)
	if got := readReport(t, path).CandidateProfiles[0]; got.Status != "failed" {
		t.Fatalf("constructor failure = %+v", got)
	}
}

func TestBakeoffRefusesLiveProductionXDGDatabase(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	live, err := defaultProductionDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(live), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(live); err != nil || !refused {
		t.Fatal("live production XDG database must be refused")
	}
	restored := filepath.Join(t.TempDir(), "restored-brain.db")
	if err := os.WriteFile(restored, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(restored); err != nil || refused {
		t.Fatal("explicit restored corpus must remain allowed")
	}
}

func TestBakeoffProductionRefusalFailsClosedForResolutionErrorAndAlias(t *testing.T) {
	original := loadProductionConfig
	t.Cleanup(func() { loadProductionConfig = original })
	loadProductionConfig = func(string) (config.Config, error) { return config.Config{}, errors.New("config unavailable") }
	if _, err := refusesLiveProductionDB(filepath.Join(t.TempDir(), "restored.db")); err == nil {
		t.Fatal("production resolution error must fail closed")
	}

	root := t.TempDir()
	live := filepath.Join(root, "brain.db")
	if err := os.WriteFile(live, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadProductionConfig = func(string) (config.Config, error) { return config.Config{DBPath: live}, nil }
	alias := filepath.Join(root, "alias.db")
	if err := os.Symlink(live, alias); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(alias); err != nil || !refused {
		t.Fatalf("symlink alias refused=%v err=%v", refused, err)
	}
	hardlink := filepath.Join(root, "hardlink.db")
	if err := os.Link(live, hardlink); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(hardlink); err != nil || !refused {
		t.Fatalf("hardlink alias refused=%v err=%v", refused, err)
	}
	if _, err := refusesLiveProductionDB(filepath.Join(root, "missing-restored.db")); err == nil {
		t.Fatal("candidate resolution error must fail closed")
	}

	missingLive := filepath.Join(root, "configured-but-missing.db")
	loadProductionConfig = func(string) (config.Config, error) { return config.Config{DBPath: missingLive}, nil }
	restored := filepath.Join(root, "existing-restored.db")
	if err := os.WriteFile(restored, []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(restored); err != nil || refused {
		t.Fatalf("missing live plus distinct restored refused=%v err=%v", refused, err)
	}
}

func TestBakeoffProviderChecksFiniteL2VectorsAtEachRequestedDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"model":"fake","embeddings":[[0.6,0.8]]}`))
	}))
	defer server.Close()
	if err := verifyEmbedding(server.URL, "fake", 2, "projection text"); err != nil {
		t.Fatal(err)
	}
	if err := finiteL2([]float32{float32(math.NaN()), 1}); err == nil {
		t.Fatal("NaN vector must fail")
	}
}

func syntheticParents(count int) []retrievalchunk.Parent {
	parents := make([]retrievalchunk.Parent, count)
	for i := range parents {
		parents[i] = retrievalchunk.Parent{
			Kind: "source", SourceKey: fmt.Sprintf("source:%06d", i),
			Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: fmt.Sprintf("semantic evidence %06d", i)}},
		}
	}
	return parents
}

func normalizedProvider(dimensions int, observe func(int)) embedding.Provider {
	return fakeProvider{
		info: embedding.Info{Provider: "fake", Model: "fake", Dimensions: dimensions},
		embed: func(_ context.Context, req embedding.Request) (embedding.Response, error) {
			if len(req.Texts) > 64 {
				return embedding.Response{}, fmt.Errorf("batch exceeded 64: %d", len(req.Texts))
			}
			if observe != nil {
				observe(len(req.Texts))
			}
			return normalizedResponse(dimensions, len(req.Texts)), nil
		},
	}
}

func normalizedResponse(dimensions, count int) embedding.Response {
	vectors := make([][]float32, count)
	for i := range vectors {
		vectors[i] = make([]float32, dimensions)
		vectors[i][0] = 1
	}
	return embedding.Response{Provider: "fake", Model: "fake", Dimensions: dimensions, Vectors: vectors}
}

func cloneReport(t *testing.T, value report) report {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned report
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func readReport(t *testing.T, path string) report {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value report
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
