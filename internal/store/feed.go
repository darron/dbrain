package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
)

const (
	FeedHealthOK      = "ok"
	FeedHealthError   = "error"
	FeedHealthBlocked = "blocked"
	FeedHealthDead    = "dead"
)

type Feed struct {
	ID                   int64
	FeedKey              string
	URL                  string
	NormalizedURL        string
	ResolvedURL          string
	SiteURL              string
	Title                string
	Description          string
	Language             string
	Enabled              bool
	HealthStatus         string
	PollIntervalSeconds  int
	NextFetchAfter       time.Time
	FetchETag            string
	FetchLastModified    string
	FetchBodyHash        string
	LastCheckedAt        time.Time
	LastFetchedAt        time.Time
	LastChangedAt        time.Time
	LastSuccessAt        time.Time
	FailureKind          string
	FirstFailedAt        time.Time
	LastFailedAt         time.Time
	LastHTTPStatus       int
	LastError            string
	ErrorCount           int
	LatestNormalizedJSON string
	UserTags             string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type FeedUpsert struct {
	FeedKey             string
	URL                 string
	NormalizedURL       string
	ResolvedURL         string
	Title               string
	SiteURL             string
	Description         string
	Language            string
	PollIntervalSeconds int
	Enabled             bool
	UserTags            string
}

type FeedUpsertResult struct {
	FeedID  int64
	Created bool
}

type FeedFetchRecord struct {
	FeedID            int64
	ObservedAt        time.Time
	RequestURL        string
	FinalURL          string
	HTTPStatus        int
	HeadersJSON       string
	ContentEncoding   string
	DecodedBodyHash   string
	WireResponseBytes []byte
	DecodedSizeBytes  int64
	ParseStatus       string
	ParseError        string
}

type FeedEntry struct {
	FeedID          int64
	EntryKey        string
	IdentityKey     string
	GUID            string
	GUIDIsPermalink bool
	Link            string
	NormalizedLink  string
	Title           string
	Author          string
	PublishedAt     string
	EntryUpdatedAt  string
	SummaryHTML     string
	SummaryText     string
	ContentHTML     string
	ContentMarkdown string
	ContentText     string
	EnclosuresJSON  string
	ExtensionsJSON  string
	RawJSON         string
	ContentHash     string
	Item            model.Item
	SourceCandidate *model.SourceCandidate
	ObservedAt      time.Time
}

type FeedEntryApplyResult struct {
	FeedEntryID      int64
	ItemID           int64
	SourceID         int64
	Created          bool
	Updated          bool
	Unchanged        bool
	Version          int
	SourceLinked     bool
	SourceCreated    bool
	IdentityConflict bool
}

type FeedFetchState struct {
	FeedID               int64
	ResolvedURL          string
	Title                string
	SiteURL              string
	Description          string
	Language             string
	FetchETag            string
	FetchLastModified    string
	FetchBodyHash        string
	LatestNormalizedJSON string
	CheckedAt            time.Time
	FetchedAt            time.Time
	Changed              bool
	NextFetchAfter       time.Time
}

type FeedFailureState struct {
	FeedID         int64
	HealthStatus   string
	FailureKind    string
	LastHTTPStatus int
	Error          string
	FailedAt       time.Time
	NextFetchAfter time.Time
}

func (s *Store) UpsertFeed(ctx context.Context, input FeedUpsert) (FeedUpsertResult, error) {
	return withBusyRetry(ctx, func() (FeedUpsertResult, error) {
		now := time.Now().UTC().Format(time.RFC3339)
		interval := input.PollIntervalSeconds
		if interval <= 0 {
			interval = 3600
		}
		enabled := 0
		if input.Enabled {
			enabled = 1
		}

		var id int64
		err := s.db.QueryRowContext(ctx, `SELECT id FROM feeds WHERE normalized_url = ?`, input.NormalizedURL).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			result, execErr := s.db.ExecContext(ctx, `
				INSERT INTO feeds (
					feed_key, url, normalized_url, resolved_url, site_url, title, description, language,
					enabled, health_status, poll_interval_seconds, user_tags, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				input.FeedKey, input.URL, input.NormalizedURL, input.ResolvedURL, input.SiteURL, input.Title, input.Description, input.Language,
				enabled, FeedHealthOK, interval, input.UserTags, now, now)
			if execErr != nil {
				return FeedUpsertResult{}, fmt.Errorf("insert feed %s: %w", input.NormalizedURL, execErr)
			}
			id, execErr = result.LastInsertId()
			if execErr != nil {
				return FeedUpsertResult{}, fmt.Errorf("last insert id feed %s: %w", input.NormalizedURL, execErr)
			}
			return FeedUpsertResult{FeedID: id, Created: true}, nil
		case err != nil:
			return FeedUpsertResult{}, fmt.Errorf("lookup feed %s: %w", input.NormalizedURL, err)
		default:
		}

		if _, err := s.db.ExecContext(ctx, `
			UPDATE feeds
			SET resolved_url = ?, site_url = ?, title = ?, description = ?, language = ?,
				enabled = ?, poll_interval_seconds = ?, user_tags = ?, updated_at = ?
			WHERE id = ?`,
			input.ResolvedURL, input.SiteURL, input.Title, input.Description, input.Language,
			enabled, interval, input.UserTags, now, id,
		); err != nil {
			return FeedUpsertResult{}, fmt.Errorf("update feed %s: %w", input.NormalizedURL, err)
		}
		return FeedUpsertResult{FeedID: id}, nil
	})
}

func (s *Store) GetFeed(ctx context.Context, ref string) (Feed, error) {
	var feed Feed
	query := `SELECT id, feed_key, url, normalized_url, resolved_url, site_url, title, description, language,
		enabled, health_status, poll_interval_seconds, next_fetch_after, fetch_etag, fetch_last_modified, fetch_body_hash,
		last_checked_at, last_fetched_at, last_changed_at, last_success_at, failure_kind, first_failed_at, last_failed_at,
		last_http_status, last_error, error_count, latest_normalized_json, user_tags, created_at, updated_at
		FROM feeds
		WHERE feed_key = ? OR normalized_url = ? OR url = ?
		LIMIT 1`
	var enabled int
	var nextFetch, lastChecked, lastFetched, lastChanged, lastSuccess, firstFailed, lastFailed, created, updated string
	err := s.db.QueryRowContext(ctx, query, ref, ref, ref).Scan(
		&feed.ID, &feed.FeedKey, &feed.URL, &feed.NormalizedURL, &feed.ResolvedURL, &feed.SiteURL, &feed.Title, &feed.Description, &feed.Language,
		&enabled, &feed.HealthStatus, &feed.PollIntervalSeconds, &nextFetch, &feed.FetchETag, &feed.FetchLastModified, &feed.FetchBodyHash,
		&lastChecked, &lastFetched, &lastChanged, &lastSuccess, &feed.FailureKind, &firstFailed, &lastFailed,
		&feed.LastHTTPStatus, &feed.LastError, &feed.ErrorCount, &feed.LatestNormalizedJSON, &feed.UserTags, &created, &updated,
	)
	if err != nil {
		return Feed{}, fmt.Errorf("get feed %s: %w", ref, err)
	}
	feed.Enabled = enabled != 0
	feed.NextFetchAfter = parseStoredTime(nextFetch)
	feed.LastCheckedAt = parseStoredTime(lastChecked)
	feed.LastFetchedAt = parseStoredTime(lastFetched)
	feed.LastChangedAt = parseStoredTime(lastChanged)
	feed.LastSuccessAt = parseStoredTime(lastSuccess)
	feed.FirstFailedAt = parseStoredTime(firstFailed)
	feed.LastFailedAt = parseStoredTime(lastFailed)
	feed.CreatedAt = parseStoredTime(created)
	feed.UpdatedAt = parseStoredTime(updated)
	return feed, nil
}

func (s *Store) ListFeeds(ctx context.Context, includeDisabled bool) ([]Feed, error) {
	query := `SELECT feed_key FROM feeds`
	if !includeDisabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY title COLLATE NOCASE, normalized_url`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list feed keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var feeds []Feed
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan feed key: %w", err)
		}
		feed, err := s.GetFeed(ctx, key)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed keys: %w", err)
	}
	return feeds, nil
}

func (s *Store) ListFeedsDue(ctx context.Context, now time.Time, limit int, includeBlocked bool) ([]Feed, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := `SELECT feed_key FROM feeds WHERE enabled = 1`
	if !includeBlocked {
		query += ` AND health_status NOT IN (?, ?)`
	}
	query += ` AND (next_fetch_after = '' OR next_fetch_after <= ?)
		ORDER BY COALESCE(NULLIF(last_checked_at, ''), created_at), id`
	args := []any{}
	if !includeBlocked {
		args = append(args, FeedHealthBlocked, FeedHealthDead)
	}
	args = append(args, now.UTC().Format(time.RFC3339))
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list due feeds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var feeds []Feed
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan due feed key: %w", err)
		}
		feed, err := s.GetFeed(ctx, key)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due feeds: %w", err)
	}
	return feeds, nil
}

func (s *Store) EnableFeed(ctx context.Context, ref string, enabled bool) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		now := time.Now().UTC().Format(time.RFC3339)
		value := 0
		if enabled {
			value = 1
		}
		query := `UPDATE feeds SET enabled = ?, updated_at = ?`
		args := []any{value, now}
		if enabled {
			query += `, health_status = ?, failure_kind = '', first_failed_at = '', last_failed_at = '', last_http_status = 0, last_error = '', error_count = 0, next_fetch_after = ''`
			args = append(args, FeedHealthOK)
		}
		query += ` WHERE feed_key = ? OR normalized_url = ? OR url = ?`
		args = append(args, ref, ref, ref)
		res, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return struct{}{}, fmt.Errorf("set feed enabled %s: %w", ref, err)
		}
		if rows, _ := res.RowsAffected(); rows == 0 {
			return struct{}{}, sql.ErrNoRows
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) RecordFeedFetch(ctx context.Context, rec FeedFetchRecord) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		observed := rec.ObservedAt
		if observed.IsZero() {
			observed = time.Now().UTC()
		}
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO feed_fetches (
				feed_id, observed_at, request_url, final_url, http_status, headers_json, content_encoding,
				decoded_body_hash, wire_response_bytes, decoded_size_bytes, parse_status, parse_error
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.FeedID, observed.UTC().Format(time.RFC3339), rec.RequestURL, rec.FinalURL, rec.HTTPStatus, rec.HeadersJSON, rec.ContentEncoding,
			rec.DecodedBodyHash, rec.WireResponseBytes, rec.DecodedSizeBytes, rec.ParseStatus, rec.ParseError,
		)
		if err != nil {
			return struct{}{}, fmt.Errorf("record feed fetch %d: %w", rec.FeedID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) UpdateFeedFetchState(ctx context.Context, state FeedFetchState) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		checkedAt := state.CheckedAt
		if checkedAt.IsZero() {
			checkedAt = time.Now().UTC()
		}
		fetchedAt := state.FetchedAt
		if fetchedAt.IsZero() {
			fetchedAt = checkedAt
		}
		lastChanged := ""
		if state.Changed {
			lastChanged = fetchedAt.UTC().Format(time.RFC3339)
		}
		_, err := s.db.ExecContext(ctx, `
			UPDATE feeds
			SET resolved_url = COALESCE(NULLIF(?, ''), resolved_url),
				title = COALESCE(NULLIF(?, ''), title),
				site_url = COALESCE(NULLIF(?, ''), site_url),
				description = COALESCE(NULLIF(?, ''), description),
				language = COALESCE(NULLIF(?, ''), language),
				health_status = ?,
				fetch_etag = ?,
				fetch_last_modified = ?,
				fetch_body_hash = ?,
				last_checked_at = ?,
				last_fetched_at = ?,
				last_success_at = ?,
				last_changed_at = CASE WHEN ? != '' THEN ? ELSE last_changed_at END,
				failure_kind = '',
				first_failed_at = '',
				last_failed_at = '',
				last_http_status = 0,
				last_error = '',
				error_count = 0,
				latest_normalized_json = COALESCE(NULLIF(?, ''), latest_normalized_json),
				next_fetch_after = ?,
				updated_at = ?
			WHERE id = ?`,
			state.ResolvedURL, state.Title, state.SiteURL, state.Description, state.Language,
			FeedHealthOK, state.FetchETag, state.FetchLastModified, state.FetchBodyHash,
			checkedAt.UTC().Format(time.RFC3339), fetchedAt.UTC().Format(time.RFC3339), fetchedAt.UTC().Format(time.RFC3339),
			lastChanged, lastChanged, state.LatestNormalizedJSON, formatTimeForDB(state.NextFetchAfter), checkedAt.UTC().Format(time.RFC3339), state.FeedID,
		)
		if err != nil {
			return struct{}{}, fmt.Errorf("update feed fetch state %d: %w", state.FeedID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) UpdateFeedFailure(ctx context.Context, state FeedFailureState) error {
	_, err := withBusyRetry(ctx, func() (struct{}, error) {
		failedAt := state.FailedAt
		if failedAt.IsZero() {
			failedAt = time.Now().UTC()
		}
		health := state.HealthStatus
		if health == "" {
			health = FeedHealthError
		}
		_, err := s.db.ExecContext(ctx, `
			UPDATE feeds
			SET health_status = ?,
				failure_kind = ?,
				first_failed_at = CASE WHEN first_failed_at = '' THEN ? ELSE first_failed_at END,
				last_failed_at = ?,
				last_http_status = ?,
				last_error = ?,
				error_count = error_count + 1,
				last_checked_at = ?,
				next_fetch_after = ?,
				updated_at = ?
			WHERE id = ?`,
			health, state.FailureKind, failedAt.UTC().Format(time.RFC3339), failedAt.UTC().Format(time.RFC3339),
			state.LastHTTPStatus, state.Error, failedAt.UTC().Format(time.RFC3339), formatTimeForDB(state.NextFetchAfter), failedAt.UTC().Format(time.RFC3339), state.FeedID,
		)
		if err != nil {
			return struct{}{}, fmt.Errorf("update feed failure %d: %w", state.FeedID, err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *Store) ApplyFeedEntry(ctx context.Context, entry FeedEntry) (FeedEntryApplyResult, error) {
	return withBusyRetry(ctx, func() (FeedEntryApplyResult, error) {
		return s.applyFeedEntry(ctx, entry)
	})
}

func (s *Store) applyFeedEntry(ctx context.Context, entry FeedEntry) (FeedEntryApplyResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FeedEntryApplyResult{}, fmt.Errorf("begin feed entry tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	match, err := resolveFeedEntryMatchTx(ctx, tx, entry)
	if err != nil {
		return FeedEntryApplyResult{}, err
	}
	if match.ItemSourceKey != "" {
		entry.Item.SourceKey = match.ItemSourceKey
	}

	itemResult, err := s.upsertItemTx(ctx, tx, entry.Item)
	if err != nil {
		return FeedEntryApplyResult{}, err
	}

	var sourceID int64
	var sourceCreated bool
	var linkCreated bool
	if entry.SourceCandidate != nil {
		sourceID, sourceCreated, err = upsertSourceCandidate(ctx, tx, *entry.SourceCandidate)
		if err != nil {
			return FeedEntryApplyResult{}, err
		}
		linkCreated, err = insertItemSourceLinkTx(ctx, tx, itemResult.ItemID, sourceID, entry.SourceCandidate.OriginalURL)
		if err != nil {
			return FeedEntryApplyResult{}, err
		}
	}

	now := entry.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowText := now.UTC().Format(time.RFC3339)

	switch match.ID {
	case 0:
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO feed_entries (
				feed_id, entry_key, identity_key, guid, guid_is_permalink, link, normalized_link, title, author,
				published_at, entry_updated_at, summary_html, summary_text, content_html, content_markdown, content_text,
				enclosures_json, extensions_json, raw_json, content_hash, version, item_id, source_id,
				first_seen_at, last_seen_at, last_changed_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.FeedID, entry.EntryKey, entry.IdentityKey, entry.GUID, boolInt(entry.GUIDIsPermalink), entry.Link, entry.NormalizedLink, entry.Title, entry.Author,
			entry.PublishedAt, entry.EntryUpdatedAt, entry.SummaryHTML, entry.SummaryText, entry.ContentHTML, entry.ContentMarkdown, entry.ContentText,
			defaultJSON(entry.EnclosuresJSON, "[]"), defaultJSON(entry.ExtensionsJSON, "{}"), entry.RawJSON, entry.ContentHash, 1, itemResult.ItemID, sourceID,
			nowText, nowText, nowText, nowText, nowText,
		)
		if execErr != nil {
			return FeedEntryApplyResult{}, fmt.Errorf("insert feed entry %s: %w", entry.EntryKey, execErr)
		}
		id, execErr := result.LastInsertId()
		if execErr != nil {
			return FeedEntryApplyResult{}, fmt.Errorf("last insert feed entry %s: %w", entry.EntryKey, execErr)
		}
		if err := tx.Commit(); err != nil {
			return FeedEntryApplyResult{}, fmt.Errorf("commit feed entry insert: %w", err)
		}
		return FeedEntryApplyResult{FeedEntryID: id, ItemID: itemResult.ItemID, SourceID: sourceID, Created: true, Version: 1, SourceLinked: linkCreated, SourceCreated: sourceCreated}, nil
	default:
	}

	if match.ContentHash == entry.ContentHash {
		if _, execErr := tx.ExecContext(ctx, `
			UPDATE feed_entries
			SET guid = ?, guid_is_permalink = ?, link = ?, normalized_link = ?, raw_json = ?, item_id = ?, source_id = ?, last_seen_at = ?, updated_at = ?
			WHERE id = ?`,
			entry.GUID, boolInt(entry.GUIDIsPermalink), entry.Link, entry.NormalizedLink, entry.RawJSON, itemResult.ItemID, sourceID, nowText, nowText, match.ID); execErr != nil {
			return FeedEntryApplyResult{}, fmt.Errorf("touch feed entry %s: %w", entry.EntryKey, execErr)
		}
		if err := tx.Commit(); err != nil {
			return FeedEntryApplyResult{}, fmt.Errorf("commit unchanged feed entry: %w", err)
		}
		return FeedEntryApplyResult{FeedEntryID: match.ID, ItemID: itemResult.ItemID, SourceID: sourceID, Unchanged: true, Version: match.Version, SourceLinked: linkCreated, SourceCreated: sourceCreated, IdentityConflict: match.IdentityConflict}, nil
	}

	if err := appendFeedEntryVersionTx(ctx, tx, match.ID, match.Version, nowText); err != nil {
		return FeedEntryApplyResult{}, err
	}
	nextVersion := match.Version + 1
	if _, execErr := tx.ExecContext(ctx, `
		UPDATE feed_entries
		SET guid = ?, guid_is_permalink = ?, link = ?, normalized_link = ?, title = ?, author = ?,
			published_at = ?, entry_updated_at = ?, summary_html = ?, summary_text = ?, content_html = ?, content_markdown = ?, content_text = ?,
			enclosures_json = ?, extensions_json = ?, raw_json = ?, content_hash = ?, version = ?, item_id = ?, source_id = ?,
			last_seen_at = ?, last_changed_at = ?, updated_at = ?
		WHERE id = ?`,
		entry.GUID, boolInt(entry.GUIDIsPermalink), entry.Link, entry.NormalizedLink, entry.Title, entry.Author,
		entry.PublishedAt, entry.EntryUpdatedAt, entry.SummaryHTML, entry.SummaryText, entry.ContentHTML, entry.ContentMarkdown, entry.ContentText,
		defaultJSON(entry.EnclosuresJSON, "[]"), defaultJSON(entry.ExtensionsJSON, "{}"), entry.RawJSON, entry.ContentHash, nextVersion, itemResult.ItemID, sourceID,
		nowText, nowText, nowText, match.ID,
	); execErr != nil {
		return FeedEntryApplyResult{}, fmt.Errorf("update feed entry %s: %w", entry.EntryKey, execErr)
	}
	if err := tx.Commit(); err != nil {
		return FeedEntryApplyResult{}, fmt.Errorf("commit feed entry update: %w", err)
	}
	return FeedEntryApplyResult{FeedEntryID: match.ID, ItemID: itemResult.ItemID, SourceID: sourceID, Updated: true, Version: nextVersion, SourceLinked: linkCreated, SourceCreated: sourceCreated, IdentityConflict: match.IdentityConflict}, nil
}

type feedEntryMatch struct {
	ID               int64
	ContentHash      string
	Version          int
	ItemID           int64
	ItemSourceKey    string
	IdentityConflict bool
}

func resolveFeedEntryMatchTx(ctx context.Context, tx *sql.Tx, entry FeedEntry) (feedEntryMatch, error) {
	identityMatch, err := lookupFeedEntryMatchTx(ctx, tx, `feed_id = ? AND identity_key = ?`, entry.FeedID, entry.IdentityKey)
	if err != nil {
		return feedEntryMatch{}, err
	}
	guidMatch := feedEntryMatch{}
	if entry.GUID != "" {
		guidMatch, err = lookupFeedEntryMatchTx(ctx, tx, `feed_id = ? AND guid = ? AND guid != ''`, entry.FeedID, entry.GUID)
		if err != nil {
			return feedEntryMatch{}, err
		}
	}
	linkMatch := feedEntryMatch{}
	if entry.NormalizedLink != "" {
		linkMatch, err = lookupFeedEntryMatchTx(ctx, tx, `feed_id = ? AND normalized_link = ? AND normalized_link != ''`, entry.FeedID, entry.NormalizedLink)
		if err != nil {
			return feedEntryMatch{}, err
		}
	}
	switch {
	case guidMatch.ID != 0:
		if linkMatch.ID != 0 && linkMatch.ID != guidMatch.ID {
			guidMatch.IdentityConflict = true
		}
		return guidMatch, nil
	case identityMatch.ID != 0:
		return identityMatch, nil
	case linkMatch.ID != 0:
		return linkMatch, nil
	default:
		return feedEntryMatch{}, nil
	}
}

func lookupFeedEntryMatchTx(ctx context.Context, tx *sql.Tx, where string, args ...any) (feedEntryMatch, error) {
	query := `
		SELECT e.id, e.content_hash, e.version, e.item_id, i.source_key
		FROM feed_entries e
		JOIN items i ON i.id = e.item_id
		WHERE ` + where + `
		LIMIT 1`
	var match feedEntryMatch
	err := tx.QueryRowContext(ctx, query, args...).Scan(&match.ID, &match.ContentHash, &match.Version, &match.ItemID, &match.ItemSourceKey)
	if errors.Is(err, sql.ErrNoRows) {
		return feedEntryMatch{}, nil
	}
	if err != nil {
		return feedEntryMatch{}, fmt.Errorf("lookup feed entry match: %w", err)
	}
	return match, nil
}

func (s *Store) upsertItemTx(ctx context.Context, tx *sql.Tx, item model.Item) (model.UpsertResult, error) {
	var existingID int64
	var existingHash string
	var existingArticleTitle string
	var existingArticleText string
	var existingSummary itemSummaryFields
	var existingOCR itemOCRFields
	row := tx.QueryRowContext(ctx, `SELECT
		id, content_hash, article_title, article_text,
		summary_text, summary_json, summary_status, summary_error, summary_model, summary_prompt_version, summary_tool, summary_tool_version, summary_input_hash, summarized_at,
		ocr_text, ocr_json, ocr_status, ocr_error, ocr_model, ocr_tool, ocr_tool_version, ocr_input_hash, ocr_at
		FROM items
		WHERE source_key = ?`, item.SourceKey)
	switch scanErr := row.Scan(
		&existingID, &existingHash, &existingArticleTitle, &existingArticleText,
		&existingSummary.Text, &existingSummary.JSON, &existingSummary.Status, &existingSummary.Error, &existingSummary.Model, &existingSummary.PromptVersion, &existingSummary.Tool, &existingSummary.ToolVersion, &existingSummary.InputHash, &existingSummary.At,
		&existingOCR.Text, &existingOCR.JSON, &existingOCR.Status, &existingOCR.Error, &existingOCR.Model, &existingOCR.Tool, &existingOCR.ToolVersion, &existingOCR.InputHash, &existingOCR.At,
	); {
	case errors.Is(scanErr, sql.ErrNoRows):
		now := item.UpdatedAt.Format(time.RFC3339)
		result, execErr := tx.ExecContext(ctx, `INSERT INTO items (
			source_key, source_type, external_id, canonical_url, title, author_handle, author_name,
			published_at, saved_at, synced_at, language, text, article_title, article_text,
			primary_category, primary_domain, links_json, categories, domains, github_urls, folder_names,
			like_count, repost_count, reply_count, quote_count, bookmark_count,
			content_hash, note_path, raw_json, imported_at, updated_at, last_seen_at, user_tags
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.SourceKey, item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
			item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
			item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
			item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
			item.ContentHash, item.NotePath, item.RawJSON, now, now, item.LastSeenAt.Format(time.RFC3339), item.UserTags,
		)
		if execErr != nil {
			return model.UpsertResult{}, fmt.Errorf("insert feed item %s: %w", item.SourceKey, execErr)
		}
		itemID, execErr := result.LastInsertId()
		if execErr != nil {
			return model.UpsertResult{}, fmt.Errorf("last insert feed item %s: %w", item.SourceKey, execErr)
		}
		if err := s.syncFTSTx(ctx, tx, itemID, item); err != nil {
			return model.UpsertResult{}, err
		}
		if err := s.syncItemEnrichmentMirrorTx(ctx, tx, itemID, item); err != nil {
			return model.UpsertResult{}, err
		}
		return model.UpsertResult{Status: model.UpsertCreated, ItemID: itemID, NotePath: item.NotePath}, nil
	case scanErr != nil:
		return model.UpsertResult{}, fmt.Errorf("load feed item %s: %w", item.SourceKey, scanErr)
	default:
	}
	if preserveExistingItemEnrichmentFields(&item, existingArticleTitle, existingArticleText, existingSummary, existingOCR) {
		item.ContentHash = itemhash.Compute(item)
	}
	if existingHash == item.ContentHash {
		if _, execErr := tx.ExecContext(ctx, `UPDATE items SET last_seen_at = ?, synced_at = ?, raw_json = ? WHERE id = ?`,
			item.LastSeenAt.Format(time.RFC3339), item.SyncedAt, item.RawJSON, existingID); execErr != nil {
			return model.UpsertResult{}, fmt.Errorf("touch feed item %s: %w", item.SourceKey, execErr)
		}
		return model.UpsertResult{Status: model.UpsertUnchanged, ItemID: existingID, NotePath: item.NotePath}, nil
	}
	if _, execErr := tx.ExecContext(ctx, `UPDATE items SET
		source_type = ?, external_id = ?, canonical_url = ?, title = ?, author_handle = ?, author_name = ?,
		published_at = ?, saved_at = ?, synced_at = ?, language = ?, text = ?, article_title = ?, article_text = ?,
		primary_category = ?, primary_domain = ?, links_json = ?, categories = ?, domains = ?, github_urls = ?, folder_names = ?,
		like_count = ?, repost_count = ?, reply_count = ?, quote_count = ?, bookmark_count = ?,
		content_hash = ?, note_path = ?, raw_json = ?, updated_at = ?, last_seen_at = ?, user_tags = ?
		WHERE id = ?`,
		item.SourceType, item.ExternalID, item.CanonicalURL, item.Title, item.AuthorHandle, item.AuthorName,
		item.PublishedAt, item.SavedAt, item.SyncedAt, item.Language, item.Text, item.ArticleTitle, item.ArticleText,
		item.PrimaryCategory, item.PrimaryDomain, item.LinksJSON, item.Categories, item.Domains, item.GitHubURLs, item.FolderNames,
		item.LikeCount, item.RepostCount, item.ReplyCount, item.QuoteCount, item.BookmarkCount,
		item.ContentHash, item.NotePath, item.RawJSON, item.UpdatedAt.Format(time.RFC3339), item.LastSeenAt.Format(time.RFC3339), item.UserTags,
		existingID); execErr != nil {
		return model.UpsertResult{}, fmt.Errorf("update feed item %s: %w", item.SourceKey, execErr)
	}
	if err := s.syncFTSTx(ctx, tx, existingID, item); err != nil {
		return model.UpsertResult{}, err
	}
	if err := s.syncItemEnrichmentMirrorTx(ctx, tx, existingID, item); err != nil {
		return model.UpsertResult{}, err
	}
	return model.UpsertResult{Status: model.UpsertUpdated, ItemID: existingID, NotePath: item.NotePath}, nil
}

func insertItemSourceLinkTx(ctx context.Context, tx *sql.Tx, itemID, sourceID int64, originalURL string) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO item_source_links (item_id, source_id, original_url, created_at)
		VALUES (?, ?, ?, ?)`,
		itemID, sourceID, originalURL, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, fmt.Errorf("link feed item %d to source %d: %w", itemID, sourceID, err)
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

func appendFeedEntryVersionTx(ctx context.Context, tx *sql.Tx, feedEntryID int64, version int, observedAt string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO feed_entry_versions (
			feed_entry_id, version, content_hash, raw_json, link, normalized_link, title, author, published_at, entry_updated_at,
			content_markdown, content_html, content_text, summary_html, summary_text, enclosures_json, extensions_json, observed_at
		)
		SELECT id, version, content_hash, raw_json, link, normalized_link, title, author, published_at, entry_updated_at,
			content_markdown, content_html, content_text, summary_html, summary_text, enclosures_json, extensions_json, ?
		FROM feed_entries
		WHERE id = ?`,
		observedAt, feedEntryID)
	if err != nil {
		return fmt.Errorf("append feed entry version %d: %w", feedEntryID, err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func defaultJSON(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
