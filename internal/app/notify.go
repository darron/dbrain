package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/notify"
	"github.com/darron/dbrain/internal/runtimeenv"
)

type notificationObserver interface {
	Observe(context.Context, notify.Outcome) error
}

type postSyncAuditor interface {
	AfterSync(context.Context)
}

func composePostRun(notifier notificationObserver, auditor postSyncAuditor, logOut io.Writer) func(context.Context, scheduledSyncOutcome) {
	if notifier == nil && auditor == nil {
		return nil
	}
	if logOut == nil {
		logOut = io.Discard
	}
	return func(ctx context.Context, settled scheduledSyncOutcome) {
		if notifier != nil {
			if err := notifier.Observe(ctx, classifyScheduledSyncOutcome(settled)); err != nil {
				logNotificationError(logOut, err)
			}
		}
		if auditor != nil {
			auditor.AfterSync(ctx)
		}
	}
}

func logNotificationError(logOut io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintf(logOut, "scheduled notification failed: error=%s\n", safeNotificationErrorCode(err))
}

func safeNotificationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	code := strings.TrimSpace(err.Error())
	switch code {
	case "notification_manager_unavailable",
		"notification_manager_invalid",
		"notification_outcome_invalid",
		"notification_state_load_failed",
		"notification_state_transition_failed",
		"notification_state_persist_failed",
		"notification_metrics_unavailable",
		"notification_delivery_failed":
		return code
	default:
		return "notification_delivery_failed"
	}
}

func buildRemoteNotificationObserver(ctx context.Context, cfg config.Config, logOut io.Writer) (notificationObserver, error) {
	notificationConfig, err := notify.LoadConfig(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("configure notifications: %w", err)
	}
	if !notificationConfig.Enabled {
		return nil, nil
	}
	providers, err := notify.BuiltinRegistry().Build(ctx, notificationConfig)
	if err != nil {
		return nil, fmt.Errorf("configure notification providers: %w", err)
	}
	stateStore, err := notify.OpenStore(cfg.LogDir)
	if err != nil {
		return nil, fmt.Errorf("open notification state: %w", err)
	}
	managerOptions := []notify.ManagerOption{
		notify.WithManagerLogger(func(code string) {
			logNotificationError(logOut, errors.New(code))
		}),
	}
	metricsConfig, metricsErr := metrics.ResolveConfig(cfg.RootDir, cfg.LogDir)
	if metricsErr != nil {
		logNotificationError(logOut, errors.New("notification_metrics_unavailable"))
	} else {
		managerOptions = append(managerOptions, notify.WithMetricsEmitter(scheduledNotificationMetricEmitter(metricsConfig)))
	}
	manager := notify.NewManager(
		notify.Options{RepeatAfter: notificationConfig.RepeatAfter},
		stateStore,
		providers,
		managerOptions...,
	)
	if _, err := manager.Status(); err != nil {
		return nil, errors.New(safeNotificationErrorCode(err))
	}
	return manager, nil
}

func scheduledNotificationMetricEmitter(cfg metrics.Config) func(metrics.Event) error {
	return func(event metrics.Event) error {
		sink, err := metrics.Open(cfg)
		if err != nil {
			return err
		}
		run := metrics.RunContext{
			RunID:      metrics.NewRunID("notification", time.Now().UTC()),
			Command:    "notify",
			Invocation: "scheduler",
			Sink:       sink,
		}
		if err := run.Emit(event); err != nil {
			_ = sink.Close()
			return err
		}
		return sink.Close()
	}
}

type notifyCommandDependencies struct {
	now            func() time.Time
	buildProviders func(context.Context, notify.Config) ([]notify.Provider, error)
}

func defaultNotifyCommandDependencies() notifyCommandDependencies {
	return notifyCommandDependencies{
		now: time.Now,
		buildProviders: func(ctx context.Context, config notify.Config) ([]notify.Provider, error) {
			return notify.BuiltinRegistry().Build(ctx, config)
		},
	}
}

func newNotifyCommand(root *rootOptions) *cobra.Command {
	return newNotifyCommandWithDependencies(root, defaultNotifyCommandDependencies())
}

func newNotifyCommandWithDependencies(root *rootOptions, dependencies notifyCommandDependencies) *cobra.Command {
	if dependencies.now == nil {
		dependencies.now = time.Now
	}
	if dependencies.buildProviders == nil {
		dependencies.buildProviders = defaultNotifyCommandDependencies().buildProviders
	}
	cmd := &cobra.Command{
		Use:         "notify",
		Short:       "Inspect and test hard-failure notifications",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE:        helpCommand,
	}
	cmd.AddCommand(
		newNotifyStatusCommand(root),
		newNotifyTestCommand(root, dependencies),
	)
	return cmd
}

func newNotifyStatusCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:         "status",
		Short:       "Show redacted notification incident and delivery status",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, notificationConfig, err := loadNotifyCommandConfig(cmd.Context(), root)
			if err != nil {
				return err
			}
			reader, err := notify.OpenReader(cfg.LogDir)
			if err != nil {
				return errors.New("notification_state_load_failed")
			}
			state, err := reader.Load()
			if err != nil {
				return errors.New("notification_state_load_failed")
			}
			status, err := notify.BuildStatus(notificationConfig, state)
			if err != nil {
				return errors.New("notification_status_invalid")
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			writeNotifyStatus(cmd.OutOrStdout(), status)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print redacted notification status as JSON")
	return cmd
}

func newNotifyTestCommand(root *rootOptions, dependencies notifyCommandDependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "test",
		Short:       "Send a synthetic notification directly through a provider",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE:        helpCommand,
	}
	cmd.AddCommand(newNotifyTestBuzzCommand(root, dependencies))
	return cmd
}

type notifyTestResult struct {
	Provider   string    `json:"provider"`
	Status     string    `json:"status"`
	ExternalID string    `json:"external_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

func newNotifyTestBuzzCommand(root *rootOptions, dependencies notifyCommandDependencies) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:         "buzz",
		Short:       "Send a synthetic test message to the configured Buzz relay",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, notificationConfig, err := loadNotifyCommandConfig(cmd.Context(), root)
			if err != nil {
				return err
			}
			if !notificationConfig.Enabled || !notificationConfig.Buzz.Enabled {
				return errors.New("notification_provider_not_configured")
			}
			providers, err := dependencies.buildProviders(cmd.Context(), notificationConfig)
			if err != nil {
				return errors.New("notification_provider_invalid")
			}
			provider := providerNamed(providers, "buzz")
			if provider == nil {
				return errors.New("notification_provider_not_configured")
			}
			createdAt := dependencies.now().UTC()
			notification, err := notify.DeriveNotification(notify.EventFacts{
				Kind:      notify.EventTest,
				Operation: notify.OperationScheduledSyncAll,
				CreatedAt: createdAt,
			})
			if err != nil {
				return errors.New("notification_test_invalid")
			}
			receipt, err := provider.Deliver(cmd.Context(), notification)
			if err != nil || !validNotifyTestReceipt(receipt, "buzz") {
				return errors.New("notification_delivery_failed")
			}
			result := notifyTestResult{
				Provider:   "buzz",
				Status:     "accepted",
				ExternalID: receipt.ExternalID,
				AcceptedAt: receipt.AcceptedAt.UTC(),
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Buzz relay accepted the test notification: event_id=%s accepted_at=%s\n", result.ExternalID, result.AcceptedAt.Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print relay acceptance as JSON")
	return cmd
}

func loadNotifyCommandConfig(ctx context.Context, root *rootOptions) (config.Config, notify.Config, error) {
	if root == nil {
		root = &rootOptions{}
	}
	cfg, _, err := loadAuditConfigContext(ctx, root.root, root.configFile)
	if err != nil {
		return config.Config{}, notify.Config{}, err
	}
	configSnapshot, err := runtimeenv.LoadConfigSnapshot(ctx, cfg.ConfigPath, auditConfigMaxBytes)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return config.Config{}, notify.Config{}, fmt.Errorf("read bounded notification config: %w", err)
		}
		configSnapshot = map[string]any{}
	}
	dotenvSnapshot, err := runtimeenv.LoadDotEnvSnapshot(ctx, cfg.RootDir, auditConfigMaxBytes)
	if err != nil {
		return config.Config{}, notify.Config{}, fmt.Errorf("read bounded notification dotenv: %w", err)
	}
	cleanupSnapshot := runtimeenv.RegisterConfigSnapshot(cfg.RootDir, configSnapshot, dotenvSnapshot)
	defer cleanupSnapshot()
	notificationConfig, err := notify.LoadConfig(cfg.RootDir)
	if err != nil {
		return config.Config{}, notify.Config{}, err
	}
	return cfg, notificationConfig, nil
}

func providerNamed(providers []notify.Provider, name string) notify.Provider {
	for _, provider := range providers {
		if provider != nil && provider.Name() == name {
			return provider
		}
	}
	return nil
}

func validNotifyTestReceipt(receipt notify.Receipt, provider string) bool {
	if receipt.Provider != provider || receipt.AcceptedAt.IsZero() || receipt.AcceptedAt.Location() != time.UTC {
		return false
	}
	if len(receipt.ExternalID) == 0 || len(receipt.ExternalID) > 128 {
		return false
	}
	for _, char := range receipt.ExternalID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == ':' {
			continue
		}
		return false
	}
	return true
}

func writeNotifyStatus(out io.Writer, status notify.Status) {
	state := "disabled"
	if status.Enabled {
		state = "enabled"
	}
	_, _ = fmt.Fprintf(out, "Notifications: %s\nRepeat after: %s\n", state, status.RepeatAfter)
	for _, provider := range status.Providers {
		_, _ = fmt.Fprintf(out, "Provider %s: configured", provider.Name)
		if provider.LastStatus == "accepted" && provider.LastAcceptedAt != nil {
			_, _ = fmt.Fprintf(out, "; last relay status: accepted at %s", provider.LastAcceptedAt.Format(time.RFC3339))
		} else if provider.LastStatus != "" {
			_, _ = fmt.Fprintf(out, "; last delivery status: %s", provider.LastStatus)
			if provider.LastErrorCode != "" {
				_, _ = fmt.Fprintf(out, " error_code=%s", provider.LastErrorCode)
			}
		}
		_, _ = fmt.Fprintln(out)
	}
	for _, incident := range status.OpenIncidents {
		_, _ = fmt.Fprintf(out, "Open incident: failure_type=%s occurrences=%d first_seen_at=%s last_seen_at=%s\n", incident.FailureType, incident.Occurrences, incident.FirstSeenAt.Format(time.RFC3339), incident.LastSeenAt.Format(time.RFC3339))
	}
	for _, incident := range status.CoolingIncidents {
		_, _ = fmt.Fprintf(out, "Cooling incident: failure_type=%s occurrences=%d rearms_at=%s\n", incident.FailureType, incident.Occurrences, incident.RearmsAt.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(out, "Pending deliveries: %d\n", status.PendingDeliveries)
}
