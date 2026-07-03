package llmclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
)

func TestChatOpenAICompatibleLocalOMLXOmitsAuthorization(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	var capturedPath string
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3.5-coder","choices":[{"message":{"content":"local response"}}]}`))
	}))
	defer server.Close()

	resp, err := Chat(context.Background(), Request{
		Model: "omlx/qwen3.5-coder",
		Messages: []Message{
			SystemMessage("system prompt"),
			UserTextMessage("body text"),
		},
		Timeout: 2 * time.Second,
		Task:    llmprovider.TaskSummary,
		Env:     map[string]string{"DBRAIN_OMLX_BASE_URL": server.URL + "/v1"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedAuth != "" {
		t.Fatalf("Authorization = %q, want empty", capturedAuth)
	}
	if capturedPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", capturedPath)
	}
	if captured.Model != "qwen3.5-coder" {
		t.Fatalf("model = %q", captured.Model)
	}
	if resp.Text != "local response" || resp.Model != "omlx/qwen3.5-coder" || resp.APIModel != "qwen3.5-coder" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Tool != llmprovider.ToolOMLXDirect || resp.ToolVersion != llmprovider.ToolVersionOMLXDirect {
		t.Fatalf("tool = %s/%s", resp.Tool, resp.ToolVersion)
	}
}

func TestChatOllamaSendsNativeOptionsAndDisablesThinking(t *testing.T) {
	t.Parallel()

	var captured ollamaChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"model":"dbrain:2026042701","message":{"content":"ollama response"}}`))
	}))
	defer server.Close()

	resp, err := Chat(context.Background(), Request{
		Model:         "ollama/dbrain:2026042701",
		Messages:      []Message{UserTextMessage("body text")},
		SamplerParams: map[string]any{"temperature": 0.6, "top_p": 0.95},
		Timeout:       2 * time.Second,
		Task:          llmprovider.TaskSummary,
		Env:           map[string]string{"DBRAIN_OLLAMA_BASE_URL": server.URL},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if captured.Think == nil || *captured.Think {
		t.Fatalf("expected think=false, got %#v", captured.Think)
	}
	if captured.Options["temperature"] != float64(0.6) || captured.Options["top_p"] != float64(0.95) {
		t.Fatalf("options = %#v", captured.Options)
	}
	if resp.Transport != llmprovider.TransportOllamaChat {
		t.Fatalf("transport = %q", resp.Transport)
	}
}

func TestChatOpenRouterSendsRequiredAuthAndCategorizeHeaders(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	var capturedReferer string
	var capturedTitle string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedReferer = r.Header.Get("HTTP-Referer")
		capturedTitle = r.Header.Get("X-Title")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"router response"}}]}`))
	}))
	defer server.Close()

	_, err := Chat(context.Background(), Request{
		Model:    "openrouter/google/gemini-test",
		Messages: []Message{UserTextMessage("body text")},
		Timeout:  2 * time.Second,
		Task:     llmprovider.TaskCategorize,
		Env: map[string]string{
			"DBRAIN_OPENROUTER_BASE_URL": server.URL,
			"DBRAIN_OPENROUTER_API_KEY":  "router-key",
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if capturedAuth != "Bearer router-key" {
		t.Fatalf("Authorization = %q", capturedAuth)
	}
	if capturedReferer != "https://local.dbrain" || capturedTitle != "dbrain categorize" {
		t.Fatalf("OpenRouter headers = referer %q title %q", capturedReferer, capturedTitle)
	}
}

func TestChatConfiguredAliasUsesConfiguredEndpoint(t *testing.T) {
	root := t.TempDir()
	writeClientConfig(t, root, `
llm_backends:
  localai:
    transport: openai_chat_completions
    base_url: http://127.0.0.1:8080/v1
    api_key: ""
    local: true
`)
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"alias response"}}]}`))
	}))
	defer server.Close()

	resp, err := Chat(context.Background(), Request{
		RootDir:  root,
		Model:    "localai/test-model",
		Messages: []Message{UserTextMessage("body text")},
		Timeout:  2 * time.Second,
		ProviderOverrides: map[llmprovider.Provider]llmprovider.ProviderOverrides{
			llmprovider.Provider("localai"): {BaseURL: server.URL + "/v1"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if captured.Model != "test-model" {
		t.Fatalf("captured model = %q", captured.Model)
	}
	if resp.Model != "localai/test-model" || resp.Provider != llmprovider.Provider("localai") {
		t.Fatalf("response = %+v", resp)
	}
}

func TestChatRejectsImagesForTextOnlyProvider(t *testing.T) {
	t.Parallel()

	_, err := Chat(context.Background(), Request{
		Model: "lmstudio/qwen/qwen3.6-35b-a3b",
		Messages: []Message{{
			Role: "user",
			Parts: []ContentPart{
				{Type: ContentText, Text: "caption this"},
				{Type: ContentImage, ImageData: []byte{1, 2, 3}, MIMEType: "image/jpeg"},
			},
		}},
		Timeout: time.Second,
		Task:    llmprovider.TaskCategorize,
	})
	if err == nil || !strings.Contains(err.Error(), "LM Studio") || !strings.Contains(err.Error(), "images") {
		t.Fatalf("expected image capability error, got %v", err)
	}
}

func TestChatOMLXAllowsImages(t *testing.T) {
	t.Parallel()

	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"qwen3.5-coder","choices":[{"message":{"content":"vision response"}}]}`))
	}))
	defer server.Close()

	resp, err := Chat(context.Background(), Request{
		Model: "omlx/qwen3.5-coder",
		Messages: []Message{{
			Role: "user",
			Parts: []ContentPart{
				{Type: ContentText, Text: "caption this"},
				{Type: ContentImage, ImageData: []byte{1, 2, 3}, MIMEType: "image/jpeg"},
			},
		}},
		Timeout: time.Second,
		Task:    llmprovider.TaskCategorize,
		Env:     map[string]string{"DBRAIN_OMLX_BASE_URL": server.URL + "/v1"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "vision response" {
		t.Fatalf("response = %+v", resp)
	}
	if !openAIRequestHasImageURL(captured) {
		t.Fatalf("expected image_url part in request: %+v", captured.Messages)
	}
}

func openAIRequestHasImageURL(req openAIChatRequest) bool {
	for _, message := range req.Messages {
		parts, ok := message.Content.([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			m, ok := part.(map[string]any)
			if ok && m["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

func TestChatOpenAICompatibleNoChoicesError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	_, err := Chat(context.Background(), Request{
		Model:    "omlx/qwen3.5-coder",
		Messages: []Message{UserTextMessage("body text")},
		Timeout:  2 * time.Second,
		Env:      map[string]string{"DBRAIN_OMLX_BASE_URL": server.URL + "/v1"},
	})
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected no choices error, got %v", err)
	}
}

func TestChatOpenAICompatibleEmptyContentError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"   "}}]}`))
	}))
	defer server.Close()

	_, err := Chat(context.Background(), Request{
		Model:    "omlx/qwen3.5-coder",
		Messages: []Message{UserTextMessage("body text")},
		Timeout:  2 * time.Second,
		Env:      map[string]string{"DBRAIN_OMLX_BASE_URL": server.URL + "/v1"},
	})
	if err == nil || !strings.Contains(err.Error(), "no content") {
		t.Fatalf("expected empty content error, got %v", err)
	}
}

func TestChatTimeoutUsesRequestContext(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	start := time.Now()
	_, err := Chat(context.Background(), Request{
		Model:      "omlx/qwen3.5-coder",
		Messages:   []Message{UserTextMessage("body text")},
		Timeout:    50 * time.Millisecond,
		HTTPClient: client,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestChatFallsBackToHTTPDefaultClient(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"default client response"}}]}`)),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	resp, err := Chat(context.Background(), Request{
		Model:    "omlx/qwen3.5-coder",
		Messages: []Message{UserTextMessage("body text")},
		Timeout:  2 * time.Second,
		Env:      map[string]string{"DBRAIN_OMLX_BASE_URL": "http://default-client.test/v1"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "default client response" {
		t.Fatalf("Text = %q", resp.Text)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func writeClientConfig(t *testing.T, root string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config.yaml: %v", err)
	}
}
