package app

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/metrics"
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
	bootstrapCtx, bootstrapCancel := auditBootstrapContext(ctx)
	defer bootstrapCancel()
	cfg, meta, err := loadAuditConfigContext(bootstrapCtx, root.root, root.configFile)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("resolve audit target: %w", err)}
	}
	req := audit.Request{Profile: profile, Since: since, Categories: categories, ExpectCommit: strings.TrimSpace(flags.expectCommit)}
	var st *store.Store
	var snapshot *store.AuditReadSnapshot
	if auditNeedsSnapshot(req) {
		st, err = store.OpenReadOnlyContext(bootstrapCtx, cfg.DBPath)
		if err != nil {
			return &ExitError{Code: 3, Err: fmt.Errorf("open audit database read-only: %w", err)}
		}
		defer func() { _ = st.Close() }()
		snapshot, err = st.BeginAuditReadSnapshot(bootstrapCtx)
		if err != nil {
			return &ExitError{Code: 3, Err: fmt.Errorf("begin audit database snapshot: %w", err)}
		}
		defer func() { _ = snapshot.Close() }()
	}

	deps, err := buildAuditDependencies(ctx, cfg, meta, snapshot, req)
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
			err = writeJSON(cmd.OutOrStdout(), newLocalAuditWrapper(report, target, localAuditIdentifiers(report)))
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

func localAuditIdentifiers(report audit.Report) map[audit.CheckID]LocalAuditIdentifiers {
	values := make(map[audit.CheckID]LocalAuditIdentifiers, len(report.Checks))
	for _, check := range report.Checks {
		values[check.ID] = LocalAuditIdentifiers{RowIDs: []int64{}, SourceKeys: []string{}, CleanupPaths: []string{}}
	}
	return values
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

func buildAuditDependencies(ctx context.Context, cfg config.Config, meta auditConfigMeta, snapshot *store.AuditReadSnapshot, req audit.Request) (audit.Dependencies, error) {
	features, err := resolveAuditFeatures(cfg, meta)
	if err != nil {
		return audit.Dependencies{}, err
	}
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
				deps.Archives = auditArchiveLister{inspector: inspector, prefix: sqlitearchive.DefaultPrefix}
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
