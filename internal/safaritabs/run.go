package safaritabs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

const sourceType = "safari_tab"

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

type ProgressFunc func(ProgressEvent)

type ProgressEvent struct {
	Phase     string `json:"phase"`
	Index     int    `json:"index,omitempty"`
	Total     int    `json:"total,omitempty"`
	SourceKey string `json:"source_key,omitempty"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	Status    string `json:"status,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Rendered  bool   `json:"rendered,omitempty"`
}

type Stats struct {
	SourceDBPath  string       `json:"source_db_path"`
	Snapshot      SnapshotInfo `json:"snapshot"`
	DevicesSeen   int          `json:"devices_seen"`
	DeviceName    string       `json:"device_name"`
	DeviceUUID    string       `json:"device_uuid"`
	TabsSeen      int          `json:"tabs_seen"`
	TabsMatched   int          `json:"tabs_matched"`
	TabsImported  int          `json:"tabs_imported"`
	TabsCreated   int          `json:"tabs_created"`
	TabsUpdated   int          `json:"tabs_updated"`
	TabsUnchanged int          `json:"tabs_unchanged"`
	TabsRendered  int          `json:"tabs_rendered"`
	TabsSkipped   int          `json:"tabs_skipped"`
	LinksFound    int          `json:"links_found"`
	SampleTitles  []string     `json:"sample_titles,omitempty"`
	DryRun        bool         `json:"dry_run"`
	Applied       bool         `json:"applied"`
	Errors        int          `json:"errors"`
}

type Device struct {
	UUID                string    `json:"uuid"`
	Name                string    `json:"name"`
	TypeIdentifier      string    `json:"type_identifier"`
	HasDuplicateName    bool      `json:"has_duplicate_name"`
	IsEphemeral         bool      `json:"is_ephemeral"`
	LastModified        time.Time `json:"last_modified"`
	TabCount            int       `json:"tab_count"`
	OldestTabLastViewed time.Time `json:"oldest_tab_last_viewed,omitempty"`
	NewestTabLastViewed time.Time `json:"newest_tab_last_viewed,omitempty"`
}

type Tab struct {
	UUID            string    `json:"uuid"`
	DeviceUUID      string    `json:"device_uuid"`
	DeviceName      string    `json:"device_name"`
	DeviceType      string    `json:"device_type"`
	Title           string    `json:"title"`
	URL             string    `json:"url"`
	IsShowingReader bool      `json:"is_showing_reader"`
	IsPinned        bool      `json:"is_pinned"`
	SceneID         string    `json:"scene_id,omitempty"`
	LastViewed      time.Time `json:"last_viewed,omitempty"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if strings.TrimSpace(opts.Device) == "" {
		return Stats{}, fmt.Errorf("safari tab import requires --device; run \"dbrain import safari-tabs devices\" to list available devices")
	}
	if !opts.DryRun && st == nil {
		return Stats{}, fmt.Errorf("store is required for Safari tab import")
	}

	devices, snapshot, err := ListDevices(ctx, cfg, opts)
	if err != nil {
		return Stats{}, err
	}
	tabs, matchedDevice, snapshot, err := readTabs(ctx, cfg, opts, devices)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		SourceDBPath: snapshot.SourceDBPath,
		Snapshot:     snapshot,
		DevicesSeen:  len(devices),
		DeviceName:   matchedDevice.Name,
		DeviceUUID:   matchedDevice.UUID,
		TabsSeen:     len(tabs),
		DryRun:       opts.DryRun,
		Applied:      !opts.DryRun,
	}
	emitProgress(opts, ProgressEvent{Phase: "loaded", Total: len(tabs)})

	now := time.Now().UTC()
	cutoff := time.Time{}
	if opts.OlderThan > 0 {
		cutoff = now.Add(-opts.OlderThan)
	}
	processed := 0
	for index, tab := range tabs {
		event := ProgressEvent{
			Index: index + 1,
			Total: len(tabs),
			Title: tab.Title,
			URL:   tab.URL,
		}
		if !cutoff.IsZero() && !tab.LastViewed.IsZero() && tab.LastViewed.After(cutoff) {
			stats.TabsSkipped++
			event.Phase = "skipped"
			event.Reason = "newer_than_cutoff"
			emitProgress(opts, event)
			continue
		}
		if !isHTTPURL(tab.URL) {
			stats.TabsSkipped++
			event.Phase = "skipped"
			event.Reason = "unsupported_url"
			emitProgress(opts, event)
			continue
		}
		if opts.Limit > 0 && processed >= opts.Limit {
			break
		}

		item, err := itemFromTab(tab, now)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		event.SourceKey = item.SourceKey
		stats.TabsMatched++
		if _, ok := linkextract.NormalizeCandidate(tab.URL); ok {
			stats.LinksFound++
		}
		processed++

		if opts.DryRun {
			if opts.ShowTitles {
				stats.SampleTitles = appendSampleTitle(stats.SampleTitles, item.Title)
			}
			event.Phase = "dry_run"
			event.Status = "would_import"
			emitProgress(opts, event)
			continue
		}

		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			stats.Errors++
			return stats, err
		}
		stats.TabsImported++
		switch result.Status {
		case model.UpsertCreated:
			stats.TabsCreated++
		case model.UpsertUpdated:
			stats.TabsUpdated++
		case model.UpsertUnchanged:
			stats.TabsUnchanged++
		}

		shouldRender := opts.Force || result.Status != model.UpsertUnchanged
		if !shouldRender {
			if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
				shouldRender = true
			}
		}
		if shouldRender {
			if err := vault.WriteItem(cfg, item); err != nil {
				stats.Errors++
				return stats, fmt.Errorf("render Safari tab %s: %w", item.SourceKey, err)
			}
			stats.TabsRendered++
		}
		event.Phase = "imported"
		event.Status = string(result.Status)
		event.Rendered = shouldRender
		emitProgress(opts, event)
	}

	return stats, nil
}

func ListDevices(ctx context.Context, cfg config.Config, opts Options) ([]Device, SnapshotInfo, error) {
	info, cleanup, err := createSnapshot(cfg, opts)
	if err != nil {
		return nil, SnapshotInfo{}, err
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	db, err := openSnapshotDB(info.DBPath)
	if err != nil {
		return nil, info, err
	}
	defer func() {
		_ = db.Close()
	}()
	if err := validateSnapshotDB(ctx, db); err != nil {
		return nil, info, err
	}
	devices, err := queryDevices(ctx, db)
	return devices, info, err
}

func readTabs(ctx context.Context, cfg config.Config, opts Options, devices []Device) ([]Tab, Device, SnapshotInfo, error) {
	matched, ok := findDevice(devices, opts.Device)
	if !ok {
		return nil, Device{}, SnapshotInfo{}, fmt.Errorf("safari device %q not found; available devices: %s", opts.Device, formatDeviceNames(devices))
	}

	info, cleanup, err := createSnapshot(cfg, opts)
	if err != nil {
		return nil, matched, info, err
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	db, err := openSnapshotDB(info.DBPath)
	if err != nil {
		return nil, matched, info, err
	}
	defer func() {
		_ = db.Close()
	}()
	if err := validateSnapshotDB(ctx, db); err != nil {
		return nil, matched, info, err
	}
	tabs, err := queryTabsForDevice(ctx, db, matched.UUID)
	return tabs, matched, info, err
}

func queryDevices(ctx context.Context, db *sql.DB) ([]Device, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			d.device_uuid,
			COALESCE(d.device_name, ''),
			COALESCE(d.device_type_identifier, ''),
			COALESCE(d.has_duplicate_device_name, 0),
			COALESCE(d.is_ephemeral_device, 0),
			COALESCE(d.last_modified, 0),
			COUNT(t.tab_uuid),
			COALESCE(MIN(t.last_viewed_time), 0),
			COALESCE(MAX(t.last_viewed_time), 0)
		FROM cloud_tab_devices d
		LEFT JOIN cloud_tabs t ON t.device_uuid = d.device_uuid
		GROUP BY d.device_uuid
		ORDER BY d.device_name, d.device_uuid`)
	if err != nil {
		return nil, fmt.Errorf("query Safari tab devices: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var devices []Device
	for rows.Next() {
		var device Device
		var hasDuplicate int
		var isEphemeral int
		var lastModified float64
		var oldest float64
		var newest float64
		if err := rows.Scan(&device.UUID, &device.Name, &device.TypeIdentifier, &hasDuplicate, &isEphemeral, &lastModified, &device.TabCount, &oldest, &newest); err != nil {
			return nil, fmt.Errorf("scan Safari tab device: %w", err)
		}
		device.HasDuplicateName = hasDuplicate != 0
		device.IsEphemeral = isEphemeral != 0
		device.LastModified = appleAbsoluteTime(lastModified)
		device.OldestTabLastViewed = appleAbsoluteTime(oldest)
		device.NewestTabLastViewed = appleAbsoluteTime(newest)
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Safari tab devices: %w", err)
	}
	return devices, nil
}

func queryTabsForDevice(ctx context.Context, db *sql.DB, deviceUUID string) ([]Tab, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			t.tab_uuid,
			d.device_uuid,
			COALESCE(d.device_name, ''),
			COALESCE(d.device_type_identifier, ''),
			COALESCE(t.title, ''),
			t.url,
			COALESCE(t.is_showing_reader, 0),
			COALESCE(t.is_pinned, 0),
			COALESCE(t.scene_id, ''),
			COALESCE(t.last_viewed_time, 0)
		FROM cloud_tabs t
		JOIN cloud_tab_devices d ON d.device_uuid = t.device_uuid
		WHERE t.device_uuid = ?
		ORDER BY t.last_viewed_time DESC, t.tab_uuid`, deviceUUID)
	if err != nil {
		return nil, fmt.Errorf("query Safari tabs for device %s: %w", deviceUUID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var tabs []Tab
	for rows.Next() {
		var tab Tab
		var isShowingReader int
		var isPinned int
		var lastViewed float64
		if err := rows.Scan(&tab.UUID, &tab.DeviceUUID, &tab.DeviceName, &tab.DeviceType, &tab.Title, &tab.URL, &isShowingReader, &isPinned, &tab.SceneID, &lastViewed); err != nil {
			return nil, fmt.Errorf("scan Safari tab: %w", err)
		}
		tab.IsShowingReader = isShowingReader != 0
		tab.IsPinned = isPinned != 0
		tab.LastViewed = appleAbsoluteTime(lastViewed)
		tabs = append(tabs, tab)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Safari tabs: %w", err)
	}
	return tabs, nil
}

func itemFromTab(tab Tab, now time.Time) (model.Item, error) {
	u, err := url.Parse(tab.URL)
	if err != nil {
		return model.Item{}, fmt.Errorf("parse Safari tab URL %q: %w", tab.URL, err)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	title := strings.TrimSpace(tab.Title)
	if title == "" {
		title = tab.URL
	}
	lastViewed := tab.LastViewed
	if lastViewed.IsZero() {
		lastViewed = now
	}
	linksJSON, _ := json.Marshal([]string{tab.URL})
	rawJSON, _ := json.Marshal(map[string]any{
		"source_type":       sourceType,
		"tab_uuid":          tab.UUID,
		"device_uuid":       tab.DeviceUUID,
		"device_name":       tab.DeviceName,
		"device_type":       tab.DeviceType,
		"title":             tab.Title,
		"url":               tab.URL,
		"is_showing_reader": tab.IsShowingReader,
		"is_pinned":         tab.IsPinned,
		"scene_id":          tab.SceneID,
		"last_viewed_at":    formatTime(lastViewed),
	})

	sourceKey := "safari-tab:" + tab.DeviceUUID + ":" + tab.UUID
	item := model.Item{
		SourceKey:     sourceKey,
		SourceType:    sourceType,
		ExternalID:    tab.UUID,
		CanonicalURL:  tab.URL,
		Title:         title,
		SavedAt:       formatTime(lastViewed),
		SyncedAt:      formatTime(now),
		Text:          safariTabText(tab, lastViewed),
		PrimaryDomain: host,
		LinksJSON:     string(linksJSON),
		NotePath:      vault.NoteRelativePath("safari-tabs", yearFor(lastViewed), safariTabSlug(tab)),
		RawJSON:       string(rawJSON),
		UpdatedAt:     now,
		LastSeenAt:    lastViewed,
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

func safariTabText(tab Tab, lastViewed time.Time) string {
	var b strings.Builder
	b.WriteString("Safari tab captured from iCloud Tabs.\n\n")
	if strings.TrimSpace(tab.DeviceName) != "" {
		b.WriteString("Device: ")
		b.WriteString(tab.DeviceName)
		b.WriteString("\n")
	}
	if strings.TrimSpace(tab.Title) != "" {
		b.WriteString("Title: ")
		b.WriteString(tab.Title)
		b.WriteString("\n")
	}
	b.WriteString("URL: ")
	b.WriteString(tab.URL)
	b.WriteString("\n")
	if !lastViewed.IsZero() {
		b.WriteString("Last viewed: ")
		b.WriteString(formatTime(lastViewed))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func safariTabSlug(tab Tab) string {
	base := strings.TrimSpace(tab.Title)
	if base == "" {
		base = strings.TrimSpace(tab.URL)
	}
	slug := nonSlug.ReplaceAllString(strings.ToLower(base), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 72 {
		slug = strings.Trim(slug[:72], "-")
	}
	if slug == "" {
		slug = "tab"
	}
	return slug + "-" + shortHash(tab.DeviceUUID+":"+tab.UUID)
}

func appleAbsoluteTime(seconds float64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return epoch.Add(time.Duration(seconds * float64(time.Second))).UTC()
}

func findDevice(devices []Device, device string) (Device, bool) {
	needle := strings.ToLower(strings.TrimSpace(device))
	for _, candidate := range devices {
		if strings.ToLower(candidate.Name) == needle || strings.ToLower(candidate.UUID) == needle {
			return candidate, true
		}
	}
	return Device{}, false
}

func formatDeviceNames(devices []Device) string {
	if len(devices) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(devices))
	for _, device := range devices {
		name := strings.TrimSpace(device.Name)
		if name == "" {
			name = device.UUID
		}
		parts = append(parts, fmt.Sprintf("%s (%d tabs)", name, device.TabCount))
	}
	return strings.Join(parts, ", ")
}

func appendSampleTitle(titles []string, title string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		return titles
	}
	if len(titles) >= 10 {
		return titles
	}
	return append(titles, title)
}

func emitProgress(opts Options, event ProgressEvent) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(event)
}

func yearFor(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return fmt.Sprintf("%04d", t.UTC().Year())
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
