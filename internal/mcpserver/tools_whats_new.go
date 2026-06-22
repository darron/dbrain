package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/store"
)

const (
	defaultMCPWhatsNewLimit = 50
	maxMCPWhatsNewLimit     = 500
)

func (s *Server) toolWhatsNew(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Since  string   `json:"since"`
		Cursor string   `json:"cursor"`
		Limit  int      `json:"limit"`
		Types  []string `json:"types"`
		View   string   `json:"view"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode whats-new args: %w", err)
	}

	cursor, err := mcpReviewCursor(args.Since, args.Cursor)
	if err != nil {
		return nil, err
	}
	if _, err := store.NormalizeReviewEventTypes(args.Types); err != nil {
		return nil, err
	}
	if _, err := store.NormalizeReviewEventView(args.View); err != nil {
		return nil, err
	}

	page, err := s.st.ListReviewEvents(ctx, store.ReviewEventFilter{
		Cursor: cursor,
		Limit:  clampMCPWhatsNewLimit(args.Limit),
		Types:  args.Types,
		View:   args.View,
	})
	if err != nil {
		return nil, err
	}
	return toolOKResult(formatWhatsNewMCPText(page), page), nil
}

func mcpReviewCursor(since string, cursorToken string) (store.ReviewCursor, error) {
	since = strings.TrimSpace(since)
	cursorToken = strings.TrimSpace(cursorToken)
	if (since == "") == (cursorToken == "") {
		return store.ReviewCursor{}, fmt.Errorf("exactly one of since or cursor is required")
	}
	if cursorToken != "" {
		return store.DecodeReviewCursor(cursorToken)
	}
	return store.ParseReviewSince(since, timeNowUTC())
}

func clampMCPWhatsNewLimit(value int) int {
	if value <= 0 {
		value = defaultMCPWhatsNewLimit
	}
	if value > maxMCPWhatsNewLimit {
		return maxMCPWhatsNewLimit
	}
	return value
}

func formatWhatsNewMCPText(page store.ReviewEventPage) string {
	var b strings.Builder
	if page.View == store.ReviewEventViewEntities {
		_, _ = fmt.Fprintf(&b, "Entities: %d", len(page.Entities))
	} else {
		_, _ = fmt.Fprintf(&b, "Events: %d", len(page.Events))
	}
	if page.Truncated {
		b.WriteString(" (truncated)")
	}
	if !page.HighWatermark.IsZero() {
		_, _ = fmt.Fprintf(&b, "\nHigh watermark: %s", page.HighWatermark.Format("2006-01-02T15:04:05Z07:00"))
	}
	if len(page.Counts) > 0 {
		b.WriteString("\nCounts:")
		for _, count := range page.Counts {
			_, _ = fmt.Fprintf(&b, "\n- %s: %d", count.Key, count.Count)
		}
	} else {
		b.WriteString("\nCounts: none")
	}
	if page.View == store.ReviewEventViewEntities && len(page.Entities) > 0 {
		b.WriteString("\nTop entities:")
		limit := len(page.Entities)
		if limit > 10 {
			limit = 10
		}
		for _, entity := range page.Entities[:limit] {
			label := entity.Title
			if label == "" {
				label = entity.EntityKey
			}
			_, _ = fmt.Fprintf(&b, "\n- %s %s", entity.EntityKey, label)
			if len(entity.EventKinds) > 0 {
				_, _ = fmt.Fprintf(&b, " (%s)", strings.Join(entity.EventKinds, ", "))
			}
		}
	}
	if strings.TrimSpace(page.NextCursor) != "" {
		_, _ = fmt.Fprintf(&b, "\nNext cursor: %s", page.NextCursor)
	}
	return b.String()
}
