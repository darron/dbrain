package store

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) scanReviewEvents(ctx context.Context, query string, args ...any) ([]ReviewEvent, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list review events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	events := make([]ReviewEvent, 0)
	for rows.Next() {
		var event ReviewEvent
		var eventAt string
		var tags string
		if err := rows.Scan(
			&event.EventKind,
			&eventAt,
			&event.EntityKind,
			&event.EntityID,
			&event.EntityKey,
			&event.EventStage,
			&event.SourceType,
			&event.Title,
			&event.URL,
			&event.NotePath,
			&event.Summary,
			&tags,
			&event.Status,
			&event.Message,
		); err != nil {
			return nil, fmt.Errorf("scan review event: %w", err)
		}
		event.EventAt = parseStoredTime(eventAt)
		event.Tags = splitReviewTags(tags)
		decorateReviewEvent(&event)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review events: %w", err)
	}
	return events, nil
}

func splitReviewTags(raw string) []string {
	tags := make([]string, 0)
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		tag := strings.TrimSpace(part)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	return tags
}

func decorateReviewEvent(event *ReviewEvent) {
	event.EventID = fmt.Sprintf("%s:%d:%s:%s", event.EntityKind, event.EntityID, event.EventStage, event.EventKind)
	event.Actionability = reviewEventActionability(event.EventKind)
	event.Importance = reviewEventImportance(*event)
	event.Reasons = reviewEventReasons(*event)
	if event.Tags == nil {
		event.Tags = make([]string, 0)
	}
	if event.Reasons == nil {
		event.Reasons = make([]string, 0)
	}
}

func reviewEventActionability(kind string) string {
	switch kind {
	case ReviewEventKindSourceCreated, ReviewEventKindSourceExtracted:
		return ReviewActionabilityBackground
	case ReviewEventKindBlocked:
		return ReviewActionabilityBlocked
	case ReviewEventKindSourceFailed, ReviewEventKindItemEnrichmentFailed:
		return ReviewActionabilityFailure
	default:
		return ReviewActionabilityReview
	}
}

func reviewEventImportance(event ReviewEvent) int {
	score := 40
	switch event.Actionability {
	case ReviewActionabilityFailure, ReviewActionabilityBlocked:
		score += 50
	}
	if strings.TrimSpace(event.Summary) != "" {
		switch event.EventKind {
		case ReviewEventKindXMediaTranscribed, ReviewEventKindXPhotoOCRed:
			score += 25
		default:
			score += 30
		}
	}
	if len(event.Tags) > 0 {
		score += 20
	}
	if isHighIntentReviewSource(event.SourceType) {
		score += 10
	}
	if event.EventKind == ReviewEventKindSourceCreated {
		score -= 10
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func isHighIntentReviewSource(sourceType string) bool {
	switch sourceType {
	case "apple_note", "safari_tab", "x_bookmark", "mastodon_bookmark", "mastodon_quote", "mastodon_reblog", "github_star", "feed_entry":
		return true
	default:
		return false
	}
}

func reviewEventReasons(event ReviewEvent) []string {
	switch event.EventKind {
	case ReviewEventKindItemImported:
		return []string{"new local item"}
	case ReviewEventKindItemUpdated:
		return []string{"changed feed item"}
	case ReviewEventKindSourceCreated:
		return []string{"new linked source"}
	case ReviewEventKindSourceExtracted:
		return []string{"source extracted"}
	case ReviewEventKindSourceSummarized:
		return []string{"source summary ready"}
	case ReviewEventKindItemSummarized:
		return []string{"item summary ready"}
	case ReviewEventKindXMediaTranscribed:
		return []string{"media transcript ready"}
	case ReviewEventKindXPhotoOCRed:
		return []string{"photo OCR ready"}
	case ReviewEventKindSourceFailed:
		return []string{"source stage failed"}
	case ReviewEventKindItemEnrichmentFailed:
		return []string{"item enrichment failed"}
	case ReviewEventKindBlocked:
		return []string{"stage blocked"}
	default:
		return []string{"review event"}
	}
}
