package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/modelbakeoff"
	"github.com/darron/dbrain/internal/store"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "model bakeoff: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	var root string
	var mode string
	var lookupFlags multiFlag
	var modelFlags multiFlag
	var timeout time.Duration
	var length string
	var language string
	var output string
	var jsonOut bool
	var maxTextChars int
	var includeImages bool
	var parityPreset string

	fs := flag.NewFlagSet("model_bakeoff", flag.ContinueOnError)
	fs.StringVar(&root, "root", "", "dbrain root override; defaults to the normal installed config/data locations")
	fs.StringVar(&mode, "mode", modelbakeoff.ModeSourceSummary, "Bakeoff mode: source-summary, categorize-item, categorize-source")
	fs.Var(&lookupFlags, "lookup", "Target lookup; repeat or comma-separate source_key, item key, URL, or note path")
	fs.Var(&modelFlags, "model", "Model to compare; repeat or comma-separate, e.g. ollama/qwen3.6:27b")
	fs.DurationVar(&timeout, "timeout", 2*time.Minute, "Per-model, per-target timeout")
	fs.StringVar(&length, "length", "medium", "Summary length hint for source-summary")
	fs.StringVar(&language, "language", "", "Summary language hint for source-summary; defaults to configured summary language")
	fs.StringVar(&output, "output", "", "Markdown report path; defaults to a temp report, or use '-' for stdout")
	fs.BoolVar(&jsonOut, "json", false, "Write full JSON results to stdout instead of a Markdown report")
	fs.IntVar(&maxTextChars, "max-text-chars", 4000, "Maximum summary chars per model in the Markdown report; 0 disables truncation")
	fs.BoolVar(&includeImages, "images", false, "Include item images for categorize-item vision-capable models")
	fs.StringVar(&parityPreset, "parity-preset", llmprovider.ParityPresetNone, "Optional parity preset: none, dbrain-modelfile")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	result, err := modelbakeoff.Run(ctx, cfg, st, modelbakeoff.Options{
		Mode:          mode,
		Lookups:       lookupFlags,
		Models:        modelFlags,
		Timeout:       timeout,
		Length:        length,
		Language:      language,
		IncludeImages: includeImages,
		ParityPreset:  parityPreset,
	})
	if err != nil {
		return err
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	report := modelbakeoff.RenderMarkdown(result, maxTextChars)
	if output == "-" {
		_, err := fmt.Fprint(os.Stdout, report)
		return err
	}
	reportPath := strings.TrimSpace(output)
	if reportPath == "" {
		file, err := cfg.CreateTemp("model-bakeoff-*.md")
		if err != nil {
			return err
		}
		reportPath = file.Name()
		if _, err := file.WriteString(report); err != nil {
			_ = file.Close()
			return fmt.Errorf("write report %s: %w", reportPath, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close report %s: %w", reportPath, err)
		}
	} else if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		return fmt.Errorf("write report %s: %w", reportPath, err)
	}

	fmt.Printf("Model bakeoff complete\n")
	fmt.Printf("Mode: %s\n", result.Mode)
	fmt.Printf("Targets: %d\n", len(result.Targets))
	fmt.Printf("Models: %s\n", strings.Join(result.Models, ", "))
	fmt.Printf("Errors: %d\n", result.Errors)
	fmt.Printf("Report: %s\n", reportPath)
	for _, row := range result.Summary {
		fmt.Printf("- %s: ok=%d errors=%d avg_duration=%dms avg_chars=%d avg_baseline_overlap=%.1f%%\n",
			row.Model,
			row.OK,
			row.Errors,
			row.AverageDurationMS,
			row.AverageOutputChars,
			row.AverageBaselineWordOverlap*100,
		)
	}
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			*m = append(*m, trimmed)
		}
	}
	return nil
}
