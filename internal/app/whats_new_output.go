package app

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

func writeWhatsNewPage(dst io.Writer, page store.ReviewEventPage) error {
	if _, err := fmt.Fprintf(dst, "What's new since %s\n\n", formatWhatsNewTime(page.Cursor.EventAt)); err != nil {
		return err
	}
	if err := writeWhatsNewCounts(dst, page.Counts); err != nil {
		return err
	}
	if page.View == store.ReviewEventViewEntities {
		if err := writeWhatsNewEntities(dst, page.Entities); err != nil {
			return err
		}
		if len(page.Entities) == 0 {
			if _, err := fmt.Fprintln(dst, "No review entities."); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(dst, "\nNext cursor: %s\n", page.NextCursor); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "High watermark: %s\n", formatWhatsNewTime(page.HighWatermark)); err != nil {
			return err
		}
		_, err := fmt.Fprintf(dst, "Truncated: %t\n", page.Truncated)
		return err
	}
	if err := writeWhatsNewSection(dst, "Review", page.Events, func(event store.ReviewEvent) bool {
		return event.Actionability == store.ReviewActionabilityReview
	}); err != nil {
		return err
	}
	if err := writeWhatsNewSection(dst, "Background", page.Events, func(event store.ReviewEvent) bool {
		return event.Actionability == store.ReviewActionabilityBackground
	}); err != nil {
		return err
	}
	if err := writeWhatsNewSection(dst, "Failures and blocked", page.Events, func(event store.ReviewEvent) bool {
		return event.Actionability == store.ReviewActionabilityFailure || event.Actionability == store.ReviewActionabilityBlocked
	}); err != nil {
		return err
	}
	if len(page.Events) == 0 {
		if _, err := fmt.Fprintln(dst, "No review events."); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(dst, "\nNext cursor: %s\n", page.NextCursor); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "High watermark: %s\n", formatWhatsNewTime(page.HighWatermark)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(dst, "Truncated: %t\n", page.Truncated)
	return err
}

func writeWhatsNewEntities(dst io.Writer, entities []store.ReviewEntityGroup) error {
	if len(entities) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(dst, "Entities"); err != nil {
		return err
	}
	for _, entity := range entities {
		label := entity.Title
		if label == "" {
			label = entity.EntityKey
		}
		if _, err := fmt.Fprintf(dst, "- [%s] %s %s\n", entity.Actionability, entity.EntityKey, label); err != nil {
			return err
		}
		if strings.TrimSpace(entity.URL) != "" {
			if _, err := fmt.Fprintf(dst, "  %s\n", entity.URL); err != nil {
				return err
			}
		}
		if len(entity.EventKinds) > 0 {
			if _, err := fmt.Fprintf(dst, "  events: %s\n", strings.Join(entity.EventKinds, ", ")); err != nil {
				return err
			}
		}
		if len(entity.Tags) > 0 {
			if _, err := fmt.Fprintf(dst, "  tags: %s\n", strings.Join(entity.Tags, ", ")); err != nil {
				return err
			}
		}
		if value := truncateWhatsNewText(entity.Summary, 260); value != "" {
			if _, err := fmt.Fprintf(dst, "  summary: %s\n", value); err != nil {
				return err
			}
		}
		if value := truncateWhatsNewText(entity.Message, 220); value != "" {
			if _, err := fmt.Fprintf(dst, "  message: %s\n", value); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(dst)
	return err
}

func writeWhatsNewCounts(dst io.Writer, counts []store.CountBucket) error {
	if _, err := fmt.Fprintln(dst, "Counts"); err != nil {
		return err
	}
	if len(counts) == 0 {
		if _, err := fmt.Fprintln(dst, "- none: 0"); err != nil {
			return err
		}
	} else {
		for _, count := range counts {
			if _, err := fmt.Fprintf(dst, "- %s: %d\n", count.Key, count.Count); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(dst)
	return err
}

func writeWhatsNewSection(dst io.Writer, title string, events []store.ReviewEvent, include func(store.ReviewEvent) bool) error {
	var section []store.ReviewEvent
	for _, event := range events {
		if include(event) {
			section = append(section, event)
		}
	}
	if len(section) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(dst, title); err != nil {
		return err
	}
	for _, event := range section {
		if _, err := fmt.Fprintf(dst, "- [%s] %s %s\n", event.EventKind, event.EntityKey, event.Title); err != nil {
			return err
		}
		if strings.TrimSpace(event.URL) != "" {
			if _, err := fmt.Fprintf(dst, "  %s\n", event.URL); err != nil {
				return err
			}
		}
		if len(event.Tags) > 0 {
			if _, err := fmt.Fprintf(dst, "  tags: %s\n", strings.Join(event.Tags, ", ")); err != nil {
				return err
			}
		}
		if value := truncateWhatsNewText(event.Summary, 220); value != "" {
			if _, err := fmt.Fprintf(dst, "  summary: %s\n", value); err != nil {
				return err
			}
		}
		if value := truncateWhatsNewText(event.Message, 220); value != "" {
			if _, err := fmt.Fprintf(dst, "  message: %s\n", value); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(dst)
	return err
}

func truncateWhatsNewText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func formatWhatsNewTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}
