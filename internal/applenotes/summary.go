package applenotes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func summarizeAppleNote(ctx context.Context, cfg config.Config, st *store.Store, opts Options, itemID int64, item model.Item) (bool, error) {
	input := appleNoteSummaryInput(item)
	if input == "" {
		return false, nil
	}
	inputHash := hashSummaryInput(input)
	if !opts.Force {
		current, err := st.GetItem(ctx, item.SourceKey)
		if err != nil {
			return false, err
		}
		if current.SummaryStatus == "ok" &&
			strings.TrimSpace(current.SummaryText) != "" &&
			strings.TrimSpace(current.SummaryInputHash) == inputHash &&
			strings.TrimSpace(current.SummaryPromptVersion) == appleNoteSummaryPromptVersion {
			return false, nil
		}
	}
	modelName := summaryconfig.Model(cfg.RootDir, opts.SummaryModel)
	length := strings.TrimSpace(opts.SummaryLength)
	if length == "" {
		length = "medium"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Summarize: true,
		Stdin:     input,
		Model:     modelName,
		CLI:       opts.SummaryCLI,
		Length:    length,
		Timeout:   timeout,
		Prompt:    appleNoteSummaryPrompt,
		RootDir:   cfg.RootDir,
	})

	summary := model.SummaryResult{
		Model:         modelName,
		PromptVersion: appleNoteSummaryPromptVersion,
		Status:        "ok",
		FetchedAt:     time.Now().UTC(),
	}
	if err != nil {
		summary.Status = "error"
		summary.Error = err.Error()
		summary.Tool = summarizecli.SummaryToolName(modelName)
		summary.ToolVersion = summarizecli.SummaryToolVersion(ctx, "summarize", modelName)
	} else {
		summary = runResult.Summary
		summary.PromptVersion = appleNoteSummaryPromptVersion
	}

	changed, err := st.SaveItemSummary(ctx, itemID, summary, inputHash)
	if err != nil {
		return false, err
	}
	if summary.Status != "ok" {
		return false, fmt.Errorf("summarize apple note %s: %s", item.SourceKey, summary.Error)
	}
	if !changed {
		return false, nil
	}
	if _, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey); err != nil {
		return false, err
	}
	return true, nil
}

func appleNoteSummaryInput(item model.Item) string {
	var b strings.Builder
	if text := strings.TrimSpace(item.Text); text != "" {
		b.WriteString("Apple Note Body:\n")
		b.WriteString(text)
	}
	if attachmentText := strings.TrimSpace(item.ArticleText); attachmentText != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Apple Note Attachments:\n")
		b.WriteString(attachmentText)
	}
	return strings.TrimSpace(b.String())
}

func hashSummaryInput(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}
