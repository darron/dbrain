package itemcategorize

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/version"
)

func effectiveSystemPrompt(opts Options) string {
	vocab := opts.Vocab.PromptSection()
	if vocab == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + vocab
}

func callLLM(ctx context.Context, bundle string, photoData [][]byte, opts Options) (Result, error) {
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		return callOllama(ctx, bundle, photoData, ollamaModel, opts)
	}
	if openrouterModel, ok := parseOpenRouterModel(opts.Model); ok {
		return callOpenRouter(ctx, bundle, photoData, openrouterModel, opts)
	}
	// Fall back: treat the model as a plain OpenRouter model name.
	return callOpenRouter(ctx, bundle, photoData, opts.Model, opts)
}

func callOllama(ctx context.Context, bundle string, photoData [][]byte, ollamaModel string, opts Options) (Result, error) {
	think := false
	sysMsg := ollamaMessage{Role: "system", Content: effectiveSystemPrompt(opts)}
	userMsg := ollamaMessage{Role: "user", Content: bundle}
	for _, data := range photoData {
		userMsg.Images = append(userMsg.Images, base64.StdEncoding.EncodeToString(data))
	}

	reqBody := ollamaRequest{
		Model:    ollamaModel,
		Messages: []ollamaMessage{sysMsg, userMsg},
		Stream:   false,
		Think:    &think,
	}

	endpoint := strings.TrimRight(opts.OllamaBase, "/") + "/api/chat"
	raw, err := doPost(ctx, endpoint, opts.OllamaKey, nil, reqBody, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("ollama categorize: %w", err)
	}

	var resp ollamaResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Result{}, fmt.Errorf("parse ollama response: %w", err)
	}

	return parseCategorizationJSON(resp.Message.Content, ollamaModel, opts.Vocab)
}

func callOpenRouter(ctx context.Context, bundle string, photoData [][]byte, openrouterModel string, opts Options) (Result, error) {
	if strings.TrimSpace(opts.OpenRouterKey) == "" {
		return Result{}, fmt.Errorf("OPENROUTER_API_KEY / DBRAIN_OPENROUTER_API_KEY not set")
	}

	sysMsg := chatMessage{Role: "system", Content: effectiveSystemPrompt(opts)}

	var userContent any
	if len(photoData) > 0 {
		parts := []contentPart{{Type: "text", Text: bundle}}
		for _, data := range photoData {
			dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
			parts = append(parts, contentPart{
				Type:     "image_url",
				ImageURL: &imageURL{URL: dataURL},
			})
		}
		userContent = parts
	} else {
		userContent = bundle
	}

	reqBody := chatRequest{
		Model:    openrouterModel,
		Messages: []chatMessage{sysMsg, {Role: "user", Content: userContent}},
		Stream:   false,
	}

	endpoint := strings.TrimRight(opts.OpenRouterBase, "/") + "/chat/completions"
	headers := map[string]string{}
	if opts.OpenRouterRef != "" {
		headers["HTTP-Referer"] = opts.OpenRouterRef
	}
	if opts.OpenRouterTitle != "" {
		headers["X-Title"] = opts.OpenRouterTitle
	}
	headers["User-Agent"] = version.UserAgent(opts.UserAgent)

	raw, err := doPost(ctx, endpoint, opts.OpenRouterKey, headers, reqBody, opts.Timeout)
	if err != nil {
		return Result{}, fmt.Errorf("openrouter categorize: %w", err)
	}

	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Result{}, fmt.Errorf("parse openrouter response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("openrouter categorize: no choices returned")
	}

	return parseCategorizationJSON(resp.Choices[0].Message.Content, openrouterModel, opts.Vocab)
}

func doPost(ctx context.Context, endpoint, apiKey string, headers map[string]string, body any, timeout time.Duration) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := strings.TrimSpace(string(raw))
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, preview)
	}

	return raw, nil
}

// parseCategorizationJSON strips optional markdown fences, parses the JSON,
// and applies canonical vocab rules if provided.
func parseCategorizationJSON(content string, modelName string, vocab categoryvocab.Vocab) (Result, error) {
	text := strings.TrimSpace(content)

	// Strip markdown code fences if the model wrapped the output
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		end := strings.LastIndex(text, "```")
		if end > start {
			text = strings.TrimSpace(text[start:end])
		}
	}

	// Find the JSON object boundaries (in case there's leading/trailing prose)
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if j := strings.LastIndex(text, "}"); j >= 0 && j < len(text)-1 {
		text = text[:j+1]
	}

	var result Result
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return Result{}, fmt.Errorf("parse categorization JSON: %w (content: %q)", err, truncate(content, 300))
	}

	// Normalise and apply vocab to tags, categories, and primary_category.
	// ApplyToTokens always normalises to lowercase-hyphenated form, then
	// applies alias resolution and drops.
	result.Tags = vocab.ApplyToTokens(result.Tags)
	result.Categories = vocab.ApplyToTokens(result.Categories)
	if pc := vocab.ApplyToTokens([]string{result.PrimaryCategory}); len(pc) > 0 {
		result.PrimaryCategory = pc[0]
	} else {
		result.PrimaryCategory = ""
	}

	result.Model = modelName
	result.RawResponse = content
	return result, nil
}
