package notify

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const StateSchemaV1 = "dbrain.notifications.state.v1"

const (
	maxStateIncidents = 64
	maxStateEnvelopes = 128
	maxProviders      = 16
	maxProviderBytes  = 64
	maxStateBytes     = 1 << 20
)

type IncidentPhase string

const (
	IncidentOpen    IncidentPhase = "open"
	IncidentCooling IncidentPhase = "cooling"
)

type DeliveryStatus string

const (
	DeliveryPending        DeliveryStatus = "pending"
	DeliveryAccepted       DeliveryStatus = "accepted"
	DeliveryPermanentError DeliveryStatus = "permanent_error"
	DeliveryAmbiguous      DeliveryStatus = "ambiguous"
	DeliveryRetired        DeliveryStatus = "retired"
)

type State struct {
	Schema       string              `json:"schema"`
	Incidents    map[string]Incident `json:"incidents"`
	Outbox       []Envelope          `json:"outbox"`
	LastDelivery DeliverySummary     `json:"last_delivery,omitempty"`
}

type Incident struct {
	ID                    string        `json:"id"`
	Operation             Operation     `json:"operation"`
	FailureType           FailureType   `json:"failure_type"`
	Phase                 IncidentPhase `json:"phase"`
	FirstSeenAt           time.Time     `json:"first_seen_at"`
	LastSeenAt            time.Time     `json:"last_seen_at"`
	Occurrences           int           `json:"occurrences"`
	LastFailureEnqueuedAt time.Time     `json:"last_failure_enqueued_at"`
	NeedsRecovery         bool          `json:"needs_recovery"`
	RecoveryNotifiedAt    time.Time     `json:"recovery_notified_at,omitempty"`
}

type Envelope struct {
	Notification Notification              `json:"notification"`
	Deliveries   map[string]DeliveryRecord `json:"deliveries"`
}

type DeliveryRecord struct {
	Status        DeliveryStatus `json:"status"`
	Attempts      int            `json:"attempts"`
	LastAttemptAt time.Time      `json:"last_attempt_at,omitempty"`
	ErrorCode     string         `json:"error_code,omitempty"`
	Receipt       Receipt        `json:"receipt,omitempty"`
}

type DeliverySummary struct {
	NotificationID string         `json:"notification_id,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Kind           EventKind      `json:"kind,omitempty"`
	Status         DeliveryStatus `json:"status,omitempty"`
	ErrorCode      string         `json:"error_code,omitempty"`
	At             time.Time      `json:"at,omitempty"`
}

type Options struct {
	RepeatAfter time.Duration
	Providers   []string
}

type Decision struct {
	Notifications []Notification
}

func EmptyState() State {
	return State{Schema: StateSchemaV1, Incidents: map[string]Incident{}, Outbox: []Envelope{}}
}

func Observe(state State, outcome Outcome, options Options) (State, Decision, error) {
	if err := ValidateOutcome(outcome); err != nil {
		return state, Decision{}, err
	}
	if outcome.Status == OutcomeCancelled {
		return state, Decision{}, nil
	}
	if err := validateOptions(options); err != nil {
		return state, Decision{}, err
	}
	if err := ValidateState(state); err != nil {
		return state, Decision{}, err
	}
	if outcome.FinishedAt.IsZero() {
		return state, Decision{}, fmt.Errorf("notification outcome has no completion timestamp")
	}
	next := cloneState(state)
	decision := Decision{}
	var err error
	switch outcome.Status {
	case OutcomeFailure:
		err = observeFailure(&next, &decision, outcome, options)
	case OutcomeSuccess:
		err = observeSuccess(&next, &decision, outcome, options)
	}
	if err != nil {
		return state, Decision{}, err
	}
	if err := ValidateState(next); err != nil {
		return state, Decision{}, err
	}
	return next, decision, nil
}

func observeFailure(state *State, decision *Decision, outcome Outcome, options Options) error {
	key := incidentKey(outcome.Operation, outcome.FailureType)
	observedAt := outcome.FinishedAt.UTC()
	incident, exists := state.Incidents[key]
	if !exists {
		incident = newIncident(outcome.Operation, outcome.FailureType, observedAt)
		state.Incidents[key] = incident
		return enqueueIncidentNotification(state, decision, incident, EventFailure, options)
	}
	if observedAt.Before(incident.LastSeenAt) {
		observedAt = incident.LastSeenAt
	}
	boundary := incident.LastFailureEnqueuedAt.Add(options.RepeatAfter)
	if incident.Phase == IncidentCooling && !observedAt.Before(boundary) {
		incident = newIncident(outcome.Operation, outcome.FailureType, observedAt)
		state.Incidents[key] = incident
		return enqueueIncidentNotification(state, decision, incident, EventFailure, options)
	}
	incident.Phase = IncidentOpen
	incident.LastSeenAt = observedAt
	incident.Occurrences++
	if !observedAt.Before(boundary) {
		incident.LastFailureEnqueuedAt = observedAt
		incident.NeedsRecovery = true
		state.Incidents[key] = incident
		return enqueueIncidentNotification(state, decision, incident, EventReminder, options)
	}
	state.Incidents[key] = incident
	return nil
}

func observeSuccess(state *State, decision *Decision, outcome Outcome, options Options) error {
	createdAt := outcome.FinishedAt.UTC()
	keys := make([]string, 0, len(state.Incidents))
	for key, incident := range state.Incidents {
		if incident.Operation == outcome.Operation && incident.Phase == IncidentOpen {
			keys = append(keys, key)
			if incident.LastSeenAt.After(createdAt) {
				createdAt = incident.LastSeenAt
			}
		}
	}
	sort.Strings(keys)
	resolved := make([]Incident, 0, len(keys))
	for _, key := range keys {
		incident := state.Incidents[key]
		incident.Phase = IncidentCooling
		if incident.NeedsRecovery {
			incident.NeedsRecovery = false
			incident.RecoveryNotifiedAt = createdAt
			resolved = append(resolved, incident)
		}
		state.Incidents[key] = incident
	}
	if len(resolved) == 0 {
		return nil
	}
	notification, err := BuildRecoveryMessage(resolved, createdAt)
	if err != nil {
		return err
	}
	notification.SuppressionAfter = options.RepeatAfter
	return enqueueNotification(state, decision, notification, options.Providers)
}

func newIncident(operation Operation, failureType FailureType, observedAt time.Time) Incident {
	return Incident{
		ID:                    deterministicIncidentID(operation, failureType, observedAt),
		Operation:             operation,
		FailureType:           failureType,
		Phase:                 IncidentOpen,
		FirstSeenAt:           observedAt,
		LastSeenAt:            observedAt,
		Occurrences:           1,
		LastFailureEnqueuedAt: observedAt,
		NeedsRecovery:         true,
	}
}

func enqueueIncidentNotification(state *State, decision *Decision, incident Incident, kind EventKind, options Options) error {
	var notification Notification
	var err error
	switch kind {
	case EventFailure:
		notification, err = BuildFailureMessage(incident, options.RepeatAfter)
	case EventReminder:
		notification, err = BuildReminderMessage(incident, options.RepeatAfter)
	default:
		return fmt.Errorf("invalid incident event kind")
	}
	if err != nil {
		return err
	}
	return enqueueNotification(state, decision, notification, options.Providers)
}

func enqueueNotification(state *State, decision *Decision, notification Notification, providers []string) error {
	if len(state.Outbox) >= maxStateEnvelopes {
		return fmt.Errorf("notification outbox exceeds limit")
	}
	deliveries := make(map[string]DeliveryRecord, len(providers))
	for _, provider := range providers {
		deliveries[provider] = DeliveryRecord{Status: DeliveryPending}
	}
	state.Outbox = append(state.Outbox, Envelope{Notification: notification, Deliveries: deliveries})
	decision.Notifications = append(decision.Notifications, notification)
	return nil
}

func deterministicIncidentID(operation Operation, failureType FailureType, firstSeenAt time.Time) string {
	digest := sha256.Sum256([]byte(string(operation) + "\x00" + string(failureType) + "\x00" + firstSeenAt.UTC().Format(time.RFC3339Nano)))
	return fmt.Sprintf("inc_%x", digest[:12])
}

func incidentKey(operation Operation, failureType FailureType) string {
	return string(operation) + "\x00" + string(failureType)
}

func validateOptions(options Options) error {
	if options.RepeatAfter <= 0 {
		return fmt.Errorf("notification repeat interval must be positive")
	}
	if len(options.Providers) == 0 || len(options.Providers) > maxProviders {
		return fmt.Errorf("notification provider count is invalid")
	}
	seen := make(map[string]struct{}, len(options.Providers))
	for _, provider := range options.Providers {
		if !validProviderName(provider) {
			return fmt.Errorf("invalid notification provider name")
		}
		if _, ok := seen[provider]; ok {
			return fmt.Errorf("duplicate notification provider")
		}
		seen[provider] = struct{}{}
	}
	return nil
}

func ValidateState(state State) error {
	if state.Schema != StateSchemaV1 {
		return fmt.Errorf("invalid notification state schema")
	}
	if state.Incidents == nil || len(state.Incidents) > maxStateIncidents {
		return fmt.Errorf("invalid notification incident map")
	}
	if state.Outbox == nil || len(state.Outbox) > maxStateEnvelopes {
		return fmt.Errorf("notification outbox exceeds limit")
	}
	for key, incident := range state.Incidents {
		if key != incidentKey(incident.Operation, incident.FailureType) {
			return fmt.Errorf("invalid notification incident key")
		}
		if len(incident.ID) == 0 || len(incident.ID) > maxIncidentIDBytes || incident.Operation != OperationScheduledSyncAll {
			return fmt.Errorf("invalid notification incident identity")
		}
		if incident.ID != deterministicIncidentID(incident.Operation, incident.FailureType, incident.FirstSeenAt) {
			return fmt.Errorf("notification incident id is not deterministic")
		}
		if _, ok := LookupFailure(incident.FailureType); !ok {
			return fmt.Errorf("unknown notification incident failure type")
		}
		if incident.Phase != IncidentOpen && incident.Phase != IncidentCooling {
			return fmt.Errorf("invalid notification incident phase")
		}
		if incident.FirstSeenAt.IsZero() ||
			incident.LastSeenAt.Before(incident.FirstSeenAt) ||
			incident.LastFailureEnqueuedAt.Before(incident.FirstSeenAt) ||
			incident.LastFailureEnqueuedAt.After(incident.LastSeenAt) ||
			incident.Occurrences < 1 {
			return fmt.Errorf("invalid notification incident chronology")
		}
		if incident.Phase == IncidentCooling && (incident.NeedsRecovery || incident.RecoveryNotifiedAt.IsZero()) {
			return fmt.Errorf("invalid cooling notification incident")
		}
		if incident.Phase == IncidentOpen && !incident.NeedsRecovery && incident.RecoveryNotifiedAt.IsZero() {
			return fmt.Errorf("invalid silently reopened notification incident")
		}
	}
	for _, envelope := range state.Outbox {
		if err := ValidateNotification(envelope.Notification); err != nil {
			return err
		}
		if len(envelope.Deliveries) == 0 || len(envelope.Deliveries) > maxProviders {
			return fmt.Errorf("invalid notification delivery map")
		}
		for provider, record := range envelope.Deliveries {
			if !validProviderName(provider) {
				return fmt.Errorf("invalid notification delivery provider")
			}
			switch record.Status {
			case DeliveryPending, DeliveryAccepted, DeliveryPermanentError, DeliveryAmbiguous, DeliveryRetired:
			default:
				return fmt.Errorf("invalid notification delivery status")
			}
			if err := validateDeliveryRecord(provider, record); err != nil {
				return err
			}
		}
	}
	if err := validateDeliverySummary(state.LastDelivery); err != nil {
		return err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode notification state: %w", err)
	}
	if len(encoded) > maxStateBytes {
		return fmt.Errorf("notification state exceeds byte limit")
	}
	return nil
}

func validateDeliverySummary(summary DeliverySummary) error {
	empty := summary.NotificationID == "" && summary.Provider == "" && summary.Kind == "" && summary.Status == "" && summary.ErrorCode == "" && summary.At.IsZero()
	if empty {
		return nil
	}
	if !validDigestID(summary.NotificationID, "ntf_") || !validProviderName(summary.Provider) || summary.At.IsZero() {
		return fmt.Errorf("invalid notification delivery summary identity")
	}
	switch summary.Kind {
	case EventFailure, EventReminder, EventRecovery, EventTest:
	default:
		return fmt.Errorf("invalid notification delivery summary kind")
	}
	if !validSafeCode(summary.ErrorCode, 64) {
		return fmt.Errorf("invalid notification delivery summary error code")
	}
	switch summary.Status {
	case DeliveryAccepted:
		if summary.ErrorCode != "" {
			return fmt.Errorf("accepted notification delivery summary has error")
		}
	case DeliveryPermanentError:
		if summary.ErrorCode == "" {
			return fmt.Errorf("failed notification delivery summary lacks error")
		}
	case DeliveryRetired:
	default:
		return fmt.Errorf("invalid notification delivery summary status")
	}
	return nil
}

func validateDeliveryRecord(provider string, record DeliveryRecord) error {
	if record.Attempts < 0 {
		return fmt.Errorf("invalid notification delivery attempts")
	}
	if !validSafeCode(record.ErrorCode, 64) {
		return fmt.Errorf("invalid notification delivery error code")
	}
	receiptPresent := record.Receipt.Provider != "" || record.Receipt.ExternalID != "" || !record.Receipt.AcceptedAt.IsZero()
	if receiptPresent {
		if record.Receipt.Provider != provider ||
			len(record.Receipt.ExternalID) == 0 ||
			len(record.Receipt.ExternalID) > 256 ||
			!utf8.ValidString(record.Receipt.ExternalID) ||
			record.Receipt.AcceptedAt.IsZero() {
			return fmt.Errorf("invalid notification delivery receipt")
		}
	}
	switch record.Status {
	case DeliveryAccepted:
		if record.Attempts < 1 || record.LastAttemptAt.IsZero() || !receiptPresent || record.ErrorCode != "" {
			return fmt.Errorf("invalid accepted notification delivery")
		}
	case DeliveryPermanentError, DeliveryAmbiguous:
		if record.Attempts < 1 || record.LastAttemptAt.IsZero() || receiptPresent || record.ErrorCode == "" {
			return fmt.Errorf("invalid failed notification delivery")
		}
	case DeliveryPending:
		if receiptPresent || (record.Attempts > 0 && record.LastAttemptAt.IsZero()) {
			return fmt.Errorf("invalid pending notification delivery")
		}
	case DeliveryRetired:
		if receiptPresent {
			return fmt.Errorf("invalid retired notification delivery")
		}
	}
	return nil
}

func validSafeCode(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("._-", character):
		default:
			return false
		}
	}
	return true
}

func validDigestID(value string, prefix string) bool {
	if len(value) != len(prefix)+24 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validProviderName(value string) bool {
	if len(value) == 0 || len(value) > maxProviderBytes {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '_', character == '-':
		default:
			return false
		}
	}
	return true
}

func cloneState(state State) State {
	cloned := state
	cloned.Incidents = make(map[string]Incident, len(state.Incidents))
	for key, incident := range state.Incidents {
		cloned.Incidents[key] = incident
	}
	cloned.Outbox = make([]Envelope, len(state.Outbox))
	for index, envelope := range state.Outbox {
		cloned.Outbox[index].Notification = envelope.Notification
		cloned.Outbox[index].Notification.IncidentIDs = append([]string(nil), envelope.Notification.IncidentIDs...)
		cloned.Outbox[index].Notification.FailureTypes = append([]FailureType(nil), envelope.Notification.FailureTypes...)
		cloned.Outbox[index].Deliveries = make(map[string]DeliveryRecord, len(envelope.Deliveries))
		for provider, record := range envelope.Deliveries {
			cloned.Outbox[index].Deliveries[provider] = record
		}
	}
	return cloned
}
