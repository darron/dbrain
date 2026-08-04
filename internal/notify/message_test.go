package notify

import (
	"strings"
	"testing"
	"time"
)

func TestBuildFailureMessageUsesOnlyCatalogPresentation(t *testing.T) {
	incident := Incident{
		ID:          "inc_6be177917afeb144381bc47c",
		Operation:   OperationScheduledSyncAll,
		FailureType: FailureAppleNotesPermission,
		Phase:       IncidentOpen,
		FirstSeenAt: time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC),
		LastSeenAt:  time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC),
		Occurrences: 1,
	}
	got, err := BuildFailureMessage(incident, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := "dbrain scheduled sync failed: Apple Notes permission denied.\n" +
		"Occurrences: 1.\n" +
		"First observed: 2026-08-03T23:35:08Z.\n" +
		"Action: Grant Full Disk Access to the installed dbrain service binary, then restart the service.\n" +
		"Further notifications for this error type are suppressed for 6h.\n" +
		"Incident: inc_6be177917afeb144381bc47c"
	if got.Body != want {
		t.Fatalf("failure body:\n%s\nwant:\n%s", got.Body, want)
	}
	if got.Title != "dbrain scheduled sync failed: Apple Notes permission denied" || got.Kind != EventFailure || got.Occurrences != 1 {
		t.Fatalf("failure notification = %#v", got)
	}
}

func TestBuildReminderMessageIncludesAccumulatedCountDurationAndSameIncident(t *testing.T) {
	incident := Incident{
		ID:          "inc_6be177917afeb144381bc47c",
		Operation:   OperationScheduledSyncAll,
		FailureType: FailureAppleNotesPermission,
		Phase:       IncidentOpen,
		FirstSeenAt: time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC),
		LastSeenAt:  time.Date(2026, 8, 4, 5, 35, 8, 0, time.UTC),
		Occurrences: 4,
	}
	got, err := BuildReminderMessage(incident, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{
		"Still failing: Apple Notes permission denied.",
		"Occurrences: 4.",
		"Duration: 6h.",
		"Further notifications for this error type are suppressed for 6h.",
		"Incident: inc_6be177917afeb144381bc47c",
	} {
		if !strings.Contains(got.Body, literal) {
			t.Fatalf("reminder omitted %q:\n%s", literal, got.Body)
		}
	}
	if got.Kind != EventReminder || len(got.IncidentIDs) != 1 || got.IncidentIDs[0] != incident.ID {
		t.Fatalf("reminder notification = %#v", got)
	}
}

func TestBuildRecoveryMessageUsesCatalogOrderAndMakesNoReadPromise(t *testing.T) {
	createdAt := time.Date(2026, 8, 4, 6, 0, 0, 0, time.UTC)
	incidents := []Incident{
		{ID: "inc_semantic", Operation: OperationScheduledSyncAll, FailureType: FailureType("sync.semantic.semantic_verify_failed"), FirstSeenAt: createdAt.Add(-time.Hour), LastSeenAt: createdAt.Add(-time.Minute), Occurrences: 2},
		{ID: "inc_store", Operation: OperationScheduledSyncAll, FailureType: FailureStoreOpen, FirstSeenAt: createdAt.Add(-2 * time.Hour), LastSeenAt: createdAt.Add(-2 * time.Minute), Occurrences: 1},
	}
	got, err := BuildRecoveryMessage(incidents, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	store := strings.Index(got.Body, "dbrain store could not be opened (incident inc_store)")
	semantic := strings.Index(got.Body, "Semantic index verification failed (incident inc_semantic)")
	if store < 0 || semantic < 0 || store >= semantic {
		t.Fatalf("recovery is not in catalog order:\n%s", got.Body)
	}
	if strings.Contains(strings.ToLower(got.Body), "you were notified") || strings.Contains(strings.ToLower(got.Body), "previous message") {
		t.Fatalf("recovery promised prior delivery/read:\n%s", got.Body)
	}
	if got.Title != "dbrain scheduled sync recovered" || got.Kind != EventRecovery || got.CreatedAt != createdAt {
		t.Fatalf("recovery notification = %#v", got)
	}
}
