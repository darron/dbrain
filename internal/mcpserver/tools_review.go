package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

func (s *Server) toolWhatsNew(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Since  string   `json:"since"`
		Cursor string   `json:"cursor"`
		Limit  int      `json:"limit"`
		Types  []string `json:"types"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode whats-new args: %w", err)
	}
	cursor, err := store.ParseReviewCursorInput(time.Now(), args.Since, args.Cursor)
	if err != nil {
		return nil, err
	}
	feed, err := s.st.ListReviewEvents(ctx, store.ReviewEventFilter{
		Cursor: cursor,
		Limit:  args.Limit,
		Types:  args.Types,
	})
	if err != nil {
		return nil, err
	}
	text := formatWhatsNewToolText(feed)
	return toolOKResult(text, feed), nil
}

func formatWhatsNewToolText(feed store.ReviewEventFeed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review events: %d", len(feed.Events))
	if feed.Truncated {
		b.WriteString(" (truncated)")
	}
	if !feed.HighWatermark.IsZero() {
		fmt.Fprintf(&b, "\nHigh watermark: %s", feed.HighWatermark.Format(time.RFC3339))
	}
	for _, event := range feed.Events {
		fmt.Fprintf(&b, "\n- [%s] %s %s", event.EventKind, event.EntityKey, firstNonEmpty(event.Title, event.URL))
		if event.Status != "" {
			fmt.Fprintf(&b, " status=%s", event.Status)
		}
	}
	if feed.NextCursor != "" {
		fmt.Fprintf(&b, "\nNext cursor: %s", feed.NextCursor)
	}
	return b.String()
}
