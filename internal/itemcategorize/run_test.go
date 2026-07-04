package itemcategorize

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestMergeUserTagsPreservesExistingAndDedupesGenerated(t *testing.T) {
	result := Result{
		Tags:       []string{"canada", "public-safety", "canada"},
		Categories: []string{"Canadian Politics", "public-safety"},
	}

	got := MergeUserTags("existing, canada\nlocal", result)
	want := "existing,canada,local,public-safety,Canadian Politics"
	if got != want {
		t.Fatalf("MergeUserTags() = %q, want %q", got, want)
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Stream        bool          `json:"stream"`
	Temperature   *float64      `json:"temperature,omitempty"`
	TopP          *float64      `json:"top_p,omitempty"`
	TopK          *int          `json:"top_k,omitempty"`
	RepeatPenalty *float64      `json:"repeat_penalty,omitempty"`
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Think    *bool           `json:"think,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

func TestCallOpenRouterSendsVersionedUserAgent(t *testing.T) {
	t.Parallel()

	var capturedUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUserAgent = r.Header.Get("User-Agent")
		if got := r.Header.Get("Authorization"); got != "Bearer test-openrouter-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	_, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", "google/gemini-test", Options{
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		UserAgent:      "dbrain/test-sha",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callOpenRouter: %v", err)
	}
	if capturedUserAgent != "dbrain/test-sha" {
		t.Fatalf("User-Agent = %q, want %q", capturedUserAgent, "dbrain/test-sha")
	}
}

func requireJSONNumberOption(t *testing.T, options map[string]any, key string, want float64) {
	t.Helper()
	got, ok := options[key].(float64)
	if !ok || got != want {
		t.Fatalf("options[%s] = %#v, want %v", key, options[key], want)
	}
}

func TestBuildSourceContentBundleIncludesSourceEvidence(t *testing.T) {
	t.Parallel()

	bundle := buildSourceContentBundle(model.SourceDocument{
		SourceKey:     "src:test-source",
		SourceType:    "web",
		CanonicalURL:  "https://example.com/article",
		Domain:        "example.com",
		SiteName:      "Example",
		Title:         "Source Title",
		Description:   "Source description.",
		SummaryText:   "Source summary.",
		ExtractedText: strings.Repeat("extract ", 20),
	})

	for _, want := range []string{
		"record_kind: source",
		"source_type: web",
		"url: https://example.com/article",
		"title: Source Title",
		"Description:\nSource description.",
		"Summary:\nSource summary.",
		"Extracted text:\nextract",
	} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("source bundle missing %q:\n%s", want, bundle)
		}
	}
}

func TestCallLMStudioTextCategorization(t *testing.T) {
	t.Parallel()

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-lmstudio-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"local-models\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:        "lmstudio/qwen/qwen3.6-35b-a3b",
		LMStudioBase: server.URL + "/v1",
		LMStudioKey:  "test-lmstudio-key",
		Timeout:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "qwen/qwen3.6-35b-a3b" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "lmstudio/qwen/qwen3.6-35b-a3b" {
		t.Fatalf("result model = %q", result.Model)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "local-models" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCallOMLXTextCategorization(t *testing.T) {
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"omlx\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	t.Setenv("DBRAIN_OMLX_BASE_URL", server.URL)
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:   "omlx/qwen3.5-coder",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "qwen3.5-coder" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "omlx/qwen3.5-coder" {
		t.Fatalf("result model = %q", result.Model)
	}
	if result.Provider != "omlx" || result.APIModel != "qwen3.5-coder" || result.Transport != "openai_chat_completions" || result.Tool != "omlx-direct" {
		t.Fatalf("unexpected result provenance: %+v", result)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "omlx" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBatchItemResultDurationPopulated(t *testing.T) {
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
	t.Cleanup(func() {
		_ = st.Close()
	})

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	inserted, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:duration-test",
		SourceType:   "x_bookmark",
		ExternalID:   "duration-test",
		CanonicalURL: "https://x.com/example/status/duration-test",
		Title:        "Duration test",
		XPostText:    "An AI infrastructure note.",
		ContentHash:  "duration-test-hash",
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if inserted.ItemID == 0 {
		t.Fatal("expected item id")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"duration\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("DBRAIN_OMLX_BASE_URL", server.URL)
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	_, results, err := Batch(context.Background(), cfg, st, Options{
		Model:   "omlx/qwen3.5-coder",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Duration <= 0 {
		t.Fatalf("Duration = %s, want positive", results[0].Duration)
	}
}

func TestBatchSourceResultDurationPopulated(t *testing.T) {
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
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	upsert, err := st.UpsertSource(ctx, model.SourceCandidate{
		OriginalURL:   "https://example.com/duration-source",
		CanonicalURL:  "https://example.com/duration-source",
		NormalizedURL: "https://example.com/duration-source",
		SourceType:    "article",
		Domain:        "example.com",
		SourceKey:     "src:duration-source",
		NotePath:      "sources/article/duration-source.md",
	})
	if err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, upsert.SourceID, model.ExtractResult{
		FinalURL:    "https://example.com/duration-source",
		Title:       "Duration Source",
		Content:     "A source about local LLM timing and categorization.",
		Status:      model.SourceExtractStatusOK,
		FetchedAt:   time.Now().UTC(),
		Tool:        "test",
		ToolVersion: "test",
	}, "duration-source-hash"); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"duration\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("DBRAIN_OMLX_BASE_URL", server.URL)
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	_, results, err := BatchSources(ctx, cfg, st, Options{
		Model:   "omlx/qwen3.5-coder",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("BatchSources: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Duration <= 0 {
		t.Fatalf("Duration = %s, want positive", results[0].Duration)
	}
}

func TestRunSendsImagesForOMLXProvider(t *testing.T) {
	// Uses t.Setenv below, so this test must remain serial.
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
	t.Cleanup(func() {
		_ = st.Close()
	})

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	item := model.Item{
		SourceKey:    "x:test-omlx-photo",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Photo-backed post",
		XPostText:    "This post has text and an attached photo.",
		ContentHash:  "x:test-omlx-photo-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/123.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upsert, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	item.ID = upsert.ItemID
	if _, err := st.SaveXHydration(context.Background(), item.ID, model.XHydration{
		FullText:  item.XPostText,
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"https://pbs.twimg.com/media/test.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	localPath := "media/x/photo/test-omlx-photo.jpg"
	if err := os.MkdirAll(filepath.Join(cfg.VaultDir, "media", "x", "photo"), 0o755); err != nil {
		t.Fatalf("mkdir media path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath)), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatalf("write local photo: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "image/jpeg",
		ByteSize:     int64(len("jpeg-bytes")),
		ContentHash:  "photo-hash",
		LocalPath:    localPath,
		Status:       model.MediaDownloadStatusDownloaded,
		AttemptedAt:  now,
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"omlx\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("DBRAIN_OMLX_BASE_URL", server.URL)
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	result, err := Run(context.Background(), cfg, st, item, Options{
		Model:         "omlx/qwen3.5-coder",
		IncludeImages: true,
		Timeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Model != "omlx/qwen3.5-coder" {
		t.Fatalf("result model = %q", result.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %+v", captured.Messages)
	}
	if !hasTextPart(captured.Messages[1].Content) {
		t.Fatalf("expected oMLX user content to include text part, got %#v", captured.Messages[1].Content)
	}
	if !hasImageURLPart(captured.Messages[1].Content) {
		t.Fatalf("expected oMLX user content to include image_url part, got %#v", captured.Messages[1].Content)
	}
}

func TestRunIncludesLinkedSourceEvidence(t *testing.T) {
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
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	now := time.Date(2026, 7, 3, 18, 48, 0, 0, time.UTC)
	item := model.Item{
		SourceKey:    "x:test-linked-article",
		SourceType:   "x_bookmark",
		ExternalID:   "2073100352921215386",
		CanonicalURL: "https://x.com/example/status/2073100352921215386",
		Title:        "x.com/i/article/2073090223194755072",
		XPostText:    "https://t.co/hPiZr1kG7r",
		ContentHash:  "x:test-linked-article-hash",
		LinksJSON:    `[{"url":"https://x.com/i/article/2073090223194755072"}]`,
		NotePath:     "items/x/2026/2073100352921215386.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upsert, err := st.UpsertItem(ctx, item)
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	item.ID = upsert.ItemID

	link, err := st.UpsertSourceLink(ctx, item.ID, model.SourceCandidate{
		OriginalURL:   "https://x.com/i/article/2073090223194755072",
		CanonicalURL:  "https://x.com/i/article/2073090223194755072",
		NormalizedURL: "https://x.com/i/article/2073090223194755072",
		SourceType:    "x_article",
		Domain:        "x.com",
		SourceKey:     "src:test-linked-article",
		NotePath:      "sources/x_article/test-linked-article.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, link.SourceID, model.ExtractResult{
		FinalURL:    "https://x.com/i/article/2073090223194755072",
		Title:       "A Field Guide to Fable: Finding Your Unknowns",
		Description: "A technical article about using Fable to build a second brain.",
		SiteName:    "X Articles",
		Content:     "Fable article body about second-brain workflows, unknown unknowns, and local knowledge tools.",
		Status:      model.SourceExtractStatusOK,
		FetchedAt:   now.Add(time.Minute),
		Tool:        "x-article",
		ToolVersion: "test",
	}, "source-hash"); err != nil {
		t.Fatalf("SaveSourceExtraction: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Text:          "Fable helps identify unknowns in a second-brain system by turning local evidence into structured research context.",
		Model:         "ollama/dbrain:test",
		PromptVersion: "test",
		Status:        model.SourceSummaryStatusOK,
		FetchedAt:     now.Add(2 * time.Minute),
		Tool:          "source-summary",
		ToolVersion:   "test",
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"fable\",\"second-brain\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	_, err = Run(ctx, cfg, st, item, Options{
		Model:          "openrouter/google/gemini-test",
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(captured.Messages) < 2 {
		t.Fatalf("messages = %+v", captured.Messages)
	}
	userText := messageText(captured.Messages[1].Content)
	for _, want := range []string{
		"Linked source evidence",
		"A Field Guide to Fable: Finding Your Unknowns",
		"Fable helps identify unknowns",
		"second-brain workflows",
	} {
		if !strings.Contains(userText, want) {
			t.Fatalf("expected user prompt to include %q, got:\n%s", want, userText)
		}
	}
}

func TestCallConfiguredAliasTextCategorization(t *testing.T) {
	root := t.TempDir()
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"localai\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    base_url: `+server.URL+`/v1
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		RootDir: root,
		Model:   "localai/test-model",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "test-model" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "localai/test-model" {
		t.Fatalf("result model = %q", result.Model)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "localai" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCallConfiguredAliasRegistryErrorDoesNotFallbackToOpenRouter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
llm_backends:
  localai:
    transport: openai_chat_completions
    local: true
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := callLLM(context.Background(), "content bundle", nil, Options{
		RootDir:       root,
		Model:         "localai/test-model",
		OpenRouterKey: "should-not-fallback",
		Timeout:       2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "llm_backends.localai base_url is required") {
		t.Fatalf("expected alias config error, got %v", err)
	}
}

func TestCallLLMPlainModelStillRoutesOpenRouter(t *testing.T) {
	t.Parallel()

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"hosted\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:          "google/gemini-test",
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if captured.Model != "google/gemini-test" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if result.Model != "google/gemini-test" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestCallLLMOpenRouterImagesStillSendImageParts(t *testing.T) {
	t.Parallel()

	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"vision\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	_, err := callLLM(context.Background(), "content bundle", [][]byte{{1, 2, 3}}, Options{
		Model:          "openrouter/google/gemini-test",
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %+v", captured.Messages)
	}
	if !hasImageURLPart(captured.Messages[1].Content) {
		t.Fatalf("expected image_url part, got %#v", captured.Messages[1].Content)
	}
}

func TestCallLLMOMLXImagesSendImageParts(t *testing.T) {
	// Uses t.Setenv below, so this test must remain serial.
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"omlx-vision\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()
	t.Setenv("DBRAIN_OMLX_BASE_URL", server.URL)
	t.Setenv("DBRAIN_OMLX_API_KEY", "")

	result, err := callLLM(context.Background(), "content bundle", [][]byte{{1, 2, 3}}, Options{
		Model:   "omlx/qwen3.5-coder",
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if result.Model != "omlx/qwen3.5-coder" {
		t.Fatalf("result model = %q", result.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %+v", captured.Messages)
	}
	if !hasTextPart(captured.Messages[1].Content) {
		t.Fatalf("expected oMLX text part, got %#v", captured.Messages[1].Content)
	}
	if !hasImageURLPart(captured.Messages[1].Content) {
		t.Fatalf("expected oMLX image_url part, got %#v", captured.Messages[1].Content)
	}
}

func hasTextPart(content any) bool {
	return hasPartType(content, "text")
}

func hasImageURLPart(content any) bool {
	return hasPartType(content, "image_url")
}

func messageText(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, part := range typed {
			if m, ok := part.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func hasPartType(content any, partType string) bool {
	parts, ok := content.([]any)
	if !ok {
		return false
	}
	for _, part := range parts {
		m, ok := part.(map[string]any)
		if ok && m["type"] == partType {
			return true
		}
	}
	return false
}

func TestProviderOverridesExplicitValuesWin(t *testing.T) {
	t.Parallel()

	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("legacy OpenRouter endpoint should not be used")
	}))
	defer legacy.Close()

	var capturedAuth string
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"override\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer override.Close()

	result, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:          "openrouter/google/gemini-test",
		OpenRouterBase: legacy.URL,
		OpenRouterKey:  "legacy-key",
		ProviderOverrides: map[llmprovider.Provider]llmprovider.ProviderOverrides{
			llmprovider.ProviderOpenRouter: {
				BaseURL: override.URL,
				APIKey:  "override-key",
			},
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callLLM: %v", err)
	}
	if capturedAuth != "Bearer override-key" {
		t.Fatalf("auth = %q", capturedAuth)
	}
	if result.PrimaryCategory != "ai" || len(result.Tags) != 1 || result.Tags[0] != "override" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCallLMStudioRejectsImages(t *testing.T) {
	t.Parallel()

	_, err := callLLM(context.Background(), "content bundle", [][]byte{{1, 2, 3}}, Options{
		Model:        "lmstudio/qwen/qwen3.6-35b-a3b",
		LMStudioBase: "http://127.0.0.1:1234/v1",
		LMStudioKey:  "lm-studio",
		Timeout:      2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "lmstudio categorization with images is not supported") {
		t.Fatalf("expected unsupported image error, got %v", err)
	}
}

func TestCallLLMRejectsEmptyLMStudioModel(t *testing.T) {
	t.Parallel()

	_, err := callLLM(context.Background(), "content bundle", nil, Options{
		Model:         "lmstudio/",
		OpenRouterKey: "should-not-fallback",
		Timeout:       2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported LM Studio model "lmstudio/"`) {
		t.Fatalf("expected unsupported LM Studio model error, got %v", err)
	}
}

func TestResolveOptsLoadsLMStudioConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(`
lmstudio:
  base_url: http://10.0.0.7:1234
  api_key: studio-key
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBRAIN_LMSTUDIO_BASE_URL", "")
	t.Setenv("DBRAIN_LMSTUDIO_API_KEY", "")

	opts := Options{Model: "lmstudio/qwen/qwen3.6-35b-a3b"}
	if err := resolveOpts(context.Background(), config.Config{
		RootDir:        root,
		CategoriesPath: filepath.Join(root, "categories.yaml"),
	}, &opts); err != nil {
		t.Fatalf("resolveOpts: %v", err)
	}
	if opts.LMStudioBase != "http://10.0.0.7:1234/v1" {
		t.Fatalf("LMStudioBase = %q", opts.LMStudioBase)
	}
	if opts.LMStudioKey != "studio-key" {
		t.Fatalf("LMStudioKey = %q", opts.LMStudioKey)
	}
}

func TestCategorizationPreservesProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	result, err := parseCategorizationJSON(`{"categories":["ai"],"tags":["agents"],"primary_category":"ai"}`, "ollama/dbrain:2026042701", categoryvocab.Vocab{})
	if err != nil {
		t.Fatalf("parseCategorizationJSON: %v", err)
	}
	if result.Model != "ollama/dbrain:2026042701" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestCallOpenRouterPreservesProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}]}`))
	}))
	defer server.Close()

	result, err := callOpenRouter(context.Background(), "content bundle", nil, "google/gemini-test", "openrouter/google/gemini-test", Options{
		OpenRouterBase: server.URL,
		OpenRouterKey:  "test-openrouter-key",
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("callOpenRouter: %v", err)
	}
	if result.Model != "openrouter/google/gemini-test" {
		t.Fatalf("result model = %q", result.Model)
	}
}

func TestCallOllamaPreservesProviderQualifiedModel(t *testing.T) {
	t.Parallel()

	var captured ollamaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"categories\":[\"ai\"],\"tags\":[\"agents\"],\"primary_category\":\"ai\"}"}}`))
	}))
	defer server.Close()

	result, err := callOllama(context.Background(), "content bundle", nil, "dbrain:2026042701", "ollama/dbrain:2026042701", Options{
		OllamaBase: server.URL,
		OllamaKey:  "ollama",
		Timeout:    2 * time.Second,
		InferenceParams: llmprovider.DbrainParityForProvider(
			llmprovider.ProviderOllama,
		),
	})
	if err != nil {
		t.Fatalf("callOllama: %v", err)
	}
	if result.Model != "ollama/dbrain:2026042701" {
		t.Fatalf("result model = %q", result.Model)
	}
	if len(captured.Options) != 5 {
		t.Fatalf("expected all Modelfile options, got %#v", captured.Options)
	}
	requireJSONNumberOption(t, captured.Options, "temperature", 0.6)
	requireJSONNumberOption(t, captured.Options, "top_p", 0.95)
	requireJSONNumberOption(t, captured.Options, "top_k", 20)
	requireJSONNumberOption(t, captured.Options, "min_p", 0)
	requireJSONNumberOption(t, captured.Options, "repeat_penalty", 1)
}

func TestNormalizeChatCompletionsBaseAddsV1(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"http://127.0.0.1:1234":    "http://127.0.0.1:1234/v1",
		"http://127.0.0.1:1234/v1": "http://127.0.0.1:1234/v1",
		"127.0.0.1:1234":           "http://127.0.0.1:1234/v1",
		"":                         defaultLMStudioBase,
	}
	for input, want := range tests {
		if got := normalizeChatCompletionsBase(input, "/v1", defaultLMStudioBase); got != want {
			t.Fatalf("normalizeChatCompletionsBase(%q) = %q, want %q", input, got, want)
		}
	}
}
