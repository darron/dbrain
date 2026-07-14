package xmediatranscribe

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	provenance, err := buildTranscriptProvenance(blocks)
	if err != nil {
		return false, err
	}
	inputHash, err := st.XMediaTranscriptionInputHash(ctx, item.ID, provenance.Settings)
	if err != nil {
		return false, fmt.Errorf("prepare x media transcription provenance: %w", err)
	}

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
	if err := st.SaveXMediaTranscription(ctx, item.ID, store.XMediaTranscriptionState{
		Status: model.XMediaTranscriptStatusOK, RawJSON: provenance.RawJSON, Model: provenance.Model,
		Tool: provenance.Tool, ToolVersion: xMediaTranscriptionToolVersion, InputHash: inputHash,
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

type transcriptProvenance struct {
	RawJSON  string
	Model    string
	Tool     string
	Settings []store.XMediaTranscriptionInputSettings
}

func buildTranscriptProvenance(blocks []transcriptBlock) (transcriptProvenance, error) {
	if len(blocks) == 0 {
		return transcriptProvenance{}, fmt.Errorf("x media transcript provenance requires at least one block")
	}
	settings := make([]store.XMediaTranscriptionInputSettings, 0, len(blocks))
	models := make([]string, 0, len(blocks))
	tools := make([]string, 0, len(blocks))
	for i, block := range blocks {
		resolved, err := resolvedTranscriptionSettings(block)
		if err != nil {
			return transcriptProvenance{}, fmt.Errorf("x media transcript block %d provenance: %w", i+1, err)
		}
		settings = append(settings, resolved)
		models = append(models, resolved.Model)
		tools = append(tools, resolved.Backend)
	}
	rawJSON, err := json.Marshal(settings)
	if err != nil {
		return transcriptProvenance{}, fmt.Errorf("marshal x media transcription provenance: %w", err)
	}
	modelName, err := aggregateTranscriptProvenanceValues(models)
	if err != nil {
		return transcriptProvenance{}, fmt.Errorf("aggregate x media transcription models: %w", err)
	}
	tool, err := aggregateTranscriptProvenanceValues(tools)
	if err != nil {
		return transcriptProvenance{}, fmt.Errorf("aggregate x media transcription tools: %w", err)
	}
	return transcriptProvenance{RawJSON: string(rawJSON), Model: modelName, Tool: tool, Settings: settings}, nil
}

func resolvedTranscriptionSettings(block transcriptBlock) (store.XMediaTranscriptionInputSettings, error) {
	backend := strings.TrimSpace(block.Backend)
	modelName := strings.TrimSpace(block.Model)
	language := strings.TrimSpace(block.Language)
	if language == "" {
		language = "auto"
	}
	if backend == "" || modelName == "" {
		return store.XMediaTranscriptionInputSettings{}, fmt.Errorf("resolved backend and model are required")
	}
	return store.XMediaTranscriptionInputSettings{
		Backend: backend, Model: modelName, Language: language, VADEnabled: block.VADEnabled,
	}, nil
}

func aggregateTranscriptProvenanceValues(values []string) (string, error) {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("provenance value is required")
		}
		unique[value] = struct{}{}
	}
	resolved := make([]string, 0, len(unique))
	for value := range unique {
		resolved = append(resolved, value)
	}
	sort.Strings(resolved)
	if len(resolved) == 1 {
		return resolved[0], nil
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
