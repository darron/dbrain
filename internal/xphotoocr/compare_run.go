package xphotoocr

import (
	"context"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func runComparePhoto(ctx context.Context, cfg config.Config, opts CompareOptions, models []string, ref model.ItemMediaRef) ([]CompareRun, string, string) {
	runs := make([]CompareRun, 0, len(models))
	absolutePath, inputSource, cleanup, err := comparePhotoInputPath(ctx, cfg, opts, ref)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		for _, modelName := range models {
			runs = append(runs, CompareRun{
				Model:  modelName,
				Status: "error",
				Error:  err.Error(),
			})
		}
		return runs, absolutePath, inputSource
	}
	for _, modelName := range models {
		run := CompareRun{Model: modelName, Status: "error"}
		if err := ctx.Err(); err != nil {
			run.Error = err.Error()
			runs = append(runs, run)
			continue
		}
		runStart := time.Now()
		block, err := ocrPhotoWithModel(ctx, absolutePath, ref, compareClientOptions(cfg, opts, modelName))
		run.DurationMS = time.Since(runStart).Milliseconds()
		if err != nil {
			run.Error = err.Error()
			runs = append(runs, run)
			continue
		}
		run.Status = "ok"
		run.Tool = block.Tool
		run.ReportedModel = block.Model
		run.Text = strings.TrimSpace(block.Text)
		run.CharCount = len([]rune(run.Text))
		run.LineCount = countLines(run.Text)
		run.WordCount = len(normalizedWordSet(run.Text))
		runs = append(runs, run)
	}
	return runs, absolutePath, inputSource
}

func compareClientOptions(cfg config.Config, opts CompareOptions, modelName string) Options {
	return resolveOptions(cfg, Options{
		Model:           modelName,
		Timeout:         opts.Timeout,
		TesseractBinary: opts.TesseractBinary,
		OpenRouterBase:  opts.OpenRouterBase,
		OpenRouterKey:   opts.OpenRouterKey,
		OpenRouterTitle: opts.OpenRouterTitle,
		OpenRouterRef:   opts.OpenRouterRef,
		UserAgent:       opts.UserAgent,
		OllamaBase:      opts.OllamaBase,
		OllamaKey:       opts.OllamaKey,
	})
}

func normalizeCompareModels(cfg config.Config, values []string) []string {
	if len(values) == 0 {
		values = []string{ResolveModel(cfg, ""), "ollama/deepseek-ocr:3b"}
	}
	models := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		modelName := ResolveModel(cfg, value)
		if strings.TrimSpace(modelName) == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		models = append(models, modelName)
	}
	return models
}
