package safaritabs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

func TestAuditInventoryUsesOneSnapshotAndMatchesNormalIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "CloudTabs.db")
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	createTestCloudTabsDB(t, dbPath, []testDevice{{
		UUID: "device-phone", Name: "phone", Type: "iphone",
		Tabs: []testTab{
			{UUID: "old-http", URL: "https://example.com/old", LastViewed: now.Add(-8 * 24 * time.Hour)},
			{UUID: "new-http", URL: "https://example.com/new", LastViewed: now.Add(-time.Hour)},
			{UUID: "old-about", URL: "about:blank", LastViewed: now.Add(-8 * 24 * time.Hour)},
		},
	}})

	snapshotDir := filepath.Join(root, "kept-snapshot")
	result, err := newSafariAuditInventory(cfg, Options{
		DBPath: dbPath, SnapshotDir: snapshotDir, Device: "device-phone", OlderThan: 7 * 24 * time.Hour,
	}, func() time.Time { return now }).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, _ := audit.HashUpstreamIdentity(audit.SourceSafariTabs, "safari-tab:device-phone:old-http")
	if !result.Complete || result.PageCount != 1 || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryRequiresUnambiguousConfiguredDevice(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "CloudTabs.db")
	createTestCloudTabsDB(t, dbPath, []testDevice{
		{UUID: "device-one", Name: "private duplicate name", Tabs: []testTab{{UUID: "one", URL: "https://example.com/one"}}},
		{UUID: "device-two", Name: "private duplicate name", Tabs: []testTab{{UUID: "two", URL: "https://example.com/two"}}},
	})

	_, err := NewAuditInventory(cfg, Options{DBPath: dbPath, Device: "private duplicate name"}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 1})
	if !errors.Is(err, audit.ErrInventoryInvalid) || strings.Contains(err.Error(), "private duplicate name") {
		t.Fatalf("ambiguous device error = %v", err)
	}

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath, Device: "device-two"}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 1})
	if err != nil || !result.Complete || len(result.IdentityHashes) != 1 {
		t.Fatalf("UUID inventory = %+v, err=%v", result, err)
	}
}

func TestAuditInventoryCapPlusOneIsIncomplete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "CloudTabs.db")
	createTestCloudTabsDB(t, dbPath, []testDevice{{UUID: "device", Name: "phone", Tabs: []testTab{
		{UUID: "one", URL: "https://example.com/one"},
		{UUID: "two", URL: "https://example.com/two"},
	}}})

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath, Device: "device"}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if !errors.Is(err, audit.ErrInventoryBudget) {
		t.Fatalf("error = %v, want ErrInventoryBudget", err)
	}
	if result.Complete || result.PageCount != 1 || len(result.IdentityHashes) != 1 {
		t.Fatalf("unexpected bounded result: %+v", result)
	}
}

func TestAuditInventoryFiltersAndDeduplicatesBeforeApplyingIdentityCap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "CloudTabs.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE cloud_tab_devices (device_uuid TEXT, device_name TEXT)`,
		`CREATE TABLE cloud_tabs (tab_uuid TEXT, device_uuid TEXT, url TEXT, last_viewed_time REAL)`,
		`INSERT INTO cloud_tab_devices (device_uuid, device_name) VALUES ('device', 'phone')`,
		`INSERT INTO cloud_tabs (tab_uuid, device_uuid, url, last_viewed_time) VALUES ('a-bad', 'device', 'about:blank', 0)`,
		`INSERT INTO cloud_tabs (tab_uuid, device_uuid, url, last_viewed_time) VALUES ('z-good', 'device', 'https://example.com/good', 0)`,
		`INSERT INTO cloud_tabs (tab_uuid, device_uuid, url, last_viewed_time) VALUES ('z-good', 'device', 'https://example.com/good', 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath, Device: "device"}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, _ := audit.HashUpstreamIdentity(audit.SourceSafariTabs, "safari-tab:device:z-good")
	if !result.Complete || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryRejectsInvalidBudgetAndCancellationWithoutLeakingPaths(t *testing.T) {
	t.Parallel()

	secret := "private-safari-owner"
	cfg := testConfig(t.TempDir())
	inventory := NewAuditInventory(cfg, Options{DBPath: filepath.Join(t.TempDir(), secret+".db"), Device: "private-device"})
	if _, err := inventory.Inventory(context.Background(), audit.InventoryBudget{}); !errors.Is(err, audit.ErrInventoryInvalid) || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid-budget error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inventory.Inventory(ctx, audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1}); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), secret) {
		t.Fatalf("canceled error = %v", err)
	}
	if _, err := inventory.Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1}); err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private-device") {
		t.Fatalf("snapshot error = %v", err)
	}
}
