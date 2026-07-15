package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func alertReport(profile Profile, id CheckID, status Status, required bool, skip SkipReason, at time.Time) Report {
	overall := status
	if status == StatusSkipped {
		overall = StatusPass
	}
	return Report{
		Profile: profile, Status: overall, CompletedAt: at,
		Checks: []Check{{ID: id, Status: status, Required: required, SkipReason: skip, Summary: "attacker-controlled summary"}},
	}
}

func TestAlertTransitionsCoverApprovedTable(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	id := CheckSchedulerLatestSync
	confirmed := func(status Status, notified time.Time) AlertState {
		return AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{
			ProfileFast: {Checks: map[CheckID]CheckAlertState{id: {Confirmed: status, LastObserved: status, Required: true, LastNotifiedAt: notified}}},
		}}
	}
	tests := []struct {
		name        string
		initial     AlertState
		report      Report
		want        Status
		wantPending Status
		wantCount   int
		wantNotify  bool
		wantReason  AlertResolutionReason
	}{
		{name: "none pass establishes baseline", initial: emptyAlertState(), report: alertReport(ProfileFast, id, StatusPass, true, "", now), want: StatusPass},
		{name: "none warning begins debounce", initial: emptyAlertState(), report: alertReport(ProfileFast, id, StatusWarn, true, "", now), wantPending: StatusWarn, wantCount: 1},
		{name: "second warning confirms and notifies", initial: AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileFast: {Checks: map[CheckID]CheckAlertState{id: {Pending: StatusWarn, PendingCount: 1, LastObserved: StatusWarn, Required: true}}}}}, report: alertReport(ProfileFast, id, StatusWarn, true, "", now), want: StatusWarn, wantNotify: true},
		{name: "higher severity resets pending", initial: confirmed(StatusWarn, now.Add(-time.Hour)), report: alertReport(ProfileFast, id, StatusFail, true, "", now), want: StatusWarn, wantPending: StatusFail, wantCount: 1},
		{name: "lower nonpass resets pending", initial: confirmed(StatusFail, now.Add(-time.Hour)), report: alertReport(ProfileFast, id, StatusUnknown, true, "", now), want: StatusFail, wantPending: StatusUnknown, wantCount: 1},
		{name: "same confirmed suppresses before repeat", initial: confirmed(StatusFail, now.Add(-time.Hour)), report: alertReport(ProfileFast, id, StatusFail, true, "", now), want: StatusFail},
		{name: "same confirmed repeats after interval", initial: confirmed(StatusFail, now.Add(-25*time.Hour)), report: alertReport(ProfileFast, id, StatusFail, true, "", now), want: StatusFail, wantNotify: true},
		{name: "pass recovers immediately", initial: confirmed(StatusFail, now.Add(-time.Hour)), report: alertReport(ProfileFast, id, StatusPass, true, "", now), want: StatusPass, wantNotify: true},
		{name: "feature disabled resolves immediately", initial: confirmed(StatusFail, now.Add(-time.Hour)), report: alertReport(ProfileFast, id, StatusSkipped, false, SkipFeatureDisabled, now), want: StatusPass, wantNotify: true, wantReason: AlertResolvedByConfiguration},
		{name: "required becomes optional resolves", initial: confirmed(StatusFail, now.Add(-time.Hour)), report: alertReport(ProfileFast, id, StatusFail, false, "", now), want: StatusFail, wantNotify: true, wantReason: AlertResolvedByConfiguration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next, decision, err := ApplyAlertObservation(test.initial, test.report, AlertOptions{ConsecutiveObservations: 2, RepeatAfter: 24 * time.Hour, WebhookConfigured: true, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			got := next.Profiles[ProfileFast].Checks[id]
			if got.Confirmed != test.want || got.Pending != test.wantPending || got.PendingCount != test.wantCount || decision.Notify != test.wantNotify || got.ResolutionReason != test.wantReason {
				t.Fatalf("state=%#v decision=%#v", got, decision)
			}
			for _, change := range decision.Changes {
				if change.Summary != fixedSummary(change.CheckID) || strings.Contains(change.Summary, "attacker") {
					t.Fatalf("non-fixed alert summary = %q", change.Summary)
				}
			}
		})
	}
}

func TestAlertProfileExclusionOptionalAndMissingWebhookAreNonNotifying(t *testing.T) {
	now := time.Now().UTC()
	id := CheckSchedulerLatestSync
	initial := AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileStandard: {Checks: map[CheckID]CheckAlertState{id: {Confirmed: StatusFail, Required: true}}}}}
	next, decision, err := ApplyAlertObservation(initial, alertReport(ProfileFast, id, StatusSkipped, false, SkipProfileExcluded, now), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: true, Now: now})
	if err != nil || decision.Notify || len(next.Profiles) != len(initial.Profiles) {
		t.Fatalf("profile exclusion changed state: %#v %#v %v", next, decision, err)
	}
	_, optionalDecision, err := ApplyAlertObservation(emptyAlertState(), alertReport(ProfileFast, id, StatusFail, false, "", now), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: true, Now: now})
	if err != nil || optionalDecision.Notify {
		t.Fatalf("optional finding notified: %#v %v", optionalDecision, err)
	}
	noWebhook, noWebhookDecision, err := ApplyAlertObservation(emptyAlertState(), alertReport(ProfileFast, CheckIntegritySchemaIdentity, StatusFail, true, "", now), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: false, Now: now})
	if err != nil || noWebhookDecision.Notify || noWebhook.Profiles[ProfileFast].Checks[CheckIntegritySchemaIdentity].Confirmed != StatusFail {
		t.Fatalf("missing webhook transition = %#v %#v %v", noWebhook, noWebhookDecision, err)
	}
}

func TestAlertOptionalNonPassMustDebounceWhenItBecomesRequired(t *testing.T) {
	now := time.Now().UTC()
	id := CheckSchedulerLatestSync
	optional, decision, err := ApplyAlertObservation(emptyAlertState(), alertReport(ProfileFast, id, StatusFail, false, "", now), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: true, Now: now})
	if err != nil || decision.Notify {
		t.Fatalf("optional baseline = %#v, %v", decision, err)
	}
	required, decision, err := ApplyAlertObservation(optional, alertReport(ProfileFast, id, StatusFail, true, "", now.Add(time.Minute)), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: true, Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	got := required.Profiles[ProfileFast].Checks[id]
	if decision.Notify || got.Confirmed != "" || got.Pending != StatusFail || got.PendingCount != 1 || !got.Required {
		t.Fatalf("first required observation bypassed debounce: state=%#v decision=%#v", got, decision)
	}
}

func TestAlertImmediateFailureExceptionsAreExact(t *testing.T) {
	now := time.Now().UTC()
	immediate := map[CheckID]bool{
		CheckIntegritySchemaIdentity: true, CheckIntegrityMigrationCompatibility: true,
		CheckIntegritySQLiteQuickCheck: true, CheckIntegrityForeignKeys: true,
		CheckDurabilityMediaRemote: true, CheckDurabilitySQLiteRestore: true,
	}
	for _, entry := range Registry() {
		next, decision, err := ApplyAlertObservation(emptyAlertState(), alertReport(ProfileStandard, entry.ID, StatusFail, true, "", now), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: true, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		got := next.Profiles[ProfileStandard].Checks[entry.ID]
		if immediate[entry.ID] {
			if got.Confirmed != StatusFail || !decision.Notify {
				t.Errorf("%s did not bypass debounce: %#v %#v", entry.ID, got, decision)
			}
		} else if got.Confirmed == StatusFail || decision.Notify {
			t.Errorf("%s unexpectedly bypassed debounce: %#v %#v", entry.ID, got, decision)
		}
	}
	for _, status := range []Status{StatusWarn, StatusUnknown} {
		next, decision, _ := ApplyAlertObservation(emptyAlertState(), alertReport(ProfileStandard, CheckIntegritySchemaIdentity, status, true, "", now), AlertOptions{ConsecutiveObservations: 2, WebhookConfigured: true, Now: now})
		got := next.Profiles[ProfileStandard].Checks[CheckIntegritySchemaIdentity]
		if got.Confirmed != "" || decision.Notify {
			t.Fatalf("%s bypassed debounce: %#v", status, got)
		}
	}
}

func TestAlertPendingResetOverallRecoveryAndDeliveryAcknowledgement(t *testing.T) {
	now := time.Now().UTC()
	firstID, secondID := CheckSchedulerLatestSync, CheckBoundaryRuntime
	initial := AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileFast: {Checks: map[CheckID]CheckAlertState{
		firstID:  {Confirmed: StatusFail, Pending: StatusUnknown, PendingCount: 1, LastObserved: StatusUnknown, Required: true},
		secondID: {Confirmed: StatusWarn, LastObserved: StatusWarn, Required: true},
	}}}}
	report := Report{Profile: ProfileFast, Status: StatusWarn, CompletedAt: now, Checks: []Check{
		{ID: firstID, Status: StatusPass, Required: true},
		{ID: secondID, Status: StatusPass, Required: true},
	}}
	next, decision, err := ApplyAlertObservation(initial, report, AlertOptions{ConsecutiveObservations: 2, RepeatAfter: 24 * time.Hour, WebhookConfigured: true, Now: now})
	if err != nil || !decision.Notify || !decision.OverallRecovery || len(decision.Changes) != 2 {
		t.Fatalf("recovery = %#v %#v %v", next, decision, err)
	}
	if next.Profiles[ProfileFast].Checks[firstID].Pending != "" {
		t.Fatal("pending status was not reset on recovery")
	}
	if !next.Profiles[ProfileFast].Checks[firstID].LastNotifiedAt.IsZero() {
		t.Fatal("transition marked delivered before webhook success")
	}
	acknowledged := MarkAlertDelivered(next, decision, now)
	if !acknowledged.Profiles[ProfileFast].Checks[firstID].LastNotifiedAt.Equal(now) {
		t.Fatal("successful delivery was not acknowledged")
	}
}

func TestAlertFailedDeliveryRetriesTransitionsAndResolvedStatePersists(t *testing.T) {
	now := time.Now().UTC()
	id := CheckSchedulerLatestSync
	tests := []struct {
		name  string
		state AlertState
		first Report
		retry Report
	}{
		{
			name: "escalation", state: AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileFast: {Checks: map[CheckID]CheckAlertState{id: {Confirmed: StatusWarn, Required: true}}}}},
			first: alertReport(ProfileFast, id, StatusFail, true, "", now), retry: alertReport(ProfileFast, id, StatusFail, true, "", now.Add(time.Minute)),
		},
		{
			name: "recovery", state: AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileFast: {Checks: map[CheckID]CheckAlertState{id: {Confirmed: StatusFail, Required: true}}}}},
			first: alertReport(ProfileFast, id, StatusPass, true, "", now), retry: alertReport(ProfileFast, id, StatusPass, true, "", now.Add(time.Minute)),
		},
		{
			name: "configuration resolution", state: AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileFast: {Checks: map[CheckID]CheckAlertState{id: {Confirmed: StatusFail, Required: true}}}}},
			first: alertReport(ProfileFast, id, StatusSkipped, false, SkipFeatureDisabled, now), retry: alertReport(ProfileFast, id, StatusSkipped, false, SkipFeatureDisabled, now.Add(time.Minute)),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first, decision, err := ApplyAlertObservation(test.state, test.first, AlertOptions{ConsecutiveObservations: 1, WebhookConfigured: true, Now: now})
			if err != nil || !decision.Notify {
				t.Fatalf("first transition = %#v %v", decision, err)
			}
			if err := ValidateAlertState(first); err != nil {
				t.Fatalf("transition state cannot persist: %v", err)
			}
			second, retryDecision, err := ApplyAlertObservation(first, test.retry, AlertOptions{ConsecutiveObservations: 1, WebhookConfigured: true, Now: now.Add(time.Minute)})
			if err != nil || !retryDecision.Notify || !second.Profiles[ProfileFast].Checks[id].DeliveryPending {
				t.Fatalf("failed delivery was consumed: %#v %#v %v", second, retryDecision, err)
			}
		})
	}
}

func TestAlertStateJSONIsStableAndContentFree(t *testing.T) {
	state := AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{ProfileFast: {Checks: map[CheckID]CheckAlertState{CheckBoundaryConfig: {Confirmed: StatusUnknown, Pending: StatusFail, PendingCount: 1, LastObserved: StatusFail, Required: true, ResolutionReason: AlertResolvedByConfiguration}}}}}
	if err := ValidateAlertState(state); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"summary", "evidence", "error", "url", "path", "credential"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("state contains forbidden field %q: %s", forbidden, data)
		}
	}
}

func TestAlertSeverityOrderExcludesSkipped(t *testing.T) {
	previous := -1
	for _, status := range []Status{StatusPass, StatusWarn, StatusUnknown, StatusFail} {
		rank, ok := alertSeverity(status)
		if !ok || rank <= previous {
			t.Fatalf("severity %s = %d, %t after %d", status, rank, ok, previous)
		}
		previous = rank
	}
	if _, ok := alertSeverity(StatusSkipped); ok {
		t.Fatal("skipped was treated as severity")
	}
}

func TestAlertObservationRejectsDuplicateAndInvalidReportShapes(t *testing.T) {
	now := time.Now().UTC()
	duplicate := alertReport(ProfileFast, CheckBoundaryConfig, StatusPass, true, "", now)
	duplicate.Checks = append(duplicate.Checks, duplicate.Checks[0])
	if _, _, err := ApplyAlertObservation(emptyAlertState(), duplicate, AlertOptions{Now: now}); err == nil {
		t.Fatal("duplicate check IDs accepted")
	}
	invalid := alertReport(ProfileFast, CheckBoundaryConfig, StatusPass, true, "", now)
	invalid.CompletedAt = time.Time{}
	if _, _, err := ApplyAlertObservation(emptyAlertState(), invalid, AlertOptions{Now: now}); err == nil {
		t.Fatal("invalid report shape accepted")
	}
}
