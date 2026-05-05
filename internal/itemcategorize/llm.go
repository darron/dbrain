package itemcategorize

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

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
