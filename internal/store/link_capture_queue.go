package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const (
	maxLinkCaptureFailureKindLength = 64
	MaxLinkCaptureAttempts          = 8
)

var linkCaptureFailureKindPattern = regexp.MustCompile(`[^a-z0-9_-]+`)

// LinkCapture is the durable, pre-authoritative intake record for a deferred
// link save. It is intentionally separate from sources: source mutations still
// cross the authoritative semantic maintenance lease when the worker drains it.
type LinkCapture struct {
	ID             int64
	Candidate      model.SourceCandidate
	EnqueuedAt     time.Time
	UpdatedAt      time.Time
	LastAttemptAt  time.Time
	NextAttemptAt  time.Time
	ProcessedAt    time.Time
	DeadLetteredAt time.Time
	AttemptCount   int
	LastError      string
}

type LinkCaptureEnqueueResult struct {
	Capture  LinkCapture
	Created  bool
	Reopened bool
}

func (s *Store) EnqueueLinkCapture(ctx context.Context, candidate model.SourceCandidate, now time.Time) (LinkCaptureEnqueueResult, error) {
	if s == nil || s.db == nil {
		return LinkCaptureEnqueueResult{}, errors.New("link capture store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(candidate.NormalizedURL) == "" {
		return LinkCaptureEnqueueResult{}, errors.New("link capture normalized URL is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	nowText := now.Format(time.RFC3339)
	db, err := s.linkCaptureDB()
	if err != nil {
		return LinkCaptureEnqueueResult{}, err
	}

	// Keep each state transition as its own autocommit statement. A deferred
	// read transaction that upgrades to a write returns SQLITE_BUSY immediately
	// under a concurrent writer, bypassing SQLite's busy handler. These direct
	// INSERT/UPDATE statements instead wait up to the admission pool's short
	// per-connection busy timeout and never hold a read snapshot while writing.
	for attempt := 0; attempt < 3; attempt++ {
		var id int64
		err = db.QueryRowContext(ctx, `
			INSERT INTO link_capture_queue (
				normalized_url, original_url, canonical_url, source_type, domain,
				source_key, note_path, enqueued_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(normalized_url) DO NOTHING
			RETURNING id`,
			candidate.NormalizedURL, candidate.OriginalURL, candidate.CanonicalURL,
			candidate.SourceType, candidate.Domain, candidate.SourceKey, candidate.NotePath,
			nowText, nowText).Scan(&id)
		if err == nil {
			return LinkCaptureEnqueueResult{
				Capture:  newEnqueuedLinkCapture(id, candidate, now),
				Created:  true,
				Reopened: false,
			}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return LinkCaptureEnqueueResult{}, fmt.Errorf("insert link capture: %w", err)
		}

		err = db.QueryRowContext(ctx, `
			UPDATE link_capture_queue
			SET original_url = ?, canonical_url = ?, source_type = ?, domain = ?,
				source_key = ?, note_path = ?, enqueued_at = ?, updated_at = ?,
				last_attempt_at = '', next_attempt_at = '', processed_at = '',
				attempt_count = 0, last_error = '', dead_lettered_at = ''
			WHERE normalized_url = ? AND (processed_at <> '' OR dead_lettered_at <> '')
			RETURNING id`,
			candidate.OriginalURL, candidate.CanonicalURL, candidate.SourceType, candidate.Domain,
			candidate.SourceKey, candidate.NotePath, nowText, nowText, candidate.NormalizedURL).Scan(&id)
		if err == nil {
			return LinkCaptureEnqueueResult{
				Capture:  newEnqueuedLinkCapture(id, candidate, now),
				Created:  false,
				Reopened: true,
			}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return LinkCaptureEnqueueResult{}, fmt.Errorf("reopen link capture: %w", err)
		}

		// A pending duplicate is an explicit retry request: clear stale
		// backoff/error state, but preserve attempt_count so repeated saves
		// cannot erase the current failure budget. Processed/dead rows take
		// the reopen transition above and intentionally start a new window.
		err = db.QueryRowContext(ctx, `
			UPDATE link_capture_queue
			SET original_url = ?, canonical_url = ?, source_type = ?, domain = ?,
				source_key = ?, note_path = ?, updated_at = ?,
				next_attempt_at = '', last_error = ''
			WHERE normalized_url = ? AND processed_at = '' AND dead_lettered_at = ''
			RETURNING id`,
			candidate.OriginalURL, candidate.CanonicalURL, candidate.SourceType, candidate.Domain,
			candidate.SourceKey, candidate.NotePath, nowText, candidate.NormalizedURL).Scan(&id)
		if err == nil {
			return LinkCaptureEnqueueResult{
				Capture:  newEnqueuedLinkCapture(id, candidate, now),
				Created:  false,
				Reopened: false,
			}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return LinkCaptureEnqueueResult{}, fmt.Errorf("refresh pending link capture: %w", err)
		}
		// A worker may have transitioned the row between the state-specific
		// updates. Re-run the state machine rather than reporting a false failure.
	}
	return LinkCaptureEnqueueResult{}, errors.New("link capture changed while enqueueing")
}

func newEnqueuedLinkCapture(id int64, candidate model.SourceCandidate, now time.Time) LinkCapture {
	return LinkCapture{
		ID: id, Candidate: candidate, EnqueuedAt: now, UpdatedAt: now,
	}
}

func (s *Store) GetLinkCapture(ctx context.Context, id int64) (LinkCapture, error) {
	if s == nil || s.db == nil {
		return LinkCapture{}, errors.New("link capture store is nil")
	}
	var capture LinkCapture
	var fields linkCaptureScanFields
	err := s.db.QueryRowContext(ctx, linkCaptureSelect+` WHERE q.id = ?`, id).Scan(fields.values()...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LinkCapture{}, fmt.Errorf("link capture %d not found", id)
		}
		return LinkCapture{}, fmt.Errorf("get link capture %d: %w", id, err)
	}
	if err := fields.apply(&capture); err != nil {
		return LinkCapture{}, fmt.Errorf("parse link capture %d: %w", id, err)
	}
	return capture, nil
}

func (s *Store) ListPendingLinkCaptures(ctx context.Context, now time.Time, limit int) ([]LinkCapture, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("link capture store is nil")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, linkCaptureSelect+`
		WHERE q.processed_at = ''
		  AND q.dead_lettered_at = ''
		  AND (q.next_attempt_at = '' OR q.next_attempt_at <= ?)
		ORDER BY q.enqueued_at ASC, q.id ASC
		LIMIT ?`, now.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending link captures: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var captures []LinkCapture
	for rows.Next() {
		var capture LinkCapture
		var fields linkCaptureScanFields
		if err := rows.Scan(fields.values()...); err != nil {
			return nil, fmt.Errorf("scan pending link capture: %w", err)
		}
		if err := fields.apply(&capture); err != nil {
			return nil, fmt.Errorf("parse pending link capture: %w", err)
		}
		captures = append(captures, capture)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending link captures: %w", err)
	}
	return captures, nil
}

// ListDeadLetteredLinkCaptures returns captures whose bounded intake retry
// window ended without creating a source. The failure fields are deliberately
// exposed as queue state: LastError is a normalized failure kind, not a raw
// diagnostic message.
func (s *Store) ListDeadLetteredLinkCaptures(ctx context.Context, limit int) ([]LinkCapture, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("link capture store is nil")
	}
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.QueryContext(ctx, linkCaptureSelect+`
		WHERE q.dead_lettered_at <> ''
		ORDER BY q.dead_lettered_at DESC, q.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list dead-lettered link captures: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var captures []LinkCapture
	for rows.Next() {
		var capture LinkCapture
		var fields linkCaptureScanFields
		if err := rows.Scan(fields.values()...); err != nil {
			return nil, fmt.Errorf("scan dead-lettered link capture: %w", err)
		}
		if err := fields.apply(&capture); err != nil {
			return nil, fmt.Errorf("parse dead-lettered link capture: %w", err)
		}
		captures = append(captures, capture)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dead-lettered link captures: %w", err)
	}
	return captures, nil
}

func (s *Store) MarkLinkCaptureAttempt(ctx context.Context, id int64, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE link_capture_queue
		SET attempt_count = attempt_count + 1, last_attempt_at = ?, updated_at = ?
		WHERE id = ? AND processed_at = '' AND dead_lettered_at = ''`,
		now.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("mark link capture %d attempt: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("link capture %d is missing or processed", id)
	}
	return nil
}

func (s *Store) MarkLinkCaptureProcessed(ctx context.Context, id int64, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE link_capture_queue
		SET processed_at = ?, next_attempt_at = '', last_error = '', updated_at = ?
		WHERE id = ? AND processed_at = '' AND dead_lettered_at = ''`,
		now.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("mark link capture %d processed: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("link capture %d is missing or already processed", id)
	}
	return nil
}

func (s *Store) MarkLinkCaptureFailed(ctx context.Context, id int64, now, nextAttempt time.Time, kind string) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if nextAttempt.IsZero() {
		nextAttempt = now.Add(time.Minute)
	}
	kind = normalizeLinkCaptureFailureKind(kind)
	nextAttemptText := nextAttempt.UTC().Format(time.RFC3339)
	deadLetteredAt := now.UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx, `
		UPDATE link_capture_queue
		SET next_attempt_at = CASE WHEN attempt_count >= ? THEN '' ELSE ? END,
			last_error = ?,
			dead_lettered_at = CASE WHEN attempt_count >= ? THEN ? ELSE dead_lettered_at END,
			updated_at = ?
		WHERE id = ? AND processed_at = '' AND dead_lettered_at = ''`,
		MaxLinkCaptureAttempts, nextAttemptText, kind,
		MaxLinkCaptureAttempts, deadLetteredAt, now.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("mark link capture %d failed: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("link capture %d is missing or processed", id)
	}
	return nil
}

func normalizeLinkCaptureFailureKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = linkCaptureFailureKindPattern.ReplaceAllString(kind, "_")
	kind = strings.Trim(kind, "_")
	if kind == "" {
		kind = "unknown"
	}
	if len(kind) > maxLinkCaptureFailureKindLength {
		kind = kind[:maxLinkCaptureFailureKindLength]
	}
	return kind
}

const linkCaptureSelect = `
	SELECT q.id, q.original_url, q.canonical_url, q.normalized_url,
		q.source_type, q.domain, q.source_key, q.note_path,
		q.enqueued_at, q.updated_at, q.last_attempt_at, q.next_attempt_at,
		q.processed_at, q.dead_lettered_at, q.attempt_count, q.last_error
	FROM link_capture_queue q`

type linkCaptureScanFields struct {
	id, attemptCount                                      int64
	originalURL, canonicalURL, normalizedURL              string
	sourceType, domain, sourceKey, notePath               string
	enqueuedAt, updatedAt, lastAttemptAt                  string
	nextAttemptAt, processedAt, deadLetteredAt, lastError string
}

func (f *linkCaptureScanFields) values() []any {
	return []any{
		&f.id, &f.originalURL, &f.canonicalURL, &f.normalizedURL,
		&f.sourceType, &f.domain, &f.sourceKey, &f.notePath,
		&f.enqueuedAt, &f.updatedAt, &f.lastAttemptAt, &f.nextAttemptAt,
		&f.processedAt, &f.deadLetteredAt, &f.attemptCount, &f.lastError,
	}
}

func (f *linkCaptureScanFields) apply(capture *LinkCapture) error {
	capture.ID = f.id
	capture.Candidate = model.SourceCandidate{
		OriginalURL: f.originalURL, CanonicalURL: f.canonicalURL,
		NormalizedURL: f.normalizedURL, SourceType: f.sourceType,
		Domain: f.domain, SourceKey: f.sourceKey, NotePath: f.notePath,
	}
	capture.EnqueuedAt = parseStoredTime(f.enqueuedAt)
	capture.UpdatedAt = parseStoredTime(f.updatedAt)
	capture.LastAttemptAt = parseStoredTime(f.lastAttemptAt)
	capture.NextAttemptAt = parseStoredTime(f.nextAttemptAt)
	capture.ProcessedAt = parseStoredTime(f.processedAt)
	capture.DeadLetteredAt = parseStoredTime(f.deadLetteredAt)
	capture.AttemptCount = int(f.attemptCount)
	capture.LastError = f.lastError
	return nil
}
