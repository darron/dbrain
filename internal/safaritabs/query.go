package safaritabs

import (
	"context"
	"database/sql"
	"fmt"
)

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
