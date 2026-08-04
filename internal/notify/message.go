package notify

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
)

func BuildFailureMessage(incident Incident, repeatAfter time.Duration) (Notification, error) {
	definition, ok := LookupFailure(incident.FailureType)
	if !ok {
		return Notification{}, fmt.Errorf("unknown failure type")
	}
	createdAt := incident.LastSeenAt.UTC()
	notification := Notification{
		IncidentIDs:      []string{incident.ID},
		Kind:             EventFailure,
		Operation:        incident.Operation,
		FailureTypes:     []FailureType{incident.FailureType},
		Title:            "dbrain scheduled sync failed: " + definition.Title,
		Occurrences:      incident.Occurrences,
		FirstSeenAt:      incident.FirstSeenAt.UTC(),
		LastSeenAt:       incident.LastSeenAt.UTC(),
		CreatedAt:        createdAt,
		SuppressionAfter: repeatAfter,
	}
	notification.Body = fmt.Sprintf(
		"dbrain scheduled sync failed: %s.\nOccurrences: %d.\nFirst observed: %s.\nAction: %s\nFurther notifications for this error type are suppressed for %s.\nIncident: %s",
		definition.Title,
		incident.Occurrences,
		formatTimestamp(incident.FirstSeenAt),
		definition.Action,
		formatSuppression(repeatAfter),
		incident.ID,
	)
	notification.ID = deterministicNotificationID(notification.Kind, notification.IncidentIDs, notification.CreatedAt)
	if err := ValidateNotification(notification); err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func BuildReminderMessage(incident Incident, repeatAfter time.Duration) (Notification, error) {
	definition, ok := LookupFailure(incident.FailureType)
	if !ok {
		return Notification{}, fmt.Errorf("unknown failure type")
	}
	createdAt := incident.LastSeenAt.UTC()
	notification := Notification{
		IncidentIDs:      []string{incident.ID},
		Kind:             EventReminder,
		Operation:        incident.Operation,
		FailureTypes:     []FailureType{incident.FailureType},
		Title:            "dbrain scheduled sync still failing: " + definition.Title,
		Occurrences:      incident.Occurrences,
		FirstSeenAt:      incident.FirstSeenAt.UTC(),
		LastSeenAt:       incident.LastSeenAt.UTC(),
		CreatedAt:        createdAt,
		SuppressionAfter: repeatAfter,
	}
	notification.Body = fmt.Sprintf(
		"Still failing: %s.\nOccurrences: %d.\nDuration: %s.\nFirst observed: %s.\nLast observed: %s.\nAction: %s\nFurther notifications for this error type are suppressed for %s.\nIncident: %s",
		definition.Title,
		incident.Occurrences,
		formatDuration(incident.LastSeenAt.Sub(incident.FirstSeenAt)),
		formatTimestamp(incident.FirstSeenAt),
		formatTimestamp(incident.LastSeenAt),
		definition.Action,
		formatSuppression(repeatAfter),
		incident.ID,
	)
	notification.ID = deterministicNotificationID(notification.Kind, notification.IncidentIDs, notification.CreatedAt)
	if err := ValidateNotification(notification); err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func BuildRecoveryMessage(incidents []Incident, createdAt time.Time) (Notification, error) {
	if len(incidents) == 0 {
		return Notification{}, fmt.Errorf("recovery requires incidents")
	}
	incidents = append([]Incident(nil), incidents...)
	sort.Slice(incidents, func(left int, right int) bool {
		return failureCatalogIndex(incidents[left].FailureType) < failureCatalogIndex(incidents[right].FailureType)
	})
	notification := Notification{
		Kind:      EventRecovery,
		Operation: OperationScheduledSyncAll,
		Title:     "dbrain scheduled sync recovered",
		CreatedAt: createdAt.UTC(),
	}
	lines := []string{"dbrain scheduled sync recovered.", "Resolved:"}
	for _, incident := range incidents {
		definition, ok := LookupFailure(incident.FailureType)
		if !ok || incident.Operation != OperationScheduledSyncAll {
			return Notification{}, fmt.Errorf("invalid recovered incident")
		}
		notification.IncidentIDs = append(notification.IncidentIDs, incident.ID)
		notification.FailureTypes = append(notification.FailureTypes, incident.FailureType)
		notification.Occurrences += incident.Occurrences
		if notification.FirstSeenAt.IsZero() || incident.FirstSeenAt.Before(notification.FirstSeenAt) {
			notification.FirstSeenAt = incident.FirstSeenAt.UTC()
		}
		if incident.LastSeenAt.After(notification.LastSeenAt) {
			notification.LastSeenAt = incident.LastSeenAt.UTC()
		}
		lines = append(lines, fmt.Sprintf("- %s (incident %s)", definition.Title, incident.ID))
	}
	notification.Body = strings.Join(lines, "\n")
	notification.ID = deterministicNotificationID(notification.Kind, notification.IncidentIDs, notification.CreatedAt)
	if err := ValidateNotification(notification); err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func failureCatalogIndex(failureType FailureType) int {
	for index, known := range orderedFailureTypes {
		if known == failureType {
			return index
		}
	}
	return len(orderedFailureTypes)
}

func deterministicNotificationID(kind EventKind, incidentIDs []string, createdAt time.Time) string {
	ids := append([]string(nil), incidentIDs...)
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(string(kind) + "\x00" + strings.Join(ids, "\x00") + "\x00" + createdAt.UTC().Format(time.RFC3339Nano)))
	return fmt.Sprintf("ntf_%x", digest[:12])
}

func formatTimestamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

func formatSuppression(duration time.Duration) string {
	if duration > 0 && duration%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(duration/time.Hour))
	}
	if duration > 0 && duration%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(duration/time.Minute))
	}
	return duration.String()
}

func formatDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return formatSuppression(duration)
}
