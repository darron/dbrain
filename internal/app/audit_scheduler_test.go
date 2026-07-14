package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runtimeenv"
	"gopkg.in/yaml.v3"
)

func TestSchedulerAuditConfigDefaultsAndBoundedPrecedence(t *testing.T) {
	root := t.TempDir()
	got, err := schedulerAuditConfigFromRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || !got.PostSyncFast || got.StandardInterval != 6*time.Hour || got.Since != 7*24*time.Hour || got.Alert.ConsecutiveObservations != 2 || got.Alert.RepeatAfter != 24*time.Hour {
		t.Fatalf("defaults = %+v", got)
	}
	cleanup := runtimeenv.RegisterConfigSnapshot(root, map[string]any{"audit": map[string]any{
		"enabled": true, "post_sync_fast": false, "standard_interval": "8h", "since": "5d",
		"alert": map[string]any{"webhook_url": "https://yaml.example/hook", "consecutive_observations": 3, "repeat_after": "12h"},
	}}, map[string]string{"DBRAIN_AUDIT_STANDARD_INTERVAL": "9h"})
	defer cleanup()
	t.Setenv("DBRAIN_AUDIT_STANDARD_INTERVAL", "10h")
	got, err = schedulerAuditConfigFromRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.PostSyncFast || got.StandardInterval != 10*time.Hour || got.Since != 5*24*time.Hour || got.Alert.WebhookURL != "https://yaml.example/hook" || got.Alert.ConsecutiveObservations != 3 || got.Alert.RepeatAfter != 12*time.Hour {
		t.Fatalf("resolved = %+v", got)
	}
}

func TestSchedulerAuditConfigRejectsInvalidValues(t *testing.T) {
	for key, value := range map[string]string{
		"DBRAIN_AUDIT_STANDARD_INTERVAL": "0s", "DBRAIN_AUDIT_SINCE": "bad", "DBRAIN_AUDIT_ALERT_CONSECUTIVE_OBSERVATIONS": "0", "DBRAIN_AUDIT_ALERT_REPEAT_AFTER": "-1h",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := schedulerAuditConfigFromRuntime(t.TempDir()); err == nil {
				t.Fatal("expected invalid audit scheduler config")
			}
		})
	}
}

func TestAuditRootAndInstallerConfigTemplatesRemainInParity(t *testing.T) {
	readAudit := func(path string) map[string]any {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		value, ok := document["audit"].(map[string]any)
		if !ok {
			t.Fatalf("missing audit config in %s", path)
		}
		return value
	}
	root := readAudit(filepath.Join("..", "..", "config.yaml.sample"))
	installer := readAudit(filepath.Join("..", "install", "templates", "config.yaml.sample"))
	if !reflect.DeepEqual(root, installer) {
		t.Fatalf("root and installer audit config differ\nroot=%#v\ninstaller=%#v", root, installer)
	}
}

func TestBuildRemoteSchedulersCreatesAuditArtifactsOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			body := []byte("audit:\n  enabled: " + strconv.FormatBool(enabled) + "\n")
			if err := os.WriteFile(cfg.ConfigPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			schedulers, err := buildRemoteSchedulers(t.Context(), cfg, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer schedulers.Stop()
			_, statErr := os.Stat(filepath.Join(cfg.LogDir, "audit"))
			if enabled && statErr != nil {
				t.Fatalf("enabled audit store missing: %v", statErr)
			}
			if !enabled && !os.IsNotExist(statErr) {
				t.Fatalf("disabled audit created artifacts: %v", statErr)
			}
			if schedulers.audit == nil || schedulers.audit.Status().Enabled != enabled {
				t.Fatalf("audit scheduler status = %+v", schedulers.audit.Status())
			}
		})
	}
}

func TestBuildRemoteSchedulersAuditRuntimeFollowsWebCapabilityNotMCPOnly(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("audit:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mcpOnly, err := buildRemoteSchedulersWithMetaAndAuditRuntime(t.Context(), cfg, auditConfigMeta{Layout: "explicit_root", Source: "flag"}, io.Discard, false)
	if err != nil {
		t.Fatal(err)
	}
	defer mcpOnly.Stop()
	if mcpOnly.auditReports != nil || mcpOnly.auditRunner != nil {
		t.Fatal("MCP-only/authenticated read surface received write-capable audit runtime")
	}
	if _, statErr := os.Stat(filepath.Join(cfg.LogDir, "audit")); !os.IsNotExist(statErr) {
		t.Fatalf("MCP-only composition created report store: %v", statErr)
	}

	webEnabled, err := buildRemoteSchedulersWithMetaAndAuditRuntime(t.Context(), cfg, auditConfigMeta{Layout: "explicit_root", Source: "flag"}, io.Discard, true)
	if err != nil {
		t.Fatal(err)
	}
	defer webEnabled.Stop()
	dependencies := webEnabled.webAuditDependencies(t.Context())
	if dependencies.Reports == nil || dependencies.Runs == nil || dependencies.SyncInterval <= 0 || dependencies.StandardInterval <= 0 {
		t.Fatalf("authenticated web audit dependencies incomplete: %#v", dependencies)
	}
	if webEnabled.auditReports != dependencies.Reports {
		t.Fatal("web coordinator did not share scheduler report store instance")
	}
}

type fakeScheduledAuditStore struct {
	mu       sync.Mutex
	reports  []audit.Report
	state    audit.AlertState
	sequence []string
}

func (s *fakeScheduledAuditStore) Save(report audit.Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, report)
	s.sequence = append(s.sequence, "report")
	return nil
}
func (s *fakeScheduledAuditStore) LoadAlertState() (audit.AlertState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Schema == "" {
		return audit.AlertState{Schema: audit.AlertStateSchemaV1, Profiles: map[audit.Profile]audit.ProfileAlertState{}}, nil
	}
	return s.state, nil
}
func (s *fakeScheduledAuditStore) SaveAlertState(state audit.AlertState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.sequence = append(s.sequence, "state")
	return nil
}

func schedulerAuditReport(profile audit.Profile, at time.Time) audit.Report {
	return audit.Report{Profile: profile, Status: audit.StatusPass, CompletedAt: at, Checks: []audit.Check{{ID: audit.CheckBoundaryConfig, Status: audit.StatusPass, Required: true}}}
}

func TestAuditSchedulerDisabledStartsNoWorkAndNeverSchedulesDeep(t *testing.T) {
	var calls atomic.Int32
	s := newAuditScheduler(schedulerAuditConfig{}, func(context.Context, audit.Profile, time.Duration) (audit.Report, error) {
		calls.Add(1)
		return audit.Report{}, nil
	}, nil, nil, io.Discard)
	s.Start(t.Context())
	s.AfterSync(t.Context())
	s.run(t.Context(), audit.ProfileDeep, "forbidden")
	s.Stop()
	if calls.Load() != 0 {
		t.Fatalf("disabled/deep audit calls = %d", calls.Load())
	}
}

func TestAuditSchedulerOwnsFastAndStandardWithoutOverlapAndPersistsBeforeCompletion(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	entered := make(chan audit.Profile, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	runner := func(ctx context.Context, profile audit.Profile, _ time.Duration) (audit.Report, error) {
		calls.Add(1)
		entered <- profile
		select {
		case <-ctx.Done():
			return audit.Report{}, ctx.Err()
		case <-release:
			return schedulerAuditReport(profile, now), nil
		}
	}
	store := &fakeScheduledAuditStore{}
	s := newAuditScheduler(schedulerAuditConfig{Enabled: true, PostSyncFast: true, StandardInterval: 6 * time.Hour, Since: 7 * 24 * time.Hour, Alert: schedulerAuditAlertConfig{ConsecutiveObservations: 2, RepeatAfter: 24 * time.Hour}}, runner, store, nil, io.Discard)
	done := make(chan struct{})
	go func() { s.AfterSync(t.Context()); close(done) }()
	if got := <-entered; got != audit.ProfileFast {
		t.Fatalf("post-sync profile = %s", got)
	}
	s.run(t.Context(), audit.ProfileStandard, "interval")
	if calls.Load() != 1 {
		t.Fatal("fast and standard audits overlapped")
	}
	close(release)
	<-done
	if len(store.reports) != 1 || store.reports[0].Profile != audit.ProfileFast || len(store.sequence) == 0 || store.sequence[0] != "report" {
		t.Fatalf("persistence sequence = %#v reports=%#v", store.sequence, store.reports)
	}
}

func TestAuditSchedulerStandardIntervalAndAuditFailureDoNotAffectSyncResult(t *testing.T) {
	ticks := make(chan struct{}, 1)
	waits := make(chan time.Duration, 1)
	var profilesMu sync.Mutex
	profiles := []audit.Profile{}
	s := newAuditScheduler(schedulerAuditConfig{Enabled: true, StandardInterval: 6 * time.Hour, Since: 7 * 24 * time.Hour}, func(_ context.Context, profile audit.Profile, _ time.Duration) (audit.Report, error) {
		profilesMu.Lock()
		profiles = append(profiles, profile)
		profilesMu.Unlock()
		return audit.Report{}, errors.New("private provider/path secret")
	}, &fakeScheduledAuditStore{}, nil, io.Discard)
	s.wait = func(ctx context.Context, duration time.Duration) bool {
		waits <- duration
		select {
		case <-ctx.Done():
			return false
		case <-ticks:
			return true
		}
	}
	s.Start(t.Context())
	if got := <-waits; got != 6*time.Hour {
		t.Fatalf("standard wait = %s", got)
	}
	ticks <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	profilesMu.Lock()
	defer profilesMu.Unlock()
	if len(profiles) != 1 || profiles[0] != audit.ProfileStandard {
		t.Fatalf("scheduled profiles = %#v", profiles)
	}
}

func TestSyncPostRunAuditHookRunsOnlyAfterActualResultAndLockSettlement(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	s := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	s.runSync = func(context.Context, config.Config, syncAllFlags, io.Writer) error { return errors.New("sync failed") }
	hooked := make(chan struct{}, 1)
	s.postRun = func(context.Context) {
		if s.Status().Running {
			t.Error("audit hook ran before sync status settled")
		}
		lock, lockErr := acquireSyncAllLock(cfg, "post-run-test")
		if lockErr != nil {
			t.Errorf("audit hook ran before sync lock settled: %v", lockErr)
		} else {
			_ = lock.Close()
		}
		hooked <- struct{}{}
	}
	s.runAndPost(t.Context(), "interval")
	select {
	case <-hooked:
	case <-time.After(time.Second):
		t.Fatal("post-run audit hook did not run after failed actual sync")
	}

	lock, err := acquireSyncAllLock(cfg, "held")
	if err != nil {
		t.Fatal(err)
	}
	s.runAndPost(t.Context(), "locked")
	_ = lock.Close()
	select {
	case <-hooked:
		t.Fatal("lock-skipped sync triggered audit")
	default:
	}
}

func TestAuditCompletionMetricIsEmittedAfterReportPersistence(t *testing.T) {
	store := &fakeScheduledAuditStore{}
	var got metrics.Event
	s := newAuditScheduler(schedulerAuditConfig{Enabled: true, Since: 7 * 24 * time.Hour, StandardInterval: 6 * time.Hour}, func(context.Context, audit.Profile, time.Duration) (audit.Report, error) {
		return schedulerAuditReport(audit.ProfileFast, time.Now().UTC()), nil
	}, store, nil, io.Discard)
	s.emitCompleted = func(event metrics.Event) error {
		store.mu.Lock()
		defer store.mu.Unlock()
		if len(store.sequence) == 0 || store.sequence[0] != "report" {
			t.Error("completion metric preceded report persistence")
		}
		got = event
		return nil
	}
	s.run(t.Context(), audit.ProfileFast, "post_sync")
	if got["event"] != "audit.run.completed" || got["profile"] != "fast" {
		t.Fatalf("metric = %#v", got)
	}
}

func TestAuditSchedulerStopCancelsAndWaitsForPostSyncRun(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	s := newAuditScheduler(schedulerAuditConfig{Enabled: true, PostSyncFast: true, StandardInterval: time.Hour, Since: 24 * time.Hour}, func(ctx context.Context, _ audit.Profile, _ time.Duration) (audit.Report, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return audit.Report{}, ctx.Err()
	}, &fakeScheduledAuditStore{}, nil, io.Discard)
	s.Start(t.Context())
	go s.AfterSync(t.Context())
	<-entered
	s.Stop()
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before active post-sync audit exited")
	}
}

func TestAuditSchedulerConcurrentStopsWaitForTheSameActiveRun(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	allowExit := make(chan struct{})
	s := newAuditScheduler(schedulerAuditConfig{Enabled: true, PostSyncFast: true, StandardInterval: time.Hour, Since: 24 * time.Hour}, func(ctx context.Context, _ audit.Profile, _ time.Duration) (audit.Report, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-allowExit
		return audit.Report{}, ctx.Err()
	}, &fakeScheduledAuditStore{}, nil, io.Discard)
	s.Start(t.Context())
	go s.AfterSync(t.Context())
	<-entered
	stopped := make(chan struct{}, 2)
	go func() { s.Stop(); stopped <- struct{}{} }()
	<-canceled
	go func() { s.Stop(); stopped <- struct{}{} }()
	time.Sleep(20 * time.Millisecond)
	close(allowExit)
	for range 2 {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("concurrent Stop did not share active-run completion")
		}
	}
}

func TestAuditSchedulerSanitizesMetricSinkFailure(t *testing.T) {
	s := newAuditScheduler(schedulerAuditConfig{Enabled: true, StandardInterval: time.Hour, Since: 24 * time.Hour}, func(context.Context, audit.Profile, time.Duration) (audit.Report, error) {
		return schedulerAuditReport(audit.ProfileFast, time.Now().UTC()), nil
	}, &fakeScheduledAuditStore{}, nil, io.Discard)
	s.emitCompleted = func(metrics.Event) error { return errors.New("/private/path?token=secret") }
	s.run(t.Context(), audit.ProfileFast, "post_sync")
	status := s.Status()
	if status.LastStatus != "failed" || status.LastError != "metrics_sink_failed" {
		t.Fatalf("metric failure status = %+v", status)
	}
}
