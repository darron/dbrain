package notify

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

func DeriveNotification(event EventFacts) (Notification, error) {
	if err := ValidateEventFacts(event); err != nil {
		return Notification{}, err
	}
	incidents := make([]Incident, len(event.Incidents))
	for index, snapshot := range event.Incidents {
		incidents[index] = Incident{
			ID:          snapshot.ID,
			Operation:   event.Operation,
			FailureType: snapshot.FailureType,
			FirstSeenAt: snapshot.FirstSeenAt,
			LastSeenAt:  snapshot.LastSeenAt,
			Occurrences: snapshot.Occurrences,
		}
	}
	switch event.Kind {
	case EventFailure:
		return BuildFailureMessage(incidents[0], event.SuppressionAfter)
	case EventReminder:
		return BuildReminderMessage(incidents[0], event.SuppressionAfter)
	case EventRecovery:
		return BuildRecoveryMessage(incidents, event.CreatedAt, event.SuppressionAfter)
	case EventTest:
		notification := Notification{
			ID:        deterministicNotificationID(EventTest, nil, event.CreatedAt),
			Kind:      EventTest,
			Operation: event.Operation,
			Title:     "dbrain notification test",
			Body:      "dbrain notification delivery test.",
			CreatedAt: event.CreatedAt,
		}
		if err := ValidateNotification(notification); err != nil {
			return Notification{}, err
		}
		return notification, nil
	default:
		return Notification{}, fmt.Errorf("invalid notification event kind")
	}
}

func BuildFailureMessage(incident Incident, repeatAfter time.Duration) (Notification, error) {
	notification, err := buildFailureMessage(incident, repeatAfter)
	if err != nil {
		return Notification{}, err
	}
	if err := ValidateNotification(notification); err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func buildFailureMessage(incident Incident, repeatAfter time.Duration) (Notification, error) {
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
	return notification, nil
}

func BuildReminderMessage(incident Incident, repeatAfter time.Duration) (Notification, error) {
	notification, err := buildReminderMessage(incident, repeatAfter)
	if err != nil {
		return Notification{}, err
	}
	if err := ValidateNotification(notification); err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func buildReminderMessage(incident Incident, repeatAfter time.Duration) (Notification, error) {
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
	return notification, nil
}

func BuildRecoveryMessage(incidents []Incident, createdAt time.Time, repeatAfter time.Duration) (Notification, error) {
	notification, err := buildRecoveryMessage(incidents, createdAt, repeatAfter, true)
	if err != nil {
		return Notification{}, err
	}
	if err := ValidateNotification(notification); err != nil {
		return Notification{}, err
	}
	return notification, nil
}

func buildRecoveryMessage(incidents []Incident, createdAt time.Time, repeatAfter time.Duration, validateIncidentIDs bool) (Notification, error) {
	if len(incidents) == 0 {
		return Notification{}, fmt.Errorf("recovery requires incidents")
	}
	incidents = append([]Incident(nil), incidents...)
	sort.Slice(incidents, func(left int, right int) bool {
		return failureCatalogIndex(incidents[left].FailureType) < failureCatalogIndex(incidents[right].FailureType)
	})
	notification := Notification{
		Kind:             EventRecovery,
		Operation:        OperationScheduledSyncAll,
		Title:            "dbrain scheduled sync recovered",
		CreatedAt:        createdAt.UTC(),
		SuppressionAfter: repeatAfter,
	}
	lines := []string{"dbrain scheduled sync recovered.", "Resolved:"}
	for _, incident := range incidents {
		definition, ok := LookupFailure(incident.FailureType)
		if !ok || incident.Operation != OperationScheduledSyncAll ||
			(validateIncidentIDs && incident.ID != deterministicIncidentID(incident.Operation, incident.FailureType, incident.FirstSeenAt)) {
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
	return notification, nil
}

func canonicalNotification(notification Notification) (Notification, error) {
	switch notification.Kind {
	case EventFailure:
		if len(notification.IncidentIDs) != 1 || len(notification.FailureTypes) != 1 ||
			notification.Occurrences != 1 ||
			!notification.FirstSeenAt.Equal(notification.LastSeenAt) ||
			!notification.LastSeenAt.Equal(notification.CreatedAt) ||
			notification.IncidentIDs[0] != deterministicIncidentID(notification.Operation, notification.FailureTypes[0], notification.FirstSeenAt) {
			return Notification{}, fmt.Errorf("invalid canonical failure notification")
		}
		return buildFailureMessage(incidentFromNotification(notification, 0), notification.SuppressionAfter)
	case EventReminder:
		if len(notification.IncidentIDs) != 1 || len(notification.FailureTypes) != 1 ||
			notification.Occurrences < 2 ||
			!notification.LastSeenAt.Equal(notification.CreatedAt) ||
			notification.IncidentIDs[0] != deterministicIncidentID(notification.Operation, notification.FailureTypes[0], notification.FirstSeenAt) {
			return Notification{}, fmt.Errorf("invalid canonical reminder notification")
		}
		return buildReminderMessage(incidentFromNotification(notification, 0), notification.SuppressionAfter)
	case EventRecovery:
		if err := validateRecoveryIdentity(notification); err != nil {
			return Notification{}, err
		}
		incidents := make([]Incident, len(notification.IncidentIDs))
		for index := range incidents {
			occurrences := 0
			if index == 0 {
				occurrences = notification.Occurrences
			}
			incidents[index] = incidentFromNotification(notification, index)
			incidents[index].Occurrences = occurrences
		}
		return buildRecoveryMessage(incidents, notification.CreatedAt, notification.SuppressionAfter, false)
	case EventTest:
		if len(notification.IncidentIDs) != 0 || len(notification.FailureTypes) != 0 ||
			notification.Occurrences != 0 || !notification.FirstSeenAt.IsZero() ||
			!notification.LastSeenAt.IsZero() || notification.CreatedAt.IsZero() ||
			notification.SuppressionAfter != 0 {
			return Notification{}, fmt.Errorf("invalid canonical test notification")
		}
		return Notification{
			ID:        deterministicNotificationID(EventTest, nil, notification.CreatedAt),
			Kind:      EventTest,
			Operation: OperationScheduledSyncAll,
			Title:     "dbrain notification test",
			Body:      "dbrain notification delivery test.",
			CreatedAt: notification.CreatedAt.UTC(),
		}, nil
	default:
		return Notification{}, fmt.Errorf("invalid notification kind")
	}
}

func incidentFromNotification(notification Notification, index int) Incident {
	return Incident{
		ID:          notification.IncidentIDs[index],
		Operation:   notification.Operation,
		FailureType: notification.FailureTypes[index],
		FirstSeenAt: notification.FirstSeenAt,
		LastSeenAt:  notification.LastSeenAt,
		Occurrences: notification.Occurrences,
	}
}

func validateRecoveryIdentity(notification Notification) error {
	if len(notification.IncidentIDs) == 0 || len(notification.IncidentIDs) != len(notification.FailureTypes) {
		return fmt.Errorf("invalid canonical recovery notification")
	}
	previousCatalogIndex := -1
	seenIncidentIDs := make(map[string]struct{}, len(notification.IncidentIDs))
	for index, incidentID := range notification.IncidentIDs {
		catalogIndex := failureCatalogIndex(notification.FailureTypes[index])
		if catalogIndex <= previousCatalogIndex {
			return fmt.Errorf("recovery notification is not in catalog order")
		}
		previousCatalogIndex = catalogIndex
		if _, exists := seenIncidentIDs[incidentID]; exists {
			return fmt.Errorf("recovery notification repeats incident")
		}
		seenIncidentIDs[incidentID] = struct{}{}
	}
	return nil
}

func equalNotification(left Notification, right Notification) bool {
	return left.ID == right.ID &&
		slices.Equal(left.IncidentIDs, right.IncidentIDs) &&
		left.Kind == right.Kind &&
		left.Operation == right.Operation &&
		slices.Equal(left.FailureTypes, right.FailureTypes) &&
		left.Title == right.Title &&
		left.Body == right.Body &&
		left.Occurrences == right.Occurrences &&
		left.FirstSeenAt.Equal(right.FirstSeenAt) &&
		left.LastSeenAt.Equal(right.LastSeenAt) &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.SuppressionAfter == right.SuppressionAfter
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
