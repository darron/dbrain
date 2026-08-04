package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildStatusExposesBoundedSafeIncidentAndProviderFields(t *testing.T) {
	now := stateAt(0)
	store := newManagerStore(t)
	provider := &fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
		return Receipt{Provider: "buzz", ExternalID: "event-id-must-not-appear", AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureAppleNotesPermission, 0)); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Enabled:     true,
		RepeatAfter: 6 * time.Hour,
		Buzz: BuzzConfig{
			Enabled:       true,
			RelayURL:      "wss://secret-relay.example",
			ChannelID:     "00000000-0000-4000-8000-000000000001",
			PrivateKeyRef: "env:SECRET_PRIVATE_KEY",
		},
	}

	got, err := BuildStatus(config, state)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.RepeatAfter != "6h0m0s" || got.PendingDeliveries != 0 {
		t.Fatalf("status = %#v", got)
	}
	if len(got.Providers) != 1 || got.Providers[0].Name != "buzz" || !got.Providers[0].Configured || got.Providers[0].LastStatus != "accepted" || !got.Providers[0].LastAcceptedAt.Equal(now) {
		t.Fatalf("providers = %#v", got.Providers)
	}
	if len(got.OpenIncidents) != 1 || got.OpenIncidents[0].FailureType != FailureAppleNotesPermission || got.OpenIncidents[0].Occurrences != 1 {
		t.Fatalf("open incidents = %#v", got.OpenIncidents)
	}
	if len(got.CoolingIncidents) != 0 {
		t.Fatalf("cooling incidents = %#v", got.CoolingIncidents)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-relay", config.Buzz.ChannelID, config.Buzz.PrivateKeyRef, "event-id-must-not-appear"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("status leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestBuildStatusReportsCoolingRearmAndPendingDeliveries(t *testing.T) {
	options := stateOptions()
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), options)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateSuccess(time.Hour), options)
	if err != nil {
		t.Fatal(err)
	}
	state.Outbox[0].Deliveries["buzz"] = DeliveryRecord{
		Status:        DeliveryPending,
		Attempts:      1,
		LastAttemptAt: stateAt(time.Hour),
		ErrorCode:     "buzz_connect_failed",
	}

	got, err := buildStatusAt(Config{Enabled: true, RepeatAfter: 6 * time.Hour, Buzz: BuzzConfig{Enabled: true}}, state, stateAt(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.PendingDeliveries != 2 {
		t.Fatalf("pending deliveries = %d, want both pending envelopes", got.PendingDeliveries)
	}
	if len(got.CoolingIncidents) != 1 || got.CoolingIncidents[0].FailureType != FailureStoreOpen || !got.CoolingIncidents[0].RearmsAt.Equal(stateAt(6*time.Hour)) {
		t.Fatalf("cooling incidents = %#v", got.CoolingIncidents)
	}
	if len(got.OpenIncidents) != 0 {
		t.Fatalf("open incidents = %#v", got.OpenIncidents)
	}
}

func TestBuildStatusOmitsCoolingIncidentAtRearmBoundary(t *testing.T) {
	options := stateOptions()
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), options)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateSuccess(time.Hour), options)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Enabled: true, RepeatAfter: 6 * time.Hour, Buzz: BuzzConfig{Enabled: true}}

	before, err := buildStatusAt(config, state, stateAt(6*time.Hour-time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(before.CoolingIncidents) != 1 {
		t.Fatalf("cooling incidents before boundary = %#v", before.CoolingIncidents)
	}
	atBoundary, err := buildStatusAt(config, state, stateAt(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(atBoundary.CoolingIncidents) != 0 {
		t.Fatalf("expired cooling incidents = %#v", atBoundary.CoolingIncidents)
	}
}

func TestBuildStatusUsesLatestAttemptedOutboxDelivery(t *testing.T) {
	tests := []struct {
		name            string
		outboxStatus    DeliveryStatus
		errorCode       string
		withPriorAccept bool
		wantStatus      string
	}{
		{name: "first temporary failure", outboxStatus: DeliveryPending, errorCode: "buzz_connect_failed", wantStatus: "pending"},
		{name: "first ambiguous failure", outboxStatus: DeliveryAmbiguous, errorCode: "buzz_publish_timeout", wantStatus: "ambiguous"},
		{name: "new temporary failure overrides prior acceptance", outboxStatus: DeliveryPending, errorCode: "buzz_connect_failed", withPriorAccept: true, wantStatus: "pending"},
		{name: "new ambiguous failure overrides prior acceptance", outboxStatus: DeliveryAmbiguous, errorCode: "buzz_publish_timeout", withPriorAccept: true, wantStatus: "ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := stateOptions()
			state := EmptyState()
			if test.withPriorAccept {
				var err error
				state, _, err = Observe(state, stateFailed(FailureStoreOpen, 0), options)
				if err != nil {
					t.Fatal(err)
				}
				acceptedNotification, err := DeriveNotification(state.Outbox[0].Event)
				if err != nil {
					t.Fatal(err)
				}
				state.Outbox[0].Deliveries["buzz"] = DeliveryRecord{
					Status:        DeliveryAccepted,
					Attempts:      1,
					LastAttemptAt: stateAt(time.Hour),
					Receipt: Receipt{
						Provider: "buzz", ExternalID: "accepted-event", AcceptedAt: stateAt(time.Hour),
					},
				}
				state.LastDelivery = DeliverySummary{
					NotificationID: acceptedNotification.ID,
					Provider:       "buzz",
					Kind:           EventFailure,
					Status:         DeliveryAccepted,
					At:             stateAt(time.Hour),
				}
				state, _, err = Observe(state, stateFailed(FailureAppleNotesPermission, 2*time.Hour), options)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				state, _, err = Observe(state, stateFailed(FailureAppleNotesPermission, 0), options)
				if err != nil {
					t.Fatal(err)
				}
			}
			latest := len(state.Outbox) - 1
			state.Outbox[latest].Deliveries["buzz"] = DeliveryRecord{
				Status:        test.outboxStatus,
				Attempts:      1,
				LastAttemptAt: stateAt(3 * time.Hour),
				ErrorCode:     test.errorCode,
			}

			got, err := BuildStatus(Config{Enabled: true, RepeatAfter: 6 * time.Hour, Buzz: BuzzConfig{Enabled: true}}, state)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Providers) != 1 {
				t.Fatalf("providers = %#v", got.Providers)
			}
			provider := got.Providers[0]
			if provider.LastStatus != test.wantStatus || provider.LastErrorCode != test.errorCode || provider.LastAttemptAt == nil || !provider.LastAttemptAt.Equal(stateAt(3*time.Hour)) {
				t.Fatalf("provider status = %#v", provider)
			}
			if provider.LastAcceptedAt != nil {
				t.Fatalf("stale acceptance survived newer attempt: %#v", provider)
			}
		})
	}
}

func TestBuildStatusRejectsInvalidStateWithoutLeakingErrorText(t *testing.T) {
	state := EmptyState()
	state.Outbox = append(state.Outbox, Envelope{Deliveries: map[string]DeliveryRecord{
		"buzz": {Status: DeliveryPending, ErrorCode: "raw private error text"},
	}})
	_, err := BuildStatus(Config{Enabled: true, RepeatAfter: 6 * time.Hour, Buzz: BuzzConfig{Enabled: true}}, state)
	if err == nil || strings.Contains(err.Error(), "raw private error text") || !errors.Is(err, ErrStatusInvalid) {
		t.Fatalf("BuildStatus error = %v", err)
	}
}
