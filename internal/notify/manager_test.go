package notify

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

type fakeProvider struct {
	name    string
	deliver func(context.Context, Notification) (Receipt, error)
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Deliver(ctx context.Context, notification Notification) (Receipt, error) {
	return p.deliver(ctx, notification)
}

func managerOptions() Options {
	return Options{RepeatAfter: 6 * time.Hour}
}

func managerFailed(failureType FailureType, offset time.Duration) Outcome {
	return stateFailed(failureType, offset)
}

func newManagerStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestManagerPersistsBeforeDeliveryAndAcknowledgesAfterAcceptance(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(persisted.Outbox) != 1 || persisted.Outbox[0].Deliveries["buzz"].Status != DeliveryPending {
			t.Fatalf("delivery preceded pending persistence: %#v", persisted)
		}
		derived, err := DeriveNotification(persisted.Outbox[0].Event)
		if err != nil || !reflect.DeepEqual(derived, notification) {
			t.Fatalf("provider payload = %#v, persisted derivation = %#v, %v", notification, derived, err)
		}
		return Receipt{Provider: "buzz", ExternalID: "event-id", AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))

	if err := manager.Observe(t.Context(), managerFailed(FailureAppleNotesPermission, 0)); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Outbox) != 0 {
		t.Fatalf("accepted envelope not pruned: %#v", persisted.Outbox)
	}
	if persisted.LastDeliveries["buzz"].Provider != "buzz" || persisted.LastDeliveries["buzz"].Status != DeliveryAccepted || persisted.LastDeliveries["buzz"].NotificationID == "" {
		t.Fatalf("last deliveries = %#v", persisted.LastDeliveries)
	}
}

func TestManagerRetainsLatestTerminalSummaryForEachProvider(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	providers := []Provider{
		&fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
			return Receipt{Provider: "buzz", ExternalID: "buzz-event", AcceptedAt: now}, nil
		}},
		&fakeProvider{name: "slack", deliver: func(context.Context, Notification) (Receipt, error) {
			return Receipt{Provider: "slack", ExternalID: "slack-event", AcceptedAt: now}, nil
		}},
	}
	manager := NewManager(managerOptions(), store, providers, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureAppleNotesPermission, 0)); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 0 {
		t.Fatalf("terminal envelopes retained: %#v", state.Outbox)
	}
	for _, name := range []string{"buzz", "slack"} {
		summary, ok := state.LastDeliveries[name]
		if !ok || summary.Provider != name || summary.Status != DeliveryAccepted || summary.NotificationID == "" {
			t.Fatalf("last delivery for %s = %#v", name, state.LastDeliveries)
		}
	}
}

func TestManagerDeliveryFailureRemainsPendingAndDoesNotNotifyRecursively(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	var calls int
	var events []metrics.Event
	var logs []string
	provider := &fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
		calls++
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_unavailable", errors.New("private relay URL and token"))
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider},
		WithClock(func() time.Time { return now }),
		WithMetricsEmitter(func(event metrics.Event) error { events = append(events, event); return nil }),
		WithManagerLogger(func(message string) { logs = append(logs, message) }),
	)

	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil || err.Error() != "notification_delivery_failed" {
		t.Fatalf("Observe error = %v", err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(state.Outbox) != 1 {
		t.Fatalf("calls=%d outbox=%#v", calls, state.Outbox)
	}
	record := state.Outbox[0].Deliveries["buzz"]
	if record.Status != DeliveryPending || record.Attempts != 1 || record.ErrorCode != "buzz_unavailable" {
		t.Fatalf("temporary record = %#v", record)
	}
	if len(events) != 1 || events[0]["status"] != "temporary_error" || len(logs) != 1 || logs[0] != "notification_delivery_failed" {
		t.Fatalf("events=%#v logs=%#v", events, logs)
	}
	if calls != 1 {
		t.Fatalf("provider failure recursively notified: %d calls", calls)
	}
}

func TestManagerNotificationMetricsAcceptEveryCatalogFailureType(t *testing.T) {
	for _, failureType := range KnownFailureTypes() {
		event := metrics.NotificationDeliveryCompletedEvent("buzz", "failure", string(failureType), "accepted", time.Second)
		if got := event["failure_type"]; got != string(failureType) {
			t.Fatalf("failure type %q sanitized to %#v", failureType, got)
		}
	}
}

func TestManagerRetriesPendingProviderOnSuppressedObservationUsingPersistedFacts(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	var delivered []Notification
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		delivered = append(delivered, notification)
		if len(delivered) == 1 {
			return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_connect_failed", errors.New("dial failed"))
		}
		return Receipt{Provider: "buzz", ExternalID: "same-event", AcceptedAt: now.Add(time.Hour)}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))

	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil {
		t.Fatal("temporary failure was not surfaced")
	}
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 || !reflect.DeepEqual(delivered[0], delivered[1]) {
		t.Fatalf("retry reconstructed notification: %#v", delivered)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	incident := state.Incidents[incidentKey(OperationScheduledSyncAll, FailureStoreOpen)]
	if incident.Occurrences != 2 || len(state.Outbox) != 0 || state.LastDeliveries["buzz"].Status != DeliveryAccepted {
		t.Fatalf("state after suppressed retry = %#v", state)
	}
}

func TestManagerContinuesOtherProvidersAfterOneFails(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	var calls []string
	providers := []Provider{
		&fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
			calls = append(calls, "buzz")
			return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_connect_failed", errors.New("offline"))
		}},
		&fakeProvider{name: "slack", deliver: func(context.Context, Notification) (Receipt, error) {
			calls = append(calls, "slack")
			return Receipt{Provider: "slack", ExternalID: "message-id", AcceptedAt: now}, nil
		}},
	}
	manager := NewManager(managerOptions(), store, providers, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil {
		t.Fatal("provider failure was not surfaced")
	}
	if !slices.Equal(calls, []string{"buzz", "slack"}) {
		t.Fatalf("provider calls = %v", calls)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].Deliveries["buzz"].Status != DeliveryPending || state.Outbox[0].Deliveries["slack"].Status != DeliveryAccepted {
		t.Fatalf("isolated delivery records = %#v", state.Outbox)
	}
}

func TestManagerProcessesPersistedEnvelopesOldestFirst(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	var kinds []EventKind
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		kinds = append(kinds, notification.Kind)
		if len(kinds) == 1 {
			return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_connect_failed", errors.New("offline"))
		}
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now.Add(time.Hour)}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil {
		t.Fatal("first delivery failure was not surfaced")
	}
	if err := manager.Observe(t.Context(), stateSuccess(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []EventKind{EventFailure, EventFailure, EventRecovery}) {
		t.Fatalf("delivery order = %v", kinds)
	}
}

func TestManagerPermanentErrorIsTerminalAndDoesNotHotLoop(t *testing.T) {
	store := newManagerStore(t)
	var calls int
	provider := &fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
		calls++
		return Receipt{}, NewDeliveryError(DeliveryErrorPermanent, "buzz_auth_rejected", errors.New("raw rejection"))
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider})
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil {
		t.Fatal("permanent failure was not surfaced")
	}
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Hour)); err != nil {
		t.Fatalf("suppressed observation retried permanent failure: %v", err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(state.Outbox) != 0 || state.LastDeliveries["buzz"].Status != DeliveryPermanentError || state.LastDeliveries["buzz"].ErrorCode != "buzz_auth_rejected" {
		t.Fatalf("calls=%d state=%#v", calls, state)
	}
}

func TestManagerAmbiguousDeliveryRetriesOnlyWhenReminderBecomesDue(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	var kinds []EventKind
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		kinds = append(kinds, notification.Kind)
		if len(kinds) == 1 {
			return Receipt{}, NewDeliveryError(DeliveryErrorAmbiguous, "buzz_publish_timeout", context.DeadlineExceeded)
		}
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now.Add(6 * time.Hour)}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err == nil {
		t.Fatal("ambiguous failure was not surfaced")
	}
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 1 {
		t.Fatalf("ambiguous delivery retried before reminder: %v", kinds)
	}
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []EventKind{EventFailure, EventFailure, EventReminder}) {
		t.Fatalf("ambiguous reminder order = %v", kinds)
	}
}

func TestManagerAmbiguousDeliveryDoesNotRetryForUnrelatedReminder(t *testing.T) {
	store := newManagerStore(t)
	options := Options{RepeatAfter: 6 * time.Hour, Providers: []string{"buzz"}}
	state, _, err := Observe(EmptyState(), managerFailed(FailureStoreOpen, 0), options)
	if err != nil {
		t.Fatal(err)
	}
	state.Outbox[0].Deliveries["buzz"] = DeliveryRecord{
		Status: DeliveryAmbiguous, Attempts: 1, LastAttemptAt: stateAt(0), ErrorCode: "buzz_publish_timeout",
	}
	state, _, err = Observe(state, managerFailed(FailureAppleNotesPermission, -time.Hour), options)
	if err != nil {
		t.Fatal(err)
	}
	appleNotification, err := DeriveNotification(state.Outbox[1].Event)
	if err != nil {
		t.Fatal(err)
	}
	state.Outbox[1].Deliveries["buzz"] = DeliveryRecord{
		Status: DeliveryAccepted, Attempts: 1, LastAttemptAt: stateAt(-time.Hour),
		Receipt: Receipt{Provider: "buzz", ExternalID: appleNotification.ID, AcceptedAt: stateAt(-time.Hour)},
	}
	if err := store.Replace(state); err != nil {
		t.Fatal(err)
	}
	var delivered []Notification
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		delivered = append(delivered, notification)
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: stateAt(5 * time.Hour)}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider})
	if err := manager.Observe(t.Context(), managerFailed(FailureAppleNotesPermission, 5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 || delivered[0].Kind != EventReminder || delivered[0].FailureTypes[0] != FailureAppleNotesPermission {
		t.Fatalf("unrelated reminder retried ambiguous delivery: %#v", delivered)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].Event.Incidents[0].FailureType != FailureStoreOpen || state.Outbox[0].Deliveries["buzz"].Status != DeliveryAmbiguous {
		t.Fatalf("unrelated ambiguous envelope changed: %#v", state.Outbox)
	}
}

func TestManagerRetiresRemovedProviderWithoutAddingCurrentProviderHistorically(t *testing.T) {
	store := newManagerStore(t)
	state, _, err := Observe(EmptyState(), managerFailed(FailureStoreOpen, 0), Options{RepeatAfter: 6 * time.Hour, Providers: []string{"removed"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(state); err != nil {
		t.Fatal(err)
	}
	var calls int
	provider := &fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
		calls++
		return Receipt{}, errors.New("must not deliver historical incident")
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider})
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Hour)); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(state.Outbox) != 0 || state.LastDeliveries["removed"].Provider != "removed" || state.LastDeliveries["removed"].Status != DeliveryRetired {
		t.Fatalf("calls=%d state=%#v", calls, state)
	}
}

func TestManagerFullOutboxRetiresRemovedProviderBeforeEnqueue(t *testing.T) {
	store := newManagerStore(t)
	removedOptions := Options{RepeatAfter: 6 * time.Hour, Providers: []string{"removed"}}
	state := EmptyState()
	for index := 0; index < maxStateEnvelopes; index++ {
		var err error
		state, _, err = Observe(state, managerFailed(FailureStoreOpen, time.Duration(index)*6*time.Hour), removedOptions)
		if err != nil {
			t.Fatalf("fill outbox at %d: %v", index, err)
		}
	}
	if len(state.Outbox) != maxStateEnvelopes {
		t.Fatalf("outbox length = %d, want %d", len(state.Outbox), maxStateEnvelopes)
	}
	if err := store.Replace(state); err != nil {
		t.Fatal(err)
	}

	now := stateAt(time.Duration(maxStateEnvelopes) * 6 * time.Hour)
	var calls int
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		calls++
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(persisted.Outbox) != 1 || persisted.Outbox[0].Deliveries["buzz"].Status != DeliveryPending {
			t.Fatalf("retired capacity not reclaimed before delivery: %#v", persisted.Outbox)
		}
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Duration(maxStateEnvelopes)*6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 0 || state.LastDeliveries["buzz"].Provider != "buzz" || state.LastDeliveries["buzz"].Status != DeliveryAccepted {
		t.Fatalf("state after reclaimed delivery = %#v", state)
	}
}

func TestManagerFullActiveProviderOutboxDeliversPersistedWorkBeforeEnqueue(t *testing.T) {
	store := newManagerStore(t)
	options := managerOptions()
	options.Providers = []string{"buzz"}
	state := EmptyState()
	wantOrder := make([]string, 0, maxStateEnvelopes+1)
	for index := 0; index < maxStateEnvelopes; index++ {
		var err error
		state, _, err = Observe(state, managerFailed(FailureStoreOpen, time.Duration(index)*6*time.Hour), options)
		if err != nil {
			t.Fatalf("fill outbox at %d: %v", index, err)
		}
		notification, err := DeriveNotification(state.Outbox[index].Event)
		if err != nil {
			t.Fatal(err)
		}
		wantOrder = append(wantOrder, notification.ID)
	}
	if err := store.Replace(state); err != nil {
		t.Fatal(err)
	}

	now := stateAt(time.Duration(maxStateEnvelopes) * 6 * time.Hour)
	var delivered []Notification
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		foundPending := false
		for _, envelope := range persisted.Outbox {
			derived, deriveErr := DeriveNotification(envelope.Event)
			if deriveErr != nil {
				t.Fatal(deriveErr)
			}
			if derived.ID == notification.ID && envelope.Deliveries["buzz"].Status == DeliveryPending {
				foundPending = true
				break
			}
		}
		if !foundPending {
			t.Fatalf("delivery %q was not persisted as pending: %#v", notification.ID, persisted.Outbox)
		}
		delivered = append(delivered, notification)
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Duration(maxStateEnvelopes)*6*time.Hour)); err != nil {
		t.Fatal(err)
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	incident := persisted.Incidents[incidentKey(OperationScheduledSyncAll, FailureStoreOpen)]
	if incident.Occurrences != maxStateEnvelopes+1 || !incident.LastFailureEnqueuedAt.Equal(now) {
		t.Fatalf("current settlement was discarded: %#v", incident)
	}
	if len(persisted.Outbox) != 0 {
		t.Fatalf("delivered outbox was not reclaimed: %#v", persisted.Outbox)
	}
	if len(delivered) != maxStateEnvelopes+1 {
		t.Fatalf("delivered %d notifications, want %d", len(delivered), maxStateEnvelopes+1)
	}
	gotPersistedOrder := make([]string, maxStateEnvelopes)
	for index := 0; index < maxStateEnvelopes; index++ {
		gotPersistedOrder[index] = delivered[index].ID
	}
	if !slices.Equal(gotPersistedOrder, wantOrder) {
		t.Fatalf("persisted delivery order = %v, want oldest first: %v", gotPersistedOrder, wantOrder)
	}
	current := delivered[len(delivered)-1]
	if current.Kind != EventReminder || current.Occurrences != maxStateEnvelopes+1 || persisted.LastDeliveries["buzz"].NotificationID != current.ID {
		t.Fatalf("current settlement delivery = %#v, last=%#v", current, persisted.LastDeliveries)
	}
}

func TestManagerFullAmbiguousOutboxDoesNotRetryWithoutSafeReminder(t *testing.T) {
	store := newManagerStore(t)
	options := managerOptions()
	options.Providers = []string{"buzz"}
	state := EmptyState()
	for index := 0; index < maxStateEnvelopes; index++ {
		var err error
		state, _, err = Observe(state, managerFailed(FailureStoreOpen, time.Duration(index)*6*time.Hour), options)
		if err != nil {
			t.Fatalf("fill outbox at %d: %v", index, err)
		}
		state.Outbox[index].Deliveries["buzz"] = DeliveryRecord{
			Status:        DeliveryAmbiguous,
			Attempts:      1,
			LastAttemptAt: stateAt(time.Duration(index) * 6 * time.Hour),
			ErrorCode:     "buzz_publish_timeout",
		}
	}
	if err := store.Replace(state); err != nil {
		t.Fatal(err)
	}

	var calls int
	provider := &fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
		calls++
		return Receipt{}, errors.New("ambiguous delivery must not be retried")
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider})
	err := manager.Observe(t.Context(), managerFailed(FailureAppleNotesPermission, time.Duration(maxStateEnvelopes)*6*time.Hour))
	if err == nil || err.Error() != "notification_state_transition_failed" {
		t.Fatalf("Observe error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("ambiguous deliveries retried during capacity recovery: %d", calls)
	}
	persisted, loadErr := store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(persisted, state) {
		t.Fatalf("capacity failure mutated ambiguous outbox:\n got: %#v\nwant: %#v", persisted, state)
	}
}

func TestManagerFullAmbiguousOutboxRetriesOnlyMatchingDueReminder(t *testing.T) {
	store := newManagerStore(t)
	options := managerOptions()
	options.Providers = []string{"buzz"}
	state := EmptyState()
	for index := 0; index < maxStateEnvelopes; index++ {
		var err error
		state, _, err = Observe(state, managerFailed(FailureStoreOpen, time.Duration(index)*6*time.Hour), options)
		if err != nil {
			t.Fatalf("fill outbox at %d: %v", index, err)
		}
		state.Outbox[index].Deliveries["buzz"] = DeliveryRecord{
			Status:        DeliveryAmbiguous,
			Attempts:      1,
			LastAttemptAt: stateAt(time.Duration(index) * 6 * time.Hour),
			ErrorCode:     "buzz_publish_timeout",
		}
	}
	if err := store.Replace(state); err != nil {
		t.Fatal(err)
	}

	now := stateAt(time.Duration(maxStateEnvelopes) * 6 * time.Hour)
	var delivered []Notification
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		delivered = append(delivered, notification)
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, time.Duration(maxStateEnvelopes)*6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != maxStateEnvelopes+1 {
		t.Fatalf("matching reminder delivered %d notifications, want %d", len(delivered), maxStateEnvelopes+1)
	}
	for index, notification := range delivered[:maxStateEnvelopes] {
		if notification.IncidentIDs[0] != state.Outbox[index].Event.Incidents[0].ID {
			t.Fatalf("delivery %d retried unrelated ambiguous incident: %#v", index, notification)
		}
	}
	if current := delivered[len(delivered)-1]; current.Kind != EventReminder || current.Occurrences != maxStateEnvelopes+1 {
		t.Fatalf("current reminder = %#v", current)
	}
}

func TestManagerCancelledDeliveryLeavesValidPendingState(t *testing.T) {
	store := newManagerStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	provider := &fakeProvider{name: "buzz", deliver: func(ctx context.Context, _ Notification) (Receipt, error) {
		return Receipt{}, ctx.Err()
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider})
	if err := manager.Observe(ctx, managerFailed(FailureStoreOpen, 0)); err == nil {
		t.Fatal("cancelled delivery was not surfaced")
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateState(state); err != nil {
		t.Fatalf("cancelled delivery left invalid state: %v", err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].Deliveries["buzz"].Status != DeliveryPending {
		t.Fatalf("cancelled delivery state = %#v", state)
	}
}

func TestManagerTestDoesNotMutateIncidentState(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		if notification.Kind != EventTest {
			t.Fatalf("test notification = %#v", notification)
		}
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	receipt, err := manager.Test(t.Context(), "buzz")
	if err != nil || receipt.Provider != "buzz" {
		t.Fatalf("Test receipt=%#v err=%v", receipt, err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, EmptyState()) {
		t.Fatalf("test delivery mutated state: %#v", state)
	}
}

func TestManagerStatusReturnsIndependentValidatedSnapshot(t *testing.T) {
	store := newManagerStore(t)
	manager := NewManager(managerOptions(), store, []Provider{&fakeProvider{name: "buzz", deliver: func(context.Context, Notification) (Receipt, error) {
		return Receipt{}, NewDeliveryError(DeliveryErrorTemporary, "buzz_offline", errors.New("offline"))
	}}})
	_ = manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0))
	first, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	first.Incidents = nil
	second, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateState(second); err != nil {
		t.Fatalf("status aliases persisted state: %v", err)
	}
}

func TestManagerSerializesConcurrentObservations(t *testing.T) {
	store := newManagerStore(t)
	now := stateAt(0)
	var mu sync.Mutex
	var calls int
	provider := &fakeProvider{name: "buzz", deliver: func(_ context.Context, notification Notification) (Receipt, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return Receipt{Provider: "buzz", ExternalID: notification.ID, AcceptedAt: now}, nil
	}}
	manager := NewManager(managerOptions(), store, []Provider{provider}, WithClock(func() time.Time { return now }))
	var wg sync.WaitGroup
	for index := 0; index < 20; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.Observe(t.Context(), managerFailed(FailureStoreOpen, 0)); err != nil {
				t.Errorf("Observe: %v", err)
			}
		}()
	}
	wg.Wait()
	state, err := manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	incident := state.Incidents[incidentKey(OperationScheduledSyncAll, FailureStoreOpen)]
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || incident.Occurrences != 20 {
		t.Fatalf("calls=%d incident=%#v", calls, incident)
	}
}
