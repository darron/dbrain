package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	ProviderOllama         = "ollama"
	maxOllamaResponseBytes = 8 << 20
	maxOllamaErrorBytes    = 64 << 10
)

type OllamaOptions struct {
	BaseURL    string
	Model      string
	Dimensions int
}

type Ollama struct {
	baseURL string
	info    Info
	client  *http.Client
}

type ollamaEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions"`
	Truncate   bool     `json:"truncate"`
}

type ollamaEmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

func NewOllama(opts OllamaOptions) (*Ollama, error) {
	baseURL, err := normalizeOllamaEndpoint(opts.BaseURL)
	if err != nil {
		return nil, FatalConfigError(err)
	}
	info := Info{Provider: ProviderOllama, Model: strings.TrimSpace(opts.Model), Dimensions: opts.Dimensions}
	if err := info.Validate(); err != nil {
		return nil, FatalConfigError(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Ollama{baseURL: baseURL, info: info, client: client}, nil
}

func (o *Ollama) Info() Info {
	if o == nil {
		return Info{}
	}
	return o.info
}

func (o *Ollama) Embed(ctx context.Context, req Request) (Response, error) {
	if o == nil {
		return Response{}, FatalConfigError(fmt.Errorf("Ollama embedding provider is nil"))
	}
	if err := ValidateRequest(req); err != nil {
		return Response{}, BlockedError(err)
	}
	if err := ctx.Err(); err != nil {
		return Response{}, contextFailure(err)
	}
	body, err := json.Marshal(ollamaEmbedRequest{
		Model: o.info.Model, Input: req.Texts, Dimensions: o.info.Dimensions, Truncate: false,
	})
	if err != nil {
		return Response{}, BlockedError(fmt.Errorf("encode Ollama embedding request: %w", err))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return Response{}, FatalConfigError(fmt.Errorf("build Ollama embedding request: %w", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResponse, err := o.client.Do(httpReq)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Response{}, contextFailure(contextErr)
		}
		return Response{}, RetryableError(fmt.Errorf("call Ollama embedding endpoint: %w", err))
	}
	defer func() { _ = httpResponse.Body.Close() }()
	responseBody, oversized, err := readBounded(httpResponse.Body, maxOllamaResponseBytes)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return Response{}, contextFailure(contextErr)
		}
		return Response{}, RetryableError(fmt.Errorf("read Ollama embedding response: %w", err))
	}
	if oversized {
		return Response{}, RetryableError(fmt.Errorf("Ollama embedding response exceeds %d bytes", maxOllamaResponseBytes))
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return Response{}, classifyOllamaHTTPError(httpResponse.StatusCode, responseBody)
	}

	var decoded ollamaEmbedResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return Response{}, RetryableError(fmt.Errorf("decode Ollama embedding response: %w", err))
	}
	response := Response{
		Vectors: decoded.Embeddings, Provider: ProviderOllama,
		Model: decoded.Model, Dimensions: o.info.Dimensions,
	}
	if err := ValidateResponse(o.info, req, response); err != nil {
		return Response{}, FatalConfigError(err)
	}
	for i, vector := range response.Vectors {
		if err := ValidateDenseF32(vector, o.info.Dimensions, NormalizationL2); err != nil {
			return Response{}, FatalConfigError(fmt.Errorf("invalid Ollama L2 embedding vector %d: %w", i, err))
		}
	}
	return response, nil
}

func normalizeOllamaEndpoint(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", fmt.Errorf("Ollama base URL is required")
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimSuffix(value, "/v1")
	value = strings.TrimSuffix(value, "/api")
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse Ollama base URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid Ollama HTTP endpoint %q", raw)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func classifyOllamaHTTPError(status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	if len(message) > maxOllamaErrorBytes {
		message = message[:maxOllamaErrorBytes] + "..."
	}
	if message == "" {
		message = http.StatusText(status)
	}
	err := fmt.Errorf("Ollama embedding endpoint returned HTTP %d: %s", status, message)
	if isInputLimitMessage(message) {
		return BlockedError(err)
	}
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
		return RetryableError(err)
	}
	return FatalConfigError(err)
}

func isInputLimitMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, fragment := range []string{
		"context length", "context window", "input too long", "input exceeds", "maximum context", "too many tokens",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func contextFailure(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return RetryableError(err)
}
