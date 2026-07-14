package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
)

var fixedAuditTime = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

type deadlineAuditInventory struct{ remaining time.Duration }

func (i *deadlineAuditInventory) ListPage(ctx context.Context, _ string, _ int) (audit.MediaInventoryPage, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return audit.MediaInventoryPage{}, errors.New("missing page deadline")
	}
	i.remaining = time.Until(deadline)
	return audit.MediaInventoryPage{Complete: true}, nil
}

func TestLoadAuditConfigIsNoWriteAndPreservesRootPrecedence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "brain")
	cfg, meta, err := loadAuditConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootDir != root || meta.Layout != "explicit_root" || meta.Source != "flag" {
		t.Fatalf("resolved = %#v %#v", cfg, meta)
	}
	for _, path := range []string{cfg.DataDir, cfg.LogDir, cfg.VaultDir, cfg.TempDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("audit config wrote %s: %v", path, err)
		}
	}
}

func TestMCPAuditDependenciesRunOnlyTheFullLocalFastProfile(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initialized store: %v", err)
	}
	features := audit.Features{
		Layout: "explicit_root", ConfigSource: "flag", ConfigVerified: true,
		Sources: map[audit.Source]bool{}, Stages: map[audit.PipelineStage]bool{},
	}
	deps, err := newMCPAuditServerDependencies(t.Context(), cfg, features, time.Hour, 6*time.Hour)
	if err != nil {
		t.Fatalf("new MCP audit dependencies: %v", err)
	}
	if deps.Audit.RunFast == nil || deps.Audit.Reports == nil {
		t.Fatalf("incomplete MCP audit dependencies: %#v", deps.Audit)
	}
	report, err := deps.Audit.RunFast(t.Context())
	if err != nil {
		t.Fatalf("run MCP fast audit: %v", err)
	}
	if report.Profile != audit.ProfileFast || !report.Scope.WholeSystem || report.Scope.Filtered || len(report.Checks) != 55 {
		t.Fatalf("MCP fast report is not the full exact profile: %#v", report.Scope)
	}
}

func TestLoadAuditConfigHonorsExplicitConfigBeforeEnvironment(t *testing.T) {
	t.Setenv("DBRAIN_ROOT", filepath.Join(t.TempDir(), "env-root"))
	t.Setenv("DBRAIN_CONFIG_FILE", filepath.Join(t.TempDir(), "env-config.yaml"))
	dir := t.TempDir()
	path := filepath.Join(dir, "chosen.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, err := loadAuditConfig("", path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConfigPath != path || meta.Layout != "explicit_config" || meta.Source != "flag" {
		t.Fatalf("resolved = %#v %#v", cfg, meta)
	}
}

func TestAuditBootstrapContextHasTenSecondCeilingAndHonorsLowerParent(t *testing.T) {
	ctx, cancel := auditBootstrapContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
		t.Fatalf("bootstrap deadline = %v ok=%t", deadline, ok)
	}
	parent, parentCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer parentCancel()
	lowered, loweredCancel := auditBootstrapContext(parent)
	defer loweredCancel()
	loweredDeadline, ok := lowered.Deadline()
	if !ok || time.Until(loweredDeadline) > 2*time.Second || time.Until(loweredDeadline) < time.Second {
		t.Fatalf("lowered bootstrap deadline = %v ok=%t", loweredDeadline, ok)
	}
}

func TestAuditCLIReachesDeepBootstrapAndRejectsDeepLimitsForStandard(t *testing.T) {
	cmd := NewRootCommand()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", t.TempDir(), "--no-debug", "audit", "all", "--profile", "deep", "--json"})
	err := cmd.ExecuteContext(context.Background())
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || exit.Silent {
		t.Fatalf("error = %#v", err)
	}
	if errors.Is(err, audit.ErrDeepUnsupported) {
		t.Fatalf("CLI still rejected deep before capability bootstrap: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected report: %s", out.String())
	}

	cmd = NewRootCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", t.TempDir(), "--no-debug", "audit", "all", "--profile", "standard", "--max-archive-bytes", "1024", "--json"})
	err = cmd.ExecuteContext(context.Background())
	if !errors.As(err, &exit) || exit.Code != 3 || !strings.Contains(exit.Error(), "deep only") {
		t.Fatalf("standard deep-limit error = %#v", err)
	}
}

func TestDeepAuditTimeoutAndLimitResolution(t *testing.T) {
	if got := defaultAuditTimeout(audit.ProfileDeep); got != 2*time.Hour {
		t.Fatalf("deep timeout = %s", got)
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte("audit:\n  max_archive_bytes: 1024\n  max_database_bytes: 2048\n  max_temp_bytes: 4096\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtimeenv.LoadConfigSnapshot(t.Context(), path, auditConfigMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := runtimeenv.RegisterConfigSnapshot(root, snapshot, nil)
	defer cleanup()
	limits, err := resolveDeepAuditLimits(root, auditCLIFlags{}, map[string]bool{}, audit.Features{})
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxArchiveBytes != 1024 || limits.MaxDatabaseBytes != 2048 || limits.MaxTempBytes != 4096 {
		t.Fatalf("configured limits = %#v", limits)
	}
	limits, err = resolveDeepAuditLimits(root, auditCLIFlags{maxArchiveBytes: 8192}, map[string]bool{"max-archive-bytes": true}, audit.Features{})
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxArchiveBytes != 8192 {
		t.Fatalf("explicit raised archive limit = %d", limits.MaxArchiveBytes)
	}
	limits, err = resolveDeepAuditLimits(root, auditCLIFlags{}, map[string]bool{}, audit.Features{RemoteRequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if limits.RequestTimeout != 5*time.Second {
		t.Fatalf("resolved deep page timeout = %s", limits.RequestTimeout)
	}
	inventory := &deadlineAuditInventory{}
	deps := audit.Dependencies{
		Clock:    func() time.Time { return fixedAuditTime },
		Features: audit.Features{Layout: "explicit_root", ConfigVerified: true, MediaRemoteEnabled: true},
		Runtime:  audit.RuntimeVersion{ReleaseVersion: "v0.6.0", Commit: "abcdef1", GitStatus: "clean", Platform: "darwin/arm64", SecurityBaselineID: "v0.6.0-security-pass", SecurityBaselineEpoch: 1},
	}
	if _, err := audit.RunDeep(t.Context(), audit.Request{Profile: audit.ProfileDeep, CheckIDs: []audit.CheckID{audit.CheckDurabilityMediaRemoteOnly}}, deps, audit.DeepDependencies{Media: inventory, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	if inventory.remaining <= 0 || inventory.remaining > 5*time.Second || inventory.remaining < 4*time.Second {
		t.Fatalf("deep media page received deadline remaining %s", inventory.remaining)
	}
}

func TestAuditCLIDatabaseOpenFailureIsExitThreeWithoutReport(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--root", t.TempDir(), "--no-debug", "audit", "all", "--json"})
	err := cmd.ExecuteContext(context.Background())
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || exit.Silent {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(out.String(), audit.SchemaV1) {
		t.Fatalf("bootstrap failure emitted report: %s", out.String())
	}
}

func TestAuditCLIFrozenDotEnvOverridesYAMLAtBootstrap(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("audit:\n  timeouts:\n    bootstrap: 2s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DBRAIN_AUDIT_TIMEOUT_BOOTSTRAP=0s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--root", root, "--no-debug", "audit", "all", "--profile", "fast", "--json"})
	err := cmd.ExecuteContext(context.Background())
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || !strings.Contains(exit.Error(), "invalid audit timeout bootstrap") {
		t.Fatalf("error = %#v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("bootstrap failure emitted report: %s", out.String())
	}
}

func TestAuditCLIEmitsCompleteStandardReportBeforeHealthExit(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("scheduler:\n  sync_all:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--root", root, "--no-debug", "audit", "all", "--profile", "standard", "--json"})
	err = cmd.ExecuteContext(context.Background())
	var exit *ExitError
	if !errors.As(err, &exit) || !exit.Silent || exit.Code != 3 {
		t.Fatalf("error = %#v", err)
	}
	var report audit.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}
	if report.Schema != audit.SchemaV1 || len(report.Checks) != 55 || !report.Boundary.DatabaseVerified {
		t.Fatalf("report boundary/checks = %#v checks=%d", report.Boundary, len(report.Checks))
	}

	cmd = NewRootCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--root", root, "--no-debug", "audit", "imports", "--json"})
	_ = cmd.ExecuteContext(context.Background())
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode imports report: %v", err)
	}
	if len(report.Scope.Categories) != 1 || report.Scope.Categories[0] != audit.CategoryImports {
		t.Fatalf("imports scope = %#v", report.Scope)
	}
	for _, check := range report.Checks {
		if check.Category != audit.CategoryImports {
			t.Fatalf("imports report included %s category check %s", check.Category, check.ID)
		}
	}
}

func TestAuditCLIDeepSyntheticNoRemoteSmoke(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte("scheduler:\n  sync_all:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--root", root, "--no-debug", "audit", "all", "--profile", "deep", "--max-archive-bytes", "1024", "--json"})
	err = cmd.ExecuteContext(t.Context())
	var exit *ExitError
	if err != nil && (!errors.As(err, &exit) || !exit.Silent) {
		t.Fatalf("deep smoke error = %v", err)
	}
	var report audit.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode deep report: %v\n%s", err, out.String())
	}
	if report.Profile != audit.ProfileDeep || len(report.Checks) != 55 || !report.Boundary.DatabaseVerified {
		t.Fatalf("deep report boundary/checks = %#v checks=%d", report.Boundary, len(report.Checks))
	}
	for _, check := range report.Checks {
		if check.ID == audit.CheckDurabilitySQLiteRestore && (check.Status != audit.StatusSkipped || check.SkipReason != audit.SkipFeatureDisabled) {
			t.Fatalf("disabled restore check = %#v", check)
		}
	}
	entries, err := os.ReadDir(cfg.TempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "dbrain-audit-") {
			t.Fatalf("deep smoke left temporary directory %s", entry.Name())
		}
	}
}

func TestLocalAuditWrapperBoundsAndUsesEmptyArrays(t *testing.T) {
	values := make([]string, 0, 105)
	for i := 0; i < 105; i++ {
		values = append(values, fmt.Sprintf("value-%03d", 104-i))
	}
	rowIDs := make([]int64, 105)
	for index := range rowIDs {
		rowIDs[index] = int64(104 - index)
	}
	target := LocalAuditTarget{ConfigPath: "/config", Database: "/database", Vault: "/vault", Metrics: "/metrics", Temporary: "/temporary", Media: "/media", OKFRoot: "/okf"}
	wrapper := newLocalAuditWrapper(audit.NewReport(audit.ProfileStandard, fixedAuditTime), target, map[audit.CheckID]LocalAuditIdentifiers{audit.CheckBoundaryConfig: {RowIDs: rowIDs, SourceKeys: values, CleanupPaths: values}})
	if wrapper.Schema != "dbrain.audit.local.v1" || len(wrapper.LocalDetails.Checks) != 1 {
		t.Fatalf("wrapper = %#v", wrapper)
	}
	item := wrapper.LocalDetails.Checks[0]
	if len(item.RowIDs) != 100 || len(item.SourceKeys) != 100 || len(item.CleanupPaths) != 20 || !item.Truncated {
		t.Fatalf("bounds = %#v", item)
	}
	if item.RowIDs[0] != 0 || item.SourceKeys[0] != "value-000" || item.CleanupPaths[0] != "value-000" || wrapper.LocalTarget != target {
		t.Fatalf("deterministic wrapper = %#v", wrapper)
	}
	data, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != 4 || top["schema"] == nil || top["local_target"] == nil || top["local_details"] == nil || top["report"] == nil {
		t.Fatalf("top-level wire shape = %s", data)
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal(top["local_details"], &details); err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details["checks"] == nil {
		t.Fatalf("local_details wire shape = %s", top["local_details"])
	}
	empty := newLocalAuditWrapper(audit.NewReport(audit.ProfileStandard, fixedAuditTime), LocalAuditTarget{}, nil)
	if empty.LocalDetails.Checks == nil {
		t.Fatal("local detail checks must encode as []")
	}
}

func TestLocalAuditArchiveTargetsExactJSONContainsNoCredentials(t *testing.T) {
	t.Setenv("DBRAIN_ARCHIVE_PROVIDER", "test-s3")
	t.Setenv("DBRAIN_R2_BUCKET", "audit-bucket")
	t.Setenv("DBRAIN_R2_ENDPOINT", "https://objects.example.test")
	t.Setenv("DBRAIN_R2_ACCESS_KEY_ID", "must-not-appear")
	t.Setenv("DBRAIN_R2_SECRET_ACCESS_KEY", "also-must-not-appear")
	media, sqlite := localAuditArchiveTargets(t.TempDir())
	target := LocalAuditTarget{MediaArchive: media, SQLiteArchive: sqlite}
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"media_archive":{"provider":"test-s3","bucket":"audit-bucket","prefix":"media/","origin":"https://objects.example.test:443"},"sqlite_archive":{"provider":"test-s3","bucket":"audit-bucket","prefix":"archive/db","origin":"https://objects.example.test:443"}}`
	if string(data) != want {
		t.Fatalf("local target JSON = %s\nwant = %s", data, want)
	}
	if strings.Contains(string(data), "must-not-appear") {
		t.Fatalf("local target leaked credentials: %s", data)
	}
}

func TestLocalAuditIdentifiersEmitDeterministicEntriesForEveryReportedCheck(t *testing.T) {
	report := audit.NewReport(audit.ProfileStandard, fixedAuditTime)
	report.Checks = []audit.Check{{ID: audit.CheckPipelineOCRPartition}, {ID: audit.CheckBoundaryConfig}}
	wrapper := newLocalAuditWrapper(report, LocalAuditTarget{}, localAuditIdentifiers(t.Context(), report, nil, 30*time.Second, nil))
	if len(wrapper.LocalDetails.Checks) != 2 {
		t.Fatalf("identifier details = %#v", wrapper.LocalDetails)
	}
	if wrapper.LocalDetails.Checks[0].CheckID != audit.CheckBoundaryConfig || wrapper.LocalDetails.Checks[1].CheckID != audit.CheckPipelineOCRPartition {
		t.Fatalf("identifier order = %#v", wrapper.LocalDetails.Checks)
	}
	for _, detail := range wrapper.LocalDetails.Checks {
		if detail.RowIDs == nil || detail.SourceKeys == nil || detail.CleanupPaths == nil {
			t.Fatalf("identifier arrays must be non-null: %#v", detail)
		}
	}
}

func TestLocalAuditCleanupPathAppearsOnlyInLocalWrapper(t *testing.T) {
	report := audit.NewReport(audit.ProfileDeep, fixedAuditTime)
	report.Checks = []audit.Check{{ID: audit.CheckDurabilitySQLiteRestore, Status: audit.StatusUnknown}}
	path := filepath.Join(t.TempDir(), "dbrain-audit-generated")
	values := localAuditIdentifiers(t.Context(), report, nil, 30*time.Second, map[audit.CheckID][]string{
		audit.CheckDurabilitySQLiteRestore: {path},
	})
	wrapper := newLocalAuditWrapper(report, LocalAuditTarget{}, values)
	wrapperJSON, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wrapperJSON, []byte(path)) {
		t.Fatalf("local wrapper omitted cleanup path: %s", wrapperJSON)
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reportJSON, []byte(path)) {
		t.Fatalf("portable report leaked cleanup path: %s", reportJSON)
	}
}

func TestLocalAuditIdentifiersReadConcreteNonPassRowsFromSnapshot(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writer, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	item, err := writer.UpsertItem(t.Context(), model.Item{
		SourceKey: "x:local-identifier", SourceType: "x_bookmark", ExternalID: "local-identifier",
		CanonicalURL: "https://x.com/example/status/local-identifier", Title: "pending",
		ContentHash: "local-identifier", LinksJSON: "[]", RawJSON: `{}`, NotePath: "items/x/local.md",
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	snapshot, err := reader.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()

	report := audit.NewReport(audit.ProfileStandard, fixedAuditTime)
	report.Checks = []audit.Check{
		{ID: audit.CheckPipelineHydrationPartition, Status: audit.StatusWarn},
		{ID: audit.CheckPipelineHydrationPendingAge, Status: audit.StatusPass},
		{ID: audit.CheckBoundaryConfig, Status: audit.StatusFail},
	}
	values := localAuditIdentifiers(t.Context(), report, snapshot, 30*time.Second, nil)
	got := values[audit.CheckPipelineHydrationPartition]
	if len(got.RowIDs) != 1 || got.RowIDs[0] != item.ItemID || len(got.SourceKeys) != 1 || got.SourceKeys[0] != "x:local-identifier" {
		t.Fatalf("hydration identifiers = %#v", got)
	}
	if pass := values[audit.CheckPipelineHydrationPendingAge]; len(pass.RowIDs) != 0 || len(pass.SourceKeys) != 0 {
		t.Fatalf("pass check identifiers = %#v", pass)
	}
	if boundary := values[audit.CheckBoundaryConfig]; len(boundary.RowIDs) != 0 || len(boundary.SourceKeys) != 0 || len(boundary.CleanupPaths) != 0 {
		t.Fatalf("unsupported boundary identifiers = %#v", boundary)
	}
}

func TestResolveAuditFeaturesUsesSchedulerAndSharedSyncPolicy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(`
scheduler:
  sync_all:
    enabled: true
    interval: 2h
    jitter: 5m
    archive_media: true
    okf_export: true
sync_all:
  imports:
    x_bookmarks: true
    github_stars: false
    youtube_watch_later: true
    youtube_liked: false
    feeds: true
    apple_notes: true
    safari_tabs: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, err := loadAuditConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	features, err := resolveAuditFeatures(cfg, meta)
	if err != nil {
		t.Fatal(err)
	}
	if !features.SchedulerEnabled || features.SchedulerInterval != 2*time.Hour || features.SchedulerJitter != 5*time.Minute {
		t.Fatalf("scheduler features = %#v", features)
	}
	if !features.Sources[audit.SourceXBookmarks] || features.Sources[audit.SourceGitHubStars] || !features.Sources[audit.SourceYouTubeWatchLater] || features.Sources[audit.SourceYouTubeLiked] || !features.Sources[audit.SourceAppleNotes] || features.Sources[audit.SourceSafariTabs] {
		t.Fatalf("source features = %#v", features.Sources)
	}
	if !features.MediaLocalEnabled || !features.MediaRemoteEnabled || !features.OKFEnabled {
		t.Fatalf("durability features = %#v", features)
	}
}

func TestResolveAuditFeaturesParsesDownwardTimeoutOverrides(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, []byte(`
audit:
  timeouts:
    bootstrap: 2s
    local_query: 3s
    metrics_or_manifest: 4s
    sqlite_or_okf_integrity: 1m
    remote_request: 5s
    remote_metadata: 1m
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, err := loadAuditConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtimeenv.LoadConfigSnapshot(context.Background(), path, auditConfigMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := runtimeenv.RegisterConfigSnapshot(root, snapshot, nil)
	defer cleanup()
	features, err := resolveAuditFeatures(cfg, meta)
	if err != nil {
		t.Fatal(err)
	}
	for class, want := range map[audit.TimeoutClass]time.Duration{
		audit.TimeoutBootstrap: 2 * time.Second, audit.TimeoutLocalQuery: 3 * time.Second,
		audit.TimeoutMetricsOrManifest: 4 * time.Second, audit.TimeoutSQLiteOrOKFIntegrity: time.Minute,
		audit.TimeoutRemoteMetadata: time.Minute,
	} {
		if got := features.Timeouts[class]; got != want {
			t.Fatalf("timeout %s = %s, want %s", class, got, want)
		}
	}
	if features.RemoteRequestTimeout != 5*time.Second {
		t.Fatalf("remote request timeout = %s", features.RemoteRequestTimeout)
	}

	delete(snapshot["audit"].(map[string]any)["timeouts"].(map[string]any), "local_query")
	snapshot["audit"].(map[string]any)["timeouts"].(map[string]any)["bootstrap"] = "0s"
	if _, err := resolveAuditFeatures(cfg, meta); err == nil {
		t.Fatal("expected non-positive timeout override rejection")
	}
}

func TestAuditPipelineEvidencePreservesByKindRows(t *testing.T) {
	got := auditPipelineEvidence([]store.PipelineStageRow{
		{Kind: "ALL", Total: 3, Current: 2, Pending: 1, PartitionValid: true},
		{Kind: "item", Total: 2, Current: 1, Pending: 1, PartitionValid: true},
		{Kind: "source", Total: 1, Current: 1, PartitionValid: true},
	})
	if len(got.ByKind) != 2 || got.ByKind[0].Kind != "item" || got.ByKind[1].Kind != "source" {
		t.Fatalf("by kind = %#v", got.ByKind)
	}
}

func TestAuditSnapshotAdapterUsesRealStorePipelineAggregates(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.UpsertSource(context.Background(), model.SourceCandidate{
		SourceKey:     "src:audit-real-pipeline",
		CanonicalURL:  "https://example.com/audit-real-pipeline",
		NormalizedURL: "https://example.com/audit-real-pipeline",
		SourceType:    "web",
		Domain:        "example.com",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.BeginAuditReadSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()

	stages, err := (auditSnapshotAdapter{snapshot: snapshot}).Pipeline(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	extraction := stages[audit.PipelineExtraction]
	if extraction.Total != 1 || extraction.Pending != 1 || !extraction.PartitionValid {
		t.Fatalf("real extraction aggregate = %#v", extraction)
	}
	if len(extraction.ByKind) != 1 || extraction.ByKind[0].Kind != "web" || extraction.ByKind[0].Total != 1 {
		t.Fatalf("real extraction by-kind = %#v", extraction.ByKind)
	}
	report, err := audit.Run(context.Background(), audit.Request{Profile: audit.ProfileFast, CheckIDs: []audit.CheckID{audit.CheckPipelineExtractionPartition}}, audit.Dependencies{
		Features: audit.Features{
			Layout: "explicit_root", ConfigSource: "flag", ConfigVerified: true, DatabaseOpenedQueryOnly: true,
			Stages: map[audit.PipelineStage]bool{audit.PipelineExtraction: true}, Sources: map[audit.Source]bool{},
		},
		Store: auditSnapshotAdapter{snapshot: snapshot},
		Runtime: audit.RuntimeVersion{
			ReleaseVersion: "v0.6.0", Commit: "abcdef1", GitStatus: "clean", Platform: "darwin/arm64",
			SecurityBaselineID: "v0.6.0-security-pass", SecurityBaselineEpoch: 1,
		},
		Clock: func() time.Time { return fixedAuditTime },
	})
	if err != nil {
		t.Fatalf("real store audit report: %v", err)
	}
	if len(report.Checks) != 1 || report.Checks[0].Evidence["total"] != 1 {
		t.Fatalf("real store audit report = %#v", report)
	}
}

func TestAuditOKFFastInspectionDoesNotTouchSpecialMarkdownTarget(t *testing.T) {
	dir := t.TempDir()
	manifest, err := json.Marshal(okf.Manifest{
		OKFVersion: okf.OKFVersion,
		Profile:    okf.ProfilePrivate,
		ExportedAt: fixedAuditTime.Format(time.RFC3339),
		Concepts:   []okf.ManifestConcept{{Path: "special.md", Type: "note"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".dbrain-okf-manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "special.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	fast, err := (auditOKFInspector{path: dir}).Inspect(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !fast.ManifestValid || fast.DocumentCount != 1 || fast.TraversalComplete {
		t.Fatalf("manifest-only inspection = %#v", fast)
	}
	full, err := (auditOKFInspector{path: dir}).Inspect(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if full.ManifestValid || full.ValidationErrorCount == 0 {
		t.Fatalf("full inspection did not inspect special target = %#v", full)
	}
}

func TestAuditNeedsSnapshotHonorsExactCheckScope(t *testing.T) {
	if auditNeedsSnapshot(audit.Request{CheckIDs: []audit.CheckID{audit.CheckDurabilityOKFFreshness}}) {
		t.Fatal("OKF-only scope must not open an unrelated database snapshot")
	}
	if !auditNeedsSnapshot(audit.Request{CheckIDs: []audit.CheckID{audit.CheckDurabilityMediaLocalCoverage}}) {
		t.Fatal("media-local scope requires the database snapshot")
	}
	if !auditNeedsSnapshot(audit.Request{CheckIDs: []audit.CheckID{audit.CheckBoundaryDatabase}}) {
		t.Fatal("database-boundary scope requires the database snapshot")
	}
	if !auditNeedsSnapshot(audit.Request{Categories: []audit.Category{audit.CategoryBoundary}}) {
		t.Fatal("boundary-only scope requires the database snapshot")
	}
	if !auditNeedsSnapshot(audit.Request{Sources: []audit.Source{audit.SourceXBookmarks}}) {
		t.Fatal("source-only mixed scope retains source-less database checks")
	}
}
