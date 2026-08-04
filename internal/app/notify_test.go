package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/notify"
	"github.com/darron/dbrain/internal/syncjob"
)

type fakeNotificationObserver struct {
	observe func(context.Context, notify.Outcome) error
}

func (f fakeNotificationObserver) Observe(ctx context.Context, outcome notify.Outcome) error {
	return f.observe(ctx, outcome)
}

type fakePostSyncAuditor struct {
	afterSync func(context.Context)
}

func (f fakePostSyncAuditor) AfterSync(ctx context.Context) { f.afterSync(ctx) }

type fakeAppNotificationProvider struct {
	deliver func(context.Context, notify.Notification) (notify.Receipt, error)
}

func (p fakeAppNotificationProvider) Name() string { return "buzz" }

func (p fakeAppNotificationProvider) Deliver(ctx context.Context, notification notify.Notification) (notify.Receipt, error) {
	return p.deliver(ctx, notification)
}

func failedScheduledOutcome() scheduledSyncOutcome {
	finished := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	return scheduledSyncOutcome{
		Reason:     "interval",
		Status:     scheduledSyncStatusError,
		StartedAt:  finished.Add(-time.Minute),
		FinishedAt: finished,
		Err:        syncjob.WrapStageError("apple_notes", fs.ErrPermission),
	}
}

func TestRemoteSchedulerHardFailureNotifiesBeforePostSyncAudit(t *testing.T) {
	calls := []string{}
	notifier := fakeNotificationObserver{observe: func(_ context.Context, outcome notify.Outcome) error {
		calls = append(calls, "notify")
		if outcome.Status != notify.OutcomeFailure || outcome.FailureType != notify.FailureAppleNotesPermission {
			t.Fatalf("notification outcome = %#v", outcome)
		}
		return errors.New("private relay URL and secret")
	}}
	audit := fakePostSyncAuditor{afterSync: func(context.Context) { calls = append(calls, "audit") }}
	var logs bytes.Buffer

	hook := composePostRun(notifier, audit, &logs)
	hook(t.Context(), failedScheduledOutcome())

	if !slices.Equal(calls, []string{"notify", "audit"}) {
		t.Fatalf("calls = %v", calls)
	}
	if strings.Contains(logs.String(), "private relay") || !strings.Contains(logs.String(), "notification_delivery_failed") {
		t.Fatalf("unsafe notification log = %q", logs.String())
	}
}

func TestNotificationWiringSupportsEitherOrNeitherPostRunDependency(t *testing.T) {
	var calls []string
	notifier := fakeNotificationObserver{observe: func(context.Context, notify.Outcome) error {
		calls = append(calls, "notify")
		return nil
	}}
	audit := fakePostSyncAuditor{afterSync: func(context.Context) { calls = append(calls, "audit") }}

	composePostRun(notifier, nil, io.Discard)(t.Context(), failedScheduledOutcome())
	if !slices.Equal(calls, []string{"notify"}) {
		t.Fatalf("notification-only calls = %v", calls)
	}
	calls = nil
	composePostRun(nil, audit, io.Discard)(t.Context(), failedScheduledOutcome())
	if !slices.Equal(calls, []string{"audit"}) {
		t.Fatalf("audit-only calls = %v", calls)
	}
	if composePostRun(nil, nil, io.Discard) != nil {
		t.Fatal("empty composition installed a post-run hook")
	}
}

func TestNotificationWiringPreservesSettledStatusesAndSchedulerResult(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	var observed []notify.OutcomeStatus
	var audits int
	notifier := fakeNotificationObserver{observe: func(_ context.Context, outcome notify.Outcome) error {
		observed = append(observed, outcome.Status)
		return errors.New("state contained a private path")
	}}
	audit := fakePostSyncAuditor{afterSync: func(context.Context) { audits++ }}
	var logs bytes.Buffer
	scheduler := newSyncScheduler(cfg, schedulerSyncConfig{Enabled: true, Interval: time.Hour}, io.Discard)
	scheduler.postRun = composePostRun(notifier, audit, &logs)
	scheduler.runSync = func(context.Context, config.Config, syncAllFlags, io.Writer) error {
		return syncjob.WrapStageError("apple_notes", fs.ErrPermission)
	}

	scheduler.runAndPost(t.Context(), "interval")
	if got := scheduler.Status(); got.LastStatus != "error" || got.Running {
		t.Fatalf("scheduler result changed by notification failure: %#v", got)
	}
	if !slices.Equal(observed, []notify.OutcomeStatus{notify.OutcomeFailure}) || audits != 1 {
		t.Fatalf("observed=%v audits=%d", observed, audits)
	}
	if strings.Contains(logs.String(), "private path") {
		t.Fatalf("raw state error logged: %q", logs.String())
	}
}

func TestNotificationWiringClassifiesSuccessAndCancellation(t *testing.T) {
	var statuses []notify.OutcomeStatus
	hook := composePostRun(fakeNotificationObserver{observe: func(_ context.Context, outcome notify.Outcome) error {
		statuses = append(statuses, outcome.Status)
		return nil
	}}, nil, io.Discard)
	base := failedScheduledOutcome()
	base.Status, base.Err = scheduledSyncStatusOK, nil
	hook(t.Context(), base)
	base.Status, base.Err = scheduledSyncStatusCancelled, context.Canceled
	hook(t.Context(), base)
	if !slices.Equal(statuses, []notify.OutcomeStatus{notify.OutcomeSuccess, notify.OutcomeCancelled}) {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestNotificationWiringDisabledCreatesNoNotificationStateDirectory(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("notifications:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	schedulers, err := buildRemoteSchedulers(t.Context(), cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer schedulers.Stop()
	if schedulers.notifier != nil {
		t.Fatal("disabled notifications constructed a notifier")
	}
	if _, err := os.Stat(filepath.Join(cfg.LogDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled notifications created state directory: %v", err)
	}
}

func TestNotificationWiringEnabledConstructsNotifierAndPrivateStateDirectory(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeEnabledNotificationConfig(t, cfg)

	schedulers, err := buildRemoteSchedulers(t.Context(), cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer schedulers.Stop()
	if schedulers.notifier == nil || schedulers.syncAll.postRun == nil {
		t.Fatal("enabled notifications were not composed into scheduled sync")
	}
	info, err := os.Stat(filepath.Join(cfg.LogDir, "notifications"))
	if err != nil || !info.IsDir() {
		t.Fatalf("notification state directory = %#v, %v", info, err)
	}
}

func TestNotificationWiringRejectsInvalidEnabledConfigurationBeforeStateCreation(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("notifications:\n  enabled: true\n  repeat_after: bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildRemoteSchedulers(t.Context(), cfg, io.Discard); err == nil {
		t.Fatal("invalid enabled notification config was accepted")
	}
	if _, err := os.Stat(filepath.Join(cfg.LogDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid configuration created state directory: %v", err)
	}
}

func TestNotifyCommandStatusJSONIsReadOnlyAndNoCreateWhenDisabled(t *testing.T) {
	root := t.TempDir()
	stdout := runRootCommand(t, root, "notify", "status", "--json")
	var status notify.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, stdout)
	}
	if status.Enabled || status.RepeatAfter != "6h0m0s" || status.PendingDeliveries != 0 {
		t.Fatalf("disabled status = %#v", status)
	}
	if _, err := os.Stat(filepath.Join(root, "logs", "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only status created notification state: %v", err)
	}
}

func TestNotifyCommandStatusShowsSafePersistedState(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeEnabledNotificationConfig(t, cfg)
	store, err := notify.OpenStore(cfg.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	provider := fakeAppNotificationProvider{deliver: func(context.Context, notify.Notification) (notify.Receipt, error) {
		return notify.Receipt{Provider: "buzz", ExternalID: "event-id-not-in-status", AcceptedAt: now}, nil
	}}
	manager := notify.NewManager(notify.Options{RepeatAfter: 6 * time.Hour}, store, []notify.Provider{provider}, notify.WithClock(func() time.Time { return now }))
	definition, _ := notify.LookupFailure(notify.FailureStoreOpen)
	outcome := notify.Outcome{Operation: notify.OperationScheduledSyncAll, Status: notify.OutcomeFailure, FailureType: notify.FailureStoreOpen, ErrorCode: definition.ErrorCode, StartedAt: now.Add(-time.Minute), FinishedAt: now}
	if err := manager.Observe(t.Context(), outcome); err != nil {
		t.Fatal(err)
	}

	stdout := runRootCommand(t, root, "notify", "status", "--json")
	var status notify.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, stdout)
	}
	if len(status.OpenIncidents) != 1 || len(status.Providers) != 1 || status.Providers[0].LastStatus != "accepted" {
		t.Fatalf("status = %#v", status)
	}
	for _, forbidden := range []string{"froese.communities.buzz.xyz", "00000000-0000-4000-8000-000000000001", "env:TEST_BUZZ_KEY", "event-id-not-in-status"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("status leaked %q: %s", forbidden, stdout)
		}
	}
}

func TestNotifyCommandTestBuzzDeliversDirectlyWithoutStateMutation(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeEnabledNotificationConfig(t, cfg)
	now := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	var delivered []notify.Notification
	deps := notifyCommandDependencies{
		now: func() time.Time { return now },
		buildProviders: func(context.Context, notify.Config) ([]notify.Provider, error) {
			return []notify.Provider{fakeAppNotificationProvider{deliver: func(_ context.Context, notification notify.Notification) (notify.Receipt, error) {
				delivered = append(delivered, notification)
				return notify.Receipt{Provider: "buzz", ExternalID: "event-id", AcceptedAt: now}, nil
			}}}, nil
		},
	}

	stdout := runNotifyCommandWithDependencies(t, root, deps, "test", "buzz", "--json")
	var result notifyTestResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode test result: %v\n%s", err, stdout)
	}
	if result.Provider != "buzz" || result.Status != "accepted" || result.ExternalID != "event-id" || !result.AcceptedAt.Equal(now) {
		t.Fatalf("test result = %#v", result)
	}
	if len(delivered) != 1 || delivered[0].Kind != notify.EventTest || delivered[0].Body != "dbrain notification delivery test." || !delivered[0].CreatedAt.Equal(now) {
		t.Fatalf("delivered = %#v", delivered)
	}
	if _, err := os.Stat(filepath.Join(cfg.LogDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("direct provider test mutated notification state: %v", err)
	}

	human := runNotifyCommandWithDependencies(t, root, deps, "test", "buzz")
	if !strings.Contains(strings.ToLower(human), "relay accepted") || strings.Contains(strings.ToLower(human), "read") {
		t.Fatalf("inaccurate human acceptance label = %q", human)
	}
}

func TestNotifyCommandTestBuzzSanitizesProviderErrors(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeEnabledNotificationConfig(t, cfg)
	deps := notifyCommandDependencies{
		now: time.Now,
		buildProviders: func(context.Context, notify.Config) ([]notify.Provider, error) {
			return []notify.Provider{fakeAppNotificationProvider{deliver: func(context.Context, notify.Notification) (notify.Receipt, error) {
				return notify.Receipt{}, errors.New("private relay and secret text")
			}}}, nil
		},
	}
	cmd := newNotifyCommandWithDependencies(&rootOptions{root: root}, deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"test", "buzz"})
	err = cmd.ExecuteContext(t.Context())
	if err == nil || strings.Contains(err.Error(), "private relay") || err.Error() != "notification_delivery_failed" {
		t.Fatalf("sanitized error = %v", err)
	}
}

func writeEnabledNotificationConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	body := `notifications:
  enabled: true
  repeat_after: 6h
  buzz:
    enabled: true
    relay_url: wss://froese.communities.buzz.xyz
    channel_id: 00000000-0000-4000-8000-000000000001
    private_key_ref: env:TEST_BUZZ_KEY
`
	if err := os.WriteFile(cfg.ConfigPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runNotifyCommandWithDependencies(t *testing.T, root string, deps notifyCommandDependencies, args ...string) string {
	t.Helper()
	cmd := newNotifyCommandWithDependencies(&rootOptions{root: root}, deps)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("execute notify %v: %v (stderr=%q)", args, err, stderr.String())
	}
	return stdout.String()
}
