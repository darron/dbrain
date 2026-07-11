package xmediatranscribe

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
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
	provenance := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		provenance = append(provenance, map[string]any{"backend": block.Backend, "model": block.Model, "language": block.Language, "vad_enabled": block.VADEnabled})
	}
	metadata, err := json.Marshal(provenance)
	if err != nil {
		return changed, fmt.Errorf("marshal x media transcription provenance: %w", err)
	}
	tool, usedModel := homogeneousTranscriptionProvenance(blocks)
	if err := st.SaveXMediaTranscription(ctx, item.ID, store.XMediaTranscriptionState{
		Status: model.XMediaTranscriptStatusOK, RawJSON: string(metadata), Tool: tool, Model: usedModel, CompletedAt: time.Now().UTC(),
	}); err != nil {
		return changed, fmt.Errorf("save x media transcription state: %w", err)
	}

	if _, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey); err != nil {
		return changed, fmt.Errorf("write x media transcript note: %w", err)
	}
	debugLog(opts.Logger, "x media transcription saved", "source_key", item.SourceKey, "item_id", item.ID, "changed", changed)
	return changed, nil
}

func homogeneousTranscriptionProvenance(blocks []transcriptBlock) (string, string) {
	if len(blocks) == 0 {
		return "", ""
	}
	tool, usedModel := blocks[0].Backend, blocks[0].Model
	for _, block := range blocks[1:] {
		if block.Backend != tool {
			tool = ""
		}
		if block.Model != usedModel {
			usedModel = ""
		}
	}
	return tool, usedModel
}
