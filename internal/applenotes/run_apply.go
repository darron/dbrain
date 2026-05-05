package applenotes

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func recordAppleNoteUpsertStats(stats *Stats, status model.UpsertStatus) {
	switch status {
	case model.UpsertCreated:
		stats.NotesCreated++
	case model.UpsertUpdated:
		stats.NotesUpdated++
	case model.UpsertUnchanged:
		stats.NotesUnchanged++
	}
}

func renderImportedAppleNote(cfg config.Config, opts Options, item model.Item, result model.UpsertResult, plan appleNoteWorkPlan) (bool, error) {
	shouldRender := opts.Force || result.Status != model.UpsertUnchanged || plan.RenderNeeded
	if !shouldRender {
		if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
			shouldRender = true
		}
	}
	if !shouldRender {
		return false, nil
	}
	if err := vault.WriteItem(cfg, item); err != nil {
		return false, fmt.Errorf("render apple note %s: %w", item.SourceKey, err)
	}
	return true, nil
}

func summarizeImportedAppleNote(ctx context.Context, cfg config.Config, st *store.Store, opts Options, result model.UpsertResult, item model.Item, stats *Stats, event *ProgressEvent) bool {
	if !opts.Summarize {
		return true
	}
	event.Phase = "summarizing"
	event.Status = string(result.Status)
	event.SummaryStatus = "running"
	emitProgress(opts, *event)

	summarized, err := summarizeAppleNote(ctx, cfg, st, opts, result.ItemID, item)
	if err != nil {
		stats.SummaryErrors++
		stats.Errors++
		event.Phase = "imported"
		event.SummaryStatus = "error"
		event.Reason = err.Error()
		emitProgress(opts, *event)
		return false
	}
	if summarized {
		stats.SummariesCreated++
		stats.NotesRendered++
		event.SummaryStatus = "ok"
		event.SummaryChanged = true
	} else {
		event.SummaryStatus = "current"
	}
	return true
}
