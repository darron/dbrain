package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/store"
)

const defaultSchedulerAuditStandardInterval = 6 * time.Hour

type schedulerAuditAlertConfig struct {
	WebhookURL              string
	BearerTokenRef          string
	AllowPrivateOrigin      bool
	ConsecutiveObservations int
	RepeatAfter             time.Duration
	AdminOrigin             string
}

type schedulerAuditConfig struct {
	Enabled          bool
	PostSyncFast     bool
	StandardInterval time.Duration
	Since            time.Duration
	Alert            schedulerAuditAlertConfig
}

func schedulerAuditConfigFromRuntime(root string) (schedulerAuditConfig, error) {
	cfg := schedulerAuditConfig{
		Enabled:          runtimeenv.FirstBoolDefault(root, false, "DBRAIN_AUDIT_ENABLED"),
		PostSyncFast:     runtimeenv.FirstBoolDefault(root, true, "DBRAIN_AUDIT_POST_SYNC_FAST"),
		StandardInterval: defaultSchedulerAuditStandardInterval,
		Since:            7 * 24 * time.Hour,
		Alert:            schedulerAuditAlertConfig{ConsecutiveObservations: 2, RepeatAfter: 24 * time.Hour},
	}
	parseDuration := func(key string, destination *time.Duration) error {
		raw := strings.TrimSpace(runtimeenv.FirstNonEmpty(root, key))
		if raw == "" {
			return nil
		}
		parsed, err := audit.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("parse %s: %q", key, raw)
		}
		*destination = parsed
		return nil
	}
	if err := parseDuration("DBRAIN_AUDIT_STANDARD_INTERVAL", &cfg.StandardInterval); err != nil {
		return schedulerAuditConfig{}, err
	}
	if err := parseDuration("DBRAIN_AUDIT_SINCE", &cfg.Since); err != nil {
		return schedulerAuditConfig{}, err
	}
	if err := parseDuration("DBRAIN_AUDIT_ALERT_REPEAT_AFTER", &cfg.Alert.RepeatAfter); err != nil {
		return schedulerAuditConfig{}, err
	}
	if raw := strings.TrimSpace(runtimeenv.FirstNonEmpty(root, "DBRAIN_AUDIT_ALERT_CONSECUTIVE_OBSERVATIONS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return schedulerAuditConfig{}, fmt.Errorf("parse DBRAIN_AUDIT_ALERT_CONSECUTIVE_OBSERVATIONS: %q", raw)
		}
		cfg.Alert.ConsecutiveObservations = value
	}
	cfg.Alert.WebhookURL = strings.TrimSpace(runtimeenv.FirstNonEmpty(root, "DBRAIN_AUDIT_ALERT_WEBHOOK_URL"))
	cfg.Alert.BearerTokenRef = strings.TrimSpace(runtimeenv.FirstNonEmpty(root, "DBRAIN_AUDIT_ALERT_BEARER_TOKEN_REF"))
	cfg.Alert.AllowPrivateOrigin = runtimeenv.FirstBoolDefault(root, false, "DBRAIN_AUDIT_ALERT_ALLOW_PRIVATE_ORIGIN")
	cfg.Alert.AdminOrigin = strings.TrimSpace(runtimeenv.FirstNonEmpty(root, "DBRAIN_AUTH_BASE_URL"))
	return cfg, nil
}

type scheduledAuditStore interface {
	Save(audit.Report) error
	LoadAlertState() (audit.AlertState, error)
	SaveAlertState(audit.AlertState) error
}

type scheduledAuditDeliverer interface {
	Configured() bool
	Deliver(context.Context, audit.Report, audit.AlertDecision) error
}

type auditScheduler struct {
	opts    schedulerAuditConfig
	runner  func(context.Context, audit.Profile, time.Duration) (audit.Report, error)
	store   scheduledAuditStore
	webhook scheduledAuditDeliverer
	logOut  io.Writer

	now           func() time.Time
	wait          func(context.Context, time.Duration) bool
	emitCompleted func(metrics.Event) error

	mu           sync.Mutex
	status       schedulerstate.AuditStatus
	cancel       context.CancelFunc
	lifecycleCtx context.Context
	done         chan struct{}
	active       int
	stopping     bool
	activeDone   chan struct{}
}

func newAuditScheduler(opts schedulerAuditConfig, runner func(context.Context, audit.Profile, time.Duration) (audit.Report, error), store scheduledAuditStore, webhook scheduledAuditDeliverer, logOut io.Writer) *auditScheduler {
	if logOut == nil {
		logOut = io.Discard
	}
	s := &auditScheduler{opts: opts, runner: runner, store: store, webhook: webhook, logOut: newTimestampedLineWriter(logOut, time.Now)}
	s.now = func() time.Time { return time.Now().UTC() }
	s.wait = func(ctx context.Context, duration time.Duration) bool {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	s.emitCompleted = func(metrics.Event) error { return nil }
	s.status = schedulerstate.AuditStatus{Enabled: opts.Enabled, PostSyncFast: opts.PostSyncFast, StandardInterval: opts.StandardInterval.String()}
	return s
}

func (s *auditScheduler) Start(ctx context.Context) {
	if s == nil || !s.opts.Enabled {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel, s.lifecycleCtx, s.done, s.stopping = cancel, child, done, false
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			if s.done == done {
				s.cancel = nil
				s.done = nil
			}
			s.mu.Unlock()
			close(done)
		}()
		s.loop(child)
	}()
}

func (s *auditScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopping = true
	cancel, done := s.cancel, s.done
	var activeDone chan struct{}
	if s.active == 0 {
		activeDone = make(chan struct{})
		close(activeDone)
	} else {
		activeDone = s.activeDone
		if activeDone == nil {
			activeDone = make(chan struct{})
			s.activeDone = activeDone
		}
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	<-activeDone
}

func (s *auditScheduler) Status() schedulerstate.AuditStatus {
	if s == nil {
		return schedulerstate.AuditStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *auditScheduler) loop(ctx context.Context) {
	for {
		s.mu.Lock()
		s.status.NextStandardRunAt = s.now().Add(s.opts.StandardInterval)
		s.mu.Unlock()
		if !s.wait(ctx, s.opts.StandardInterval) {
			return
		}
		s.run(ctx, audit.ProfileStandard, "interval")
	}
}

func (s *auditScheduler) AfterSync(ctx context.Context) {
	if s == nil || !s.opts.Enabled || !s.opts.PostSyncFast {
		return
	}
	s.mu.Lock()
	if s.lifecycleCtx != nil {
		ctx = s.lifecycleCtx
	}
	s.mu.Unlock()
	s.run(ctx, audit.ProfileFast, "post_sync")
}

func (s *auditScheduler) run(ctx context.Context, profile audit.Profile, reason string) {
	if s == nil || !s.opts.Enabled || profile == audit.ProfileDeep || (profile != audit.ProfileFast && profile != audit.ProfileStandard) {
		return
	}
	s.mu.Lock()
	if s.stopping || s.status.Running {
		s.mu.Unlock()
		return
	}
	s.active++
	started := s.now()
	s.status.Running = true
	s.status.CurrentProfile = string(profile)
	s.status.LastStatus = "running"
	s.status.LastError = ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.status.Running = false
		s.status.CurrentProfile = ""
		s.status.LastFinishedAt = s.now()
		s.active--
		if s.stopping && s.active == 0 && s.activeDone != nil {
			close(s.activeDone)
			s.activeDone = nil
		}
		s.mu.Unlock()
	}()
	if s.runner == nil || s.store == nil {
		s.finish("failed", "unavailable")
		return
	}
	report, err := s.runner(ctx, profile, s.opts.Since)
	if err != nil {
		s.finish("failed", "run_failed")
		return
	}
	if err := s.store.Save(report); err != nil {
		s.finish("failed", "report_sink_failed")
		return
	}
	counts := report.Summary.All
	metricFailed := s.emitCompleted(metrics.AuditRunCompletedEvent(string(profile), string(report.Status), s.now().Sub(started), metrics.AuditStatusCounts{Pass: counts.Pass, Warn: counts.Warn, Fail: counts.Fail, Unknown: counts.Unknown, Skipped: counts.Skipped})) != nil
	state, err := s.store.LoadAlertState()
	if err != nil {
		s.finish("failed", "alert_state_read_failed")
		return
	}
	configured := s.webhook != nil && s.webhook.Configured()
	next, decision, err := audit.ApplyAlertObservation(state, report, audit.AlertOptions{ConsecutiveObservations: s.opts.Alert.ConsecutiveObservations, RepeatAfter: s.opts.Alert.RepeatAfter, WebhookConfigured: configured, Now: s.now()})
	if err != nil {
		s.finish("failed", "alert_transition_failed")
		return
	}
	if err := s.store.SaveAlertState(next); err != nil {
		s.finish("failed", "alert_state_write_failed")
		return
	}
	if decision.Notify {
		if err := s.webhook.Deliver(ctx, report, decision); err != nil {
			s.finish("failed", "webhook_failed")
			return
		}
		ack := audit.MarkAlertDelivered(next, decision, s.now())
		if err := s.store.SaveAlertState(ack); err != nil {
			s.finish("failed", "alert_state_ack_failed")
			return
		}
	}
	if metricFailed {
		s.finish("failed", "metrics_sink_failed")
		return
	}
	s.finish("ok", "")
	_ = reason
}

func (s *auditScheduler) finish(status, code string) {
	s.mu.Lock()
	s.status.LastStatus = status
	s.status.LastError = code
	s.mu.Unlock()
	if code != "" {
		_, _ = fmt.Fprintf(s.logOut, "scheduled audit failed: code=%s\n", code)
	}
}

func newScheduledAuditRunner(ctx context.Context, cfg config.Config, features audit.Features) (func(context.Context, audit.Profile, time.Duration) (audit.Report, error), error) {
	base, err := buildAuditDependencies(ctx, cfg, nil, audit.Request{Profile: audit.ProfileStandard, Since: 7 * 24 * time.Hour}, features)
	if err != nil {
		return nil, err
	}
	return func(runCtx context.Context, profile audit.Profile, since time.Duration) (audit.Report, error) {
		if profile != audit.ProfileFast && profile != audit.ProfileStandard {
			return audit.Report{}, fmt.Errorf("scheduled audit profile is not allowed")
		}
		st, err := store.OpenReadOnlyContext(runCtx, cfg.DBPath)
		if err != nil {
			return audit.Report{}, err
		}
		defer func() { _ = st.Close() }()
		snapshot, err := st.BeginAuditReadSnapshot(runCtx)
		if err != nil {
			return audit.Report{}, err
		}
		defer func() { _ = snapshot.Close() }()
		deps := base
		deps.Store = auditSnapshotAdapter{snapshot: snapshot}
		deps.Semantic = newAuditSemanticInspector(cfg.RootDir, snapshot)
		deps.Features.DatabaseOpenedQueryOnly = true
		return audit.Run(runCtx, audit.Request{Profile: profile, Since: since}, deps)
	}, nil
}

func scheduledAuditMetricEmitter(cfg metrics.Config) func(metrics.Event) error {
	return func(event metrics.Event) error {
		sink, err := metrics.Open(cfg)
		if err != nil {
			return err
		}
		run := metrics.RunContext{RunID: metrics.NewRunID("audit", time.Now().UTC()), Command: "audit", Invocation: "scheduler", Sink: sink}
		if err := run.Emit(event); err != nil {
			_ = sink.Close()
			return err
		}
		return sink.Close()
	}
}
