package xphotoocr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

const CompareSchemaVersion = "x_photo_ocr_compare.v1"

const compareDownloadMaxBytes = 25 << 20

type CompareOptions struct {
	Limit           int
	Models          []string
	Concurrency     int
	Timeout         time.Duration
	DownloadMissing bool
	TesseractBinary string
	OpenRouterBase  string
	OpenRouterKey   string
	OpenRouterTitle string
	OpenRouterRef   string
	UserAgent       string
	OllamaBase      string
	OllamaKey       string
}

type CompareResult struct {
	SchemaVersion string                `json:"schema_version"`
	StartedAt     time.Time             `json:"started_at"`
	FinishedAt    time.Time             `json:"finished_at"`
	DurationMS    int64                 `json:"duration_ms"`
	Limit         int                   `json:"limit"`
	Models        []string              `json:"models"`
	Images        []CompareImageResult  `json:"images"`
	Summary       []CompareModelSummary `json:"summary"`
	Errors        int                   `json:"errors"`
}

type CompareImageResult struct {
	Index        int          `json:"index"`
	ItemID       int64        `json:"item_id"`
	SourceKey    string       `json:"source_key"`
	Title        string       `json:"title,omitempty"`
	CanonicalURL string       `json:"canonical_url,omitempty"`
	NotePath     string       `json:"note_path,omitempty"`
	PhotoOrdinal int          `json:"photo_ordinal"`
	LocalPath    string       `json:"local_path"`
	InputPath    string       `json:"input_path,omitempty"`
	InputSource  string       `json:"input_source,omitempty"`
	RemoteURL    string       `json:"remote_url,omitempty"`
	ExpandedURL  string       `json:"expanded_url,omitempty"`
	ExistingOCR  ExistingOCR  `json:"existing_ocr"`
	Runs         []CompareRun `json:"runs"`
}

type ExistingOCR struct {
	Status string `json:"status,omitempty"`
	Model  string `json:"model,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Text   string `json:"text,omitempty"`
}

type CompareRun struct {
	Model                  string  `json:"model"`
	ReportedModel          string  `json:"reported_model,omitempty"`
	Tool                   string  `json:"tool,omitempty"`
	Status                 string  `json:"status"`
	Error                  string  `json:"error,omitempty"`
	Text                   string  `json:"text,omitempty"`
	DurationMS             int64   `json:"duration_ms"`
	CharCount              int     `json:"char_count"`
	LineCount              int     `json:"line_count"`
	WordCount              int     `json:"word_count"`
	BaselineWordOverlap    float64 `json:"baseline_word_overlap,omitempty"`
	BaselineOnlyWordCount  int     `json:"baseline_only_word_count,omitempty"`
	CandidateOnlyWordCount int     `json:"candidate_only_word_count,omitempty"`
}

type CompareModelSummary struct {
	Model                      string  `json:"model"`
	OK                         int     `json:"ok"`
	Errors                     int     `json:"errors"`
	TotalDurationMS            int64   `json:"total_duration_ms"`
	AverageDurationMS          int64   `json:"average_duration_ms"`
	TotalChars                 int     `json:"total_chars"`
	AverageChars               int     `json:"average_chars"`
	AverageBaselineWordOverlap float64 `json:"average_baseline_word_overlap,omitempty"`
}

type comparePhotoSample struct {
	result CompareImageResult
	ref    model.ItemMediaRef
}

func Compare(ctx context.Context, cfg config.Config, st *store.Store, opts CompareOptions) (CompareResult, error) {
	started := time.Now().UTC()
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	models := normalizeCompareModels(cfg, opts.Models)
	if len(models) == 0 {
		return CompareResult{}, fmt.Errorf("at least one OCR model is required")
	}

	samples, err := collectComparePhotoSamples(ctx, st, opts.Limit, opts.DownloadMissing)
	if err != nil {
		return CompareResult{}, err
	}

	result := CompareResult{
		SchemaVersion: CompareSchemaVersion,
		StartedAt:     started,
		Limit:         opts.Limit,
		Models:        models,
		Images:        make([]CompareImageResult, len(samples)),
	}
	if len(samples) == 0 {
		result.FinishedAt = time.Now().UTC()
		result.DurationMS = result.FinishedAt.Sub(started).Milliseconds()
		return result, nil
	}

	concurrency := opts.Concurrency
	if concurrency > len(samples) {
		concurrency = len(samples)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				image := samples[idx].result
				var inputPath string
				image.Runs, inputPath, image.InputSource = runComparePhoto(ctx, cfg, opts, models, samples[idx].ref)
				image.InputPath = inputPath
				annotateBaselineOverlap(image.Runs)
				result.Images[idx] = image
			}
		}()
	}
dispatch:
	for idx := range samples {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- idx:
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}

	result.Summary = summarizeCompareRuns(models, result.Images)
	for _, image := range result.Images {
		for _, run := range image.Runs {
			if run.Status != "ok" {
				result.Errors++
			}
		}
	}
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = result.FinishedAt.Sub(started).Milliseconds()
	return result, nil
}

func collectComparePhotoSamples(ctx context.Context, st *store.Store, limit int, includePruned bool) ([]comparePhotoSample, error) {
	items, err := st.ListItemsForXPhotoOCRAudit(ctx, limit, includePruned)
	if err != nil {
		return nil, err
	}
	samples := make([]comparePhotoSample, 0, limit)
	for _, item := range items {
		refs, err := st.ListItemMediaRefs(ctx, item.ID)
		if err != nil {
			return nil, fmt.Errorf("list media refs for %s: %w", item.SourceKey, err)
		}
		for _, ref := range comparePhotoRefs(refs, includePruned) {
			if len(samples) >= limit {
				return samples, nil
			}
			samples = append(samples, comparePhotoSample{
				ref: ref,
				result: CompareImageResult{
					Index:        len(samples) + 1,
					ItemID:       item.ID,
					SourceKey:    item.SourceKey,
					Title:        item.Title,
					CanonicalURL: item.CanonicalURL,
					NotePath:     item.NotePath,
					PhotoOrdinal: ref.Ordinal,
					LocalPath:    ref.LocalPath,
					RemoteURL:    ref.RemoteURL,
					ExpandedURL:  ref.ExpandedURL,
					ExistingOCR: ExistingOCR{
						Status: item.OCRStatus,
						Model:  item.OCRModel,
						Tool:   item.OCRTool,
						Text:   item.OCRText,
					},
				},
			})
		}
	}
	return samples, nil
}

func comparePhotoRefs(refs []model.ItemMediaRef, includePruned bool) []model.ItemMediaRef {
	photos := make([]model.ItemMediaRef, 0, len(refs))
	for _, ref := range refs {
		if ref.MediaType != "photo" || ref.DownloadStatus != "downloaded" || strings.TrimSpace(ref.LocalPath) == "" {
			continue
		}
		if !includePruned && !ref.LocalPrunedAt.IsZero() {
			continue
		}
		photos = append(photos, ref)
	}
	return photos
}

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

func comparePhotoInputPath(ctx context.Context, cfg config.Config, opts CompareOptions, ref model.ItemMediaRef) (string, string, func(), error) {
	absolutePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
	if info, err := os.Stat(absolutePath); err == nil && info.Mode().IsRegular() {
		return absolutePath, "local", nil, nil
	}
	if !opts.DownloadMissing {
		return absolutePath, "missing", nil, fmt.Errorf("local image is unavailable; rerun with --download-missing to fetch a temp copy")
	}
	remoteURL := strings.TrimSpace(ref.RemoteURL)
	if remoteURL == "" {
		return absolutePath, "missing", nil, fmt.Errorf("local image is unavailable and media has no remote_url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return absolutePath, "missing", nil, fmt.Errorf("build image download request: %w", err)
	}
	if strings.TrimSpace(opts.UserAgent) != "" {
		req.Header.Set("User-Agent", strings.TrimSpace(opts.UserAgent))
	} else {
		req.Header.Set("User-Agent", "dbrain-ocr-compare")
	}
	client := &http.Client{Timeout: opts.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return absolutePath, "missing", nil, fmt.Errorf("download temp image: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return absolutePath, "missing", nil, fmt.Errorf("download temp image %s: %s", remoteURL, resp.Status)
	}

	ext := filepath.Ext(ref.LocalPath)
	if ext == "" {
		ext = ".img"
	}
	file, err := cfg.CreateTemp("x-photo-ocr-compare-image-*" + ext)
	if err != nil {
		return absolutePath, "missing", nil, err
	}
	tempPath := file.Name()
	cleanup := func() {
		_ = os.Remove(tempPath)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, compareDownloadMaxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		cleanup()
		return tempPath, "temp_download", nil, fmt.Errorf("write temp image: %w", copyErr)
	}
	if closeErr != nil {
		cleanup()
		return tempPath, "temp_download", nil, fmt.Errorf("close temp image: %w", closeErr)
	}
	if written > compareDownloadMaxBytes {
		cleanup()
		return tempPath, "temp_download", nil, fmt.Errorf("downloaded image exceeds %d bytes", compareDownloadMaxBytes)
	}
	return tempPath, "temp_download", cleanup, nil
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

func summarizeCompareRuns(models []string, images []CompareImageResult) []CompareModelSummary {
	summaryByModel := make(map[string]*CompareModelSummary, len(models))
	overlapTotals := make(map[string]float64, len(models))
	overlapCounts := make(map[string]int, len(models))
	for _, modelName := range models {
		summaryByModel[modelName] = &CompareModelSummary{Model: modelName}
	}
	for _, image := range images {
		for _, run := range image.Runs {
			row := summaryByModel[run.Model]
			if row == nil {
				row = &CompareModelSummary{Model: run.Model}
				summaryByModel[run.Model] = row
			}
			if run.Status == "ok" {
				row.OK++
				row.TotalDurationMS += run.DurationMS
				row.TotalChars += run.CharCount
				if run.BaselineWordOverlap > 0 {
					overlapTotals[run.Model] += run.BaselineWordOverlap
					overlapCounts[run.Model]++
				}
			} else {
				row.Errors++
			}
		}
	}
	out := make([]CompareModelSummary, 0, len(summaryByModel))
	for _, modelName := range models {
		row := summaryByModel[modelName]
		if row == nil {
			continue
		}
		if row.OK > 0 {
			row.AverageDurationMS = row.TotalDurationMS / int64(row.OK)
			row.AverageChars = row.TotalChars / row.OK
		}
		if overlapCounts[modelName] > 0 {
			row.AverageBaselineWordOverlap = overlapTotals[modelName] / float64(overlapCounts[modelName])
		}
		out = append(out, *row)
	}
	return out
}

func annotateBaselineOverlap(runs []CompareRun) {
	if len(runs) < 2 || runs[0].Status != "ok" {
		return
	}
	baseline := normalizedWordSet(runs[0].Text)
	if len(baseline) == 0 {
		return
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].Status != "ok" {
			continue
		}
		candidate := normalizedWordSet(runs[i].Text)
		if len(candidate) == 0 {
			continue
		}
		shared := 0
		for word := range baseline {
			if _, ok := candidate[word]; ok {
				shared++
			}
		}
		runs[i].BaselineWordOverlap = float64(shared) / float64(len(baseline))
		runs[i].BaselineOnlyWordCount = len(baseline) - shared
		runs[i].CandidateOnlyWordCount = len(candidate) - shared
	}
}

func RenderCompareMarkdown(result CompareResult, maxTextChars int) string {
	var b strings.Builder
	b.WriteString("# X Photo OCR Model Compare\n\n")
	_, _ = fmt.Fprintf(&b, "- Schema: `%s`\n", result.SchemaVersion)
	_, _ = fmt.Fprintf(&b, "- Images: %d\n", len(result.Images))
	_, _ = fmt.Fprintf(&b, "- Models: %s\n", strings.Join(result.Models, ", "))
	_, _ = fmt.Fprintf(&b, "- Started: %s\n", result.StartedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintf(&b, "- Duration: %dms\n\n", result.DurationMS)

	b.WriteString("## Summary\n\n")
	b.WriteString("| Model | OK | Errors | Avg Duration | Avg Chars | Avg Baseline Word Overlap |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, row := range result.Summary {
		_, _ = fmt.Fprintf(&b, "| %s | %d | %d | %dms | %d | %.1f%% |\n",
			markdownTableCell(row.Model),
			row.OK,
			row.Errors,
			row.AverageDurationMS,
			row.AverageChars,
			row.AverageBaselineWordOverlap*100,
		)
	}

	for _, image := range result.Images {
		_, _ = fmt.Fprintf(&b, "\n## %d. %s\n\n", image.Index, image.SourceKey)
		if strings.TrimSpace(image.Title) != "" {
			_, _ = fmt.Fprintf(&b, "- Title: %s\n", image.Title)
		}
		if strings.TrimSpace(image.CanonicalURL) != "" {
			_, _ = fmt.Fprintf(&b, "- URL: %s\n", image.CanonicalURL)
		}
		_, _ = fmt.Fprintf(&b, "- Local path: `%s`\n", image.LocalPath)
		if strings.TrimSpace(image.InputSource) != "" {
			_, _ = fmt.Fprintf(&b, "- Input source: %s\n", image.InputSource)
		}
		if strings.TrimSpace(image.ExpandedURL) != "" {
			_, _ = fmt.Fprintf(&b, "- Expanded media URL: %s\n", image.ExpandedURL)
		}
		if strings.TrimSpace(image.ExistingOCR.Status) != "" || strings.TrimSpace(image.ExistingOCR.Text) != "" {
			_, _ = fmt.Fprintf(&b, "- Existing OCR: status=%s model=%s tool=%s\n", emptyReportValue(image.ExistingOCR.Status), emptyReportValue(image.ExistingOCR.Model), emptyReportValue(image.ExistingOCR.Tool))
		}
		for _, run := range image.Runs {
			_, _ = fmt.Fprintf(&b, "\n### %s\n\n", run.Model)
			_, _ = fmt.Fprintf(&b, "- Status: %s\n", run.Status)
			_, _ = fmt.Fprintf(&b, "- Duration: %dms\n", run.DurationMS)
			if strings.TrimSpace(run.Tool) != "" {
				_, _ = fmt.Fprintf(&b, "- Tool: %s\n", run.Tool)
			}
			if strings.TrimSpace(run.ReportedModel) != "" && run.ReportedModel != run.Model {
				_, _ = fmt.Fprintf(&b, "- Reported model: %s\n", run.ReportedModel)
			}
			if run.BaselineWordOverlap > 0 {
				_, _ = fmt.Fprintf(&b, "- Baseline word overlap: %.1f%%\n", run.BaselineWordOverlap*100)
			}
			if strings.TrimSpace(run.Error) != "" {
				_, _ = fmt.Fprintf(&b, "- Error: %s\n", run.Error)
			}
			writeReportTextBlock(&b, run.Text, maxTextChars)
		}
	}
	return b.String()
}

func writeReportTextBlock(b *strings.Builder, text string, maxChars int) {
	text = strings.TrimSpace(text)
	if text == "" {
		b.WriteString("\n_No text returned._\n")
		return
	}
	if maxChars > 0 && len([]rune(text)) > maxChars {
		runes := []rune(text)
		text = string(runes[:maxChars]) + "\n\n[truncated]"
	}
	text = strings.ReplaceAll(text, "```", "` ` `")
	b.WriteString("\n```text\n")
	b.WriteString(text)
	b.WriteString("\n```\n")
}

var wordSplitRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

func normalizedWordSet(text string) map[string]struct{} {
	words := wordSplitRE.FindAllString(strings.ToLower(text), -1)
	out := make(map[string]struct{}, len(words))
	for _, word := range words {
		word = strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if len([]rune(word)) < 2 {
			continue
		}
		out[word] = struct{}{}
	}
	return out
}

func countLines(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func markdownTableCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func emptyReportValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
