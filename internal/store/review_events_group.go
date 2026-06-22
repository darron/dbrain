package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	reviewEntitySummaryMaxRunes = 1800
	reviewEntityMessageMaxRunes = 800
)

type reviewEntityAccumulator struct {
	group       ReviewEntityGroup
	eventKinds  map[string]bool
	tags        map[string]bool
	reasons     map[string]bool
	summaryRank int
	summaryAt   time.Time
	messageRank int
	messageAt   time.Time
}

func groupReviewEvents(events []ReviewEvent) []ReviewEntityGroup {
	if len(events) == 0 {
		return make([]ReviewEntityGroup, 0)
	}
	accs := make(map[string]*reviewEntityAccumulator)
	order := make([]string, 0)
	for _, event := range events {
		key := reviewEntityGroupingKey(event)
		acc, ok := accs[key]
		if !ok {
			acc = newReviewEntityAccumulator(event)
			accs[key] = acc
			order = append(order, key)
		}
		acc.add(event)
	}

	groups := make([]ReviewEntityGroup, 0, len(order))
	for _, key := range order {
		group := accs[key].group
		if group.EventKinds == nil {
			group.EventKinds = make([]string, 0)
		}
		if group.Tags == nil {
			group.Tags = make([]string, 0)
		}
		if group.Reasons == nil {
			group.Reasons = make([]string, 0)
		}
		if group.Events == nil {
			group.Events = make([]ReviewEntityEvent, 0)
		}
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Importance != groups[j].Importance {
			return groups[i].Importance > groups[j].Importance
		}
		if reviewActionabilityPriority(groups[i].Actionability) != reviewActionabilityPriority(groups[j].Actionability) {
			return reviewActionabilityPriority(groups[i].Actionability) > reviewActionabilityPriority(groups[j].Actionability)
		}
		if !groups[i].LatestEventAt.Equal(groups[j].LatestEventAt) {
			return groups[i].LatestEventAt.After(groups[j].LatestEventAt)
		}
		return groups[i].EntityKey < groups[j].EntityKey
	})
	return groups
}

func newReviewEntityAccumulator(event ReviewEvent) *reviewEntityAccumulator {
	return &reviewEntityAccumulator{
		group: ReviewEntityGroup{
			EntityKind:    event.EntityKind,
			EntityID:      event.EntityID,
			EntityKey:     event.EntityKey,
			SourceType:    event.SourceType,
			Title:         event.Title,
			URL:           event.URL,
			NotePath:      event.NotePath,
			FirstEventAt:  event.EventAt,
			LatestEventAt: event.EventAt,
			Tags:          make([]string, 0),
			Reasons:       make([]string, 0),
			EventKinds:    make([]string, 0),
			Events:        make([]ReviewEntityEvent, 0),
		},
		eventKinds: make(map[string]bool),
		tags:       make(map[string]bool),
		reasons:    make(map[string]bool),
	}
}

func (acc *reviewEntityAccumulator) add(event ReviewEvent) {
	group := &acc.group
	group.EventCount++
	if event.EventAt.Before(group.FirstEventAt) {
		group.FirstEventAt = event.EventAt
	}
	if event.EventAt.After(group.LatestEventAt) {
		group.LatestEventAt = event.EventAt
	}
	if group.Title == "" {
		group.Title = event.Title
	}
	if group.URL == "" {
		group.URL = event.URL
	}
	if group.NotePath == "" {
		group.NotePath = event.NotePath
	}
	if group.SourceType == "" {
		group.SourceType = event.SourceType
	}
	if group.Status == "" || event.EventAt.Equal(group.LatestEventAt) || event.EventAt.After(group.LatestEventAt) {
		group.Status = event.Status
	}
	if !acc.eventKinds[event.EventKind] {
		acc.eventKinds[event.EventKind] = true
		group.EventKinds = append(group.EventKinds, event.EventKind)
	}
	for _, tag := range event.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || acc.tags[tag] {
			continue
		}
		acc.tags[tag] = true
		group.Tags = append(group.Tags, tag)
	}
	for _, reason := range event.Reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" || acc.reasons[reason] {
			continue
		}
		acc.reasons[reason] = true
		group.Reasons = append(group.Reasons, reason)
	}
	if event.Importance > group.Importance {
		group.Importance = event.Importance
	}
	if reviewActionabilityPriority(event.Actionability) > reviewActionabilityPriority(group.Actionability) {
		group.Actionability = event.Actionability
	}
	if rank := reviewEntitySummaryRank(event); rank > 0 && (rank > acc.summaryRank || (rank == acc.summaryRank && event.EventAt.After(acc.summaryAt))) {
		acc.summaryRank = rank
		acc.summaryAt = event.EventAt
		group.Summary = compactReviewEntityText(event.Summary, reviewEntitySummaryMaxRunes)
		group.SummaryEventID = event.EventID
		group.SummaryEventKind = event.EventKind
	}
	if rank := reviewEntityMessageRank(event); rank > 0 && (rank > acc.messageRank || (rank == acc.messageRank && event.EventAt.After(acc.messageAt))) {
		acc.messageRank = rank
		acc.messageAt = event.EventAt
		group.Message = compactReviewEntityText(event.Message, reviewEntityMessageMaxRunes)
	}
	reasons := event.Reasons
	if reasons == nil {
		reasons = make([]string, 0)
	}
	group.Events = append(group.Events, ReviewEntityEvent{
		EventID:       event.EventID,
		EventKind:     event.EventKind,
		EventAt:       event.EventAt,
		EventStage:    event.EventStage,
		Status:        event.Status,
		Actionability: event.Actionability,
		Importance:    event.Importance,
		Message:       event.Message,
		Reasons:       reasons,
	})
}

func reviewEntityGroupingKey(event ReviewEvent) string {
	return fmt.Sprintf("%s:%d", event.EntityKind, event.EntityID)
}

func reviewEntitySummaryRank(event ReviewEvent) int {
	if strings.TrimSpace(event.Summary) == "" {
		return 0
	}
	switch event.EventKind {
	case ReviewEventKindItemSummarized, ReviewEventKindSourceSummarized:
		return 50
	case ReviewEventKindXMediaTranscribed:
		return 40
	case ReviewEventKindXPhotoOCRed:
		return 35
	default:
		return 10
	}
}

func reviewEntityMessageRank(event ReviewEvent) int {
	if strings.TrimSpace(event.Message) == "" {
		return 0
	}
	switch event.Actionability {
	case ReviewActionabilityFailure:
		return 50
	case ReviewActionabilityBlocked:
		return 45
	default:
		return 10
	}
}

func reviewActionabilityPriority(value string) int {
	switch value {
	case ReviewActionabilityFailure:
		return 4
	case ReviewActionabilityBlocked:
		return 3
	case ReviewActionabilityReview:
		return 2
	case ReviewActionabilityBackground:
		return 1
	default:
		return 0
	}
}

func compactReviewEntityText(value string, limit int) string {
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
