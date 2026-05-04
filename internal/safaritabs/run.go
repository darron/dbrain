package safaritabs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

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
