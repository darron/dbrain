package safaritabs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
)

type safariAuditInventory struct {
	cfg  config.Config
	opts Options
	now  func() time.Time
}

// NewAuditInventory returns a read-only Safari iCloud Tabs inventory for one
// explicitly configured device. It snapshots CloudTabs exactly once per run.
func NewAuditInventory(cfg config.Config, opts Options) audit.UpstreamInventory {
	return newSafariAuditInventory(cfg, opts, time.Now)
}

func newSafariAuditInventory(cfg config.Config, opts Options, now func() time.Time) *safariAuditInventory {
	return &safariAuditInventory{cfg: cfg, opts: opts, now: now}
}

func (i *safariAuditInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	result := audit.InventoryResult{}
	if err := validateSafariAuditBudget(budget); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if i == nil || i.now == nil || strings.TrimSpace(i.opts.Device) == "" {
		return result, fmt.Errorf("%w: safari audit inventory configuration invalid", audit.ErrInventoryInvalid)
	}

	info, cleanup, err := createSnapshot(i.cfg, i.opts)
	if err != nil {
		return result, privacySafeSafariAuditError(ctx, "snapshot", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()
	db, err := openSnapshotDB(info.DBPath)
	if err != nil {
		return result, privacySafeSafariAuditError(ctx, "open snapshot", err)
	}
	defer func() { _ = db.Close() }()
	if err := validateSnapshotDB(ctx, db); err != nil {
		return result, privacySafeSafariAuditError(ctx, "validate snapshot", err)
	}

	deviceUUID, err := resolveSafariAuditDevice(ctx, db, i.opts.Device)
	if err != nil {
		return result, err
	}
	rows, err := querySafariAuditTabs(ctx, db, deviceUUID, i.opts.OlderThan, i.now().UTC())
	if err != nil {
		return result, privacySafeSafariAuditError(ctx, "read inventory", err)
	}
	defer func() { _ = rows.Close() }()
	result.PageCount = 1

	seen := make(map[string]struct{}, min(budget.MaxIdentities, 1024))
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			result.IdentityHashes = sortedSafariAuditHashes(seen)
			return result, err
		}
		var tabUUID, rowDeviceUUID, rawURL string
		var lastViewed float64
		if err := rows.Scan(&tabUUID, &rowDeviceUUID, &rawURL, &lastViewed); err != nil {
			return result, privacySafeSafariAuditError(ctx, "scan inventory", err)
		}
		if !isHTTPURL(rawURL) {
			continue
		}
		if strings.TrimSpace(tabUUID) == "" || strings.TrimSpace(rowDeviceUUID) == "" || !strings.EqualFold(rowDeviceUUID, deviceUUID) {
			result.IdentityHashes = sortedSafariAuditHashes(seen)
			return result, fmt.Errorf("%w: safari tab identity invalid", audit.ErrInventoryInvalid)
		}
		sourceKey := safariTabSourceKey(rowDeviceUUID, tabUUID)
		hash, err := audit.HashUpstreamIdentity(audit.SourceSafariTabs, sourceKey)
		if err != nil {
			result.IdentityHashes = sortedSafariAuditHashes(seen)
			return result, fmt.Errorf("%w: safari tab identity invalid", audit.ErrInventoryInvalid)
		}
		if _, exists := seen[hash]; exists {
			continue
		}
		if len(seen) == budget.MaxIdentities {
			result.IdentityHashes = sortedSafariAuditHashes(seen)
			return result, fmt.Errorf("%w: safari tab identity budget exhausted", audit.ErrInventoryBudget)
		}
		seen[hash] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		result.IdentityHashes = sortedSafariAuditHashes(seen)
		return result, privacySafeSafariAuditError(ctx, "iterate inventory", err)
	}
	result.IdentityHashes = sortedSafariAuditHashes(seen)
	result.Complete = true
	return result, nil
}

func validateSafariAuditBudget(budget audit.InventoryBudget) error {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > audit.InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > audit.InventoryMaxPages {
		return fmt.Errorf("%w: safari inventory budget", audit.ErrInventoryInvalid)
	}
	return nil
}

func resolveSafariAuditDevice(ctx context.Context, db *sql.DB, requested string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT device_uuid, COALESCE(device_name, '') FROM cloud_tab_devices ORDER BY device_uuid`)
	if err != nil {
		return "", privacySafeSafariAuditError(ctx, "read devices", err)
	}
	defer func() { _ = rows.Close() }()
	needle := strings.ToLower(strings.TrimSpace(requested))
	var uuidMatches []string
	var nameMatches []string
	for rows.Next() {
		var uuid, name string
		if err := rows.Scan(&uuid, &name); err != nil {
			return "", privacySafeSafariAuditError(ctx, "scan devices", err)
		}
		if strings.ToLower(strings.TrimSpace(uuid)) == needle {
			uuidMatches = append(uuidMatches, uuid)
		}
		if strings.ToLower(strings.TrimSpace(name)) == needle {
			nameMatches = append(nameMatches, uuid)
		}
	}
	if err := rows.Err(); err != nil {
		return "", privacySafeSafariAuditError(ctx, "iterate devices", err)
	}
	if len(uuidMatches) == 1 {
		return uuidMatches[0], nil
	}
	if len(uuidMatches) > 1 || len(nameMatches) != 1 {
		return "", fmt.Errorf("%w: safari configured device is missing or ambiguous", audit.ErrInventoryInvalid)
	}
	return nameMatches[0], nil
}

func querySafariAuditTabs(ctx context.Context, db *sql.DB, deviceUUID string, olderThan time.Duration, now time.Time) (*sql.Rows, error) {
	query := `
		SELECT tab_uuid, device_uuid, url, COALESCE(last_viewed_time, 0)
		FROM cloud_tabs
		WHERE device_uuid = ?`
	args := []any{deviceUUID}
	if olderThan > 0 {
		cutoff := now.Add(-olderThan)
		query += ` AND (COALESCE(last_viewed_time, 0) <= 0 OR last_viewed_time <= ?)`
		args = append(args, cfAbsoluteSecondsForAudit(cutoff))
	}
	query += ` ORDER BY last_viewed_time DESC, tab_uuid`
	return db.QueryContext(ctx, query, args...)
}

func cfAbsoluteSecondsForAudit(value time.Time) float64 {
	epoch := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	return value.UTC().Sub(epoch).Seconds()
}

func privacySafeSafariAuditError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, audit.ErrInventoryBudget) || errors.Is(err, audit.ErrInventoryInvalid) {
		return err
	}
	return fmt.Errorf("safari tabs audit %s failed", operation)
}

func sortedSafariAuditHashes(seen map[string]struct{}) []string {
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}
