package safaritabs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

type cancelSafariAfterFirstRead struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelSafariAfterFirstRead) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, fmt.Errorf("reader was called after cancellation")
	}
	copy(p, "first chunk")
	r.cancel()
	return len("first chunk"), nil
}

func TestSnapshotCopyReaderStopsBetweenChunksOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelSafariAfterFirstRead{cancel: cancel}
	var dst bytes.Buffer
	_, err := copyReaderContext(ctx, &dst, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copyReaderContext error = %v, want canceled", err)
	}
	if reader.reads != 1 || dst.String() != "first chunk" {
		t.Fatalf("copy state reads=%d body=%q", reader.reads, dst.String())
	}
}

func TestCreateSnapshotContextChecksCancellationBeforeSourceAccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := testConfig(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := createSnapshotContext(ctx, cfg, Options{DBPath: filepath.Join(root, "missing.sqlite")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("createSnapshotContext error = %v, want canceled", err)
	}
}

func TestRunImportsDeviceTabsAsItems(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := testConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	cloudTabsPath := filepath.Join(root, "CloudTabs.db")
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	lastViewed := now.Add(-48 * time.Hour)
	createTestCloudTabsDB(t, cloudTabsPath, []testDevice{
		{
			UUID: "device-iphone",
			Name: "phone",
			Type: "com.apple.iphone",
			Tabs: []testTab{
				{UUID: "tab-one", Title: "Example One", URL: "https://example.com/one", LastViewed: lastViewed},
				{UUID: "tab-two", Title: "Example Two", URL: "https://example.com/two", LastViewed: lastViewed.Add(-time.Hour)},
				{UUID: "tab-x", Title: "X Status", URL: "https://twitter.com/example/status/123", LastViewed: lastViewed.Add(-2 * time.Hour)},
			},
		},
		{
			UUID: "device-mac",
			Name: "mac",
			Type: "com.apple.macbookpro",
			Tabs: []testTab{
				{UUID: "tab-three", Title: "Wrong Device", URL: "https://example.com/three", LastViewed: lastViewed},
			},
		},
	})

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		_ = st.Close()
	}()

	stats, err := Run(ctx, cfg, st, Options{
		DBPath: cloudTabsPath,
		Device: "phone",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stats.TabsSeen != 3 || stats.TabsMatched != 3 || stats.TabsImported != 3 || stats.TabsCreated != 3 || stats.LinksFound != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	item, err := st.GetItem(ctx, "safari-tab:device-iphone:tab-one")
	if err != nil {
		t.Fatalf("get imported item: %v", err)
	}
	if item.SourceType != sourceType || item.CanonicalURL != "https://example.com/one" || item.Title != "Example One" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if item.LinksJSON != `["https://example.com/one"]` {
		t.Fatalf("unexpected links json: %s", item.LinksJSON)
	}
	if item.PrimaryDomain != "example.com" {
		t.Fatalf("unexpected primary domain: %s", item.PrimaryDomain)
	}

	items, err := st.ListItemsForLinkDiscovery(ctx, 10, false)
	if err != nil {
		t.Fatalf("list link discovery items: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected safari tabs to be link discovery candidates, got %d", len(items))
	}
}

func TestRunSupportsOlderThanAndDryRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := testConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	cloudTabsPath := filepath.Join(root, "CloudTabs.db")
	now := time.Now().UTC()
	createTestCloudTabsDB(t, cloudTabsPath, []testDevice{
		{
			UUID: "device-iphone",
			Name: "phone",
			Type: "com.apple.iphone",
			Tabs: []testTab{
				{UUID: "old-tab", Title: "Old", URL: "https://example.com/old", LastViewed: now.Add(-14 * 24 * time.Hour)},
				{UUID: "new-tab", Title: "New", URL: "https://example.com/new", LastViewed: now.Add(-2 * time.Hour)},
				{UUID: "bad-tab", Title: "Bad", URL: "about:blank", LastViewed: now.Add(-14 * 24 * time.Hour)},
			},
		},
	})

	stats, err := Run(ctx, cfg, nil, Options{
		DBPath:     cloudTabsPath,
		Device:     "phone",
		OlderThan:  7 * 24 * time.Hour,
		DryRun:     true,
		ShowTitles: true,
	})
	if err != nil {
		t.Fatalf("run dry-run: %v", err)
	}
	if stats.TabsSeen != 3 || stats.TabsMatched != 1 || stats.TabsImported != 0 || stats.TabsSkipped != 2 || stats.LinksFound != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if len(stats.SampleTitles) != 1 || stats.SampleTitles[0] != "Old" {
		t.Fatalf("unexpected sample titles: %#v", stats.SampleTitles)
	}
}

type testDevice struct {
	UUID string
	Name string
	Type string
	Tabs []testTab
}

type testTab struct {
	UUID       string
	Title      string
	URL        string
	LastViewed time.Time
}

func testConfig(root string) config.Config {
	return config.Config{
		RootDir:        root,
		ConfigDir:      root,
		ConfigPath:     filepath.Join(root, "config.yaml"),
		CategoriesPath: filepath.Join(root, "categories.yaml"),
		DataDir:        filepath.Join(root, "data"),
		TempDir:        filepath.Join(root, "tmp"),
		CacheDir:       filepath.Join(root, "cache"),
		LogDir:         filepath.Join(root, "logs"),
		MediaDir:       filepath.Join(root, "vault", "media"),
		VaultDir:       filepath.Join(root, "vault"),
		DBPath:         filepath.Join(root, "data", "brain.db"),
	}
}

func createTestCloudTabsDB(t *testing.T, path string, devices []testDevice) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test cloud tabs db: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	for _, stmt := range []string{
		`CREATE TABLE cloud_tab_devices (
			device_uuid TEXT PRIMARY KEY NOT NULL,
			system_fields BLOB NOT NULL,
			device_name TEXT,
			device_type_identifier TEXT,
			has_duplicate_device_name BOOLEAN DEFAULT 0,
			is_ephemeral_device BOOLEAN DEFAULT 0,
			last_modified REAL NOT NULL
		);`,
		`CREATE TABLE cloud_tabs (
			tab_uuid TEXT PRIMARY KEY NOT NULL,
			system_fields BLOB NOT NULL,
			device_uuid TEXT NOT NULL,
			position BLOB NOT NULL,
			title TEXT,
			url TEXT NOT NULL,
			is_showing_reader BOOLEAN DEFAULT 0,
			is_pinned BOOLEAN DEFAULT 0,
			reader_scroll_position_page_index INTEGER,
			scene_id TEXT,
			last_viewed_time REAL DEFAULT 0
		);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}

	for _, device := range devices {
		if _, err := db.Exec(
			`INSERT INTO cloud_tab_devices (device_uuid, system_fields, device_name, device_type_identifier, last_modified)
			 VALUES (?, x'00', ?, ?, ?)`,
			device.UUID,
			device.Name,
			device.Type,
			cfAbsoluteSeconds(time.Now().UTC()),
		); err != nil {
			t.Fatalf("insert device: %v", err)
		}
		for _, tab := range device.Tabs {
			if _, err := db.Exec(
				`INSERT INTO cloud_tabs (tab_uuid, system_fields, device_uuid, position, title, url, last_viewed_time)
				 VALUES (?, x'00', ?, x'00', ?, ?, ?)`,
				tab.UUID,
				device.UUID,
				tab.Title,
				tab.URL,
				cfAbsoluteSeconds(tab.LastViewed),
			); err != nil {
				t.Fatalf("insert tab: %v", err)
			}
		}
	}
}

func cfAbsoluteSeconds(t time.Time) float64 {
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return t.UTC().Sub(epoch).Seconds()
}
