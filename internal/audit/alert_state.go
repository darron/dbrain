package audit

import (
	"fmt"
	"sort"
	"time"
)

const AlertStateSchemaV1 = "dbrain.audit.alert-state.v1"

type AlertResolutionReason string

const AlertResolvedByConfiguration AlertResolutionReason = "resolved_by_configuration"

type CheckAlertState struct {
	Confirmed        Status                `json:"confirmed,omitempty"`
	Pending          Status                `json:"pending,omitempty"`
	PendingCount     int                   `json:"pending_count,omitempty"`
	LastObserved     Status                `json:"last_observed,omitempty"`
	Required         bool                  `json:"required"`
	LastNotifiedAt   time.Time             `json:"last_notified_at,omitempty"`
	ResolutionReason AlertResolutionReason `json:"resolution_reason,omitempty"`
	DeliveryPending  bool                  `json:"delivery_pending,omitempty"`
}

type ProfileAlertState struct {
	Checks map[CheckID]CheckAlertState `json:"checks"`
}

type AlertState struct {
	Schema   string                        `json:"schema"`
	Profiles map[Profile]ProfileAlertState `json:"profiles"`
}

func emptyAlertState() AlertState {
	return AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{}}
}

func ValidateAlertState(state AlertState) error {
	if state.Schema != AlertStateSchemaV1 || state.Profiles == nil {
		return fmt.Errorf("invalid audit alert state schema")
	}
	for profile, profileState := range state.Profiles {
		if !profile.Valid() || profileState.Checks == nil {
			return fmt.Errorf("invalid audit alert profile state")
		}
		for id, check := range profileState.Checks {
			if _, ok := Lookup(id); !ok {
				return fmt.Errorf("invalid audit alert check id")
			}
			for _, status := range []Status{check.Confirmed, check.Pending, check.LastObserved} {
				if status != "" && (!status.Valid() || status == StatusSkipped) {
					return fmt.Errorf("invalid audit alert status")
				}
			}
			if check.PendingCount < 0 || (check.Pending == "" && check.PendingCount != 0) {
				return fmt.Errorf("invalid audit alert pending state")
			}
			if check.ResolutionReason != "" && check.ResolutionReason != AlertResolvedByConfiguration {
				return fmt.Errorf("invalid audit alert resolution reason")
			}
		}
	}
	return nil
}

type AlertOptions struct {
	ConsecutiveObservations int
	RepeatAfter             time.Duration
	WebhookConfigured       bool
	Now                     time.Time
}

type AlertChange struct {
	CheckID CheckID               `json:"check_id"`
	Status  Status                `json:"status"`
	Summary string                `json:"summary"`
	Reason  AlertResolutionReason `json:"reason,omitempty"`
}

type AlertDecision struct {
	Profile         Profile       `json:"profile"`
	Notify          bool          `json:"notify"`
	OverallRecovery bool          `json:"overall_recovery"`
	Changes         []AlertChange `json:"changes"`
}

var immediateFailureAlertIDs = map[CheckID]bool{
	CheckIntegritySchemaIdentity:         true,
	CheckIntegrityMigrationCompatibility: true,
	CheckIntegritySQLiteQuickCheck:       true,
	CheckIntegrityForeignKeys:            true,
	CheckDurabilityMediaRemote:           true,
	CheckDurabilitySQLiteRestore:         true,
}

func ApplyAlertObservation(state AlertState, report Report, opts AlertOptions) (AlertState, AlertDecision, error) {
	if !report.Profile.Valid() {
		return AlertState{}, AlertDecision{}, fmt.Errorf("invalid alert report profile")
	}
	if !report.Status.Valid() || report.Status == StatusSkipped || report.Checks == nil || report.CompletedAt.IsZero() {
		return AlertState{}, AlertDecision{}, fmt.Errorf("invalid alert report shape")
	}
	if opts.ConsecutiveObservations <= 0 {
		opts.ConsecutiveObservations = 2
	}
	if opts.RepeatAfter <= 0 {
		opts.RepeatAfter = 24 * time.Hour
	}
	if opts.Now.IsZero() {
		opts.Now = report.CompletedAt.UTC()
		if opts.Now.IsZero() {
			opts.Now = time.Now().UTC()
		}
	}
	if state.Schema == "" && state.Profiles == nil {
		state = emptyAlertState()
	}
	if err := ValidateAlertState(state); err != nil {
		return AlertState{}, AlertDecision{}, err
	}
	next := cloneAlertState(state)
	decision := AlertDecision{Profile: report.Profile, Changes: []AlertChange{}}
	profileState, existed := next.Profiles[report.Profile]
	if !existed {
		profileState = ProfileAlertState{Checks: map[CheckID]CheckAlertState{}}
	}
	beforeNonPass := hasConfirmedRequiredNonPass(profileState)
	changedState := false
	seen := make(map[CheckID]struct{}, len(report.Checks))
	for _, observed := range report.Checks {
		if _, ok := Lookup(observed.ID); !ok {
			return AlertState{}, AlertDecision{}, fmt.Errorf("invalid alert report check id")
		}
		if _, duplicate := seen[observed.ID]; duplicate {
			return AlertState{}, AlertDecision{}, fmt.Errorf("duplicate alert report check id")
		}
		seen[observed.ID] = struct{}{}
		if !observed.Status.Valid() || (observed.Status == StatusSkipped && !observed.SkipReason.Valid()) || (observed.Status != StatusSkipped && observed.SkipReason != "") {
			return AlertState{}, AlertDecision{}, fmt.Errorf("invalid alert report check shape")
		}
		if observed.Status == StatusSkipped && observed.SkipReason == SkipProfileExcluded {
			continue
		}
		current := profileState.Checks[observed.ID]
		updated, notify, reason := applyCheckObservation(current, observed, opts)
		if notify && opts.WebhookConfigured {
			updated.DeliveryPending = true
		}
		profileState.Checks[observed.ID] = updated
		changedState = true
		if notify && opts.WebhookConfigured {
			decision.Changes = append(decision.Changes, AlertChange{CheckID: observed.ID, Status: updated.Confirmed, Summary: fixedSummary(observed.ID), Reason: reason})
		}
	}
	if changedState {
		next.Profiles[report.Profile] = profileState
	}
	afterNonPass := hasConfirmedRequiredNonPass(profileState)
	decision.OverallRecovery = beforeNonPass && !afterNonPass && len(decision.Changes) > 0
	decision.Notify = opts.WebhookConfigured && len(decision.Changes) > 0
	sort.Slice(decision.Changes, func(i, j int) bool {
		return registryIndex(decision.Changes[i].CheckID) < registryIndex(decision.Changes[j].CheckID)
	})
	return next, decision, nil
}

func applyCheckObservation(current CheckAlertState, observed Check, opts AlertOptions) (CheckAlertState, bool, AlertResolutionReason) {
	previousConfirmed := current.Confirmed
	wasRequired := current.Required
	if current.DeliveryPending && observationMatchesPendingDelivery(current, observed) {
		if observed.Status != StatusSkipped {
			current.LastObserved = observed.Status
		}
		return current, true, current.ResolutionReason
	}
	current.DeliveryPending = false
	if observed.Status != StatusSkipped {
		current.LastObserved = observed.Status
	}
	current.ResolutionReason = ""

	if observed.Status == StatusSkipped && observed.SkipReason == SkipFeatureDisabled {
		wasNonPass := current.Required && isNonPass(current.Confirmed)
		current.Required = false
		current.Pending = ""
		current.PendingCount = 0
		if wasNonPass {
			current.Confirmed = StatusPass
			current.LastNotifiedAt = time.Time{}
			current.ResolutionReason = AlertResolvedByConfiguration
			return current, true, AlertResolvedByConfiguration
		}
		return current, false, ""
	}

	if !observed.Required {
		wasRequiredNonPass := current.Required && isNonPass(current.Confirmed)
		current.Required = false
		current.Confirmed = observed.Status
		current.Pending = ""
		current.PendingCount = 0
		if wasRequiredNonPass {
			current.LastNotifiedAt = time.Time{}
			current.ResolutionReason = AlertResolvedByConfiguration
			return current, true, AlertResolvedByConfiguration
		}
		return current, false, ""
	}

	if !wasRequired {
		// Optional display state is not a confirmed required baseline. When the
		// check becomes required, debounce its first non-pass observation just
		// like a new check (except for the exact immediate-failure allowlist).
		current.Confirmed = ""
		current.Pending = ""
		current.PendingCount = 0
		current.LastNotifiedAt = time.Time{}
		previousConfirmed = ""
	}
	current.Required = true
	if observed.Status == StatusPass {
		current.Confirmed = StatusPass
		current.Pending = ""
		current.PendingCount = 0
		if isNonPass(previousConfirmed) {
			current.LastNotifiedAt = time.Time{}
		}
		return current, isNonPass(previousConfirmed), ""
	}
	if !isNonPass(observed.Status) {
		return current, false, ""
	}
	if current.Confirmed == observed.Status {
		current.Pending = ""
		current.PendingCount = 0
		if current.LastNotifiedAt.IsZero() || opts.Now.Sub(current.LastNotifiedAt) >= opts.RepeatAfter {
			return current, true, ""
		}
		return current, false, ""
	}
	if observed.Status == StatusFail && immediateFailureAlertIDs[observed.ID] {
		current.Confirmed = StatusFail
		current.Pending = ""
		current.PendingCount = 0
		if previousConfirmed != StatusFail {
			current.LastNotifiedAt = time.Time{}
		}
		return current, true, ""
	}
	if current.Pending != observed.Status {
		current.Pending = observed.Status
		current.PendingCount = 1
	} else {
		current.PendingCount++
	}
	if current.PendingCount >= opts.ConsecutiveObservations {
		current.Confirmed = observed.Status
		current.Pending = ""
		current.PendingCount = 0
		current.LastNotifiedAt = time.Time{}
		return current, true, ""
	}
	return current, false, ""
}

func MarkAlertDelivered(state AlertState, decision AlertDecision, deliveredAt time.Time) AlertState {
	next := cloneAlertState(state)
	profileState, ok := next.Profiles[decision.Profile]
	if !ok {
		return next
	}
	for _, change := range decision.Changes {
		check, ok := profileState.Checks[change.CheckID]
		if !ok {
			continue
		}
		check.LastNotifiedAt = deliveredAt.UTC()
		check.DeliveryPending = false
		profileState.Checks[change.CheckID] = check
	}
	next.Profiles[decision.Profile] = profileState
	return next
}

func observationMatchesPendingDelivery(current CheckAlertState, observed Check) bool {
	if current.ResolutionReason == AlertResolvedByConfiguration {
		return (observed.Status == StatusSkipped && observed.SkipReason == SkipFeatureDisabled) || !observed.Required
	}
	return observed.Status == current.Confirmed
}

func cloneAlertState(state AlertState) AlertState {
	out := AlertState{Schema: state.Schema, Profiles: make(map[Profile]ProfileAlertState, len(state.Profiles))}
	for profile, profileState := range state.Profiles {
		checks := make(map[CheckID]CheckAlertState, len(profileState.Checks))
		for id, check := range profileState.Checks {
			checks[id] = check
		}
		out.Profiles[profile] = ProfileAlertState{Checks: checks}
	}
	return out
}

func hasConfirmedRequiredNonPass(state ProfileAlertState) bool {
	for _, check := range state.Checks {
		if check.Required && isNonPass(check.Confirmed) {
			return true
		}
	}
	return false
}

func isNonPass(status Status) bool {
	return status == StatusWarn || status == StatusUnknown || status == StatusFail
}

func alertSeverity(status Status) (int, bool) {
	switch status {
	case StatusPass:
		return 0, true
	case StatusWarn:
		return 1, true
	case StatusUnknown:
		return 2, true
	case StatusFail:
		return 3, true
	default:
		return 0, false
	}
}

func registryIndex(id CheckID) int {
	entry, ok := Lookup(id)
	if !ok {
		return int(^uint(0) >> 1)
	}
	return entry.index
}
