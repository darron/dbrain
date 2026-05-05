package applenotes

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

type appleNoteWorkPlan struct {
	Actionable    bool
	RenderNeeded  bool
	SummaryNeeded bool
	Reason        string
}

func planAppleNoteWork(ctx context.Context, cfg config.Config, st *store.Store, opts Options, item model.Item) (appleNoteWorkPlan, error) {
	if opts.Force {
		return appleNoteWorkPlan{Actionable: true, RenderNeeded: true, SummaryNeeded: opts.Summarize, Reason: "force"}, nil
	}
	current, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		if isItemNotFound(err) {
			return appleNoteWorkPlan{Actionable: true, RenderNeeded: true, SummaryNeeded: opts.Summarize, Reason: "new"}, nil
		}
		return appleNoteWorkPlan{}, err
	}

	plan := appleNoteWorkPlan{}
	if current.ContentHash != item.ContentHash {
		plan.Actionable = true
		plan.Reason = appendReason(plan.Reason, "changed")
	}
	if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
		plan.Actionable = true
		plan.RenderNeeded = true
		plan.Reason = appendReason(plan.Reason, "missing_render")
	}
	if opts.Summarize && appleNoteSummaryNeeded(current, item) {
		plan.Actionable = true
		plan.SummaryNeeded = true
		plan.Reason = appendReason(plan.Reason, "summary")
	}
	return plan, nil
}

func appleNoteSummaryNeeded(current model.Item, item model.Item) bool {
	input := appleNoteSummaryInput(item)
	if input == "" {
		return false
	}
	inputHash := hashSummaryInput(input)
	return current.SummaryStatus != "ok" ||
		strings.TrimSpace(current.SummaryText) == "" ||
		strings.TrimSpace(current.SummaryInputHash) != inputHash ||
		strings.TrimSpace(current.SummaryPromptVersion) != appleNoteSummaryPromptVersion
}

func isItemNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "item not found:")
}

func appendReason(current string, next string) string {
	if current == "" {
		return next
	}
	return current + "," + next
}
