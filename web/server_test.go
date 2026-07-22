package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/store"
)

type fakeArchiveProxy struct {
	body      []byte
	signedURL string
}

func TestWebResearchTimeoutDefaultsLeaveRoomForExactTagRetrieval(t *testing.T) {
	t.Parallel()

	if defaultWebResearchStageTimeout < 2*time.Minute {
		t.Fatalf("expected web research stage timeout to cover slower exact-tag retrieval, got %s", defaultWebResearchStageTimeout)
	}
	if defaultWebResearchSynthesisTimeout < 2*time.Minute {
		t.Fatalf("expected web research synthesis timeout to allow full local synthesis, got %s", defaultWebResearchSynthesisTimeout)
	}
	if defaultWebResearchRunnerTimeout < defaultWebResearchStageTimeout*2+defaultWebResearchSynthesisTimeout {
		t.Fatalf("expected web research runner timeout to leave room for retrieval, retry, and synthesis; runner=%s stage=%s synthesis=%s", defaultWebResearchRunnerTimeout, defaultWebResearchStageTimeout, defaultWebResearchSynthesisTimeout)
	}
	if defaultResearchTimeout < defaultWebResearchStageTimeout {
		t.Fatalf("expected legacy web research timeout to be at least the runner stage timeout, got legacy=%s stage=%s", defaultResearchTimeout, defaultWebResearchStageTimeout)
	}
}

func TestResearchEndpointsRejectConflictingSemanticOverridesBeforeWork(t *testing.T) {
	cfg, st := openTestStore(t)
	if err := os.WriteFile(cfg.ConfigPath, []byte("research:\n  semantic:\n    mode: malformed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"question":"agent memory","use_semantic":true,"disable_semantic":true}`
	for _, path := range []string{"/api/research", "/api/research/run"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("%s started SSE before rejecting conflict", path)
		}
	}
}

func TestResearchEndpointsPropagateConfiguredShadowAndBooleanOverrides(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	_, sourceKey := seedTestData(t, ctx, cfg, st)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/embed":
			_, _ = w.Write([]byte(`{"model":"test-model","embeddings":[[1,0]]}`))
		case "/api/chat":
			_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Agent memory evidence [` + sourceKey + `]."}}`))
		default:
			t.Fatalf("unexpected provider path %q", r.URL.Path)
		}
	}))
	t.Cleanup(provider.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", provider.URL)
	configYAML := "research:\n  semantic:\n    mode: shadow\n    model: test-model\n    dimensions: 2\n"
	if err := os.WriteFile(cfg.ConfigPath, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	includeTopic := false
	direct := func(useSemantic, disableSemantic bool) brainresearch.Pack {
		body, err := json.Marshal(ResearchRequest{Question: "agent memory", Limit: 4, IncludeTopicBrief: &includeTopic, DisablePlanner: true, UseSemantic: useSemantic, DisableSemantic: disableSemantic})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("direct status=%d body=%s", rec.Code, rec.Body.String())
		}
		var pack brainresearch.Pack
		if err := json.Unmarshal(rec.Body.Bytes(), &pack); err != nil {
			t.Fatal(err)
		}
		return pack
	}
	directShadow, directOff, directOn := direct(false, false), direct(false, true), direct(true, false)
	if directShadow.QueryPlan.SemanticMode != semanticconfig.ModeShadow || directShadow.QueryPlan.ShadowComparison == nil || directOff.QueryPlan.SemanticMode != semanticconfig.ModeOff || directOn.QueryPlan.SemanticMode != semanticconfig.ModeOn || !reflect.DeepEqual(directShadow.Evidence, directOff.Evidence) {
		t.Fatalf("direct mode propagation failed: shadow=%#v off=%#v on=%#v", directShadow.QueryPlan, directOff.QueryPlan, directOn.QueryPlan)
	}

	type runnerDone struct {
		ResearchPack brainresearch.Pack             `json:"research_pack"`
		Synthesis    *brainresearch.SynthesisResult `json:"synthesis"`
	}
	run := func(useSemantic, disableSemantic bool) runnerDone {
		traceEnabled := false
		body, err := json.Marshal(ResearchRunRequest{Question: "agent memory", Limit: 4, IncludeTopicBrief: &includeTopic, DisablePlanner: true, UseSemantic: useSemantic, DisableSemantic: disableSemantic, Model: "ollama/qwen-test", TraceEnabled: &traceEnabled})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research/run", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("runner status=%d body=%s", rec.Code, rec.Body.String())
		}
		events := parseSSEEvents(t, rec.Body.String())
		if len(events["done"]) != 1 {
			t.Fatalf("missing runner done event: %s", rec.Body.String())
		}
		var done runnerDone
		if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
			t.Fatal(err)
		}
		return done
	}
	runnerShadow, runnerOff, runnerOn := run(false, false), run(false, true), run(true, false)
	if runnerShadow.ResearchPack.QueryPlan.SemanticMode != semanticconfig.ModeShadow || runnerShadow.ResearchPack.QueryPlan.ShadowComparison == nil || runnerOff.ResearchPack.QueryPlan.SemanticMode != semanticconfig.ModeOff || runnerOn.ResearchPack.QueryPlan.SemanticMode != semanticconfig.ModeOn || !reflect.DeepEqual(runnerShadow.ResearchPack.Evidence, runnerOff.ResearchPack.Evidence) || !reflect.DeepEqual(runnerShadow.Synthesis, runnerOff.Synthesis) {
		t.Fatalf("runner mode/identity failed: shadow=%#v off=%#v on=%#v", runnerShadow, runnerOff, runnerOn)
	}

	traceResult, err := researchtrace.Write(cfg, researchtrace.ResearchTrace{SchemaVersion: researchtrace.SchemaVersion, RunID: "web-shadow-current", Surface: "web_chat", Question: "agent memory", StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(), Pack: &runnerShadow.ResearchPack, StopReason: "enough_evidence"}, researchtrace.ArtifactContents{}, researchtrace.WriteOptions{Retention: researchtrace.RetentionOptions{KeepAll: true}})
	if err != nil {
		t.Fatal(err)
	}
	compareBody, err := json.Marshal(ResearchTraceCompareRequest{TracePath: traceResult.RelativePath, RunCurrent: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/trace-compare", bytes.NewReader(compareBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trace-current status=%d body=%s", rec.Code, rec.Body.String())
	}
	var currentResponse struct {
		Current struct {
			ResearchPack brainresearch.Pack `json:"research_pack"`
		} `json:"current"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &currentResponse); err != nil {
		t.Fatal(err)
	}
	if currentResponse.Current.ResearchPack.QueryPlan.SemanticMode != semanticconfig.ModeShadow || currentResponse.Current.ResearchPack.QueryPlan.ShadowComparison == nil {
		t.Fatalf("trace-current lost shadow comparison: %s", rec.Body.String())
	}
}

func (f *fakeArchiveProxy) GetObject(_ context.Context, _, _ string, rangeHeader string) (archiveObject, error) {
	if rangeHeader != "" {
		return archiveObject{
			Body:          io.NopCloser(bytes.NewReader(f.body[:4])),
			ContentType:   "video/mp4",
			ContentLength: 4,
			ContentRange:  "bytes 0-3/12",
			ETag:          "etag-1",
			LastModified:  time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC),
		}, nil
	}
	return archiveObject{
		Body:          io.NopCloser(bytes.NewReader(f.body)),
		ContentType:   "video/mp4",
		ContentLength: int64(len(f.body)),
		ETag:          "etag-1",
		LastModified:  time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeArchiveProxy) HeadObject(_ context.Context, _, _ string) (archiveObject, error) {
	return archiveObject{
		ContentType:   "video/mp4",
		ContentLength: int64(len(f.body)),
		ETag:          "etag-1",
		LastModified:  time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeArchiveProxy) PresignGetObject(_ context.Context, _, _ string, ttl time.Duration) (string, time.Time, error) {
	return f.signedURL, time.Date(2026, time.April, 25, 22, 5, 0, 0, time.UTC).Add(ttl), nil
}

func TestNewArchiveProxyResolvesSecretRefsFromRuntimeConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
r2:
  endpoint: https://account.example.r2.cloudflarestorage.com
  region: auto
  access_key_id: env:DBRAIN_TEST_R2_ACCESS_KEY
  secret_access_key: env:DBRAIN_TEST_R2_SECRET_KEY
  session_token: env:DBRAIN_TEST_R2_SESSION_TOKEN
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("DBRAIN_TEST_R2_ACCESS_KEY", "resolved-access")
	t.Setenv("DBRAIN_TEST_R2_SECRET_KEY", "resolved-secret")
	t.Setenv("DBRAIN_TEST_R2_SESSION_TOKEN", "resolved-token")

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	proxy, err := newArchiveProxy(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newArchiveProxy: %v", err)
	}
	s3Proxy, ok := proxy.(*s3ArchiveProxy)
	if !ok {
		t.Fatalf("expected s3 archive proxy, got %T", proxy)
	}
	creds, err := s3Proxy.client.Options().Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve credentials: %v", err)
	}
	if creds.AccessKeyID != "resolved-access" {
		t.Fatalf("access key = %q, want resolved secret ref", creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "resolved-secret" {
		t.Fatalf("secret key = %q, want resolved secret ref", creds.SecretAccessKey)
	}
	if creds.SessionToken != "resolved-token" {
		t.Fatalf("session token = %q, want resolved secret ref", creds.SessionToken)
	}
}

func TestWebHandlerServesBootstrapSearchGetAndResearch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openTestStore(t)

	itemID, sourceKey := seedTestData(t, ctx, cfg, st)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	t.Run("bootstrap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response BootstrapResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode bootstrap: %v", err)
		}
		if response.App.Name != "dbrain" {
			t.Fatalf("expected app name dbrain, got %q", response.App.Name)
		}
		if response.App.Version.Commit == "" || response.App.Version.ReleaseVersion == "" {
			t.Fatalf("expected bootstrap version metadata, got %#v", response.App.Version)
		}
		for _, forbidden := range []string{`"root_dir"`, `"vault_dir"`, `"db_path"`, `"build_settings"`, cfg.RootDir, cfg.VaultDir, cfg.DBPath} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("bootstrap response exposed host-local metadata %q: %s", forbidden, rec.Body.String())
			}
		}
		if len(response.SourceActivity.RecentSuccesses) == 0 {
			t.Fatalf("expected bootstrap source activity successes")
		}
		if len(response.SourceActivity.RecentFailures) == 0 {
			t.Fatalf("expected bootstrap source activity failures")
		}
		if response.SourceActivity.Window == "" {
			t.Fatalf("expected bootstrap source activity window")
		}
		if response.Backlog.SourceSummaryPending != 0 {
			t.Fatalf("expected bootstrap backlog to use model-agnostic summary coverage, got %d pending summaries", response.Backlog.SourceSummaryPending)
		}
	})

	t.Run("source activity", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/stats/source-activity?limit=3", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response store.SourceActivityFeed
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode source activity: %v", err)
		}
		if len(response.RecentSuccesses) == 0 {
			t.Fatalf("expected at least one recent success")
		}
		if len(response.RecentFailures) == 0 {
			t.Fatalf("expected at least one recent failure")
		}
		if len(response.FailureKinds) == 0 {
			t.Fatalf("expected source activity failure kind buckets")
		}
		if len(response.FailureDomains) == 0 {
			t.Fatalf("expected source activity failure domain buckets")
		}
		if len(response.FailureTable) != 2 || response.FailureTableTotal != 2 {
			t.Fatalf("expected source activity failure table, got total=%d rows=%+v", response.FailureTableTotal, response.FailureTable)
		}
		if response.FailureTableSort != "newest" {
			t.Fatalf("expected default failure table sort newest, got %q", response.FailureTableSort)
		}
		if len(response.Trend) == 0 || response.TrendBucket == "" {
			t.Fatalf("expected source activity trend data, got bucket=%q trend=%+v", response.TrendBucket, response.Trend)
		}
	})

	t.Run("scheduler status without provider", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/scheduler/sync-all", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response SchedulerSyncAllResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode scheduler status: %v", err)
		}
		if response.SyncAll.Enabled {
			t.Fatalf("expected disabled scheduler without provider, got %#v", response.SyncAll)
		}
	})

	t.Run("source activity filters", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/stats/source-activity?limit=5&source_type=web&domain=broken.example.com&status=error&failure_kind=connectivity&message=connect&window=24h", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response store.SourceActivityFeed
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode filtered source activity: %v", err)
		}
		if len(response.RecentSuccesses) != 0 {
			t.Fatalf("expected no filtered successes, got %+v", response.RecentSuccesses)
		}
		if len(response.RecentFailures) != 2 {
			t.Fatalf("expected 2 filtered failures, got %+v", response.RecentFailures)
		}
		if response.RecentFailures[0].SourceKey != "src:test-agent-memory-failure" {
			t.Fatalf("unexpected filtered failure: %+v", response.RecentFailures[0])
		}
		if len(response.FailureHotspots) != 1 {
			t.Fatalf("expected 1 failure hotspot, got %+v", response.FailureHotspots)
		}
		if response.FailureHotspots[0].Domain != "broken.example.com" || response.FailureHotspots[0].Count != 2 {
			t.Fatalf("unexpected filtered hotspot: %+v", response.FailureHotspots[0])
		}
		if response.FailureHotspots[0].FailureKind != "connectivity" {
			t.Fatalf("unexpected filtered hotspot failure kind: %+v", response.FailureHotspots[0])
		}
		if len(response.FailureKinds) != 1 || response.FailureKinds[0].Key != "connectivity" || response.FailureKinds[0].Count != 2 {
			t.Fatalf("unexpected filtered failure kind buckets: %+v", response.FailureKinds)
		}
		if len(response.FailureStatuses) != 1 || response.FailureStatuses[0].Key != "error" || response.FailureStatuses[0].Count != 2 {
			t.Fatalf("unexpected filtered failure status buckets: %+v", response.FailureStatuses)
		}
		if len(response.FailureDomains) != 1 || response.FailureDomains[0].Key != "broken.example.com" || response.FailureDomains[0].Count != 2 {
			t.Fatalf("unexpected filtered failure domain buckets: %+v", response.FailureDomains)
		}
		if len(response.FailureTable) != 2 || response.FailureTableTotal != 2 {
			t.Fatalf("unexpected filtered failure table: total=%d rows=%+v", response.FailureTableTotal, response.FailureTable)
		}
	})

	t.Run("source activity failure table paging", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/stats/source-activity?limit=1&source_type=web&domain=broken.example.com&status=error&failure_sort=oldest&failure_offset=1&window=24h", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response store.SourceActivityFeed
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode paged source activity: %v", err)
		}
		if response.FailureTableTotal != 2 || response.FailureTableOffset != 1 || response.FailureTableSort != "oldest" {
			t.Fatalf("unexpected paged failure table metadata: %+v", response)
		}
		if len(response.FailureTable) != 1 || response.FailureTable[0].SourceKey != "src:test-agent-memory-failure" {
			t.Fatalf("unexpected paged failure rows: %+v", response.FailureTable)
		}
	})

	t.Run("search", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/search?q=agent+memory&limit=5", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response SearchResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode search: %v", err)
		}
		if len(response.Results) == 0 {
			t.Fatalf("expected at least one search result")
		}
	})

	t.Run("get item", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup=item:test-agent-memory", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get item: %v", err)
		}
		if response.Kind != "item" {
			t.Fatalf("expected kind item, got %q", response.Kind)
		}
		if response.Item == nil || response.Item.ID != itemID {
			t.Fatalf("expected item %d, got %+v", itemID, response.Item)
		}
		if response.NoteContent == "" {
			t.Fatalf("expected item note content")
		}
		if len(response.LinkedSources) == 0 {
			t.Fatalf("expected linked sources")
		}
		for _, forbidden := range []string{`"raw_json"`, `"x_post_json"`, `"summary_json"`, `"ocr_json"`} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("item detail response exposed raw/internal JSON field %q: %s", forbidden, rec.Body.String())
			}
		}
	})

	t.Run("get item note error hides absolute path", func(t *testing.T) {
		notePath := filepath.Join(cfg.VaultDir, "items", "test-agent-memory.md")
		missingPath := notePath + ".missing"
		if err := os.Rename(notePath, missingPath); err != nil {
			t.Fatalf("hide note fixture: %v", err)
		}
		defer func() {
			if err := os.Rename(missingPath, notePath); err != nil {
				t.Fatalf("restore note fixture: %v", err)
			}
		}()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup=item:test-agent-memory", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get item: %v", err)
		}
		if response.NoteError == "" {
			t.Fatalf("expected note error")
		}
		if strings.Contains(response.NoteError, cfg.VaultDir) || strings.Contains(response.NoteError, notePath) {
			t.Fatalf("note error exposed absolute path: %q", response.NoteError)
		}
		if !strings.Contains(response.NoteError, "items/test-agent-memory.md") {
			t.Fatalf("note error should include relative path, got %q", response.NoteError)
		}
	})

	t.Run("get source", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup="+sourceKey, nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get source: %v", err)
		}
		if response.Kind != "source" {
			t.Fatalf("expected kind source, got %q", response.Kind)
		}
		if response.Source == nil || response.Source.SourceKey != sourceKey {
			t.Fatalf("expected source %q, got %+v", sourceKey, response.Source)
		}
		for _, forbidden := range []string{`"extract_json"`, `"summary_json"`, `"extract_error"`, `"summary_error"`} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("source detail response exposed raw/internal field %q: %s", forbidden, rec.Body.String())
			}
		}
		if len(response.Backlinks) == 0 {
			t.Fatalf("expected backlinks")
		}
		if response.Backlinks[0].UserTags != "agent-memory, retrieval" {
			t.Fatalf("expected backlink tags, got %+v", response.Backlinks[0])
		}
	})

	t.Run("tag source", func(t *testing.T) {
		body := bytes.NewBufferString(`{"lookup":"` + sourceKey + `","tags":"source-memory,example-source"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/tag", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response SourceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode tagged source: %v", err)
		}
		if response.UserTags != "source-memory,example-source" {
			t.Fatalf("expected source tags, got %q", response.UserTags)
		}
		for _, forbidden := range []string{`"extract_json"`, `"summary_json"`, `"extract_error"`, `"summary_error"`} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("tag source response exposed raw/internal field %q: %s", forbidden, rec.Body.String())
			}
		}
	})

	t.Run("research pack", func(t *testing.T) {
		body := bytes.NewBufferString(`{"question":"What do I have on agent memory?","limit":4,"include_related":true,"related_limit":2,"max_chars_per_doc":4000,"disable_planner":true}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode research response: %v", err)
		}
		if response["schema_version"] != "research_pack.v1" {
			t.Fatalf("expected schema version, got %#v", response["schema_version"])
		}
		if response["question"] != "What do I have on agent memory?" {
			t.Fatalf("expected echoed question, got %#v", response["question"])
		}
		plan := response["query_plan"].(map[string]any)
		if plan["max_chars_per_doc"] != float64(4000) {
			t.Fatalf("expected web-requested max chars in query plan, got %#v", plan)
		}
		evidence := response["evidence"].([]any)
		if len(evidence) == 0 {
			t.Fatalf("expected evidence rows, got %#v", response)
		}
		coverage := response["coverage"].(map[string]any)
		if coverage["recall_note"] == "" {
			t.Fatalf("expected recall note in research pack, got %#v", coverage)
		}
		nextSteps := response["next_steps"].([]any)
		if len(nextSteps) == 0 || nextSteps[0].(map[string]any)["action"] == "" {
			t.Fatalf("expected semantic next steps, got %#v", nextSteps)
		}
	})

	t.Run("research invalid body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", bytes.NewBufferString(`{`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("research requires question", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", bytes.NewBufferString(`{"question":" "}`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("ask route removed", func(t *testing.T) {
		body := bytes.NewBufferString(`{"question":"What do I have on agent memory?","limit":4}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/ask", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("save chat transcript", func(t *testing.T) {
		request := ChatTranscriptSaveRequest{
			PinnedEvidenceKeys: []string{sourceKey},
			SelectedLookup:     sourceKey,
			Turns: []ChatTranscriptTurn{
				{
					ID:                "chat:turn-1",
					Question:          "What do I know about Tanka?",
					RetrievalQuestion: "Current question: What do I know about Tanka?",
					Status:            "ready",
					Answer:            "Tanka uses Jsonnet for Kubernetes configuration [" + sourceKey + "].",
					CreatedAt:         "2026-05-02T16:00:00Z",
					Citations: []brainresearch.Citation{
						{SourceKey: sourceKey, Title: "Agent Memory Article", NotePath: "sources/test-agent-memory.md", Kind: "source"},
					},
					ResearchPack: brainresearch.Pack{
						SchemaVersion: brainresearch.SchemaVersion,
						Question:      "Current question: What do I know about Tanka?",
						QueryPlan: brainresearch.QueryPlan{
							TextQuery:  "tanka helm",
							QueryTerms: []string{"tanka", "helm"},
							TagQueries: []string{"tanka"},
						},
						Coverage: brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
						Evidence: []ask.Evidence{
							{
								SourceKey:  sourceKey,
								Kind:       "source",
								Title:      "Agent Memory Article",
								URL:        "https://example.com/agent-memory",
								NotePath:   "sources/test-agent-memory.md",
								Summary:    "Summary about durable retrieval.",
								Excerpt:    "Excerpt about citations and retrieval.",
								SourceType: "web",
							},
						},
					},
				},
			},
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal transcript request: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chat/transcripts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response ChatTranscriptSaveResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode transcript save response: %v", err)
		}
		if response.Turns != 1 || response.Bytes == 0 || response.Path == "" {
			t.Fatalf("unexpected transcript save response: %+v", response)
		}
		transcriptRoot := filepath.Join(cfg.DataDir, "chat-transcripts")
		if filepath.IsAbs(response.Path) {
			t.Fatalf("transcript response path should be relative, got %s", response.Path)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte(cfg.DataDir)) {
			t.Fatalf("transcript response exposed data dir: %s", rec.Body.String())
		}
		transcriptPath := filepath.Clean(filepath.Join(cfg.DataDir, filepath.FromSlash(response.Path)))
		if !strings.HasPrefix(transcriptPath, transcriptRoot+string(os.PathSeparator)) {
			t.Fatalf("transcript response path escapes expected root: %s", response.Path)
		}
		content, err := os.ReadFile(transcriptPath)
		if err != nil {
			t.Fatalf("read transcript: %v", err)
		}
		for _, want := range []string{
			"diagnostic export only",
			"What do I know about Tanka?",
			"Tanka uses Jsonnet",
			sourceKey,
			"Summary about durable retrieval.",
		} {
			if !bytes.Contains(content, []byte(want)) {
				t.Fatalf("expected transcript to contain %q:\n%s", want, string(content))
			}
		}
	})

	t.Run("reject verification failed chat transcript", func(t *testing.T) {
		request := ChatTranscriptSaveRequest{
			Turns: []ChatTranscriptTurn{
				{
					ID:        "chat:turn-failed",
					Question:  "What do I know about Tanka?",
					Status:    "verification_failed",
					Answer:    "Unverified answer.",
					CreatedAt: "2026-05-02T16:00:00Z",
				},
			},
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("marshal transcript request: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chat/transcripts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "verification-failed") {
			t.Fatalf("expected verification failure diagnostic, got %s", rec.Body.String())
		}
	})

	t.Run("add link", func(t *testing.T) {
		body := bytes.NewBufferString(`{"url":"https://example.com/manual?utm_source=test"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/links", body)
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Queued  int `json:"queued"`
			Results []struct {
				CanonicalURL string `json:"canonical_url"`
				SourceType   string `json:"source_type"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode add link response: %v", err)
		}
		if response.Queued != 1 || len(response.Results) != 1 || response.Results[0].CanonicalURL != "https://example.com/manual" || response.Results[0].SourceType != "web" {
			t.Fatalf("unexpected add link response %+v", response)
		}
	})
}

func TestWebHandlerServesSchedulerStatus(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	now := time.Date(2026, time.May, 8, 12, 0, 0, 0, time.UTC)
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{
		SchedulerStatus: func() schedulerstate.SyncAllStatus {
			return schedulerstate.SyncAllStatus{
				Enabled:        true,
				Interval:       "1h",
				RunOnStart:     true,
				Running:        false,
				LastStartedAt:  now.Add(-time.Hour),
				LastFinishedAt: now.Add(-59 * time.Minute),
				LastStatus:     "ok",
				NextRunAt:      now.Add(time.Minute),
			}
		},
	})
	if err != nil {
		t.Fatalf("NewHandlerWithOptions: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/scheduler/sync-all", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response SchedulerSyncAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode scheduler status: %v", err)
	}
	if !response.SyncAll.Enabled || response.SyncAll.Interval != "1h" || response.SyncAll.LastStatus != "ok" {
		t.Fatalf("unexpected scheduler response: %#v", response.SyncAll)
	}
	if response.SyncAll.NextRunAt.IsZero() {
		t.Fatalf("expected next run timestamp")
	}
}

func TestWebHandlerServesFullDiskAccessStatusFromServiceProcess(t *testing.T) {
	cfg, st := openTestStore(t)
	probePath := filepath.Join(t.TempDir(), "NoteStore.sqlite")
	if err := os.WriteFile(probePath, []byte("notes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	handler, err := NewHandlerWithOptions(cfg, st, HandlerOptions{
		FullDiskAccessPath: probePath,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/doctor/full-disk-access", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response FullDiskAccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode full disk access status: %v", err)
	}
	if !response.OK || !response.Readable || response.Path != probePath || response.PID == 0 {
		t.Fatalf("unexpected full disk access response: %#v", response)
	}
}

func TestWebHandlerAddLinkRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	body := bytes.NewBufferString(`{"url":"not a url"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/links", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWebHandlerServesIndexHTML(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	for _, path := range []string{"/", "/admin"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if body := rec.Body.String(); body == "" || !bytes.Contains(rec.Body.Bytes(), []byte("dbrain")) {
				t.Fatalf("expected index html to mention dbrain, got %q", body)
			}
		})
	}
}

func TestResearchSynthesizeStreamsFinalAnswer(t *testing.T) {
	cfg, st := openTestStore(t)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Agent memory requires durable retrieval [src:test]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan: brainresearch.QueryPlan{
			TextQuery:  "agent memory",
			QueryTerms: []string{"agent", "memory"},
			TagQueries: []string{"agent-memory"},
		},
		Coverage: brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{
				SourceKey: "src:test",
				Kind:      "source",
				Title:     "Agent Memory",
				URL:       "https://example.com/agent-memory",
				NotePath:  "sources/test.md",
				Summary:   "Agent memory benefits from durable retrieval and citations.",
			},
		},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:         pack.Question,
		ResearchPack:     pack,
		Model:            "ollama/qwen-test",
		MaxEvidenceChars: 4000,
		TraceSurface:     "web_chat",
		TraceContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion:    "What do I know about agent memory?",
			RetrievalQuestion:   "Current question: What do I know about agent memory?",
			PriorQuestionIDs:    []string{"chat:prior"},
			PinnedEvidenceKeys:  []string{"src:test"},
			MergedPriorEvidence: []string{"src:test"},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", got)
	}
	events := parseSSEEvents(t, rec.Body.String())
	for _, event := range []string{"start", "answer", "citation", "done"} {
		if _, ok := events[event]; !ok {
			t.Fatalf("expected %s event in stream:\n%s", event, rec.Body.String())
		}
	}
	if _, ok := events["token"]; ok {
		t.Fatalf("request/response synthesis must not emit token events: %+v", events)
	}
	var answer struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(events["answer"][0]), &answer); err != nil {
		t.Fatalf("decode answer event: %v", err)
	}
	if answer.Text != "Agent memory requires durable retrieval [src:test]." {
		t.Fatalf("unexpected answer text %q", answer.Text)
	}
	var done struct {
		brainresearch.SynthesisResult
		TracePath string `json:"trace_path"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if done.AnswerStatus != "ok" || done.Model != "ollama/qwen-test" || len(done.Citations) != 1 || done.Citations[0].SourceKey != "src:test" {
		t.Fatalf("unexpected done event: %+v", done)
	}
	if !strings.HasPrefix(done.TracePath, "research-runs/") {
		t.Fatalf("expected relative trace path in done event, got %+v", done)
	}
	traceDir := filepath.Join(cfg.DataDir, filepath.FromSlash(done.TracePath))
	for _, name := range []string{"run.md", "run.json", "synthesis-input.md", ".complete"} {
		if _, err := os.Stat(filepath.Join(traceDir, name)); err != nil {
			t.Fatalf("expected web trace file %s: %v", name, err)
		}
	}
	traceJSON, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{`"surface": "web_chat"`, `"chat_continuity"`, `"synthesis_input_path": "synthesis-input.md"`, `"stop_reason": "enough_evidence"`} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected trace json to contain %q:\n%s", value, string(traceJSON))
		}
	}
	for _, value := range []string{"Agent memory requires durable retrieval [src:test].", `"source_key": "src:test"`} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected synthesized chat trace to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func TestResearchRunStreamsProgressAnswerAndTrace(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	_, sourceKey := seedTestData(t, ctx, cfg, st)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Agent memory requires durable retrieval and citations [` + sourceKey + `]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	sentinel := "PREVIOUS_MODEL_ANSWER_SHOULD_NOT_ENTER_SYNTHESIS_INPUT"
	requestBody, err := json.Marshal(ResearchRunRequest{
		Question:         "Current question: What do I know about agent memory?\n\nRecent user questions:\n- How should retrieval cite sources?",
		Limit:            4,
		DisablePlanner:   true,
		Model:            "ollama/qwen-test",
		MaxEvidenceChars: 4000,
		TraceSurface:     "web_chat",
		TraceContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion:    "What do I know about agent memory?",
			RetrievalQuestion:   "Current question: What do I know about agent memory?",
			PriorQuestionIDs:    []string{"chat:prior-" + sentinel},
			PinnedEvidenceKeys:  []string{sourceKey},
			MergedPriorEvidence: []string{sourceKey},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/run", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", got)
	}
	events := parseSSEEvents(t, rec.Body.String())
	for _, event := range []string{"progress", "answer", "citation", "done"} {
		if _, ok := events[event]; !ok {
			t.Fatalf("expected %s event in stream:\n%s", event, rec.Body.String())
		}
	}
	if _, ok := events["error"]; ok {
		t.Fatalf("successful runner must not emit error: %+v", events)
	}

	progressStages := map[string]bool{}
	for _, raw := range events["progress"] {
		var event struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatalf("decode progress event: %v", err)
		}
		progressStages[event.Stage] = true
	}
	for _, stage := range []string{"planning", "retrieval", "inspection", "judge", "prepare_synthesis", "synthesis", "verification", "trace"} {
		if !progressStages[stage] {
			t.Fatalf("expected progress stage %q, got %+v\nstream:\n%s", stage, progressStages, rec.Body.String())
		}
	}

	var done struct {
		SchemaVersion string                         `json:"schema_version"`
		AnswerStatus  string                         `json:"answer_status"`
		ResearchPack  brainresearch.Pack             `json:"research_pack"`
		Synthesis     *brainresearch.SynthesisResult `json:"synthesis"`
		TracePath     string                         `json:"trace_path"`
		StopReason    string                         `json:"stop_reason"`
		Verification  struct {
			Passed bool `json:"passed"`
		} `json:"verification"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if done.SchemaVersion != "research_run.v1" || done.AnswerStatus != "ok" || done.StopReason != "enough_evidence" || !done.Verification.Passed {
		t.Fatalf("unexpected done event: %+v", done)
	}
	if !evidenceContainsSourceKey(done.ResearchPack.Evidence, sourceKey) {
		t.Fatalf("expected final pack to include seeded evidence, got %+v", done.ResearchPack.Evidence)
	}
	if done.Synthesis == nil || done.Synthesis.Answer == "" || !citationsContainSourceKey(done.Synthesis.Citations, sourceKey) {
		t.Fatalf("unexpected synthesis payload: %+v", done.Synthesis)
	}
	if !strings.HasPrefix(done.TracePath, "research-runs/") {
		t.Fatalf("expected relative trace path in done event, got %+v", done)
	}
	traceDir := filepath.Join(cfg.DataDir, filepath.FromSlash(done.TracePath))
	for _, name := range []string{"run.md", "run.json", "synthesis-input.md", ".complete"} {
		if _, err := os.Stat(filepath.Join(traceDir, name)); err != nil {
			t.Fatalf("expected runner trace file %s: %v", name, err)
		}
	}
	synthesisInput, err := os.ReadFile(filepath.Join(traceDir, "synthesis-input.md"))
	if err != nil {
		t.Fatalf("read synthesis input: %v", err)
	}
	if !bytes.Contains(synthesisInput, []byte("## Question\nWhat do I know about agent memory?")) {
		t.Fatalf("expected synthesis input to use original chat question:\n%s", string(synthesisInput))
	}
	if bytes.Contains(synthesisInput, []byte(sentinel)) {
		t.Fatalf("previous model answer sentinel leaked into synthesis input:\n%s", string(synthesisInput))
	}
	traceJSON, err := os.ReadFile(filepath.Join(traceDir, "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{`"surface": "web_chat"`, `"schema_version": "research_trace.v1"`, `"schema_version": "research_pack.v1"`, `"stop_reason": "enough_evidence"`, `"synthesis_input_path": "synthesis-input.md"`} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected trace json to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func TestResearchRunUsesRawCurrentQuestionForProtectedAnchors(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	sourceKey := seedWebXAuthorItem(t, ctx, cfg, st, "x:other-author", "Other_Author", "Other Author", "Other Author essays")
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Other Author evidence [` + sourceKey + `]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rawQuestion := "Can you synthesize @Other_Author?"
	requestBody, err := json.Marshal(ResearchRunRequest{
		Question:       "Current question: " + rawQuestion + "\n\nPrior evidence titles for query focus:\n- @Kristof_Poland old row",
		Limit:          4,
		DisablePlanner: true,
		Model:          "ollama/qwen-test",
		TraceSurface:   "web_chat",
		TraceContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion:  rawQuestion,
			RetrievalQuestion: "Current question: " + rawQuestion,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/run", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	var done struct {
		ResearchPack brainresearch.Pack `json:"research_pack"`
		TracePath    string             `json:"trace_path"`
		Verification struct {
			Passed bool `json:"passed"`
		} `json:"verification"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if !done.Verification.Passed {
		t.Fatalf("expected verified result, stream:\n%s", rec.Body.String())
	}
	if !protectedAnchorsContainCanonical(done.ResearchPack.QueryPlan.ProtectedAnchors, "other_author") {
		t.Fatalf("expected current raw @Other_Author anchor, got %+v", done.ResearchPack.QueryPlan.ProtectedAnchors)
	}
	if protectedAnchorsContainCanonical(done.ResearchPack.QueryPlan.ProtectedAnchors, "kristof_poland") {
		t.Fatalf("stale prior @Kristof_Poland leaked into protected anchors: %+v", done.ResearchPack.QueryPlan.ProtectedAnchors)
	}
	traceJSON, err := os.ReadFile(filepath.Join(cfg.DataDir, filepath.FromSlash(done.TracePath), "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	if !strings.Contains(string(traceJSON), `"original_question": "Can you synthesize @Other_Author?"`) {
		t.Fatalf("expected trace to preserve raw original question:\n%s", string(traceJSON))
	}
}

func TestResearchRunUsesContinuityAnchorsForPronounFollowup(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	sourceKey := seedWebXAuthorItem(t, ctx, cfg, st, "x:kristof-followup", "Kristof_Poland", "Krzysztof Szczawinski", "Kristof follow-up essays")
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Kristof follow-up evidence [` + sourceKey + `]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	requestBody, err := json.Marshal(ResearchRunRequest{
		Question:       "Current question: Synthesize those",
		Limit:          4,
		DisablePlanner: true,
		Model:          "ollama/qwen-test",
		TraceSurface:   "web_chat",
		TraceContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion:  "Synthesize those",
			RetrievalQuestion: "Current question: Synthesize those",
			ContinuityAnchors: []brainresearch.ProtectedAnchor{{
				Kind:       "handle",
				Relation:   "authored_by",
				Raw:        "@Kristof_Poland",
				Canonical:  "kristof_poland",
				ExactTerms: []string{"@Kristof_Poland", "Kristof_Poland", "kristof_poland"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/run", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	var done struct {
		ResearchPack brainresearch.Pack `json:"research_pack"`
		Verification struct {
			Passed bool `json:"passed"`
		} `json:"verification"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if !done.Verification.Passed {
		t.Fatalf("expected verified result, stream:\n%s", rec.Body.String())
	}
	if !protectedAnchorsContainCanonical(done.ResearchPack.QueryPlan.ProtectedAnchors, "kristof_poland") {
		t.Fatalf("expected continuity anchor in protected anchors, got %+v", done.ResearchPack.QueryPlan.ProtectedAnchors)
	}
}

func TestRunnerChatContinuityKeepsPronounAnchorsForCodeLikeUnderscores(t *testing.T) {
	anchor := brainresearch.ProtectedAnchor{
		Kind:      "handle",
		Relation:  "authored_by",
		Raw:       "@Kristof_Poland",
		Canonical: "kristof_poland",
	}
	continuity := runnerChatContinuity(ResearchRunRequest{
		TraceContinuity: &researchtrace.ChatContinuity{ContinuityAnchors: []brainresearch.ProtectedAnchor{anchor}},
	}, "Can you update those user_id tokens?")
	if continuity == nil || len(continuity.ContinuityAnchors) != 1 || continuity.ContinuityAnchors[0].Canonical != "kristof_poland" {
		t.Fatalf("expected code-like underscore to keep pronoun continuity anchors, got %#v", continuity)
	}
}

func TestRunnerRawQuestionRecoversCurrentQuestionWhenOriginalQuestionMissing(t *testing.T) {
	anchor := brainresearch.ProtectedAnchor{
		Kind:      "handle",
		Relation:  "authored_by",
		Raw:       "@Kristof_Poland",
		Canonical: "kristof_poland",
	}
	req := ResearchRunRequest{
		Question: "Current question: Synthesize those\n\nRecent user questions:\n- Can you synthesize @Kristof_Poland?\n\nPrior evidence titles:\n- Kristof row",
		TraceContinuity: &researchtrace.ChatContinuity{
			RetrievalQuestion: "Current question: Synthesize those",
			ContinuityAnchors: []brainresearch.ProtectedAnchor{anchor},
		},
	}
	raw := runnerRawQuestion(req)
	if raw != "Synthesize those" {
		t.Fatalf("expected raw current question, got %q", raw)
	}
	continuity := runnerChatContinuity(req, raw)
	if continuity == nil || len(continuity.ContinuityAnchors) != 1 || continuity.ContinuityAnchors[0].Canonical != "kristof_poland" {
		t.Fatalf("expected recovered raw pronoun question to keep continuity anchor, got %#v", continuity)
	}
}

func TestResearchRunProtectsFeedEntrySourceKeyRawQuestion(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	sourceKey := seedWebGenericItem(t, ctx, cfg, st, "feed-entry:abc123def456", "feed_entry", "Feed entry source-key evidence")
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Feed entry evidence [` + sourceKey + `]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rawQuestion := "Summarize " + sourceKey
	requestBody, err := json.Marshal(ResearchRunRequest{
		Question:       "Current question: " + rawQuestion + "\n\nPrior evidence titles for query focus:\n- @Kristof_Poland old row",
		Limit:          4,
		DisablePlanner: true,
		Model:          "ollama/qwen-test",
		TraceSurface:   "web_chat",
		TraceContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion:  rawQuestion,
			RetrievalQuestion: "Current question: " + rawQuestion,
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/run", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	var done struct {
		ResearchPack brainresearch.Pack `json:"research_pack"`
		Verification struct {
			Passed bool `json:"passed"`
		} `json:"verification"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if !done.Verification.Passed {
		t.Fatalf("expected verified result, stream:\n%s", rec.Body.String())
	}
	if !protectedAnchorsContainCanonical(done.ResearchPack.QueryPlan.ProtectedAnchors, sourceKey) {
		t.Fatalf("expected feed-entry source-key anchor, got %+v", done.ResearchPack.QueryPlan.ProtectedAnchors)
	}
}

func TestRunnerRawQuestionRejectsComposedTraceOriginalQuestion(t *testing.T) {
	t.Parallel()

	raw := runnerRawQuestion(ResearchRunRequest{
		Question: "Current question: Synthesize @Other_Author",
		TraceContinuity: &researchtrace.ChatContinuity{
			OriginalQuestion: "Current question: Synthesize @Kristof_Poland\n\nPrior evidence titles:\n- stale row",
		},
	})
	if raw != "Synthesize @Other_Author" {
		t.Fatalf("expected composed trace original question to be ignored, got %q", raw)
	}
}

func TestResearchRunStreamsVerificationFailure(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	seedTestData(t, ctx, cfg, st)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen-test","message":{"role":"assistant","content":"Agent memory cites missing evidence [src:missing-web-run]."}}`))
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	requestBody, err := json.Marshal(ResearchRunRequest{
		Question:         "What do I know about agent memory?",
		Limit:            4,
		DisablePlanner:   true,
		Model:            "ollama/qwen-test",
		MaxEvidenceChars: 4000,
		TraceSurface:     "web_chat",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/run", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	if _, ok := events["answer"]; ok {
		t.Fatalf("verification-failed runner must not emit normal answer event: %+v", events)
	}
	if _, ok := events["verification_failed"]; !ok {
		t.Fatalf("expected verification_failed event:\n%s", rec.Body.String())
	}
	var failed struct {
		AnswerStatus string   `json:"answer_status"`
		StopReason   string   `json:"stop_reason"`
		Errors       []string `json:"errors"`
		Error        string   `json:"error"`
		TracePath    string   `json:"trace_path"`
	}
	if err := json.Unmarshal([]byte(events["verification_failed"][0]), &failed); err != nil {
		t.Fatalf("decode verification_failed event: %v", err)
	}
	if failed.AnswerStatus != "error" || failed.StopReason != "verification_failed" || !strings.Contains(failed.Error, "src:missing-web-run") {
		t.Fatalf("unexpected verification_failed payload: %+v", failed)
	}
	var done struct {
		StopReason   string                   `json:"stop_reason"`
		Citations    []brainresearch.Citation `json:"citations"`
		Verification struct {
			Passed bool     `json:"passed"`
			Errors []string `json:"errors"`
		} `json:"verification"`
		TracePath string `json:"trace_path"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if done.StopReason != "verification_failed" || done.Verification.Passed || len(done.Verification.Errors) == 0 {
		t.Fatalf("unexpected verification failed done event: %+v", done)
	}
	if len(done.Citations) != 0 {
		t.Fatalf("verification-failed runner must not expose rejected citations: %+v", done.Citations)
	}
	traceJSON, err := os.ReadFile(filepath.Join(cfg.DataDir, filepath.FromSlash(done.TracePath), "run.json"))
	if err != nil {
		t.Fatalf("read trace json: %v", err)
	}
	for _, value := range []string{`"stop_reason": "verification_failed"`, `"code": "verification_failed"`, "src:missing-web-run"} {
		if !strings.Contains(string(traceJSON), value) {
			t.Fatalf("expected verification trace to contain %q:\n%s", value, string(traceJSON))
		}
	}
}

func TestResearchTraceListAndCompare(t *testing.T) {
	ctx := t.Context()
	cfg, st := openTestStore(t)
	_, sourceKey := seedTestData(t, ctx, cfg, st)
	pack, err := brainresearch.Build(ctx, cfg, st, brainresearch.Options{
		Question:       "What do I know about agent memory?",
		Limit:          4,
		DisablePlanner: true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	result, err := researchtrace.Write(cfg, researchtrace.ResearchTrace{
		SchemaVersion: researchtrace.SchemaVersion,
		RunID:         "web-trace-compare",
		Surface:       "web_chat",
		Question:      "What do I know about agent memory?",
		StartedAt:     time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		CompletedAt:   time.Date(2026, 5, 23, 12, 0, 1, 0, time.UTC),
		Pack:          &pack,
		Synthesis: &brainresearch.SynthesisResult{
			SchemaVersion: brainresearch.SynthesisSchemaVersion,
			Question:      "What do I know about agent memory?",
			Answer:        "Agent memory answer [" + sourceKey + "].",
			AnswerStatus:  "ok",
			Citations:     []brainresearch.Citation{{SourceKey: sourceKey}},
		},
		StopReason: "enough_evidence",
	}, researchtrace.ArtifactContents{}, researchtrace.WriteOptions{Retention: researchtrace.RetentionOptions{KeepAll: true}})
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	_, err = researchtrace.Write(cfg, researchtrace.ResearchTrace{
		SchemaVersion: researchtrace.SchemaVersion,
		RunID:         "web-trace-compare-newer",
		Surface:       "web_chat",
		Question:      "What changed in agent memory?",
		StartedAt:     time.Date(2026, 5, 23, 12, 1, 0, 0, time.UTC),
		CompletedAt:   time.Date(2026, 5, 23, 12, 1, 1, 0, time.UTC),
		Pack:          &pack,
		Synthesis: &brainresearch.SynthesisResult{
			SchemaVersion: brainresearch.SynthesisSchemaVersion,
			Question:      "What changed in agent memory?",
			Answer:        "Newer agent memory answer [" + sourceKey + "].",
			AnswerStatus:  "ok",
			Citations:     []brainresearch.Citation{{SourceKey: sourceKey}},
		},
		StopReason: "enough_evidence",
	}, researchtrace.ArtifactContents{}, researchtrace.WriteOptions{Retention: researchtrace.RetentionOptions{KeepAll: true}})
	if err != nil {
		t.Fatalf("write newer trace: %v", err)
	}
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/research/traces?limit=10", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store trace list response, got %q", got)
	}
	var listResponse struct {
		Traces []researchtrace.TraceSummary `json:"traces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("decode traces list: %v", err)
	}
	if len(listResponse.Traces) != 2 {
		t.Fatalf("expected trace listing, got %+v", listResponse)
	}
	if listResponse.Traces[0].RelativePath != "research-runs/web-trace-compare-newer" || listResponse.Traces[1].RelativePath != "research-runs/web-trace-compare" {
		t.Fatalf("expected newest trace listing first, got %+v", listResponse)
	}

	body, err := json.Marshal(ResearchTraceCompareRequest{TracePath: result.RelativePath})
	if err != nil {
		t.Fatalf("marshal compare body: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/research/trace-compare", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store trace compare response, got %q", got)
	}
	var response struct {
		OldAnswer string `json:"old_answer"`
		Diff      struct {
			OldSourceKeys []string `json:"old_source_keys"`
			NewSourceKeys []string `json:"new_source_keys"`
		} `json:"diff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	if !strings.Contains(response.OldAnswer, sourceKey) {
		t.Fatalf("expected old answer, got %#v", response)
	}
	if len(response.Diff.OldSourceKeys) == 0 || len(response.Diff.NewSourceKeys) == 0 {
		t.Fatalf("expected old/new source-key diff, got %#v", response.Diff)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/research/trace-compare", bytes.NewBufferString(`{"trace_path":"../outside.json"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected traversal rejection, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestResearchTraceCompareReturnsSavedTraceWhenDiffFails(t *testing.T) {
	cfg, st := openTestStore(t)
	result, err := researchtrace.Write(cfg, researchtrace.ResearchTrace{
		SchemaVersion: researchtrace.SchemaVersion,
		RunID:         "web-trace-diff-fails",
		Surface:       "web_chat",
		Question:      "Trace without pack",
		StartedAt:     time.Date(2026, 5, 23, 12, 2, 0, 0, time.UTC),
		CompletedAt:   time.Date(2026, 5, 23, 12, 2, 1, 0, time.UTC),
		Synthesis: &brainresearch.SynthesisResult{
			SchemaVersion: brainresearch.SynthesisSchemaVersion,
			Question:      "Trace without pack",
			Answer:        "Saved answer from a trace whose diff cannot rerun.",
			AnswerStatus:  "ok",
		},
		StopReason: "verification_failed",
	}, researchtrace.ArtifactContents{}, researchtrace.WriteOptions{Retention: researchtrace.RetentionOptions{KeepAll: true}})
	if err != nil {
		t.Fatalf("write trace: %v", err)
	}
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	body, err := json.Marshal(ResearchTraceCompareRequest{TracePath: result.RelativePath})
	if err != nil {
		t.Fatalf("marshal compare body: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/trace-compare", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		OldAnswer string `json:"old_answer"`
		DiffError string `json:"diff_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	if !strings.Contains(response.OldAnswer, "Saved answer") {
		t.Fatalf("expected saved answer to remain available: %+v", response)
	}
	if !strings.Contains(response.DiffError, "does not contain a research pack") {
		t.Fatalf("expected diff error in 200 response: %+v", response)
	}
}

func TestResearchSynthesizeRejectsInvalidPack(t *testing.T) {
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewBufferString(`{"question":"Agent memory","research_pack":{"schema_version":"old"}}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "research_pack.schema_version") {
		t.Fatalf("expected schema diagnostic, got %q", rec.Body.String())
	}
}

func TestResearchSynthesizeReturnsUnavailableWithoutConfiguredModel(t *testing.T) {
	t.Setenv("DBRAIN_SUMMARY_MODEL", "")
	t.Setenv("SUMMARIZE_MODEL", "")
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan:     brainresearch.QueryPlan{TextQuery: "agent memory"},
		Coverage:      brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{SourceKey: "src:test", Kind: "source", Title: "Agent Memory", NotePath: "sources/test.md", Summary: "Agent memory evidence."},
		},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:     pack.Question,
		ResearchPack: pack,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("expected JSON unavailable response, got %q", got)
	}
	var payload struct {
		AnswerStatus string   `json:"answer_status"`
		Warnings     []string `json:"answer_warnings"`
		Error        string   `json:"error"`
		TracePath    string   `json:"trace_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode unavailable response: %v", err)
	}
	if payload.AnswerStatus != "unavailable" || len(payload.Warnings) != 1 || payload.Warnings[0] != "model_unavailable" {
		t.Fatalf("unexpected unavailable payload: %+v", payload)
	}
}

func TestResearchSynthesizeNoEvidenceSkipsModel(t *testing.T) {
	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about nonexistent material?",
		QueryPlan:     brainresearch.QueryPlan{TextQuery: "nonexistent material"},
		Coverage:      brainresearch.Coverage{EvidenceCount: 0, RecallNote: "no evidence"},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:     pack.Question,
		ResearchPack: pack,
		Model:        "ollama/unused",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	if _, ok := events["answer"]; ok {
		t.Fatalf("no-evidence synthesis should not emit answer: %+v", events)
	}
	var done struct {
		brainresearch.SynthesisResult
		TracePath string `json:"trace_path"`
	}
	if err := json.Unmarshal([]byte(events["done"][0]), &done); err != nil {
		t.Fatalf("decode done event: %v", err)
	}
	if done.AnswerStatus != "no_evidence" || len(done.Warnings) == 0 {
		t.Fatalf("unexpected no-evidence done event: %+v", done)
	}
	if !strings.HasPrefix(done.TracePath, "research-runs/") {
		t.Fatalf("expected no-evidence trace path, got %+v", done)
	}
}

func TestResearchSynthesizeStreamsPostStartError(t *testing.T) {
	cfg, st := openTestStore(t)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(ollama.Close)
	t.Setenv("DBRAIN_OLLAMA_BASE_URL", ollama.URL)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	pack := brainresearch.Pack{
		SchemaVersion: brainresearch.SchemaVersion,
		Question:      "What do I know about agent memory?",
		QueryPlan:     brainresearch.QueryPlan{TextQuery: "agent memory"},
		Coverage:      brainresearch.Coverage{EvidenceCount: 1, RecallNote: "one evidence row"},
		Evidence: []ask.Evidence{
			{SourceKey: "src:test", Kind: "source", Title: "Agent Memory", NotePath: "sources/test.md", Summary: "Agent memory evidence."},
		},
	}
	requestBody, err := json.Marshal(ResearchSynthesisRequest{
		Question:     pack.Question,
		ResearchPack: pack,
		Model:        "ollama/qwen-test",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/research/synthesize", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected stream to start with 200, got %d: %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	if _, ok := events["start"]; !ok {
		t.Fatalf("expected start event: %+v", events)
	}
	if _, ok := events["error"]; !ok {
		t.Fatalf("expected terminal error event: %+v", events)
	}
	if _, ok := events["done"]; ok {
		t.Fatalf("error stream must not also emit done: %+v", events)
	}
	var payload struct {
		AnswerStatus string   `json:"answer_status"`
		Warnings     []string `json:"answer_warnings"`
		Error        string   `json:"error"`
		TracePath    string   `json:"trace_path"`
	}
	if err := json.Unmarshal([]byte(events["error"][0]), &payload); err != nil {
		t.Fatalf("decode error event: %v", err)
	}
	if payload.AnswerStatus != "error" || !strings.Contains(payload.Error, "503") {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	if !strings.HasPrefix(payload.TracePath, "research-runs/") {
		t.Fatalf("expected error trace path, got %+v", payload)
	}
}

func parseSSEEvents(t *testing.T, body string) map[string][]string {
	t.Helper()

	events := map[string][]string{}
	currentEvent := ""
	var data strings.Builder
	flush := func() {
		if currentEvent == "" {
			data.Reset()
			return
		}
		events[currentEvent] = append(events[currentEvent], data.String())
		currentEvent = ""
		data.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data: ")))
		}
	}
	flush()
	return events
}

func evidenceContainsSourceKey(rows []ask.Evidence, sourceKey string) bool {
	for _, row := range rows {
		if row.SourceKey == sourceKey {
			return true
		}
	}
	return false
}

func citationsContainSourceKey(rows []brainresearch.Citation, sourceKey string) bool {
	for _, row := range rows {
		if row.SourceKey == sourceKey {
			return true
		}
	}
	return false
}

func TestWebHandlerServesArchivedMediaAndSignedURL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openTestStore(t)
	itemID, _ := seedTestData(t, ctx, cfg, st)
	now := time.Date(2026, time.April, 25, 22, 0, 0, 0, time.UTC)

	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "Video post",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"source":"graphql",
			"fetched_at":"2026-04-25T22:00:00Z",
			"snapshot":{
				"id":"123",
				"text":"Video post",
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/test.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":1280,"height":720}
				]
			},
			"raw":{}
		}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 media ref, got %+v", refs)
	}

	if _, err := st.SaveMediaDownload(ctx, refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     12,
		ContentHash:  "video-hash",
		LocalPath:    "media/x/video/ab/test.mp4",
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}
	if _, err := st.SaveMediaArchive(ctx, refs[0].MediaAssetID, model.MediaArchiveResult{
		Provider:   "cloudflare_r2",
		Bucket:     "dbrain",
		Key:        "media/x/video/ab/test.mp4",
		Status:     "archived",
		ArchivedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaArchive: %v", err)
	}
	if _, err := st.MarkMediaLocalPrunedByPath(ctx, "media/x/video/ab/test.mp4", now); err != nil {
		t.Fatalf("MarkMediaLocalPrunedByPath: %v", err)
	}

	staticFS, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	indexHTML, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		t.Fatalf("ReadFile index: %v", err)
	}

	s := &server{
		cfg:         cfg,
		store:       st,
		archive:     &fakeArchiveProxy{body: []byte("hello-video!"), signedURL: "https://signed.example.com/video.mp4"},
		proxyBase:   "http://127.0.0.1:8742",
		staticFS:    staticFS,
		static:      http.FileServerFS(staticFS),
		indexHTML:   indexHTML,
		toolVersion: "test",
	}
	handler := s.newMux()

	t.Run("media get", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("expected video/mp4 content type, got %q", got)
		}
		if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
			t.Fatalf("expected bytes accept ranges, got %q", got)
		}
		if got := rec.Body.String(); got != "hello-video!" {
			t.Fatalf("unexpected body %q", got)
		}
	})

	t.Run("media head", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodHead, "/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty head body, got %q", rec.Body.String())
		}
	})

	t.Run("media range", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		req.Header.Set("Range", "bytes=0-3")
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Range"); got != "bytes 0-3/12" {
			t.Fatalf("unexpected content-range %q", got)
		}
		if got := rec.Body.String(); got != "hell" {
			t.Fatalf("unexpected ranged body %q", got)
		}
	})

	t.Run("signed url", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/media/signed-url?id="+strconv.FormatInt(refs[0].MediaAssetID, 10), nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var response signedURLResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode signed url response: %v", err)
		}
		if response.URL != "https://signed.example.com/video.mp4" {
			t.Fatalf("unexpected signed url response %+v", response)
		}
		if response.ProxyURL != "http://127.0.0.1:8742/media/asset/"+strconv.FormatInt(refs[0].MediaAssetID, 10) {
			t.Fatalf("unexpected proxy url response %+v", response)
		}
		for _, forbidden := range []string{`"bucket"`, `"key"`, `"source"`, "dbrain", "media/x/video/ab/test.mp4"} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("signed url response exposed storage metadata %q: %s", forbidden, rec.Body.String())
			}
		}
	})

	t.Run("detail media payload hides storage metadata", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/get?lookup=item:test-agent-memory", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, forbidden := range []string{`"local_path"`, `"archive_bucket"`, `"archive_key"`, `"raw_json"`, `"x_post_json"`, `"summary_json"`, `"ocr_json"`, "media/x/video/ab/test.mp4"} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("detail media response exposed storage metadata %q: %s", forbidden, rec.Body.String())
			}
		}
		var response GetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode get item: %v", err)
		}
		if response.Item == nil || len(response.Item.Media) != 1 {
			t.Fatalf("expected sanitized media ref, got %+v", response.Item)
		}
		if response.Item.Media[0].MediaAssetID != refs[0].MediaAssetID || response.Item.Media[0].MediaType != "video" {
			t.Fatalf("unexpected sanitized media ref %+v", response.Item.Media[0])
		}
	})

	t.Run("search media payload hides storage metadata", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/search?q=Video+post&limit=5", nil)
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, forbidden := range []string{`"local_path"`, `"archive_bucket"`, `"archive_key"`, "media/x/video/ab/test.mp4"} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("search media response exposed storage metadata %q: %s", forbidden, rec.Body.String())
			}
		}
		var response SearchResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode search response: %v", err)
		}
		if len(response.Results) == 0 || len(response.Results[0].Media) != 1 {
			t.Fatalf("expected search result with sanitized media, got %+v", response.Results)
		}
		if response.Results[0].Media[0].MediaAssetID != refs[0].MediaAssetID || response.Results[0].Media[0].MediaType != "video" {
			t.Fatalf("unexpected search media ref %+v", response.Results[0].Media[0])
		}
	})

	t.Run("research evidence includes media payload", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/research", strings.NewReader(`{"question":"Video post","limit":4,"disable_planner":true}`))
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		for _, forbidden := range []string{`"local_path"`, `"archive_bucket"`, `"archive_key"`, "media/x/video/ab/test.mp4"} {
			if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
				t.Fatalf("research media response exposed storage metadata %q: %s", forbidden, rec.Body.String())
			}
		}
		var response brainresearch.Pack
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode research response: %v", err)
		}
		if len(response.Evidence) == 0 || len(response.Evidence[0].Media) != 1 {
			t.Fatalf("expected research evidence with media, got %+v", response.Evidence)
		}
		if response.Evidence[0].Media[0].MediaAssetID != refs[0].MediaAssetID || response.Evidence[0].Media[0].MediaType != "video" {
			t.Fatalf("unexpected research media ref %+v", response.Evidence[0].Media[0])
		}
	})
}

func TestWhatsNewEndpointReturnsReviewEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openTestStore(t)
	seedTestData(t, ctx, cfg, st)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/whats-new?since=24h&limit=25&types=imports,enrichments,failures", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, forbidden := range []string{`"events": null`, `"counts": null`, `"reasons": null`, `"tags": null`} {
		if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("whats-new response exposed null array field %q: %s", forbidden, rec.Body.String())
		}
	}

	var response store.ReviewEventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode whats-new response: %v", err)
	}
	if len(response.Events) == 0 {
		t.Fatalf("expected review events")
	}
	if response.NextCursor == "" {
		t.Fatalf("expected continuation cursor")
	}
	if response.HighWatermark.IsZero() {
		t.Fatalf("expected high watermark")
	}
	if response.Counts == nil {
		t.Fatalf("expected non-nil count buckets")
	}

	seenKinds := map[string]bool{}
	for _, event := range response.Events {
		seenKinds[event.EventKind] = true
		if event.EventID == "" || event.EntityKey == "" || event.EventAt.IsZero() {
			t.Fatalf("expected populated event identity and timestamp, got %+v", event)
		}
	}
	for _, want := range []string{store.ReviewEventKindItemImported, store.ReviewEventKindSourceFailed} {
		if !seenKinds[want] {
			t.Fatalf("expected whats-new response to include %q, got kinds=%v", want, seenKinds)
		}
	}
}

func TestWhatsNewEndpointReturnsEntityView(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg, st := openTestStore(t)
	seedTestData(t, ctx, cfg, st)

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/whats-new?since=24h&limit=25&view=entities", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, forbidden := range []string{`"events": null`, `"entities": null`, `"event_kinds": null`, `"counts": null`} {
		if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("whats-new entity response exposed null array field %q: %s", forbidden, rec.Body.String())
		}
	}

	var response store.ReviewEventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode whats-new entity response: %v", err)
	}
	if len(response.Events) != 0 {
		t.Fatalf("entity view should suppress raw event rows, got %+v", response.Events)
	}
	if len(response.Entities) == 0 {
		t.Fatalf("expected grouped entities")
	}
	if response.NextCursor == "" || response.HighWatermark.IsZero() {
		t.Fatalf("expected cursor metadata, got %+v", response)
	}
}

func TestWhatsNewEndpointRejectsInvalidSince(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/whats-new?since=2026-06-21", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "since must be RFC3339") {
		t.Fatalf("expected invalid since diagnostic, got %s", rec.Body.String())
	}
}

func TestWhatsNewEndpointRejectsCursorAndSinceTogether(t *testing.T) {
	t.Parallel()

	cfg, st := openTestStore(t)
	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	cursorToken, err := store.EncodeReviewCursor(store.ReviewCursor{EventAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/whats-new?since=24h&cursor="+cursorToken, nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exactly one of since or cursor") {
		t.Fatalf("expected mutually exclusive cursor diagnostic, got %s", rec.Body.String())
	}
}

func openTestStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(cfg.VaultDir, "sources"),
		filepath.Join(cfg.VaultDir, "items"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	return cfg, st
}

func seedTestData(t *testing.T, ctx context.Context, cfg config.Config, st *store.Store) (int64, string) {
	t.Helper()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:       "item:test-agent-memory",
		SourceType:      "x_bookmark",
		ExternalID:      "x-123",
		CanonicalURL:    "https://x.com/darron/status/123",
		Title:           "Agent Memory Notes",
		AuthorHandle:    "darron",
		AuthorName:      "Darron",
		Language:        "en",
		Text:            "Short note about agent memory systems.",
		ArticleTitle:    "Agent Memory Systems",
		ArticleText:     "Agent memory systems need durable notes, retrieval, and source tracking.",
		PrimaryCategory: "agents",
		PrimaryDomain:   "x.com",
		LinksJSON:       "[]",
		ContentHash:     "item-hash",
		NotePath:        "items/test-agent-memory.md",
		RawJSON:         "{}",
		UpdatedAt:       now,
		LastSeenAt:      now,
	}

	upsert, err := st.UpsertItem(ctx, item)
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := upsert.ItemID
	if err := st.SaveItemUserTags(ctx, itemID, "agent-memory, retrieval"); err != nil {
		t.Fatalf("SaveItemUserTags: %v", err)
	}

	itemNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	if err := os.WriteFile(itemNotePath, []byte("# Agent Memory Notes\n\nLocal note content.\n"), 0o644); err != nil {
		t.Fatalf("write item note: %v", err)
	}

	link, err := st.UpsertSourceLink(ctx, itemID, model.SourceCandidate{
		OriginalURL:   "https://example.com/agent-memory",
		CanonicalURL:  "https://example.com/agent-memory",
		NormalizedURL: "https://example.com/agent-memory",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:test-agent-memory",
		NotePath:      "sources/test-agent-memory.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}

	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		CanonicalURL: "https://example.com/agent-memory",
		FinalURL:     "https://example.com/agent-memory",
		Title:        "Agent Memory Article",
		Description:  "Why durable memory matters",
		SiteName:     "Example",
		Content:      "This article explains retrieval, memory, and note linking.",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, "source-hash"); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Text:          "Summary about agent memory retrieval.",
		RawJSON:       `{"summary":"Summary about agent memory retrieval."}`,
		Status:        "ok",
		FetchedAt:     now,
		PromptVersion: "test-prompt",
		Tool:          "summarize",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}

	sourceNotePath := filepath.Join(cfg.VaultDir, "sources", "test-agent-memory.md")
	if err := os.WriteFile(sourceNotePath, []byte("# Agent Memory Article\n\nRendered source note.\n"), 0o644); err != nil {
		t.Fatalf("write source note: %v", err)
	}

	failedLink, err := st.UpsertSourceLink(ctx, itemID, model.SourceCandidate{
		OriginalURL:   "https://broken.example.com/agent-memory",
		CanonicalURL:  "https://broken.example.com/agent-memory",
		NormalizedURL: "https://broken.example.com/agent-memory",
		SourceType:    "web",
		Domain:        "broken.example.com",
		SourceKey:     "src:test-agent-memory-failure",
		NotePath:      "sources/test-agent-memory-failure.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink failed source: %v", err)
	}
	failureAt := now.Add(2 * time.Minute)
	if _, err := st.SaveSourceExtraction(ctx, failedLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://broken.example.com/agent-memory",
		FinalURL:     "https://broken.example.com/agent-memory",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    failureAt,
		Tool:         "summarize",
		ToolVersion:  "test",
	}, ""); err != nil {
		t.Fatalf("save failed source extraction: %v", err)
	}

	failedLinkTwo, err := st.UpsertSourceLink(ctx, itemID, model.SourceCandidate{
		OriginalURL:   "https://broken.example.com/agent-memory-two",
		CanonicalURL:  "https://broken.example.com/agent-memory-two",
		NormalizedURL: "https://broken.example.com/agent-memory-two",
		SourceType:    "web",
		Domain:        "broken.example.com",
		SourceKey:     "src:test-agent-memory-failure-two",
		NotePath:      "sources/test-agent-memory-failure-two.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink second failed source: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, failedLinkTwo.SourceID, model.ExtractResult{
		CanonicalURL: "https://broken.example.com/agent-memory-two",
		FinalURL:     "https://broken.example.com/agent-memory-two",
		Status:       "error",
		Error:        "Unable to connect. Is the computer able to access the url?",
		FetchedAt:    failureAt.Add(-45 * time.Minute),
		Tool:         "summarize",
		ToolVersion:  "test",
	}, ""); err != nil {
		t.Fatalf("save second failed source extraction: %v", err)
	}

	return itemID, "src:test-agent-memory"
}

func seedWebXAuthorItem(t *testing.T, ctx context.Context, cfg config.Config, st *store.Store, sourceKey string, handle string, name string, title string) string {
	t.Helper()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   strings.TrimPrefix(sourceKey, "x:"),
		CanonicalURL: "https://x.com/" + handle + "/status/" + strings.TrimPrefix(sourceKey, "x:"),
		Title:        title,
		AuthorHandle: handle,
		AuthorName:   name,
		Language:     "en",
		Text:         title + " with direct local evidence.",
		SummaryText:  title + " with direct local evidence.",
		ContentHash:  sourceKey + "-hash",
		NotePath:     "items/x/" + strings.TrimPrefix(sourceKey, "x:") + ".md",
		RawJSON:      "{}",
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	if _, err := st.UpsertItem(ctx, item); err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}
	itemNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	if err := os.MkdirAll(filepath.Dir(itemNotePath), 0o755); err != nil {
		t.Fatalf("create item note dir: %v", err)
	}
	if err := os.WriteFile(itemNotePath, []byte("# "+title+"\n\nLocal note content.\n"), 0o644); err != nil {
		t.Fatalf("write item note: %v", err)
	}
	return item.SourceKey
}

func seedWebGenericItem(t *testing.T, ctx context.Context, cfg config.Config, st *store.Store, sourceKey string, sourceType string, title string) string {
	t.Helper()

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    sourceKey,
		SourceType:   sourceType,
		ExternalID:   strings.TrimPrefix(sourceKey, sourceType+":"),
		CanonicalURL: "https://example.com/" + strings.ReplaceAll(sourceKey, ":", "/"),
		Title:        title,
		Language:     "en",
		Text:         title + " with direct local evidence.",
		SummaryText:  title + " with direct local evidence.",
		ContentHash:  sourceKey + "-hash",
		NotePath:     "items/generic/" + strings.NewReplacer(":", "-", "/", "-").Replace(sourceKey) + ".md",
		RawJSON:      "{}",
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	if _, err := st.UpsertItem(ctx, item); err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}
	itemNotePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	if err := os.MkdirAll(filepath.Dir(itemNotePath), 0o755); err != nil {
		t.Fatalf("create item note dir: %v", err)
	}
	if err := os.WriteFile(itemNotePath, []byte("# "+title+"\n\nLocal note content.\n"), 0o644); err != nil {
		t.Fatalf("write item note: %v", err)
	}
	return item.SourceKey
}

func protectedAnchorsContainCanonical(anchors []brainresearch.ProtectedAnchor, canonical string) bool {
	for _, anchor := range anchors {
		if anchor.Canonical == canonical {
			return true
		}
	}
	return false
}
