package summarizecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/version"
)

func UsesDirectSummary(model string) bool {
	_, ollama := parseOllamaModel(model)
	if ollama {
		return true
	}
	_, openrouter := parseOpenRouterModel(model)
	return openrouter
}

func SummaryToolName(model string) string {
	if _, ok := parseOllamaModel(model); ok {
		return DirectSummaryToolName
	}
	if _, ok := parseOpenRouterModel(model); ok {
		return DirectOpenRouterToolName
	}
	return ToolName
}

func SummaryToolVersion(ctx context.Context, binary string, model string) string {
	if _, ok := parseOllamaModel(model); ok {
		return directOllamaVersion
	}
	if _, ok := parseOpenRouterModel(model); ok {
		return directOpenRouterVersion
	}
	return Version(ctx, binary)
}

func localSummaryInput(opts Options) (string, bool, error) {
	if !opts.Summarize {
		return "", false, nil
	}
	if value := strings.TrimSpace(opts.Stdin); value != "" {
		return value, true, nil
	}
	input := strings.TrimSpace(opts.Input)
	if input == "" {
		return "", false, nil
	}
	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("stat summary input: %w", err)
	}
	if info.IsDir() {
		return "", false, nil
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return "", false, fmt.Errorf("read summary input: %w", err)
	}
	return string(data), true, nil
}

func runDirectSummary(ctx context.Context, opts Options, inputText string) (Result, error) {
	target, err := resolveDirectSummaryTarget(ctx, opts)
	if err != nil {
		return Result{}, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	messages := make([]chatMessage, 0, 2)
	if prompt := strings.TrimSpace(promptWithLengthAndLanguageHints(opts.Prompt, opts.Length, opts.Language)); prompt != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: prompt,
		})
	}
	messages = append(messages, chatMessage{
		Role:    "user",
		Content: strings.TrimSpace(inputText),
	})

	endpoint := target.baseURL + "/chat/completions"
	requestBody := any(chatCompletionsRequest{
		Model:    target.model,
		Messages: messages,
		Stream:   false,
	})
	if target.nativeOllama {
		think := false
		endpoint = target.baseURL + "/api/chat"
		requestBody = ollamaChatRequest{
			Model:    target.model,
			Messages: messages,
			Stream:   false,
			Think:    &think,
		}
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return Result{}, fmt.Errorf("marshal %s summary request: %w", target.label, err)
	}

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("create %s summary request: %w", target.label, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+target.apiKey)
	for key, value := range target.headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("run %s summary: %w", target.label, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("read %s summary response: %w", target.label, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := strings.TrimSpace(string(respBody))
		if len(bodyText) > 200 {
			bodyText = bodyText[:200]
		}
		return Result{}, fmt.Errorf("run %s summary: http %d: %s", target.label, resp.StatusCode, bodyText)
	}

	summaryText, err := directSummaryText(target, respBody)
	if err != nil {
		return Result{}, err
	}
	if summaryText == "" {
		return Result{}, fmt.Errorf("run %s summary: response contained no summary text", target.label)
	}

	now := time.Now().UTC()
	return Result{
		Summary: model.SummaryResult{
			Text:        summaryText,
			RawJSON:     strings.TrimSpace(string(respBody)),
			Model:       target.displayName,
			Status:      "ok",
			FetchedAt:   now,
			Tool:        target.toolName,
			ToolVersion: target.toolVersion,
		},
	}, nil
}

func directSummaryText(target directSummaryTarget, respBody []byte) (string, error) {
	if target.nativeOllama {
		var payload ollamaChatResponse
		if err := json.Unmarshal(respBody, &payload); err != nil {
			bodyText := strings.TrimSpace(string(respBody))
			if len(bodyText) > 200 {
				bodyText = bodyText[:200]
			}
			return "", fmt.Errorf("parse %s summary response: %w (body prefix: %q)", target.label, err, bodyText)
		}
		return strings.TrimSpace(payload.Message.Content), nil
	}

	var payload chatCompletionsResponse
	if err := json.Unmarshal(respBody, &payload); err != nil {
		bodyText := strings.TrimSpace(string(respBody))
		if len(bodyText) > 200 {
			bodyText = bodyText[:200]
		}
		return "", fmt.Errorf("parse %s summary response: %w (body prefix: %q)", target.label, err, bodyText)
	}
	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("run %s summary: response contained no choices", target.label)
	}
	return strings.TrimSpace(payload.Choices[0].Message.Content), nil
}

func resolveDirectSummaryTarget(ctx context.Context, opts Options) (directSummaryTarget, error) {
	if ollamaModel, ok := parseOllamaModel(opts.Model); ok {
		return directSummaryTarget{
			model:        ollamaModel,
			displayName:  defaultDirectDisplayName(opts.Model, "ollama/"+ollamaModel),
			baseURL:      ollamaNativeBaseURLWithEnv(opts.Env),
			apiKey:       ollamaAPIKeyWithEnv(opts.Env),
			toolName:     SummaryToolName(opts.Model),
			toolVersion:  SummaryToolVersion(ctx, opts.Binary, opts.Model),
			label:        "ollama",
			nativeOllama: true,
		}, nil
	}
	if openrouterModel, ok := parseOpenRouterModel(opts.Model); ok {
		apiKey := openRouterAPIKeyWithEnv(opts.Env)
		if strings.TrimSpace(apiKey) == "" {
			return directSummaryTarget{}, fmt.Errorf("direct openrouter summary requested without DBRAIN_OPENROUTER_API_KEY or OPENROUTER_API_KEY")
		}
		headers := map[string]string{}
		if value := openRouterRefererWithEnv(opts.Env); value != "" {
			headers["HTTP-Referer"] = value
		}
		if value := openRouterTitleWithEnv(opts.Env); value != "" {
			headers["X-Title"] = value
		}
		headers["User-Agent"] = version.UserAgent(userAgentWithEnv(opts.Env))
		return directSummaryTarget{
			model:       openrouterModel,
			displayName: defaultDirectDisplayName(opts.Model, "openrouter/"+openrouterModel),
			baseURL:     openRouterBaseURLWithEnv(opts.Env),
			apiKey:      apiKey,
			toolName:    SummaryToolName(opts.Model),
			toolVersion: SummaryToolVersion(ctx, opts.Binary, opts.Model),
			headers:     headers,
			label:       "openrouter",
		}, nil
	}
	return directSummaryTarget{}, fmt.Errorf("direct summary requested without a supported direct model")
}

func defaultDirectDisplayName(current string, fallback string) string {
	value := strings.TrimSpace(current)
	if value != "" {
		return value
	}
	return fallback
}
