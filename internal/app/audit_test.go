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
	"github.com/darron/dbrain/internal/store"
)

var fixedAuditTime = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

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

func TestAuditCLIRejectsDeepAsBootstrapExitThreeWithoutReport(t *testing.T) {
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
	if out.Len() != 0 {
		t.Fatalf("unexpected report: %s", out.String())
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

func TestAuditPipelineEvidencePreservesByKindRows(t *testing.T) {
	got := auditPipelineEvidence([]store.PipelineStageRow{
		{Kind: "all", Total: 3, Current: 2, Pending: 1, PartitionValid: true},
		{Kind: "item", Total: 2, Current: 1, Pending: 1, PartitionValid: true},
		{Kind: "source", Total: 1, Current: 1, PartitionValid: true},
	})
	if len(got.ByKind) != 2 || got.ByKind[0].Kind != "item" || got.ByKind[1].Kind != "source" {
		t.Fatalf("by kind = %#v", got.ByKind)
	}
}

func TestAuditNeedsSnapshotHonorsExactCheckScope(t *testing.T) {
	if auditNeedsSnapshot(audit.Request{CheckIDs: []audit.CheckID{audit.CheckDurabilityOKFFreshness}}) {
		t.Fatal("OKF-only scope must not open an unrelated database snapshot")
	}
	if !auditNeedsSnapshot(audit.Request{CheckIDs: []audit.CheckID{audit.CheckDurabilityMediaLocalCoverage}}) {
		t.Fatal("media-local scope requires the database snapshot")
	}
}
