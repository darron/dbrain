package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

type whatsNewFlags struct {
	since  string
	cursor string
	types  string
	limit  int
	json   bool
}

func newWhatsNewCommand(root *rootOptions) *cobra.Command {
	var flags whatsNewFlags
	cmd := &cobra.Command{
		Use:   "whats-new",
		Short: "Review new imports, enrichments, and failures since a timestamp or cursor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			now := time.Now()
			cursor, err := store.ParseReviewCursorInput(now, flags.since, flags.cursor)
			if err != nil {
				return err
			}
			feed, err := st.ListReviewEvents(cmd.Context(), store.ReviewEventFilter{
				Cursor: cursor,
				Limit:  flags.limit,
				Types:  splitCommaFlag(flags.types),
				Now:    now,
			})
			if err != nil {
				return err
			}
			if flags.json {
				return writeJSON(cmd.OutOrStdout(), feed)
			}
			return writeWhatsNewDigest(cmd.OutOrStdout(), cursor, feed)
		},
	}
	cmd.Flags().StringVar(&flags.since, "since", "", "RFC3339 timestamp or relative duration such as 2h, 24h, or 7d")
	cmd.Flags().StringVar(&flags.cursor, "cursor", "", "Review cursor returned by a previous whats-new response")
	cmd.Flags().StringVar(&flags.types, "types", "all", "Comma-separated groups: all, imports, enrichments, failures, categorization")
	cmd.Flags().IntVar(&flags.limit, "limit", 100, "Maximum number of events to return")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Write JSON output")
	return cmd
}

func writeWhatsNewDigest(dst interface{ Write([]byte) (int, error) }, cursor store.ReviewCursor, feed store.ReviewEventFeed) error {
	if _, err := fmt.Fprintf(dst, "What's new since %s\n\n", cursorDisplayTime(cursor.EventAt)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Events: %d", len(feed.Events)); err != nil {
		return err
	}
	if feed.Truncated {
		if _, err := fmt.Fprint(dst, " (truncated)"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(dst); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "High watermark: %s", cursorDisplayTime(feed.HighWatermark)); err != nil {
		return err
	}
	if feed.HighWatermarkAge != "" {
		if _, err := fmt.Fprintf(dst, " (%s)", feed.HighWatermarkAge); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(dst); err != nil {
		return err
	}
	if err := writeWhatsNewCounts(dst, feed.Counts); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(dst); err != nil {
		return err
	}

	if len(feed.Events) == 0 {
		if _, err := fmt.Fprintln(dst, "No reviewable changes."); err != nil {
			return err
		}
	} else {
		if err := writeWhatsNewEventSection(dst, "High-signal review", feed.Events, false); err != nil {
			return err
		}
		if err := writeWhatsNewEventSection(dst, "Failures and blocked", feed.Events, true); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(dst, "\nNext cursor: %s\n", feed.NextCursor); err != nil {
		return err
	}
	return nil
}

func writeWhatsNewCounts(dst interface{ Write([]byte) (int, error) }, counts store.ReviewEventCounts) error {
	lines := []struct {
		label   string
		buckets []store.CountBucket
	}{
		{"By kind", counts.ByKind},
		{"By source", counts.BySourceType},
		{"By status", counts.ByStatus},
	}
	for _, line := range lines {
		if len(line.buckets) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(dst, "%s: %s\n", line.label, formatCountBucketsInline(line.buckets)); err != nil {
			return err
		}
	}
	return nil
}

func writeWhatsNewEventSection(dst interface{ Write([]byte) (int, error) }, title string, events []store.ReviewEvent, failuresOnly bool) error {
	wroteTitle := false
	for _, event := range events {
		isFailure := event.EventKind == store.ReviewEventKindFailed || event.EventKind == store.ReviewEventKindBlocked
		if failuresOnly != isFailure {
			continue
		}
		if !wroteTitle {
			if _, err := fmt.Fprintf(dst, "%s\n", title); err != nil {
				return err
			}
			wroteTitle = true
		}
		if _, err := fmt.Fprintf(dst, "- [%s] %s %s\n", event.EventKind, event.EntityKey, firstNonEmpty(event.Title, event.URL)); err != nil {
			return err
		}
		if event.URL != "" {
			if _, err := fmt.Fprintf(dst, "  %s\n", event.URL); err != nil {
				return err
			}
		}
		if len(event.Tags) > 0 {
			if _, err := fmt.Fprintf(dst, "  tags: %s\n", strings.Join(event.Tags, ", ")); err != nil {
				return err
			}
		}
		if event.Status != "" {
			if _, err := fmt.Fprintf(dst, "  status: %s\n", event.Status); err != nil {
				return err
			}
		}
		if event.Message != "" {
			if _, err := fmt.Fprintf(dst, "  message: %s\n", truncateForDigest(event.Message, 220)); err != nil {
				return err
			}
		}
		if event.Summary != "" {
			if _, err := fmt.Fprintf(dst, "  summary: %s\n", truncateForDigest(event.Summary, 320)); err != nil {
				return err
			}
		}
		if event.EventAtLocal != "" {
			if _, err := fmt.Fprintf(dst, "  at: %s", event.EventAtLocal); err != nil {
				return err
			}
			if event.EventAge != "" {
				if _, err := fmt.Fprintf(dst, " (%s)", event.EventAge); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(dst); err != nil {
				return err
			}
		}
	}
	if wroteTitle {
		if _, err := fmt.Fprintln(dst); err != nil {
			return err
		}
	}
	return nil
}

func formatCountBucketsInline(buckets []store.CountBucket) string {
	parts := make([]string, 0, len(buckets))
	for _, bucket := range buckets {
		parts = append(parts, fmt.Sprintf("%s=%d", bucket.Key, bucket.Count))
	}
	return strings.Join(parts, ", ")
}

func cursorDisplayTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func truncateForDigest(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return strings.Repeat(".", limit)
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func splitCommaFlag(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
