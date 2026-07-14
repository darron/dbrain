package xmediatranscribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
)

const xMediaTranscriptionToolVersion = "xmediatranscribe-v1"

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
		settings := resolvedTranscriptionSettings(block)
		provenance = append(provenance, map[string]any{"backend": settings.Backend, "model": settings.Model, "language": settings.Language, "vad_enabled": settings.VADEnabled})
	}
	metadata, err := json.Marshal(provenance)
	if err != nil {
		return changed, fmt.Errorf("marshal x media transcription provenance: %w", err)
	}
	settings, err := homogeneousTranscriptionProvenance(blocks)
	if err != nil {
		return changed, err
	}
	if err := st.SaveXMediaTranscription(ctx, item.ID, store.XMediaTranscriptionState{
		Status: model.XMediaTranscriptStatusOK, RawJSON: string(metadata), Model: settings.Model,
		Tool: settings.Backend, ToolVersion: xMediaTranscriptionToolVersion, InputSettings: settings,
		CompletedAt: time.Now().UTC(),
	}); err != nil {
		return changed, fmt.Errorf("save x media transcription state: %w", err)
	}

	if _, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey); err != nil {
		return changed, fmt.Errorf("write x media transcript note: %w", err)
	}
	debugLog(opts.Logger, "x media transcription saved", "source_key", item.SourceKey, "item_id", item.ID, "changed", changed)
	return changed, nil
}

func homogeneousTranscriptionProvenance(blocks []transcriptBlock) (store.XMediaTranscriptionInputSettings, error) {
	if len(blocks) == 0 {
		return store.XMediaTranscriptionInputSettings{}, fmt.Errorf("x media transcript provenance requires at least one block")
	}
	settings := resolvedTranscriptionSettings(blocks[0])
	for _, block := range blocks[1:] {
		if other := resolvedTranscriptionSettings(block); other != settings {
			return store.XMediaTranscriptionInputSettings{}, fmt.Errorf("x media transcript blocks used inconsistent resolved transcription settings")
		}
	}
	return settings, nil
}

func resolvedTranscriptionSettings(block transcriptBlock) store.XMediaTranscriptionInputSettings {
	backend := strings.TrimSpace(block.Backend)
	modelName := strings.TrimSpace(block.Model)
	if modelName == "" {
		modelName = "default"
	}
	language := strings.TrimSpace(block.Language)
	if language == "" {
		language = "auto"
	}
	return store.XMediaTranscriptionInputSettings{
		Backend: backend, Model: modelName, Language: language, VADEnabled: block.VADEnabled,
	}
}
