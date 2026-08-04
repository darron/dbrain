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

	got, err := BuildStatus(Config{Enabled: true, RepeatAfter: 6 * time.Hour, Buzz: BuzzConfig{Enabled: true}}, state)
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
