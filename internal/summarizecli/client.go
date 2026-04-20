package summarizecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"dbrain/internal/model"
)

const ToolName = "summarize"

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
	} else {
		cmd.Stdin = nil
	}
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return Result{}, fmt.Errorf("run summarize: %s", errMsg)
	}

	var payload outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return Result{}, fmt.Errorf("parse summarize json: %w", err)
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
			RawJSON:      strings.TrimSpace(stdout.String()),
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
			result.Summary.RawJSON = strings.TrimSpace(stdout.String())
			result.Summary.Status = "ok"
		} else {
			result.Summary.Status = "error"
			result.Summary.Error = "summarize returned no summary text"
		}
	}

	return result, nil
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
