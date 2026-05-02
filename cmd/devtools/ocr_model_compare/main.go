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
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xphotoocr"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ocr model compare: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	var root string
	var limit int
	var concurrency int
	var timeout time.Duration
	var baselineModel string
	var candidateModel string
	var extraModels string
	var output string
	var jsonOut bool
	var maxTextChars int
	var downloadMissing bool
	var tesseractBinary string
	var openRouterBase string
	var openRouterKey string
	var ollamaBase string

	fs := flag.NewFlagSet("ocr_model_compare", flag.ContinueOnError)
	fs.StringVar(&root, "root", "", "dbrain root override; defaults to the normal installed config/data locations")
	fs.IntVar(&limit, "limit", 20, "Maximum downloaded X photo images to sample")
	fs.IntVar(&concurrency, "concurrency", 1, "Number of images to compare concurrently")
	fs.DurationVar(&timeout, "timeout", 2*time.Minute, "Per-model, per-image timeout")
	fs.StringVar(&baselineModel, "baseline-model", "", "Baseline OCR model; defaults to the currently configured OCR model")
	fs.StringVar(&candidateModel, "candidate-model", "ollama/deepseek-ocr:3b", "Candidate OCR model to compare")
	fs.StringVar(&extraModels, "models", "", "Optional comma-separated explicit model list; overrides baseline/candidate when set")
	fs.StringVar(&output, "output", "", "Markdown report path; defaults to a temp report, or use '-' for stdout")
	fs.BoolVar(&jsonOut, "json", false, "Write full JSON results to stdout instead of a Markdown report")
	fs.IntVar(&maxTextChars, "max-text-chars", 4000, "Maximum OCR text chars per model in the Markdown report; 0 disables truncation")
	fs.BoolVar(&downloadMissing, "download-missing", false, "Temporarily download pruned/missing image files from saved remote URLs without changing DB/media state")
	fs.StringVar(&tesseractBinary, "tesseract-binary", "tesseract", "Tesseract binary when comparing model 'tesseract'")
	fs.StringVar(&openRouterBase, "openrouter-base", "", "OpenRouter API base override")
	fs.StringVar(&openRouterKey, "openrouter-key", "", "OpenRouter API key override; normally read from config/env")
	fs.StringVar(&ollamaBase, "ollama-base", "", "Ollama base URL override; normally read from config/env")
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

	models := modelList(cfg, baselineModel, candidateModel, extraModels)
	result, err := xphotoocr.Compare(ctx, cfg, st, xphotoocr.CompareOptions{
		Limit:           limit,
		Models:          models,
		Concurrency:     concurrency,
		Timeout:         timeout,
		DownloadMissing: downloadMissing,
		TesseractBinary: tesseractBinary,
		OpenRouterBase:  openRouterBase,
		OpenRouterKey:   openRouterKey,
		OllamaBase:      ollamaBase,
	})
	if err != nil {
		return err
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	report := xphotoocr.RenderCompareMarkdown(result, maxTextChars)
	if output == "-" {
		_, err := fmt.Fprint(os.Stdout, report)
		return err
	}
	reportPath := output
	if strings.TrimSpace(reportPath) == "" {
		file, err := cfg.CreateTemp("x-photo-ocr-compare-*.md")
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

	fmt.Printf("OCR model comparison complete\n")
	fmt.Printf("Images: %d\n", len(result.Images))
	fmt.Printf("Models: %s\n", strings.Join(result.Models, ", "))
	fmt.Printf("Errors: %d\n", result.Errors)
	fmt.Printf("Report: %s\n", reportPath)
	for _, row := range result.Summary {
		fmt.Printf("- %s: ok=%d errors=%d avg_duration=%dms avg_chars=%d avg_baseline_overlap=%.1f%%\n",
			row.Model,
			row.OK,
			row.Errors,
			row.AverageDurationMS,
			row.AverageChars,
			row.AverageBaselineWordOverlap*100,
		)
	}
	return nil
}

func modelList(cfg config.Config, baselineModel string, candidateModel string, explicitModels string) []string {
	if strings.TrimSpace(explicitModels) != "" {
		return splitCSV(explicitModels)
	}
	models := []string{xphotoocr.ResolveModel(cfg, baselineModel)}
	if strings.TrimSpace(candidateModel) != "" {
		models = append(models, strings.TrimSpace(candidateModel))
	}
	return models
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
