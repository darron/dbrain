package xmediatranscribe

import (
	"context"
	"strings"
	"sync"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

const (
	xMediaSummaryPromptVersion = "x-media-summary-v2"
	xMediaSummaryPrompt        = "Summarize this bookmarked X media item. Use the X post context, including any quoted post context, as supporting context and the media transcript as primary evidence. Attribute claims from post text when they are not directly supported by the transcript. Write a concise plain-text summary without markdown headings."
)

func summarizeTranscriptItems(ctx context.Context, cfg config.Config, st *store.Store, opts Options) Stats {
	items, err := st.ListItemsForXMediaSummary(ctx, opts.Limit, opts.Force)
	if err != nil {
		debugLog(opts.Logger, "x media summary candidates failed", "error", err.Error())
		return Stats{SummaryErrors: 1}
	}

	stats := Stats{SummaryCandidates: len(items)}
	if len(items) == 0 {
		return stats
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}
	debugLog(opts.Logger, "x media summary candidates loaded", "items", len(items), "concurrency", concurrency)

	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan model.Item)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				summarized, summaryErrors := summarizeTranscriptItem(ctx, cfg, st, opts, item)
				mu.Lock()
				stats.ItemsSummarized += summarized
				stats.SummaryErrors += summaryErrors
				mu.Unlock()
			}
		}()
	}
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	wg.Wait()

	return stats
}

func summarizeTranscriptItem(ctx context.Context, cfg config.Config, st *store.Store, opts Options, item model.Item) (int, int) {
	input := buildTranscriptSummaryInput(item)
	inputHash := hashSummaryInput(input)

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Summarize: true,
		Stdin:     input,
		Model:     opts.SummaryModel,
		CLI:       opts.SummaryCLI,
		Length:    opts.SummaryLength,
		Language:  opts.SummaryLanguage,
		Timeout:   opts.Timeout,
		Prompt:    xMediaSummaryPrompt,
		RootDir:   cfg.RootDir,
	})

	summary := model.SummaryResult{
		Model:         strings.TrimSpace(opts.SummaryModel),
		PromptVersion: xMediaSummaryPromptVersion,
		Status:        model.ItemSummaryStatusOK,
	}
	if err != nil {
		summary = summaryResultFromError(opts, err)
	} else {
		summary = runResult.Summary
		summary.PromptVersion = xMediaSummaryPromptVersion
	}

	changed, saveErr := st.SaveItemSummary(ctx, item.ID, summary, inputHash)
	if saveErr != nil {
		debugLog(opts.Logger, "x media summary save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", saveErr.Error())
		return 0, 1
	}
	if summary.Status != model.ItemSummaryStatusOK {
		debugLog(opts.Logger, "x media summary failed", "source_key", item.SourceKey, "item_id", item.ID, "status", summary.Status, "error", summary.Error)
		return 0, 1
	}
	if !changed {
		return 0, 0
	}

	refreshed, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey)
	if err != nil {
		debugLog(opts.Logger, "x media summary refresh failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return 0, 1
	}
	debugLog(opts.Logger, "x media summary saved", "source_key", item.SourceKey, "item_id", item.ID, "summary_chars", len(refreshed.SummaryText), "model", refreshed.SummaryModel, "tool", refreshed.SummaryTool)
	return 1, 0
}
