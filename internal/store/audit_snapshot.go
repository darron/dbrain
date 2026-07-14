package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type AuditPipelinePartitions struct {
	Hydration     []PipelineStageRow
	Extraction    []PipelineStageRow
	Summary       []PipelineStageRow
	Transcription []PipelineStageRow
	OCR           []PipelineStageRow
	MediaArchive  []PipelineStageRow
}

type AuditMediaLocalEvidence struct {
	EligibleLocalCount   int
	UncoveredPrunedCount int
	OrphanCount          int
}

type AuditArchivedMediaRecord struct {
	Key             string
	SizeBytes       int64
	ArchivedAt      time.Time
	ArchivedAtValid bool
}

type AuditReadSnapshot struct {
	mu             sync.Mutex
	tx             *sql.Tx
	conn           *sql.Conn
	priorQueryOnly bool
	closed         bool
}

func (s *Store) BeginAuditReadSnapshot(ctx context.Context) (*AuditReadSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open audit snapshot connection: %w", err)
	}
	priorQueryOnly, err := auditQueryOnly(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read audit snapshot query-only state: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		restoreErr := restoreAuditQueryOnly(conn, priorQueryOnly)
		_ = conn.Close()
		if restoreErr != nil {
			return nil, fmt.Errorf("set audit snapshot query-only: %w; restore query-only: %v", err, restoreErr)
		}
		return nil, fmt.Errorf("set audit snapshot query-only: %w", err)
	}
	// Keep the transaction lifetime explicit: callers bound every snapshot
	// query and close the snapshot themselves. The bootstrap context may have a
	// deliberately short deadline and must not roll back a successfully opened
	// snapshot in the middle of later bounded reads.
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		restoreErr := restoreAuditQueryOnly(conn, priorQueryOnly)
		_ = conn.Close()
		if restoreErr != nil {
			return nil, fmt.Errorf("begin audit read snapshot: %w; restore query-only: %v", err, restoreErr)
		}
		return nil, fmt.Errorf("begin audit read snapshot: %w", err)
	}
	return &AuditReadSnapshot{tx: tx, conn: conn, priorQueryOnly: priorQueryOnly}, nil
}

func (s *AuditReadSnapshot) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	rollbackErr := s.tx.Rollback()
	restoreErr := restoreAuditQueryOnly(s.conn, s.priorQueryOnly)
	connErr := s.conn.Close()
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return rollbackErr
	}
	if restoreErr != nil {
		return restoreErr
	}
	return connErr
}

func auditQueryOnly(ctx context.Context, conn *sql.Conn) (bool, error) {
	var state int
	if err := conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&state); err != nil {
		return false, err
	}
	if state != 0 && state != 1 {
		return false, fmt.Errorf("unexpected PRAGMA query_only value %d", state)
	}
	return state == 1, nil
}

func restoreAuditQueryOnly(conn *sql.Conn, enabled bool) error {
	pragma := `PRAGMA query_only = OFF`
	if enabled {
		pragma = `PRAGMA query_only = ON`
	}
	_, err := conn.ExecContext(context.Background(), pragma)
	if err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return err
}

func (s *AuditReadSnapshot) PipelinePartitions(ctx context.Context) (AuditPipelinePartitions, error) {
	if err := ctx.Err(); err != nil {
		return AuditPipelinePartitions{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return AuditPipelinePartitions{}, fmt.Errorf("audit read snapshot is closed")
	}
	tx := s.tx
	s.mu.Unlock()

	view := &Store{read: tx}
	stats, err := view.Pipeline(ctx, "", "", "")
	if err != nil {
		return AuditPipelinePartitions{}, err
	}
	return AuditPipelinePartitions{
		Hydration: stats.Hydration, Extraction: stats.Extraction, Summary: stats.Summary,
		Transcription: stats.Transcription, OCR: stats.OCR, MediaArchive: stats.MediaArchive,
	}, nil
}

func (s *AuditReadSnapshot) MediaLocalEvidence(ctx context.Context) (AuditMediaLocalEvidence, error) {
	tx, err := s.query(ctx)
	if err != nil {
		return AuditMediaLocalEvidence{}, err
	}
	var out AuditMediaLocalEvidence
	queries := []struct {
		dst *int
		sql string
	}{
		{&out.EligibleLocalCount, `SELECT COUNT(*) FROM media_assets a WHERE ` + mediaArchiveCandidateWhere("a", false)},
		{&out.UncoveredPrunedCount, `SELECT COUNT(*) FROM media_assets a
			WHERE a.local_pruned_at != ''
			AND EXISTS (SELECT 1 FROM item_media_links l WHERE l.media_asset_id = a.id)
			AND NOT ` + mediaArchiveEnrichmentCompleteWhere("a")},
		{&out.OrphanCount, `SELECT COUNT(*) FROM media_assets a
			WHERE a.download_status = '` + model.MediaDownloadStatusDownloaded + `'
			AND NOT EXISTS (SELECT 1 FROM item_media_links l WHERE l.media_asset_id = a.id)`},
	}
	for _, query := range queries {
		if err := tx.QueryRowContext(ctx, query.sql).Scan(query.dst); err != nil {
			return AuditMediaLocalEvidence{}, fmt.Errorf("inspect audit media coverage: %w", err)
		}
	}
	return out, nil
}

func (s *AuditReadSnapshot) ArchivedMediaRecords(ctx context.Context) ([]AuditArchivedMediaRecord, error) {
	tx, err := s.query(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT archive_key, byte_size, archived_at
		FROM media_assets
		WHERE archive_status = ? AND archive_key != ''
		ORDER BY id ASC`, model.MediaArchiveStatusArchived)
	if err != nil {
		return nil, fmt.Errorf("list audit archived media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]AuditArchivedMediaRecord, 0)
	for rows.Next() {
		var record AuditArchivedMediaRecord
		var archivedAt string
		if err := rows.Scan(&record.Key, &record.SizeBytes, &archivedAt); err != nil {
			return nil, fmt.Errorf("scan audit archived media: %w", err)
		}
		if parsed, err := time.Parse(time.RFC3339, archivedAt); err == nil {
			record.ArchivedAt = parsed.UTC()
			record.ArchivedAtValid = true
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit archived media: %w", err)
	}
	return out, nil
}

func (s *AuditReadSnapshot) query(ctx context.Context) (*sql.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("audit read snapshot is closed")
	}
	return s.tx, nil
}
