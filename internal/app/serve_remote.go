package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/sqlitearchive"
	"github.com/darron/dbrain/web"
)

func newServeRemoteCommand(root *rootOptions) *cobra.Command {
	var webEnabled bool
	var mcpEnabled bool
	var mcpPath string
	var hostname string
	var stateDir string
	var listen string
	var tlsEnabled bool
	var funnelEnabled bool
	var startupTimeout time.Duration
	var authKey string
	var authKeyRef string
	var allowSecretCommand bool
	var advertiseTags []string
	var controlURL string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Serve web and MCP on a built-in Tailscale tsnet node",
		Long: `Serve web and/or MCP on a built-in Tailscale tsnet node.

Remote web is the full read/write web UI. MCP remains read-only. By default,
Tailscale ACLs govern who can reach the node. --tsnet-funnel makes the selected
surfaces public through Tailscale Funnel when tailnet policy permits it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			applyServeRemoteFlagOverrides(cmd, cfg.DataDir, &opts, serveRemoteFlags{
				webEnabled:         webEnabled,
				mcpEnabled:         mcpEnabled,
				mcpPath:            mcpPath,
				hostname:           hostname,
				stateDir:           stateDir,
				listen:             listen,
				tlsEnabled:         tlsEnabled,
				funnelEnabled:      funnelEnabled,
				startupTimeout:     startupTimeout,
				authKey:            authKey,
				authKeyRef:         authKeyRef,
				allowSecretCommand: allowSecretCommand,
				advertiseTags:      advertiseTags,
				controlURL:         controlURL,
				verbose:            verbose,
			})
			webAuditEnabled := false
			if opts.Web {
				webAuditEnabled, err = web.AuditAPIEnabled(cmd.Context(), cfg)
				if err != nil {
					return fmt.Errorf("resolve web audit authentication: %w", err)
				}
			}
			mcpAuditEnabled := opts.MCP && mcpserver.AuthEnabled(cfg)
			schedulers, err := buildRemoteSchedulersWithMetaAndAuditRuntime(cmd.Context(), cfg, auditMetaForInvocation(root), cmd.ErrOrStderr(), webAuditEnabled)
			if err != nil {
				return err
			}
			if webAuditEnabled {
				remote.SetWebAuditDependencies(&opts, schedulers.webAuditDependencies(cmd.Context()))
			}
			if mcpAuditEnabled {
				auditDependencies, auditErr := resolveMCPAuditServerDependenciesWithReports(cmd.Context(), cfg, auditMetaForInvocation(root), schedulers.auditReports)
				if auditErr != nil {
					return fmt.Errorf("configure MCP audit: %w", auditErr)
				}
				remote.SetMCPDependencies(&opts, auditDependencies)
			}
			opts.SchedulerStatus = schedulers.syncAll.Status
			opts.OnReady = func() {
				schedulers.audit.Start(cmd.Context())
				schedulers.syncAll.Start(cmd.Context())
				schedulers.sqliteArchive.Start(cmd.Context())
			}
			defer schedulers.Stop()
			return remote.Serve(cmd.Context(), cfg, opts, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().BoolVar(&webEnabled, "web", true, "Mount the read/write web UI at /")
	cmd.Flags().BoolVar(&mcpEnabled, "mcp", true, "Mount read-only MCP Streamable HTTP at /mcp")
	cmd.Flags().StringVar(&mcpPath, "mcp-path", remote.DefaultMCPPath, "Remote MCP endpoint path")
	cmd.Flags().StringVar(&hostname, "tsnet-hostname", remote.DefaultHostname, "Stable tailnet machine name")
	cmd.Flags().StringVar(&stateDir, "tsnet-state-dir", "", "Durable tsnet state directory (default: <data_dir>/tsnet/<hostname>)")
	cmd.Flags().StringVar(&listen, "tsnet-listen", "", "Tailnet listen address (default: :443 with TLS/Funnel, :80 without TLS)")
	cmd.Flags().BoolVar(&tlsEnabled, "tsnet-tls", true, "Use Tailscale HTTPS via ListenTLS")
	cmd.Flags().BoolVar(&funnelEnabled, "tsnet-funnel", false, "Expose the tsnet listener through Tailscale Funnel")
	cmd.Flags().DurationVar(&startupTimeout, "tsnet-startup-timeout", remote.DefaultStartupTimeout, "Maximum time to wait for tsnet Up(ctx)")
	cmd.Flags().StringVar(&authKey, "tsnet-auth-key", "", "Optional direct Tailscale auth key; prefer --tsnet-auth-key-ref")
	cmd.Flags().StringVar(&authKeyRef, "tsnet-auth-key-ref", "", "Typed auth key secret ref: env:, op://, or keychain://")
	cmd.Flags().BoolVar(&allowSecretCommand, "tsnet-allow-secret-command", false, "Permit YAML-only tsnet.auth_key_command execution")
	cmd.Flags().StringSliceVar(&advertiseTags, "tsnet-advertise-tags", nil, "Comma-separated Tailscale tags to request")
	cmd.Flags().StringVar(&controlURL, "tsnet-control-url", "", "Experimental alternate Tailscale control server URL")
	cmd.Flags().BoolVar(&verbose, "tsnet-verbose", false, "Enable verbose tsnet backend logs")

	return cmd
}

type remoteSchedulers struct {
	syncAll          *syncScheduler
	sqliteArchive    *sqliteArchiveScheduler
	audit            *auditScheduler
	auditReports     *audit.ReportStore
	auditRunner      func(context.Context, audit.Profile, time.Duration) (audit.Report, error)
	auditSince       time.Duration
	syncInterval     time.Duration
	standardInterval time.Duration
}

func buildRemoteSchedulers(ctx context.Context, cfg config.Config, logOut io.Writer) (remoteSchedulers, error) {
	return buildRemoteSchedulersWithMeta(ctx, cfg, auditConfigMeta{Layout: "xdg", Source: "default"}, logOut)
}

func buildRemoteSchedulersWithMeta(ctx context.Context, cfg config.Config, meta auditConfigMeta, logOut io.Writer) (remoteSchedulers, error) {
	return buildRemoteSchedulersWithMetaAndAuditRuntime(ctx, cfg, meta, logOut, false)
}

func buildRemoteSchedulersWithMetaAndAuditRuntime(ctx context.Context, cfg config.Config, meta auditConfigMeta, logOut io.Writer, includeAuditRuntime bool) (remoteSchedulers, error) {
	configSnapshot, err := runtimeenv.LoadConfigSnapshot(ctx, cfg.ConfigPath, auditConfigMaxBytes)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return remoteSchedulers{}, fmt.Errorf("read bounded scheduler config: %w", err)
		}
		configSnapshot = map[string]any{}
	}
	dotenvSnapshot, err := runtimeenv.LoadDotEnvSnapshot(ctx, cfg.RootDir, auditConfigMaxBytes)
	if err != nil {
		return remoteSchedulers{}, fmt.Errorf("read bounded scheduler dotenv: %w", err)
	}
	cleanupSnapshot := runtimeenv.RegisterConfigSnapshot(cfg.RootDir, configSnapshot, dotenvSnapshot)
	defer cleanupSnapshot()

	syncOpts, err := schedulerSyncConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return remoteSchedulers{}, err
	}
	archiveOpts, err := schedulerSQLiteArchiveConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return remoteSchedulers{}, err
	}
	auditOpts, err := schedulerAuditConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return remoteSchedulers{}, err
	}
	var writer sqlitearchive.ObjectWriter
	if archiveOpts.Enabled {
		writer, err = buildScheduledSQLiteArchiveWriter(ctx, cfg.RootDir)
		if err != nil {
			return remoteSchedulers{}, fmt.Errorf("configure scheduled SQLite archive: %w", err)
		}
	}
	schedulers := remoteSchedulers{
		syncAll:          newSyncScheduler(cfg, syncOpts, logOut),
		sqliteArchive:    newSQLiteArchiveScheduler(cfg, archiveOpts, writer, logOut),
		audit:            newAuditScheduler(auditOpts, nil, nil, nil, logOut),
		auditSince:       auditOpts.Since,
		syncInterval:     syncOpts.Interval,
		standardInterval: auditOpts.StandardInterval,
	}
	if auditOpts.Enabled || includeAuditRuntime {
		features, resolveErr := resolveAuditFeatures(cfg, meta)
		if resolveErr != nil {
			return remoteSchedulers{}, fmt.Errorf("configure scheduled audit features: %w", resolveErr)
		}
		runner, resolveErr := newScheduledAuditRunner(ctx, cfg, features)
		if resolveErr != nil {
			return remoteSchedulers{}, fmt.Errorf("configure scheduled audit runner: %w", resolveErr)
		}
		reportStore, storeErr := audit.NewReportStore(cfg.LogDir)
		if storeErr != nil {
			return remoteSchedulers{}, fmt.Errorf("configure scheduled audit report store: %w", storeErr)
		}
		schedulers.auditRunner = runner
		schedulers.auditReports = reportStore
	}
	if auditOpts.Enabled {
		webhook, webhookErr := audit.NewWebhook(audit.WebhookConfig{URL: auditOpts.Alert.WebhookURL, BearerTokenRef: auditOpts.Alert.BearerTokenRef, AllowPrivateOrigin: auditOpts.Alert.AllowPrivateOrigin, AdminOrigin: auditOpts.Alert.AdminOrigin})
		if webhookErr != nil {
			return remoteSchedulers{}, fmt.Errorf("configure scheduled audit webhook: %w", webhookErr)
		}
		schedulers.audit = newAuditScheduler(auditOpts, schedulers.auditRunner, schedulers.auditReports, webhook, logOut)
		metricsConfig, metricsErr := metrics.ResolveConfig(cfg.RootDir, cfg.LogDir)
		if metricsErr != nil {
			return remoteSchedulers{}, fmt.Errorf("configure scheduled audit metrics: %w", metricsErr)
		}
		schedulers.audit.emitCompleted = scheduledAuditMetricEmitter(metricsConfig)
		schedulers.syncAll.postRun = func(ctx context.Context, _ scheduledSyncOutcome) {
			schedulers.audit.AfterSync(ctx)
		}
	}
	return schedulers, nil
}

func (s remoteSchedulers) webAuditDependencies(ctx context.Context) web.AuditHandlerDependencies {
	if s.auditReports == nil || s.auditRunner == nil {
		return web.AuditHandlerDependencies{}
	}
	runs := web.NewAuditRunCoordinator(ctx, web.AuditRunCoordinatorOptions{
		Runner: func(runCtx context.Context, profile audit.Profile) (audit.Report, error) {
			return s.auditRunner(runCtx, profile, s.auditSince)
		},
		Reports:          s.auditReports,
		SyncInterval:     s.syncInterval,
		StandardInterval: s.standardInterval,
	})
	return web.AuditHandlerDependencies{Reports: s.auditReports, Runs: runs, SyncInterval: s.syncInterval, StandardInterval: s.standardInterval}
}

func (s remoteSchedulers) Stop() {
	if s.syncAll != nil {
		s.syncAll.Stop()
	}
	if s.audit != nil {
		s.audit.Stop()
	}
	if s.sqliteArchive != nil {
		s.sqliteArchive.Stop()
	}
}

func auditMetaForInvocation(root *rootOptions) auditConfigMeta {
	if root != nil && strings.TrimSpace(root.configFile) != "" {
		return auditConfigMeta{Layout: "explicit_config", Source: "flag"}
	}
	if root != nil && strings.TrimSpace(root.root) != "" {
		return auditConfigMeta{Layout: "explicit_root", Source: "flag"}
	}
	if strings.TrimSpace(os.Getenv(configFileEnvVar)) != "" {
		return auditConfigMeta{Layout: "explicit_config", Source: "environment"}
	}
	if strings.TrimSpace(os.Getenv(rootEnvVar)) != "" {
		return auditConfigMeta{Layout: "explicit_root", Source: "environment"}
	}
	return auditConfigMeta{Layout: "xdg", Source: "default"}
}

type serveRemoteFlags struct {
	webEnabled         bool
	mcpEnabled         bool
	mcpPath            string
	hostname           string
	stateDir           string
	listen             string
	tlsEnabled         bool
	funnelEnabled      bool
	startupTimeout     time.Duration
	authKey            string
	authKeyRef         string
	allowSecretCommand bool
	advertiseTags      []string
	controlURL         string
	verbose            bool
}

func applyServeRemoteFlagOverrides(cmd *cobra.Command, dataDir string, opts *remote.Options, flags serveRemoteFlags) {
	changed := cmd.Flags().Changed
	if changed("web") {
		opts.Web = flags.webEnabled
	}
	if changed("mcp") {
		opts.MCP = flags.mcpEnabled
	}
	if changed("mcp-path") {
		opts.MCPPath = flags.mcpPath
	}
	if changed("tsnet-hostname") {
		opts.Hostname = flags.hostname
		if !changed("tsnet-state-dir") {
			opts.StateDir = filepath.Join(dataDir, "tsnet", opts.Hostname)
		}
	}
	if changed("tsnet-state-dir") {
		opts.StateDir = flags.stateDir
	}
	if changed("tsnet-listen") {
		opts.Listen = flags.listen
	}
	if changed("tsnet-tls") {
		opts.TLS = flags.tlsEnabled
		if !changed("tsnet-listen") {
			opts.Listen = ""
		}
	}
	if changed("tsnet-funnel") {
		opts.Funnel = flags.funnelEnabled
		if !changed("tsnet-listen") {
			opts.Listen = ""
		}
	}
	if changed("tsnet-startup-timeout") {
		opts.StartupTimeout = flags.startupTimeout
	}
	if changed("tsnet-auth-key") {
		opts.AuthKey = flags.authKey
	}
	if changed("tsnet-auth-key-ref") {
		opts.AuthKeyRef = flags.authKeyRef
	}
	if changed("tsnet-allow-secret-command") {
		opts.AllowSecretCommand = flags.allowSecretCommand
	}
	if changed("tsnet-advertise-tags") {
		opts.AdvertiseTags = flags.advertiseTags
	}
	if changed("tsnet-control-url") {
		opts.ControlURL = flags.controlURL
	}
	if changed("tsnet-verbose") {
		opts.Verbose = flags.verbose
	}
	finalizeRemoteServeDefaults(dataDir, opts)
}

func finalizeRemoteServeDefaults(dataDir string, opts *remote.Options) {
	if opts.StateDir == "" {
		opts.StateDir = filepath.Join(dataDir, "tsnet", opts.Hostname)
	}
	if opts.Listen == "" {
		if opts.TLS || opts.Funnel {
			opts.Listen = ":443"
		} else {
			opts.Listen = ":80"
		}
	}
}
