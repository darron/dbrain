package notify

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"
)

type Operation string
type FailureType string
type EventKind string
type OutcomeStatus string

const OperationScheduledSyncAll Operation = "scheduled_sync_all"

const (
	OutcomeSuccess   OutcomeStatus = "success"
	OutcomeFailure   OutcomeStatus = "failure"
	OutcomeCancelled OutcomeStatus = "cancelled"
)

const (
	EventFailure  EventKind = "failure"
	EventReminder EventKind = "reminder"
	EventRecovery EventKind = "recovery"
	EventTest     EventKind = "test"
)

const (
	maxNotificationIDBytes = 64
	maxIncidentIDBytes     = 64
	maxNotificationItems   = 64
	maxNotificationTitle   = 160
	maxNotificationBody    = 4096
)

type Outcome struct {
	Operation   Operation     `json:"operation"`
	Status      OutcomeStatus `json:"status"`
	FailureType FailureType   `json:"failure_type,omitempty"`
	ErrorCode   string        `json:"error_code,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	FinishedAt  time.Time     `json:"finished_at"`
}

type Notification struct {
	ID               string        `json:"id"`
	IncidentIDs      []string      `json:"incident_ids"`
	Kind             EventKind     `json:"kind"`
	Operation        Operation     `json:"operation"`
	FailureTypes     []FailureType `json:"failure_types"`
	Title            string        `json:"title"`
	Body             string        `json:"body"`
	Occurrences      int           `json:"occurrences"`
	FirstSeenAt      time.Time     `json:"first_seen_at"`
	LastSeenAt       time.Time     `json:"last_seen_at"`
	CreatedAt        time.Time     `json:"created_at"`
	SuppressionAfter time.Duration `json:"suppression_after"`
}

type Receipt struct {
	Provider   string    `json:"provider"`
	ExternalID string    `json:"external_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type Provider interface {
	Name() string
	Deliver(context.Context, Notification) (Receipt, error)
}

func ValidateOutcome(outcome Outcome) error {
	if outcome.Operation != OperationScheduledSyncAll {
		return fmt.Errorf("invalid notification operation")
	}
	switch outcome.Status {
	case OutcomeSuccess, OutcomeCancelled:
		if outcome.FailureType != "" || outcome.ErrorCode != "" {
			return fmt.Errorf("non-failure outcome carries failure identity")
		}
	case OutcomeFailure:
		definition, ok := LookupFailure(outcome.FailureType)
		if !ok || definition.ErrorCode != outcome.ErrorCode {
			return fmt.Errorf("invalid notification failure identity")
		}
	default:
		return fmt.Errorf("invalid notification outcome status")
	}
	return nil
}

func ValidateNotification(notification Notification) error {
	if len(notification.ID) > maxNotificationIDBytes || !validDigestID(notification.ID, "ntf_") {
		return fmt.Errorf("invalid notification id")
	}
	if notification.Operation != OperationScheduledSyncAll {
		return fmt.Errorf("invalid notification operation")
	}
	switch notification.Kind {
	case EventFailure, EventReminder, EventRecovery, EventTest:
	default:
		return fmt.Errorf("invalid notification kind")
	}
	if len(notification.IncidentIDs) > maxNotificationItems || len(notification.FailureTypes) > maxNotificationItems {
		return fmt.Errorf("notification item count exceeds limit")
	}
	if notification.Kind != EventTest && (len(notification.IncidentIDs) == 0 || len(notification.IncidentIDs) != len(notification.FailureTypes)) {
		return fmt.Errorf("notification incident and failure type counts differ")
	}
	for _, id := range notification.IncidentIDs {
		if len(id) > maxIncidentIDBytes || !validDigestID(id, "inc_") {
			return fmt.Errorf("invalid incident id")
		}
	}
	for _, failureType := range notification.FailureTypes {
		if _, ok := LookupFailure(failureType); !ok {
			return fmt.Errorf("unknown notification failure type")
		}
	}
	if len(notification.Title) == 0 || len(notification.Title) > maxNotificationTitle || !utf8.ValidString(notification.Title) {
		return fmt.Errorf("invalid notification title")
	}
	if len(notification.Body) == 0 || len(notification.Body) > maxNotificationBody || !utf8.ValidString(notification.Body) {
		return fmt.Errorf("invalid notification body")
	}
	if notification.Kind != EventTest && notification.Occurrences < 1 {
		return fmt.Errorf("invalid notification occurrence count")
	}
	if notification.Kind != EventTest && notification.SuppressionAfter <= 0 {
		return fmt.Errorf("invalid notification suppression interval")
	}
	if notification.Kind != EventTest && (notification.FirstSeenAt.IsZero() ||
		notification.LastSeenAt.Before(notification.FirstSeenAt) ||
		notification.CreatedAt.Before(notification.LastSeenAt)) {
		return fmt.Errorf("invalid notification chronology")
	}
	if (!notification.FirstSeenAt.IsZero() && notification.FirstSeenAt.Location() != time.UTC) ||
		(!notification.LastSeenAt.IsZero() && notification.LastSeenAt.Location() != time.UTC) ||
		(!notification.CreatedAt.IsZero() && notification.CreatedAt.Location() != time.UTC) {
		return fmt.Errorf("notification timestamps are not canonical UTC")
	}
	canonical, err := canonicalNotification(notification)
	if err != nil {
		return err
	}
	if !equalNotification(notification, canonical) {
		return fmt.Errorf("notification is not canonical")
	}
	return nil
}
