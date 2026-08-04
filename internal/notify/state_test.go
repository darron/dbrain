package notify

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var stateTestStart = time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)

func stateAt(offset time.Duration) time.Time { return stateTestStart.Add(offset) }

func stateFailed(failureType FailureType, offset time.Duration) Outcome {
	definition, ok := LookupFailure(failureType)
	if !ok {
		panic("test uses unknown failure type")
	}
	at := stateAt(offset)
	return Outcome{Operation: OperationScheduledSyncAll, Status: OutcomeFailure, FailureType: failureType, ErrorCode: definition.ErrorCode, StartedAt: at.Add(-time.Minute), FinishedAt: at}
}

func stateSuccess(offset time.Duration) Outcome {
	at := stateAt(offset)
	return Outcome{Operation: OperationScheduledSyncAll, Status: OutcomeSuccess, StartedAt: at.Add(-time.Minute), FinishedAt: at}
}

func stateOptions() Options { return Options{RepeatAfter: 6 * time.Hour, Providers: []string{"buzz"}} }

func requireStateNotifications(t *testing.T, decision Decision, kinds ...EventKind) {
	t.Helper()
	if len(decision.Notifications) != len(kinds) {
		t.Fatalf("notifications = %#v, want kinds %v", decision.Notifications, kinds)
	}
	for index, kind := range kinds {
		if decision.Notifications[index].Kind != kind {
			t.Fatalf("notification[%d].Kind = %q, want %q", index, decision.Notifications[index].Kind, kind)
		}
	}
}

func incidentFor(t *testing.T, state State, failureType FailureType) Incident {
	t.Helper()
	incident, ok := state.Incidents[string(OperationScheduledSyncAll)+"\x00"+string(failureType)]
	if !ok {
		t.Fatalf("incident %q not found in %#v", failureType, state.Incidents)
	}
	return incident
}

func TestObserveSuppressesSameTypeButNotDifferentType(t *testing.T) {
	state := EmptyState()
	state, decision, err := Observe(state, stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision, EventFailure)

	state, decision, err = Observe(state, stateFailed(FailureAppleNotesPermission, 5*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision)

	state, decision, err = Observe(state, stateFailed(FailureStoreOpen, 5*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision, EventFailure)
	if len(state.Incidents) != 2 {
		t.Fatalf("incident count = %d, want 2", len(state.Incidents))
	}
}

func TestObserveRemindsAtRepeatBoundary(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := Observe(state, stateFailed(FailureAppleNotesPermission, 5*time.Hour+59*time.Minute), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision)
	state, decision, err = Observe(state, stateFailed(FailureAppleNotesPermission, 6*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision, EventReminder)
	if decision.Notifications[0].Occurrences != 3 {
		t.Fatalf("reminder occurrences = %d, want 3", decision.Notifications[0].Occurrences)
	}
	if incidentFor(t, state, FailureAppleNotesPermission).LastFailureEnqueuedAt != stateAt(6*time.Hour) {
		t.Fatal("repeat window did not advance from reminder time")
	}
}

func TestObserveSuccessCreatesOneConsolidatedRecovery(t *testing.T) {
	state, first, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, second, err := Observe(state, stateFailed(FailureStoreOpen, time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, recovery, err := Observe(state, stateSuccess(2*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, first, EventFailure)
	requireStateNotifications(t, second, EventFailure)
	requireStateNotifications(t, recovery, EventRecovery)
	got := recovery.Notifications[0]
	if !reflect.DeepEqual(got.FailureTypes, []FailureType{FailureStoreOpen, FailureAppleNotesPermission}) {
		t.Fatalf("recovery failure types = %v", got.FailureTypes)
	}
	if len(got.IncidentIDs) != 2 {
		t.Fatalf("recovery incident IDs = %v", got.IncidentIDs)
	}
	for _, failureType := range []FailureType{FailureAppleNotesPermission, FailureStoreOpen} {
		incident := incidentFor(t, state, failureType)
		if incident.Phase != IncidentCooling || incident.NeedsRecovery || incident.RecoveryNotifiedAt != stateAt(2*time.Hour) {
			t.Fatalf("recovered incident = %#v", incident)
		}
	}
}

func TestObserveCancellationIsExactNoOp(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := Outcome{Operation: OperationScheduledSyncAll, Status: OutcomeCancelled, StartedAt: stateAt(time.Hour), FinishedAt: stateAt(time.Hour)}
	afterState, decision, err := Observe(state, cancelled, stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(afterState)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("cancellation mutated state:\nbefore=%s\nafter=%s", before, after)
	}
	requireStateNotifications(t, decision)
}

func TestObserveCancellationDoesNotPruneExpiredCoolingIncident(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateSuccess(time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := Outcome{Operation: OperationScheduledSyncAll, Status: OutcomeCancelled, StartedAt: stateAt(7 * time.Hour), FinishedAt: stateAt(7 * time.Hour)}
	afterState, decision, err := Observe(state, cancelled, stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(afterState)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("expired cooling incident was pruned by cancellation:\nbefore=%s\nafter=%s", before, after)
	}
	requireStateNotifications(t, decision)
}

func TestObservePrunesCoolingIncidentAtRearmBoundary(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateSuccess(time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateSuccess(6*time.Hour-time.Nanosecond), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if incidentFor(t, state, FailureStoreOpen).Phase != IncidentCooling {
		t.Fatal("cooling incident pruned before its rearm boundary")
	}
	state, decision, err := Observe(state, stateSuccess(6*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision)
	if _, exists := state.Incidents[incidentKey(OperationScheduledSyncAll, FailureStoreOpen)]; exists {
		t.Fatalf("cooling incident survived its rearm boundary: %#v", state.Incidents)
	}
}

func TestObserveFlapInsideRepeatWindowReusesIncidentWithoutChatter(t *testing.T) {
	state, failure, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	firstID := incidentFor(t, state, FailureAppleNotesPermission).ID
	state, recovery, err := Observe(state, stateSuccess(time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, recurrent, err := Observe(state, stateFailed(FailureAppleNotesPermission, 2*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, secondSuccess, err := Observe(state, stateSuccess(3*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, failure, EventFailure)
	requireStateNotifications(t, recovery, EventRecovery)
	requireStateNotifications(t, recurrent)
	requireStateNotifications(t, secondSuccess)
	incident := incidentFor(t, state, FailureAppleNotesPermission)
	if incident.ID != firstID || incident.Phase != IncidentCooling || incident.Occurrences != 2 || incident.NeedsRecovery {
		t.Fatalf("flapping incident = %#v", incident)
	}
}

func TestObserveFailureAfterRecoveryAndRepeatBoundaryStartsNewIncident(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	oldID := incidentFor(t, state, FailureAppleNotesPermission).ID
	state, _, err = Observe(state, stateSuccess(time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, decision, err := Observe(state, stateFailed(FailureAppleNotesPermission, 6*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision, EventFailure)
	newIncident := incidentFor(t, state, FailureAppleNotesPermission)
	if newIncident.ID == oldID || newIncident.Occurrences != 1 || newIncident.FirstSeenAt != stateAt(6*time.Hour) {
		t.Fatalf("new episode = %#v, old ID %q", newIncident, oldID)
	}
}

func TestObserveClockRollbackAndDuplicateTimestampCannotBypassSuppression(t *testing.T) {
	state, first, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, rolledBack, err := Observe(state, stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, duplicate, err := Observe(state, stateFailed(FailureStoreOpen, time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, first, EventFailure)
	requireStateNotifications(t, rolledBack)
	requireStateNotifications(t, duplicate)
	incident := incidentFor(t, state, FailureStoreOpen)
	if incident.LastSeenAt != stateAt(time.Hour) || incident.Occurrences != 3 {
		t.Fatalf("rollback state = %#v", incident)
	}
}

func TestObserveRejectsZeroProvidersAndUnknownOutcomeWithoutMutation(t *testing.T) {
	initial := EmptyState()
	for _, test := range []struct {
		name    string
		outcome Outcome
		options Options
	}{
		{name: "zero providers", outcome: stateFailed(FailureStoreOpen, 0), options: Options{RepeatAfter: 6 * time.Hour}},
		{name: "unknown status", outcome: Outcome{Operation: OperationScheduledSyncAll, Status: "skipped"}, options: stateOptions()},
		{name: "unknown failure", outcome: Outcome{Operation: OperationScheduledSyncAll, Status: OutcomeFailure, FailureType: "raw-provider-error", ErrorCode: "private"}, options: stateOptions()},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, decision, err := Observe(initial, test.outcome, test.options)
			if err == nil {
				t.Fatal("invalid transition accepted")
			}
			if !reflect.DeepEqual(got, initial) || len(decision.Notifications) != 0 {
				t.Fatalf("invalid transition mutated state: %#v %#v", got, decision)
			}
		})
	}
}

func TestObserveSuppressionPreservesExistingPendingDelivery(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].Deliveries["buzz"].Status != DeliveryPending {
		t.Fatalf("initial outbox = %#v", state.Outbox)
	}
	before := state.Outbox[0]
	state, decision, err := Observe(state, stateFailed(FailureStoreOpen, time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, decision)
	if len(state.Outbox) != 1 || !reflect.DeepEqual(state.Outbox[0], before) {
		t.Fatalf("pending delivery changed during suppression: %#v", state.Outbox)
	}
}

func TestObserveRepeatedSuccessDuringCoolingIsSilent(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, first, err := Observe(state, stateSuccess(time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, second, err := Observe(state, stateSuccess(2*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, first, EventRecovery)
	requireStateNotifications(t, second)
	if incidentFor(t, state, FailureStoreOpen).RecoveryNotifiedAt != stateAt(time.Hour) {
		t.Fatal("repeated success moved recovery timestamp")
	}
}

func TestObserveSuppressedRecurrenceStaysOpenUntilReminderBoundary(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateFailed(FailureStoreOpen, time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if incidentFor(t, state, FailureStoreOpen).Phase != IncidentOpen {
		t.Fatal("suppressed failure did not remain open")
	}
	_, reminder, err := Observe(state, stateFailed(FailureStoreOpen, 6*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireStateNotifications(t, reminder, EventReminder)
}

func TestObserveAlternatingFailureTypesUseIndependentWindows(t *testing.T) {
	state := EmptyState()
	sequence := []struct {
		outcome Outcome
		kind    EventKind
	}{
		{stateFailed(FailureStoreOpen, 0), EventFailure},
		{stateFailed(FailureAppleNotesPermission, time.Hour), EventFailure},
		{stateFailed(FailureStoreOpen, 2*time.Hour), ""},
		{stateFailed(FailureAppleNotesPermission, 6*time.Hour), ""},
		{stateFailed(FailureStoreOpen, 6*time.Hour), EventReminder},
		{stateFailed(FailureAppleNotesPermission, 7*time.Hour), EventReminder},
	}
	for index, step := range sequence {
		var decision Decision
		var err error
		state, decision, err = Observe(state, step.outcome, stateOptions())
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		if step.kind == "" {
			requireStateNotifications(t, decision)
		} else {
			requireStateNotifications(t, decision, step.kind)
		}
	}
}

func TestObserveHourlyFlapProducesAtMostOneFailureAndRecoveryInsideSixHours(t *testing.T) {
	state := EmptyState()
	counts := map[EventKind]int{}
	for hour := 0; hour < 6; hour++ {
		var decision Decision
		var err error
		state, decision, err = Observe(state, stateFailed(FailureAppleNotesPermission, time.Duration(hour)*time.Hour), stateOptions())
		if err != nil {
			t.Fatal(err)
		}
		for _, notification := range decision.Notifications {
			counts[notification.Kind]++
		}
		state, decision, err = Observe(state, stateSuccess(time.Duration(hour)*time.Hour+30*time.Minute), stateOptions())
		if err != nil {
			t.Fatal(err)
		}
		for _, notification := range decision.Notifications {
			counts[notification.Kind]++
		}
	}
	if counts[EventFailure] != 1 || counts[EventRecovery] != 1 || counts[EventReminder] != 0 {
		t.Fatalf("six-hour flap notifications = %v", counts)
	}
}

func TestEnvelopePersistsOnlyTypedEventFactsAndDerivesCanonicalNotification(t *testing.T) {
	state, decision, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	incident := incidentFor(t, state, FailureAppleNotesPermission)
	notification := decision.Notifications[0]
	if incident.ID == notification.ID || notification.IncidentIDs[0] != incident.ID {
		t.Fatalf("incident ID %q and notification %#v are not separate", incident.ID, notification)
	}
	if len(state.Outbox) != 1 {
		t.Fatalf("persisted envelope count = %d", len(state.Outbox))
	}
	derived, err := DeriveNotification(state.Outbox[0].Event)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(derived, notification) {
		t.Fatalf("derived notification = %#v, want %#v", derived, notification)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"notification"`, `"title"`, `"body"`, "dbrain scheduled sync failed", "Full Disk Access"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("persisted envelope contains rendered notification content %q: %s", forbidden, encoded)
		}
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := ValidateState(restored); err != nil {
		t.Fatalf("restored typed event state is invalid: %v", err)
	}
	retry, err := DeriveNotification(restored.Outbox[0].Event)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retry, notification) {
		t.Fatalf("retry derivation changed notification: %#v", retry)
	}
}

func TestObserveUsesHandDerivedDeterministicBoundedIDs(t *testing.T) {
	state, decision, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	incident := incidentFor(t, state, FailureAppleNotesPermission)
	if incident.ID != "inc_c52c70e4db1a4174bb4b7ec9" {
		t.Fatalf("incident ID = %q", incident.ID)
	}
	if decision.Notifications[0].ID != "ntf_2cb3af6bfda48ce2fdcf319b" {
		t.Fatalf("notification ID = %q", decision.Notifications[0].ID)
	}
}

func TestObserveRejectsInvalidOrOversizedStateInsteadOfResetting(t *testing.T) {
	invalid := EmptyState()
	invalid.Schema = "unknown.schema"
	if got, decision, err := Observe(invalid, stateSuccess(0), stateOptions()); err == nil || !reflect.DeepEqual(got, invalid) || len(decision.Notifications) != 0 {
		t.Fatalf("invalid schema transition = %#v %#v %v", got, decision, err)
	}
	tooMany := EmptyState()
	for index := 0; index < 65; index++ {
		key := time.Duration(index).String()
		tooMany.Incidents[key] = Incident{ID: "inc_012345678901234567890123", Operation: OperationScheduledSyncAll, FailureType: FailureUnknown, Phase: IncidentOpen, FirstSeenAt: stateAt(0), LastSeenAt: stateAt(0), Occurrences: 1, LastFailureEnqueuedAt: stateAt(0), NeedsRecovery: true}
	}
	if err := ValidateState(tooMany); err == nil {
		t.Fatal("65 incidents accepted")
	}
	missingOutbox := EmptyState()
	missingOutbox.Outbox = nil
	if err := ValidateState(missingOutbox); err == nil {
		t.Fatal("null outbox accepted")
	}
	tooManyProviders := stateOptions()
	for index := 0; index < 16; index++ {
		tooManyProviders.Providers = append(tooManyProviders.Providers, time.Duration(index).String())
	}
	if _, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), tooManyProviders); err == nil {
		t.Fatal("17 providers accepted")
	}
}

func TestValidateStateRejectsForgedIncidentIdentityAndChronology(t *testing.T) {
	valid, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	key := incidentKey(OperationScheduledSyncAll, FailureStoreOpen)
	tests := []struct {
		name   string
		mutate func(*Incident)
	}{
		{name: "forged deterministic ID", mutate: func(incident *Incident) { incident.ID = "forged\nprovider text" }},
		{name: "enqueue before first seen", mutate: func(incident *Incident) { incident.LastFailureEnqueuedAt = incident.FirstSeenAt.Add(-time.Second) }},
		{name: "enqueue after last seen", mutate: func(incident *Incident) { incident.LastFailureEnqueuedAt = incident.LastSeenAt.Add(time.Hour) }},
		{name: "open without recovery need or prior recovery", mutate: func(incident *Incident) { incident.NeedsRecovery = false }},
		{name: "cooling still needs recovery", mutate: func(incident *Incident) {
			incident.Phase = IncidentCooling
			incident.NeedsRecovery = true
			incident.RecoveryNotifiedAt = incident.LastSeenAt
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneState(valid)
			incident := candidate.Incidents[key]
			test.mutate(&incident)
			candidate.Incidents[key] = incident
			if err := ValidateState(candidate); err == nil {
				t.Fatalf("corrupt incident accepted: %#v", incident)
			}
		})
	}
}

func TestValidateStateRejectsForgedTypedEventFacts(t *testing.T) {
	failureState, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	recoveryState, _, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	recoveryState, _, err = Observe(recoveryState, stateFailed(FailureStoreOpen, time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	recoveryState, _, err = Observe(recoveryState, stateSuccess(2*time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		state         State
		envelopeIndex int
		mutate        func(*EventFacts)
	}{
		{name: "digest-shaped forged failure incident ID", state: failureState, mutate: func(event *EventFacts) {
			event.Incidents[0].ID = "inc_000000000000000000000000"
		}},
		{name: "unknown failure type", state: failureState, mutate: func(event *EventFacts) {
			event.Incidents[0].FailureType = "sync.raw.secret"
		}},
		{name: "zero occurrence count", state: failureState, mutate: func(event *EventFacts) {
			event.Incidents[0].Occurrences = 0
		}},
		{name: "zero suppression", state: failureState, mutate: func(event *EventFacts) {
			event.SuppressionAfter = 0
		}},
		{name: "negative suppression", state: failureState, mutate: func(event *EventFacts) {
			event.SuppressionAfter = -time.Hour
		}},
		{name: "last seen before first", state: failureState, mutate: func(event *EventFacts) {
			event.Incidents[0].LastSeenAt = event.Incidents[0].FirstSeenAt.Add(-time.Second)
		}},
		{name: "created before last seen", state: failureState, mutate: func(event *EventFacts) {
			event.CreatedAt = event.Incidents[0].LastSeenAt.Add(-time.Second)
		}},
		{name: "too many incident snapshots", state: recoveryState, envelopeIndex: 2, mutate: func(event *EventFacts) {
			event.Incidents = make([]IncidentSnapshot, 65)
		}},
		{name: "digest-shaped forged recovery incident ID", state: recoveryState, envelopeIndex: 2, mutate: func(event *EventFacts) {
			event.Incidents[0].ID = "inc_000000000000000000000000"
		}},
		{name: "duplicate recovery incident", state: recoveryState, envelopeIndex: 2, mutate: func(event *EventFacts) {
			event.Incidents[1] = event.Incidents[0]
		}},
		{name: "recovery not in catalog order", state: recoveryState, envelopeIndex: 2, mutate: func(event *EventFacts) {
			event.Incidents[0], event.Incidents[1] = event.Incidents[1], event.Incidents[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneState(test.state)
			test.mutate(&candidate.Outbox[test.envelopeIndex].Event)
			if err := ValidateState(candidate); err == nil {
				t.Fatalf("forged event facts accepted: %#v", candidate.Outbox[test.envelopeIndex].Event)
			}
		})
	}
}

func TestDeriveNotificationUsesValidatedTypedFactsAndCatalogOnly(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureAppleNotesPermission, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	got, err := DeriveNotification(state.Outbox[0].Event)
	if err != nil {
		t.Fatal(err)
	}
	wantBody := "dbrain scheduled sync failed: Apple Notes permission denied.\n" +
		"Occurrences: 1.\n" +
		"First observed: 2026-08-03T23:35:08Z.\n" +
		"Action: Grant Full Disk Access to the installed dbrain service binary, then restart the service.\n" +
		"Further notifications for this error type are suppressed for 6h.\n" +
		"Incident: inc_c52c70e4db1a4174bb4b7ec9"
	if got.ID != "ntf_2cb3af6bfda48ce2fdcf319b" || got.Title != "dbrain scheduled sync failed: Apple Notes permission denied" || got.Body != wantBody {
		t.Fatalf("derived notification = %#v", got)
	}
	forged := state.Outbox[0].Event
	forged.Incidents[0].ID = "inc_000000000000000000000000"
	if _, err := DeriveNotification(forged); err == nil {
		t.Fatal("derived notification from invalid event facts")
	}
}

func TestValidateStateAllowsHistoricalEventFactsAfterEpisodeReplacement(t *testing.T) {
	state, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = Observe(state, stateSuccess(time.Hour), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	oldIncidentID := state.Outbox[0].Event.Incidents[0].ID
	state, _, err = Observe(state, stateFailed(FailureStoreOpen, 6*time.Hour), stateOptions())
	if err != nil {
		t.Fatalf("historical pending envelopes invalidated by replacement: %v", err)
	}
	current := incidentFor(t, state, FailureStoreOpen)
	if current.ID == oldIncidentID {
		t.Fatal("test did not create a replacement episode")
	}
	if err := ValidateState(state); err != nil {
		t.Fatalf("historical event facts rejected: %v", err)
	}
	for index := range state.Outbox {
		if _, err := DeriveNotification(state.Outbox[index].Event); err != nil {
			t.Fatalf("derive historical envelope %d: %v", index, err)
		}
	}
}

func TestValidateStateRejectsImpossibleAcceptedDelivery(t *testing.T) {
	valid, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		record DeliveryRecord
	}{
		{name: "zero attempts", record: DeliveryRecord{Status: DeliveryAccepted, Receipt: Receipt{Provider: "buzz", ExternalID: "event", AcceptedAt: stateAt(0)}}},
		{name: "missing receipt", record: DeliveryRecord{Status: DeliveryAccepted, Attempts: 1, LastAttemptAt: stateAt(0)}},
		{name: "mismatched provider", record: DeliveryRecord{Status: DeliveryAccepted, Attempts: 1, LastAttemptAt: stateAt(0), Receipt: Receipt{Provider: "slack", ExternalID: "event", AcceptedAt: stateAt(0)}}},
		{name: "zero acceptance time", record: DeliveryRecord{Status: DeliveryAccepted, Attempts: 1, LastAttemptAt: stateAt(0), Receipt: Receipt{Provider: "buzz", ExternalID: "event"}}},
		{name: "unbounded external ID", record: DeliveryRecord{Status: DeliveryAccepted, Attempts: 1, LastAttemptAt: stateAt(0), Receipt: Receipt{Provider: "buzz", ExternalID: string(make([]byte, 257)), AcceptedAt: stateAt(0)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneState(valid)
			candidate.Outbox[0].Deliveries["buzz"] = test.record
			if err := ValidateState(candidate); err == nil {
				t.Fatalf("impossible accepted delivery accepted: %#v", test.record)
			}
		})
	}
}

func TestDeliverySummaryRetainsTerminalErrorCode(t *testing.T) {
	summary := DeliverySummary{Status: DeliveryPermanentError, ErrorCode: "buzz_auth_rejected"}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"status":"permanent_error","error_code":"buzz_auth_rejected","at":"0001-01-01T00:00:00Z"}` {
		t.Fatalf("delivery summary JSON = %s", encoded)
	}
}

func TestValidateStateRejectsUnsafeLastDelivery(t *testing.T) {
	valid := EmptyState()
	tests := []struct {
		name    string
		summary DeliverySummary
	}{
		{name: "missing notification ID", summary: DeliverySummary{Provider: "buzz", Kind: EventFailure, Status: DeliveryAccepted, At: stateAt(0)}},
		{name: "unbounded provider", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: string(make([]byte, 65)), Kind: EventFailure, Status: DeliveryAccepted, At: stateAt(0)}},
		{name: "invalid kind", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: "buzz", Kind: "raw", Status: DeliveryAccepted, At: stateAt(0)}},
		{name: "pending is not terminal", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: "buzz", Kind: EventFailure, Status: DeliveryPending, At: stateAt(0)}},
		{name: "accepted with error", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: "buzz", Kind: EventFailure, Status: DeliveryAccepted, ErrorCode: "buzz_error", At: stateAt(0)}},
		{name: "permanent without error", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: "buzz", Kind: EventFailure, Status: DeliveryPermanentError, At: stateAt(0)}},
		{name: "unsafe error code", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: "buzz", Kind: EventFailure, Status: DeliveryPermanentError, ErrorCode: "provider secret text", At: stateAt(0)}},
		{name: "zero time", summary: DeliverySummary{NotificationID: "ntf_012345678901234567890123", Provider: "buzz", Kind: EventFailure, Status: DeliveryAccepted}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.LastDelivery = test.summary
			if err := ValidateState(candidate); err == nil {
				t.Fatalf("unsafe delivery summary accepted: %#v", test.summary)
			}
		})
	}
}
