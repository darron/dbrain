package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
)

type AuditPipelinePartitions struct {
	Hydration     []PipelineStageRow
	Extraction    []PipelineStageRow
	Summary       []PipelineStageRow
	Transcription []PipelineStageRow
	OCR           []PipelineStageRow
	MediaArchive  []PipelineStageRow
}

type AuditReadSnapshot struct {
	mu     sync.Mutex
	tx     *sql.Tx
	conn   *sql.Conn
	closed bool
}

func (s *Store) BeginAuditReadSnapshot(ctx context.Context) (*AuditReadSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open audit snapshot connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		_ = resetAuditQueryOnly(conn)
		_ = conn.Close()
		return nil, fmt.Errorf("set audit snapshot query-only: %w", err)
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		resetErr := resetAuditQueryOnly(conn)
		_ = conn.Close()
		if resetErr != nil {
			return nil, fmt.Errorf("begin audit read snapshot: %w; reset query-only: %v", err, resetErr)
		}
		return nil, fmt.Errorf("begin audit read snapshot: %w", err)
	}
	return &AuditReadSnapshot{tx: tx, conn: conn}, nil
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
	resetErr := resetAuditQueryOnly(s.conn)
	connErr := s.conn.Close()
	if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return rollbackErr
	}
	if resetErr != nil {
		return resetErr
	}
	return connErr
}

func resetAuditQueryOnly(conn *sql.Conn) error {
	_, err := conn.ExecContext(context.Background(), `PRAGMA query_only = OFF`)
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
