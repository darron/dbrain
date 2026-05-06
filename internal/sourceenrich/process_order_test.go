package sourceenrich

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func TestProcessSingleSourceUsesStoredExtractBeforeHTTPReaderFallback(t *testing.T) {
	root := t.TempDir()
	cfg, st := openSourceEnrichProcessOrderStore(t, root)
	defer func() { _ = st.Close() }()
	installSourceEnrichFakeSummarize(t, root)

	var readerHits atomic.Int32
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readerHits.Add(1)
		http.Error(w, "reader should not be called", http.StatusInternalServerError)
	}))
	defer reader.Close()

	now := time.Now().UTC()
	sourceURL := reader.URL + "/stored-extract-first"
	sourceID := upsertProcessOrderSource(t, st, model.SourceCandidate{
		SourceKey:     "src:stored-extract-before-reader",
		OriginalURL:   sourceURL,
		CanonicalURL:  sourceURL,
		NormalizedURL: sourceURL,
		SourceType:    "web",
		Domain:        "127.0.0.1",
		NotePath:      vault.SourceNoteRelativePath("web", "stored-extract-before-reader"),
	})
	if _, err := st.SaveSourceExtraction(context.Background(), sourceID, model.ExtractResult{
		CanonicalURL: sourceURL,
		FinalURL:     sourceURL,
		Title:        "Stored extract",
		SiteName:     "Example",
		Content:      "README CONTENT FROM GITHUB API",
		Status:       model.SourceExtractStatusOK,
		FetchedAt:    now,
		Tool:         "test-stored-extract",
		ToolVersion:  "v1",
	}, "stored-extract-hash"); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{sourceID}, Options{
		Summarize:                 true,
		Model:                     "cli/test/sourceenrich",
		Length:                    "short",
		Timeout:                   5 * time.Second,
		HTTPReaderFallbackDomains: []string{"127.0.0.1"},
		HTTPReaderBaseURL:         reader.URL + "/reader/",
		ResolveHost: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.Errors != 0 || stats.SourcesSummarized != 1 {
		t.Fatalf("expected stored extract summary success, got %+v", stats)
	}
	if got := readerHits.Load(); got != 0 {
		t.Fatalf("expected reader fallback to be skipped, got %d hits", got)
	}

	source, err := st.GetSourceByID(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.SummaryStatus != model.SourceSummaryStatusOK {
		t.Fatalf("expected summary ok, got %q error=%q", source.SummaryStatus, source.SummaryError)
	}
	if source.SummaryText != "summary from stored extract" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func TestProcessSingleSourcePreflightTerminalBeforeHTTPReaderFallback(t *testing.T) {
	t.Setenv("DBRAIN_SOURCE_WAYBACK_ENABLED", "false")

	root := t.TempDir()
	cfg, st := openSourceEnrichProcessOrderStore(t, root)
	defer func() { _ = st.Close() }()

	var readerHits atomic.Int32
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readerHits.Add(1)
		http.Error(w, "reader should not be called", http.StatusInternalServerError)
	}))
	defer reader.Close()

	sourceURL := "https://reader-order-test.invalid/article"
	sourceID := upsertProcessOrderSource(t, st, model.SourceCandidate{
		SourceKey:     "src:preflight-before-reader",
		OriginalURL:   sourceURL,
		CanonicalURL:  sourceURL,
		NormalizedURL: sourceURL,
		SourceType:    "web",
		Domain:        "reader-order-test.invalid",
		NotePath:      vault.SourceNoteRelativePath("web", "preflight-before-reader"),
	})

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{sourceID}, Options{
		Summarize:                 true,
		Timeout:                   5 * time.Second,
		HTTPReaderFallbackDomains: []string{"reader-order-test.invalid"},
		HTTPReaderBaseURL:         reader.URL + "/reader/",
		ResolveHost: func(context.Context, string) error {
			return &net.DNSError{Err: "no such host", Name: "reader-order-test.invalid", IsNotFound: true}
		},
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected one terminal preflight error, got %+v", stats)
	}
	if got := readerHits.Load(); got != 0 {
		t.Fatalf("expected reader fallback to be skipped, got %d hits", got)
	}

	source, err := st.GetSourceByID(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("GetSourceByID: %v", err)
	}
	if source.ExtractStatus != model.SourceExtractStatusDead {
		t.Fatalf("expected dead extract status, got %q", source.ExtractStatus)
	}
	if !strings.Contains(source.ExtractError, "host does not resolve") {
		t.Fatalf("unexpected extract error: %q", source.ExtractError)
	}
	if source.SummaryStatus != model.SourceSummaryStatusSkipped {
		t.Fatalf("expected skipped summary status, got %q", source.SummaryStatus)
	}
}

func openSourceEnrichProcessOrderStore(t *testing.T, root string) (config.Config, *store.Store) {
	t.Helper()

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return cfg, st
}

func upsertProcessOrderSource(t *testing.T, st *store.Store, candidate model.SourceCandidate) int64 {
	t.Helper()

	result, err := st.UpsertSource(context.Background(), candidate)
	if err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	return result.SourceID
}
