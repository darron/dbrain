package notify

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

const defaultDeliveryTimeout = 10 * time.Second

type StateStore interface {
	Load() (State, error)
	Replace(State) error
}

type DeliveryErrorKind string

const (
	DeliveryErrorTemporary DeliveryErrorKind = "temporary"
	DeliveryErrorPermanent DeliveryErrorKind = "permanent"
	DeliveryErrorAmbiguous DeliveryErrorKind = "ambiguous"
)

type DeliveryError struct {
	Kind DeliveryErrorKind
	Code string
	err  error
}

func NewDeliveryError(kind DeliveryErrorKind, code string, cause error) error {
	return &DeliveryError{Kind: kind, Code: code, err: cause}
}

func (e *DeliveryError) Error() string {
	if e != nil && e.Code != "" && validSafeCode(e.Code, 64) {
		return e.Code
	}
	return "delivery_failed"
}

func (e *DeliveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type ManagerOption func(*Manager)

func WithClock(now func() time.Time) ManagerOption {
	return func(manager *Manager) {
		if now != nil {
			manager.now = now
		}
	}
}

func WithMetricsEmitter(emit func(metrics.Event) error) ManagerOption {
	return func(manager *Manager) { manager.emit = emit }
}

func WithManagerLogger(log func(string)) ManagerOption {
	return func(manager *Manager) { manager.log = log }
}

func WithDeliveryTimeout(timeout time.Duration) ManagerOption {
	return func(manager *Manager) {
		if timeout > 0 {
			manager.deliveryTimeout = timeout
		}
	}
}

type Manager struct {
	observeMu sync.Mutex
	stateMu   sync.Mutex

	options         Options
	store           StateStore
	providers       []Provider
	providersByName map[string]Provider
	deliveryTimeout time.Duration
	now             func() time.Time
	emit            func(metrics.Event) error
	log             func(string)
	initErr         error
}

func NewManager(options Options, store StateStore, providers []Provider, managerOptions ...ManagerOption) *Manager {
	manager := &Manager{
		options:         options,
		store:           store,
		providers:       append([]Provider(nil), providers...),
		providersByName: make(map[string]Provider, len(providers)),
		deliveryTimeout: defaultDeliveryTimeout,
		now:             time.Now,
	}
	manager.options.Providers = make([]string, 0, len(providers))
	for _, provider := range providers {
		if provider == nil || !validProviderName(provider.Name()) {
			manager.initErr = fmt.Errorf("invalid notification provider")
			continue
		}
		name := provider.Name()
		if _, exists := manager.providersByName[name]; exists {
			manager.initErr = fmt.Errorf("duplicate notification provider")
			continue
		}
		manager.providersByName[name] = provider
		manager.options.Providers = append(manager.options.Providers, name)
	}
	if manager.store == nil {
		manager.initErr = fmt.Errorf("notification state store is unavailable")
	}
	if err := validateOptions(manager.options); err != nil {
		manager.initErr = err
	}
	for _, option := range managerOptions {
		if option != nil {
			option(manager)
		}
	}
	return manager
}

func (m *Manager) Observe(ctx context.Context, outcome Outcome) error {
	if m == nil {
		return managerCodeError("notification_manager_unavailable")
	}
	m.observeMu.Lock()
	defer m.observeMu.Unlock()
	if m.initErr != nil {
		return managerCodeError("notification_manager_invalid")
	}
	if err := ValidateOutcome(outcome); err != nil {
		return managerCodeError("notification_outcome_invalid")
	}
	if outcome.Status == OutcomeCancelled {
		return nil
	}

	m.stateMu.Lock()
	state, err := m.store.Load()
	if err != nil {
		m.stateMu.Unlock()
		return managerCodeError("notification_state_load_failed")
	}
	if err := retireRemovedProviders(&state, m.providersByName, m.now().UTC()); err != nil {
		m.stateMu.Unlock()
		return managerCodeError("notification_state_transition_failed")
	}
	next, decision, err := Observe(state, outcome, m.options)
	if err != nil {
		m.stateMu.Unlock()
		return managerCodeError("notification_state_transition_failed")
	}
	pruneTerminalEnvelopes(&next)
	if err := m.store.Replace(next); err != nil {
		m.stateMu.Unlock()
		return managerCodeError("notification_state_persist_failed")
	}
	m.stateMu.Unlock()

	ambiguousIncidentIDs := make(map[string]struct{})
	for _, notification := range decision.Notifications {
		if notification.Kind == EventReminder {
			for _, incidentID := range notification.IncidentIDs {
				ambiguousIncidentIDs[incidentID] = struct{}{}
			}
		}
	}
	var failed bool
	for _, provider := range m.providers {
		if provider == nil {
			continue
		}
		if m.deliverProvider(ctx, provider, ambiguousIncidentIDs) {
			failed = true
		}
	}
	if failed {
		return managerCodeError("notification_delivery_failed")
	}
	return nil
}

func (m *Manager) deliverProvider(ctx context.Context, provider Provider, ambiguousIncidentIDs map[string]struct{}) bool {
	failed := false
	for {
		m.stateMu.Lock()
		state, err := m.store.Load()
		if err != nil {
			m.stateMu.Unlock()
			m.reportFailure()
			return true
		}
		index, event, record, notification, ok := nextDelivery(state, provider.Name(), ambiguousIncidentIDs)
		m.stateMu.Unlock()
		if !ok {
			return failed
		}

		started := time.Now()
		deliveryCtx, cancel := context.WithTimeout(ctx, m.deliveryTimeout)
		receipt, deliveryErr := provider.Deliver(deliveryCtx, notification)
		cancel()
		status, code := classifyDeliveryResult(provider.Name(), receipt, deliveryErr, m.now().UTC())
		m.emitDeliveryMetric(provider.Name(), event, status, time.Since(started))

		m.stateMu.Lock()
		current, loadErr := m.store.Load()
		if loadErr != nil {
			m.stateMu.Unlock()
			m.reportFailure()
			return true
		}
		matched := updateMatchingDelivery(&current, index, event, provider.Name(), record, receipt, status, code, m.now().UTC())
		if !matched {
			m.stateMu.Unlock()
			m.reportFailure()
			return true
		}
		pruneTerminalEnvelopes(&current)
		persistErr := m.store.Replace(current)
		m.stateMu.Unlock()
		if persistErr != nil {
			m.reportFailure()
			return true
		}

		if status != DeliveryAccepted {
			failed = true
			m.reportFailure()
		}
		if status == DeliveryPending || status == DeliveryAmbiguous {
			return failed
		}
	}
}

func nextDelivery(state State, provider string, ambiguousIncidentIDs map[string]struct{}) (int, EventFacts, DeliveryRecord, Notification, bool) {
	for index, envelope := range state.Outbox {
		record, exists := envelope.Deliveries[provider]
		deliverable := record.Status == DeliveryPending ||
			(record.Status == DeliveryAmbiguous && eventContainsIncident(envelope.Event, ambiguousIncidentIDs))
		if !exists || !deliverable {
			continue
		}
		notification, err := DeriveNotification(envelope.Event)
		if err != nil {
			return 0, EventFacts{}, DeliveryRecord{}, Notification{}, false
		}
		event := envelope.Event
		event.Incidents = append([]IncidentSnapshot(nil), envelope.Event.Incidents...)
		return index, event, record, notification, true
	}
	return 0, EventFacts{}, DeliveryRecord{}, Notification{}, false
}

func eventContainsIncident(event EventFacts, incidentIDs map[string]struct{}) bool {
	for _, incident := range event.Incidents {
		if _, ok := incidentIDs[incident.ID]; ok {
			return true
		}
	}
	return false
}

func updateMatchingDelivery(state *State, hintedIndex int, event EventFacts, provider string, expected DeliveryRecord, receipt Receipt, status DeliveryStatus, code string, attemptedAt time.Time) bool {
	indices := make([]int, 0, len(state.Outbox))
	if hintedIndex >= 0 && hintedIndex < len(state.Outbox) {
		indices = append(indices, hintedIndex)
	}
	for index := range state.Outbox {
		if index != hintedIndex {
			indices = append(indices, index)
		}
	}
	for _, index := range indices {
		envelope := &state.Outbox[index]
		if !reflect.DeepEqual(envelope.Event, event) {
			continue
		}
		current, exists := envelope.Deliveries[provider]
		if !exists || !reflect.DeepEqual(current, expected) {
			return false
		}
		current.Attempts++
		current.LastAttemptAt = attemptedAt.UTC()
		current.ErrorCode = code
		current.Receipt = Receipt{}
		current.Status = status
		if status == DeliveryAccepted {
			current.ErrorCode = ""
			current.Receipt = receipt
		}
		envelope.Deliveries[provider] = current
		notification, err := DeriveNotification(event)
		if err != nil {
			return false
		}
		if status == DeliveryAccepted || status == DeliveryPermanentError {
			at := current.LastAttemptAt
			if status == DeliveryAccepted {
				at = receipt.AcceptedAt
			}
			state.LastDelivery = DeliverySummary{
				NotificationID: notification.ID, Provider: provider, Kind: event.Kind,
				Status: status, ErrorCode: current.ErrorCode, At: at.UTC(),
			}
		}
		return true
	}
	return false
}

func classifyDeliveryResult(provider string, receipt Receipt, err error, attemptedAt time.Time) (DeliveryStatus, string) {
	if err == nil {
		record := DeliveryRecord{Status: DeliveryAccepted, Attempts: 1, LastAttemptAt: attemptedAt, Receipt: receipt}
		if validateDeliveryRecord(provider, record) == nil {
			return DeliveryAccepted, ""
		}
		return DeliveryAmbiguous, "invalid_receipt"
	}
	var deliveryErr *DeliveryError
	if errors.As(err, &deliveryErr) && validSafeCode(deliveryErr.Code, 64) && deliveryErr.Code != "" {
		switch deliveryErr.Kind {
		case DeliveryErrorPermanent:
			return DeliveryPermanentError, deliveryErr.Code
		case DeliveryErrorAmbiguous:
			return DeliveryAmbiguous, deliveryErr.Code
		case DeliveryErrorTemporary:
			return DeliveryPending, deliveryErr.Code
		}
	}
	if errors.Is(err, context.Canceled) {
		return DeliveryPending, "delivery_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return DeliveryAmbiguous, "delivery_timeout"
	}
	return DeliveryPending, "delivery_failed"
}

func retireRemovedProviders(state *State, configured map[string]Provider, at time.Time) error {
	for envelopeIndex := range state.Outbox {
		envelope := &state.Outbox[envelopeIndex]
		notification, err := DeriveNotification(envelope.Event)
		if err != nil {
			return err
		}
		providers := make([]string, 0, len(envelope.Deliveries))
		for provider := range envelope.Deliveries {
			providers = append(providers, provider)
		}
		sort.Strings(providers)
		for _, provider := range providers {
			if _, exists := configured[provider]; exists {
				continue
			}
			record := envelope.Deliveries[provider]
			if record.Status == DeliveryAccepted || record.Status == DeliveryPermanentError || record.Status == DeliveryRetired {
				continue
			}
			record.Status = DeliveryRetired
			record.ErrorCode = ""
			record.Receipt = Receipt{}
			envelope.Deliveries[provider] = record
			state.LastDelivery = DeliverySummary{
				NotificationID: notification.ID, Provider: provider, Kind: envelope.Event.Kind,
				Status: DeliveryRetired, At: at.UTC(),
			}
		}
	}
	return nil
}

func pruneTerminalEnvelopes(state *State) {
	kept := state.Outbox[:0]
	for _, envelope := range state.Outbox {
		terminal := true
		for _, record := range envelope.Deliveries {
			switch record.Status {
			case DeliveryAccepted, DeliveryPermanentError, DeliveryRetired:
			default:
				terminal = false
			}
		}
		if !terminal {
			kept = append(kept, envelope)
		}
	}
	state.Outbox = kept
}

func (m *Manager) Test(ctx context.Context, providerName string) (Receipt, error) {
	if m == nil || m.initErr != nil {
		return Receipt{}, managerCodeError("notification_manager_invalid")
	}
	provider, ok := m.providersByName[providerName]
	if !ok {
		return Receipt{}, managerCodeError("notification_provider_unknown")
	}
	event := EventFacts{Kind: EventTest, Operation: OperationScheduledSyncAll, CreatedAt: m.now().UTC()}
	notification, err := DeriveNotification(event)
	if err != nil {
		return Receipt{}, managerCodeError("notification_test_invalid")
	}
	started := time.Now()
	deliveryCtx, cancel := context.WithTimeout(ctx, m.deliveryTimeout)
	receipt, deliveryErr := provider.Deliver(deliveryCtx, notification)
	cancel()
	status, _ := classifyDeliveryResult(providerName, receipt, deliveryErr, m.now().UTC())
	m.emitDeliveryMetric(providerName, event, status, time.Since(started))
	if status != DeliveryAccepted {
		m.reportFailure()
		return Receipt{}, managerCodeError("notification_delivery_failed")
	}
	return receipt, nil
}

func (m *Manager) Status() (State, error) {
	if m == nil || m.initErr != nil {
		return State{}, managerCodeError("notification_manager_invalid")
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		return State{}, managerCodeError("notification_state_load_failed")
	}
	return cloneState(state), nil
}

func (m *Manager) emitDeliveryMetric(provider string, event EventFacts, status DeliveryStatus, duration time.Duration) {
	if m.emit == nil {
		return
	}
	failureType := "none"
	if len(event.Incidents) == 1 {
		failureType = string(event.Incidents[0].FailureType)
	} else if len(event.Incidents) > 1 {
		failureType = "multiple"
	}
	metricStatus := string(status)
	if status == DeliveryPending {
		metricStatus = "temporary_error"
	}
	_ = m.emit(metrics.NotificationDeliveryCompletedEvent(provider, string(event.Kind), failureType, metricStatus, duration))
}

func (m *Manager) reportFailure() {
	if m.log != nil {
		m.log("notification_delivery_failed")
	}
}

type managerError string

func managerCodeError(code string) error { return managerError(code) }

func (e managerError) Error() string { return string(e) }
