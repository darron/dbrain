package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCatalogRejectsUnknownFailureType(t *testing.T) {
	if _, ok := LookupFailure("raw-provider-error"); ok {
		t.Fatal("unknown failure type accepted")
	}
}

func TestCatalogContainsOnlyBoundedSafePresentationData(t *testing.T) {
	for _, failureType := range KnownFailureTypes() {
		definition, ok := LookupFailure(failureType)
		if !ok {
			t.Fatalf("known failure type %q is not registered", failureType)
		}
		if definition.Type != failureType || definition.ErrorCode == "" || definition.Title == "" || definition.Action == "" {
			t.Fatalf("incomplete catalog definition: %#v", definition)
		}
		if len(definition.ErrorCode) > 64 || len(definition.Title) > 160 || len(definition.Action) > 512 {
			t.Fatalf("unbounded catalog definition: %#v", definition)
		}
	}
}

func TestCatalogGeneratesOnlyKnownSyncStages(t *testing.T) {
	tests := []struct {
		stage string
		want  FailureType
		ok    bool
	}{
		{"apple_notes", "sync.stage.apple_notes.failed", true},
		{"safari_tabs", "sync.stage.safari_tabs.failed", true},
		{"x_frontier", "sync.stage.x_frontier.failed", true},
		{"x_media", "sync.stage.x_media.failed", true},
		{"x_photo_ocr", "sync.stage.x_photo_ocr.failed", true},
		{"github", "sync.stage.github.failed", true},
		{"youtube", "sync.stage.youtube.failed", true},
		{"feeds", "sync.stage.feeds.failed", true},
		{"sources", "sync.stage.sources.failed", true},
		{"categorize", "sync.stage.categorize.failed", true},
		{"media_archive", "sync.stage.media_archive.failed", true},
		{"okf_export", "sync.stage.okf_export.failed", true},
		{"new_provider_stage", "", false},
	}
	for _, test := range tests {
		t.Run(test.stage, func(t *testing.T) {
			got, ok := LookupStageFailure(test.stage)
			if ok != test.ok || got != test.want {
				t.Fatalf("LookupStageFailure(%q) = %q, %t; want %q, %t", test.stage, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestValidateNotificationEnforcesExplicitBounds(t *testing.T) {
	observedAt := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	valid, err := BuildFailureMessage(Incident{
		ID:          "inc_c52c70e4db1a4174bb4b7ec9",
		Operation:   OperationScheduledSyncAll,
		FailureType: FailureAppleNotesPermission,
		FirstSeenAt: observedAt,
		LastSeenAt:  observedAt,
		Occurrences: 1,
	}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Notification)
	}{
		{name: "notification ID bytes", mutate: func(n *Notification) { n.ID = strings.Repeat("a", 65) }},
		{name: "incident ID bytes", mutate: func(n *Notification) { n.IncidentIDs[0] = strings.Repeat("b", 65) }},
		{name: "incident count", mutate: func(n *Notification) { n.IncidentIDs = make([]string, 65) }},
		{name: "failure type count", mutate: func(n *Notification) { n.FailureTypes = make([]FailureType, 65) }},
		{name: "title bytes", mutate: func(n *Notification) { n.Title = strings.Repeat("t", 161) }},
		{name: "body bytes", mutate: func(n *Notification) { n.Body = strings.Repeat("b", 4097) }},
		{name: "invalid UTF-8 title", mutate: func(n *Notification) { n.Title = string([]byte{0xff}) }},
		{name: "invalid UTF-8 body", mutate: func(n *Notification) { n.Body = string([]byte{0xff}) }},
		{name: "mismatched incident and type lists", mutate: func(n *Notification) { n.FailureTypes = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.IncidentIDs = append([]string(nil), valid.IncidentIDs...)
			candidate.FailureTypes = append([]FailureType(nil), valid.FailureTypes...)
			test.mutate(&candidate)
			if err := ValidateNotification(candidate); err == nil {
				t.Fatalf("unbounded notification accepted: %#v", candidate)
			}
		})
	}
}

func TestValidateOutcomeRejectsEveryStatusOutsideClosedEnum(t *testing.T) {
	for _, status := range []OutcomeStatus{"", "ok", "error", "skipped", "partial", "failure "} {
		outcome := Outcome{Operation: OperationScheduledSyncAll, Status: status}
		if err := ValidateOutcome(outcome); err == nil {
			t.Fatalf("status %q accepted", status)
		}
	}
}

type catalogTestProvider struct{}

func (catalogTestProvider) Name() string { return "test" }
func (catalogTestProvider) Deliver(context.Context, Notification) (Receipt, error) {
	return Receipt{Provider: "test"}, nil
}

func TestProviderContractIsProviderNeutral(t *testing.T) {
	var provider Provider = catalogTestProvider{}
	if provider.Name() != "test" {
		t.Fatalf("provider name = %q", provider.Name())
	}
}
