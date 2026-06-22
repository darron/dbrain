package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ParseReviewSince(value string, now time.Time) (ReviewCursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ReviewCursor{}, fmt.Errorf("since must be RFC3339 or a relative duration")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return ReviewCursor{EventAt: t.UTC()}, nil
	}
	durationValue := value
	if strings.HasSuffix(durationValue, "d") {
		daysText := strings.TrimSuffix(durationValue, "d")
		days, err := strconv.Atoi(daysText)
		if err != nil || days <= 0 {
			return ReviewCursor{}, fmt.Errorf("since must be RFC3339 or a relative duration")
		}
		durationValue = fmt.Sprintf("%dh", days*24)
	}
	duration, err := time.ParseDuration(durationValue)
	if err != nil || duration <= 0 {
		return ReviewCursor{}, fmt.Errorf("since must be RFC3339 or a relative duration")
	}
	return ReviewCursor{EventAt: now.UTC().Add(-duration)}, nil
}

func EncodeReviewCursor(cursor ReviewCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode review cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeReviewCursor(token string) (ReviewCursor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ReviewCursor{}, fmt.Errorf("cursor is required")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ReviewCursor{}, fmt.Errorf("decode review cursor: %w", err)
	}
	var cursor ReviewCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return ReviewCursor{}, fmt.Errorf("decode review cursor JSON: %w", err)
	}
	cursor.EventAt = cursor.EventAt.UTC()
	return cursor, nil
}

func NormalizeReviewEventView(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ReviewEventViewEvents, nil
	}
	switch value {
	case ReviewEventViewEvents, ReviewEventViewEntities:
		return value, nil
	default:
		return "", fmt.Errorf("unknown review event view %q", value)
	}
}

func NormalizeReviewEventTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return append([]string{}, reviewEventAllKinds...), nil
	}
	requested := map[string]bool{}
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			switch value {
			case reviewEventTypeAll:
				return append([]string{}, reviewEventAllKinds...), nil
			case reviewEventTypeImports, reviewEventTypeEnrichments, reviewEventTypeFailures:
				requested[value] = true
			default:
				return nil, fmt.Errorf("unknown review event type %q", value)
			}
		}
	}
	if len(requested) == 0 {
		return append([]string{}, reviewEventAllKinds...), nil
	}
	kinds := make([]string, 0, len(reviewEventAllKinds))
	addGroup := func(group string, values []string) {
		if !requested[group] {
			return
		}
		kinds = append(kinds, values...)
	}
	addGroup(reviewEventTypeImports, reviewEventImportKinds)
	addGroup(reviewEventTypeEnrichments, reviewEventEnrichmentKinds)
	addGroup(reviewEventTypeFailures, reviewEventFailureKinds)
	return kinds, nil
}
