package xphotoocr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func TestCompareRunsModelsWithoutPersistingOCR(t *testing.T) {
	cfg, st, item := seedDownloadedPhotoItem(t, "x:test-photo-compare-ocr", "2049000000000000201")

	openRouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected openrouter path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"google/gemini-test","choices":[{"message":{"content":"Alpha Beta Gamma"}}]}`))
	}))
	defer openRouter.Close()

	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected ollama path: %s", r.URL.Path)
		}
		var captured ollamaOCRRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode ollama request: %v", err)
		}
		if captured.Model != "deepseek-ocr:3b" {
			t.Fatalf("unexpected ollama model: %q", captured.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"deepseek-ocr:3b","message":{"role":"assistant","content":"Alpha Beta Delta"}}`))
	}))
	defer ollama.Close()

	result, err := Compare(context.Background(), cfg, st, CompareOptions{
		Limit:          5,
		Models:         []string{"openrouter/google/gemini-test", "ollama/deepseek-ocr:3b"},
		OpenRouterBase: openRouter.URL,
		OpenRouterKey:  "test-key",
		OllamaBase:     ollama.URL,
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if result.SchemaVersion != CompareSchemaVersion || len(result.Images) != 1 || len(result.Images[0].Runs) != 2 {
		t.Fatalf("unexpected result shape: %+v", result)
	}
	if result.Summary[0].OK != 1 || result.Summary[1].OK != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if result.Images[0].Runs[1].BaselineWordOverlap <= 0 {
		t.Fatalf("expected candidate overlap against baseline: %+v", result.Images[0].Runs[1])
	}

	refreshed, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if refreshed.OCRStatus != "" || refreshed.OCRText != "" {
		t.Fatalf("compare should not persist OCR state, got status=%q text=%q", refreshed.OCRStatus, refreshed.OCRText)
	}

	report := RenderCompareMarkdown(result, 1000)
	if !strings.Contains(report, "Alpha Beta Gamma") || !strings.Contains(report, "Alpha Beta Delta") {
		t.Fatalf("expected both model outputs in report:\n%s", report)
	}
}

func TestCompareRunsFrankenOCRWithoutPersistingOCR(t *testing.T) {
	cfg, st, item := seedDownloadedPhotoItem(t, "x:test-photo-compare-franken-ocr", "2049000000000000202")
	focr := installFakeFOCR(t, "Compare Franken OCR text.\n", "")

	result, err := Compare(context.Background(), cfg, st, CompareOptions{
		Limit:      5,
		Models:     []string{"focr/default"},
		FOCRBinary: focr,
		Timeout:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if result.SchemaVersion != CompareSchemaVersion || len(result.Images) != 1 || len(result.Images[0].Runs) != 1 {
		t.Fatalf("unexpected result shape: %+v", result)
	}
	run := result.Images[0].Runs[0]
	if run.Status != "ok" || run.Tool != frankenOCRTool || run.ReportedModel != "focr/default" {
		t.Fatalf("unexpected Franken OCR run: %+v", run)
	}
	if !strings.Contains(run.Text, "Compare Franken OCR text") {
		t.Fatalf("expected Franken OCR text, got %q", run.Text)
	}

	refreshed, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if refreshed.OCRStatus != "" || refreshed.OCRText != "" {
		t.Fatalf("compare should not persist OCR state, got status=%q text=%q", refreshed.OCRStatus, refreshed.OCRText)
	}
}

func TestComparePhotoInputPathDownloadsMissingTempFile(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/image.png" {
			t.Fatalf("unexpected image path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("image bytes"))
	}))
	defer server.Close()

	path, source, cleanup, err := comparePhotoInputPath(context.Background(), cfg, CompareOptions{
		DownloadMissing: true,
		Timeout:         2 * time.Second,
	}, model.ItemMediaRef{
		MediaType:      "photo",
		DownloadStatus: "downloaded",
		LocalPath:      "media/x/photo/missing.png",
		RemoteURL:      server.URL + "/image.png",
	})
	if err != nil {
		t.Fatalf("comparePhotoInputPath: %v", err)
	}
	if source != "temp_download" {
		t.Fatalf("expected temp_download source, got %q", source)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp image: %v", err)
	}
	if string(data) != "image bytes" {
		t.Fatalf("unexpected temp image: %q", string(data))
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected temp image cleanup, got %v", err)
	}
}
