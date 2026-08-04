package notify

import (
	"errors"
	"sort"
	"time"
)

var ErrStatusInvalid = errors.New("notification_status_invalid")

type Status struct {
	Enabled           bool                    `json:"enabled"`
	RepeatAfter       string                  `json:"repeat_after"`
	Providers         []ProviderStatus        `json:"providers"`
	OpenIncidents     []OpenIncidentStatus    `json:"open_incidents"`
	CoolingIncidents  []CoolingIncidentStatus `json:"cooling_incidents"`
	PendingDeliveries int                     `json:"pending_deliveries"`
}

type ProviderStatus struct {
	Name           string     `json:"name"`
	Configured     bool       `json:"configured"`
	LastStatus     string     `json:"last_status,omitempty"`
	LastErrorCode  string     `json:"last_error_code,omitempty"`
	LastAttemptAt  *time.Time `json:"last_attempt_at,omitempty"`
	LastAcceptedAt *time.Time `json:"last_accepted_at,omitempty"`
}

type OpenIncidentStatus struct {
	FailureType FailureType `json:"failure_type"`
	Occurrences int         `json:"occurrences"`
	FirstSeenAt time.Time   `json:"first_seen_at"`
	LastSeenAt  time.Time   `json:"last_seen_at"`
}

type CoolingIncidentStatus struct {
	FailureType FailureType `json:"failure_type"`
	Occurrences int         `json:"occurrences"`
	RearmsAt    time.Time   `json:"rearms_at"`
}

func BuildStatus(config Config, state State) (Status, error) {
	return buildStatusAt(config, state, time.Now())
}

func buildStatusAt(config Config, state State, now time.Time) (Status, error) {
	if config.RepeatAfter <= 0 || ValidateState(state) != nil {
		return Status{}, ErrStatusInvalid
	}
	now = now.UTC()
	status := Status{
		Enabled:          config.Enabled,
		RepeatAfter:      config.RepeatAfter.String(),
		Providers:        []ProviderStatus{},
		OpenIncidents:    []OpenIncidentStatus{},
		CoolingIncidents: []CoolingIncidentStatus{},
	}
	for _, name := range configuredProviderNames(config) {
		provider := ProviderStatus{Name: name, Configured: true}
		var latestAt time.Time
		if state.LastDelivery.Provider == name {
			provider.LastStatus = string(state.LastDelivery.Status)
			provider.LastErrorCode = state.LastDelivery.ErrorCode
			latestAt = state.LastDelivery.At.UTC()
			provider.LastAttemptAt = timePointer(latestAt)
			if state.LastDelivery.Status == DeliveryAccepted && !state.LastDelivery.At.IsZero() {
				acceptedAt := state.LastDelivery.At.UTC()
				provider.LastAcceptedAt = &acceptedAt
			}
		}
		for _, envelope := range state.Outbox {
			delivery, ok := envelope.Deliveries[name]
			if !ok || delivery.Attempts == 0 || delivery.LastAttemptAt.Before(latestAt) {
				continue
			}
			latestAt = delivery.LastAttemptAt.UTC()
			provider.LastStatus = string(delivery.Status)
			provider.LastErrorCode = delivery.ErrorCode
			provider.LastAttemptAt = timePointer(latestAt)
			provider.LastAcceptedAt = nil
			if delivery.Status == DeliveryAccepted {
				acceptedAt := delivery.Receipt.AcceptedAt.UTC()
				provider.LastAcceptedAt = &acceptedAt
			}
		}
		status.Providers = append(status.Providers, provider)
	}
	for _, incident := range state.Incidents {
		switch incident.Phase {
		case IncidentOpen:
			status.OpenIncidents = append(status.OpenIncidents, OpenIncidentStatus{
				FailureType: incident.FailureType,
				Occurrences: incident.Occurrences,
				FirstSeenAt: incident.FirstSeenAt.UTC(),
				LastSeenAt:  incident.LastSeenAt.UTC(),
			})
		case IncidentCooling:
			rearmsAt := incident.LastFailureEnqueuedAt.Add(config.RepeatAfter).UTC()
			if !now.Before(rearmsAt) {
				continue
			}
			status.CoolingIncidents = append(status.CoolingIncidents, CoolingIncidentStatus{
				FailureType: incident.FailureType,
				Occurrences: incident.Occurrences,
				RearmsAt:    rearmsAt,
			})
		}
	}
	for _, envelope := range state.Outbox {
		for _, delivery := range envelope.Deliveries {
			if delivery.Status == DeliveryPending || delivery.Status == DeliveryAmbiguous {
				status.PendingDeliveries++
			}
		}
	}
	sort.Slice(status.OpenIncidents, func(left, right int) bool {
		return failureCatalogIndex(status.OpenIncidents[left].FailureType) < failureCatalogIndex(status.OpenIncidents[right].FailureType)
	})
	sort.Slice(status.CoolingIncidents, func(left, right int) bool {
		return failureCatalogIndex(status.CoolingIncidents[left].FailureType) < failureCatalogIndex(status.CoolingIncidents[right].FailureType)
	})
	return status, nil
}

func timePointer(value time.Time) *time.Time {
	return &value
}
