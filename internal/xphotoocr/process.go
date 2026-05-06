package xphotoocr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
)

func processOCRItem(ctx context.Context, cfg config.Config, st *store.Store, opts Options, item model.Item) ocrItemOutcome {
	outcome := ocrItemOutcome{}
	if err := ctx.Err(); err != nil {
		return outcome
	}

	refs, err := st.ListItemMediaRefs(ctx, item.ID)
	if err != nil {
		if isContextCanceled(err) || ctx.Err() != nil {
			return outcome
		}
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr refs failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	photos := downloadedPhotoRefs(refs)
	outcome.photoCandidates = len(photos)
	if len(photos) == 0 {
		outcome.itemsSkipped++
		return outcome
	}

	blocks := make([]ocrBlock, 0, len(photos))
	lastErr := ""
	toolsUsed := map[string]struct{}{}
	modelsUsed := map[string]struct{}{}
	for _, ref := range photos {
		if err := ctx.Err(); err != nil {
			return outcome
		}
		absolutePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
		block, hostedAttempted, hostedFallback, err := ocrPhoto(ctx, absolutePath, ref, opts)
		if hostedAttempted {
			outcome.hostedAttempts++
		}
		if hostedFallback {
			outcome.hostedFallbacks++
		}
		if err != nil {
			if isContextCanceled(err) || ctx.Err() != nil {
				return outcome
			}
			lastErr = err.Error()
			debugLog(opts.Logger, "x photo ocr failed", "source_key", item.SourceKey, "item_id", item.ID, "local_path", ref.LocalPath, "error", err.Error())
			continue
		}
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		blocks = append(blocks, block)
		toolsUsed[block.Tool] = struct{}{}
		if strings.TrimSpace(block.Model) != "" {
			modelsUsed[block.Model] = struct{}{}
		}
		outcome.photosOCRed++
	}

	inputHash := hashPhotoInputs(photos)
	if err := ctx.Err(); err != nil {
		return outcome
	}
	if len(blocks) == 0 {
		_, saveErr := st.SaveItemOCR(ctx, item.ID, model.OCRResult{
			Status:      model.ItemOCRStatusError,
			Error:       firstNonEmpty(lastErr, "no OCR text extracted"),
			FetchedAt:   time.Now().UTC(),
			Tool:        collapseSet(toolsUsed),
			ToolVersion: collapseToolVersion(toolsUsed),
			Model:       collapseSet(modelsUsed),
		}, inputHash)
		if saveErr != nil {
			if isContextCanceled(saveErr) || ctx.Err() != nil {
				return outcome
			}
			outcome.errors++
			debugLog(opts.Logger, "x photo ocr state save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", saveErr.Error())
		} else {
			outcome.errors++
		}
		outcome.itemsSkipped++
		return outcome
	}

	rawJSON, _ := json.Marshal(blocks)
	changed, err := st.SaveItemOCR(ctx, item.ID, model.OCRResult{
		Text:        renderOCRBlocks(blocks),
		RawJSON:     string(rawJSON),
		Status:      model.ItemOCRStatusOK,
		Model:       collapseSet(modelsUsed),
		FetchedAt:   time.Now().UTC(),
		Tool:        collapseSet(toolsUsed),
		ToolVersion: collapseToolVersion(toolsUsed),
	}, inputHash)
	if err != nil {
		if isContextCanceled(err) || ctx.Err() != nil {
			return outcome
		}
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	if !changed {
		outcome.itemsUnchanged++
		debugLog(opts.Logger, "x photo ocr unchanged", "source_key", item.SourceKey, "item_id", item.ID)
		return outcome
	}

	refreshed, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey)
	if err != nil {
		if isContextCanceled(err) || ctx.Err() != nil {
			return outcome
		}
		outcome.errors++
		debugLog(opts.Logger, "x photo ocr refresh failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
		return outcome
	}
	outcome.itemsUpdated++
	debugLog(opts.Logger, "x photo ocr saved", "source_key", item.SourceKey, "item_id", item.ID, "ocr_chars", len(refreshed.OCRText), "tool", refreshed.OCRTool, "model", refreshed.OCRModel)
	return outcome
}

func downloadedPhotoRefs(refs []model.ItemMediaRef) []model.ItemMediaRef {
	photos := make([]model.ItemMediaRef, 0, len(refs))
	for _, ref := range refs {
		if ref.MediaType != "photo" || ref.DownloadStatus != "downloaded" || strings.TrimSpace(ref.LocalPath) == "" || !ref.LocalPrunedAt.IsZero() {
			continue
		}
		photos = append(photos, ref)
	}
	return photos
}
