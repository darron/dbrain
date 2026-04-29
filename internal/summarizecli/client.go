package summarizecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const ToolName = "summarize"
const DirectSummaryToolName = "ollama-direct"
const DirectOpenRouterToolName = "openrouter-direct"

const (
	commandRetryAttempts     = 4
	commandRetryDelay        = 100 * time.Millisecond
	commandRetryMaxDelay     = 2 * time.Second
	defaultSummaryLanguage   = "en"
	defaultOllamaBaseURL     = "http://127.0.0.1:11434/v1"
	defaultOllamaAPIKey      = "ollama"
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	directOllamaVersion      = "ollama-direct-v1"
	directOpenRouterVersion  = "openrouter-direct-v1"
)

type Options struct {
	Binary    string
	Input     string
	Stdin     string
	Env       map[string]string
	Args      []string
	Summarize bool
	Model     string
	CLI       string
	Prompt    string
	Length    string
	Language  string
	Timeout   time.Duration
	RootDir   string
}

type Result struct {
	Extract model.ExtractResult
	Summary model.SummaryResult
}

type outputEnvelope struct {
	Input struct {
		Model string `json:"model"`
	} `json:"input"`
	Extracted struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Description string `json:"description"`
		SiteName    string `json:"siteName"`
		Content     string `json:"content"`
	} `json:"extracted"`
	Summary *string `json:"summary"`
}

type cliState struct {
	LastSuccessfulProvider string `json:"lastSuccessfulProvider"`
}

type chatCompletionsRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ollamaChatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Think    *bool         `json:"think,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionsResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type ollamaChatResponse struct {
	Model   string      `json:"model"`
	Message chatMessage `json:"message"`
}

type directSummaryTarget struct {
	model        string
	displayName  string
	baseURL      string
	apiKey       string
	toolName     string
	toolVersion  string
	headers      map[string]string
	label        string
	nativeOllama bool
}

var (
	versionMu    sync.Mutex
	versionCache = map[string]string{}
)

func Run(ctx context.Context, opts Options) (Result, error) {
	if strings.TrimSpace(opts.Binary) == "" {
		opts.Binary = "summarize"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if strings.TrimSpace(opts.Length) == "" {
		opts.Length = "medium"
	}
	opts.Env = envWithRuntimeConfig(opts.RootDir, opts.Env)
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = summaryLanguageWithEnv(opts.Env)
	}

	if inputText, ok, err := localSummaryInput(opts); err != nil {
		return Result{}, err
	} else if ok && opts.Summarize && UsesDirectSummary(opts.Model) {
		return runDirectSummary(ctx, opts, inputText)
	}

	opts.Model, opts.Env = resolveModelAndEnv(opts.Model, opts.Env)
	opts.CLI = ResolveCLIProvider(opts.CLI, opts.Model)

	version := Version(ctx, opts.Binary)
	args := []string{"--json", "--timeout", formatTimeout(opts.Timeout), "--format", "text"}
	if opts.Summarize {
		args = append(args, "--length", opts.Length)
		if value := strings.TrimSpace(opts.Language); value != "" {
			args = append(args, "--language", value)
		}
	} else {
		args = append(args, "--extract")
	}
	args = append(args, opts.Args...)
	if value := strings.TrimSpace(opts.Model); value != "" {
		args = append(args, "--model", value)
	}
	if value := strings.TrimSpace(opts.CLI); value != "" {
		args = append(args, "--cli", value)
	}
	if value := strings.TrimSpace(opts.Prompt); value != "" && opts.Summarize {
		args = append(args, "--prompt", value)
	}
	args = append(args, opts.Input)

	stdout, err := runCommand(ctx, opts, args)
	if err != nil {
		return Result{}, err
	}
	stdoutText := strings.TrimSpace(string(stdout))

	var payload outputEnvelope
	if err := json.Unmarshal(stdout, &payload); err != nil {
		output := stdoutText
		if len(output) > 160 {
			output = output[:160]
		}
		return Result{}, fmt.Errorf("parse summarize json: %w (stdout prefix: %q)", err, output)
	}

	now := time.Now().UTC()
	result := Result{
		Extract: model.ExtractResult{
			CanonicalURL: opts.Input,
			FinalURL:     strings.TrimSpace(payload.Extracted.URL),
			Title:        strings.TrimSpace(payload.Extracted.Title),
			Description:  strings.TrimSpace(payload.Extracted.Description),
			SiteName:     strings.TrimSpace(payload.Extracted.SiteName),
			Content:      strings.TrimSpace(payload.Extracted.Content),
			RawJSON:      stdoutText,
			FetchedAt:    now,
			Tool:         ToolName,
			ToolVersion:  version,
		},
	}

	if result.Extract.FinalURL == "" {
		result.Extract.FinalURL = strings.TrimSpace(opts.Input)
	}
	if result.Extract.Content == "" {
		result.Extract.Status = "empty"
	} else {
		result.Extract.Status = "ok"
	}

	if opts.Summarize {
		result.Summary = model.SummaryResult{
			Model:       strings.TrimSpace(payload.Input.Model),
			FetchedAt:   now,
			Tool:        ToolName,
			ToolVersion: version,
		}
		if payload.Summary != nil && strings.TrimSpace(*payload.Summary) != "" {
			result.Summary.Text = strings.TrimSpace(*payload.Summary)
			result.Summary.RawJSON = stdoutText
			result.Summary.Status = "ok"
		} else {
			result.Summary.Status = "error"
			result.Summary.Error = "summarize returned no summary text"
		}
	}

	return result, nil
}

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

func runCommand(ctx context.Context, opts Options, args []string) ([]byte, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	delay := commandRetryDelay

	for attempt := 0; ; attempt++ {
		cmd := exec.CommandContext(timeoutCtx, opts.Binary, args...)
		configureCommandForCancellation(cmd)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if len(opts.Env) > 0 {
			env := os.Environ()
			for key, value := range opts.Env {
				env = append(env, key+"="+value)
			}
			cmd.Env = env
		}
		if opts.Stdin != "" {
			cmd.Stdin = strings.NewReader(opts.Stdin)
		}

		if err := cmd.Run(); err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			if !isRetryableCommandError(errMsg) || attempt >= commandRetryAttempts-1 {
				return nil, fmt.Errorf("run summarize: %s", errMsg)
			}
			if err := sleepWithContext(timeoutCtx, delay); err != nil {
				return nil, err
			}
			delay *= 2
			if delay > commandRetryMaxDelay {
				delay = commandRetryMaxDelay
			}
			continue
		}

		return stdout.Bytes(), nil
	}
}

func isRetryableCommandError(message string) bool {
	value := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(value, "sqlite_busy") ||
		strings.Contains(value, "sqlite_locked") ||
		strings.Contains(value, "database is locked") ||
		strings.Contains(value, "database table is locked")
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func PreferredCLIProvider() string {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		path := filepath.Join(home, ".summarize", "cli-state.json")
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			var state cliState
			if err := json.Unmarshal(data, &state); err == nil {
				if provider := strings.TrimSpace(state.LastSuccessfulProvider); provider != "" {
					return provider
				}
			}
		}
	}
	return "codex"
}

func ResolveCLIProvider(cli, model string) string {
	if strings.TrimSpace(model) != "" {
		return ""
	}
	if value := strings.TrimSpace(cli); value != "" {
		return value
	}
	return PreferredCLIProvider()
}

func Version(ctx context.Context, binary string) string {
	value := strings.TrimSpace(binary)
	if value == "" {
		value = "summarize"
	}

	versionMu.Lock()
	cached, ok := versionCache[value]
	versionMu.Unlock()
	if ok {
		return cached
	}

	version := detectVersion(ctx, value)

	versionMu.Lock()
	versionCache[value] = version
	versionMu.Unlock()
	return version
}

func detectVersion(ctx context.Context, binary string) string {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	for _, args := range [][]string{{"--version"}, {"version"}} {
		cmd := exec.CommandContext(timeoutCtx, binary, args...)
		configureCommandForCancellation(cmd)
		output, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				output = exitErr.Stderr
			} else {
				continue
			}
		}
		if len(output) == 0 {
			continue
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			continue
		}
		if idx := strings.IndexByte(value, '\n'); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		return value
	}

	return ""
}

func formatTimeout(value time.Duration) string {
	seconds := int(value.Seconds())
	if seconds <= 0 {
		seconds = 1
	}
	return fmt.Sprintf("%ds", seconds)
}

func resolveModelAndEnv(model string, env map[string]string) (string, map[string]string) {
	trimmedModel := strings.TrimSpace(model)
	ollamaModel, ok := parseOllamaModel(trimmedModel)
	if !ok {
		return trimmedModel, env
	}

	out := cloneEnv(env)
	if !hasEnvValue(out, "OPENAI_BASE_URL") {
		out["OPENAI_BASE_URL"] = ollamaBaseURLWithEnv(out)
	}
	if !hasEnvValue(out, "OPENAI_API_KEY") {
		out["OPENAI_API_KEY"] = ollamaAPIKeyWithEnv(out)
	}
	if !hasEnvValue(out, "OPENAI_USE_CHAT_COMPLETIONS") {
		out["OPENAI_USE_CHAT_COMPLETIONS"] = "1"
	}

	return "openai/" + ollamaModel, out
}

func promptWithLengthAndLanguageHints(prompt string, length string, language string) string {
	base := strings.TrimSpace(prompt)
	hints := make([]string, 0, 2)
	if hint := strings.TrimSpace(lengthHint(length)); hint != "" {
		hints = append(hints, hint)
	}
	if hint := strings.TrimSpace(languageHint(language)); hint != "" {
		hints = append(hints, hint)
	}
	hint := strings.Join(hints, "\n")
	switch {
	case base == "":
		return hint
	case hint == "":
		return base
	default:
		return base + "\n\n" + hint
	}
}

func languageHint(language string) string {
	value := strings.TrimSpace(language)
	if value == "" || strings.EqualFold(value, "auto") {
		return ""
	}
	return "Write the summary in this output language: " + value + "."
}

func lengthHint(length string) string {
	switch strings.ToLower(strings.TrimSpace(length)) {
	case "short":
		return "Target response length: short. Compress aggressively."
	case "long":
		return "Target response length: long. Preserve more detail while remaining meaningfully shorter than the source."
	case "medium", "":
		return "Target response length: medium. Keep the response meaningfully shorter than the source."
	default:
		return ""
	}
}

func parseOllamaModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	if value == "" {
		return "", false
	}

	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "ollama/"):
		resolved := strings.TrimSpace(value[len("ollama/"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "ollama:"):
		resolved := strings.TrimSpace(value[len("ollama:"):])
		return resolved, resolved != ""
	default:
		return "", false
	}
}

func parseOpenRouterModel(model string) (string, bool) {
	value := strings.TrimSpace(model)
	if value == "" {
		return "", false
	}

	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "openrouter/"):
		resolved := strings.TrimSpace(value[len("openrouter/"):])
		return resolved, resolved != ""
	case strings.HasPrefix(lower, "openrouter:"):
		resolved := strings.TrimSpace(value[len("openrouter:"):])
		return resolved, resolved != ""
	default:
		return "", false
	}
}

func ollamaBaseURLWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST", "OPENAI_BASE_URL")
	if value == "" {
		value = defaultOllamaBaseURL
	}
	return normalizeBaseURLWithV1(value)
}

func ollamaNativeBaseURLWithEnv(env map[string]string) string {
	return strings.TrimSuffix(ollamaBaseURLWithEnv(env), "/v1")
}

func ollamaAPIKeyWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY", "OPENAI_API_KEY")
	if value == "" {
		value = defaultOllamaAPIKey
	}
	return value
}

func openRouterBaseURLWithEnv(env map[string]string) string {
	value := firstEnvValue(env, "DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL")
	if value == "" {
		value = defaultOpenRouterBaseURL
	}
	return normalizeBaseURLWithPath(value, "/api/v1", defaultOpenRouterBaseURL)
}

func openRouterAPIKeyWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY")
}

func openRouterRefererWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER")
}

func openRouterTitleWithEnv(env map[string]string) string {
	return firstEnvValue(env, "DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE")
}

func summaryLanguageWithEnv(env map[string]string) string {
	if value := firstEnvValue(env, "DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE"); value != "" {
		return value
	}
	return defaultSummaryLanguage
}

func normalizeBaseURLWithV1(raw string) string {
	return normalizeBaseURLWithPath(raw, "/v1", defaultOllamaBaseURL)
}

func normalizeBaseURLWithPath(raw string, suffix string, fallback string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimRight(value, "/")
	if strings.HasSuffix(value, suffix) {
		return value
	}
	return value + suffix
}

func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}

func envWithRuntimeConfig(rootDir string, env map[string]string) map[string]string {
	if strings.TrimSpace(rootDir) == "" {
		return env
	}
	out := cloneEnv(env)
	for _, keys := range [][]string{
		{"DBRAIN_OLLAMA_BASE_URL", "OLLAMA_BASE_URL", "OLLAMA_HOST"},
		{"DBRAIN_OLLAMA_API_KEY", "OLLAMA_API_KEY"},
		{"OPENAI_BASE_URL"},
		{"OPENAI_API_KEY"},
		{"OPENAI_USE_CHAT_COMPLETIONS"},
		{"DBRAIN_OPENROUTER_BASE_URL", "OPENROUTER_BASE_URL"},
		{"DBRAIN_OPENROUTER_API_KEY", "OPENROUTER_API_KEY"},
		{"DBRAIN_OPENROUTER_REFERER", "OPENROUTER_HTTP_REFERER"},
		{"DBRAIN_OPENROUTER_TITLE", "OPENROUTER_X_TITLE"},
		{"DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE"},
	} {
		value := runtimeenv.FirstNonEmpty(rootDir, keys...)
		if value == "" {
			continue
		}
		for _, key := range keys {
			if strings.TrimSpace(out[key]) == "" {
				out[key] = value
				break
			}
		}
	}
	return out
}

func hasEnvValue(env map[string]string, key string) bool {
	if value := strings.TrimSpace(env[key]); value != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv(key)) != ""
}

func firstEnvValue(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
