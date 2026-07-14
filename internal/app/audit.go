package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/sqlitearchive"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
	"github.com/spf13/cobra"
)

type auditCLIFlags struct {
	profile            string
	since              string
	json               bool
	timeout            string
	includeIdentifiers bool
	expectCommit       string
}

const auditConfigMaxBytes int64 = 1 << 20

func newAuditCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "audit", Short: "Inspect production health without modifying the target", RunE: helpCommand}
	cmd.Annotations = map[string]string{skipKeepAwakeAnnotation: "true"}
	cmd.AddCommand(
		newAuditRunCommand(root, "all", nil),
		newAuditRunCommand(root, "imports", []audit.Category{audit.CategoryImports}),
		newAuditRunCommand(root, "pipeline", []audit.Category{audit.CategoryPipeline}),
		newAuditRunCommand(root, "durability", []audit.Category{audit.CategoryDurability}),
	)
	return cmd
}

func newAuditRunCommand(root *rootOptions, name string, categories []audit.Category) *cobra.Command {
	flags := &auditCLIFlags{}
	cmd := &cobra.Command{
		Use:   name,
		Short: "Run the " + name + " health audit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuditCLI(cmd, root, flags, categories)
		},
	}
	cmd.Annotations = map[string]string{skipKeepAwakeAnnotation: "true"}
	cmd.Flags().StringVar(&flags.profile, "profile", string(audit.ProfileStandard), "Audit profile: fast, standard, or deep")
	cmd.Flags().StringVar(&flags.since, "since", "7d", "Metrics and arrival-history window")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Emit the stable JSON audit report")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "Bound the complete audit run")
	cmd.Flags().BoolVar(&flags.includeIdentifiers, "include-identifiers", false, "Include bounded internal identifiers in the local CLI wrapper")
	cmd.Flags().StringVar(&flags.expectCommit, "expect-commit", "", "Require the running binary to report this commit")
	return cmd
}

func runAuditCLI(cmd *cobra.Command, root *rootOptions, flags *auditCLIFlags, categories []audit.Category) error {
	profile := audit.Profile(strings.TrimSpace(flags.profile))
	if profile == audit.ProfileDeep {
		return &ExitError{Code: 3, Err: audit.ErrDeepUnsupported}
	}
	if !profile.Valid() {
		return &ExitError{Code: 3, Err: fmt.Errorf("invalid audit profile %q", profile)}
	}
	since, err := audit.ParseDuration(flags.since)
	if err != nil || since <= 0 {
		if err == nil {
			err = fmt.Errorf("duration must be positive")
		}
		return &ExitError{Code: 3, Err: fmt.Errorf("invalid --since: %w", err)}
	}
	timeout := defaultAuditTimeout(profile)
	if strings.TrimSpace(flags.timeout) != "" {
		requested, parseErr := audit.ParseDuration(flags.timeout)
		err = parseErr
		timeout = requested
		if err != nil || timeout <= 0 {
			if err == nil {
				err = fmt.Errorf("duration must be positive")
			}
			return &ExitError{Code: 3, Err: fmt.Errorf("invalid --timeout: %w", err)}
		}
		if ceiling := defaultAuditTimeout(profile); timeout > ceiling {
			timeout = ceiling
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	bootstrapStarted := time.Now()
	bootstrapCtx, bootstrapCancel := auditBootstrapContext(ctx)
	defer bootstrapCancel()
	cfg, meta, err := loadAuditConfigContext(bootstrapCtx, root.root, root.configFile)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("resolve audit target: %w", err)}
	}
	configSnapshot, err := runtimeenv.LoadConfigSnapshot(bootstrapCtx, cfg.ConfigPath, auditConfigMaxBytes)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return &ExitError{Code: 3, Err: fmt.Errorf("read bounded audit config: %w", err)}
		}
		configSnapshot = map[string]any{}
	}
	dotenvSnapshot, err := runtimeenv.LoadDotEnvSnapshot(bootstrapCtx, cfg.RootDir, auditConfigMaxBytes)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("read bounded audit dotenv: %w", err)}
	}
	cleanupConfigSnapshot := runtimeenv.RegisterConfigSnapshot(cfg.RootDir, configSnapshot, dotenvSnapshot)
	defer cleanupConfigSnapshot()
	timeoutOverrides, _, err := resolveAuditTimeoutOverrides(cfg.RootDir)
	if err != nil {
		return &ExitError{Code: 3, Err: err}
	}
	bootstrapDeadline := bootstrapStarted.Add(timeoutForAuditBootstrap(timeoutOverrides))
	effectiveBootstrapCtx, effectiveBootstrapCancel := context.WithDeadline(bootstrapCtx, bootstrapDeadline)
	defer effectiveBootstrapCancel()
	if err := effectiveBootstrapCtx.Err(); err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("audit bootstrap timeout: %w", err)}
	}
	features, err := resolveAuditFeatures(cfg, meta)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("resolve audit features: %w", err)}
	}
	if err := effectiveBootstrapCtx.Err(); err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("audit bootstrap timeout: %w", err)}
	}
	req := audit.Request{Profile: profile, Since: since, Categories: categories, ExpectCommit: strings.TrimSpace(flags.expectCommit)}
	var st *store.Store
	var snapshot *store.AuditReadSnapshot
	if auditNeedsSnapshot(req) {
		st, err = store.OpenReadOnlyContext(effectiveBootstrapCtx, cfg.DBPath)
		if err != nil {
			return &ExitError{Code: 3, Err: fmt.Errorf("open audit database read-only: %w", err)}
		}
		defer func() { _ = st.Close() }()
		snapshot, err = st.BeginAuditReadSnapshot(effectiveBootstrapCtx)
		if err != nil {
			return &ExitError{Code: 3, Err: fmt.Errorf("begin audit database snapshot: %w", err)}
		}
		defer func() { _ = snapshot.Close() }()
	}

	deps, err := buildAuditDependencies(ctx, cfg, snapshot, req, features)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("resolve audit capabilities: %w", err)}
	}
	report, err := audit.Run(ctx, req, deps)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("run audit: %w", err)}
	}
	if flags.json {
		if flags.includeIdentifiers {
			target := LocalAuditTarget{ConfigPath: cfg.ConfigPath, Database: cfg.DBPath, Vault: cfg.VaultDir, Temporary: cfg.TempDir, Media: cfg.MediaDir, OKFRoot: cfg.OKFDir}
			target.MediaArchive, target.SQLiteArchive = localAuditArchiveTargets(cfg.RootDir)
			if metricsCfg, resolveErr := metrics.ResolveConfig(cfg.RootDir, cfg.LogDir); resolveErr == nil {
				target.Metrics = metricsCfg.Path
			}
			err = writeJSON(cmd.OutOrStdout(), newLocalAuditWrapper(report, target,
				localAuditIdentifiers(ctx, report, snapshot, localAuditIdentifierTimeout(profile, features.Timeouts))))
		} else {
			err = writeJSON(cmd.OutOrStdout(), report)
		}
	} else {
		err = writeAuditHuman(cmd, report, cfg.ConfigPath, cfg.DBPath, since)
	}
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("encode audit report: %w", err)}
	}
	code := audit.ExitCode(report)
	if code != 0 {
		return &ExitError{Code: code, Err: fmt.Errorf("audit status: %s", report.Status), Silent: true}
	}
	return nil
}

func localAuditIdentifiers(ctx context.Context, report audit.Report, snapshot *store.AuditReadSnapshot, timeout time.Duration) map[audit.CheckID]LocalAuditIdentifiers {
	values := make(map[audit.CheckID]LocalAuditIdentifiers, len(report.Checks))
	for _, check := range report.Checks {
		values[check.ID] = LocalAuditIdentifiers{RowIDs: []int64{}, SourceKeys: []string{}, CleanupPaths: []string{}}
		if snapshot == nil || check.Status == audit.StatusPass || check.Status == audit.StatusSkipped {
			continue
		}
		queryCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			queryCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		rows, err := snapshot.LocalIdentifierRows(queryCtx, string(check.ID), 101)
		cancel()
		if err != nil {
			continue
		}
		rowIDs := make([]int64, 0, len(rows))
		sourceKeys := make([]string, 0, len(rows))
		seenRows := make(map[int64]struct{}, len(rows))
		seenKeys := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			if _, ok := seenRows[row.RowID]; !ok {
				seenRows[row.RowID] = struct{}{}
				rowIDs = append(rowIDs, row.RowID)
			}
			key := strings.TrimSpace(row.SourceKey)
			if key == "" {
				continue
			}
			if _, ok := seenKeys[key]; !ok {
				seenKeys[key] = struct{}{}
				sourceKeys = append(sourceKeys, key)
			}
		}
		values[check.ID] = LocalAuditIdentifiers{RowIDs: rowIDs, SourceKeys: sourceKeys, CleanupPaths: []string{}}
	}
	return values
}

func localAuditIdentifierTimeout(profile audit.Profile, overrides map[audit.TimeoutClass]time.Duration) time.Duration {
	ceiling := 30 * time.Second
	if profile == audit.ProfileFast {
		ceiling = 5 * time.Second
	}
	if value := overrides[audit.TimeoutLocalQuery]; value > 0 && value < ceiling {
		return value
	}
	return ceiling
}

func localAuditArchiveTargets(root string) (LocalArchiveTarget, LocalArchiveTarget) {
	provider := strings.TrimSpace(firstNonEmptyEnv(root, "DBRAIN_ARCHIVE_PROVIDER", "DBRAIN_R2_PROVIDER"))
	if provider == "" {
		provider = "cloudflare_r2"
	}
	bucket := strings.TrimSpace(firstNonEmptyEnv(root, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET", "DBRAIN_S3_BUCKET"))
	origin := strings.TrimSpace(firstNonEmptyEnv(root, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT"))
	if origin != "" {
		canonical, err := safehttp.CanonicalOriginEndpoint(origin)
		if err != nil {
			origin = ""
		} else {
			origin = canonical
		}
	}
	return LocalArchiveTarget{Provider: provider, Bucket: bucket, Prefix: "media", Origin: origin}, LocalArchiveTarget{Provider: provider, Bucket: bucket, Prefix: sqlitearchive.DefaultPrefix, Origin: origin}
}

func auditBootstrapContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

func timeoutForAuditBootstrap(overrides map[audit.TimeoutClass]time.Duration) time.Duration {
	ceiling := 10 * time.Second
	if value := overrides[audit.TimeoutBootstrap]; value > 0 && value < ceiling {
		return value
	}
	return ceiling
}

func defaultAuditTimeout(profile audit.Profile) time.Duration {
	if profile == audit.ProfileFast {
		return 30 * time.Second
	}
	return 10 * time.Minute
}

type auditDatabaseInspector struct{ path string }

func (i auditDatabaseInspector) Inspect(ctx context.Context, full bool) (audit.DatabaseInspection, error) {
	value, err := store.InspectDatabaseReadOnly(ctx, i.path, full)
	if err != nil {
		return audit.DatabaseInspection{}, err
	}
	return audit.DatabaseInspection{
		SchemaCompatibility: value.SchemaCompatibility, MigrationCompatibility: value.MigrationCompatibility,
		MissingTableCount: value.MissingTableCount, MissingColumnCount: value.MissingColumnCount,
		QuickCheck: value.QuickCheck, QuickViolationCount: value.QuickViolationCount,
		ForeignKeyViolationCount: value.ForeignKeyViolationCount, UserVersion: value.UserVersion,
		SupportedVersion: value.SupportedVersion, AppliedCount: value.AppliedMigrationCount,
	}, nil
}

func buildAuditDependencies(ctx context.Context, cfg config.Config, snapshot *store.AuditReadSnapshot, req audit.Request, features audit.Features) (audit.Dependencies, error) {
	features.DatabaseOpenedQueryOnly = snapshot != nil
	current := version.Current()
	platform := current.BuildPlatform
	if strings.TrimSpace(platform) == "" || strings.EqualFold(platform, "unknown") {
		platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	deps := audit.Dependencies{
		Features: features,
		Database: auditDatabaseInspector{path: cfg.DBPath},
		Runtime: audit.RuntimeVersion{
			ReleaseVersion: current.ReleaseVersion, Commit: current.Commit, GitStatus: current.GitStatus,
			Platform: platform, SecurityBaselineID: current.SecurityBaselineID, SecurityBaselineEpoch: current.SecurityBaselineEpoch,
		},
	}
	if snapshot != nil {
		deps.Store = auditSnapshotAdapter{snapshot: snapshot}
	}
	if metricsCfg, err := metrics.ResolveConfig(cfg.RootDir, cfg.LogDir); err == nil && metricsCfg.Enabled && strings.TrimSpace(metricsCfg.Path) != "" {
		deps.Metrics = metrics.NewReader(metricsCfg.Path)
	} else if err != nil {
		return audit.Dependencies{}, err
	}
	if features.OKFEnabled {
		deps.OKF = auditOKFInspector{path: cfg.OKFDir}
	}
	remoteAllowed := req.Profile != audit.ProfileFast && auditRequestMayIncludeCategory(req, audit.CategoryDurability)
	backupRequired := features.SQLiteBackupSchedulerEnabled || features.SQLiteBackupAuditRequired
	needMediaClient := remoteAllowed && features.MediaRemoteEnabled && features.SQLiteProviderConfigured && features.SQLiteCredentialConfigured
	needSQLiteClient := remoteAllowed && backupRequired && features.SQLiteProviderConfigured && features.SQLiteCredentialConfigured
	if needMediaClient || needSQLiteClient {
		archiveOpts, resolveErr := archiveRuntimeValues(ctx, cfg.RootDir)
		if resolveErr != nil {
			deps.Features.SQLiteResolutionError = backupRequired
			return deps, nil
		}
		if needMediaClient {
			if inspector, inspectErr := mediaarchive.NewS3Inspector(archiveOpts); inspectErr == nil {
				deps.Media = auditMediaInspector{inspector: inspector}
			}
		}
		if needSQLiteClient {
			inspector, inspectErr := sqlitearchive.NewS3Inspector(sqlitearchive.S3Options{
				Bucket: archiveOpts.Bucket, Endpoint: archiveOpts.Endpoint, Region: archiveOpts.Region,
				AccessKeyID: archiveOpts.AccessKeyID, SecretKey: archiveOpts.SecretKey,
				SessionToken: archiveOpts.SessionToken, PathStyle: true,
			})
			if inspectErr != nil {
				deps.Features.SQLiteResolutionError = true
			} else {
				pageTimeout := features.RemoteRequestTimeout
				if pageTimeout <= 0 || pageTimeout > 30*time.Second {
					pageTimeout = 30 * time.Second
				}
				deps.Archives = auditArchiveLister{inspector: inspector, prefix: sqlitearchive.DefaultPrefix, pageTimeout: pageTimeout}
			}
		}
	}
	return deps, nil
}

func auditNeedsSnapshot(req audit.Request) bool {
	if len(req.CheckIDs) > 0 {
		for _, id := range req.CheckIDs {
			entry, ok := audit.Lookup(id)
			if !ok {
				continue
			}
			if entry.Category == audit.CategoryPipeline || id == audit.CheckBoundaryDatabase || id == audit.CheckDurabilityMediaLocalCoverage || id == audit.CheckDurabilityMediaRemote {
				return true
			}
		}
		return false
	}
	return auditRequestMayIncludeCategory(req, audit.CategoryBoundary) || auditRequestMayIncludeCategory(req, audit.CategoryPipeline) || auditRequestMayIncludeCategory(req, audit.CategoryDurability)
}

func auditRequestMayIncludeCategory(req audit.Request, category audit.Category) bool {
	if len(req.CheckIDs) > 0 {
		for _, id := range req.CheckIDs {
			if entry, ok := audit.Lookup(id); ok && entry.Category == category {
				return true
			}
		}
		return false
	}
	if len(req.Categories) > 0 {
		for _, value := range req.Categories {
			if value == category {
				return true
			}
		}
		return false
	}
	return true
}

func writeAuditHuman(cmd *cobra.Command, report audit.Report, configPath, databasePath string, since time.Duration) error {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Target: config=%s database=%s\nStatus: %s (confidence: %s)\nWindow: %s\n", configPath, databasePath, report.Status, report.Confidence, since); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if check.Status == audit.StatusFail || check.Status == audit.StatusUnknown || check.Status == audit.StatusWarn {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", check.Status, check.ID, check.Summary); err != nil {
				return err
			}
		}
	}
	return nil
}
