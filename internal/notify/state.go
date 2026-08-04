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

const (
	StateSchemaV1 = "dbrain.notifications.state.v1"
	StateSchemaV2 = "dbrain.notifications.state.v2"
)

const (
	maxStateIncidents = 64
	maxStateEnvelopes = 128
	maxProviders      = 16
	maxProviderBytes  = 64
	maxStateBytes     = 1 << 20
)

var errOutboxCapacity = fmt.Errorf("notification outbox exceeds limit")

type outboxCapacityError struct {
	event EventFacts
}

func (e *outboxCapacityError) Error() string { return errOutboxCapacity.Error() }

func (e *outboxCapacityError) Unwrap() error { return errOutboxCapacity }

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
	Schema    string              `json:"schema"`
	Incidents map[string]Incident `json:"incidents"`
	Outbox    []Envelope          `json:"outbox"`
	// LastDelivery is retained only to decode v1 state. Canonical v2 state must
	// leave it empty and use LastDeliveries instead.
	LastDelivery   DeliverySummary            `json:"last_delivery,omitempty"`
	LastDeliveries map[string]DeliverySummary `json:"last_deliveries"`
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

type IncidentSnapshot struct {
	ID          string      `json:"id"`
	FailureType FailureType `json:"failure_type"`
	FirstSeenAt time.Time   `json:"first_seen_at"`
	LastSeenAt  time.Time   `json:"last_seen_at"`
	Occurrences int         `json:"occurrences"`
}

type EventFacts struct {
	Kind             EventKind          `json:"kind"`
	Operation        Operation          `json:"operation"`
	Incidents        []IncidentSnapshot `json:"incidents"`
	SuppressionAfter time.Duration      `json:"suppression_after"`
	CreatedAt        time.Time          `json:"created_at"`
}

type Envelope struct {
	Event      EventFacts                `json:"event"`
	Deliveries map[string]DeliveryRecord `json:"deliveries"`
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
	return State{Schema: StateSchemaV2, Incidents: map[string]Incident{}, Outbox: []Envelope{}, LastDeliveries: map[string]DeliverySummary{}}
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
	pruneExpiredCoolingIncidents(&next, outcome.FinishedAt.UTC(), options.RepeatAfter)
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

func pruneExpiredCoolingIncidents(state *State, observedAt time.Time, repeatAfter time.Duration) {
	for key, incident := range state.Incidents {
		if incident.Phase == IncidentCooling && !observedAt.Before(incident.LastFailureEnqueuedAt.Add(repeatAfter)) {
			delete(state.Incidents, key)
		}
	}
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
	event := eventFactsFromIncidents(EventRecovery, resolved, options.RepeatAfter, createdAt)
	return enqueueEvent(state, decision, event, options.Providers)
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
	switch kind {
	case EventFailure, EventReminder:
	default:
		return fmt.Errorf("invalid incident event kind")
	}
	event := eventFactsFromIncidents(kind, []Incident{incident}, options.RepeatAfter, incident.LastSeenAt)
	return enqueueEvent(state, decision, event, options.Providers)
}

func eventFactsFromIncidents(kind EventKind, incidents []Incident, repeatAfter time.Duration, createdAt time.Time) EventFacts {
	incidents = append([]Incident(nil), incidents...)
	if kind == EventRecovery {
		sort.Slice(incidents, func(left int, right int) bool {
			return failureCatalogIndex(incidents[left].FailureType) < failureCatalogIndex(incidents[right].FailureType)
		})
	}
	event := EventFacts{
		Kind:             kind,
		Operation:        OperationScheduledSyncAll,
		Incidents:        make([]IncidentSnapshot, 0, len(incidents)),
		SuppressionAfter: repeatAfter,
		CreatedAt:        createdAt.UTC(),
	}
	for _, incident := range incidents {
		event.Incidents = append(event.Incidents, IncidentSnapshot{
			ID:          incident.ID,
			FailureType: incident.FailureType,
			FirstSeenAt: incident.FirstSeenAt.UTC(),
			LastSeenAt:  incident.LastSeenAt.UTC(),
			Occurrences: incident.Occurrences,
		})
	}
	return event
}

func enqueueEvent(state *State, decision *Decision, event EventFacts, providers []string) error {
	if len(state.Outbox) >= maxStateEnvelopes {
		return &outboxCapacityError{event: event}
	}
	notification, err := DeriveNotification(event)
	if err != nil {
		return err
	}
	deliveries := make(map[string]DeliveryRecord, len(providers))
	for _, provider := range providers {
		deliveries[provider] = DeliveryRecord{Status: DeliveryPending}
	}
	state.Outbox = append(state.Outbox, Envelope{Event: event, Deliveries: deliveries})
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

func ValidateEventFacts(event EventFacts) error {
	if event.Operation != OperationScheduledSyncAll {
		return fmt.Errorf("invalid notification event operation")
	}
	if event.CreatedAt.IsZero() || event.CreatedAt.Location() != time.UTC {
		return fmt.Errorf("invalid notification event creation time")
	}
	if len(event.Incidents) > maxNotificationItems {
		return fmt.Errorf("notification event incident count exceeds limit")
	}
	switch event.Kind {
	case EventFailure, EventReminder:
		if len(event.Incidents) != 1 || event.SuppressionAfter <= 0 {
			return fmt.Errorf("invalid notification incident event facts")
		}
	case EventRecovery:
		if len(event.Incidents) == 0 || event.SuppressionAfter <= 0 {
			return fmt.Errorf("invalid notification recovery event facts")
		}
	case EventTest:
		if len(event.Incidents) != 0 || event.SuppressionAfter != 0 {
			return fmt.Errorf("invalid notification test event facts")
		}
		return nil
	default:
		return fmt.Errorf("invalid notification event kind")
	}

	seenIDs := make(map[string]struct{}, len(event.Incidents))
	seenTypes := make(map[FailureType]struct{}, len(event.Incidents))
	previousCatalogIndex := -1
	for _, incident := range event.Incidents {
		if _, ok := LookupFailure(incident.FailureType); !ok {
			return fmt.Errorf("unknown notification event failure type")
		}
		if incident.FirstSeenAt.IsZero() || incident.FirstSeenAt.Location() != time.UTC ||
			incident.LastSeenAt.Location() != time.UTC ||
			incident.LastSeenAt.Before(incident.FirstSeenAt) ||
			event.CreatedAt.Before(incident.LastSeenAt) ||
			incident.Occurrences < 1 {
			return fmt.Errorf("invalid notification event incident chronology")
		}
		if incident.ID != deterministicIncidentID(event.Operation, incident.FailureType, incident.FirstSeenAt) {
			return fmt.Errorf("notification event incident id is not deterministic")
		}
		if _, exists := seenIDs[incident.ID]; exists {
			return fmt.Errorf("notification event repeats incident id")
		}
		seenIDs[incident.ID] = struct{}{}
		if _, exists := seenTypes[incident.FailureType]; exists {
			return fmt.Errorf("notification event repeats failure type")
		}
		seenTypes[incident.FailureType] = struct{}{}
		if event.Kind == EventRecovery {
			catalogIndex := failureCatalogIndex(incident.FailureType)
			if catalogIndex <= previousCatalogIndex {
				return fmt.Errorf("notification recovery event is not in catalog order")
			}
			previousCatalogIndex = catalogIndex
		}
	}

	incident := event.Incidents[0]
	switch event.Kind {
	case EventFailure:
		if incident.Occurrences != 1 ||
			!incident.FirstSeenAt.Equal(incident.LastSeenAt) ||
			!incident.LastSeenAt.Equal(event.CreatedAt) {
			return fmt.Errorf("invalid notification failure event facts")
		}
	case EventReminder:
		if incident.Occurrences < 2 ||
			!incident.LastSeenAt.Equal(event.CreatedAt) ||
			!incident.LastSeenAt.After(incident.FirstSeenAt) {
			return fmt.Errorf("invalid notification reminder event facts")
		}
	}
	return nil
}

func ValidateState(state State) error {
	if state.Schema != StateSchemaV2 {
		return fmt.Errorf("invalid notification state schema")
	}
	if state.LastDelivery != (DeliverySummary{}) {
		return fmt.Errorf("legacy notification delivery summary is not empty")
	}
	if state.LastDeliveries == nil || len(state.LastDeliveries) > maxProviders {
		return fmt.Errorf("invalid notification delivery summary map")
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
		if _, err := DeriveNotification(envelope.Event); err != nil {
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
	for provider, summary := range state.LastDeliveries {
		if !validProviderName(provider) || provider != summary.Provider {
			return fmt.Errorf("invalid notification delivery summary provider")
		}
		if err := validateDeliverySummary(summary); err != nil {
			return err
		}
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
		cloned.Outbox[index].Event = envelope.Event
		cloned.Outbox[index].Event.Incidents = append([]IncidentSnapshot(nil), envelope.Event.Incidents...)
		cloned.Outbox[index].Deliveries = make(map[string]DeliveryRecord, len(envelope.Deliveries))
		for provider, record := range envelope.Deliveries {
			cloned.Outbox[index].Deliveries[provider] = record
		}
	}
	return cloned
}
