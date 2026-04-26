package xmediatranscribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
	"dbrain/internal/vault"
	"dbrain/internal/xpost"
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
	})

	summary := model.SummaryResult{
		Model:         strings.TrimSpace(opts.SummaryModel),
		PromptVersion: xMediaSummaryPromptVersion,
		Status:        "ok",
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
	if summary.Status != "ok" {
		debugLog(opts.Logger, "x media summary failed", "source_key", item.SourceKey, "item_id", item.ID, "status", summary.Status, "error", summary.Error)
		return 0, 1
	}
	if !changed {
		return 0, 0
	}

	refreshed, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		debugLog(opts.Logger, "x media summary refresh failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return 0, 1
	}
	if err := vault.WriteItem(cfg, refreshed); err != nil {
		debugLog(opts.Logger, "x media summary note write failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return 0, 1
	}
	debugLog(opts.Logger, "x media summary saved", "source_key", item.SourceKey, "item_id", item.ID, "summary_chars", len(refreshed.SummaryText), "model", refreshed.SummaryModel, "tool", refreshed.SummaryTool)
	return 1, 0
}

func buildTranscriptSummaryInput(item model.Item) string {
	var b strings.Builder
	b.WriteString("X post context:\n")
	if snapshot, ok, _ := xpost.DecodeSnapshot(item.XPostJSON); ok && snapshot != nil {
		writeSnapshotSummaryContext(&b, snapshot, "Primary post", strings.TrimSpace(item.XPostText))
	} else if text := strings.TrimSpace(item.XPostText); text != "" {
		b.WriteString(text)
	} else {
		b.WriteString("(none)")
	}
	b.WriteString("\n\nVideo transcript:\n")
	b.WriteString(strings.TrimSpace(item.ArticleText))
	return b.String()
}

func writeSnapshotSummaryContext(b *strings.Builder, snapshot *xpost.Snapshot, label string, fallbackText string) {
	if snapshot == nil {
		b.WriteString("(none)")
		return
	}
	b.WriteString(label)
	b.WriteString(":\n")
	if snapshot.AuthorHandle != "" || snapshot.AuthorName != "" {
		b.WriteString("Author: ")
		if snapshot.AuthorName != "" {
			b.WriteString(snapshot.AuthorName)
			if snapshot.AuthorHandle != "" {
				b.WriteString(" ")
			}
		}
		if snapshot.AuthorHandle != "" {
			b.WriteString("(@")
			b.WriteString(snapshot.AuthorHandle)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if url := strings.TrimSpace(snapshot.URL); url != "" {
		b.WriteString("URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if text := firstNonEmptyText(strings.TrimSpace(snapshot.Text), strings.TrimSpace(fallbackText)); text != "" {
		b.WriteString(text)
		b.WriteString("\n")
	} else {
		b.WriteString("(no text)\n")
	}
	if snapshot.QuotedPost != nil {
		b.WriteString("\n")
		writeSnapshotSummaryContext(b, snapshot.QuotedPost, "Quoted post", "")
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func hashSummaryInput(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func summaryResultFromError(opts Options, err error) model.SummaryResult {
	status := "error"
	if isBlockedSummaryError(err) {
		status = "blocked"
	}
	return model.SummaryResult{
		Model:         strings.TrimSpace(opts.SummaryModel),
		PromptVersion: xMediaSummaryPromptVersion,
		Status:        status,
		Error:         err.Error(),
		Tool:          summarizecli.SummaryToolName(opts.SummaryModel),
		ToolVersion:   summarizecli.SummaryToolVersion(context.Background(), "summarize", opts.SummaryModel),
	}
}

func isBlockedSummaryError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "maximum context length") ||
		strings.Contains(message, "context length") ||
		strings.Contains(message, "too many tokens") ||
		strings.Contains(message, "input is too long")
}
