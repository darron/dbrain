package xmediatranscribe

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func saveTranscriptItem(ctx context.Context, cfg config.Config, st *store.Store, opts Options, item model.Item, blocks []transcriptBlock) (bool, error) {
	item.ArticleTitle = transcriptArticleTitle
	item.ArticleText = renderTranscriptBlocks(blocks)
	item.ContentHash = itemhash.Compute(item)
	item.UpdatedAt = time.Now().UTC()

	result, err := st.UpsertItem(ctx, item)
	if err != nil {
		return false, err
	}
	changed := result.Status != model.UpsertUnchanged
	if changed {
		if err := st.InvalidateItemSummary(ctx, result.ItemID); err != nil {
			return false, fmt.Errorf("invalidate x media summary: %w", err)
		}
		item.SummaryText = ""
		item.SummaryJSON = ""
		item.SummaryStatus = ""
		item.SummaryError = ""
		item.SummaryModel = ""
		item.SummaryPromptVersion = ""
		item.SummaryTool = ""
		item.SummaryToolVersion = ""
		item.SummaryInputHash = ""
		item.SummarizedAt = time.Time{}
	}
	if err := st.SaveXMediaTranscriptionState(ctx, item.ID, "ok", "", time.Now().UTC()); err != nil {
		return changed, fmt.Errorf("save x media transcription state: %w", err)
	}

	if err := vault.WriteItem(cfg, item); err != nil {
		return changed, fmt.Errorf("write x media transcript note: %w", err)
	}
	debugLog(opts.Logger, "x media transcription saved", "source_key", item.SourceKey, "item_id", item.ID, "changed", changed)
	return changed, nil
}
