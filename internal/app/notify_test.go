package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	name    string
	deliver func(context.Context, notify.Notification) (notify.Receipt, error)
}

func (p fakeAppNotificationProvider) Name() string {
	if p.name == "" {
		return "buzz"
	}
	return p.name
}

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
	writeNotificationAppConfig(t, cfg, true)

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

func TestNotifyCommandStatusReportsConfiguredProvidersWhileAutomaticNotificationsDisabled(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeNotificationAppConfig(t, cfg, false)

	stdout := runRootCommand(t, root, "notify", "status", "--json")
	var status notify.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, stdout)
	}
	if status.Enabled || len(status.Providers) != 2 || status.Providers[0].Name != "buzz" || status.Providers[1].Name != "slack" || !status.Providers[0].Configured || !status.Providers[1].Configured {
		t.Fatalf("disabled configured status = %#v", status)
	}
	if _, err := os.Stat(filepath.Join(cfg.LogDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configured read-only status created notification state: %v", err)
	}
}

func TestNotifyCommandStatusReportsConfiguredRepeatAfterWhileAutomaticNotificationsDisabled(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeNotificationAppConfig(t, cfg, false)
	configBody, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte(strings.Replace(string(configBody), "repeat_after: 6h", "repeat_after: 12h", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout := runRootCommand(t, root, "notify", "status", "--json")
	var status notify.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, stdout)
	}
	if status.Enabled || status.RepeatAfter != "12h0m0s" {
		t.Fatalf("disabled configured status = %#v", status)
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
	writeNotificationAppConfig(t, cfg, true)
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
	if len(status.OpenIncidents) != 1 || len(status.Providers) != 2 || status.Providers[0].LastStatus != "accepted" || status.Providers[1].Name != "slack" {
		t.Fatalf("status = %#v", status)
	}
	for _, forbidden := range []string{"froese.communities.buzz.xyz", "00000000-0000-4000-8000-000000000001", "env:TEST_BUZZ_KEY", "event-id-not-in-status"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("status leaked %q: %s", forbidden, stdout)
		}
	}
}

func TestWriteNotifyStatusUsesProviderNeutralAcceptedWording(t *testing.T) {
	t.Parallel()
	acceptedAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	writeNotifyStatus(&out, notify.Status{Providers: []notify.ProviderStatus{{Name: "slack", LastStatus: "accepted", LastAcceptedAt: &acceptedAt}}})
	if got := out.String(); !strings.Contains(got, "last delivery status: accepted at 2026-08-04T12:00:00Z") || strings.Contains(got, "last relay status") {
		t.Fatalf("status output = %q", got)
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
	writeNotificationAppConfig(t, cfg, false)
	now := time.Date(2026, 8, 3, 23, 35, 8, 0, time.UTC)
	var delivered []notify.Notification
	var builtConfig notify.Config
	deps := notifyCommandDependencies{
		now: func() time.Time { return now },
		buildProviders: func(_ context.Context, config notify.Config) ([]notify.Provider, error) {
			builtConfig = config
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
	if !builtConfig.Enabled || !builtConfig.Buzz.Enabled {
		t.Fatalf("explicit provider test did not enable registry construction: %#v", builtConfig)
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
	writeNotificationAppConfig(t, cfg, true)
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

func TestNotifyCommandTestSlackDeliversDirectlyWithoutStateMutation(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeNotificationAppConfig(t, cfg, false)
	now := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	var delivered []notify.Notification
	var builtConfig notify.Config
	deps := notifyCommandDependencies{
		now: func() time.Time { return now },
		buildProviders: func(_ context.Context, config notify.Config) ([]notify.Provider, error) {
			builtConfig = config
			return []notify.Provider{fakeAppNotificationProvider{name: "slack", deliver: func(_ context.Context, notification notify.Notification) (notify.Receipt, error) {
				delivered = append(delivered, notification)
				return notify.Receipt{Provider: "slack", ExternalID: "correlation-id", AcceptedAt: now}, nil
			}}}, nil
		},
	}

	stdout := runNotifyCommandWithDependencies(t, root, deps, "test", "slack", "--json")
	var result struct {
		Provider      string `json:"provider"`
		Status        string `json:"status"`
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode Slack JSON acceptance: %v\n%s", err, stdout)
	}
	if result.Provider != "slack" || result.Status != "accepted" || result.CorrelationID != "correlation-id" || strings.Contains(stdout, "message_id") {
		t.Fatalf("Slack JSON acceptance = %q", stdout)
	}
	if len(delivered) != 1 || delivered[0].Kind != notify.EventTest || delivered[0].Body != "dbrain notification delivery test." || !delivered[0].CreatedAt.Equal(now) {
		t.Fatalf("delivered = %#v", delivered)
	}
	if !builtConfig.Enabled || !builtConfig.Slack.Enabled || builtConfig.Buzz.Enabled {
		t.Fatalf("explicit Slack test did not enable registry construction: %#v", builtConfig)
	}
	if _, err := os.Stat(filepath.Join(cfg.LogDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("direct Slack test mutated notification state: %v", err)
	}

	human := runNotifyCommandWithDependencies(t, root, deps, "test", "slack")
	if !strings.Contains(human, "Slack webhook accepted the test notification") || !strings.Contains(human, "correlation_id=correlation-id") || strings.Contains(strings.ToLower(human), "message_id") || strings.Contains(strings.ToLower(human), "read") {
		t.Fatalf("inaccurate Slack acceptance label = %q", human)
	}
}

func TestNotifyCommandTestSlackSanitizesProviderErrors(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeNotificationAppConfig(t, cfg, false)
	deps := notifyCommandDependencies{
		now: time.Now,
		buildProviders: func(context.Context, notify.Config) ([]notify.Provider, error) {
			return []notify.Provider{fakeAppNotificationProvider{name: "slack", deliver: func(context.Context, notify.Notification) (notify.Receipt, error) {
				return notify.Receipt{}, errors.New("private Slack webhook and secret text")
			}}}, nil
		},
	}
	cmd := newNotifyCommandWithDependencies(&rootOptions{root: root}, deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"test", "slack"})
	err = cmd.ExecuteContext(t.Context())
	if err == nil || strings.Contains(err.Error(), "private Slack") || err.Error() != "notification_delivery_failed" {
		t.Fatalf("sanitized error = %v", err)
	}
}

func TestNotifyCommandRejectsInvalidBuzzConfigWhileAutomaticNotificationsDisabled(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	body := `notifications:
  enabled: false
  buzz:
    enabled: true
    relay_url: https://user:private@relay.example/path
    channel_id: not-a-uuid
    private_key_ref: inline-secret
`
	if err := os.WriteFile(cfg.ConfigPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--json"}, {"test", "buzz", "--json"}} {
		cmd := NewRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{"--root", root, "--no-caffeinate", "--no-debug", "notify"}, args...))
		err := cmd.ExecuteContext(t.Context())
		if err == nil || strings.Contains(err.Error(), "user:private") || strings.Contains(err.Error(), "inline-secret") {
			t.Fatalf("notify %v safe config error = %v", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.LogDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid provider config created notification state: %v", err)
	}
}

func writeNotificationAppConfig(t *testing.T, cfg config.Config, globallyEnabled bool) {
	t.Helper()
	body := fmt.Sprintf(`notifications:
  enabled: %t
  repeat_after: 6h
  buzz:
    enabled: true
    relay_url: wss://froese.communities.buzz.xyz
    channel_id: 00000000-0000-4000-8000-000000000001
    private_key_ref: env:TEST_BUZZ_KEY
  slack:
    enabled: true
    webhook_url_ref: env:TEST_SLACK_WEBHOOK
`, globallyEnabled)
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
