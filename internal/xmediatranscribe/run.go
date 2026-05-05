package xmediatranscribe

import (
	"context"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	opts = normalizeOptions(cfg, opts)

	items, err := st.ListItemsForXMediaTranscription(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{ItemsQueued: len(items)}
	debugLog(opts.Logger, "x media transcription candidates loaded", "items", len(items), "limit", opts.Limit, "force", opts.Force)

	for _, item := range items {
		stats.ItemsProcessed++
		debugLog(opts.Logger, "transcribing x media item", "source_key", item.SourceKey, "item_id", item.ID)

		refs, err := st.ListItemMediaRefs(ctx, item.ID)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media refs failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
			continue
		}
		item.Media = refs

		if shouldSkipItem(item, opts.Force) {
			stats.ItemsSkipped++
			debugLog(opts.Logger, "skipping x media transcription item", "source_key", item.SourceKey, "item_id", item.ID, "reason", "existing article text")
			continue
		}

		blocks, blockStats, outcome := transcribeItemMedia(ctx, cfg, refs, opts)
		stats.MediaCandidates += blockStats.MediaCandidates
		stats.MediaWithAudio += blockStats.MediaWithAudio
		stats.MediaTranscribed += blockStats.MediaTranscribed
		stats.Errors += blockStats.Errors

		if len(blocks) == 0 {
			if outcome.Status != "" {
				if err := st.SaveXMediaTranscriptionState(ctx, item.ID, outcome.Status, outcome.Error, time.Now().UTC()); err != nil {
					stats.Errors++
					debugLog(opts.Logger, "x media transcription state save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
				}
			}
			stats.ItemsSkipped++
			debugLog(opts.Logger, "x media transcription produced no transcript", "source_key", item.SourceKey, "item_id", item.ID)
			continue
		}

		changed, err := saveTranscriptItem(ctx, cfg, st, opts, item, blocks)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media transcription save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
			continue
		}
		if changed {
			stats.ItemsUpdated++
		} else {
			stats.ItemsUnchanged++
		}
	}

	if opts.Summarize {
		summaryStats := summarizeTranscriptItems(ctx, cfg, st, opts)
		stats.SummaryCandidates += summaryStats.SummaryCandidates
		stats.ItemsSummarized += summaryStats.ItemsSummarized
		stats.SummaryErrors += summaryStats.SummaryErrors
	}

	return stats, nil
}

func shouldSkipItem(item model.Item, force bool) bool {
	if force {
		return false
	}
	return strings.TrimSpace(item.ArticleText) != "" && strings.TrimSpace(item.ArticleTitle) != transcriptArticleTitle
}
