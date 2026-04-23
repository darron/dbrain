package summarizecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dbrain/internal/model"
)

const ToolName = "summarize"

const (
	commandRetryAttempts = 4
	commandRetryDelay    = 100 * time.Millisecond
	commandRetryMaxDelay = 2 * time.Second
	defaultOllamaBaseURL = "http://127.0.0.1:11434/v1"
	defaultOllamaAPIKey  = "ollama"
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
	Timeout   time.Duration
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
	opts.Model, opts.Env = resolveModelAndEnv(opts.Model, opts.Env)

	version := Version(ctx, opts.Binary)
	args := []string{"--json", "--timeout", formatTimeout(opts.Timeout), "--format", "text"}
	if opts.Summarize {
		args = append(args, "--length", opts.Length)
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

func runCommand(ctx context.Context, opts Options, args []string) ([]byte, error) {
	delay := commandRetryDelay

	for attempt := 0; ; attempt++ {
		cmd := exec.CommandContext(ctx, opts.Binary, args...)
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
			if err := sleepWithContext(ctx, delay); err != nil {
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
		out["OPENAI_BASE_URL"] = ollamaBaseURL()
	}
	if !hasEnvValue(out, "OPENAI_API_KEY") {
		out["OPENAI_API_KEY"] = defaultOllamaAPIKey
	}
	if !hasEnvValue(out, "OPENAI_USE_CHAT_COMPLETIONS") {
		out["OPENAI_USE_CHAT_COMPLETIONS"] = "1"
	}

	return "openai/" + ollamaModel, out
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

func ollamaBaseURL() string {
	value := strings.TrimSpace(os.Getenv("DBRAIN_OLLAMA_BASE_URL"))
	if value == "" {
		value = strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL"))
	}
	if value == "" {
		value = strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	}
	if value == "" {
		value = defaultOllamaBaseURL
	}
	return normalizeBaseURLWithV1(value)
}

func normalizeBaseURLWithV1(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultOllamaBaseURL
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	value = strings.TrimRight(value, "/")
	if strings.HasSuffix(value, "/v1") {
		return value
	}
	return value + "/v1"
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

func hasEnvValue(env map[string]string, key string) bool {
	if value := strings.TrimSpace(env[key]); value != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv(key)) != ""
}
