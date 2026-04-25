package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbrain/internal/model"
)

const mediaSelectColumns = `
	id, remote_url, media_type, mime_type, width, height, byte_size, content_hash,
	download_status, download_error, local_path,
	archive_provider, archive_bucket, archive_key, archive_url, archive_etag, archive_status, archive_error,
	discovered_at, downloaded_at, archived_at, local_pruned_at, updated_at`

func (s *Store) ensureMediaTables() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			remote_url TEXT NOT NULL UNIQUE,
			media_type TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			byte_size INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT '',
			download_status TEXT NOT NULL DEFAULT '',
			download_error TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			archive_provider TEXT NOT NULL DEFAULT '',
			archive_bucket TEXT NOT NULL DEFAULT '',
			archive_key TEXT NOT NULL DEFAULT '',
			archive_url TEXT NOT NULL DEFAULT '',
			archive_etag TEXT NOT NULL DEFAULT '',
			archive_status TEXT NOT NULL DEFAULT '',
			archive_error TEXT NOT NULL DEFAULT '',
			discovered_at TEXT NOT NULL DEFAULT '',
			downloaded_at TEXT NOT NULL DEFAULT '',
			archived_at TEXT NOT NULL DEFAULT '',
			local_pruned_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_media_assets_download_status ON media_assets(download_status);`,
		`CREATE INDEX IF NOT EXISTS idx_media_assets_content_hash ON media_assets(content_hash);`,
		`CREATE TABLE IF NOT EXISTS item_media_links (
			item_id INTEGER NOT NULL,
			media_asset_id INTEGER NOT NULL,
			ordinal INTEGER NOT NULL DEFAULT 0,
			expanded_url TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (item_id, media_asset_id),
			UNIQUE (item_id, ordinal),
			FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE,
			FOREIGN KEY (media_asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_item_media_links_media_asset_id ON item_media_links(media_asset_id);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("apply media schema: %w", err)
		}
	}

	if err := s.ensureMediaAssetColumns(); err != nil {
		return err
	}
	if err := s.ensureItemMediaLinkColumns(); err != nil {
		return err
	}

	return nil
}

func (s *Store) ensureMediaAssetColumns() error {
	existing, err := s.tableColumns("media_assets")
	if err != nil {
		return fmt.Errorf("load media_assets table info: %w", err)
	}

	required := map[string]string{
		"media_type":       "TEXT NOT NULL DEFAULT ''",
		"mime_type":        "TEXT NOT NULL DEFAULT ''",
		"width":            "INTEGER NOT NULL DEFAULT 0",
		"height":           "INTEGER NOT NULL DEFAULT 0",
		"byte_size":        "INTEGER NOT NULL DEFAULT 0",
		"content_hash":     "TEXT NOT NULL DEFAULT ''",
		"download_status":  "TEXT NOT NULL DEFAULT ''",
		"download_error":   "TEXT NOT NULL DEFAULT ''",
		"local_path":       "TEXT NOT NULL DEFAULT ''",
		"archive_provider": "TEXT NOT NULL DEFAULT ''",
		"archive_bucket":   "TEXT NOT NULL DEFAULT ''",
		"archive_key":      "TEXT NOT NULL DEFAULT ''",
		"archive_url":      "TEXT NOT NULL DEFAULT ''",
		"archive_etag":     "TEXT NOT NULL DEFAULT ''",
		"archive_status":   "TEXT NOT NULL DEFAULT ''",
		"archive_error":    "TEXT NOT NULL DEFAULT ''",
		"discovered_at":    "TEXT NOT NULL DEFAULT ''",
		"downloaded_at":    "TEXT NOT NULL DEFAULT ''",
		"archived_at":      "TEXT NOT NULL DEFAULT ''",
		"local_pruned_at":  "TEXT NOT NULL DEFAULT ''",
		"updated_at":       "TEXT NOT NULL DEFAULT ''",
	}

	for name, definition := range required {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE media_assets ADD COLUMN %s %s", name, definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add media_assets.%s: %w", name, err)
		}
	}

	return nil
}

func (s *Store) ensureItemMediaLinkColumns() error {
	existing, err := s.tableColumns("item_media_links")
	if err != nil {
		return fmt.Errorf("load item_media_links table info: %w", err)
	}

	required := map[string]string{
		"ordinal":      "INTEGER NOT NULL DEFAULT 0",
		"expanded_url": "TEXT NOT NULL DEFAULT ''",
		"created_at":   "TEXT NOT NULL DEFAULT ''",
		"updated_at":   "TEXT NOT NULL DEFAULT ''",
	}

	for name, definition := range required {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE item_media_links ADD COLUMN %s %s", name, definition)
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("add item_media_links.%s: %w", name, err)
		}
	}

	return nil
}

func (s *Store) syncXHydrationMediaTx(ctx context.Context, tx *sql.Tx, itemID int64, hydration model.XHydration, now time.Time) (bool, error) {
	snapshot, hasSnapshot, err := decodeXHydrationSnapshot(hydration.APIJSON)
	if err != nil {
		return false, fmt.Errorf("decode media snapshot for item %d: %w", itemID, err)
	}

	switch {
	case hasSnapshot:
		return s.replaceItemMediaLinksTx(ctx, tx, itemID, snapshot.MediaObjects, now)
	case hydration.Status == "not_found":
		current, err := s.listItemMediaRefsTx(ctx, tx, itemID)
		if err != nil {
			return false, err
		}
		if len(current) == 0 {
			return false, nil
		}
		return true, s.clearItemMediaLinksTx(ctx, tx, itemID)
	default:
		return false, nil
	}
}

func (s *Store) replaceItemMediaLinksTx(ctx context.Context, tx *sql.Tx, itemID int64, media []xHydrationMedia, now time.Time) (bool, error) {
	current, err := s.listItemMediaRefsTx(ctx, tx, itemID)
	if err != nil {
		return false, err
	}

	desired := desiredItemMediaRefs(itemID, media)
	nowText := now.UTC().Format(time.RFC3339)
	linkRows := make([]itemMediaLinkRow, 0, len(desired))
	assetChanged := false

	for _, candidate := range desired {
		assetID, changed, err := s.upsertMediaAssetTx(ctx, tx, xHydrationMedia{
			Type:        candidate.MediaType,
			URL:         candidate.RemoteURL,
			ExpandedURL: candidate.ExpandedURL,
			Width:       candidate.Width,
			Height:      candidate.Height,
		}, nowText)
		if err != nil {
			return false, err
		}
		if changed {
			assetChanged = true
		}
		linkRows = append(linkRows, itemMediaLinkRow{
			ItemID:       itemID,
			MediaAssetID: assetID,
			Ordinal:      candidate.Ordinal,
			ExpandedURL:  candidate.ExpandedURL,
		})
	}

	linkChanged := !sameItemMediaRefs(current, desired)
	if !linkChanged {
		return assetChanged, nil
	}

	if err := s.clearItemMediaLinksTx(ctx, tx, itemID); err != nil {
		return false, err
	}
	for _, link := range linkRows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item_media_links (
				item_id, media_asset_id, ordinal, expanded_url, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			link.ItemID,
			link.MediaAssetID,
			link.Ordinal,
			link.ExpandedURL,
			nowText,
			nowText,
		); err != nil {
			return false, fmt.Errorf("insert item media link item=%d asset=%d: %w", link.ItemID, link.MediaAssetID, err)
		}
	}

	return true, nil
}

func (s *Store) clearItemMediaLinksTx(ctx context.Context, tx *sql.Tx, itemID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_media_links WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear item media links %d: %w", itemID, err)
	}
	return nil
}

func (s *Store) upsertMediaAssetTx(ctx context.Context, tx *sql.Tx, media xHydrationMedia, nowText string) (int64, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, media_type, width, height, download_status, discovered_at
		FROM media_assets
		WHERE remote_url = ?`, media.URL)

	var (
		assetID       int64
		currentType   string
		currentWidth  int
		currentHeight int
		currentStatus string
		currentSeenAt string
	)
	switch err := row.Scan(&assetID, &currentType, &currentWidth, &currentHeight, &currentStatus, &currentSeenAt); {
	case errors.Is(err, sql.ErrNoRows):
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO media_assets (
				remote_url, media_type, mime_type, width, height, byte_size, content_hash,
				download_status, download_error, local_path, discovered_at, downloaded_at, updated_at
			) VALUES (?, ?, '', ?, ?, 0, '', 'pending', '', '', ?, '', ?)`,
			media.URL,
			media.Type,
			media.Width,
			media.Height,
			nowText,
			nowText,
		)
		if execErr != nil {
			return 0, false, fmt.Errorf("insert media asset %q: %w", media.URL, execErr)
		}
		insertedID, execErr := result.LastInsertId()
		if execErr != nil {
			return 0, false, fmt.Errorf("fetch inserted media asset id %q: %w", media.URL, execErr)
		}
		return insertedID, true, nil
	case err != nil:
		return 0, false, fmt.Errorf("load media asset %q: %w", media.URL, err)
	default:
	}

	nextType := firstNonEmpty(media.Type, currentType)
	nextWidth := maxInt(media.Width, currentWidth)
	nextHeight := maxInt(media.Height, currentHeight)
	nextStatus := currentStatus
	if strings.TrimSpace(nextStatus) == "" {
		nextStatus = "pending"
	}
	nextSeenAt := currentSeenAt
	if strings.TrimSpace(nextSeenAt) == "" {
		nextSeenAt = nowText
	}

	changed := nextType != currentType ||
		nextWidth != currentWidth ||
		nextHeight != currentHeight ||
		nextStatus != currentStatus ||
		nextSeenAt != currentSeenAt
	if !changed {
		return assetID, false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET media_type = ?,
			width = ?,
			height = ?,
			download_status = CASE WHEN download_status = '' THEN 'pending' ELSE download_status END,
			discovered_at = CASE WHEN discovered_at = '' THEN ? ELSE discovered_at END,
			updated_at = ?
		WHERE id = ?`,
		nextType,
		nextWidth,
		nextHeight,
		nowText,
		nowText,
		assetID,
	); err != nil {
		return 0, false, fmt.Errorf("update media asset %d: %w", assetID, err)
	}

	_ = currentStatus
	_ = currentSeenAt
	return assetID, changed, nil
}

func (s *Store) ListItemMediaRefs(ctx context.Context, itemID int64) ([]model.ItemMediaRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			l.item_id,
			l.media_asset_id,
			l.ordinal,
			l.expanded_url,
			a.remote_url,
			a.media_type,
			a.download_status,
			a.local_path,
			a.archive_provider,
			a.archive_bucket,
			a.archive_key,
			a.archive_url,
			a.archive_status,
			a.width,
			a.height,
			a.local_pruned_at
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = ?
		ORDER BY l.ordinal ASC, l.media_asset_id ASC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list item media refs for item %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.ItemMediaRef
	for rows.Next() {
		var ref model.ItemMediaRef
		if err := scanItemMediaRef(rows.Scan, &ref); err != nil {
			return nil, fmt.Errorf("scan item media ref for item %d: %w", itemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item media refs for item %d: %w", itemID, err)
	}
	return refs, nil
}

func (s *Store) ListMediaAssetsForDownload(ctx context.Context, limit int, force bool) ([]model.MediaAsset, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT ` + mediaSelectColumns + `
		FROM media_assets
		WHERE remote_url != ''`
	if !force {
		query += `
			AND (download_status = ''
				OR download_status = 'pending'
				OR download_status = 'error')`
	}
	query += `
		ORDER BY
			CASE download_status
				WHEN 'pending' THEN 0
				WHEN '' THEN 1
				WHEN 'error' THEN 2
				ELSE 3
			END,
			discovered_at ASC,
			id ASC
		LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list media assets for download: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var assets []model.MediaAsset
	for rows.Next() {
		var asset model.MediaAsset
		if err := scanMediaAsset(rows.Scan, &asset); err != nil {
			return nil, fmt.Errorf("scan media asset: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media assets: %w", err)
	}

	return assets, nil
}

func (s *Store) SaveMediaDownload(ctx context.Context, assetID int64, result model.MediaDownloadResult) (bool, error) {
	return withBusyRetry(ctx, func() (bool, error) {
		row := s.db.QueryRowContext(ctx, `
			SELECT mime_type, byte_size, content_hash, local_path, download_status, download_error, downloaded_at, local_pruned_at
			FROM media_assets
			WHERE id = ?`, assetID)

		var currentMIME string
		var currentByteSize int64
		var currentHash string
		var currentPath string
		var currentStatus string
		var currentError string
		var currentDownloadedAt string
		var currentLocalPrunedAt string
		if err := row.Scan(&currentMIME, &currentByteSize, &currentHash, &currentPath, &currentStatus, &currentError, &currentDownloadedAt, &currentLocalPrunedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("media asset not found: %d", assetID)
			}
			return false, fmt.Errorf("load media asset %d: %w", assetID, err)
		}

		downloadedAt := ""
		if !result.DownloadedAt.IsZero() {
			downloadedAt = result.DownloadedAt.UTC().Format(time.RFC3339)
		}
		nextLocalPrunedAt := currentLocalPrunedAt
		if result.Status == "downloaded" && strings.TrimSpace(result.LocalPath) != "" {
			nextLocalPrunedAt = ""
		}

		changed := currentMIME != result.MIMEType ||
			currentByteSize != result.ByteSize ||
			currentHash != result.ContentHash ||
			currentPath != result.LocalPath ||
			currentStatus != result.Status ||
			currentError != result.Error ||
			currentDownloadedAt != downloadedAt ||
			currentLocalPrunedAt != nextLocalPrunedAt
		if !changed {
			return false, nil
		}

		if _, err := s.db.ExecContext(ctx, `
			UPDATE media_assets
			SET mime_type = ?,
				byte_size = ?,
				content_hash = ?,
				local_path = ?,
				download_status = ?,
				download_error = ?,
				downloaded_at = ?,
				local_pruned_at = ?,
				updated_at = ?
			WHERE id = ?`,
			result.MIMEType,
			result.ByteSize,
			result.ContentHash,
			result.LocalPath,
			result.Status,
			result.Error,
			downloadedAt,
			nextLocalPrunedAt,
			time.Now().UTC().Format(time.RFC3339),
			assetID,
		); err != nil {
			return false, fmt.Errorf("save media download %d: %w", assetID, err)
		}

		return true, nil
	})
}

type xHydrationSnapshot struct {
	MediaObjects []xHydrationMedia `json:"media_objects,omitempty"`
}

type xHydrationEnvelope struct {
	Snapshot xHydrationSnapshot `json:"snapshot"`
	Raw      map[string]any     `json:"raw"`
}

type xHydrationMedia struct {
	Type        string `json:"type,omitempty"`
	URL         string `json:"url,omitempty"`
	ExpandedURL string `json:"expanded_url,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

func decodeXHydrationSnapshot(raw string) (xHydrationSnapshot, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return xHydrationSnapshot{}, false, nil
	}

	var envelope xHydrationEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		var snapshot xHydrationSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return xHydrationSnapshot{}, false, err
		}
		if len(snapshot.MediaObjects) > 0 {
			return snapshot, true, nil
		}
		return snapshot, true, nil
	}

	snapshot := enrichXHydrationSnapshotFromRaw(envelope.Snapshot, envelope.Raw)
	if len(snapshot.MediaObjects) > 0 {
		return snapshot, true, nil
	}

	var direct xHydrationSnapshot
	if err := json.Unmarshal([]byte(raw), &direct); err != nil {
		return xHydrationSnapshot{}, false, err
	}
	return direct, true, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func enrichXHydrationSnapshotFromRaw(snapshot xHydrationSnapshot, raw map[string]any) xHydrationSnapshot {
	rawMedia := extractXHydrationMediaFromRaw(raw)
	if len(rawMedia) == 0 {
		return snapshot
	}
	if len(snapshot.MediaObjects) == 0 {
		snapshot.MediaObjects = rawMedia
		return snapshot
	}
	snapshot.MediaObjects = mergeXHydrationMedia(snapshot.MediaObjects, rawMedia)
	return snapshot
}

func mergeXHydrationMedia(current, raw []xHydrationMedia) []xHydrationMedia {
	if len(raw) == 0 {
		return current
	}
	if len(current) == len(raw) {
		out := make([]xHydrationMedia, 0, len(current))
		for i := range current {
			out = append(out, mergeXHydrationMediaRef(current[i], raw[i]))
		}
		return out
	}

	out := make([]xHydrationMedia, 0, len(current))
	used := make([]bool, len(raw))
	for i, media := range current {
		match := findRawMediaMatch(media, raw, used, i)
		if match >= 0 {
			used[match] = true
			out = append(out, mergeXHydrationMediaRef(media, raw[match]))
			continue
		}
		out = append(out, media)
	}
	return out
}

func findRawMediaMatch(target xHydrationMedia, candidates []xHydrationMedia, used []bool, index int) int {
	if target.ExpandedURL != "" {
		for i, candidate := range candidates {
			if used[i] {
				continue
			}
			if strings.TrimSpace(candidate.ExpandedURL) == strings.TrimSpace(target.ExpandedURL) {
				return i
			}
		}
	}
	if index >= 0 && index < len(candidates) && !used[index] {
		candidate := candidates[index]
		if sameMediaKind(target.Type, candidate.Type) {
			return index
		}
	}
	for i, candidate := range candidates {
		if used[i] {
			continue
		}
		if sameMediaKind(target.Type, candidate.Type) {
			return i
		}
	}
	return -1
}

func mergeXHydrationMediaRef(current, raw xHydrationMedia) xHydrationMedia {
	merged := current
	merged.Type = firstNonEmpty(current.Type, raw.Type)
	merged.ExpandedURL = firstNonEmpty(current.ExpandedURL, raw.ExpandedURL)
	merged.Width = maxInt(current.Width, raw.Width)
	merged.Height = maxInt(current.Height, raw.Height)

	switch merged.Type {
	case "video", "animated_gif":
		if looksPlayableVideoURL(raw.URL) && !looksPlayableVideoURL(current.URL) {
			merged.URL = raw.URL
		} else {
			merged.URL = firstNonEmpty(current.URL, raw.URL)
		}
	default:
		merged.URL = firstNonEmpty(current.URL, raw.URL)
	}
	return merged
}

func extractXHydrationMediaFromRaw(raw map[string]any) []xHydrationMedia {
	if len(raw) == 0 {
		return nil
	}
	if media := extractGraphQLMediaFromRaw(raw); len(media) > 0 {
		return media
	}
	if media := extractSyndicationMediaFromRaw(raw); len(media) > 0 {
		return media
	}
	return nil
}

func extractGraphQLMediaFromRaw(payload map[string]any) []xHydrationMedia {
	result := digMap(payload, "data", "tweetResult", "result")
	if len(result) == 0 {
		return nil
	}
	tweet := result
	if nested := mapAny(result["tweet"]); len(nested) > 0 {
		tweet = nested
	}
	legacy := mapAny(tweet["legacy"])
	if len(legacy) == 0 {
		return nil
	}
	mediaEntities := listAny(digMap(legacy, "extended_entities")["media"])
	if len(mediaEntities) == 0 {
		mediaEntities = listAny(digMap(legacy, "entities")["media"])
	}
	return buildXHydrationMediaFromRaw(mediaEntities)
}

func extractSyndicationMediaFromRaw(payload map[string]any) []xHydrationMedia {
	return buildXHydrationMediaFromRaw(listAny(payload["mediaDetails"]))
}

func buildXHydrationMediaFromRaw(media []map[string]any) []xHydrationMedia {
	if len(media) == 0 {
		return nil
	}
	out := make([]xHydrationMedia, 0, len(media))
	for _, entry := range media {
		out = append(out, xHydrationMedia{
			Type:        stringAny(entry["type"]),
			URL:         selectRawMediaURL(entry),
			ExpandedURL: stringAny(entry["expanded_url"]),
			Width:       intAny(digMap(entry, "original_info")["width"]),
			Height:      intAny(digMap(entry, "original_info")["height"]),
		})
	}
	return out
}

func selectRawMediaURL(media map[string]any) string {
	mediaType := stringAny(media["type"])
	if mediaType == "video" || mediaType == "animated_gif" {
		if variant := bestRawVideoVariantURL(media); variant != "" {
			return variant
		}
	}
	return firstNonEmpty(stringAny(media["media_url_https"]), stringAny(media["media_url"]))
}

func bestRawVideoVariantURL(media map[string]any) string {
	var bestURL string
	bestBitrate := -1
	for _, variant := range listAny(digMap(media, "video_info")["variants"]) {
		url := stringAny(variant["url"])
		contentType := strings.ToLower(stringAny(variant["content_type"]))
		if url == "" {
			continue
		}
		if contentType != "" && !strings.Contains(contentType, "mp4") {
			continue
		}
		bitrate := intAny(variant["bitrate"])
		if bitrate > bestBitrate {
			bestBitrate = bitrate
			bestURL = url
		}
	}
	return bestURL
}

func looksPlayableVideoURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "video.twimg.com/") ||
		strings.HasSuffix(value, ".mp4") ||
		strings.Contains(value, ".mp4?")
}

func sameMediaKind(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func digMap(m map[string]any, keys ...string) map[string]any {
	current := m
	for _, key := range keys {
		next := mapAny(current[key])
		if len(next) == 0 {
			return nil
		}
		current = next
	}
	return current
}

func mapAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func listAny(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if m, ok := entry.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringAny(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func intAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

type itemMediaLinkRow struct {
	ItemID       int64
	MediaAssetID int64
	Ordinal      int
	ExpandedURL  string
}

func (s *Store) listItemMediaRefsTx(ctx context.Context, tx *sql.Tx, itemID int64) ([]model.ItemMediaRef, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			l.item_id,
			l.media_asset_id,
			l.ordinal,
			l.expanded_url,
			a.remote_url,
			a.media_type,
			a.download_status,
			a.local_path,
			a.archive_provider,
			a.archive_bucket,
			a.archive_key,
			a.archive_url,
			a.archive_status,
			a.width,
			a.height,
			a.local_pruned_at
		FROM item_media_links l
		JOIN media_assets a ON a.id = l.media_asset_id
		WHERE l.item_id = ?
		ORDER BY l.ordinal ASC, l.media_asset_id ASC`, itemID)
	if err != nil {
		return nil, fmt.Errorf("list item media refs in tx for item %d: %w", itemID, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var refs []model.ItemMediaRef
	for rows.Next() {
		var ref model.ItemMediaRef
		if err := scanItemMediaRef(rows.Scan, &ref); err != nil {
			return nil, fmt.Errorf("scan item media ref in tx for item %d: %w", itemID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate item media refs in tx for item %d: %w", itemID, err)
	}
	return refs, nil
}

func desiredItemMediaRefs(itemID int64, media []xHydrationMedia) []model.ItemMediaRef {
	seen := make(map[string]struct{}, len(media))
	refs := make([]model.ItemMediaRef, 0, len(media))
	for _, candidate := range media {
		url := strings.TrimSpace(candidate.URL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		refs = append(refs, model.ItemMediaRef{
			ItemID:      itemID,
			Ordinal:     len(refs),
			ExpandedURL: strings.TrimSpace(candidate.ExpandedURL),
			RemoteURL:   url,
			MediaType:   strings.TrimSpace(candidate.Type),
			Width:       candidate.Width,
			Height:      candidate.Height,
		})
	}
	return refs
}

func scanMediaAsset(scan func(dest ...any) error, asset *model.MediaAsset) error {
	var discoveredAt string
	var downloadedAt string
	var archivedAt string
	var localPrunedAt string
	var updatedAt string
	if err := scan(
		&asset.ID,
		&asset.RemoteURL,
		&asset.MediaType,
		&asset.MIMEType,
		&asset.Width,
		&asset.Height,
		&asset.ByteSize,
		&asset.ContentHash,
		&asset.DownloadStatus,
		&asset.DownloadError,
		&asset.LocalPath,
		&asset.ArchiveProvider,
		&asset.ArchiveBucket,
		&asset.ArchiveKey,
		&asset.ArchiveURL,
		&asset.ArchiveETag,
		&asset.ArchiveStatus,
		&asset.ArchiveError,
		&discoveredAt,
		&downloadedAt,
		&archivedAt,
		&localPrunedAt,
		&updatedAt,
	); err != nil {
		return err
	}
	asset.DiscoveredAt = parseStoredTime(discoveredAt)
	asset.DownloadedAt = parseStoredTime(downloadedAt)
	asset.ArchivedAt = parseStoredTime(archivedAt)
	asset.LocalPrunedAt = parseStoredTime(localPrunedAt)
	asset.UpdatedAt = parseStoredTime(updatedAt)
	return nil
}

func scanItemMediaRef(scan func(dest ...any) error, ref *model.ItemMediaRef) error {
	var localPrunedAt string
	if err := scan(
		&ref.ItemID,
		&ref.MediaAssetID,
		&ref.Ordinal,
		&ref.ExpandedURL,
		&ref.RemoteURL,
		&ref.MediaType,
		&ref.DownloadStatus,
		&ref.LocalPath,
		&ref.ArchiveProvider,
		&ref.ArchiveBucket,
		&ref.ArchiveKey,
		&ref.ArchiveURL,
		&ref.ArchiveStatus,
		&ref.Width,
		&ref.Height,
		&localPrunedAt,
	); err != nil {
		return err
	}
	ref.LocalPrunedAt = parseStoredTime(localPrunedAt)
	return nil
}

func sameItemMediaRefs(current []model.ItemMediaRef, desired []model.ItemMediaRef) bool {
	if len(current) != len(desired) {
		return false
	}
	for i := range current {
		if current[i].Ordinal != desired[i].Ordinal ||
			current[i].ExpandedURL != desired[i].ExpandedURL ||
			current[i].RemoteURL != desired[i].RemoteURL ||
			current[i].MediaType != desired[i].MediaType ||
			current[i].Width != desired[i].Width ||
			current[i].Height != desired[i].Height {
			return false
		}
	}
	return true
}
