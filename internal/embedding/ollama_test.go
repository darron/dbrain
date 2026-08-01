package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOllamaEmbedSendsExactNativeRequestAndValidatesL2Response(t *testing.T) {
	t.Parallel()

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "" {
			t.Errorf("Authorization = %q; embedding calls must not resolve or send chat secrets", authorization)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"embedding-model:latest","embeddings":[[0.6,0.8],[0,1]]}`)
	}))
	defer server.Close()

	provider, err := NewOllama(OllamaOptions{BaseURL: server.URL, Model: "embedding-model:latest", Dimensions: 2})
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}
	req := Request{Texts: []string{"first", "second"}, Purpose: PurposeDocument}
	got, err := provider.Embed(t.Context(), req)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if body["model"] != "embedding-model:latest" || body["dimensions"] != float64(2) || body["truncate"] != false {
		t.Fatalf("request body = %#v", body)
	}
	if len(body) != 4 {
		t.Fatalf("request body has unexpected fields: %#v", body)
	}
	inputs, ok := body["input"].([]any)
	if !ok || len(inputs) != 2 || inputs[0] != "first" || inputs[1] != "second" {
		t.Fatalf("request input = %#v", body["input"])
	}
	if got.Provider != ProviderOllama || got.Model != "embedding-model:latest" || got.Dimensions != 2 {
		t.Fatalf("response provenance = %#v", got)
	}
	for i, vector := range got.Vectors {
		if err := ValidateDenseF32(vector, 2, NormalizationL2); err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
	}
	if math.Abs(float64(got.Vectors[0][0]-0.6)) > 1e-6 || math.Abs(float64(got.Vectors[0][1]-0.8)) > 1e-6 {
		t.Fatalf("normalized vector = %#v", got.Vectors[0])
	}
}

func TestOllamaUsesDedicatedClientWithoutProxyOrRedirects(t *testing.T) {
	t.Parallel()

	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusTemporaryRedirect))
	defer source.Close()

	provider, err := NewOllama(OllamaOptions{BaseURL: source.URL, Model: "model", Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := provider.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("transport = %#v; environment proxy routing must be disabled", provider.client.Transport)
	}
	if provider.client.Timeout != DefaultOllamaRequestTimeout {
		t.Fatalf("client timeout = %s, want %s", provider.client.Timeout, DefaultOllamaRequestTimeout)
	}
	_, err = provider.Embed(t.Context(), Request{Texts: []string{"text"}, Purpose: PurposeQuery})
	if !IsFatalConfig(err) {
		t.Fatalf("redirect error = %v, want fatal config", err)
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect destination received %d requests", redirected.Load())
	}
}

func TestOllamaClassifiesBoundedHTTPAndJSONFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		kind   FailureKind
	}{
		{"server", http.StatusServiceUnavailable, "temporarily unavailable", FailureRetryable},
		{"request timeout", http.StatusRequestTimeout, "request timed out", FailureRetryable},
		{"rate limit", http.StatusTooManyRequests, "slow down", FailureRetryable},
		{"input limit", http.StatusBadRequest, "input exceeds the context length", FailureBlocked},
		{"model missing", http.StatusNotFound, "model embedding-model not found", FailureFatalConfig},
		{"malformed JSON", http.StatusOK, "{", FailureRetryable},
		{"oversized JSON", http.StatusOK, strings.Repeat(" ", maxOllamaResponseBytes+1), FailureRetryable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			provider, err := NewOllama(OllamaOptions{BaseURL: server.URL, Model: "model", Dimensions: 2})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Embed(t.Context(), Request{Texts: []string{"text"}, Purpose: PurposeQuery})
			switch tt.kind {
			case FailureBlocked:
				if !IsBlocked(err) {
					t.Fatalf("error = %v, want blocked", err)
				}
			case FailureFatalConfig:
				if !IsFatalConfig(err) {
					t.Fatalf("error = %v, want fatal config", err)
				}
			case FailureRetryable:
				if !IsRetryable(err) {
					t.Fatalf("error = %v, want retryable", err)
				}
			}
			if len(err.Error()) > maxOllamaErrorBytes+1000 {
				t.Fatalf("error body was not bounded: %d bytes", len(err.Error()))
			}
		})
	}
}

func TestOllamaClassifiesNetworkFailureAsRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()
	provider, err := NewOllama(OllamaOptions{BaseURL: baseURL, Model: "model", Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Embed(t.Context(), Request{Texts: []string{"text"}, Purpose: PurposeQuery})
	if !IsRetryable(err) {
		t.Fatalf("network error = %v, want retryable", err)
	}
}

func TestOllamaRejectsResponseContractMismatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"model", `{"model":"other","embeddings":[[1,0]]}`, "model"},
		{"model whitespace", `{"model":" model ","embeddings":[[1,0]]}`, "model"},
		{"dimensions", `{"model":"model","embeddings":[[1,0,0]]}`, "dimensions"},
		{"cardinality", `{"model":"model","embeddings":[]}`, "count"},
		{"zero vector", `{"model":"model","embeddings":[[0,0]]}`, "zero norm"},
		{"non-unit vector", `{"model":"model","embeddings":[[3,4]]}`, "not unit length"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			provider, err := NewOllama(OllamaOptions{BaseURL: server.URL, Model: "model", Dimensions: 2})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Embed(t.Context(), Request{Texts: []string{"text"}, Purpose: PurposeQuery})
			if !IsFatalConfig(err) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want fatal config containing %q", err, tt.want)
			}
		})
	}
}

func TestOllamaHonorsCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()
	provider, err := NewOllama(OllamaOptions{BaseURL: server.URL, Model: "model", Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = provider.Embed(ctx, Request{Texts: []string{"text"}, Purpose: PurposeQuery})
	if !errors.Is(err, context.Canceled) || IsRetryable(err) {
		t.Fatalf("error = %v, want unwrapped caller cancellation", err)
	}
}

func TestOllamaClassifiesDeadlineAsRetryableAndPreservesContextIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()
	provider, err := NewOllama(OllamaOptions{BaseURL: server.URL, Model: "model", Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	_, err = provider.Embed(ctx, Request{Texts: []string{"text"}, Purpose: PurposeQuery})
	if !errors.Is(err, context.DeadlineExceeded) || !IsRetryable(err) {
		t.Fatalf("error = %v, want retryable deadline", err)
	}
}

func TestOllamaOwnedTimeoutStopsAcceptedStalledResponse(t *testing.T) {
	t.Parallel()

	var accepted atomic.Bool
	provider, err := NewOllama(OllamaOptions{
		BaseURL: "http://ollama.test",
		Model:   "model", Dimensions: 2, Timeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.client.Transport = ollamaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		accepted.Store(true)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       &ollamaStalledBody{ctx: request.Context()},
			Request:    request,
		}, nil
	})
	started := time.Now()
	_, err = provider.Embed(context.Background(), Request{Texts: []string{"text"}, Purpose: PurposeQuery})
	if !IsRetryable(err) {
		t.Fatalf("stalled response error = %v, want retryable", err)
	}
	if !accepted.Load() {
		t.Fatal("server never accepted the request")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("owned timeout took %s", elapsed)
	}
}

type ollamaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ollamaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type ollamaStalledBody struct {
	ctx context.Context
}

func (b *ollamaStalledBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*ollamaStalledBody) Close() error {
	return nil
}

func TestNewOllamaRejectsInvalidConfigurationAndEmbedRejectsInput(t *testing.T) {
	t.Parallel()

	for _, opts := range []OllamaOptions{
		{Model: "model", Dimensions: 2},
		{BaseURL: "http://127.0.0.1:11434", Dimensions: 2},
		{BaseURL: "http://127.0.0.1:11434", Model: "model"},
		{BaseURL: "http://127.0.0.1:11434", Model: "model", Dimensions: 2, Timeout: -time.Second},
		{BaseURL: "://bad", Model: "model", Dimensions: 2},
		{BaseURL: "http://user:secret@127.0.0.1:11434", Model: "model", Dimensions: 2},
	} {
		if _, err := NewOllama(opts); !IsFatalConfig(err) {
			t.Fatalf("NewOllama(%#v) error = %v, want fatal config", opts, err)
		}
	}
	provider, err := NewOllama(OllamaOptions{BaseURL: "http://127.0.0.1:11434", Model: "model", Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Embed(t.Context(), Request{}); !IsBlocked(err) {
		t.Fatalf("Embed invalid request error = %v, want blocked", err)
	}
}
