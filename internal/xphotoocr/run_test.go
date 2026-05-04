package xphotoocr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestRunHostedOCRWritesItemOCRAndNote(t *testing.T) {
	t.Parallel()

	cfg, st, item := seedDownloadedPhotoItem(t, "x:test-photo-hosted-ocr", "2049000000000000001")

	var capturedAuth string
	var capturedUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedUserAgent = r.Header.Get("User-Agent")
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"google/gemini-3.1-flash-lite-preview","choices":[{"message":{"content":"Heading\nCaptured OCR text from the hosted model."}}]}`))
	}))
	defer server.Close()

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:           10,
		Model:           "openrouter/google/gemini-3.1-flash-lite-preview",
		OpenRouterBase:  server.URL,
		OpenRouterKey:   "test-openrouter-key",
		OpenRouterRef:   "https://dbrain.test",
		OpenRouterTitle: "dbrain-test",
		UserAgent:       "dbrain/test-sha",
		Timeout:         2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedAuth != "Bearer test-openrouter-key" {
		t.Fatalf("unexpected auth header: %q", capturedAuth)
	}
	if capturedUserAgent != "dbrain/test-sha" {
		t.Fatalf("unexpected user-agent header: %q", capturedUserAgent)
	}
	if stats.ItemsUpdated != 1 || stats.PhotosOCRed != 1 || stats.HostedAttempts != 1 || stats.HostedFallbacks != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	refreshed, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if refreshed.OCRStatus != "ok" {
		t.Fatalf("expected ocr status ok, got %q", refreshed.OCRStatus)
	}
	if refreshed.OCRTool != openRouterVisionTool {
		t.Fatalf("expected hosted ocr tool, got %q", refreshed.OCRTool)
	}
	if !strings.Contains(refreshed.OCRText, "Captured OCR text") {
		t.Fatalf("expected hosted OCR text, got %q", refreshed.OCRText)
	}

	noteBytes, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(refreshed.NotePath)))
	if err != nil {
		t.Fatalf("ReadFile note: %v", err)
	}
	note := string(noteBytes)
	if !strings.Contains(note, "## OCR / Vision Extract") || !strings.Contains(note, "Captured OCR text") {
		t.Fatalf("expected OCR section in note, got %q", note)
	}
}

func TestRunOllamaOCRWritesItemOCRAndNote(t *testing.T) {
	t.Parallel()

	cfg, st, item := seedDownloadedPhotoItem(t, "x:test-photo-ollama-ocr", "2049000000000000101")

	var captured ollamaOCRRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3.6:35b-a3b-nvfp4","message":{"role":"assistant","content":"Local Ollama OCR text."},"done":true}`))
	}))
	defer server.Close()

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:      10,
		Model:      "ollama/qwen3.6:35b-a3b-nvfp4",
		OllamaBase: server.URL,
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.ItemsUpdated != 1 || stats.PhotosOCRed != 1 || stats.HostedAttempts != 0 || stats.HostedFallbacks != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if captured.Model != "qwen3.6:35b-a3b-nvfp4" {
		t.Fatalf("unexpected ollama model: %q", captured.Model)
	}
	if captured.Think == nil || *captured.Think {
		t.Fatalf("expected ollama OCR to disable thinking, got %#v", captured.Think)
	}
	if len(captured.Messages) != 1 || len(captured.Messages[0].Images) != 1 {
		t.Fatalf("expected one message with one image, got %+v", captured.Messages)
	}

	refreshed, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if refreshed.OCRTool != ollamaVisionTool {
		t.Fatalf("expected ollama OCR tool, got %q", refreshed.OCRTool)
	}
	if refreshed.OCRModel != "ollama/qwen3.6:35b-a3b-nvfp4" {
		t.Fatalf("expected ollama OCR model, got %q", refreshed.OCRModel)
	}
	if !strings.Contains(refreshed.OCRText, "Local Ollama OCR text") {
		t.Fatalf("expected Ollama OCR text, got %q", refreshed.OCRText)
	}
}

func TestRunFallsBackToTesseractAndPreservesOCRAcrossLaterBlankUpsert(t *testing.T) {
	t.Parallel()

	cfg, st, item := seedDownloadedPhotoItem(t, "x:test-photo-fallback-ocr", "2049000000000000002")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream refused image", http.StatusBadGateway)
	}))
	defer server.Close()

	tesseract := installFakeTesseract(t, "Fallback OCR text from local tesseract.\n")
	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:           10,
		Model:           "openrouter/google/gemini-3.1-flash-lite-preview",
		OpenRouterBase:  server.URL,
		OpenRouterKey:   "test-openrouter-key",
		TesseractBinary: tesseract,
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.ItemsUpdated != 1 || stats.PhotosOCRed != 1 || stats.HostedAttempts != 1 || stats.HostedFallbacks != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	refreshed, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if refreshed.OCRTool != tesseractTool {
		t.Fatalf("expected tesseract tool, got %q", refreshed.OCRTool)
	}
	if !strings.Contains(refreshed.OCRText, "Fallback OCR text") {
		t.Fatalf("expected fallback OCR text, got %q", refreshed.OCRText)
	}

	later := model.Item{
		SourceKey:    refreshed.SourceKey,
		SourceType:   refreshed.SourceType,
		ExternalID:   refreshed.ExternalID,
		CanonicalURL: refreshed.CanonicalURL,
		Title:        "Photo post refreshed",
		LinksJSON:    refreshed.LinksJSON,
		NotePath:     refreshed.NotePath,
		RawJSON:      `{"refreshed":true}`,
		ImportedAt:   refreshed.ImportedAt,
		UpdatedAt:    time.Now().UTC().Add(time.Hour),
		LastSeenAt:   time.Now().UTC().Add(time.Hour),
	}
	later.ContentHash = itemhash.Compute(later)
	if _, err := st.UpsertItem(context.Background(), later); err != nil {
		t.Fatalf("UpsertItem refresh: %v", err)
	}

	refreshed, err = st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem after refresh: %v", err)
	}
	if refreshed.OCRStatus != "ok" || !strings.Contains(refreshed.OCRText, "Fallback OCR text") {
		t.Fatalf("expected OCR preserved after refresh, got status=%q text=%q", refreshed.OCRStatus, refreshed.OCRText)
	}
}

func TestRunCancellationDoesNotCountInterruptedOCRAsFailures(t *testing.T) {
	t.Parallel()

	cfg, st, item := seedDownloadedPhotoItem(t, "x:test-photo-cancel-ocr", "2049000000000000003")

	tesseract := filepath.Join(t.TempDir(), "tesseract")
	script := "#!/bin/sh\nsleep 5\nprintf 'late OCR text'\n"
	if err := os.WriteFile(tesseract, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tesseract: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	stats, err := Run(ctx, cfg, st, Options{
		Limit:           10,
		TesseractBinary: tesseract,
		Timeout:         10 * time.Second,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no counted OCR errors on cancel, got %+v", stats)
	}

	refreshed, getErr := st.GetItem(context.Background(), item.SourceKey)
	if getErr != nil {
		t.Fatalf("GetItem after cancel: %v", getErr)
	}
	if refreshed.OCRStatus != "" || refreshed.OCRText != "" {
		t.Fatalf("expected no OCR state persisted on cancel, got status=%q text=%q", refreshed.OCRStatus, refreshed.OCRText)
	}
}

func seedDownloadedPhotoItem(t *testing.T, sourceKey, externalID string) (config.Config, *store.Store, model.Item) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   externalID,
		CanonicalURL: "https://x.com/example/status/" + externalID,
		Title:        "Photo post",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/" + externalID + ".md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "Photo post body",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"photo","url":"https://pbs.twimg.com/media/test-photo.png","expanded_url":"https://x.com/example/status/` + externalID + `/photo/1","width":1200,"height":900}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one photo ref, got %#v", refs)
	}

	localRel := "media/x/photo/aa/" + externalID + ".png"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake png with text"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "image/png",
		ByteSize:     24,
		ContentHash:  "sha256:test-photo",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	item, err := st.GetItem(context.Background(), sourceKey)
	if err != nil {
		t.Fatalf("GetItem seeded item: %v", err)
	}

	return cfg, st, item
}

func installFakeTesseract(t *testing.T, stdout string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tesseract")
	script := "#!/bin/sh\nprintf '%s' " + shellQuote(stdout) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tesseract: %v", err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
