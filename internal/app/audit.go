package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/sqlitearchive"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
	"github.com/darron/dbrain/internal/version"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/youtubeimport"
	"github.com/spf13/cobra"
)

type auditCLIFlags struct {
	profile            string
	since              string
	json               bool
	timeout            string
	includeIdentifiers bool
	expectCommit       string
	maxArchiveBytes    int64
	maxDatabaseBytes   int64
	maxTempBytes       int64
}

const auditConfigMaxBytes int64 = 1 << 20

var newAuditMediaInspector = func(opts mediaarchive.Options) (audit.MediaArchiveInspector, error) {
	inspector, err := mediaarchive.NewS3Inspector(opts)
	if err != nil {
		return nil, err
	}
	return auditMediaInspector{inspector: inspector}, nil
}

func newAuditCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "audit", Short: "Inspect production health without modifying the target", RunE: helpCommand}
	cmd.Annotations = map[string]string{skipKeepAwakeAnnotation: "true"}
	cmd.AddCommand(
		newAuditRunCommand(root, "all", nil),
		newAuditRunCommand(root, "imports", []audit.Category{audit.CategoryImports}),
		newAuditRunCommand(root, "pipeline", []audit.Category{audit.CategoryPipeline}),
		newAuditRunCommand(root, "durability", []audit.Category{audit.CategoryDurability}),
		newAuditSourceCommand(root, "apple-notes", audit.SourceAppleNotes),
		newAuditSourceCommand(root, "safari-tabs", audit.SourceSafariTabs),
		newAuditSourceCommand(root, "x-bookmarks", audit.SourceXBookmarks),
		newAuditSourceCommand(root, "github-stars", audit.SourceGitHubStars),
		newAuditSourceCommand(root, "youtube-liked", audit.SourceYouTubeLiked),
		newAuditSourceCommand(root, "youtube-watch-later", audit.SourceYouTubeWatchLater),
		newAuditSourceCommand(root, "feeds", audit.SourceFeeds),
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
			return runAuditCLI(cmd, root, flags, categories, nil, nil)
		},
	}
	cmd.Annotations = map[string]string{skipKeepAwakeAnnotation: "true"}
	cmd.Flags().StringVar(&flags.profile, "profile", string(audit.ProfileStandard), "Audit profile: fast, standard, or deep")
	cmd.Flags().StringVar(&flags.since, "since", "7d", "Metrics and arrival-history window")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Emit the stable JSON audit report")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "Bound the complete audit run")
	cmd.Flags().BoolVar(&flags.includeIdentifiers, "include-identifiers", false, "Include bounded internal identifiers in the local CLI wrapper")
	cmd.Flags().StringVar(&flags.expectCommit, "expect-commit", "", "Require the running binary to report this commit")
	cmd.Flags().Int64Var(&flags.maxArchiveBytes, "max-archive-bytes", 0, "Deep-only compressed archive byte ceiling")
	cmd.Flags().Int64Var(&flags.maxDatabaseBytes, "max-database-bytes", 0, "Deep-only decompressed database byte ceiling")
	cmd.Flags().Int64Var(&flags.maxTempBytes, "max-temp-bytes", 0, "Deep-only total temporary-space byte ceiling")
	return cmd
}

func newAuditSourceCommand(root *rootOptions, name string, source audit.Source) *cobra.Command {
	flags := &auditCLIFlags{profile: string(audit.ProfileDeep)}
	cmd := &cobra.Command{
		Use:   name,
		Short: "Run bounded upstream parity for " + name,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile := audit.Profile(strings.TrimSpace(flags.profile))
			if cmd.Flags().Changed("profile") && profile != audit.ProfileDeep {
				return &ExitError{Code: 3, Err: fmt.Errorf("audit source command %s requires --profile deep", name)}
			}
			return runAuditCLI(cmd, root, flags, []audit.Category{audit.CategoryImports}, []audit.Source{source}, []audit.Source{source})
		},
	}
	cmd.Annotations = map[string]string{skipKeepAwakeAnnotation: "true"}
	cmd.Flags().StringVar(&flags.profile, "profile", string(audit.ProfileDeep), "Audit profile: deep")
	cmd.Flags().StringVar(&flags.since, "since", "7d", "Metrics and arrival-history window")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Emit the stable JSON audit report")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "Bound the complete audit run")
	cmd.Flags().BoolVar(&flags.includeIdentifiers, "include-identifiers", false, "Include bounded internal identifiers in the local CLI wrapper")
	cmd.Flags().StringVar(&flags.expectCommit, "expect-commit", "", "Require the running binary to report this commit")
	return cmd
}

func runAuditCLI(cmd *cobra.Command, root *rootOptions, flags *auditCLIFlags, categories []audit.Category, sources []audit.Source, sourceOverrides []audit.Source) error {
	profile := audit.Profile(strings.TrimSpace(flags.profile))
	if !profile.Valid() {
		return &ExitError{Code: 3, Err: fmt.Errorf("invalid audit profile %q", profile)}
	}
	flagChanged := func(name string) bool {
		flag := cmd.Flags().Lookup(name)
		return flag != nil && flag.Changed
	}
	deepFlags := map[string]bool{
		"max-archive-bytes":  flagChanged("max-archive-bytes"),
		"max-database-bytes": flagChanged("max-database-bytes"),
		"max-temp-bytes":     flagChanged("max-temp-bytes"),
	}
	if profile != audit.ProfileDeep && (deepFlags["max-archive-bytes"] || deepFlags["max-database-bytes"] || deepFlags["max-temp-bytes"]) {
		return &ExitError{Code: 3, Err: fmt.Errorf("deep byte ceilings are deep only")}
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
	resolvedRuntime, err := resolveAuditRuntime(cfg, meta)
	if err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("resolve audit features: %w", err)}
	}
	features := resolvedRuntime.Features
	if err := effectiveBootstrapCtx.Err(); err != nil {
		return &ExitError{Code: 3, Err: fmt.Errorf("audit bootstrap timeout: %w", err)}
	}
	req := audit.Request{
		Profile: profile, Since: since, Categories: categories, Sources: sources, SourceOverrides: sourceOverrides,
		ExpectCommit: strings.TrimSpace(flags.expectCommit),
	}
	deepLimits := audit.DefaultDeepLimits()
	if profile == audit.ProfileDeep {
		if auditRequestMayIncludeCategory(req, audit.CategoryDurability) {
			deepLimits, err = resolveDeepAuditLimits(cfg.RootDir, *flags, deepFlags, features)
			if err != nil {
				return &ExitError{Code: 3, Err: err}
			}
		}
	}
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
	var report audit.Report
	localCleanupPaths := map[audit.CheckID][]string{}
	if profile == audit.ProfileDeep {
		deepDeps, buildErr := buildDeepAuditDependencies(ctx, cfg, snapshot, req, resolvedRuntime, deepLimits)
		if buildErr != nil {
			return &ExitError{Code: 3, Err: fmt.Errorf("resolve deep audit capabilities: %w", buildErr)}
		}
		deepDeps.RecordCleanupFailure = func(path string) {
			localCleanupPaths[audit.CheckDurabilitySQLiteRestore] = append(localCleanupPaths[audit.CheckDurabilitySQLiteRestore], path)
		}
		report, err = audit.RunDeep(ctx, req, deps, deepDeps)
	} else {
		report, err = audit.Run(ctx, req, deps)
	}
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
				localAuditIdentifiers(ctx, report, snapshot, localAuditIdentifierTimeout(profile, features.Timeouts), localCleanupPaths)))
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

func localAuditIdentifiers(ctx context.Context, report audit.Report, snapshot *store.AuditReadSnapshot, timeout time.Duration, localCleanupPaths map[audit.CheckID][]string) map[audit.CheckID]LocalAuditIdentifiers {
	values := make(map[audit.CheckID]LocalAuditIdentifiers, len(report.Checks))
	for _, check := range report.Checks {
		cleanupPaths := append([]string(nil), localCleanupPaths[check.ID]...)
		values[check.ID] = LocalAuditIdentifiers{RowIDs: []int64{}, SourceKeys: []string{}, CleanupPaths: cleanupPaths}
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
		values[check.ID] = LocalAuditIdentifiers{RowIDs: rowIDs, SourceKeys: sourceKeys, CleanupPaths: cleanupPaths}
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
	return LocalArchiveTarget{Provider: provider, Bucket: bucket, Prefix: mediaarchive.DefaultPrefix, Origin: origin}, LocalArchiveTarget{Provider: provider, Bucket: bucket, Prefix: sqlitearchive.DefaultPrefix, Origin: origin}
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
	if profile == audit.ProfileDeep {
		return 2 * time.Hour
	}
	return 10 * time.Minute
}

func resolveDeepAuditLimits(root string, flags auditCLIFlags, explicit map[string]bool, features audit.Features) (audit.DeepLimits, error) {
	limits := audit.DefaultDeepLimits()
	configured := []struct {
		key     string
		target  *int64
		ceiling int64
	}{
		{"DBRAIN_AUDIT_MAX_ARCHIVE_BYTES", &limits.MaxArchiveBytes, audit.DefaultDeepMaxArchiveBytes},
		{"DBRAIN_AUDIT_MAX_DATABASE_BYTES", &limits.MaxDatabaseBytes, audit.DefaultDeepMaxDatabaseBytes},
		{"DBRAIN_AUDIT_MAX_TEMP_BYTES", &limits.MaxTempBytes, audit.DefaultDeepMaxTempBytes},
	}
	for _, item := range configured {
		value := strings.TrimSpace(runtimeenv.FirstNonEmpty(root, item.key))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return audit.DeepLimits{}, fmt.Errorf("invalid %s: must be a positive byte count", item.key)
		}
		if parsed > item.ceiling {
			parsed = item.ceiling
		}
		*item.target = parsed
	}
	requested := []struct {
		name   string
		value  int64
		target *int64
	}{
		{"max-archive-bytes", flags.maxArchiveBytes, &limits.MaxArchiveBytes},
		{"max-database-bytes", flags.maxDatabaseBytes, &limits.MaxDatabaseBytes},
		{"max-temp-bytes", flags.maxTempBytes, &limits.MaxTempBytes},
	}
	for _, item := range requested {
		if !explicit[item.name] {
			continue
		}
		if item.value <= 0 {
			return audit.DeepLimits{}, fmt.Errorf("--%s must be positive", item.name)
		}
		*item.target = item.value
	}
	if timeout := features.RemoteRequestTimeout; timeout > 0 && timeout < limits.RequestTimeout {
		limits.RequestTimeout = timeout
	}
	return limits, nil
}

type auditDatabaseInspector struct{ path string }

type auditFeedInventory struct {
	snapshot            *store.AuditReadSnapshot
	userAgent           string
	allowPrivateNetwork bool
}

func (i auditFeedInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	if i.snapshot == nil {
		return audit.InventoryResult{}, fmt.Errorf("%w: feed audit snapshot unavailable", audit.ErrInventoryInvalid)
	}
	feeds, err := i.snapshot.ListEnabledFeedsForAudit(ctx)
	if err != nil {
		return audit.InventoryResult{}, err
	}
	return feedimport.NewAuditInventory(feeds, i.userAgent, i.allowPrivateNetwork).Inventory(ctx, budget)
}

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
			if needMediaClient {
				deps.MediaErrorCode = audit.ErrorCredentialResolution
			}
			return deps, nil
		}
		if needMediaClient {
			inspector, inspectErr := newAuditMediaInspector(archiveOpts)
			if inspectErr != nil {
				deps.MediaErrorCode = audit.ErrorConfiguration
			} else {
				deps.Media = inspector
				deps.MediaErrorCode = ""
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
				pageLimit := 100
				if req.Profile == audit.ProfileDeep {
					pageLimit = audit.DeepMaxPages
				}
				deps.Archives = auditArchiveLister{inspector: inspector, prefix: sqlitearchive.DefaultPrefix, pageLimit: pageLimit, pageTimeout: pageTimeout}
			}
		}
	}
	return deps, nil
}

func buildDeepAuditDependencies(ctx context.Context, cfg config.Config, snapshot *store.AuditReadSnapshot, req audit.Request, resolved auditRuntimeConfig, limits audit.DeepLimits) (audit.DeepDependencies, error) {
	features := resolved.Features
	deep := audit.DeepDependencies{
		Limits: limits, VerifyArchive: auditArchiveVerifier{},
		NewTemp:  func() (*vaultfs.PrivateTemp, error) { return vaultfs.NewPrivateTemp(cfg.TempDir) },
		Upstream: audit.UpstreamInventories{},
	}
	if auditSourceSelected(req, features, audit.SourceAppleNotes, audit.CheckUpstreamAppleNotesParity) {
		deep.Upstream[audit.SourceAppleNotes] = applenotes.NewAuditInventory(cfg, applenotes.Options{
			DBPath:          resolved.Flags.appleNotesDBPath,
			ExcludeFolders:  append([]string(nil), resolved.Flags.appleNotesExcludeFolders...),
			ExcludeAccounts: append([]string(nil), resolved.Flags.appleNotesExcludeAccounts...),
			ExcludeShared:   resolved.Flags.appleNotesExcludeShared,
		})
	}
	if auditSourceSelected(req, features, audit.SourceSafariTabs, audit.CheckUpstreamSafariTabsParity) {
		deep.Upstream[audit.SourceSafariTabs] = safaritabs.NewAuditInventory(cfg, safaritabs.Options{
			DBPath: resolved.Flags.safariTabsDBPath, Device: resolved.Flags.safariTabsDevice,
			OlderThan: resolved.Flags.safariTabsOlderThan,
		})
	}
	if auditSourceSelected(req, features, audit.SourceXBookmarks, audit.CheckUpstreamXBookmarksParity) {
		deep.Upstream[audit.SourceXBookmarks] = xapi.NewBookmarkAuditInventory(xapi.BookmarkOptions{
			Browser: resolved.Flags.browser, Profile: resolved.Flags.profile, Timeout: resolved.Flags.xTimeout,
		})
	}
	if auditSourceSelected(req, features, audit.SourceGitHubStars, audit.CheckUpstreamGitHubStarsParity) {
		deep.Upstream[audit.SourceGitHubStars] = githubimport.NewAuditInventory(
			cfg.RootDir,
			runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_USER_AGENT"),
		)
	}
	if auditSourceSelected(req, features, audit.SourceYouTubeLiked, audit.CheckUpstreamYouTubeLikedParity) {
		deep.Upstream[audit.SourceYouTubeLiked] = youtubeimport.NewLikedAuditInventory(resolved.Flags.browser, resolved.Flags.profile)
	}
	if auditSourceSelected(req, features, audit.SourceYouTubeWatchLater, audit.CheckUpstreamYouTubeWatchLaterParity) {
		deep.Upstream[audit.SourceYouTubeWatchLater] = youtubeimport.NewWatchLaterAuditInventory(resolved.Flags.browser, resolved.Flags.profile)
	}
	if auditSourceSelected(req, features, audit.SourceFeeds, audit.CheckUpstreamFeedsParity) {
		deep.Upstream[audit.SourceFeeds] = auditFeedInventory{
			snapshot:            snapshot,
			userAgent:           runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_USER_AGENT"),
			allowPrivateNetwork: feedAllowPrivateNetworkFromRuntime(cfg.RootDir),
		}
	}
	needMedia := (auditRequestSelectsCheck(req, audit.CheckDurabilityMediaRemote) || auditRequestSelectsCheck(req, audit.CheckDurabilityMediaRemoteOnly)) && features.MediaRemoteEnabled && features.SQLiteProviderConfigured && features.SQLiteCredentialConfigured
	needArchive := auditRequestSelectsCheck(req, audit.CheckDurabilitySQLiteRestore) && (features.SQLiteBackupSchedulerEnabled || features.SQLiteBackupAuditRequired) && features.SQLiteProviderConfigured && features.SQLiteCredentialConfigured
	if !needMedia && !needArchive {
		return deep, nil
	}
	archiveOpts, err := archiveRuntimeValues(ctx, cfg.RootDir)
	if err != nil {
		return deep, nil
	}
	archiveOpts.ConnectTimeout = 10 * time.Second
	archiveOpts.TLSHandshakeTimeout = 10 * time.Second
	archiveOpts.ResponseHeaderTimeout = 30 * time.Second
	if needMedia {
		inventory, inventoryErr := mediaarchive.NewS3Inventory(archiveOpts)
		if inventoryErr == nil {
			// The deep runner cannot choose or widen the prefix; this adapter is
			// permanently confined to the media archival namespace.
			deep.Media = auditMediaInventory{inventory: inventory, prefix: mediaarchive.DefaultPrefix}
		}
	}
	if needArchive {
		reader, readerErr := sqlitearchive.NewS3Reader(sqlitearchive.S3Options{
			Bucket: archiveOpts.Bucket, Endpoint: archiveOpts.Endpoint, Region: archiveOpts.Region,
			AccessKeyID: archiveOpts.AccessKeyID, SecretKey: archiveOpts.SecretKey,
			SessionToken: archiveOpts.SessionToken, PathStyle: true,
			ConnectTimeout: 10 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		})
		if readerErr == nil {
			deep.Archives = reader
		}
	}
	return deep, nil
}

func auditSourceSelected(req audit.Request, features audit.Features, source audit.Source, checkID audit.CheckID) bool {
	if !auditRequestSelectsCheck(req, checkID) {
		return false
	}
	for _, override := range req.SourceOverrides {
		if override == source {
			return true
		}
	}
	return features.Sources[source]
}

func auditNeedsSnapshot(req audit.Request) bool {
	if len(req.CheckIDs) > 0 {
		for _, id := range req.CheckIDs {
			entry, ok := audit.Lookup(id)
			if !ok {
				continue
			}
			if entry.Category == audit.CategoryPipeline || id == audit.CheckBoundaryDatabase || id == audit.CheckDurabilityMediaLocalCoverage || id == audit.CheckDurabilityMediaRemote || (req.Profile == audit.ProfileDeep && id == audit.CheckDurabilityMediaRemoteOnly) || (req.Profile == audit.ProfileDeep && strings.HasPrefix(string(id), "upstream.")) {
				return true
			}
		}
		return false
	}
	return auditRequestMayIncludeCategory(req, audit.CategoryBoundary) || auditRequestMayIncludeCategory(req, audit.CategoryPipeline) || auditRequestMayIncludeCategory(req, audit.CategoryDurability) || (req.Profile == audit.ProfileDeep && auditRequestMayIncludeCategory(req, audit.CategoryImports))
}

func auditRequestSelectsCheck(req audit.Request, id audit.CheckID) bool {
	entry, ok := audit.Lookup(id)
	if !ok || !entry.InProfile(req.Profile) {
		return false
	}
	if len(req.CheckIDs) > 0 {
		for _, candidate := range req.CheckIDs {
			if candidate == id {
				return true
			}
		}
		return false
	}
	if len(req.Categories) > 0 {
		selected := false
		for _, category := range req.Categories {
			if category == entry.Category {
				selected = true
				break
			}
		}
		if !selected {
			return false
		}
	}
	if entry.Source != "" && len(req.Sources) > 0 {
		for _, source := range req.Sources {
			if source == entry.Source {
				return true
			}
		}
		return false
	}
	return true
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
