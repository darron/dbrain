package summarizecli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
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
	if err := unsupportedProviderModelError(opts.Model); err != nil {
		return Result{}, err
	}
	var err error
	opts.Env, err = envWithRuntimeConfig(ctx, opts.RootDir, opts.Env, opts.Model)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(opts.Language) == "" {
		opts.Language = summaryLanguageWithEnv(opts.Env)
	}

	directSummary, err := usesDirectSummaryForRoot(opts.RootDir, opts.Model)
	if err != nil {
		return Result{}, err
	}
	if inputText, ok, err := localSummaryInput(opts); err != nil {
		return Result{}, err
	} else if ok && opts.Summarize && directSummary {
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
